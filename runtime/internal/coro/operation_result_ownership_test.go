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

func discardUnselectedTestResult(t *testing.T, record *OperationRecord, id OperationID) {
	t.Helper()
	if record != nil && record.disposition != OperationDispositionWinner && record.resultState == operationResultOwned &&
		!DiscardUnselectedOperationResult(record, id) {
		t.Fatal("discard unselected operation result")
	}
}

func TestReadyCommitSuccessRequiresExactResultBinding(t *testing.T) {
	source := newCommitSelectFakeSource(t, 0x711, []commitSelectCandidateSpec{{
		caseID: 71, mode: OperationCommitReadyThenTryCommit, canCommit: true,
	}}, []int{0}, false, 0)
	source.publish(t, 0)
	if source.records[0].resultState != operationResultEmpty {
		t.Fatal("Ready hint owned a result before TryCommit")
	}
	_, request, status := ResolveParkSnapshotStep(source.state, source.ticket, ParkCommitAttempt{})
	if status != ParkResolveNeedsCommit || !currentParkCommitRequest(request) {
		t.Fatal("resolver did not return a current Ready request")
	}

	beforeState, beforeRecord := *source.state, source.records[0]
	forged := ParkCommitAttempt{request: request, result: ParkCommitAttemptSucceeded}
	if resolution, _, got := ResolveParkSnapshotStep(source.state, source.ticket, forged); got != ParkResolveInvalid ||
		resolution != (CompletionResolution{}) || *source.state != beforeState || source.records[0] != beforeRecord {
		t.Fatal("unbound successful attempt changed the frozen snapshot")
	}
	attempt, bound := BindParkCommitResult(request)
	if !bound || attempt.result != ParkCommitAttemptSucceeded || source.records[0].resultState != operationResultOwned ||
		currentParkCommitRequest(request) {
		t.Fatal("exact Ready result was not bound once")
	}
	if duplicate, ok := BindParkCommitResult(request); ok || duplicate != (ParkCommitAttempt{}) ||
		request.Failed() != (ParkCommitAttempt{}) {
		t.Fatal("bound Ready request produced a duplicate attempt")
	}
	if resolution, _, got := ResolveParkSnapshotStep(source.state, source.ticket, attempt); got != ParkResolveResolved ||
		resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}) {
		t.Fatalf("resolve bound Ready result = (%+v, %d)", resolution, got)
	}
	source.finish(t)
}

func TestPublishedResultsMustBeDiscardedAfterSourceRollbackBeforeLoserAck(t *testing.T) {
	source := newCommitSelectFakeSource(t, 0x712, []commitSelectCandidateSpec{
		{caseID: 72, mode: OperationCommitIrreversibleCompletion},
		{caseID: 73, mode: OperationCommitReservable},
	}, []int{1, 0}, false, 0)
	for index := range source.records {
		if source.records[index].resultState != operationResultEmpty {
			t.Fatalf("candidate %d was not initially Empty", index)
		}
		source.publish(t, index)
		if source.records[index].resultState != operationResultOwned {
			t.Fatalf("candidate %d publication did not establish Owned", index)
		}
	}
	if !RequestParkCancel(source.state, source.ticket, ParkCancelTaskAbort) {
		t.Fatal("request strong cancellation")
	}
	if resolution, status := source.resolve(t); status != ParkResolveResolved ||
		resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 2}) {
		t.Fatalf("resolve published-result cancellation = (%+v, %d)", resolution, status)
	}
	if operationCandidateState(&source.records[1]) != OperationCommitRolledBack {
		t.Fatal("reservable loser did not reach logical rollback")
	}
	for index := range source.records {
		record, id := &source.records[index], source.ids[index]
		if AcknowledgeOperationResolution(record, id, OperationDispositionCanceled) {
			t.Fatalf("candidate %d acknowledged while its result was still Owned", index)
		}
		// This bit models completion of source-specific cancel/rollback before
		// the generic ownership transition.
		source.released[index] = true
		if !DiscardUnselectedOperationResult(record, id) || record.resultState != operationResultDiscarded ||
			DiscardUnselectedOperationResult(record, id) ||
			!AcknowledgeOperationResolution(record, id, OperationDispositionCanceled) {
			t.Fatalf("candidate %d loser cleanup/ack sequence failed", index)
		}
	}
	if outcome, _, lease := source.finish(t); outcome != ParkOutcomeCanceled || lease.Valid() {
		t.Fatalf("consume canceled published results = (%d, %+v)", outcome, lease)
	}
}

