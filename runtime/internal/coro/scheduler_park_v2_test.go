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

const (
	wantParkStateSize     = 40 + 2*unsafe.Sizeof(uintptr(0))
	wantRunDecisionSize   = 32 + unsafe.Sizeof(uintptr(0))
	wantWaitSetRecordSize = 8 + 5*unsafe.Sizeof(uintptr(0))
)

// Keep the always-live G park cell and the transient per-P resume decision
// pointer-size neutral. Both array pairs fail at compile time if either side
// of an exact layout equality becomes negative on 32- or 64-bit targets.
var (
	_ [wantParkStateSize - unsafe.Sizeof(ParkState{})]byte
	_ [unsafe.Sizeof(ParkState{}) - wantParkStateSize]byte
	_ [wantRunDecisionSize - unsafe.Sizeof(RunDecision{})]byte
	_ [unsafe.Sizeof(RunDecision{}) - wantRunDecisionSize]byte
	_ [wantWaitSetRecordSize - unsafe.Sizeof(WaitSetRecord{})]byte
	_ [unsafe.Sizeof(WaitSetRecord{}) - wantWaitSetRecordSize]byte
)

func TestRunDecisionBindsLeaseToExactTicketAndSuppressesCanceledCase(t *testing.T) {
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize run-decision validation G")
	}
	id, idOK := MakeOperationID(OperationSourceManual, 1, 1)
	ticket := ParkTicket{generation: 1}
	other := ParkTicket{generation: 2}
	if !idOK {
		t.Fatal("initialize run-decision validation operation")
	}
	if validRunDecision(RunDecision{
		g:       g,
		ticket:  ticket,
		caseID:  7,
		outcome: ParkOutcomeCompleted,
		lease:   OperationResultLease{id: id, ticket: other},
	}) {
		t.Fatal("accepted winner lease from another logical park")
	}
	if validRunDecision(RunDecision{
		g:       g,
		ticket:  ticket,
		caseID:  7,
		outcome: ParkOutcomeCanceled,
		task:    TaskCancelAbort,
		lease:   OperationResultLease{id: id, ticket: ticket},
	}) {
		t.Fatal("accepted selected case after prompt cancellation")
	}
	if !validRunDecision(RunDecision{
		g:       g,
		ticket:  ticket,
		outcome: ParkOutcomeCanceled,
		task:    TaskCancelAbort,
		lease:   OperationResultLease{id: id, ticket: ticket},
	}) {
		t.Fatal("rejected exact late-cancellation winner lease")
	}
}

type schedulerParkV2Operations struct {
	ticket  ParkTicket
	wait    WaitSetRecord
	records []OperationRecord
	ids     []OperationID
	cases   []uint32
}

func sealSchedulerParkV2(
	t *testing.T,
	g *G,
	seed uint32,
	cases ...uint32,
) *schedulerParkV2Operations {
	t.Helper()
	operations := &schedulerParkV2Operations{
		records: make([]OperationRecord, len(cases)),
		ids:     make([]OperationID, len(cases)),
		cases:   append([]uint32(nil), cases...),
	}
	ticket, ok := BeginParkSet(&g.park, uint32(len(cases)), seed)
	if !ok {
		t.Fatal("begin scheduler park-set")
	}
	operations.ticket = ticket
	if !PrepareWaitSetRecord(&operations.wait, g, ticket) {
		t.Fatal("prepare scheduler wait-set record")
	}
	for index, caseID := range cases {
		id, idOK := MakeOperationID(OperationSourceManual, uint32(index+1), 1)
		if !idOK || !InitOperation(&operations.records[index], id) ||
			!AttachParkWaitOperation(&g.park, ticket, &operations.wait, &operations.records[index], caseID) {
			t.Fatalf("attach scheduler park candidate %d", index)
		}
		operations.ids[index] = id
	}
	if !SealParkSet(&g.park, ticket) {
		t.Fatal("seal scheduler park-set")
	}
	return operations
}

func publishSchedulerParkV2(t *testing.T, p *P, operations *schedulerParkV2Operations, index int) {
	t.Helper()
	if result := PublishOperationCompletion(&operations.records[index], operations.ids[index]); result != OperationCompletionPublished {
		t.Fatalf("publish scheduler park candidate %d = %d", index, result)
	}
	if operations.wait.state == waitSetRecordActive && !MarkWaitSetAffected(p, &operations.wait) {
		t.Fatalf("mark scheduler park candidate %d affected", index)
	}
}

