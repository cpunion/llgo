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

const affectedTestSourceCapacity = 2

// affectedTestSource models the smallest intended source-side shape. The
// source owns stable OperationRecords and an intrusive one-based affected
// chain in its own slots. There is no per-G link, central hash, or separately
// capacity-limited fact buffer; publication itself guarantees one enqueue per
// operation generation.
type affectedTestSourceSlot struct {
	record       OperationRecord
	id           OperationID
	nextAffected uint32
}

type affectedTestSource struct {
	slots        [affectedTestSourceCapacity]affectedTestSourceSlot
	affectedHead uint32
	affectedTail uint32
}

func (source *affectedTestSource) attach(state *ParkState, ticket ParkTicket, index int, id OperationID, caseID uint32) bool {
	if source == nil || index < 0 || index >= len(source.slots) {
		return false
	}
	slot := &source.slots[index]
	if slot.id != (OperationID{}) || slot.nextAffected != 0 ||
		!InitOperation(&slot.record, id) || !AttachParkOperation(state, ticket, &slot.record, caseID) {
		return false
	}
	slot.id = id
	return true
}

func (source *affectedTestSource) publish(index int) OperationCompletionResult {
	if source == nil || index < 0 || index >= len(source.slots) {
		return OperationCompletionInvalid
	}
	slot := &source.slots[index]
	result := PublishOperationCompletion(&slot.record, slot.id)
	if result != OperationCompletionPublished {
		return result
	}

	oneBased := uint32(index) + 1
	if source.affectedHead == 0 {
		if source.affectedTail != 0 {
			return OperationCompletionInvalid
		}
		source.affectedHead = oneBased
		source.affectedTail = oneBased
		return result
	}
	if source.affectedTail == 0 || source.affectedTail > uint32(len(source.slots)) {
		return OperationCompletionInvalid
	}
	tail := &source.slots[source.affectedTail-1]
	if tail.nextAffected != 0 {
		return OperationCompletionInvalid
	}
	tail.nextAffected = oneBased
	source.affectedTail = oneBased
	return result
}

func addAffectedTestResolution(total *CompletionResolution, resolution CompletionResolution) {
	total.WaitSets += resolution.WaitSets
	total.Completed += resolution.Completed
	total.Canceled += resolution.Canceled
	total.Winners += resolution.Winners
	total.Losers += resolution.Losers
}

func (source *affectedTestSource) resolvePublishedEpoch() (total CompletionResolution, resolved, duplicates uint32, ok bool) {
	if source == nil {
		return CompletionResolution{}, 0, 0, false
	}
	for source.affectedHead != 0 {
		if source.affectedHead > uint32(len(source.slots)) {
			return total, resolved, duplicates, false
		}
		slot := &source.slots[source.affectedHead-1]
		resolution, result := resolveAffectedOperationPublishedEpoch(&slot.record, slot.id)
		if result == affectedOperationResolveInvalid {
			return total, resolved, duplicates, false
		}

		source.affectedHead = slot.nextAffected
		slot.nextAffected = 0
		if source.affectedHead == 0 {
			source.affectedTail = 0
		}
		switch result {
		case affectedOperationResolved:
			addAffectedTestResolution(&total, resolution)
			resolved++
		case affectedOperationAlreadyResolved:
			duplicates++
		default:
			return total, resolved, duplicates, false
		}
	}
	return total, resolved, duplicates, source.affectedTail == 0
}

type affectedTestEntry struct {
	source int
	slot   int
}