func TestReadyFailureAndRepublishNeverOwnAResult(t *testing.T) {
	source := newCommitSelectFakeSource(t, 0x713, []commitSelectCandidateSpec{{
		caseID: 74, mode: OperationCommitReadyThenTryCommit,
	}}, []int{0}, true, 75)
	source.publish(t, 0)
	if source.records[0].resultState != operationResultEmpty {
		t.Fatal("initial Ready hint owned a result")
	}
	if resolution, status := source.resolve(t); status != ParkResolveResolved || resolution.Defaulted != 1 ||
		source.records[0].resultState != operationResultEmpty {
		t.Fatalf("failed Ready/default ownership = (%+v, %d, %d)", resolution, status, source.records[0].resultState)
	}
	if outcome, caseID, lease := source.finish(t); outcome != ParkOutcomeDefault || caseID != 75 || lease.Valid() {
		t.Fatalf("consume Ready/default = (%d, %d, %+v)", outcome, caseID, lease)
	}

	retry := newCommitSelectFakeSource(t, 0x714, []commitSelectCandidateSpec{{
		caseID: 76, mode: OperationCommitReadyThenTryCommit,
	}}, []int{0}, false, 0)
	retry.publish(t, 0)
	if resolution, status := retry.resolve(t); status != ParkResolvePending || resolution != (CompletionResolution{WaitSets: 1}) ||
		retry.records[0].resultState != operationResultEmpty {
		t.Fatalf("failed Ready ownership = (%+v, %d, %d)", resolution, status, retry.records[0].resultState)
	}
	retry.publish(t, 0)
	if retry.records[0].resultState != operationResultEmpty {
		t.Fatal("republished Ready hint owned a result")
	}
	if !RequestParkCancel(retry.state, retry.ticket, ParkCancelOperation) {
		t.Fatal("cancel republished Ready fixture")
	}
	if resolution, status := retry.resolve(t); status != ParkResolveResolved || resolution.Canceled != 1 {
		t.Fatalf("resolve republished Ready cleanup = (%+v, %d)", resolution, status)
	}
	retry.finish(t)
}

func TestWinnerResultLeaseTakeAndDiscardAreDistinctTerminalActions(t *testing.T) {
	for _, discard := range []bool{false, true} {
		name := "take"
		if discard {
			name = "discard"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newParkV2Fixture(t, 0x715, []uint32{77})
			publishParkV2(t, fixture, 0)
			resolveParkV2(t, fixture)
			if fixture.records[0].resultState != operationResultOwned {
				t.Fatal("resolved winner did not retain Owned result")
			}
			detachParkV2(t, fixture, 0)
			if !ConfirmOperationQuiesced(&fixture.records[0], fixture.ids[0]) {
				t.Fatal("quiesce winner")
			}
			outcome, caseID, lease, consumed := ConsumeParkSet(&fixture.state, fixture.ticket)
			if !consumed || outcome != ParkOutcomeCompleted || caseID != 77 || !lease.Valid() ||
				fixture.records[0].resultState != operationResultLeased {
				t.Fatalf("consume winner lease = (%d, %d, %+v, %t)", outcome, caseID, lease, consumed)
			}
			invalid := lease
			invalid.ticket = ParkTicket{epoch: 1}
			stale := lease
			stale.ticket.generation++
			if invalid.Valid() || TakeOperationResult(&fixture.records[0], invalid) ||
				DiscardOperationResult(&fixture.records[0], stale) || OperationCanRecycle(&fixture.records[0], fixture.ids[0]) {
				t.Fatal("invalid/stale lease changed a leased winner")
			}
			if discard {
				if !DiscardOperationResult(&fixture.records[0], lease) || TakeOperationResult(&fixture.records[0], lease) ||
					fixture.records[0].resultState != operationResultDiscarded {
					t.Fatal("Discard did not reach its distinct terminal state")
				}
			} else if !TakeOperationResult(&fixture.records[0], lease) || DiscardOperationResult(&fixture.records[0], lease) ||
				fixture.records[0].resultState != operationResultTaken {
				t.Fatal("Take did not reach its distinct terminal state")
			}
			if !OperationCanRecycle(&fixture.records[0], fixture.ids[0]) ||
				!RecycleOperation(&fixture.records[0], fixture.ids[0]) {
				t.Fatal("recycle terminal winner")
			}
		})
	}
}

func TestReadyResultBindingIsAllocationFree(t *testing.T) {
	source := newCommitSelectFakeSource(t, 0x716, []commitSelectCandidateSpec{{
		caseID: 78, mode: OperationCommitReadyThenTryCommit, canCommit: true,
	}}, []int{0}, false, 0)
	source.publish(t, 0)
	_, request, status := ResolveParkSnapshotStep(source.state, source.ticket, ParkCommitAttempt{})
	if status != ParkResolveNeedsCommit {
		t.Fatal("prepare allocation-free Ready bind")
	}
	stateBefore, recordBefore := *source.state, source.records[0]
	failed := false
	allocations := testing.AllocsPerRun(1000, func() {
		*source.state = stateBefore
		source.records[0] = recordBefore
		if _, bound := BindParkCommitResult(request); !bound {
			failed = true
		}
	})
	if failed || allocations != 0 {
		t.Fatalf("Ready result bind = failed %t allocations %.2f", failed, allocations)
	}
	*source.state = stateBefore
	source.records[0] = recordBefore
	attempt, bound := BindParkCommitResult(request)
	if !bound {
		t.Fatal("restore allocation-free Ready bind")
	}
	if resolution, _, got := ResolveParkSnapshotStep(source.state, source.ticket, attempt); got != ParkResolveResolved || resolution.Completed != 1 {
		t.Fatalf("resolve allocation-free Ready bind = (%+v, %d)", resolution, got)
	}
	source.finish(t)
}