func detachSchedulerParkV2(t *testing.T, g *G, operations *schedulerParkV2Operations, index int) {
	t.Helper()
	disposition, ok := OperationDispositionOf(&operations.records[index], operations.ids[index])
	if !ok || !AcknowledgeOperationResolution(&operations.records[index], operations.ids[index], disposition) {
		t.Fatalf("acknowledge scheduler park candidate %d", index)
	}
	if !DetachParkWaitOperation(&g.park, operations.ticket, &operations.records[index], operations.ids[index]) {
		t.Fatalf("detach scheduler park candidate %d", index)
	}
}

func finishSchedulerParkV2Operations(
	t *testing.T,
	operations *schedulerParkV2Operations,
	winnerLease OperationResultLease,
) {
	t.Helper()
	winnerID, hasWinner := winnerLease.ID()
	for index := range operations.records {
		record := &operations.records[index]
		id := operations.ids[index]
		if !ConfirmOperationQuiesced(record, id) {
			t.Fatalf("quiesce scheduler park candidate %d", index)
		}
		if hasWinner && id == winnerID {
			if OperationCanRecycle(record, id) || !TakeOperationResult(record, winnerLease) {
				t.Fatalf("release scheduler park winner %d", index)
			}
		}
		if !OperationCanRecycle(record, id) || !RecycleOperation(record, id) {
			t.Fatalf("recycle scheduler park candidate %d", index)
		}
	}
}

func commitSchedulerParkV2(
	t *testing.T,
	p *P,
	task *yieldingTestG,
	action Action,
	operations *schedulerParkV2Operations,
) {
	t.Helper()
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(task.g, task.handle, task.frame.header, operations.ticket, &operations.wait) {
		t.Fatal("prepare scheduler park-set")
	}
	action, ok := Resumed(p, task.g, action)
	if !ok || action.Kind != ActionPark || action.Handle != nil || task.g.state != GWaiting || !task.g.waiting || !HasWaiting(p) {
		t.Fatalf("commit scheduler park-set = (%+v, %t), state=%d waiting=%t", action, ok, task.g.state, task.g.waiting)
	}
}

