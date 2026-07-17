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

type parkV2Fixture struct {
	state   ParkState
	ticket  ParkTicket
	records []OperationRecord
	ids     []OperationID
	cases   []uint32
}

func newParkV2Fixture(t *testing.T, seed uint32, cases []uint32) *parkV2Fixture {
	t.Helper()
	fixture := &parkV2Fixture{
		records: make([]OperationRecord, len(cases)),
		ids:     make([]OperationID, len(cases)),
		cases:   append([]uint32(nil), cases...),
	}
	ticket, ok := BeginParkSet(&fixture.state, uint32(len(cases)), seed)
	if !ok {
		t.Fatal("begin park-set")
	}
	fixture.ticket = ticket
	for index, caseID := range cases {
		source := OperationSourceWait
		if index%2 != 0 {
			source = OperationSourceTimer
		}
		id, idOK := MakeOperationID(source, uint32(index+1), 1)
		if !idOK || !InitOperation(&fixture.records[index], id) ||
			!AttachParkOperation(&fixture.state, ticket, &fixture.records[index], caseID) {
			t.Fatalf("attach candidate %d", index)
		}
		fixture.ids[index] = id
	}
	if !SealParkSet(&fixture.state, ticket) || !CommitParkSet(&fixture.state, ticket) || !validParkState(&fixture.state) {
		t.Fatal("commit park-set")
	}
	return fixture
}

func publishParkV2(t *testing.T, fixture *parkV2Fixture, indices ...int) {
	t.Helper()
	for _, index := range indices {
		if result := PublishOperationCompletion(&fixture.records[index], fixture.ids[index]); result != OperationCompletionPublished {
			t.Fatalf("publish candidate %d = %d", index, result)
		}
	}
}

func resolveParkV2(t *testing.T, fixture *parkV2Fixture, order []int, includeCancel bool) CompletionResolution {
	t.Helper()
	var sink CompletionSink
	if !BeginCompletionBatch(&sink) {
		t.Fatal("begin completion batch")
	}
	for _, index := range order {
		if result := CollectOperationCompletion(&sink, &fixture.records[index], fixture.ids[index]); result != CompletionCollectAccepted {
			t.Fatalf("collect candidate %d = %d", index, result)
		}
	}
	if includeCancel {
		if result := CollectParkCancellation(&sink, &fixture.state, fixture.ticket); result != CompletionCollectAccepted {
			t.Fatalf("collect cancel = %d", result)
		}
	}
	if !SealCompletionBatch(&sink) {
		t.Fatal("seal completion batch")
	}
	resolution, ok := ResolveCompletionBatch(&sink)
	if !ok {
		t.Fatal("resolve completion batch")
	}
	return resolution
}

func detachParkV2(t *testing.T, fixture *parkV2Fixture, order ...int) {
	t.Helper()
	for position, index := range order {
		disposition, ok := OperationDispositionOf(&fixture.records[index], fixture.ids[index])
		if !ok || !AcknowledgeOperationResolution(&fixture.records[index], fixture.ids[index], disposition) {
			t.Fatalf("apply candidate %d resolution", index)
		}
		if !DetachParkOperation(&fixture.state, fixture.ticket, &fixture.records[index], fixture.ids[index]) {
			t.Fatalf("detach candidate %d", index)
		}
		wantReady := position == len(order)-1 && len(order) == len(fixture.records)
		if ParkReady(&fixture.state, fixture.ticket) != wantReady {
			t.Fatalf("ready after detach %d = %t, want %t", index, ParkReady(&fixture.state, fixture.ticket), wantReady)
		}
	}
}

func finishParkV2Operations(t *testing.T, fixture *parkV2Fixture, winnerLease OperationResultLease) {
	t.Helper()
	for index := range fixture.records {
		record := &fixture.records[index]
		id := fixture.ids[index]
		if !ConfirmOperationQuiesced(record, id) {
			t.Fatalf("quiesce candidate %d", index)
		}
		winnerID, _ := winnerLease.ID()
		if id == winnerID {
			if OperationCanRecycle(record, id) {
				t.Fatalf("winner %d recycled before result release", index)
			}
			if !TakeOperationResult(record, winnerLease) {
				t.Fatalf("release winner %d result", index)
			}
		}
		if !OperationCanRecycle(record, id) || !RecycleOperation(record, id) {
			t.Fatalf("recycle candidate %d", index)
		}
	}
}

