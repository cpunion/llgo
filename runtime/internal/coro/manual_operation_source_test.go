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

func reserveManualWaitSet(t *testing.T, source *ManualOperationSource, p *P, seed uint32, cases []uint32) (*ParkState, ParkTicket, []OperationID) {
	t.Helper()
	state := new(ParkState)
	ticket, ok := BeginParkSet(state, uint32(len(cases)), seed)
	if !ok {
		t.Fatal("begin manual wait-set")
	}
	ids := make([]OperationID, len(cases))
	for index, caseID := range cases {
		id, reserved := source.ReserveAndAttach(p, state, ticket, caseID)
		if !reserved {
			t.Fatalf("reserve manual operation %d", index)
		}
		ids[index] = id
	}
	if !SealParkSet(state, ticket) || !CommitParkSet(state, ticket) {
		t.Fatal("commit manual wait-set")
	}
	return state, ticket, ids
}

func finishManualOperations(t *testing.T, source *ManualOperationSource, p *P, ids []OperationID, lease OperationResultLease) {
	t.Helper()
	winnerID, hasWinner := lease.ID()
	for _, id := range ids {
		if !source.ConfirmQuiesced(p, id) {
			t.Fatalf("confirm manual operation quiesced: %+v", id)
		}
	}
	if hasWinner && !source.TakeResult(p, lease) {
		t.Fatalf("take manual winner result: %+v", winnerID)
	}
	for _, id := range ids {
		if !source.Recycle(p, id) {
			t.Fatalf("recycle manual operation: %+v", id)
		}
	}
}

func TestManualOperationSourceAffectedResolveAndUnpublishedLoserDetach(t *testing.T) {
	p := new(P)
	source := new(ManualOperationSource)
	if !BindManualOperationSource(source, p) {
		t.Fatal("bind manual source")
	}
	state, ticket, ids := reserveManualWaitSet(t, source, p, 41, []uint32{10, 20, 30})

	if result := source.Post(ids[0]); result != ManualOperationPosted {
		t.Fatalf("post first manual operation = %d", result)
	}
	if result := source.Post(ids[1]); result != ManualOperationPosted {
		t.Fatalf("post second manual operation = %d", result)
	}
	if result := source.Post(ids[0]); result != ManualOperationPostDuplicate {
		t.Fatalf("duplicate manual post = %d", result)
	}
	if !source.Pending() {
		t.Fatal("manual source lost pending doorbell")
	}
	published, lost, ok := source.PublishPass(p)
	if !ok || published != 2 || lost != 0 || source.Pending() {
		t.Fatalf("manual publish pass = (%d, %d, %t), pending=%t", published, lost, ok, source.Pending())
	}
	if state.phase != parkParked || state.outcome != ParkOutcomePending {
		t.Fatalf("manual publish resolved before published epoch: phase=%d outcome=%d", state.phase, state.outcome)
	}

	resolution, duplicates, ok := source.ResolveAffectedPublishedEpoch(p)
	wantResolution := CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1, Losers: 2}
	if !ok || resolution != wantResolution || duplicates != 1 {
		t.Fatalf("manual affected resolve = (%+v, duplicates=%d, %t), want %+v", resolution, duplicates, ok, wantResolution)
	}
	applied, detached, ok := source.ApplyAndDetach(p)
	if !ok || applied != 3 || detached != 3 || !ParkReady(state, ticket) {
		t.Fatalf("manual apply/detach = (%d, %d, %t), ready=%t", applied, detached, ok, ParkReady(state, ticket))
	}
	// ids[2] never posted and therefore was never in the affected chain. The
	// all-live-slot apply pass must still close, acknowledge, and detach it.
	thirdSlot, _ := manualOperationSlotFor(source, ids[2])
	if operationCandidateIsPublished(&thirdSlot.record) || thirdSlot.record.phase != operationDetached ||
		thirdSlot.record.disposition != OperationDispositionLost || !thirdSlot.record.resolutionApplied ||
		preemptLoad(&thirdSlot.state) != uint32(manualOperationClosing) {
		t.Fatal("unpublished select loser was not detached by source apply pass")
	}

	winnerCase, winnerID, winnerOK := ParkWinner(state, ticket)
	if !winnerOK {
		t.Fatal("missing manual winner")
	}
	outcome, consumedCase, lease, consumed := ConsumeParkSet(state, ticket)
	leaseID, leaseOK := lease.ID()
	if !consumed || outcome != ParkOutcomeCompleted || consumedCase != winnerCase || !leaseOK || leaseID != winnerID {
		t.Fatalf("consume manual winner = (%d, %d, %+v, %t)", outcome, consumedCase, lease, consumed)
	}
	finishManualOperations(t, source, p, ids, lease)

	// Reuse must advance the exact physical generation; a copied old producer
	// ID cannot publish into the new operation.
	nextState, nextTicket, nextIDs := reserveManualWaitSet(t, source, p, 42, []uint32{40})
	if nextIDs[0].Slot() != ids[0].Slot() || nextIDs[0].Generation == ids[0].Generation {
		t.Fatalf("manual generation did not advance: old=%+v next=%+v", ids[0], nextIDs[0])
	}
	if result := source.Post(ids[0]); result != ManualOperationPostStale || source.Pending() {
		t.Fatalf("stale manual post = %d, pending=%t", result, source.Pending())
	}
	if !RequestParkCancel(nextState, nextTicket, ParkCancelOperation) {
		t.Fatal("cancel next manual wait-set")
	}
	cancelResolution, cancelOK := ResolveParkSnapshot(nextState, nextTicket)
	if !cancelOK || cancelResolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 1}) {
		t.Fatalf("resolve next manual cancellation = (%+v, %t)", cancelResolution, cancelOK)
	}
	if applied, detached, ok = source.ApplyAndDetach(p); !ok || applied != 1 || detached != 1 {
		t.Fatalf("apply next manual cancellation = (%d, %d, %t)", applied, detached, ok)
	}
	if outcome, _, lease, consumed = ConsumeParkSet(nextState, nextTicket); !consumed || outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) {
		t.Fatalf("consume next manual cancellation = (%d, %+v, %t)", outcome, lease, consumed)
	}
	finishManualOperations(t, source, p, nextIDs, OperationResultLease{})
	if !UnbindManualOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release manual source")
	}
}

