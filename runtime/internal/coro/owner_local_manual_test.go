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
	"testing"
	"unsafe"
)

func TestOwnerLocalManualCompletionDirectlyMaterializesSingleKeyedPark(t *testing.T) {
	p := new(P)
	driver, registry, manual, handle := bindTestExecutorDriverWithManual(t, p)
	waiter := newYieldingTestG(t, "manual-owner-local-direct-waiter")
	if !Enqueue(p, waiter.g) {
		t.Fatal("enqueue owner-local direct manual waiter")
	}
	if g, runnable := NextRunnable(p); !runnable || g != waiter.g {
		t.Fatalf("dequeue owner-local direct manual waiter = (%p,%t)", g, runnable)
	}
	waiterAction := beginWaitTestResume(t, p, waiter)
	var (
		wait      WaitSetRecord
		packet    ResumePacket
		plan      ResumeCleanupPlan
		operation OperationID
		token     byte
	)
	waiter.frame.header.SuspendReason = uint16(SuspendPark)
	waiter.frame.header.Lifecycle = uint16(FrameSuspended)
	ticket, id, prepared := PrepareSingleManualPark(
		waiter.g,
		waiter.handle,
		waiter.frame.header,
		manual,
		&wait,
		1,
		181,
	)
	operation = id
	if !prepared || !BindWaitSetResumeCleanup(
		&wait,
		&packet,
		&plan,
		ResumeCleanupBinding{
			Kind:         ResumeCleanupKeyedPark,
			Context:      unsafe.Pointer(&token),
			Entries:      unsafe.Pointer(&operation),
			Count:        1,
			RuntimeCount: 1,
			Stride:       unsafe.Sizeof(OperationID{}),
		},
	) {
		t.Fatal("prepare owner-local direct manual cleanup")
	}
	if action, parked := Resumed(p, waiter.g, waiterAction); !parked || action.Kind != ActionPark {
		t.Fatalf("commit owner-local direct manual park = (%+v,%t)", action, parked)
	}

	producer := newYieldingTestG(t, "manual-owner-local-direct-producer")
	if !Enqueue(p, producer.g) {
		t.Fatal("enqueue owner-local direct manual producer")
	}
	if g, runnable := NextRunnable(p); !runnable || g != producer.g {
		t.Fatalf("dequeue owner-local direct manual producer = (%p,%t)", g, runnable)
	}
	producerAction := beginWaitTestResume(t, p, producer)
	completion, cleanup, local, localOK := BeginOwnerLocalManualCompletionCurrent(
		producer.g,
		driver,
		id,
	)
	slot, slotOK := manualOperationSlotFor(manual, id)
	if !localOK || !local || !slotOK || cleanup.Kind != ResumeCleanupKeyedPark ||
		cleanup.Context != unsafe.Pointer(&token) || cleanup.Index != 0 ||
		cleanup.WinnerCase != 1 || cleanup.Outcome != ParkOutcomeCompleted ||
		manualOperationMailbox(preemptLoad(&slot.mailbox)) != manualOperationMailboxDelivered ||
		manual.Pending() || !emptyOwnerLocalCompletion(&driver.local) ||
		p.affectedWaitHead != nil || p.affectedWaitTail != nil ||
		registry.ObserveRequested(handle) || plan.phase != resumeCleanupRuntime ||
		wait.work != waitSetWorkResolving || waiter.g.park.phase != parkConsumed ||
		preemptWordState(loadGPreempt(producer.g)) != preemptRequested {
		t.Fatalf("begin owner-local direct manual completion = (%t,%t) cleanup=%+v slot=%t mailbox=%d pending=%t local=%+v affected=(%p,%p) request=%t plan=%+v wait=%+v park=%+v preempt=%#x",
			local, localOK, cleanup, slotOK,
			manualOperationMailbox(preemptLoad(&slot.mailbox)), manual.Pending(), driver.local,
			p.affectedWaitHead, p.affectedWaitTail, registry.ObserveRequested(handle),
			plan, wait, waiter.g.park, loadGPreempt(producer.g))
	}
	if !CommitResumeCleanupStep(cleanup, ResumeSmallInvalid) ||
		!FinishOwnerLocalManualCompletionCurrent(&completion) {
		t.Fatal("finish owner-local direct manual completion")
	}
	if completion != (OwnerLocalManualCompletion{}) || operation != (OperationID{}) ||
		plan != (ResumeCleanupPlan{}) || wait != (WaitSetRecord{}) ||
		packet.state != resumePacketMaterialized || packet.outcome != ParkOutcomeCompleted ||
		packet.caseID != 1 || packet.result != ResumeResultNone || packet.small != ResumeSmallInvalid ||
		!manualOperationSourceEmpty(manual, p) || registry.ObserveRequested(handle) ||
		p.readyHead != waiter.g || p.readyTail != waiter.g {
		t.Fatalf("owner-local direct manual final state: completion=%+v operation=%+v plan=%+v wait=%+v packet=%+v empty=%t request=%t ready=(%p,%p)",
			completion, operation, plan, wait, packet, manualOperationSourceEmpty(manual, p),
			registry.ObserveRequested(handle), p.readyHead, p.readyTail)
	}
	yieldRunningDriverTask(t, p, producer, producerAction)

	if g, runnable := NextRunnable(p); !runnable || g != waiter.g {
		t.Fatalf("dequeue owner-local direct manual waiter = (%p,%t)", g, runnable)
	}
	waiterAction = beginWaitTestResume(t, p, waiter)
	outcome, caseID, cancel, result, small, taken := TakeResumePacket(
		waiter.g,
		ticket,
		&packet,
		nil,
	)
	if !taken || outcome != ParkOutcomeCompleted || caseID != 1 ||
		cancel != TaskCancelNone || result != ResumeResultNone || small != ResumeSmallInvalid {
		t.Fatalf("take owner-local direct manual packet = (%d,%d,%d,%d,%d,%t)",
			outcome, caseID, cancel, result, small, taken)
	}
	finishWaitTestTask(t, p, waiter, waiterAction)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{producer.g: producer})
	if !manual.CanRelease() || !registry.CanRelease() {
		t.Fatal("owner-local direct manual cleanup retained stable state")
	}
	runtime.KeepAlive(waiter.frame.memory)
	runtime.KeepAlive(producer.frame.memory)
}