func runAffectedSourceOrder(t *testing.T, publishOrder []affectedTestEntry, resolveOrder []int) uint32 {
	t.Helper()

	const seed = uint32(0x51ec7)
	cases := [3]uint32{11, 22, 33}
	var state ParkState
	ticket, ok := BeginParkSet(&state, uint32(len(cases)), seed)
	if !ok {
		t.Fatal("begin affected wait-set")
	}
	var sources [2]affectedTestSource
	entries := [3]affectedTestEntry{{source: 0, slot: 0}, {source: 0, slot: 1}, {source: 1, slot: 0}}
	for index, entry := range entries {
		id, idOK := MakeOperationID(OperationSourceManual, uint32(index+1), 1)
		if !idOK || !sources[entry.source].attach(&state, ticket, entry.slot, id, cases[index]) {
			t.Fatalf("attach affected operation %d", index)
		}
	}
	if !SealParkSet(&state, ticket) || !CommitParkSet(&state, ticket) {
		t.Fatal("commit affected wait-set")
	}

	for _, entry := range publishOrder {
		if result := sources[entry.source].publish(entry.slot); result != OperationCompletionPublished {
			t.Fatalf("publish affected source %d slot %d = %d", entry.source, entry.slot, result)
		}
	}
	// Publishing all source-local facts is not itself resolution. The caller now
	// simulates one complete catalog publication before invoking either source resolver.
	if state.phase != parkParked || state.outcome != ParkOutcomePending {
		t.Fatalf("publication resolved before the epoch resolver: phase=%d outcome=%d", state.phase, state.outcome)
	}
	for _, entry := range entries {
		if sources[entry.source].slots[entry.slot].record.disposition != OperationDispositionPending {
			t.Fatalf("published operation %+v resolved before the epoch resolver", entry)
		}
	}

	var total CompletionResolution
	var resolved, duplicates uint32
	for _, sourceIndex := range resolveOrder {
		resolution, sourceResolved, sourceDuplicates, resolveOK := sources[sourceIndex].resolvePublishedEpoch()
		if !resolveOK {
			t.Fatalf("resolve affected source %d", sourceIndex)
		}
		addAffectedTestResolution(&total, resolution)
		resolved += sourceResolved
		duplicates += sourceDuplicates
	}
	wantResolution := CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1, Losers: 2}
	if total != wantResolution || resolved != 1 || duplicates != 2 {
		t.Fatalf("affected resolution = (%+v, resolved=%d duplicates=%d), want (%+v, 1, 2)", total, resolved, duplicates, wantResolution)
	}
	for sourceIndex := range sources {
		if sources[sourceIndex].affectedHead != 0 || sources[sourceIndex].affectedTail != 0 {
			t.Fatalf("source %d retained drained affected chain", sourceIndex)
		}
		if resolution, sourceResolved, sourceDuplicates, resolveOK := sources[sourceIndex].resolvePublishedEpoch(); !resolveOK || resolution != (CompletionResolution{}) || sourceResolved != 0 || sourceDuplicates != 0 {
			t.Fatalf("repeat source %d resolve = (%+v, %d, %d, %t)", sourceIndex, resolution, sourceResolved, sourceDuplicates, resolveOK)
		}
	}

	winnerCase, winnerID, winnerOK := ParkWinner(&state, ticket)
	if !winnerOK {
		t.Fatal("missing affected wait-set winner")
	}
	for _, entry := range entries {
		slot := &sources[entry.source].slots[entry.slot]
		disposition, dispositionOK := OperationDispositionOf(&slot.record, slot.id)
		if !dispositionOK || !AcknowledgeOperationResolution(&slot.record, slot.id, disposition) ||
			!DetachParkOperation(&state, ticket, &slot.record, slot.id) {
			t.Fatalf("detach affected operation %+v", entry)
		}
	}
	outcome, consumedCase, lease, consumed := ConsumeParkSet(&state, ticket)
	leaseID, leaseOK := lease.ID()
	if !consumed || outcome != ParkOutcomeCompleted || consumedCase != winnerCase || !leaseOK || leaseID != winnerID {
		t.Fatalf("consume affected winner = (%d, %d, %+v, %t)", outcome, consumedCase, lease, consumed)
	}
	for _, entry := range entries {
		slot := &sources[entry.source].slots[entry.slot]
		if !ConfirmOperationQuiesced(&slot.record, slot.id) {
			t.Fatalf("quiesce affected operation %+v", entry)
		}
		if slot.id == winnerID && !TakeOperationResult(&slot.record, lease) {
			t.Fatalf("take affected winner result %+v", entry)
		}
		if !RecycleOperation(&slot.record, slot.id) {
			t.Fatalf("recycle affected operation %+v", entry)
		}
	}
	return winnerCase
}

func TestSourceLocalAffectedOperationsDeduplicateWaitSetWithinPublishedEpoch(t *testing.T) {
	forwardEntries := []affectedTestEntry{{source: 0, slot: 0}, {source: 1, slot: 0}, {source: 0, slot: 1}}
	reverseEntries := []affectedTestEntry{{source: 0, slot: 1}, {source: 1, slot: 0}, {source: 0, slot: 0}}
	forwardWinner := runAffectedSourceOrder(t, forwardEntries, []int{0, 1})
	reverseWinner := runAffectedSourceOrder(t, reverseEntries, []int{1, 0})
	if forwardWinner != reverseWinner {
		t.Fatalf("source order selected winner: forward=%d reverse=%d", forwardWinner, reverseWinner)
	}
}
