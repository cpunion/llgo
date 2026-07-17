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
	"testing"
	"unsafe"
)

func newReadyTaskCancelFixture(t *testing.T) (*P, *G) {
	t.Helper()
	p := new(P)
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize cancelable G")
	}
	g.state = GRunnable
	if !Enqueue(p, g) {
		t.Fatal("enqueue cancelable G")
	}
	return p, g
}

func attachWaitingTaskCancelFixture(p *P, g *G) {
	g.state = GWaiting
	g.waiting = true
	p.waitHead = g
	p.waitTail = g
}

func detachWaitingTaskCancelFixture(p *P, g *G) {
	p.waitHead = nil
	p.waitTail = nil
	g.waiting = false
	g.nextWait = nil
}

func resumeTaskCancelFixture(t *testing.T, p *P, g *G) {
	t.Helper()
	detachWaitingTaskCancelFixture(p, g)
	g.state = GRunnable
	if !Enqueue(p, g) {
		t.Fatal("enqueue resumed task")
	}
}

func finishTaskCancelFixture(t *testing.T, p *P, g *G, kind TaskCancelKind) {
	t.Helper()
	if dequeue(p) != g {
		t.Fatal("dequeue terminal task")
	}
	preemptStore(preemptAddress(g), preemptDisabled)
	g.state = GDead
	if !AcknowledgeTaskCancellation(g, kind) {
		t.Fatal("acknowledge terminal task cancellation")
	}
}

func TestTaskCancellationIsOwnerOnlyStickyAndMonotonic(t *testing.T) {
	if unsafe.Sizeof(TaskCancelKind(0)) != 1 {
		t.Fatalf("task cancel token size = %d", unsafe.Sizeof(TaskCancelKind(0)))
	}
	p, g := newReadyTaskCancelFixture(t)
	if RequestTaskCancellation(new(P), g, TaskCancelAbort) ||
		RequestTaskCancellation(p, g, TaskCancelNone) {
		t.Fatal("accepted non-owner or invalid task cancellation")
	}
	if !RequestTaskCancellation(p, g, TaskCancelAbort) ||
		!RequestTaskCancellation(p, g, TaskCancelAbort) {
		t.Fatal("publish or preserve task abort")
	}
	if kind, ok := TaskCancellationOf(new(P), g); ok || kind != TaskCancelNone {
		t.Fatalf("non-owner observed task cancellation = (%d, %t)", kind, ok)
	}
	if kind, ok := TaskCancellationOf(p, g); !ok || kind != TaskCancelAbort {
		t.Fatalf("task abort = (%d, %t)", kind, ok)
	}
	if !RequestTaskCancellation(p, g, TaskCancelShutdown) ||
		!RequestTaskCancellation(p, g, TaskCancelAbort) {
		t.Fatal("upgrade or preserve task shutdown")
	}
	if kind, ok := TaskCancellationOf(p, g); !ok || kind != TaskCancelShutdown {
		t.Fatalf("task shutdown = (%d, %t)", kind, ok)
	}
	if kind, ok := ClaimTaskCancellation(p, g); !ok || kind != TaskCancelShutdown {
		t.Fatalf("claim task shutdown = (%d, %t)", kind, ok)
	}
	if kind, ok := ClaimTaskCancellation(p, g); ok || kind != TaskCancelNone {
		t.Fatalf("claimed task shutdown twice = (%d, %t)", kind, ok)
	}
	if !RequestTaskCancellation(p, g, TaskCancelAbort) {
		t.Fatal("coalesce request during cleanup")
	}
	if kind, ok := TaskCancellationOf(p, g); !ok || kind != TaskCancelShutdown {
		t.Fatalf("cleanup terminal cause changed = (%d, %t)", kind, ok)
	}
	finishTaskCancelFixture(t, p, g, TaskCancelShutdown)
}