func TestManualOperationSourceApplyOneRequiresExactGenerationAndRecord(t *testing.T) {
	p := new(P)
	source := new(ManualOperationSource)
	if !BindManualOperationSource(source, p) {
		t.Fatal("bind exact-apply manual source")
	}
	state, ticket, ids := reserveManualWaitSet(t, source, p, 47, []uint32{11})
	id := ids[0]
	slot, _ := manualOperationSlotFor(source, id)
	if result := source.Post(id); result != ManualOperationPosted {
		t.Fatalf("post exact-apply completion = %d", result)
	}
	if published, lost, ok := source.PublishPass(p); !ok || published != 1 || lost != 0 {
		t.Fatalf("publish exact-apply completion = (%d, %d, %t)", published, lost, ok)
	}
	if resolution, duplicates, ok := source.ResolveAffectedPublishedEpoch(p); !ok || duplicates != 0 ||
		resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}) {
		t.Fatalf("resolve exact-apply completion = (%+v, %d, %t)", resolution, duplicates, ok)
	}

	wrongGeneration := id
	wrongGeneration.Generation++
	if result := source.ApplyOne(p, wrongGeneration, &slot.record); result != OperationApplyInvalid {
		t.Fatalf("wrong-generation apply = %d", result)
	}
	copyRecord := slot.record
	if result := source.ApplyOne(p, id, &copyRecord); result != OperationApplyInvalid {
		t.Fatalf("copied-record apply = %d", result)
	}
	if slot.record.phase != operationActive || slot.record.resolutionApplied ||
		preemptLoad(&slot.state) != uint32(manualOperationActive) {
		t.Fatal("invalid exact apply changed live operation")
	}
	if result := source.ApplyOne(p, id, &slot.record); result != OperationApplyDetached ||
		!ParkReady(state, ticket) || slot.record.phase != operationDetached || !slot.record.resolutionApplied ||
		preemptLoad(&slot.state) != uint32(manualOperationClosing) {
		t.Fatalf("exact manual apply = %d", result)
	}
	if result := source.ApplyOne(p, id, &slot.record); result != OperationApplyInvalid {
		t.Fatalf("duplicate detached apply = %d", result)
	}
	outcome, _, lease, consumed := ConsumeParkSet(state, ticket)
	if !consumed || outcome != ParkOutcomeCompleted || !lease.Valid() {
		t.Fatalf("consume exact-apply winner = (%d, %+v, %t)", outcome, lease, consumed)
	}
	if !source.ConfirmQuiesced(p, id) || source.Recycle(p, id) ||
		!source.DiscardResult(p, lease) || source.TakeResult(p, lease) || !source.Recycle(p, id) {
		t.Fatal("discard exact manual winner lease")
	}
	if !UnbindManualOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release exact-apply manual source")
	}
}