func resolveWinnerForOrder(t *testing.T, seed uint32, order []int) uint32 {
	t.Helper()
	fixture := newParkV2Fixture(t, seed, []uint32{10, 20, 30})
	publishParkV2(t, fixture, 0, 1, 2)
	resolution := resolveParkV2(t, fixture, order, false)
	if resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1, Losers: 2}) {
		t.Fatalf("resolution = %+v", resolution)
	}
	caseID, winnerID, ok := ParkWinner(&fixture.state, fixture.ticket)
	if !ok {
		t.Fatal("missing winner")
	}
	winners := 0
	for index := range fixture.records {
		claim := ParkOperationClaim(&fixture.records[index], fixture.ids[index])
		if claim == ParkClaimWon {
			winners++
		} else if claim != ParkClaimLost {
			t.Fatalf("candidate %d claim = %d", index, claim)
		}
	}
	if winners != 1 {
		t.Fatalf("winner count = %d", winners)
	}
	detachParkV2(t, fixture, 2, 0, 1)
	outcome, consumedCase, winnerLease, consumed := ConsumeParkSet(&fixture.state, fixture.ticket)
	leaseID, leaseOK := winnerLease.ID()
	if !consumed || outcome != ParkOutcomeCompleted || consumedCase != caseID || !leaseOK || leaseID != winnerID {
		t.Fatalf("consume winner = (%d, %d, %+v, %t), want (%d, %d, %+v)", outcome, consumedCase, winnerLease, consumed, ParkOutcomeCompleted, caseID, winnerID)
	}
	finishParkV2Operations(t, fixture, winnerLease)
	return caseID
}

func TestOperationIDIsTwoWordPODAndFailsClosedAtExhaustion(t *testing.T) {
	if unsafe.Sizeof(OperationID{}) != 8 || unsafe.Alignof(OperationID{}) != 4 ||
		unsafe.Offsetof(OperationID{}.Generation) != 4 {
		t.Fatalf("OperationID layout: size=%d align=%d generation=%d", unsafe.Sizeof(OperationID{}), unsafe.Alignof(OperationID{}), unsafe.Offsetof(OperationID{}.Generation))
	}
	if unsafe.Sizeof(ParkTicket{}) != 8 || unsafe.Alignof(ParkTicket{}) != 4 ||
		unsafe.Offsetof(ParkTicket{}.generation) != 4 {
		t.Fatalf("ParkTicket layout: size=%d align=%d generation=%d", unsafe.Sizeof(ParkTicket{}), unsafe.Alignof(ParkTicket{}), unsafe.Offsetof(ParkTicket{}.generation))
	}
	id, ok := MakeOperationID(OperationSourcePoll, operationSlotMask, 41)
	if !ok || id.Source() != OperationSourcePoll || id.Slot() != operationSlotMask || id.Generation != 41 || !id.Valid() {
		t.Fatalf("operation ID = %+v, valid=%t", id, ok)
	}
	if invalid, ok := MakeOperationID(OperationSourceInvalid, 1, 1); ok || invalid != (OperationID{}) {
		t.Fatal("accepted invalid source")
	}
	if invalid, ok := MakeOperationID(OperationSource(255), 1, 1); ok || invalid != (OperationID{}) {
		t.Fatal("accepted unregistered source")
	}
	if invalid, ok := MakeOperationID(OperationSourceWait, operationSlotMask+1, 1); ok || invalid != (OperationID{}) {
		t.Fatal("accepted overflowing slot")
	}
	last, _ := MakeOperationID(OperationSourceWait, 1, ^uint32(0))
	if next, ok := NextOperationID(last, OperationSourceWait, 1); ok || next != (OperationID{}) {
		t.Fatal("physical generation wrapped")
	}
}

func TestZeroCandidateParkCanOnlyResumeThroughLogicalCancel(t *testing.T) {
	var state ParkState
	ticket, ok := BeginParkSet(&state, 0, 3)
	if !ok || !SealParkSet(&state, ticket) || !CommitParkSet(&state, ticket) || ParkReady(&state, ticket) {
		t.Fatal("prepare zero-candidate park")
	}
	if !RequestParkCancel(&state, ticket, ParkCancelTaskAbort) {
		t.Fatal("cancel zero-candidate park")
	}
	var sink CompletionSink
	if !BeginCompletionBatch(&sink) || CollectParkCancellation(&sink, &state, ticket) != CompletionCollectAccepted ||
		!SealCompletionBatch(&sink) {
		t.Fatal("collect zero-candidate cancellation")
	}
	resolution, resolved := ResolveCompletionBatch(&sink)
	if !resolved || resolution != (CompletionResolution{WaitSets: 1, Canceled: 1}) || !ParkReady(&state, ticket) {
		t.Fatalf("resolve zero-candidate cancellation = (%+v, %t)", resolution, resolved)
	}
	outcome, _, lease, consumed := ConsumeParkSet(&state, ticket)
	if !consumed || outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) {
		t.Fatalf("consume zero-candidate cancellation = (%d, %+v, %t)", outcome, lease, consumed)
	}
}

