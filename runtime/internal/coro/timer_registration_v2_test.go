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

import "testing"

type timerV2TestPark struct {
	task   *yieldingTestG
	ticket ParkTicket
	wait   *WaitSetRecord
	action Action
}

func beginTimerV2TestPark(t *testing.T, p *P, name string, expected, seed uint32) *timerV2TestPark {
	t.Helper()
	task := newYieldingTestG(t, name)
	if !Enqueue(p, task.g) {
		t.Fatalf("enqueue %s", name)
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatalf("dequeue %s", name)
	}
	action := beginWaitTestResume(t, p, task)
	ticket, ok := BeginParkSet(&task.g.park, expected, seed)
	wait := new(WaitSetRecord)
	if !ok || !PrepareWaitSetRecord(wait, task.g, ticket) {
		t.Fatalf("prepare %s park", name)
	}
	return &timerV2TestPark{task: task, ticket: ticket, wait: wait, action: action}
}

func rebeginTimerV2TestPark(t *testing.T, task *yieldingTestG, action Action, expected, seed uint32) *timerV2TestPark {
	t.Helper()
	ticket, ok := BeginParkSet(&task.g.park, expected, seed)
	wait := new(WaitSetRecord)
	if !ok || !PrepareWaitSetRecord(wait, task.g, ticket) {
		t.Fatal("prepare repeated timer V2 park")
	}
	return &timerV2TestPark{task: task, ticket: ticket, wait: wait, action: action}
}

func commitTimerV2TestPark(t *testing.T, p *P, park *timerV2TestPark) {
	t.Helper()
	if !SealParkSet(&park.task.g.park, park.ticket) {
		t.Fatal("seal timer V2 park")
	}
	park.task.frame.header.SuspendReason = uint16(SuspendPark)
	park.task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(park.task.g, park.task.handle, park.task.frame.header, park.ticket, park.wait) {
		t.Fatal("prepare scheduler timer V2 park")
	}
	action, ok := Resumed(p, park.task.g, park.action)
	if !ok || action.Kind != ActionPark || park.task.g.state != GWaiting || !park.task.g.waiting {
		t.Fatalf("commit timer V2 park = (%+v, %t)", action, ok)
	}
}

func resumeTimerV2TestPark(t *testing.T, p *P, park *timerV2TestPark) (Action, ParkOutcome, uint32, OperationResultLease, TaskCancelKind) {
	t.Helper()
	if g, ok := NextRunnable(p); !ok || g != park.task.g {
		t.Fatal("dequeue promoted timer V2 task")
	}
	action := beginWaitTestResume(t, p, park.task)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(park.task.g, park.ticket)
	if !ok {
		t.Fatal("take timer V2 run decision")
	}
	return action, outcome, caseID, lease, taskCancel
}

func bindTimerV2TestSources(t *testing.T, p *P, manual *ManualOperationSource) (*ExecutorSourceSet, *WaitRegistrationTable, *TimerRegistrationTable) {
	t.Helper()
	sources := new(ExecutorSourceSet)
	waits := new(WaitRegistrationTable)
	timers := new(TimerRegistrationTable)
	if !bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Waits: waits, Timers: timers, Manual: manual}) {
		t.Fatal("bind timer V2 source set")
	}
	return sources, waits, timers
}

func finishTimerV2Test(t *testing.T, p *P, sources *ExecutorSourceSet, waits *WaitRegistrationTable, timers *TimerRegistrationTable, park *timerV2TestPark, action Action) {
	t.Helper()
	finishWaitTestTask(t, p, park.task, action)
	if !unbindExecutorSourceSet(sources, p) || !waits.CanRelease() || !timers.CanRelease() {
		t.Fatal("release timer V2 source set")
	}
}

