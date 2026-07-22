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
	"sync"
	"testing"
	"unsafe"
)

func registerTestExecutor(t *testing.T, registry *ExecutorRegistry) ExecutorHandle {
	t.Helper()
	handle, ok := registry.Register()
	if !ok || handle.Slot == 0 || handle.Generation == 0 {
		t.Fatalf("register executor = (%+v, %t)", handle, ok)
	}
	return handle
}

func retireTestExecutor(t *testing.T, registry *ExecutorRegistry, handle ExecutorHandle) {
	t.Helper()
	if !registry.BeginClose(handle) {
		t.Fatal("begin executor close")
	}
	if !registry.ConfirmQuiesced(handle) {
		t.Fatal("confirm executor quiescence")
	}
	if !registry.Retire(handle) {
		t.Fatal("retire executor")
	}
}

func TestExecutorRequestLifecycleAndReuse(t *testing.T) {
	registry := new(ExecutorRegistry)
	if !registry.CanRelease() {
		t.Fatal("zero executor registry is not releasable")
	}
	handle := registerTestExecutor(t, registry)
	if registry.CanRelease() {
		t.Fatal("live executor registry reported releasable")
	}
	if result := registry.Request(handle); result != ExecutorRequestPublished || ExecutorRequestNeedsDoorbell(result) {
		t.Fatalf("first request = %d", result)
	}
	if !registry.ObserveRequested(handle) {
		t.Fatal("running executor did not observe request")
	}
	if result := registry.Request(handle); result != ExecutorRequestCoalesced || ExecutorRequestNeedsDoorbell(result) {
		t.Fatalf("coalesced request = %d", result)
	}
	if cleared, ok := registry.Acknowledge(handle); !ok || !cleared {
		t.Fatalf("acknowledge request = (%t, %t)", cleared, ok)
	}
	if registry.ObserveRequested(handle) {
		t.Fatal("acknowledged request remained visible")
	}
	if cleared, ok := registry.Acknowledge(handle); !ok || cleared {
		t.Fatalf("empty acknowledge = (%t, %t)", cleared, ok)
	}
	retireTestExecutor(t, registry, handle)
	if !registry.CanRelease() {
		t.Fatal("retired executor registry retained ownership")
	}
	if result := registry.Request(handle); result != ExecutorRequestClosed {
		t.Fatalf("same-generation request after retire = %d", result)
	}

	next := registerTestExecutor(t, registry)
	if next.Slot != handle.Slot || next.Generation == handle.Generation {
		t.Fatalf("next generation = %+v, old = %+v", next, handle)
	}
	if result := registry.Request(handle); result != ExecutorRequestStale {
		t.Fatalf("old request against reused slot = %d", result)
	}
	retireTestExecutor(t, registry, next)
}

func TestExecutorRequestIdleHandshake(t *testing.T) {
	registry := new(ExecutorRegistry)
	handle := registerTestExecutor(t, registry)
	if !registry.ArmIdle(handle) || !registry.idleArmed(handle) {
		t.Fatal("arm executor idle")
	}
	if registry.ArmIdle(handle) {
		t.Fatal("double idle arm succeeded")
	}
	if cleared, ok := registry.Acknowledge(handle); ok || cleared {
		t.Fatalf("acknowledge while idle = (%t, %t)", cleared, ok)
	}
	if !registry.CommitSleep(handle) {
		t.Fatal("commit exact idle gate")
	}
	if result := registry.Request(handle); result != ExecutorRequestIdleWake || !ExecutorRequestNeedsDoorbell(result) {
		t.Fatalf("idle request = %d", result)
	}
	if registry.CommitSleep(handle) {
		t.Fatal("committed sleep over a concurrent request")
	}
	if !registry.ObserveRequested(handle) || !registry.idleArmed(handle) {
		t.Fatal("idle request did not preserve both gate bits")
	}
	if result := registry.Request(handle); result != ExecutorRequestCoalesced || ExecutorRequestNeedsDoorbell(result) {
		t.Fatalf("coalesced idle request = %d", result)
	}
	if cleared, ok := registry.Acknowledge(handle); ok || cleared {
		t.Fatalf("acknowledge before leaving idle = (%t, %t)", cleared, ok)
	}
	if left, ok := registry.LeaveIdle(handle); !ok || !left {
		t.Fatalf("leave requested idle = (%t, %t)", left, ok)
	}
	if registry.idleArmed(handle) || !registry.ObserveRequested(handle) {
		t.Fatal("leave idle lost request or retained idle bit")
	}
	if cleared, ok := registry.Acknowledge(handle); !ok || !cleared {
		t.Fatalf("acknowledge after wake = (%t, %t)", cleared, ok)
	}

	if result := registry.Request(handle); result != ExecutorRequestPublished {
		t.Fatalf("running request = %d", result)
	}
	if registry.ArmIdle(handle) {
		t.Fatal("armed idle over a published request")
	}
	if cleared, ok := registry.Acknowledge(handle); !ok || !cleared {
		t.Fatal("clear running request")
	}
	retireTestExecutor(t, registry, handle)
}