func TestAbortParkPreparationUsesNormalDetachBarrier(t *testing.T) {
	var state ParkState
	ticket, ok := BeginParkSet(&state, 3, 5)
	if !ok {
		t.Fatal("begin partial preparation")
	}
	var records [3]OperationRecord
	var ids [3]OperationID
	for index := range records {
		id, idOK := MakeOperationID(OperationSourceManual, uint32(index+1), 1)
		if !idOK || !InitOperation(&records[index], id) {
			t.Fatalf("reserve operation %d", index)
		}
		ids[index] = id
		if index < 2 && !AttachParkOperation(&state, ticket, &records[index], uint32(index)) {
			t.Fatalf("attach operation %d", index)
		}
	}
	if !AbortParkSet(&state, ticket) || ParkReady(&state, ticket) || AbortParkSet(&state, ticket) {
		t.Fatal("abort partial preparation")
	}
	for index := 0; index < 2; index++ {
		disposition, dispositionOK := OperationDispositionOf(&records[index], ids[index])
		if !dispositionOK || disposition != OperationDispositionCanceled ||
			!AcknowledgeOperationResolution(&records[index], ids[index], disposition) ||
			!DetachParkOperation(&state, ticket, &records[index], ids[index]) {
			t.Fatalf("detach aborted operation %d", index)
		}
	}
	if !ParkReady(&state, ticket) {
		t.Fatal("aborted preparation did not finish detach barrier")
	}
	if !AbortReservedOperation(&records[2], ids[2]) {
		t.Fatal("abort unpublished reservation")
	}
	if next, rearmed := RearmOperation(&records[2]); !rearmed || next.Generation != 2 {
		t.Fatal("aborted reservation did not consume generation")
	}
}

func TestAbortPartialParkPreparationDiscardsPublishedCompletion(t *testing.T) {
	var state ParkState
	ticket, ok := BeginParkSet(&state, 2, 6)
	firstID, firstIDOK := MakeOperationID(OperationSourceManual, 1, 1)
	secondID, secondIDOK := MakeOperationID(OperationSourceManual, 2, 1)
	var first, second OperationRecord
	if !ok || !firstIDOK || !secondIDOK || !InitOperation(&first, firstID) || !InitOperation(&second, secondID) ||
		!AttachParkOperation(&state, ticket, &first, 1) ||
		PublishOperationCompletion(&first, firstID) != OperationCompletionPublished {
		t.Fatal("prepare partial park with early completion")
	}
	// The second candidate's admission/submission fails before attach. The
	// published first result must still have a terminal cleanup path.
	if !AbortParkSet(&state, ticket) || ParkReady(&state, ticket) ||
		first.disposition != OperationDispositionCanceled || !first.cancelRequested {
		t.Fatal("abort partial park after early completion")
	}
	if !AcknowledgeOperationResolution(&first, firstID, OperationDispositionCanceled) ||
		!DetachParkOperation(&state, ticket, &first, firstID) || !ParkReady(&state, ticket) ||
		!AbortReservedOperation(&second, secondID) {
		t.Fatal("clean up partial park after early completion")
	}
	outcome, _, lease, consumed := ConsumeParkSet(&state, ticket)
	if !consumed || outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) {
		t.Fatalf("consume aborted partial park = (%d, %+v, %t)", outcome, lease, consumed)
	}
	if !ConfirmOperationQuiesced(&first, firstID) || !OperationCanRecycle(&first, firstID) ||
		!RecycleOperation(&first, firstID) {
		t.Fatal("recycle discarded early completion")
	}
}

func TestOperationBecomesProducerVisibleOnlyAfterAttach(t *testing.T) {
	var state ParkState
	ticket, ok := BeginParkSet(&state, 1, 7)
	id, idOK := MakeOperationID(OperationSourceManual, 1, 1)
	var record OperationRecord
	if !ok || !idOK || !InitOperation(&record, id) {
		t.Fatal("reserve early-completion operation")
	}
	if PublishOperationCompletion(&record, id) != OperationCompletionInvalid || record.Matches(id) {
		t.Fatal("reserved operation was producer-visible before attach")
	}
	if !AttachParkOperation(&state, ticket, &record, 1) || !record.Matches(id) ||
		PublishOperationCompletion(&record, id) != OperationCompletionPublished {
		t.Fatal("attached operation rejected synchronous early completion")
	}
	if !SealParkSet(&state, ticket) || !CommitParkSet(&state, ticket) {
		t.Fatal("commit early-completed park")
	}
	var sink CompletionSink
	if !BeginCompletionBatch(&sink) || CollectOperationCompletion(&sink, &record, id) != CompletionCollectAccepted ||
		!SealCompletionBatch(&sink) {
		t.Fatal("collect synchronous early completion")
	}
	if resolution, resolved := ResolveCompletionBatch(&sink); !resolved || resolution.Completed != 1 {
		t.Fatalf("resolve synchronous early completion = (%+v, %t)", resolution, resolved)
	}
}

func TestCompletionBatchWinnerIsIndependentOfFactOrder(t *testing.T) {
	forward := resolveWinnerForOrder(t, 0x13579bdf, []int{0, 1, 2})
	reverse := resolveWinnerForOrder(t, 0x13579bdf, []int{2, 1, 0})
	mixed := resolveWinnerForOrder(t, 0x13579bdf, []int{1, 2, 0})
	if forward != reverse || forward != mixed {
		t.Fatalf("winner depends on fact order: %d %d %d", forward, reverse, mixed)
	}
}