func TestTaskCancellationOverridesCompletionAtWaitingPark(t *testing.T) {
	p := new(P)
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize waiting G")
	}
	ticket, ok := BeginParkSet(&g.park, 1, 17)
	id, idOK := MakeOperationID(OperationSourceManual, 1, 1)
	var record OperationRecord
	if !ok || !idOK || !InitOperation(&record, id) ||
		!AttachParkOperation(&g.park, ticket, &record, 7) ||
		!SealParkSet(&g.park, ticket) || !CommitParkSet(&g.park, ticket) ||
		PublishOperationCompletion(&record, id) != OperationCompletionPublished {
		t.Fatal("prepare completed waiting park")
	}
	attachWaitingTaskCancelFixture(p, g)
	if !RequestTaskCancellation(p, g, TaskCancelShutdown) {
		t.Fatal("request waiting task shutdown")
	}
	if kind, ok := ParkCancelKindOf(&g.park, ticket); !ok || kind != ParkCancelShutdown {
		t.Fatalf("waiting park cancel = (%d, %t)", kind, ok)
	}
	if resolution, resolved := ResolveParkSnapshot(&g.park, ticket); !resolved ||
		resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 1}) ||
		record.disposition != OperationDispositionCanceled {
		t.Fatalf("resolve task cancel/completion race = (%+v, %t)", resolution, resolved)
	}
	if !AcknowledgeOperationResolution(&record, id, OperationDispositionCanceled) ||
		!DetachParkOperation(&g.park, ticket, &record, id) || !ParkReady(&g.park, ticket) {
		t.Fatal("detach task-canceled operation")
	}
	resumeTaskCancelFixture(t, p, g)
	outcome, caseID, lease, task, consumed := ConsumeTaskParkSet(p, g, ticket)
	if !consumed || outcome != ParkOutcomeCanceled || caseID != 0 || lease != (OperationResultLease{}) || task != TaskCancelShutdown {
		t.Fatalf("consume task-canceled park = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, task, consumed)
	}
	if !ConfirmOperationQuiesced(&record, id) || !OperationCanRecycle(&record, id) || !RecycleOperation(&record, id) {
		t.Fatal("recycle task-canceled operation")
	}
	if AcknowledgeTaskCancellation(g, TaskCancelAbort) {
		t.Fatal("acknowledged wrong terminal cause")
	}
	finishTaskCancelFixture(t, p, g, TaskCancelShutdown)
}

func TestRunnableTaskCancellationCarriesIntoNextParkBoundary(t *testing.T) {
	p, g := newReadyTaskCancelFixture(t)
	if !RequestTaskCancellation(p, g, TaskCancelAbort) {
		t.Fatal("request runnable task abort")
	}
	if dequeue(p) != g {
		t.Fatal("dequeue task before park")
	}
	ticket, ok := BeginParkSet(&g.park, 0, 23)
	if !ok || !SealParkSet(&g.park, ticket) || !CommitParkSet(&g.park, ticket) {
		t.Fatal("commit next zero-candidate park")
	}
	if kind, ok := ParkCancelKindOf(&g.park, ticket); !ok || kind != ParkCancelTaskAbort {
		t.Fatalf("park-boundary cancel = (%d, %t)", kind, ok)
	}
	attachWaitingTaskCancelFixture(p, g)
	if resolution, resolved := ResolveParkSnapshot(&g.park, ticket); !resolved ||
		resolution != (CompletionResolution{WaitSets: 1, Canceled: 1}) || !ParkReady(&g.park, ticket) {
		t.Fatalf("resolve pending task cancellation = (%+v, %t)", resolution, resolved)
	}
	resumeTaskCancelFixture(t, p, g)
	outcome, caseID, lease, task, consumed := ConsumeTaskParkSet(p, g, ticket)
	if !consumed || outcome != ParkOutcomeCanceled || caseID != 0 || lease != (OperationResultLease{}) || task != TaskCancelAbort {
		t.Fatalf("consume carried task cancellation = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, task, consumed)
	}
	finishTaskCancelFixture(t, p, g, TaskCancelAbort)
}

func TestLateTaskCancellationSuppressesReadyWinnerAndKeepsLease(t *testing.T) {
	p := new(P)
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize late-cancel G")
	}
	ticket, ok := BeginParkSet(&g.park, 1, 29)
	id, idOK := MakeOperationID(OperationSourceManual, 2, 1)
	var record OperationRecord
	if !ok || !idOK || !InitOperation(&record, id) ||
		!AttachParkOperation(&g.park, ticket, &record, 41) ||
		!SealParkSet(&g.park, ticket) || !CommitParkSet(&g.park, ticket) ||
		PublishOperationCompletion(&record, id) != OperationCompletionPublished {
		t.Fatal("prepare late-cancel winner")
	}
	attachWaitingTaskCancelFixture(p, g)
	if resolution, resolved := ResolveParkSnapshot(&g.park, ticket); !resolved ||
		resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}) {
		t.Fatalf("resolve late-cancel winner = (%+v, %t)", resolution, resolved)
	}
	if !AcknowledgeOperationResolution(&record, id, OperationDispositionWinner) ||
		!DetachParkOperation(&g.park, ticket, &record, id) || !ParkReady(&g.park, ticket) {
		t.Fatal("detach late-cancel winner")
	}
	if !RequestTaskCancellation(p, g, TaskCancelAbort) {
		t.Fatal("request task abort after winner became ready")
	}
	resumeTaskCancelFixture(t, p, g)
	outcome, caseID, lease, task, consumed := ConsumeTaskParkSet(p, g, ticket)
	if !consumed || outcome != ParkOutcomeCanceled || caseID != 0 || !lease.Valid() || task != TaskCancelAbort {
		t.Fatalf("consume late-canceled winner = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, task, consumed)
	}
	if leaseID, valid := lease.ID(); !valid || leaseID != id {
		t.Fatalf("winner lease ID = (%+v, %t)", leaseID, valid)
	}
	if !ConfirmOperationQuiesced(&record, id) || OperationCanRecycle(&record, id) {
		t.Fatal("winner recycled before cleanup discarded its leased result")
	}
	if !TakeOperationResult(&record, lease) || !OperationCanRecycle(&record, id) || !RecycleOperation(&record, id) {
		t.Fatal("discard and recycle late-canceled winner result")
	}
	finishTaskCancelFixture(t, p, g, TaskCancelAbort)
}

