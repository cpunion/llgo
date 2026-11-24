/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package coro

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
)

func TestExecutionQuotaLifecycleAndStickyWake(t *testing.T) {
	quota := new(ExecutionQuota)
	if got := unsafe.Sizeof(*quota); got != executionQuotaSize {
		t.Fatalf("execution quota size = %d, want %d", got, executionQuotaSize)
	}
	if !quota.CanRelease() || quota.Start(0) || !quota.Start(1) || quota.Start(1) {
		t.Fatal("execution quota start lifecycle invalid")
	}
	if limit, active, ok := quota.Usage(); !ok || limit != 1 || active != 0 {
		t.Fatalf("execution quota initial usage = (%d, %d, %t)", limit, active, ok)
	}
	if acquired, ok := quota.TryAcquire(1); !acquired || !ok {
		t.Fatal("route 1 did not acquire the only permit")
	}
	if held, ok := quota.Held(1); !ok || !held {
		t.Fatalf("route 1 held lease = (%t, %t)", held, ok)
	}
	if held, ok := quota.Held(2); !ok || held {
		t.Fatalf("idle route 2 held lease = (%t, %t)", held, ok)
	}
	if acquired, ok := quota.TryAcquire(2); acquired || !ok {
		t.Fatalf("contended route 2 acquire = (%t, %t)", acquired, ok)
	}
	if waiters, ok := quota.WaiterMask(); !ok || waiters != 1<<(2-1) {
		t.Fatalf("exact execution waiter mask = (%08b, %t)", waiters, ok)
	}
	if previous, waiters, ok := quota.SetLimit(4); !ok || previous != 1 || waiters != 1<<(2-1) {
		t.Fatalf("grow execution quota = (%d, %08b, %t)", previous, waiters, ok)
	}
	if acquired, ok := quota.TryAcquire(2); !acquired || !ok {
		t.Fatal("route 2 did not acquire after growth")
	}
	if waiters, ok := quota.WaiterMask(); !ok || waiters != 0 {
		t.Fatalf("acquired route retained waiter = (%08b, %t)", waiters, ok)
	}
	if previous, waiters, ok := quota.SetLimit(1); !ok || previous != 4 || waiters != 0 {
		t.Fatalf("shrink execution quota = (%d, %08b, %t)", previous, waiters, ok)
	}
	if acquired, ok := quota.TryAcquire(3); acquired || !ok {
		t.Fatalf("route 3 acquired above shrunken limit: (%t, %t)", acquired, ok)
	}
	if waiters, ok := quota.Release(2); !ok || waiters != 1<<(3-1) {
		t.Fatalf("route 2 release = (%08b, %t), want route 3 waiter", waiters, ok)
	}
	if acquired, ok := quota.TryAcquire(3); acquired || !ok {
		t.Fatalf("route 3 acquired while route 1 still held: (%t, %t)", acquired, ok)
	}
	if waiters, ok := quota.Release(1); !ok || waiters != 1<<(3-1) {
		t.Fatalf("route 1 release = (%08b, %t), want route 3 waiter", waiters, ok)
	}
	if acquired, ok := quota.TryAcquire(1); acquired || !ok {
		t.Fatalf("freshly released route bypassed published waiter: (%t, %t)", acquired, ok)
	}
	if held, ok := quota.Held(1); !ok || held {
		t.Fatalf("released route 1 held lease = (%t, %t)", held, ok)
	}
	if acquired, ok := quota.TryAcquire(3); !acquired || !ok {
		t.Fatal("route 3 did not acquire released permit")
	}
	if waiters, ok := quota.Release(3); !ok || waiters != 1<<(1-1) {
		t.Fatalf("route 3 release = (%08b, %t), want route 1 waiter", waiters, ok)
	}
	if acquired, ok := quota.TryAcquire(1); !acquired || !ok {
		t.Fatal("deferred route 1 did not acquire after waiter release")
	}
	if waiters, ok := quota.Release(1); !ok || waiters != 0 {
		t.Fatalf("route 1 final release = (%08b, %t), want no waiters", waiters, ok)
	}
	if wake, ok := quota.Seal(); !ok || wake {
		t.Fatalf("execution quota seal = (%t, %t)", wake, ok)
	}
	if acquired, ok := quota.TryAcquire(4); acquired || ok {
		t.Fatalf("sealed quota acquire = (%t, %t)", acquired, ok)
	}
	if !quota.Quiesced() || !quota.Retire() || !quota.CanRelease() ||
		quota.Retire() {
		t.Fatal("execution quota retirement lifecycle invalid")
	}
}