func TestParkCaseRankVariesWinnerAcrossSeeds(t *testing.T) {
	seen := make(map[uint32]bool)
	for seed := uint32(0); seed < 256 && len(seen) != 3; seed++ {
		fixture := newParkV2Fixture(t, seed, []uint32{10, 20, 30})
		publishParkV2(t, fixture, 0, 1, 2)
		resolveParkV2(t, fixture, []int{2, 0, 1}, false)
		caseID, _, ok := ParkWinner(&fixture.state, fixture.ticket)
		if !ok {
			t.Fatal("missing seeded winner")
		}
		seen[caseID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("seeded ranks never selected every candidate: %v", seen)
	}
}

func TestFixedCallerSeedMixesEachLogicalParkGeneration(t *testing.T) {
	var state ParkState
	cases := [...]uint32{10, 20, 30}
	seen := make(map[uint32]bool)
	for iteration := 0; iteration < 256 && len(seen) != len(cases); iteration++ {
		ticket, ok := BeginParkSet(&state, 0, 0x12345678)
		if !ok {
			t.Fatalf("begin logical generation %d", iteration)
		}
		winner := cases[0]
		winnerRank := parkCaseRank(state.seed, winner)
		for _, candidate := range cases[1:] {
			if rank := parkCaseRank(state.seed, candidate); rank < winnerRank {
				winner, winnerRank = candidate, rank
			}
		}
		seen[winner] = true
		if !AbortParkSet(&state, ticket) {
			t.Fatalf("abort logical generation %d", iteration)
		}
		if outcome, _, _, consumed := ConsumeParkSet(&state, ticket); !consumed || outcome != ParkOutcomeCanceled {
			t.Fatalf("consume logical generation %d", iteration)
		}
	}
	if len(seen) != len(cases) {
		t.Fatalf("fixed caller seed permanently biased one generation: %v", seen)
	}
}

func TestCompletionBatchRequiresCompletePublishedSnapshot(t *testing.T) {
	fixture := newParkV2Fixture(t, 7, []uint32{1, 2})
	publishParkV2(t, fixture, 0, 1)
	var sink CompletionSink
	if !BeginCompletionBatch(&sink) || CollectOperationCompletion(&sink, &fixture.records[0], fixture.ids[0]) != CompletionCollectAccepted ||
		!SealCompletionBatch(&sink) {
		t.Fatal("build incomplete completion batch")
	}
	if resolution, ok := ResolveCompletionBatch(&sink); ok || resolution != (CompletionResolution{}) ||
		fixture.state.phase != parkParked || fixture.records[0].disposition != OperationDispositionPending ||
		fixture.records[1].disposition != OperationDispositionPending {
		t.Fatalf("incomplete batch partially resolved: %+v, ok=%t", resolution, ok)
	}
	if !ResetCompletionBatch(&sink) || !BeginCompletionBatch(&sink) {
		t.Fatal("reset incomplete batch")
	}
	for index := range fixture.records {
		if CollectOperationCompletion(&sink, &fixture.records[index], fixture.ids[index]) != CompletionCollectAccepted {
			t.Fatalf("recollect candidate %d", index)
		}
	}
	if !SealCompletionBatch(&sink) {
		t.Fatal("seal complete replay")
	}
	if resolution, ok := ResolveCompletionBatch(&sink); !ok || resolution.WaitSets != 1 || resolution.Winners != 1 {
		t.Fatalf("resolve complete replay = (%+v, %t)", resolution, ok)
	}
}

func TestCompletionBatchRequiresStickyCancelFact(t *testing.T) {
	fixture := newParkV2Fixture(t, 9, []uint32{1})
	if !RequestParkCancel(&fixture.state, fixture.ticket, ParkCancelOperation) {
		t.Fatal("request sticky cancel")
	}
	publishParkV2(t, fixture, 0)
	var sink CompletionSink
	if !BeginCompletionBatch(&sink) || CollectOperationCompletion(&sink, &fixture.records[0], fixture.ids[0]) != CompletionCollectAccepted ||
		!SealCompletionBatch(&sink) {
		t.Fatal("build batch without cancel fact")
	}
	if resolution, ok := ResolveCompletionBatch(&sink); ok || resolution != (CompletionResolution{}) ||
		fixture.state.phase != parkParked || fixture.records[0].disposition != OperationDispositionPending {
		t.Fatalf("missing cancel fact partially resolved: %+v, ok=%t", resolution, ok)
	}
}

func TestPhysicalCancelRequestDoesNotChooseLogicalWinner(t *testing.T) {
	t.Run("cancel-request-before-completion", func(t *testing.T) {
		fixture := newParkV2Fixture(t, 10, []uint32{1})
		if result := RequestPhysicalOperationCancel(&fixture.records[0], fixture.ids[0]); result != OperationCancelRequested {
			t.Fatalf("first physical cancel = %d", result)
		}
		if result := RequestPhysicalOperationCancel(&fixture.records[0], fixture.ids[0]); result != OperationCancelAlreadyRequested {
			t.Fatalf("duplicate physical cancel = %d", result)
		}
		publishParkV2(t, fixture, 0)
		resolution := resolveParkV2(t, fixture, []int{0}, false)
		if resolution.Completed != 1 || ParkOperationClaim(&fixture.records[0], fixture.ids[0]) != ParkClaimWon {
			t.Fatalf("physical request incorrectly chose logical cancel: %+v", resolution)
		}
	})

	t.Run("completion-before-cancel-request", func(t *testing.T) {
		fixture := newParkV2Fixture(t, 12, []uint32{1})
		publishParkV2(t, fixture, 0)
		if result := RequestPhysicalOperationCancel(&fixture.records[0], fixture.ids[0]); result != OperationCancelCompletionPending {
			t.Fatalf("cancel after completion publish = %d", result)
		}
		resolution := resolveParkV2(t, fixture, []int{0}, false)
		if resolution.Completed != 1 || resolution.Canceled != 0 {
			t.Fatalf("published completion lost to physical request: %+v", resolution)
		}
	})
}

func TestParkCancelCompletionRaceAndLateLoser(t *testing.T) {
	t.Run("completion-wins-same-batch", func(t *testing.T) {
		fixture := newParkV2Fixture(t, 11, []uint32{3, 4})
		if !RequestParkCancel(&fixture.state, fixture.ticket, ParkCancelOperation) ||
			!RequestParkCancel(&fixture.state, fixture.ticket, ParkCancelOperation) {
			t.Fatal("park cancellation was not idempotent")
		}
		publishParkV2(t, fixture, 1)
		resolution := resolveParkV2(t, fixture, []int{1}, true)
		if resolution.Completed != 1 || resolution.Canceled != 0 || resolution.Losers != 1 {
			t.Fatalf("completion/cancel resolution = %+v", resolution)
		}
		if ParkOperationClaim(&fixture.records[1], fixture.ids[1]) != ParkClaimWon ||
			ParkOperationClaim(&fixture.records[0], fixture.ids[0]) != ParkClaimLost {
			t.Fatal("completion/cancel claims")
		}
		if result := PublishOperationCompletion(&fixture.records[0], fixture.ids[0]); result != OperationCompletionLost {
			t.Fatalf("late loser completion = %d", result)
		}
	})

	t.Run("task-abort-overrides-completion", func(t *testing.T) {
		fixture := newParkV2Fixture(t, 12, []uint32{7, 8})
		if !RequestParkCancel(&fixture.state, fixture.ticket, ParkCancelOperation) ||
			!RequestParkCancel(&fixture.state, fixture.ticket, ParkCancelTaskAbort) ||
			!RequestParkCancel(&fixture.state, fixture.ticket, ParkCancelOperation) {
			t.Fatal("request or preserve task-abort priority")
		}
		if kind, ok := ParkCancelKindOf(&fixture.state, fixture.ticket); !ok || kind != ParkCancelTaskAbort {
			t.Fatalf("task-abort kind = (%d, %t)", kind, ok)
		}
		publishParkV2(t, fixture, 0)
		resolution := resolveParkV2(t, fixture, []int{0}, true)
		if resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 2}) {
			t.Fatalf("task-abort resolution = %+v", resolution)
		}
		for index := range fixture.records {
			if fixture.records[index].disposition != OperationDispositionCanceled ||
				ParkOperationClaim(&fixture.records[index], fixture.ids[index]) != ParkClaimLost {
				t.Fatalf("task-abort candidate %d was not canceled", index)
			}
		}
	})

	t.Run("cancel-wins-without-completion", func(t *testing.T) {
		fixture := newParkV2Fixture(t, 13, []uint32{5, 6})
		if !RequestParkCancel(&fixture.state, fixture.ticket, ParkCancelOperation) {
			t.Fatal("request park cancel")
		}
		resolution := resolveParkV2(t, fixture, nil, true)
		if resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 2}) {
			t.Fatalf("cancel resolution = %+v", resolution)
		}
		for index := range fixture.records {
			if ParkOperationClaim(&fixture.records[index], fixture.ids[index]) != ParkClaimLost {
				t.Fatalf("canceled candidate %d was not a normal loser", index)
			}
			if result := PublishOperationCompletion(&fixture.records[index], fixture.ids[index]); result != OperationCompletionLost {
				t.Fatalf("late canceled completion %d = %d", index, result)
			}
		}
		detachParkV2(t, fixture, 0, 1)
		outcome, _, lease, ok := ConsumeParkSet(&fixture.state, fixture.ticket)
		if !ok || outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) {
			t.Fatalf("consume cancellation = (%d, %+v, %t)", outcome, lease, ok)
		}
		finishParkV2Operations(t, fixture, OperationResultLease{})
	})
}