func TestManualOperationSourceLateAdmittedLoserRequiresDrainBeforeQuiescence(t *testing.T) {
	p := new(P)
	source := new(ManualOperationSource)
	if !BindManualOperationSource(source, p) {
		t.Fatal("bind manual source")
	}
	state, ticket, ids := reserveManualWaitSet(t, source, p, 51, []uint32{1})
	id := ids[0]
	slot, _ := manualOperationSlotFor(source, id)

	// Model a producer which entered and observed the active generation before
	// owner close, but was descheduled before publishing its mailbox.
	if !manualOperationAcquireProducer(slot) || preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.state) != uint32(manualOperationActive) {
		t.Fatal("admit manual producer")
	}
	if !RequestParkCancel(state, ticket, ParkCancelOperation) {
		t.Fatal("request manual cancellation")
	}
	resolution, resolved := ResolveParkSnapshot(state, ticket)
	if !resolved || resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 1}) {
		t.Fatalf("resolve manual cancellation = (%+v, %t)", resolution, resolved)
	}
	if applied, detached, ok := source.ApplyAndDetach(p); !ok || applied != 1 || detached != 1 {
		t.Fatalf("apply manual cancellation = (%d, %d, %t)", applied, detached, ok)
	}
	if source.ConfirmQuiesced(p, id) {
		t.Fatal("manual source quiesced with admitted producer")
	}
	if result := source.Post(id); result != ManualOperationPostClosed {
		t.Fatalf("new post entered closed manual source = %d", result)
	}

	// The admitted producer may finish after logical detach. Its mailbox is still
	// durable, but owner publication classifies it as a normal late loser.
	if !preemptCompareAndSwap(&slot.mailbox, uint32(manualOperationMailboxEmpty), uint32(manualOperationMailboxPosting)) {
		t.Fatal("publish late manual mailbox")
	}
	preemptStore(&slot.mailbox, uint32(manualOperationMailboxPosted))
	preemptStore(&source.pending, 1)
	manualOperationReleaseProducer(slot)
	if source.ConfirmQuiesced(p, id) {
		t.Fatal("manual source quiesced before final mailbox drain")
	}
	if published, lost, ok := source.PublishPass(p); !ok || published != 0 || lost != 1 {
		t.Fatalf("drain late manual loser = (%d, %d, %t)", published, lost, ok)
	}
	if !source.ConfirmQuiesced(p, id) {
		t.Fatal("confirm manual source after strong join and final drain")
	}
	if outcome, _, lease, consumed := ConsumeParkSet(state, ticket); !consumed || outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) {
		t.Fatalf("consume late-loser cancellation = (%d, %+v, %t)", outcome, lease, consumed)
	}
	if !source.Recycle(p, id) || !UnbindManualOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("recycle late manual loser")
	}
}

func TestManualOperationSourceConcurrentProducerCoalescing(t *testing.T) {
	p := new(P)
	source := new(ManualOperationSource)
	if !BindManualOperationSource(source, p) {
		t.Fatal("bind manual source")
	}
	state, ticket, ids := reserveManualWaitSet(t, source, p, 61, []uint32{7})
	id := ids[0]

	const producers = 32
	results := make(chan ManualOperationPostResult, producers)
	var group sync.WaitGroup
	group.Add(producers)
	for index := 0; index < producers; index++ {
		go func() {
			defer group.Done()
			results <- source.Post(id)
		}()
	}
	group.Wait()
	close(results)
	posted, duplicate := 0, 0
	for result := range results {
		switch result {
		case ManualOperationPosted:
			posted++
		case ManualOperationPostDuplicate:
			duplicate++
		default:
			t.Fatalf("concurrent manual post = %d", result)
		}
	}
	if posted != 1 || duplicate != producers-1 {
		t.Fatalf("concurrent manual posts = (posted=%d duplicate=%d)", posted, duplicate)
	}
	if published, lost, ok := source.PublishPass(p); !ok || published != 1 || lost != 0 {
		t.Fatalf("publish concurrent manual post = (%d, %d, %t)", published, lost, ok)
	}
	resolution, duplicates, ok := source.ResolveAffectedPublishedEpoch(p)
	if !ok || duplicates != 0 || resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}) {
		t.Fatalf("resolve concurrent manual post = (%+v, %d, %t)", resolution, duplicates, ok)
	}
	if applied, detached, ok := source.ApplyAndDetach(p); !ok || applied != 1 || detached != 1 || !ParkReady(state, ticket) {
		t.Fatalf("apply concurrent manual post = (%d, %d, %t)", applied, detached, ok)
	}
	outcome, _, lease, consumed := ConsumeParkSet(state, ticket)
	if !consumed || outcome != ParkOutcomeCompleted {
		t.Fatalf("consume concurrent manual post = (%d, %+v, %t)", outcome, lease, consumed)
	}
	finishManualOperations(t, source, p, ids, lease)
	if !UnbindManualOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release concurrent manual source")
	}
}

func TestManualOperationSourceProducerPrefixIsAlignedPOD(t *testing.T) {
	if unsafe.Offsetof(manualOperationSlot{}.state)%4 != 0 ||
		unsafe.Offsetof(manualOperationSlot{}.generation)%4 != 0 ||
		unsafe.Offsetof(manualOperationSlot{}.inflight)%4 != 0 ||
		unsafe.Offsetof(manualOperationSlot{}.mailbox)%4 != 0 ||
		unsafe.Offsetof(manualOperationSlot{}.record) < 4*unsafe.Sizeof(uint32(0)) {
		t.Fatalf("manual operation producer prefix layout: state=%d generation=%d inflight=%d mailbox=%d record=%d",
			unsafe.Offsetof(manualOperationSlot{}.state), unsafe.Offsetof(manualOperationSlot{}.generation),
			unsafe.Offsetof(manualOperationSlot{}.inflight), unsafe.Offsetof(manualOperationSlot{}.mailbox),
			unsafe.Offsetof(manualOperationSlot{}.record))
	}
}