func TestExecutionQuotaRejectsRouteMisuse(t *testing.T) {
	quota := new(ExecutionQuota)
	if !quota.Start(2) {
		t.Fatal("start execution quota")
	}
	if acquired, ok := quota.TryAcquire(0); acquired || ok {
		t.Fatal("zero route acquired execution quota")
	}
	if acquired, ok := quota.TryAcquire(RouteID(ExecutorFleetCapacity + 1)); acquired || ok {
		t.Fatal("out-of-range route acquired execution quota")
	}
	if acquired, ok := quota.TryAcquire(1); !acquired || !ok {
		t.Fatal("route 1 acquire")
	}
	if acquired, ok := quota.TryAcquire(1); acquired || ok {
		t.Fatal("route 1 double acquire was not rejected")
	}
	if _, ok := quota.Release(2); ok {
		t.Fatal("unheld route release was not rejected")
	}
	if _, ok := quota.Release(1); !ok {
		t.Fatal("route 1 release")
	}
	if _, ok := quota.Release(1); ok {
		t.Fatal("route 1 double release was not rejected")
	}
	if _, ok := quota.Seal(); !ok || !quota.Retire() {
		t.Fatal("retire misuse fixture")
	}
}

func TestExecutionQuotaKeepsFullLogicalLimit(t *testing.T) {
	var quota ExecutionQuota
	const maximum = ^uint32(0)
	if !quota.Start(maximum) {
		t.Fatal("start with the maximum logical limit")
	}
	if acquired, ok := quota.TryAcquire(1); !acquired || !ok {
		t.Fatal("acquire below the maximum logical limit")
	}
	if limit, active, ok := quota.Usage(); limit != maximum || active != 1 || !ok {
		t.Fatalf("usage = (%d, %d, %t), want (%d, 1, true)", limit, active, ok, maximum)
	}
	if previous, waiters, ok := quota.SetLimit(1); previous != maximum || waiters != 0 || !ok {
		t.Fatalf("shrink = (%d, %08b, %t), want (%d, 0, true)", previous, waiters, ok, maximum)
	}
	if _, ok := quota.Release(1); !ok {
		t.Fatal("release the held route after shrinking")
	}
}

func TestExecutionQuotaReleaseAndResizeReturnWaiterMaskAcrossSeal(t *testing.T) {
	var quota ExecutionQuota
	if !quota.Start(1) {
		t.Fatal("start execution quota")
	}
	if acquired, ok := quota.TryAcquire(1); !acquired || !ok {
		t.Fatal("route 1 acquire")
	}
	if acquired, ok := quota.TryAcquire(2); acquired || !ok {
		t.Fatal("route 2 should register as waiter")
	}
	if acquired, ok := quota.TryAcquire(4); acquired || !ok {
		t.Fatal("route 4 should register as waiter")
	}
	const wantWaiters uint32 = 1<<(2-1) | 1<<(4-1)
	if previous, waiters, ok := quota.SetLimit(1); !ok || previous != 1 || waiters != 0 {
		t.Fatalf("no-op resize = (%d, %08b, %t), want (1, 0, true)", previous, waiters, ok)
	}
	if previous, waiters, ok := quota.SetLimit(2); !ok || previous != 1 || waiters != wantWaiters {
		t.Fatalf("growth resize = (%d, %08b, %t), want (1, %08b, true)", previous, waiters, ok, wantWaiters)
	}
	if waiters, ok := quota.Release(1); !ok || waiters != wantWaiters {
		t.Fatalf("active release = (%08b, %t), want (%08b, true)", waiters, ok, wantWaiters)
	}
	if acquired, ok := quota.TryAcquire(2); !acquired || !ok {
		t.Fatal("route 2 acquire before seal")
	}
	if wake, ok := quota.Seal(); !ok || !wake {
		t.Fatalf("seal with route 4 waiting = (%t, %t), want (true, true)", wake, ok)
	}
	if _, ok := quota.WaiterMask(); ok {
		t.Fatal("WaiterMask must reject sealed quota")
	}
	if waiters, ok := quota.Release(2); !ok || waiters != 0 {
		t.Fatalf("sealed release = (%08b, %t), want (0, true)", waiters, ok)
	}
	if !quota.Quiesced() || !quota.Retire() || !quota.CanRelease() {
		t.Fatal("retire after sealed release")
	}
}