func TestDetachBarrierAndPhysicalQuiescenceAreIndependent(t *testing.T) {
	fixture := newParkV2Fixture(t, 17, []uint32{7, 8, 9})
	publishParkV2(t, fixture, 0)
	resolveParkV2(t, fixture, []int{0}, false)
	_, winnerID, ok := ParkWinner(&fixture.state, fixture.ticket)
	if !ok {
		t.Fatal("winner before detach")
	}
	if DetachParkOperation(&fixture.state, fixture.ticket, &fixture.records[0], fixture.ids[0]) {
		t.Fatal("detached before source applied logical resolution")
	}
	disposition, dispositionOK := OperationDispositionOf(&fixture.records[0], fixture.ids[0])
	if !dispositionOK || !AcknowledgeOperationResolution(&fixture.records[0], fixture.ids[0], disposition) ||
		!DetachParkOperation(&fixture.state, fixture.ticket, &fixture.records[0], fixture.ids[0]) || ParkReady(&fixture.state, fixture.ticket) {
		t.Fatal("first detach crossed ready barrier")
	}
	if !ConfirmOperationQuiesced(&fixture.records[0], fixture.ids[0]) || OperationCanRecycle(&fixture.records[0], fixture.ids[0]) {
		t.Fatal("winner result lease did not block early recycle")
	}
	disposition, dispositionOK = OperationDispositionOf(&fixture.records[1], fixture.ids[1])
	if !dispositionOK || !AcknowledgeOperationResolution(&fixture.records[1], fixture.ids[1], disposition) ||
		!DetachParkOperation(&fixture.state, fixture.ticket, &fixture.records[1], fixture.ids[1]) || ParkReady(&fixture.state, fixture.ticket) {
		t.Fatal("second detach crossed ready barrier")
	}
	disposition, dispositionOK = OperationDispositionOf(&fixture.records[2], fixture.ids[2])
	if !dispositionOK || !AcknowledgeOperationResolution(&fixture.records[2], fixture.ids[2], disposition) ||
		!DetachParkOperation(&fixture.state, fixture.ticket, &fixture.records[2], fixture.ids[2]) || !ParkReady(&fixture.state, fixture.ticket) {
		t.Fatal("last detach did not publish ready")
	}
	// Candidates 1 and 2 are deliberately not quiescent: ready depends on
	// pointer-free detach, while slot reuse independently waits for backend ack.
	if OperationCanRecycle(&fixture.records[1], fixture.ids[1]) || OperationCanRecycle(&fixture.records[2], fixture.ids[2]) {
		t.Fatal("unquiesced loser became reusable")
	}
	if _, _, winnerLease, consumed := ConsumeParkSet(&fixture.state, fixture.ticket); !consumed {
		t.Fatal("consume detached winner")
	} else if consumedID, valid := winnerLease.ID(); !valid || consumedID != winnerID {
		t.Fatal("consume detached winner lease")
	}
	for index := 1; index < 3; index++ {
		if !ConfirmOperationQuiesced(&fixture.records[index], fixture.ids[index]) || !OperationCanRecycle(&fixture.records[index], fixture.ids[index]) {
			t.Fatalf("quiesce detached loser %d", index)
		}
	}
}

