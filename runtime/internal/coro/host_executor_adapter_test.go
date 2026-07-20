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

func TestHostActionV1IsWasmFriendlyPOD(t *testing.T) {
	if unsafe.Sizeof(HostActionV1{}) != 32 || unsafe.Alignof(HostActionV1{}) != 4 ||
		unsafe.Offsetof(HostActionV1{}.Epoch) != 12 || unsafe.Offsetof(HostActionV1{}.DeadlineHi) != 20 {
		t.Fatalf("HostActionV1 layout = size %d align %d epoch %d deadlineHi %d", unsafe.Sizeof(HostActionV1{}), unsafe.Alignof(HostActionV1{}), unsafe.Offsetof(HostActionV1{}.Epoch), unsafe.Offsetof(HostActionV1{}.DeadlineHi))
	}
}

func TestHostExecutorRunSchedulesLaterExactTurn(t *testing.T) {
	handle := ExecutorHandle{Slot: 2, Generation: 7}
	var adapter HostExecutorAdapter
	if !adapter.Start(handle, true) || !adapter.BeginRun(handle, 11) {
		t.Fatal("start/begin run rejected")
	}
	var action HostActionV1
	if !adapter.NextAction(&action) || action != (HostActionV1{
		Kind: uint32(HostActionScheduleV1), ExecutorSlot: 2, ExecutorGeneration: 7, Epoch: 11,
	}) {
		t.Fatalf("run action = %+v", action)
	}
	if adapter.NextAction(&action) || action != (HostActionV1{}) {
		t.Fatalf("duplicate action = %+v", action)
	}
	if claim, lease := adapter.ClaimCallback(handle, 10, HostCallbackScheduleV1); claim != HostCallbackClaimStale || lease {
		t.Fatalf("stale claim = %v, lease=%t", claim, lease)
	}
	claim, lease := adapter.ClaimCallback(handle, 11, HostCallbackScheduleV1)
	if claim != HostCallbackClaimed || !lease || adapter.CompleteRun(handle, 11) != HostAdapterCompletionComplete ||
		!adapter.FinishCallback(handle, 11, HostCallbackScheduleV1, lease, false) {
		t.Fatalf("run completion = claim %v lease=%t", claim, lease)
	}
}

func TestHostExecutorDeadlineWakeCancelsAlarmBeforeSchedule(t *testing.T) {
	handle := ExecutorHandle{Slot: 1, Generation: 3}
	var adapter HostExecutorAdapter
	if !adapter.Start(handle, true) || !adapter.BeginWait(handle, 19, 0x123456789, true) {
		t.Fatal("start/begin wait rejected")
	}
	var action HostActionV1
	var deadline HostActionV1
	if !adapter.NextDeadline(&deadline) || deadline.Epoch != 19 || deadline.DeadlineLo != 0x23456789 ||
		deadline.DeadlineHi != 1 || HostActionKindV1(deadline.Kind) != HostActionAlarmV1 {
		t.Fatalf("deadline snapshot = %+v", deadline)
	}
	if !adapter.NextAction(&action) || HostActionKindV1(action.Kind) != HostActionAlarmV1 ||
		action.DeadlineLo != 0x23456789 || action.DeadlineHi != 1 {
		t.Fatalf("alarm action = %+v", action)
	}
	if !adapter.RequestWake(handle) || !adapter.NextAction(&action) || HostActionKindV1(action.Kind) != HostActionCancelAlarmV1 ||
		adapter.NextAction(&HostActionV1{}) || !adapter.AcknowledgeCancel(handle, 19, HostActionCancelAlarmV1) {
		t.Fatalf("alarm cancellation = %+v", action)
	}
	if !adapter.NextAction(&action) || HostActionKindV1(action.Kind) != HostActionScheduleV1 {
		t.Fatalf("wake schedule = %+v", action)
	}
	claim, lease := adapter.ClaimCallback(handle, 19, HostCallbackScheduleV1)
	if claim != HostCallbackClaimed || !lease || adapter.CompleteWait(handle, 19) != HostAdapterCompletionComplete ||
		!adapter.FinishCallback(handle, 19, HostCallbackScheduleV1, lease, false) {
		t.Fatalf("wait completion = claim %v lease=%t", claim, lease)
	}
}