func TestExecutorRetainedDoorbellClosesWakeBeforeBlock(t *testing.T) {
	registry := new(ExecutorRegistry)
	handle := registerTestExecutor(t, registry)
	// A capacity-one channel models a level-triggered/retained target
	// doorbell. The producer wins after CommitSleep but before the scheduler's
	// physical wait begins.
	doorbell := make(chan struct{}, 1)
	if !registry.ArmIdle(handle) || !registry.CommitSleep(handle) {
		t.Fatal("prepare retained-doorbell wait")
	}
	result := registry.Request(handle)
	if result != ExecutorRequestIdleWake || !ExecutorRequestNeedsDoorbell(result) {
		t.Fatalf("request between commit and block = %d", result)
	}
	select {
	case doorbell <- struct{}{}:
	default:
	}
	select {
	case <-doorbell:
	default:
		t.Fatal("wake delivered before block was not retained")
	}
	if left, ok := registry.LeaveIdle(handle); !ok || !left {
		t.Fatal("leave retained-doorbell idle")
	}
	if cleared, ok := registry.Acknowledge(handle); !ok || !cleared {
		t.Fatal("acknowledge retained-doorbell request")
	}
	retireTestExecutor(t, registry, handle)
}

func TestExecutorRequestConcurrentPublishCoalesces(t *testing.T) {
	const workers = 32
	registry := new(ExecutorRegistry)
	handle := registerTestExecutor(t, registry)
	start := make(chan struct{})
	results := make(chan ExecutorRequestResult, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wg.Done()
			<-start
			results <- registry.Request(handle)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	published, coalesced := 0, 0
	for result := range results {
		switch result {
		case ExecutorRequestPublished:
			published++
		case ExecutorRequestCoalesced:
			coalesced++
		default:
			t.Fatalf("concurrent request = %d", result)
		}
	}
	if published != 1 || coalesced != workers-1 {
		t.Fatalf("published=%d coalesced=%d", published, coalesced)
	}
	if cleared, ok := registry.Acknowledge(handle); !ok || !cleared {
		t.Fatal("acknowledge concurrent request")
	}
	retireTestExecutor(t, registry, handle)
}

func TestExecutorRequestIdleArmRace(t *testing.T) {
	const iterations = 500
	for iteration := 0; iteration < iterations; iteration++ {
		registry := new(ExecutorRegistry)
		handle := registerTestExecutor(t, registry)
		start := make(chan struct{})
		armed := make(chan bool, 1)
		requested := make(chan ExecutorRequestResult, 1)
		go func() {
			<-start
			armed <- registry.ArmIdle(handle)
		}()
		go func() {
			<-start
			requested <- registry.Request(handle)
		}()
		close(start)
		armResult, requestResult := <-armed, <-requested
		switch {
		case armResult && requestResult == ExecutorRequestIdleWake:
			if left, ok := registry.LeaveIdle(handle); !ok || !left {
				t.Fatal("leave raced idle")
			}
		case !armResult && requestResult == ExecutorRequestPublished:
		default:
			t.Fatalf("arm/request race = (%t, %d)", armResult, requestResult)
		}
		if cleared, ok := registry.Acknowledge(handle); !ok || !cleared {
			t.Fatal("acknowledge raced request")
		}
		retireTestExecutor(t, registry, handle)
	}
}

func TestExecutorRequestCloseRace(t *testing.T) {
	const iterations = 500
	for iteration := 0; iteration < iterations; iteration++ {
		registry := new(ExecutorRegistry)
		handle := registerTestExecutor(t, registry)
		start := make(chan struct{})
		closed := make(chan bool, 1)
		requested := make(chan ExecutorRequestResult, 1)
		go func() {
			<-start
			closed <- registry.BeginClose(handle)
		}()
		go func() {
			<-start
			requested <- registry.Request(handle)
		}()
		close(start)
		closeResult, requestResult := <-closed, <-requested
		switch {
		case closeResult && requestResult == ExecutorRequestClosed:
		case !closeResult && requestResult == ExecutorRequestPublished:
			if cleared, ok := registry.Acknowledge(handle); !ok || !cleared {
				t.Fatal("acknowledge close-race winner")
			}
			if !registry.BeginClose(handle) {
				t.Fatal("retry close after request drain")
			}
		default:
			t.Fatalf("close/request race = (%t, %d)", closeResult, requestResult)
		}
		if !registry.ConfirmQuiesced(handle) || !registry.Retire(handle) {
			t.Fatal("quiesce and retire close race")
		}
	}
}

func TestExecutorRequestAdmittedProducerPinsQuiescence(t *testing.T) {
	registry := new(ExecutorRegistry)
	handle := registerTestExecutor(t, registry)
	slot, _ := executorSlot(registry, handle)
	if !executorAcquireProducer(slot) {
		t.Fatal("admit model producer")
	}
	if !registry.BeginClose(handle) {
		t.Fatal("close with admitted producer")
	}
	if registry.ConfirmQuiesced(handle) || registry.Retire(handle) {
		t.Fatal("admitted producer did not pin generation")
	}
	if result := registry.Request(handle); result != ExecutorRequestClosed {
		t.Fatalf("new request after producer seal = %d", result)
	}
	executorReleaseProducer(slot)
	if !registry.ConfirmQuiesced(handle) || !registry.Retire(handle) {
		t.Fatal("retire generation after producer release")
	}
}

func TestExecutorClosedGateFailsClosed(t *testing.T) {
	registry := new(ExecutorRegistry)
	handle := registerTestExecutor(t, registry)
	if !registry.BeginClose(handle) {
		t.Fatal("begin closed-gate test")
	}
	if cleared, ok := registry.Acknowledge(handle); ok || cleared {
		t.Fatalf("acknowledge closed gate = (%t, %t)", cleared, ok)
	}
	if left, ok := registry.LeaveIdle(handle); ok || left {
		t.Fatalf("leave closed gate = (%t, %t)", left, ok)
	}
	if registry.ArmIdle(handle) || registry.CommitSleep(handle) || registry.ObserveRequested(handle) || registry.idleArmed(handle) {
		t.Fatal("closed gate accepted an active-state operation")
	}
	if result := registry.Request(handle); result != ExecutorRequestClosed {
		t.Fatalf("request closed gate = %d", result)
	}
	if !registry.ConfirmQuiesced(handle) || !registry.Retire(handle) {
		t.Fatal("quiesce and retire closed gate")
	}
}

func TestExecutorRequestCapacityAndStaleGeneration(t *testing.T) {
	registry := new(ExecutorRegistry)
	handles := make([]ExecutorHandle, ExecutorRequestCapacity)
	for index := range handles {
		handles[index] = registerTestExecutor(t, registry)
	}
	if handle, ok := registry.Register(); ok || handle != (ExecutorHandle{}) {
		t.Fatalf("register beyond capacity = (%+v, %t)", handle, ok)
	}
	for _, handle := range handles {
		retireTestExecutor(t, registry, handle)
	}
	next := registerTestExecutor(t, registry)
	if next.Slot != handles[0].Slot || next.Generation == handles[0].Generation {
		t.Fatalf("reused executor = %+v, old = %+v", next, handles[0])
	}
	if result := registry.Request(handles[0]); result != ExecutorRequestStale {
		t.Fatalf("stale request = %d", result)
	}
	retireTestExecutor(t, registry, next)
}

func TestExecutorRequestAtomicLayout(t *testing.T) {
	if unsafe.Sizeof(ExecutorHandle{}) != 8 || unsafe.Alignof(ExecutorHandle{}) != 4 {
		t.Fatalf("executor handle layout = size %d align %d", unsafe.Sizeof(ExecutorHandle{}), unsafe.Alignof(ExecutorHandle{}))
	}
	if unsafe.Offsetof(ExecutorRegistry{}.slots)%4 != 0 ||
		unsafe.Offsetof(executorRequestSlot{}.state)%4 != 0 ||
		unsafe.Offsetof(executorRequestSlot{}.generation)%4 != 0 ||
		unsafe.Offsetof(executorRequestSlot{}.inflight)%4 != 0 ||
		unsafe.Offsetof(executorRequestSlot{}.gate)%4 != 0 {
		t.Fatal("executor registry atomic prefix is not uint32 aligned")
	}
}