func TestCompletionBatchResolvesMultipleWaitSets(t *testing.T) {
	left := newParkV2Fixture(t, 21, []uint32{1, 2})
	right := newParkV2Fixture(t, 22, []uint32{3})
	publishParkV2(t, left, 0, 1)
	publishParkV2(t, right, 0)
	var sink CompletionSink
	if !BeginCompletionBatch(&sink) {
		t.Fatal("begin multi-set batch")
	}
	collect := []struct {
		fixture *parkV2Fixture
		index   int
	}{{left, 1}, {right, 0}, {left, 0}}
	for _, item := range collect {
		if CollectOperationCompletion(&sink, &item.fixture.records[item.index], item.fixture.ids[item.index]) != CompletionCollectAccepted {
			t.Fatal("collect multi-set fact")
		}
	}
	if !SealCompletionBatch(&sink) {
		t.Fatal("seal multi-set batch")
	}
	resolution, ok := ResolveCompletionBatch(&sink)
	if !ok || resolution != (CompletionResolution{WaitSets: 2, Completed: 2, Winners: 2, Losers: 1}) {
		t.Fatalf("multi-set resolution = (%+v, %t)", resolution, ok)
	}
}

func TestCompletionBatchInvalidLaterWaitSetDoesNotResolveEarlierSet(t *testing.T) {
	left := newParkV2Fixture(t, 25, []uint32{1})
	right := newParkV2Fixture(t, 26, []uint32{2})
	publishParkV2(t, left, 0)
	publishParkV2(t, right, 0)
	var sink CompletionSink
	if !BeginCompletionBatch(&sink) ||
		CollectOperationCompletion(&sink, &left.records[0], left.ids[0]) != CompletionCollectAccepted ||
		CollectOperationCompletion(&sink, &right.records[0], right.ids[0]) != CompletionCollectAccepted ||
		!SealCompletionBatch(&sink) {
		t.Fatal("build multi-set validation batch")
	}
	right.records[0].completionPublished = false
	if resolution, ok := ResolveCompletionBatch(&sink); ok || resolution != (CompletionResolution{}) ||
		left.state.phase != parkParked || left.records[0].disposition != OperationDispositionPending ||
		right.state.phase != parkParked || right.records[0].disposition != OperationDispositionPending {
		t.Fatalf("invalid later set partially resolved batch: %+v, ok=%t", resolution, ok)
	}
}

