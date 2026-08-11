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
	if got := unsafe.Sizeof(*quota); got != 5*unsafe.Sizeof(uint32(0)) {
		t.Fatalf("execution quota size = %d, want five atomic words", got)
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
	if previous, wake, ok := quota.SetLimit(4); !ok || previous != 1 || !wake {
		t.Fatalf("grow execution quota = (%d, %t, %t)", previous, wake, ok)
	}
	if acquired, ok := quota.TryAcquire(2); !acquired || !ok {
		t.Fatal("route 2 did not acquire after growth")
	}
	if waiters, ok := quota.WaiterMask(); !ok || waiters != 0 {
		t.Fatalf("acquired route retained waiter = (%08b, %t)", waiters, ok)
	}
	if previous, wake, ok := quota.SetLimit(1); !ok || previous != 4 || wake {
		t.Fatalf("shrink execution quota = (%d, %t, %t)", previous, wake, ok)
	}
	if acquired, ok := quota.TryAcquire(3); acquired || !ok {
		t.Fatalf("route 3 acquired above shrunken limit: (%t, %t)", acquired, ok)
	}
	if wake, ok := quota.Release(2); !ok || !wake {
		t.Fatalf("route 2 release = (%t, %t), want sticky wake", wake, ok)
	}
	if acquired, ok := quota.TryAcquire(3); acquired || !ok {
		t.Fatalf("route 3 acquired while route 1 still held: (%t, %t)", acquired, ok)
	}
	if wake, ok := quota.Release(1); !ok || !wake {
		t.Fatalf("route 1 release = (%t, %t), want sticky wake", wake, ok)
	}
	if held, ok := quota.Held(1); !ok || held {
		t.Fatalf("released route 1 held lease = (%t, %t)", held, ok)
	}
	if acquired, ok := quota.TryAcquire(3); !acquired || !ok {
		t.Fatal("route 3 did not acquire released permit")
	}
	if _, ok := quota.Release(3); !ok {
		t.Fatal("route 3 release failed")
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