func TestPrepareOperationAtGenerationSkipsLegacyPhysicalGenerations(t *testing.T) {
	var record OperationRecord
	first, _ := MakeOperationID(OperationSourceTimer, 3, 4)
	if !PrepareOperationAtGeneration(&record, first) || record.phase != operationReserved || record.id != first ||
		!AbortReservedOperation(&record, first) {
		t.Fatal("prepare operation at first physical generation")
	}
	stale, _ := MakeOperationID(OperationSourceTimer, 3, 4)
	older, _ := MakeOperationID(OperationSourceTimer, 3, 2)
	wrongSlot, _ := MakeOperationID(OperationSourceTimer, 4, 7)
	wrongSource, _ := MakeOperationID(OperationSourceManual, 3, 7)
	if PrepareOperationAtGeneration(&record, stale) || PrepareOperationAtGeneration(&record, older) ||
		PrepareOperationAtGeneration(&record, wrongSlot) || PrepareOperationAtGeneration(&record, wrongSource) {
		t.Fatal("generation helper accepted stale or different physical identity")
	}
	next, _ := MakeOperationID(OperationSourceTimer, 3, 9)
	if !PrepareOperationAtGeneration(&record, next) || record.id != next || !AbortReservedOperation(&record, next) {
		t.Fatal("generation helper did not skip legacy generations")
	}
	corrupt := OperationRecord{id: next, phase: operationReusable, resultTaken: true}
	newer, _ := MakeOperationID(OperationSourceTimer, 3, 10)
	if PrepareOperationAtGeneration(&corrupt, newer) {
		t.Fatal("generation helper accepted terminal residue")
	}
}

func TestTimerRegistrationV2DueEpochDetachLeaseAndUnrelatedSlot(t *testing.T) {
	p := new(P)
	sources, waits, timers := bindTimerV2TestSources(t, p, nil)

	// Keep a legacy timer live in another slot. The resolved V2 ApplyOne must
	// neither reinterpret nor mutate this unrelated generation.
	unrelatedToken, unrelatedTicket, unrelated := prepareTestTimer(t, timers, p, 1000)
	if !claimWait(unrelatedToken, unrelatedTicket) {
		t.Fatal("claim unrelated legacy timer")
	}
	unrelatedSlot, _ := timerRegistrationSlotFor(timers, unrelated)
	unrelatedBefore := *unrelatedSlot

	park := beginTimerV2TestPark(t, p, "timer-v2-due", 1, 101)
	staleTicket := park.ticket
	staleTicket.generation++
	if failed, ok := timers.ReserveAndAttachTimerV2(p, &park.task.g.park, staleTicket, park.wait, 77, 0); ok ||
		failed != (TimerRegistrationHandle{}) {
		t.Fatal("timer V2 preparation accepted stale logical ticket")
	}
	failedSlot, failedGeneration := uint32(0), uint32(0)
	for index := range timers.slots {
		slot := &timers.slots[index]
		if slot.state == timerRegistrationFree && slot.generation != 0 && slot.record != (OperationRecord{}) {
			if failedSlot != 0 || !reusableTimerRegistrationSlot(slot, uint32(index)) ||
				slot.record.id.Generation != slot.generation {
				t.Fatal("failed timer V2 preparation left non-canonical residue")
			}
			failedSlot = uint32(index) + 1
			failedGeneration = slot.generation
		}
	}
	if failedSlot == 0 {
		t.Fatal("failed timer V2 preparation did not consume its physical generation")
	}
	// Deadline zero is already due while the coroutine is still preparing its
	// park. The first owner scan occurs only after park commit; the initial
	// affected visit plus sticky completion must preserve this early expiry.
	handle, attached := timers.ReserveAndAttachTimerV2(p, &park.task.g.park, park.ticket, park.wait, 77, 0)
	if !attached || handle == (TimerRegistrationHandle{}) || timers.Cancel(handle) != WaitCancelInvalid ||
		timers.Retire(handle) {
		t.Fatal("reserve or V1/V2 mode isolation for due timer")
	}
	if handle.Slot != failedSlot || handle.Generation != failedGeneration+1 {
		t.Fatal("timer V2 did not reuse the rolled-back slot with a newer shared generation")
	}
	commitTimerV2TestPark(t, p, park)

	scan, ok := sources.publishPass(p, 0, true)
	if !ok || scan.timers != 1 || scan.completed != 1 || !scan.hasDeadline || scan.deadline != 1000 {
		t.Fatalf("publish due timer V2 = (%+v, %t)", scan, ok)
	}
	dueSlot, _ := timerRegistrationSlotFor(timers, handle)
	if dueSlot.state != timerRegistrationDelivered || !dueSlot.record.completionPublished ||
		park.task.g.state != GWaiting || !park.task.g.waiting {
		t.Fatal("timer V2 publication resolved or promoted before the common epoch phase")
	}
	if promoted, visits, resolved := sources.resolvePublishedEpoch(p); !resolved || promoted != 1 || visits != 1 {
		t.Fatalf("resolve due timer V2 = (%d, %d, %t)", promoted, visits, resolved)
	}
	if *unrelatedSlot != unrelatedBefore {
		t.Fatal("resolved timer V2 batch mutated unrelated legacy slot")
	}
	if duplicate, _, _, duplicateOK := timers.drainDueFor(p, 0); !duplicateOK || duplicate != 0 {
		t.Fatalf("duplicate due drain = (%d, %t)", duplicate, duplicateOK)
	}

	action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
	leaseID, leaseOK := lease.ID()
	id, _ := timerRegistrationIDForHandle(handle)
	if outcome != ParkOutcomeCompleted || caseID != 77 || taskCancel != TaskCancelNone || !leaseOK || leaseID != id {
		t.Fatalf("due timer V2 decision = (%d, %d, %+v, %d)", outcome, caseID, lease, taskCancel)
	}
	stale := handle
	stale.Generation++
	if timers.RecycleTimerV2(p, handle) || timers.TakeTimerV2Result(p, stale, lease) ||
		!timers.TakeTimerV2Result(p, handle, lease) || timers.DiscardTimerV2Result(p, handle, lease) ||
		!timers.RecycleTimerV2(p, handle) {
		t.Fatal("timer V2 winner lease/recycle barrier")
	}

	if result := timers.Cancel(unrelated); result != WaitCancelWon {
		t.Fatalf("cancel unrelated legacy timer = %d", result)
	}
	if outcome, consumed := consumeWait(unrelatedToken, unrelatedTicket); !consumed || outcome != WaitOutcomeCanceled ||
		!timers.RetireCanceledTimer(unrelated, unrelatedToken, unrelatedTicket) {
		t.Fatal("retire unrelated legacy timer")
	}
	finishTimerV2Test(t, p, sources, waits, timers, park, action)
}