func TestParkSetRejectsUnrepresentableOperationSnapshot(t *testing.T) {
	if ticket, ok := BeginParkSet(new(ParkState), CompletionSinkOperationCapacity+1, 23); ok || ticket != (ParkTicket{}) {
		t.Fatalf("oversized park-set = (%+v, %t)", ticket, ok)
	}
}

func TestCompletionSinkMergesCancelWithReadyFact(t *testing.T) {
	fixture := newParkV2Fixture(t, 24, []uint32{1})
	if !RequestParkCancel(&fixture.state, fixture.ticket, ParkCancelOperation) {
		t.Fatal("request merged cancellation")
	}
	publishParkV2(t, fixture, 0)
	var sink CompletionSink
	if !BeginCompletionBatch(&sink) {
		t.Fatal("begin merged batch")
	}
	if CollectParkCancellation(&sink, &fixture.state, fixture.ticket) != CompletionCollectAccepted || sink.count != 1 ||
		sink.cancelOnlyFacts != 1 || CollectOperationCompletion(&sink, &fixture.records[0], fixture.ids[0]) != CompletionCollectAccepted ||
		sink.count != 1 || sink.cancelOnlyFacts != 0 || sink.operationFacts != 1 {
		t.Fatalf("cancel/ready merge: count=%d operations=%d cancels=%d", sink.count, sink.operationFacts, sink.cancelOnlyFacts)
	}
	if !SealCompletionBatch(&sink) {
		t.Fatal("seal merged batch")
	}
	if resolution, ok := ResolveCompletionBatch(&sink); !ok || resolution.Completed != 1 || resolution.Canceled != 0 {
		t.Fatalf("resolve merged batch = (%+v, %t)", resolution, ok)
	}
}

func TestCompletionSinkCatalogBoundIncludesCompletionAndCancel(t *testing.T) {
	states := make([]ParkState, CompletionSinkOperationCapacity)
	records := make([]OperationRecord, len(states))
	var sink CompletionSink
	if !BeginCompletionBatch(&sink) {
		t.Fatal("begin catalog-bound batch")
	}
	for index := range states {
		ticket, ok := BeginParkSet(&states[index], 1, uint32(index+1))
		id, idOK := MakeOperationID(OperationSourceManual, uint32(index+1), 1)
		if !ok || !idOK || !InitOperation(&records[index], id) ||
			!AttachParkOperation(&states[index], ticket, &records[index], uint32(index)) ||
			!SealParkSet(&states[index], ticket) || !CommitParkSet(&states[index], ticket) ||
			!RequestParkCancel(&states[index], ticket, ParkCancelOperation) || PublishOperationCompletion(&records[index], id) != OperationCompletionPublished {
			t.Fatalf("prepare catalog-bound wait-set %d", index)
		}
		if CollectParkCancellation(&sink, &states[index], ticket) != CompletionCollectAccepted ||
			CollectOperationCompletion(&sink, &records[index], id) != CompletionCollectAccepted {
			t.Fatalf("collect catalog-bound wait-set %d", index)
		}
	}
	if sink.count != CompletionSinkOperationCapacity || sink.operationFacts != CompletionSinkOperationCapacity ||
		sink.cancelOnlyFacts != 0 || sink.overflow || !SealCompletionBatch(&sink) {
		t.Fatalf("catalog-bound counts: total=%d operations=%d cancels=%d overflow=%t", sink.count, sink.operationFacts, sink.cancelOnlyFacts, sink.overflow)
	}
	resolution, ok := ResolveCompletionBatch(&sink)
	if !ok || resolution.WaitSets != CompletionSinkOperationCapacity || resolution.Completed != CompletionSinkOperationCapacity ||
		resolution.Canceled != 0 || resolution.Winners != CompletionSinkOperationCapacity {
		t.Fatalf("catalog-bound resolution = (%+v, %t)", resolution, ok)
	}
}

func TestCompletionSinkCancelAdmissionOverflowDoesNotResolve(t *testing.T) {
	states := make([]ParkState, CompletionSinkCancelOnlyCapacity+1)
	tickets := make([]ParkTicket, len(states))
	var sink CompletionSink
	if !BeginCompletionBatch(&sink) {
		t.Fatal("begin cancel overflow batch")
	}
	for index := range states {
		ticket, ok := BeginParkSet(&states[index], 0, uint32(index+1))
		if !ok || !SealParkSet(&states[index], ticket) || !CommitParkSet(&states[index], ticket) ||
			!RequestParkCancel(&states[index], ticket, ParkCancelOperation) {
			t.Fatalf("prepare cancel-only wait-set %d", index)
		}
		tickets[index] = ticket
		result := CollectParkCancellation(&sink, &states[index], ticket)
		if index < CompletionSinkCancelOnlyCapacity {
			if result != CompletionCollectAccepted {
				t.Fatalf("collect cancel %d = %d", index, result)
			}
		} else if result != CompletionCollectOverflow {
			t.Fatalf("cancel overflow = %d", result)
		}
	}
	if SealCompletionBatch(&sink) {
		t.Fatal("sealed overflowed cancel batch")
	}
	for index := range states {
		if states[index].phase != parkParked || states[index].outcome != ParkOutcomePending {
			t.Fatalf("overflow resolved cancel wait-set %d", index)
		}
	}
	if !ResetCompletionBatch(&sink) {
		t.Fatal("reset cancel overflow")
	}
}

