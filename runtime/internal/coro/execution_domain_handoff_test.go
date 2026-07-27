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

func TestExecutionDomainHandoffFixedPODLayout(t *testing.T) {
	if got, want := unsafe.Sizeof(ExecutionDomainHandoff{}), uintptr(8); got != want {
		t.Fatalf("ExecutionDomainHandoff size = %d, want %d", got, want)
	}
	if got, want := unsafe.Alignof(ExecutionDomainHandoff{}), uintptr(4); got != want {
		t.Fatalf("ExecutionDomainHandoff alignment = %d, want %d", got, want)
	}
	if got, want := unsafe.Sizeof(ExecutionDomainHandoffHandle{}), uintptr(8); got != want {
		t.Fatalf("ExecutionDomainHandoffHandle size = %d, want %d", got, want)
	}
}

func TestExecutionDomainHandoffClaimedReturnLifecycle(t *testing.T) {
	var handoff ExecutionDomainHandoff
	if !handoff.Idle() || handoff.Retired() {
		t.Fatal("zero handoff is not reusable idle")
	}
	handle, ok := handoff.Begin(17)
	if !ok || handle != (ExecutionDomainHandoffHandle{Generation: 1, OwnerEpoch: 17}) {
		t.Fatalf("begin = (%+v, %t)", handle, ok)
	}
	published, ok := handoff.Released()
	if !ok || published != handle {
		t.Fatalf("released = (%+v, %t), want %+v", published, ok, handle)
	}
	if !handoff.Claim(handle) || handoff.Claim(handle) {
		t.Fatal("claim was not exactly once")
	}
	if result := handoff.RequestReturn(handle); result != ExecutionDomainHandoffReturnClaimed {
		t.Fatalf("request return = %d, want claimed", result)
	}
	if !handoff.ReturnRequested(handle) || handoff.Returned(handle) {
		t.Fatal("claimed return request state invalid")
	}
	if !handoff.FinishReturn(handle) || handoff.FinishReturn(handle) ||
		!handoff.Returned(handle) {
		t.Fatal("finish return was not exactly once")
	}
	if !handoff.Complete(handle) || handoff.Complete(handle) || !handoff.Idle() {
		t.Fatal("complete did not restore reusable idle")
	}

	next, ok := handoff.Begin(29)
	if !ok || next.Generation != handle.Generation+1 || next.OwnerEpoch != 29 {
		t.Fatalf("next begin = (%+v, %t)", next, ok)
	}
	if handoff.Claim(handle) ||
		handoff.RequestReturn(handle) != ExecutionDomainHandoffReturnInvalid ||
		handoff.ReturnRequested(handle) || handoff.FinishReturn(handle) ||
		handoff.Returned(handle) || handoff.Complete(handle) {
		t.Fatal("stale generation mutated reused handoff")
	}
	if result := handoff.RequestReturn(next); result != ExecutionDomainHandoffReturnUnclaimed {
		t.Fatalf("withdraw next = %d, want unclaimed", result)
	}
	if !handoff.Returned(next) || !handoff.Complete(next) || !handoff.Idle() {
		t.Fatal("unclaimed next generation did not complete")
	}
}

func TestExecutionDomainHandoffClaimantRequestsReturn(t *testing.T) {
	var handoff ExecutionDomainHandoff
	handle, ok := handoff.Begin(31)
	if !ok {
		t.Fatal("begin claimant-requested return")
	}
	wrong := handle
	wrong.OwnerEpoch++
	if handoff.RequestClaimedReturn(handle) ||
		handoff.RequestClaimedReturn(wrong) {
		t.Fatal("unclaimed or wrong-epoch claimant requested return")
	}
	if !handoff.Claim(handle) ||
		!handoff.RequestClaimedReturn(handle) ||
		handoff.RequestClaimedReturn(handle) ||
		!handoff.ReturnRequested(handle) ||
		!handoff.FinishReturn(handle) ||
		!handoff.Returned(handle) ||
		!handoff.Complete(handle) ||
		!handoff.Idle() {
		t.Fatal("claimant-requested return lifecycle failed")
	}
	if handoff.RequestClaimedReturn(handle) {
		t.Fatal("stale claimant request mutated completed generation")
	}
}

func TestExecutionDomainHandoffRejectsWrongOwnerEpoch(t *testing.T) {
	var handoff ExecutionDomainHandoff
	handle, ok := handoff.Begin(41)
	if !ok {
		t.Fatal("begin")
	}
	wrong := handle
	wrong.OwnerEpoch++
	if handoff.Claim(wrong) ||
		handoff.RequestReturn(wrong) != ExecutionDomainHandoffReturnInvalid ||
		handoff.RequestClaimedReturn(wrong) ||
		handoff.ReturnRequested(wrong) || handoff.FinishReturn(wrong) ||
		handoff.Returned(wrong) || handoff.Complete(wrong) {
		t.Fatal("wrong owner epoch mutated handoff")
	}
	if result := handoff.RequestReturn(handle); result != ExecutionDomainHandoffReturnUnclaimed ||
		!handoff.Complete(handle) {
		t.Fatal("exact owner could not withdraw handoff")
	}
}

func TestExecutionDomainHandoffClaimReturnRace(t *testing.T) {
	const iterations = 2000
	var handoff ExecutionDomainHandoff
	for iteration := 0; iteration < iterations; iteration++ {
		handle, ok := handoff.Begin(uint32(iteration + 1))
		if !ok {
			t.Fatalf("iteration %d: begin", iteration)
		}
		start := make(chan struct{})
		claimedResult := make(chan bool, 1)
		returnResult := make(chan ExecutionDomainHandoffReturnResult, 1)
		var callers sync.WaitGroup
		callers.Add(2)
		go func() {
			defer callers.Done()
			<-start
			claimedResult <- handoff.Claim(handle)
		}()
		go func() {
			defer callers.Done()
			<-start
			returnResult <- handoff.RequestReturn(handle)
		}()
		close(start)
		callers.Wait()
		claimed := <-claimedResult
		returned := <-returnResult
		if claimed {
			if returned != ExecutionDomainHandoffReturnClaimed ||
				!handoff.ReturnRequested(handle) ||
				!handoff.FinishReturn(handle) {
				t.Fatalf(
					"iteration %d: claimed race = (return:%d requested:%t)",
					iteration,
					returned,
					handoff.ReturnRequested(handle),
				)
			}
		} else if returned != ExecutionDomainHandoffReturnUnclaimed {
			t.Fatalf("iteration %d: unclaimed race return = %d", iteration, returned)
		}
		if !handoff.Returned(handle) || !handoff.Complete(handle) || !handoff.Idle() {
			t.Fatalf("iteration %d: return did not quiesce", iteration)
		}
	}
}

func TestExecutionDomainHandoffRetireAndGenerationExhaustion(t *testing.T) {
	var retired ExecutionDomainHandoff
	if !retired.Retire() || !retired.Retired() || retired.Retire() {
		t.Fatal("idle retire was not exact and permanent")
	}
	if handle, ok := retired.Begin(1); ok || handle != (ExecutionDomainHandoffHandle{}) {
		t.Fatalf("retired begin = (%+v, %t)", handle, ok)
	}

	var exhausted ExecutionDomainHandoff
	exhausted.state = executionDomainHandoffPack(
		executionDomainHandoffIdle,
		executionDomainHandoffGenerationMask,
	)
	if handle, ok := exhausted.Begin(1); ok || handle != (ExecutionDomainHandoffHandle{}) ||
		!exhausted.Retired() {
		t.Fatalf("exhausted begin = (%+v, %t), retired=%t", handle, ok, exhausted.Retired())
	}
}