func TestTimerRegistrationV2FutureDeadlineOnlyPublishesWhenDue(t *testing.T) {
	p := new(P)
	sources, waits, timers := bindTimerV2TestSources(t, p, nil)
	park := beginTimerV2TestPark(t, p, "timer-v2-future", 1, 103)
	handle, attached := timers.ReserveAndAttachTimerV2(p, &park.task.g.park, park.ticket, park.wait, 88, 50)
	if !attached {
		t.Fatal("reserve future timer V2")
	}
	commitTimerV2TestPark(t, p, park)

	if scan, ok := sources.publishPass(p, 49, true); !ok || scan.timers != 0 || !scan.hasDeadline || scan.deadline != 50 {
		t.Fatalf("early future timer scan = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 0 || visits != 0 {
		t.Fatalf("early future timer resolve = (%d, %d, %t)", promoted, visits, ok)
	}
	if scan, ok := sources.publishPass(p, 50, true); !ok || scan.timers != 1 || scan.hasDeadline {
		t.Fatalf("due future timer scan = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("due future timer resolve = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, _, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
	if outcome != ParkOutcomeCompleted || taskCancel != TaskCancelNone ||
		!timers.DiscardTimerV2Result(p, handle, lease) || !timers.RecycleTimerV2(p, handle) {
		t.Fatal("consume future timer V2")
	}
	finishTimerV2Test(t, p, sources, waits, timers, park, action)
}

func TestTimerRegistrationV2CompletionAgainstCancellationClasses(t *testing.T) {
	tests := []struct {
		name       string
		taskCancel TaskCancelKind
		want       ParkOutcome
	}{
		{name: "operation", want: ParkOutcomeCompleted},
		{name: "task-abort", taskCancel: TaskCancelAbort, want: ParkOutcomeCanceled},
		{name: "shutdown", taskCancel: TaskCancelShutdown, want: ParkOutcomeCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := new(P)
			sources, waits, timers := bindTimerV2TestSources(t, p, nil)
			park := beginTimerV2TestPark(t, p, "timer-v2-cancel-"+test.name, 1, 107)
			handle, attached := timers.ReserveAndAttachTimerV2(p, &park.task.g.park, park.ticket, park.wait, 99, 0)
			if !attached {
				t.Fatal("reserve cancellation timer V2")
			}
			commitTimerV2TestPark(t, p, park)
			if test.taskCancel == TaskCancelNone {
				if !timers.RequestTimerV2Cancel(p, park.wait) {
					t.Fatal("request timer V2 operation cancellation")
				}
			} else if !RequestTaskCancellation(p, park.task.g, test.taskCancel) {
				t.Fatal("request timer V2 task cancellation")
			}
			if scan, ok := sources.publishPass(p, 0, true); !ok || scan.timers != 1 {
				t.Fatalf("publish cancellation race = (%+v, %t)", scan, ok)
			}
			if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
				t.Fatalf("resolve cancellation race = (%d, %d, %t)", promoted, visits, ok)
			}
			action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
			if outcome != test.want || taskCancel != test.taskCancel {
				t.Fatalf("cancellation decision = (%d, %d, %+v, %d)", outcome, caseID, lease, taskCancel)
			}
			if test.want == ParkOutcomeCompleted {
				if caseID != 99 || !lease.Valid() || !timers.TakeTimerV2Result(p, handle, lease) {
					t.Fatal("ordinary operation cancellation did not lose to same-epoch completion")
				}
			} else if caseID != 0 || lease != (OperationResultLease{}) {
				t.Fatal("task/shutdown cancellation retained suppressed timer result")
			}
			if !timers.RecycleTimerV2(p, handle) {
				t.Fatal("recycle cancellation timer V2")
			}
			finishTimerV2Test(t, p, sources, waits, timers, park, action)
			if test.taskCancel != TaskCancelNone && !AcknowledgeTaskCancellation(park.task.g, test.taskCancel) {
				t.Fatal("acknowledge terminal task cancellation")
			}
		})
	}
}

func TestTimerRegistrationV2LateTaskCancellationDiscardsWinnerLease(t *testing.T) {
	p := new(P)
	sources, waits, timers := bindTimerV2TestSources(t, p, nil)
	park := beginTimerV2TestPark(t, p, "timer-v2-late-task-cancel", 1, 108)
	handle, attached := timers.ReserveAndAttachTimerV2(p, &park.task.g.park, park.ticket, park.wait, 100, 0)
	if !attached {
		t.Fatal("reserve late-cancel timer V2")
	}
	commitTimerV2TestPark(t, p, park)
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.timers != 1 {
		t.Fatalf("publish late-cancel winner = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("resolve late-cancel winner = (%d, %d, %t)", promoted, visits, ok)
	}
	if !RequestTaskCancellation(p, park.task.g, TaskCancelAbort) {
		t.Fatal("request task cancellation after timer winner promotion")
	}
	action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
	if outcome != ParkOutcomeCanceled || caseID != 0 || !lease.Valid() || taskCancel != TaskCancelAbort ||
		timers.RecycleTimerV2(p, handle) || !timers.DiscardTimerV2Result(p, handle, lease) ||
		!timers.RecycleTimerV2(p, handle) {
		t.Fatalf("late-cancel timer decision/lease = (%d, %d, %+v, %d)", outcome, caseID, lease, taskCancel)
	}
	finishTimerV2Test(t, p, sources, waits, timers, park, action)
	if !AcknowledgeTaskCancellation(park.task.g, TaskCancelAbort) {
		t.Fatal("acknowledge late timer task cancellation")
	}
}

func TestTimerRegistrationV2MixedManualSelectIsIndependentOfSourceOrder(t *testing.T) {
	p := new(P)
	manual := new(ManualOperationSource)
	sources, waits, timers := bindTimerV2TestSources(t, p, manual)
	park := beginTimerV2TestPark(t, p, "timer-v2-mixed", 2, 109)

	// Choose case IDs so the manual source has the lower logical rank even
	// though the static publication catalog visits Timer before Manual.
	timerCase, manualCase := uint32(11), uint32(22)
	if parkCaseRank(park.task.g.park.seed, manualCase) > parkCaseRank(park.task.g.park.seed, timerCase) {
		timerCase, manualCase = manualCase, timerCase
	}
	timer, timerOK := timers.ReserveAndAttachTimerV2(p, &park.task.g.park, park.ticket, park.wait, timerCase, 0)
	manualID, manualOK := manual.ReserveAndAttachWait(p, &park.task.g.park, park.ticket, park.wait, manualCase)
	if !timerOK || !manualOK {
		t.Fatal("attach mixed timer/manual select")
	}
	commitTimerV2TestPark(t, p, park)
	if result := manual.Post(manualID); result != ManualOperationPosted {
		t.Fatalf("post mixed manual candidate = %d", result)
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.timers != 1 || scan.manual != 1 {
		t.Fatalf("publish mixed source epoch = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 2 {
		t.Fatalf("resolve mixed source epoch = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
	leaseID, leaseOK := lease.ID()
	if outcome != ParkOutcomeCompleted || caseID != manualCase || taskCancel != TaskCancelNone || !leaseOK || leaseID != manualID {
		t.Fatalf("mixed select decision = (%d, %d, %+v, %d), manual=%d", outcome, caseID, lease, taskCancel, manualCase)
	}
	if !timers.RecycleTimerV2(p, timer) || !manual.ConfirmQuiesced(p, manualID) ||
		!manual.TakeResult(p, lease) || !manual.Recycle(p, manualID) {
		t.Fatal("release mixed source operations")
	}
	finishWaitTestTask(t, p, park.task, action)
	if !unbindExecutorSourceSet(sources, p) || !waits.CanRelease() || !timers.CanRelease() || !manual.CanRelease() {
		t.Fatal("release mixed source set")
	}
}

func TestTimerRegistrationAlternatesV1AndV2OnOnePhysicalGeneration(t *testing.T) {
	p := new(P)
	sources, waits, timers := bindTimerV2TestSources(t, p, nil)

	v1Token, v1Ticket, v1 := prepareTestTimer(t, timers, p, 10)
	if !claimWait(v1Token, v1Ticket) {
		t.Fatal("claim first alternating V1 timer")
	}
	if count, _, _, ok := timers.drainDueFor(p, 10); !ok || count != 1 {
		t.Fatal("complete first alternating V1 timer")
	}
	if outcome, ok := consumeWait(v1Token, v1Ticket); !ok || outcome != WaitOutcomeCompleted ||
		!timers.RetireCompletedTimer(v1, v1Token, v1Ticket) {
		t.Fatal("retire first alternating V1 timer")
	}

	park := beginTimerV2TestPark(t, p, "timer-v2-alternating", 1, 113)
	v2, attached := timers.ReserveAndAttachTimerV2(p, &park.task.g.park, park.ticket, park.wait, 1, 100)
	if !attached || v2.Slot != v1.Slot || v2.Generation != v1.Generation+1 {
		t.Fatalf("first alternating V2 identity = %+v after %+v", v2, v1)
	}
	commitTimerV2TestPark(t, p, park)
	if !timers.RequestTimerV2Cancel(p, park.wait) {
		t.Fatal("cancel alternating V2 timer")
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.timers != 0 {
		t.Fatalf("publish alternating V2 cancellation = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("resolve alternating V2 cancellation = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, _, lease, _ := resumeTimerV2TestPark(t, p, park)
	if outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) || !timers.RecycleTimerV2(p, v2) {
		t.Fatal("recycle alternating V2 timer")
	}
	v1bToken, v1bTicket, v1b := prepareTestTimer(t, timers, p, 200)
	if v1b.Slot != v2.Slot || v1b.Generation != v2.Generation+1 || timers.RecycleTimerV2(p, v2) {
		t.Fatalf("second alternating V1 identity = %+v after %+v", v1b, v2)
	}
	if !claimWait(v1bToken, v1bTicket) || timers.Cancel(v1b) != WaitCancelWon {
		t.Fatal("cancel second alternating V1 timer")
	}
	if outcome, ok := consumeWait(v1bToken, v1bTicket); !ok || outcome != WaitOutcomeCanceled ||
		!timers.RetireCanceledTimer(v1b, v1bToken, v1bTicket) {
		t.Fatal("retire second alternating V1 timer")
	}

	park2 := rebeginTimerV2TestPark(t, park.task, action, 1, 127)
	v2b, attached := timers.ReserveAndAttachTimerV2(p, &park2.task.g.park, park2.ticket, park2.wait, 2, 300)
	if !attached || v2b.Slot != v1b.Slot || v2b.Generation != v1b.Generation+1 {
		t.Fatalf("second alternating V2 identity = %+v after %+v", v2b, v1b)
	}
	commitTimerV2TestPark(t, p, park2)
	if !timers.RequestTimerV2Cancel(p, park2.wait) {
		t.Fatal("cancel second alternating V2 timer")
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.timers != 0 {
		t.Fatalf("publish second alternating V2 cancellation = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("resolve second alternating V2 cancellation = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, _, lease, _ = resumeTimerV2TestPark(t, p, park2)
	if outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) || timers.RecycleTimerV2(p, v2) ||
		!timers.RecycleTimerV2(p, v2b) {
		t.Fatal("recycle second alternating V2 timer or reject stale first identity")
	}
	finishWaitTestTask(t, p, park2.task, action)

	if !unbindExecutorSourceSet(sources, p) || !waits.CanRelease() || !timers.CanRelease() {
		t.Fatal("release alternating timer source set")
	}
}