func TestExecutionQuotaReleaseOrdersWaiterPublication(t *testing.T) {
	for iteration := 0; iteration < 128; iteration++ {
		var quota ExecutionQuota
		if !quota.Start(1) {
			t.Fatal("start one-permit quota")
		}
		if acquired, ok := quota.TryAcquire(1); !acquired || !ok {
			t.Fatal("hold the initial permit")
		}
		start := make(chan struct{})
		var joined sync.WaitGroup
		joined.Add(2)
		var acquired, acquireOK, releaseOK bool
		var waiters uint32
		go func() {
			defer joined.Done()
			<-start
			acquired, acquireOK = quota.TryAcquire(2)
		}()
		go func() {
			defer joined.Done()
			<-start
			waiters, releaseOK = quota.Release(1)
		}()
		close(start)
		joined.Wait()
		if !acquireOK || !releaseOK || !acquired && waiters != 1<<(2-1) {
			t.Fatalf("race lost permit or wake: acquired=%t/%t, waiters=%08b/%t", acquired, acquireOK, waiters, releaseOK)
		}
		if !acquired {
			if acquired, ok := quota.TryAcquire(2); !acquired || !ok {
				t.Fatal("awakened route could not claim the returned permit")
			}
		}
		if _, ok := quota.Release(2); !ok {
			t.Fatal("release the final route")
		}
	}
}

