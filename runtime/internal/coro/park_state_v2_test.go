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

func resolveParkV2(t *testing.T, fixture *parkV2Fixture) CompletionResolution {
	t.Helper()
	resolution, ok := ResolveParkSnapshot(&fixture.state, fixture.ticket)
	if !ok || resolution.Completed+resolution.Canceled != 1 {
		t.Fatalf("resolve park snapshot = (%+v, %t)", resolution, ok)
	}
	return resolution
}

func detachParkV2(t *testing.T, fixture *parkV2Fixture, order ...int) {
	t.Helper()
	for position, index := range order {
		disposition, ok := OperationDispositionOf(&fixture.records[index], fixture.ids[index])
		if !ok {
			t.Fatalf("read candidate %d resolution", index)
		}
		discardUnselectedTestResult(t, &fixture.records[index], fixture.ids[index])
		if !AcknowledgeOperationResolution(&fixture.records[index], fixture.ids[index], disposition) {
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
	publishParkV2(t, fixture, order...)
	resolution := resolveParkV2(t, fixture)
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
	resolution, resolved := ResolveParkSnapshot(&state, ticket)
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
		if !dispositionOK || disposition != OperationDispositionCanceled {
			t.Fatalf("read aborted operation %d disposition", index)
		}
		discardUnselectedTestResult(t, &records[index], ids[index])
		if !AcknowledgeOperationResolution(&records[index], ids[index], disposition) ||
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

func TestDuplicateCaseSealFailureRemainsAbortable(t *testing.T) {
	var g G
	if !InitG(&g) {
		t.Fatal("initialize duplicate-case G")
	}
	ticket, ok := BeginParkSet(&g.park, 2, 0x91)
	var wait WaitSetRecord
	if !ok || !PrepareWaitSetRecord(&wait, &g, ticket) {
		t.Fatal("prepare duplicate-case wait-set")
	}
	var records [2]OperationRecord
	var ids [2]OperationID
	for index := range records {
		id, idOK := MakeOperationID(OperationSourceHost, uint32(index+1), 1)
		if !idOK || !InitOperation(&records[index], id) ||
			!AttachParkWaitOperation(&g.park, ticket, &wait, &records[index], 7) {
			t.Fatalf("attach duplicate case %d", index)
		}
		ids[index] = id
	}
	beforeSeed := g.park.seed
	if SealParkSet(&g.park, ticket) || g.park.phase != parkPreparing || g.park.seed != beforeSeed || !validParkState(&g.park) {
		t.Fatal("duplicate case Seal did not remain valid and abortable")
	}
	if !AbortParkSet(&g.park, ticket) {
		t.Fatal("abort duplicate-case preparation")
	}
	for index := range records {
		discardUnselectedTestResult(t, &records[index], ids[index])
		if !AcknowledgeOperationResolution(&records[index], ids[index], OperationDispositionCanceled) ||
			!DetachParkWaitOperation(&g.park, ticket, &records[index], ids[index]) {
			t.Fatalf("detach duplicate case %d", index)
		}
	}
	if !ParkReady(&g.park, ticket) {
		t.Fatal("duplicate-case abort did not cross detach barrier")
	}
	if outcome, _, lease, consumed := ConsumeParkSet(&g.park, ticket); !consumed ||
		outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) {
		t.Fatalf("consume duplicate-case abort = (%d, %+v, %t)", outcome, lease, consumed)
	}
	if !ReleasePreparedWaitSetRecord(&wait) {
		t.Fatal("release duplicate-case wait-set record")
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
	discardUnselectedTestResult(t, &first, firstID)
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
	if resolution, resolved := ResolveParkSnapshot(&state, ticket); !resolved || resolution.Completed != 1 {
		t.Fatalf("resolve synchronous early completion = (%+v, %t)", resolution, resolved)
	}
}

func TestParkSnapshotWinnerIsIndependentOfPublicationOrder(t *testing.T) {
	forward := resolveWinnerForOrder(t, 0x13579bdf, []int{0, 1, 2})
	reverse := resolveWinnerForOrder(t, 0x13579bdf, []int{2, 1, 0})
	mixed := resolveWinnerForOrder(t, 0x13579bdf, []int{1, 2, 0})
	if forward != reverse || forward != mixed {
		t.Fatalf("winner depends on publication order: %d %d %d", forward, reverse, mixed)
	}
}

func TestParkCaseRankVariesWinnerAcrossSeeds(t *testing.T) {
	seen := make(map[uint32]bool)
	for seed := uint32(0); seed < 256 && len(seen) != 3; seed++ {
		fixture := newParkV2Fixture(t, seed, []uint32{10, 20, 30})
		publishParkV2(t, fixture, 0, 1, 2)
		resolveParkV2(t, fixture)
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

func TestParkSnapshotWithoutCompletionRemainsPending(t *testing.T) {
	fixture := newParkV2Fixture(t, 7, []uint32{1, 2})
	resolution, ok := ResolveParkSnapshot(&fixture.state, fixture.ticket)
	if !ok || resolution != (CompletionResolution{WaitSets: 1}) ||
		fixture.state.phase != parkParked || fixture.records[0].disposition != OperationDispositionPending ||
		fixture.records[1].disposition != OperationDispositionPending {
		t.Fatalf("pending snapshot = (%+v, %t), phase=%d", resolution, ok, fixture.state.phase)
	}
	publishParkV2(t, fixture, 1)
	resolution, ok = ResolveParkSnapshot(&fixture.state, fixture.ticket)
	if !ok || resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1, Losers: 1}) {
		t.Fatalf("completed snapshot = (%+v, %t)", resolution, ok)
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
		resolution := resolveParkV2(t, fixture)
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
		resolution := resolveParkV2(t, fixture)
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
		resolution := resolveParkV2(t, fixture)
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
		resolution := resolveParkV2(t, fixture)
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
		resolution := resolveParkV2(t, fixture)
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
	resolveParkV2(t, fixture)
	_, winnerID, ok := ParkWinner(&fixture.state, fixture.ticket)
	if !ok {
		t.Fatal("winner before detach")
	}
	if DetachParkOperation(&fixture.state, fixture.ticket, &fixture.records[0], fixture.ids[0]) {
		t.Fatal("detached before source applied logical resolution")
	}
	disposition, dispositionOK := OperationDispositionOf(&fixture.records[0], fixture.ids[0])
	if !dispositionOK {
		t.Fatal("read first detach disposition")
	}
	discardUnselectedTestResult(t, &fixture.records[0], fixture.ids[0])
	if !AcknowledgeOperationResolution(&fixture.records[0], fixture.ids[0], disposition) ||
		!DetachParkOperation(&fixture.state, fixture.ticket, &fixture.records[0], fixture.ids[0]) || ParkReady(&fixture.state, fixture.ticket) {
		t.Fatal("first detach crossed ready barrier")
	}
	if !ConfirmOperationQuiesced(&fixture.records[0], fixture.ids[0]) || OperationCanRecycle(&fixture.records[0], fixture.ids[0]) {
		t.Fatal("winner result lease did not block early recycle")
	}
	disposition, dispositionOK = OperationDispositionOf(&fixture.records[1], fixture.ids[1])
	if !dispositionOK {
		t.Fatal("read second detach disposition")
	}
	discardUnselectedTestResult(t, &fixture.records[1], fixture.ids[1])
	if !AcknowledgeOperationResolution(&fixture.records[1], fixture.ids[1], disposition) ||
		!DetachParkOperation(&fixture.state, fixture.ticket, &fixture.records[1], fixture.ids[1]) || ParkReady(&fixture.state, fixture.ticket) {
		t.Fatal("second detach crossed ready barrier")
	}
	disposition, dispositionOK = OperationDispositionOf(&fixture.records[2], fixture.ids[2])
	if !dispositionOK {
		t.Fatal("read final detach disposition")
	}
	discardUnselectedTestResult(t, &fixture.records[2], fixture.ids[2])
	if !AcknowledgeOperationResolution(&fixture.records[2], fixture.ids[2], disposition) ||
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

func TestParkSnapshotsResolveIndependentlyWithoutBatchStorage(t *testing.T) {
	left := newParkV2Fixture(t, 21, []uint32{1, 2})
	right := newParkV2Fixture(t, 22, []uint32{3})
	publishParkV2(t, left, 0, 1)
	publishParkV2(t, right, 0)
	leftResolution, leftOK := ResolveParkSnapshot(&left.state, left.ticket)
	rightResolution, rightOK := ResolveParkSnapshot(&right.state, right.ticket)
	resolution := CompletionResolution{
		WaitSets:  leftResolution.WaitSets + rightResolution.WaitSets,
		Completed: leftResolution.Completed + rightResolution.Completed,
		Canceled:  leftResolution.Canceled + rightResolution.Canceled,
		Winners:   leftResolution.Winners + rightResolution.Winners,
		Losers:    leftResolution.Losers + rightResolution.Losers,
	}
	if !leftOK || !rightOK || resolution != (CompletionResolution{WaitSets: 2, Completed: 2, Winners: 2, Losers: 1}) {
		t.Fatalf("independent snapshot resolution = (%+v, %t, %t)", resolution, leftOK, rightOK)
	}
}

func TestParkSetMatchesGoSelectCaseLimit(t *testing.T) {
	var state ParkState
	ticket, ok := BeginParkSet(&state, MaxSelectOperationCases, 23)
	if !ok || !AbortParkSet(&state, ticket) || !ParkReady(&state, ticket) {
		t.Fatalf("maximum logical wait-set preparation = (%+v, %t)", ticket, ok)
	}
	if outcome, _, _, consumed := ConsumeParkSet(&state, ticket); !consumed || outcome != ParkOutcomeCanceled {
		t.Fatal("consume maximum aborted wait-set")
	}
	before := state
	if rejected, accepted := BeginParkSet(&state, MaxSelectOperationCases+1, 24); accepted || rejected != (ParkTicket{}) || state != before {
		t.Fatal("accepted more than Go's select case limit")
	}

	var defaultState ParkState
	defaultTicket, defaultOK := BeginParkSetWithDefault(&defaultState, MaxSelectOperationCases, 25, 1)
	if !defaultOK || !AbortParkSet(&defaultState, defaultTicket) {
		t.Fatal("rejected maximum operation set plus compiler default")
	}
	var tooManyDefault ParkState
	if rejected, accepted := BeginParkSetWithDefault(&tooManyDefault, MaxSelectOperationCases+1, 26, 1); accepted ||
		rejected != (ParkTicket{}) || tooManyDefault != (ParkState{}) {
		t.Fatal("accepted default with too many physical operations")
	}
	fullTicket, fullOK := BeginParkSet(&tooManyDefault, MaxSelectOperationCases, 27)
	if !fullOK || !SetParkDefault(&tooManyDefault, fullTicket, 1) || !AbortParkSet(&tooManyDefault, fullTicket) {
		t.Fatal("rejected compiler default beside a full operation set")
	}
}

func TestParkSnapshotRejectsStaleTicketWithoutMutation(t *testing.T) {
	fixture := newParkV2Fixture(t, 25, []uint32{1})
	publishParkV2(t, fixture, 0)
	before := fixture.state
	stale := ParkTicket{epoch: fixture.ticket.epoch, generation: fixture.ticket.generation + 1}
	if resolution, ok := ResolveParkSnapshot(&fixture.state, stale); ok || resolution != (CompletionResolution{}) ||
		fixture.state != before || fixture.records[0].disposition != OperationDispositionPending {
		t.Fatalf("stale snapshot resolve = (%+v, %t)", resolution, ok)
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
	resolveParkV2(t, fixture)
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