func TestSchedulerParkSetEarlyCompletionDetachGateAndRunDecision(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "park-v2-early")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue early-completion task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue early-completion task")
	}
	action := beginWaitTestResume(t, p, task)
	operations := sealSchedulerParkV2(t, task.g, 17, 41, 42)

	// The source may publish before the coroutine has returned to the
	// scheduler. PrepareParkSet must preserve this sticky completion.
	publishSchedulerParkV2(t, p, operations, 0)
	commitSchedulerParkV2(t, p, task, action, operations)

	// PollReady owns logical resolution, but the G must remain waiting until
	// every source has acknowledged its decision and detached its ParkLink.
	if count, ok := PollReady(p); !ok || count != 0 || task.g.park.phase != parkDetaching || !HasWaiting(p) {
		t.Fatalf("poll early completion = (%d, %t), phase=%d waiting=%t", count, ok, task.g.park.phase, HasWaiting(p))
	}
	if disposition, ok := OperationDispositionOf(&operations.records[0], operations.ids[0]); !ok || disposition != OperationDispositionWinner {
		t.Fatalf("early winner disposition = (%d, %t)", disposition, ok)
	}
	if disposition, ok := OperationDispositionOf(&operations.records[1], operations.ids[1]); !ok || disposition != OperationDispositionLost {
		t.Fatalf("early loser disposition = (%d, %t)", disposition, ok)
	}
	detachSchedulerParkV2(t, task.g, operations, 0)
	if count, ok := PollReady(p); !ok || count != 0 || !HasWaiting(p) || ParkReady(&task.g.park, operations.ticket) {
		t.Fatalf("poll partial detach = (%d, %t), waiting=%t ready=%t", count, ok, HasWaiting(p), ParkReady(&task.g.park, operations.ticket))
	}
	detachSchedulerParkV2(t, task.g, operations, 1)
	if !ParkReady(&task.g.park, operations.ticket) {
		t.Fatal("final source detach did not publish ParkReady")
	}
	if count, ok := PollReady(p); !ok || count != 1 || HasWaiting(p) || !task.g.queued || task.g.park.phase != parkReady {
		t.Fatalf("promote ready park-set = (%d, %t), waiting=%t queued=%t phase=%d", count, ok, HasWaiting(p), task.g.queued, task.g.park.phase)
	}

	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue promoted park-set task")
	}
	action = beginWaitTestResume(t, p, task)
	wrongTicket := operations.ticket
	wrongTicket.generation++
	if outcome, caseID, lease, taskCancel, ok := TakeRunDecision(task.g, wrongTicket); ok ||
		outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) || taskCancel != TaskCancelNone ||
		p.runDecision == (RunDecision{}) || p.runDecisionTaken {
		t.Fatalf("stale decision take = (%d, %d, %+v, %d, %t), retained=%t taken=%t", outcome, caseID, lease, taskCancel, ok, p.runDecision != (RunDecision{}), p.runDecisionTaken)
	}
	outcome, caseID, winnerLease, taskCancel, ok := TakeRunDecision(task.g, operations.ticket)
	if !ok || outcome != ParkOutcomeCompleted || caseID != operations.cases[0] || !winnerLease.Valid() || taskCancel != TaskCancelNone ||
		p.runDecision != (RunDecision{}) || !p.runDecisionTaken || task.g.park.phase != parkDelivered {
		t.Fatalf("take ready decision = (%d, %d, %+v, %d, %t), retained=%t taken=%t phase=%d", outcome, caseID, winnerLease, taskCancel, ok, p.runDecision != (RunDecision{}), p.runDecisionTaken, task.g.park.phase)
	}
	if outcome, caseID, lease, taskCancel, ok := TakeRunDecision(task.g, operations.ticket); ok ||
		outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) || taskCancel != TaskCancelNone {
		t.Fatalf("duplicate decision take = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, ok)
	}

	finishSchedulerParkV2Operations(t, operations, winnerLease)
	finishWaitTestTask(t, p, task, action)
	if !TerminalG(p, task.g) {
		t.Fatal("early-completion scheduler park retained state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestSchedulerRecordAwareWaitSetHighCardinalityUsesLocalDetach(t *testing.T) {
	const candidateCount = 1024

	p := new(P)
	task := newYieldingTestG(t, "park-v2-high-cardinality")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue high-cardinality task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue high-cardinality task")
	}
	action := beginWaitTestResume(t, p, task)
	cases := make([]uint32, candidateCount)
	for index := range cases {
		cases[index] = uint32(index + 1)
	}
	operations := sealSchedulerParkV2(t, task.g, 19, cases...)
	commitSchedulerParkV2(t, p, task, action, operations)
	if task.g.park.attached != candidateCount {
		t.Fatalf("attached candidates = %d, want %d", task.g.park.attached, candidateCount)
	}

	// Activation contributes one initial affected visit to catch preparation-
	// time completions. With no sticky fact it must cost one candidate scan and
	// leave the active wait-set off the affected FIFO.
	if count, ok := PollReady(p); !ok || count != 0 || task.g.park.phase != parkParked ||
		p.affectedWaitHead != nil || p.affectedWaitTail != nil {
		t.Fatalf("drain high-cardinality initial visit = (%d, %t), phase=%d", count, ok, task.g.park.phase)
	}
	publishSchedulerParkV2(t, p, operations, 0)
	if count, ok := PollReady(p); !ok || count != 0 || task.g.park.phase != parkDetaching {
		t.Fatalf("resolve high-cardinality wait = (%d, %t), phase=%d", count, ok, task.g.park.phase)
	}

	// The first record attached is now the distant tail. Corrupting its ticket
	// makes a complete ParkState audit fail. Detaching the current head must
	// nevertheless succeed: the production record-aware path inspects only the
	// ParkState header and the target's two neighboring links.
	distant := &operations.records[0]
	savedTicket := distant.link.ticket
	distant.link.ticket = ParkTicket{}
	if validParkState(&task.g.park) {
		t.Fatal("distant candidate corruption escaped complete audit")
	}
	detached := make([]bool, candidateCount)
	detachSchedulerParkV2(t, task.g, operations, candidateCount-1)
	detached[candidateCount-1] = true
	distant.link.ticket = savedTicket
	if !validParkState(&task.g.park) {
		t.Fatal("restored high-cardinality wait-set failed complete audit")
	}

	// Exercise the tail and a middle unlink before draining all remaining
	// records. Every successful call removes exactly one physical candidate.
	for _, index := range []int{0, candidateCount / 2} {
		detachSchedulerParkV2(t, task.g, operations, index)
		detached[index] = true
	}
	detachedCount := 3
	for index := range operations.records {
		if detached[index] {
			continue
		}
		detachSchedulerParkV2(t, task.g, operations, index)
		detachedCount++
	}
	if detachedCount != candidateCount || task.g.park.attached != 0 || !ParkReady(&task.g.park, operations.ticket) {
		t.Fatalf("high-cardinality detach = %d/%d, attached=%d ready=%t",
			detachedCount, candidateCount, task.g.park.attached, ParkReady(&task.g.park, operations.ticket))
	}
	if count, ok := PollReady(p); !ok || count != 1 || HasWaiting(p) {
		t.Fatalf("promote high-cardinality wait = (%d, %t), waiting=%t", count, ok, HasWaiting(p))
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue high-cardinality promoted task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, winnerLease, taskCancel, ok := TakeRunDecision(task.g, operations.ticket)
	if !ok || outcome != ParkOutcomeCompleted || caseID != cases[0] || !winnerLease.Valid() || taskCancel != TaskCancelNone {
		t.Fatalf("take high-cardinality decision = (%d, %d, %+v, %d, %t)", outcome, caseID, winnerLease, taskCancel, ok)
	}
	finishSchedulerParkV2Operations(t, operations, winnerLease)
	finishWaitTestTask(t, p, task, action)
	if !TerminalG(p, task.g) {
		t.Fatal("high-cardinality scheduler park retained state")
	}
}

func TestSchedulerParkSetReadyTaskCancelSuppressesCaseAndKeepsLease(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "park-v2-late-cancel")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue late-cancel task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue late-cancel task")
	}
	action := beginWaitTestResume(t, p, task)
	operations := sealSchedulerParkV2(t, task.g, 23, 77)
	commitSchedulerParkV2(t, p, task, action, operations)
	publishSchedulerParkV2(t, p, operations, 0)
	if count, ok := PollReady(p); !ok || count != 0 || task.g.park.phase != parkDetaching {
		t.Fatalf("resolve late-cancel winner = (%d, %t), phase=%d", count, ok, task.g.park.phase)
	}
	detachSchedulerParkV2(t, task.g, operations, 0)
	if count, ok := PollReady(p); !ok || count != 1 || !task.g.queued || task.g.park.phase != parkReady {
		t.Fatalf("promote late-cancel winner = (%d, %t), queued=%t phase=%d", count, ok, task.g.queued, task.g.park.phase)
	}
	if !RequestTaskCancellation(p, task.g, TaskCancelAbort) {
		t.Fatal("request task cancellation after winner became ready")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue late-canceled task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, winnerLease, taskCancel, ok := TakeRunDecision(task.g, operations.ticket)
	if !ok || outcome != ParkOutcomeCanceled || caseID != 0 || !winnerLease.Valid() || taskCancel != TaskCancelAbort ||
		task.g.park.taskCancelPhase != taskCancelCleanup || task.g.park.phase != parkDelivered {
		t.Fatalf("take late-canceled decision = (%d, %d, %+v, %d, %t), cancelPhase=%d parkPhase=%d", outcome, caseID, winnerLease, taskCancel, ok, task.g.park.taskCancelPhase, task.g.park.phase)
	}
	if winnerID, valid := winnerLease.ID(); !valid || winnerID != operations.ids[0] {
		t.Fatalf("late-canceled winner lease = (%+v, %t)", winnerID, valid)
	}

	finishSchedulerParkV2Operations(t, operations, winnerLease)
	finishWaitTestTask(t, p, task, action)
	if AcknowledgeTaskCancellation(task.g, TaskCancelShutdown) || !AcknowledgeTaskCancellation(task.g, TaskCancelAbort) || !TerminalG(p, task.g) {
		t.Fatal("late-canceled task did not reach acknowledged terminal state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestSchedulerTaskCancelAfterDeliveredParkIsObservedAtNextResumeGate(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "post-delivery-cancel")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue post-delivery cancellation task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue post-delivery cancellation task")
	}
	action := beginWaitTestResume(t, p, task)
	operations := sealSchedulerParkV2(t, task.g, 27, 81)
	commitSchedulerParkV2(t, p, task, action, operations)
	publishSchedulerParkV2(t, p, operations, 0)
	if count, ok := PollReady(p); !ok || count != 0 || task.g.park.phase != parkDetaching {
		t.Fatalf("resolve post-delivery park = (%d, %t), phase=%d", count, ok, task.g.park.phase)
	}
	detachSchedulerParkV2(t, task.g, operations, 0)
	if count, ok := PollReady(p); !ok || count != 1 {
		t.Fatalf("promote post-delivery park = (%d, %t)", count, ok)
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue post-delivery resumed task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, winnerLease, taskCancel, ok := TakeRunDecision(task.g, operations.ticket)
	if !ok || outcome != ParkOutcomeCompleted || caseID != operations.cases[0] || !winnerLease.Valid() ||
		taskCancel != TaskCancelNone || task.g.park.phase != parkDelivered {
		t.Fatalf("take post-delivery park = (%d, %d, %+v, %d, %t), phase=%d", outcome, caseID, winnerLease, taskCancel, ok, task.g.park.phase)
	}
	finishSchedulerParkV2Operations(t, operations, winnerLease)

	// Cancellation after the resume prologue cannot rewrite the decision that
	// user code already took. It stays sticky through a yield and is claimed by
	// the following resume gate.
	if !RequestTaskCancellation(p, task.g, TaskCancelAbort) {
		t.Fatal("request cancellation after delivered park")
	}
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare yield after delivered park cancellation")
	}
	action, ok = Resumed(p, task.g, action)
	if !ok || action.Kind != ActionYield || !task.g.queued || task.g.park.taskCancelPhase != taskCancelRequested {
		t.Fatalf("yield after delivered park cancellation = (%+v, %t), queued=%t phase=%d", action, ok, task.g.queued, task.g.park.taskCancelPhase)
	}
	if g, nextOK := NextRunnable(p); !nextOK || g != task.g {
		t.Fatal("dequeue post-delivery canceled task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(task.g, ParkTicket{})
	if !ok || outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) ||
		taskCancel != TaskCancelAbort || task.g.park.taskCancelPhase != taskCancelCleanup {
		t.Fatalf("take post-delivery task cancellation = (%d, %d, %+v, %d, %t), phase=%d", outcome, caseID, lease, taskCancel, ok, task.g.park.taskCancelPhase)
	}
	finishWaitTestTask(t, p, task, action)
	if !AcknowledgeTaskCancellation(task.g, TaskCancelAbort) || !TerminalG(p, task.g) {
		t.Fatal("post-delivery task cancellation did not finish cleanly")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestSchedulerRequestedTaskCancelCannotSkipGateAtTerminalSuspend(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "terminal-cancel-gate")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue terminal cancellation gate task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue terminal cancellation gate task")
	}
	action := beginWaitTestResume(t, p, task)
	if !RequestTaskCancellation(p, task.g, TaskCancelShutdown) {
		t.Fatal("request cancellation before terminal suspend")
	}
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if PrepareComplete(task.g, task.handle, task.frame.header) || task.g.pending.kind != pendingNone ||
		task.g.park.taskCancelPhase != taskCancelRequested {
		t.Fatal("terminal completion skipped requested cancellation gate")
	}

	// A legal safepoint yield leaves the request sticky. The following resume
	// gate claims cleanup, after which terminal completion is admitted.
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare cancellation safepoint yield")
	}
	action, ok := Resumed(p, task.g, action)
	if !ok || action.Kind != ActionYield {
		t.Fatalf("commit cancellation safepoint yield = (%+v, %t)", action, ok)
	}
	if g, nextOK := NextRunnable(p); !nextOK || g != task.g {
		t.Fatal("dequeue cancellation cleanup task")
	}
	action = beginWaitTestResume(t, p, task)
	if outcome, caseID, lease, taskCancel, takeOK := TakeRunDecision(task.g, ParkTicket{}); !takeOK ||
		outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) ||
		taskCancel != TaskCancelShutdown || task.g.park.taskCancelPhase != taskCancelCleanup {
		t.Fatalf("take terminal cancellation gate = (%d, %d, %+v, %d, %t), phase=%d", outcome, caseID, lease, taskCancel, takeOK, task.g.park.taskCancelPhase)
	}
	finishWaitTestTask(t, p, task, action)
	if !AcknowledgeTaskCancellation(task.g, TaskCancelShutdown) || !TerminalG(p, task.g) {
		t.Fatal("terminal cancellation gate did not finish cleanly")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestRequestedTaskCancelRejectsTerminalPanicPublication(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "panic-cancel-gate")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue panic cancellation gate task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue panic cancellation gate task")
	}
	_ = beginWaitTestResume(t, p, task)
	if !RequestTaskCancellation(p, task.g, TaskCancelAbort) {
		t.Fatal("request cancellation before panic publication")
	}
	task.frame.header.SuspendReason = uint16(SuspendPanic)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if PreparePanic(task.g, task.handle, task.frame.header, unsafe.Pointer(new(byte)), nil) ||
		task.g.pending.kind != pendingNone || task.g.park.taskCancelPhase != taskCancelRequested ||
		preemptLoad(&task.g.panicRecord.status) != explicitStatusRejected {
		t.Fatal("terminal panic skipped requested cancellation gate")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestSchedulerTaskOnlyCancelDecisionAllowsCleanupPark(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "task-only-cancel")
	if !Enqueue(p, task.g) || !RequestTaskCancellation(p, task.g, TaskCancelAbort) {
		t.Fatal("enqueue and cancel runnable task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue task-only cancellation")
	}
	action := beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(task.g, ParkTicket{})
	if !ok || outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) || taskCancel != TaskCancelAbort ||
		task.g.park.taskCancelPhase != taskCancelCleanup {
		t.Fatalf("take task-only decision = (%d, %d, %+v, %d, %t), phase=%d", outcome, caseID, lease, taskCancel, ok, task.g.park.taskCancelPhase)
	}
	if _, _, _, _, ok := TakeRunDecision(task.g, ParkTicket{}); ok {
		t.Fatal("task-only run decision replayed")
	}

	cleanupPark := sealSchedulerParkV2(t, task.g, 29)
	if kind, ok := ParkCancelKindOf(&task.g.park, cleanupPark.ticket); ok || kind != ParkCancelNone {
		t.Fatalf("cleanup park inherited task cancellation = (%d, %t)", kind, ok)
	}
	commitSchedulerParkV2(t, p, task, action, cleanupPark)
	if !RequestWaitSetCancel(p, &cleanupPark.wait, ParkCancelOperation) {
		t.Fatal("cancel cleanup park operation")
	}
	if count, ok := PollReady(p); !ok || count != 1 || !task.g.queued || task.g.park.phase != parkReady {
		t.Fatalf("promote cleanup park = (%d, %t), queued=%t phase=%d", count, ok, task.g.queued, task.g.park.phase)
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue cleanup park")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskCancel, ok = TakeRunDecision(task.g, cleanupPark.ticket)
	if !ok || outcome != ParkOutcomeCanceled || caseID != 0 || lease != (OperationResultLease{}) || taskCancel != TaskCancelNone ||
		task.g.park.taskCancelPhase != taskCancelCleanup {
		t.Fatalf("take cleanup park decision = (%d, %d, %+v, %d, %t), phase=%d", outcome, caseID, lease, taskCancel, ok, task.g.park.taskCancelPhase)
	}

	finishWaitTestTask(t, p, task, action)
	if !AcknowledgeTaskCancellation(task.g, TaskCancelAbort) || !TerminalG(p, task.g) {
		t.Fatal("task-only cancellation did not finish cleanup")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestSchedulerParkSetAndLegacyWaitPreserveQueueOrder(t *testing.T) {
	p := new(P)
	legacy := newYieldingTestG(t, "legacy-wait")
	v2 := newYieldingTestG(t, "park-v2-mixed")
	if !Enqueue(p, legacy.g) || !Enqueue(p, v2.g) {
		t.Fatal("enqueue mixed wait tasks")
	}

	if g, ok := NextRunnable(p); !ok || g != legacy.g {
		t.Fatal("dequeue legacy wait task")
	}
	legacyAction := beginWaitTestResume(t, p, legacy)
	legacyToken := new(WaitToken)
	legacyTicket, ok := ArmWait(legacyToken)
	if !ok {
		t.Fatal("arm mixed legacy wait")
	}
	legacy.frame.header.SuspendReason = uint16(SuspendPark)
	legacy.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(legacy.g, legacy.handle, legacy.frame.header, legacyToken, legacyTicket) {
		t.Fatal("prepare mixed legacy wait")
	}
	legacyAction, ok = Resumed(p, legacy.g, legacyAction)
	if !ok || legacyAction.Kind != ActionPark {
		t.Fatalf("commit mixed legacy wait = (%+v, %t)", legacyAction, ok)
	}

	if g, ok := NextRunnable(p); !ok || g != v2.g {
		t.Fatal("dequeue mixed V2 wait task")
	}
	v2Action := beginWaitTestResume(t, p, v2)
	operations := sealSchedulerParkV2(t, v2.g, 31, 91)
	commitSchedulerParkV2(t, p, v2, v2Action, operations)
	if p.waitHead != legacy.g || p.waitTail != legacy.g || legacy.g.nextWait != nil ||
		p.parkWaitHead != &operations.wait || p.parkWaitTail != &operations.wait {
		t.Fatal("mixed legacy/V2 wait queues changed")
	}

	publishSchedulerParkV2(t, p, operations, 0)
	if count, ok := PollReady(p); !ok || count != 0 || v2.g.park.phase != parkDetaching {
		t.Fatalf("resolve mixed V2 wait = (%d, %t), phase=%d", count, ok, v2.g.park.phase)
	}
	detachSchedulerParkV2(t, v2.g, operations, 0)
	if count, ok := PollReady(p); !ok || count != 1 || p.waitHead != legacy.g || p.waitTail != legacy.g ||
		p.readyHead != v2.g || p.readyTail != v2.g {
		t.Fatalf("promote V2 behind pending legacy = (%d, %t)", count, ok)
	}
	if !CompleteWait(legacyToken, legacyTicket) {
		t.Fatal("complete mixed legacy wait")
	}
	if count, ok := PollReady(p); !ok || count != 1 || HasWaiting(p) ||
		p.readyHead != v2.g || p.readyTail != legacy.g || v2.g.nextReady != legacy.g {
		t.Fatalf("mixed ready queue order = (%d, %t), waiting=%t", count, ok, HasWaiting(p))
	}

	if g, ok := NextRunnable(p); !ok || g != v2.g {
		t.Fatal("dequeue V2 before later-ready legacy task")
	}
	v2Action = beginWaitTestResume(t, p, v2)
	outcome, caseID, winnerLease, taskCancel, ok := TakeRunDecision(v2.g, operations.ticket)
	if !ok || outcome != ParkOutcomeCompleted || caseID != operations.cases[0] || !winnerLease.Valid() || taskCancel != TaskCancelNone {
		t.Fatalf("take mixed V2 decision = (%d, %d, %+v, %d, %t)", outcome, caseID, winnerLease, taskCancel, ok)
	}
	finishSchedulerParkV2Operations(t, operations, winnerLease)
	finishWaitTestTask(t, p, v2, v2Action)

	if g, ok := NextRunnable(p); !ok || g != legacy.g {
		t.Fatal("dequeue legacy task after V2 task")
	}
	legacyAction = beginWaitTestResume(t, p, legacy)
	if outcome, caseID, lease, taskCancel, ok := TakeRunDecision(legacy.g, ParkTicket{}); !ok ||
		outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) || taskCancel != TaskCancelNone {
		t.Fatalf("legacy normal resume decision = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, ok)
	}
	finishWaitTestTask(t, p, legacy, legacyAction)
	if !TerminalG(p, legacy.g) || !TerminalG(p, v2.g) {
		t.Fatal("mixed legacy/V2 waits retained scheduler state")
	}
	runtime.KeepAlive(legacy.frame.memory)
	runtime.KeepAlive(v2.frame.memory)
}

func TestPrepareParkSetFailsClosedForUnsealedStaleAndDuplicate(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "park-v2-reject")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue rejected-park task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue rejected-park task")
	}
	action := beginWaitTestResume(t, p, task)
	ticket, ok := BeginParkSet(&task.g.park, 0, 37)
	if !ok {
		t.Fatal("begin rejected scheduler park-set")
	}
	var wait WaitSetRecord
	if !PrepareWaitSetRecord(&wait, task.g, ticket) {
		t.Fatal("prepare rejected scheduler wait-set record")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if PrepareParkSet(task.g, task.handle, task.frame.header, ticket, &wait) || task.g.park.phase != parkPreparing || task.g.pending.kind != pendingNone {
		t.Fatal("unsealed park-set partially committed")
	}
	if !SealParkSet(&task.g.park, ticket) {
		t.Fatal("seal rejected scheduler park-set")
	}
	stale := ticket
	stale.generation++
	if PrepareParkSet(task.g, task.handle, task.frame.header, stale, &wait) || task.g.park.phase != parkSealed || task.g.pending.kind != pendingNone {
		t.Fatal("stale park ticket partially committed")
	}
	if !PrepareParkSet(task.g, task.handle, task.frame.header, ticket, &wait) || task.g.park.phase != parkParked || task.g.pending.kind != pendingParkSet {
		t.Fatal("exact sealed park-set was not committed")
	}
	if PrepareParkSet(task.g, task.handle, task.frame.header, ticket, &wait) || task.g.park.phase != parkParked || task.g.pending.kind != pendingParkSet {
		t.Fatal("duplicate park preparation changed committed state")
	}
	action, ok = Resumed(p, task.g, action)
	if !ok || action.Kind != ActionPark || task.g.state != GWaiting || !HasWaiting(p) {
		t.Fatalf("resume exact rejected-test park = (%+v, %t), state=%d waiting=%t", action, ok, task.g.state, HasWaiting(p))
	}

	if !RequestWaitSetCancel(p, &wait, ParkCancelOperation) {
		t.Fatal("cancel zero-candidate rejected-test park")
	}
	if count, ok := PollReady(p); !ok || count != 1 || !task.g.queued || task.g.park.phase != parkReady {
		t.Fatalf("promote rejected-test park = (%d, %t), queued=%t phase=%d", count, ok, task.g.queued, task.g.park.phase)
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue rejected-test park")
	}
	action = beginWaitTestResume(t, p, task)
	if outcome, caseID, lease, taskCancel, ok := TakeRunDecision(task.g, ticket); !ok || outcome != ParkOutcomeCanceled ||
		caseID != 0 || lease != (OperationResultLease{}) || taskCancel != TaskCancelNone {
		t.Fatalf("take rejected-test decision = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, ok)
	}
	finishWaitTestTask(t, p, task, action)
	if !TerminalG(p, task.g) {
		t.Fatal("rejected scheduler park retained state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestSchedulerParkPreparationAbortDetachesInlineWithoutParkingG(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "park-v2-prepare-abort")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue preparation-abort task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue preparation-abort task")
	}
	action := beginWaitTestResume(t, p, task)
	operations := sealSchedulerParkV2(t, task.g, 43, 101)

	// Submission may publish before a later candidate/admission step fails.
	// Abort owns that sticky fact, but because the logical wait was never
	// committed it must clean up in this resume episode without enqueueing G.
	publishSchedulerParkV2(t, p, operations, 0)
	if !AbortParkSet(&task.g.park, operations.ticket) || task.g.park.phase != parkDetaching ||
		task.g.pending.kind != pendingNone || task.g.state != GRunning || HasWaiting(p) {
		t.Fatalf("abort producer-visible preparation: phase=%d pending=%d state=%d waiting=%t", task.g.park.phase, task.g.pending.kind, task.g.state, HasWaiting(p))
	}
	detachSchedulerParkV2(t, task.g, operations, 0)
	if !ParkReady(&task.g.park, operations.ticket) {
		t.Fatal("preparation abort did not finish detach barrier")
	}
	outcome, caseID, lease, ok := ConsumeParkSet(&task.g.park, operations.ticket)
	if !ok || outcome != ParkOutcomeCanceled || caseID != 0 || lease != (OperationResultLease{}) ||
		task.g.park.phase != parkConsumed {
		t.Fatalf("consume preparation abort = (%d, %d, %+v, %t), phase=%d", outcome, caseID, lease, ok, task.g.park.phase)
	}
	if !ReleasePreparedWaitSetRecord(&operations.wait) {
		t.Fatal("release preparation-abort wait-set record")
	}
	finishSchedulerParkV2Operations(t, operations, OperationResultLease{})
	finishWaitTestTask(t, p, task, action)
	if !TerminalG(p, task.g) {
		t.Fatal("preparation abort entered scheduler wait state")
	}
	runtime.KeepAlive(task.frame.memory)
}