func TestTaskCancellationCleanupDoesNotReenterOrCancelCleanupPark(t *testing.T) {
	p, g := newReadyTaskCancelFixture(t)
	if !RequestTaskCancellation(p, g, TaskCancelAbort) {
		t.Fatal("request task abort")
	}
	if kind, ok := ClaimTaskCancellation(p, g); !ok || kind != TaskCancelAbort {
		t.Fatalf("claim task abort = (%d, %t)", kind, ok)
	}
	if !RequestTaskCancellation(p, g, TaskCancelShutdown) {
		t.Fatal("coalesce stronger request during cleanup")
	}
	if kind, ok := TaskCancellationOf(p, g); !ok || kind != TaskCancelAbort {
		t.Fatalf("cleanup cause was upgraded = (%d, %t)", kind, ok)
	}
	if kind, ok := ClaimTaskCancellation(p, g); ok || kind != TaskCancelNone {
		t.Fatalf("cleanup re-entered = (%d, %t)", kind, ok)
	}
	ticket, ok := BeginParkSet(&g.park, 0, 31)
	if !ok || !SealParkSet(&g.park, ticket) || !CommitParkSet(&g.park, ticket) {
		t.Fatal("commit park from cleanup")
	}
	if kind, ok := ParkCancelKindOf(&g.park, ticket); ok || kind != ParkCancelNone {
		t.Fatalf("cleanup park inherited old task cancellation = (%d, %t)", kind, ok)
	}
	if !RequestParkCancel(&g.park, ticket, ParkCancelOperation) {
		t.Fatal("cancel cleanup operation")
	}
	if resolution, resolved := ResolveParkSnapshot(&g.park, ticket); !resolved ||
		resolution != (CompletionResolution{WaitSets: 1, Canceled: 1}) {
		t.Fatalf("resolve cleanup park = (%+v, %t)", resolution, resolved)
	}
	outcome, caseID, lease, task, consumed := ConsumeTaskParkSet(p, g, ticket)
	if !consumed || outcome != ParkOutcomeCanceled || caseID != 0 || lease != (OperationResultLease{}) || task != TaskCancelNone {
		t.Fatalf("consume cleanup park = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, task, consumed)
	}
	finishTaskCancelFixture(t, p, g, TaskCancelAbort)
}

func TestTaskCancellationRejectsLegacyWaitWithoutPartialMutation(t *testing.T) {
	p := new(P)
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize legacy waiting G")
	}
	attachWaitingTaskCancelFixture(p, g)
	if RequestTaskCancellation(p, g, TaskCancelAbort) || g.park.taskCancelKind != TaskCancelNone ||
		g.park.taskCancelPhase != taskCancelIdle || g.park.phase != parkIdle {
		t.Fatal("partially canceled legacy wait without V2 park")
	}
}

func TestTaskCancellationRejectsCyclicOwnerQueue(t *testing.T) {
	p, g := newReadyTaskCancelFixture(t)
	g.nextReady = g
	if RequestTaskCancellation(p, g, TaskCancelAbort) || g.park.taskCancelKind != TaskCancelNone ||
		g.park.taskCancelPhase != taskCancelIdle {
		t.Fatal("accepted task from corrupt cyclic ready queue")
	}
}

func TestTaskCancellationAcknowledgesOnlyClaimedTerminalCleanG(t *testing.T) {
	p, g := newReadyTaskCancelFixture(t)
	if !RequestTaskCancellation(p, g, TaskCancelAbort) ||
		AcknowledgeTaskCancellation(g, TaskCancelAbort) || ReclaimableG(g) {
		t.Fatal("acknowledged live requested task")
	}
	if kind, ok := ClaimTaskCancellation(p, g); !ok || kind != TaskCancelAbort {
		t.Fatalf("claim terminal task = (%d, %t)", kind, ok)
	}
	if AcknowledgeTaskCancellation(g, TaskCancelAbort) {
		t.Fatal("acknowledged live cleanup task")
	}
	if dequeue(p) != g {
		t.Fatal("dequeue terminal task")
	}
	preemptStore(preemptAddress(g), preemptDisabled)
	g.state = GDead
	if ReclaimableG(g) || AcknowledgeTaskCancellation(g, TaskCancelShutdown) {
		t.Fatal("unacknowledged or mismatched task became reclaimable")
	}
	if !AcknowledgeTaskCancellation(g, TaskCancelAbort) || !ReclaimableG(g) {
		t.Fatal("terminal task acknowledgement")
	}
}