func TestExecutionQuotaConcurrentLimitNeverExceeded(t *testing.T) {
	quota := new(ExecutionQuota)
	if !quota.Start(3) {
		t.Fatal("start execution quota")
	}
	var active atomic.Int32
	var maximum atomic.Int32
	start := make(chan struct{})
	var workers sync.WaitGroup
	for route := RouteID(1); route <= RouteID(ExecutorFleetCapacity); route++ {
		route := route
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for iteration := 0; iteration < 2000; iteration++ {
				acquired, ok := quota.TryAcquire(route)
				if !ok {
					t.Errorf("route %d acquire invalid", route)
					return
				}
				if !acquired {
					continue
				}
				current := active.Add(1)
				for {
					old := maximum.Load()
					if current <= old || maximum.CompareAndSwap(old, current) {
						break
					}
				}
				if current > 3 {
					t.Errorf("active managed executions = %d, limit 3", current)
				}
				active.Add(-1)
				if _, ok := quota.Release(route); !ok {
					t.Errorf("route %d release invalid", route)
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	if maximum.Load() < 1 || maximum.Load() > 3 {
		t.Fatalf("observed maximum managed executions = %d, want 1..3", maximum.Load())
	}
	if _, active, ok := quota.Usage(); !ok || active != 0 {
		t.Fatalf("execution quota leaked %d active holders", active)
	}
	if _, ok := quota.Seal(); !ok || !quota.Quiesced() || !quota.Retire() {
		t.Fatal("retire concurrent execution quota")
	}
}

func TestExecutionQuotaConcurrentResizeMaintainsPhysicalBound(t *testing.T) {
	quota := new(ExecutionQuota)
	if !quota.Start(1) {
		t.Fatal("start execution quota")
	}
	var active atomic.Int32
	var maximum atomic.Int32
	start := make(chan struct{})
	expanded := make(chan struct{})
	overlap := make(chan struct{})
	var workers sync.WaitGroup
	for route := RouteID(1); route <= RouteID(ExecutorFleetCapacity); route++ {
		route := route
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			// Do not let bounded acquisition attempts race ahead of the
			// deliberate initial expansion. Otherwise every contender but
			// the limit-1 holder can exhaust its attempts before SetLimit
			// runs, leaving the resizer waiting forever for active >= 2.
			<-expanded
			for iteration := 0; iteration < 1000; iteration++ {
				acquired, ok := quota.TryAcquire(route)
				if !ok {
					t.Errorf("route %d resize acquire invalid", route)
					return
				}
				if !acquired {
					continue
				}
				current := active.Add(1)
				for {
					old := maximum.Load()
					if current <= old || maximum.CompareAndSwap(old, current) {
						break
					}
				}
				if current > int32(ExecutorFleetCapacity) {
					t.Errorf("active managed executions = %d, physical capacity %d", current, ExecutorFleetCapacity)
				}
				<-overlap
				active.Add(-1)
				if _, ok := quota.Release(route); !ok {
					t.Errorf("route %d resize release invalid", route)
					return
				}
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		if _, _, ok := quota.SetLimit(ExecutorFleetCapacity); !ok {
			t.Error("grow execution quota for overlap")
			close(expanded)
			close(overlap)
			return
		}
		close(expanded)
		for active.Load() < 2 {
			runtime.Gosched()
		}
		close(overlap)
		for iteration := 0; iteration < 4000; iteration++ {
			limit := uint32(iteration%ExecutorFleetCapacity + 1)
			if _, _, ok := quota.SetLimit(limit); !ok {
				t.Errorf("resize to %d failed", limit)
				return
			}
		}
	}()
	close(start)
	workers.Wait()
	if maximum.Load() < 2 || maximum.Load() > int32(ExecutorFleetCapacity) {
		t.Fatalf("resize maximum managed executions = %d", maximum.Load())
	}
	if _, ok := quota.Seal(); !ok || !quota.Quiesced() || !quota.Retire() {
		t.Fatal("retire resized execution quota")
	}
}

func TestExecutionQuotaSoloActive(t *testing.T) {
	quota := new(ExecutionQuota)
	if quota.SoloActive(1) {
		t.Fatal("unused quota reported SoloActive")
	}
	if !quota.Start(2) {
		t.Fatal("start execution quota")
	}
	if quota.SoloActive(1) {
		t.Fatal("idle quota reported SoloActive")
	}
	if acquired, ok := quota.TryAcquire(1); !acquired || !ok {
		t.Fatal("acquire route 1")
	}
	if !quota.SoloActive(1) || quota.SoloActive(2) {
		t.Fatal("route 1 alone should report SoloActive(1)=true, SoloActive(2)=false")
	}
	// Second active route disables SoloActive.
	if acquired, ok := quota.TryAcquire(2); !acquired || !ok {
		t.Fatal("acquire route 2")
	}
	if quota.SoloActive(1) || quota.SoloActive(2) {
		t.Fatal("two active routes must not report SoloActive")
	}
	if _, ok := quota.Release(2); !ok {
		t.Fatal("release route 2")
	}
	if !quota.SoloActive(1) {
		t.Fatal("route 1 alone after release should report SoloActive")
	}
	// Waiting route under limit=1 disables SoloActive.
	if _, _, ok := quota.SetLimit(1); !ok {
		t.Fatal("set limit 1")
	}
	if acquired, ok := quota.TryAcquire(2); acquired || !ok {
		t.Fatalf("route 2 acquire under limit=1 = (%t, %t), want (false, true)", acquired, ok)
	}
	if quota.SoloActive(1) {
		t.Fatal("route 1 with route 2 waiting must not report SoloActive")
	}
	if _, ok := quota.Release(1); !ok {
		t.Fatal("release route 1")
	}
	if acquired, ok := quota.TryAcquire(2); !acquired || !ok {
		t.Fatal("route 2 claim permit")
	}
	if !quota.SoloActive(2) {
		t.Fatal("route 2 alone should report SoloActive")
	}
	if _, ok := quota.Seal(); !ok {
		t.Fatal("seal quota")
	}
	if quota.SoloActive(2) {
		t.Fatal("sealed quota must not report SoloActive")
	}
	if _, ok := quota.Release(2); !ok || !quota.Quiesced() || !quota.Retire() {
		t.Fatal("retire quota")
	}
}