func TestHostExecutorCloseCancelsPendingAlarmAndStrongJoinsCallbackTail(t *testing.T) {
	handle := ExecutorHandle{Slot: 1, Generation: 5}
	var adapter HostExecutorAdapter
	if !adapter.Start(handle, true) || !adapter.BeginWait(handle, 23, 99, true) {
		t.Fatal("start/begin wait rejected")
	}
	var action HostActionV1
	if !adapter.NextAction(&action) || HostActionKindV1(action.Kind) != HostActionAlarmV1 {
		t.Fatalf("alarm action = %+v", action)
	}
	if got := adapter.BeginClose(handle, 29); got != HostAdapterCompletionPending {
		t.Fatalf("begin close = %v", got)
	}
	if !adapter.NextAction(&action) || HostActionKindV1(action.Kind) != HostActionCancelAlarmV1 || action.Epoch != 23 ||
		!adapter.AcknowledgeCancel(handle, 23, HostActionCancelAlarmV1) {
		t.Fatalf("cancel action = %+v", action)
	}
	if !adapter.NextAction(&action) || HostActionKindV1(action.Kind) != HostActionScheduleV1 || action.Epoch != 29 {
		t.Fatalf("close schedule = %+v", action)
	}
	claim, lease := adapter.ClaimCallback(handle, 29, HostCallbackScheduleV1)
	if claim != HostCallbackClaimed || lease || !adapter.RepostCloseCallback(handle, 29) {
		t.Fatalf("close repost = claim %v lease=%t", claim, lease)
	}
	claim, lease = adapter.ClaimCallback(handle, 29, HostCallbackScheduleV1)
	if claim != HostCallbackClaimed || lease || adapter.CompleteClose(handle, 29) != HostAdapterCompletionComplete || !adapter.CanRelease() {
		t.Fatalf("close completion = claim %v lease=%t release=%t", claim, lease, adapter.CanRelease())
	}
	if claim, _ := adapter.ClaimCallback(handle, 23, HostCallbackAlarmV1); claim != HostCallbackClaimStale {
		t.Fatalf("late alarm claim = %v", claim)
	}
}

func TestHostExecutorCloseWaitsForClaimTailWithoutBlocking(t *testing.T) {
	handle := ExecutorHandle{Slot: 1, Generation: 9}
	var adapter HostExecutorAdapter
	if !adapter.Start(handle, true) || !adapter.BeginRun(handle, 31) {
		t.Fatal("start/begin rejected")
	}
	var action HostActionV1
	if !adapter.NextAction(&action) {
		t.Fatal("missing run action")
	}
	claim, lease := adapter.ClaimCallback(handle, 31, HostCallbackScheduleV1)
	if claim != HostCallbackClaimed || !lease {
		t.Fatalf("claim = %v lease=%t", claim, lease)
	}
	// Seal can race an admitted callback after Claim but before its scheduler
	// continuation consumes the action. Delivered is no longer physically
	// cancelable; close waits for the callback tail instead of failing or
	// pretending it has joined.
	if got := adapter.BeginClose(handle, 37); got != HostAdapterCompletionPending {
		t.Fatalf("close with admitted tail = %v", got)
	}
	if adapter.NextAction(&action) {
		t.Fatalf("close scheduled before strong join: %+v", action)
	}
	if !adapter.FinishCallback(handle, 31, HostCallbackScheduleV1, lease, false) ||
		!adapter.NextAction(&action) || action.Epoch != 37 {
		t.Fatalf("close not scheduled after tail: %+v", action)
	}
}

func TestHostExecutorStartCannotReusePreopenedIngress(t *testing.T) {
	var adapter HostExecutorAdapter
	if !adapter.ingress.Start() {
		t.Fatal("test pre-open failed")
	}
	if adapter.Start(ExecutorHandle{Slot: 1, Generation: 1}, true) || adapter.CanRelease() {
		t.Fatal("partial/pre-opened ingress was accepted or reported releasable")
	}
}

func TestHostExecutorRejectsLegacyEntryModeWithoutPublishingState(t *testing.T) {
	handle := ExecutorHandle{Slot: 1, Generation: 1}
	var adapter HostExecutorAdapter
	if adapter.Start(handle, false) || !adapter.Start(handle, true) {
		t.Fatal("legacy entry was accepted or poisoned a later Slice-owned start")
	}
}

func TestHostMonotonicClockConcurrentSnapshotsDoNotTear(t *testing.T) {
	var clock HostMonotonicClock
	if _, ok := clock.Snapshot(); ok || !clock.Publish(1, 0) || clock.Publish(0, 0) {
		t.Fatal("clock initialization/regression contract failed")
	}
	const updates = 2000
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for value := uint32(2); value <= updates; value++ {
			if !clock.Publish(value, value) {
				t.Errorf("publish %d rejected", value)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for index := 0; index != updates; index++ {
			value, ok := clock.Snapshot()
			if !ok {
				continue
			}
			word := uint64(value)
			if uint32(word) != uint32(word>>32) && word != 1 {
				t.Errorf("torn clock value %#x", word)
				return
			}
		}
	}()
	wg.Wait()
}