func TestCompletionSinkCorruptCountsFailClosed(t *testing.T) {
	sink := CompletionSink{
		phase:           completionSinkCollecting,
		count:           CompletionSinkCapacity + 1,
		operationFacts:  CompletionSinkOperationCapacity,
		cancelOnlyFacts: CompletionSinkCancelOnlyCapacity,
	}
	if SealCompletionBatch(&sink) || ResetCompletionBatch(&sink) {
		t.Fatal("accepted corrupt completion count")
	}
	if resolution, ok := ResolveCompletionBatch(&sink); ok || resolution != (CompletionResolution{}) {
		t.Fatalf("resolved corrupt completion count = (%+v, %t)", resolution, ok)
	}
	idle := CompletionSink{count: 1, operationFacts: 1}
	if BeginCompletionBatch(&idle) {
		t.Fatal("accepted non-zero idle sink")
	}
}

func TestLogicalTicketCarriesIntoNextEpochWithoutAliasing(t *testing.T) {
	stale := ParkTicket{epoch: 41, generation: ^uint32(0)}
	state := ParkState{
		ticket:     stale,
		phase:      parkConsumed,
		expected:   1,
		cancelKind: ParkCancelOperation,
		outcome:    ParkOutcomeCanceled,
	}
	if !validParkState(&state) {
		t.Fatal("synthetic consumed max generation invalid")
	}
	ticket, ok := BeginParkSet(&state, 1, 1)
	want := ParkTicket{epoch: 42, generation: 1}
	if !ok || ticket != want {
		t.Fatalf("logical epoch carry = (%+v, %t), want %+v", ticket, ok, want)
	}
	if RequestParkCancel(&state, stale, ParkCancelOperation) {
		t.Fatal("stale prior-epoch ticket accepted")
	}
}

func TestLogicalTicketExhaustionFailsClosed(t *testing.T) {
	state := ParkState{
		ticket:     ParkTicket{epoch: ^uint32(0), generation: ^uint32(0)},
		phase:      parkConsumed,
		cancelKind: ParkCancelOperation,
		outcome:    ParkOutcomeCanceled,
	}
	before := state
	if !validParkState(&state) {
		t.Fatal("synthetic exhausted ticket state invalid")
	}
	if ticket, ok := BeginParkSet(&state, 1, 1); ok || ticket != (ParkTicket{}) || state != before {
		t.Fatalf("logical ticket exhaustion = (%+v, %t), state=%+v", ticket, ok, state)
	}
}

func TestPhysicalOperationGenerationRejectsStaleIDAfterRecycle(t *testing.T) {
	fixture := newParkV2Fixture(t, 31, []uint32{1})
	publishParkV2(t, fixture, 0)
	resolveParkV2(t, fixture, []int{0}, false)
	detachParkV2(t, fixture, 0)
	oldID := fixture.ids[0]
	if TakeOperationResult(&fixture.records[0], OperationResultLease{id: oldID, ticket: fixture.ticket}) {
		t.Fatal("winner result taken before G consumed park")
	}
	_, _, winnerLease, consumed := ConsumeParkSet(&fixture.state, fixture.ticket)
	if !consumed {
		t.Fatal("consume first physical generation")
	}
	if !ConfirmOperationQuiesced(&fixture.records[0], oldID) || !TakeOperationResult(&fixture.records[0], winnerLease) ||
		!RecycleOperation(&fixture.records[0], oldID) {
		t.Fatal("recycle first physical generation")
	}
	if InitOperation(&fixture.records[0], oldID) {
		t.Fatal("reinitialized recycled operation with stale ID")
	}
	newID, ok := RearmOperation(&fixture.records[0])
	if !ok || newID.Generation != oldID.Generation+1 {
		t.Fatal("initialize next physical generation")
	}
	if PublishOperationCompletion(&fixture.records[0], oldID) != OperationCompletionInvalid || fixture.records[0].Matches(oldID) ||
		PublishOperationCompletion(&fixture.records[0], newID) != OperationCompletionInvalid {
		t.Fatal("stale physical generation reached reused record")
	}
	nextState := new(ParkState)
	nextTicket, ok := BeginParkSet(nextState, 1, 32)
	if !ok || !AttachParkOperation(nextState, nextTicket, &fixture.records[0], 2) ||
		!SealParkSet(nextState, nextTicket) || !CommitParkSet(nextState, nextTicket) ||
		PublishOperationCompletion(&fixture.records[0], newID) != OperationCompletionPublished {
		t.Fatal("arm next physical generation")
	}
}
