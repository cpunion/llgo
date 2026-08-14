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
	"reflect"
	"testing"
	"unsafe"
)

func scalarPayloadForTest(t *testing.T, count uint8, values ...uint64) ScalarResultPayloadV1 {
	t.Helper()
	var scalars [3]uint64
	copy(scalars[:], values)
	payload, ok := MakeScalarResultPayloadV1(ScalarResultKindWords, ScalarResultFlags(0xa5), count, scalars[0], scalars[1], scalars[2])
	if !ok {
		t.Fatal("make scalar result payload")
	}
	return payload
}

func typeHasManagedPointer(typ reflect.Type) bool {
	switch typ.Kind() {
	case reflect.Array:
		return typeHasManagedPointer(typ.Elem())
	case reflect.Struct:
		for index := 0; index < typ.NumField(); index++ {
			if typeHasManagedPointer(typ.Field(index).Type) {
				return true
			}
		}
		return false
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice, reflect.String, reflect.UnsafePointer:
		return true
	default:
		return false
	}
}

func TestScalarResultPayloadV1LayoutAndEncoding(t *testing.T) {
	if unsafe.Sizeof(ScalarResultPayloadV1{}) != 28 || unsafe.Alignof(ScalarResultPayloadV1{}) != 4 ||
		unsafe.Offsetof(ScalarResultPayloadV1{}.Words) != 4 || unsafe.Sizeof(ScalarResultCell{}) != 36 ||
		unsafe.Alignof(ScalarResultCell{}) != 4 || typeHasManagedPointer(reflect.TypeOf(ScalarResultPayloadV1{})) ||
		typeHasManagedPointer(reflect.TypeOf(ScalarResultCell{})) {
		t.Fatalf("scalar payload layout = payload(%d,%d,%d) cell(%d,%d) pointers(%t,%t)",
			unsafe.Sizeof(ScalarResultPayloadV1{}), unsafe.Alignof(ScalarResultPayloadV1{}), unsafe.Offsetof(ScalarResultPayloadV1{}.Words),
			unsafe.Sizeof(ScalarResultCell{}), unsafe.Alignof(ScalarResultCell{}),
			typeHasManagedPointer(reflect.TypeOf(ScalarResultPayloadV1{})), typeHasManagedPointer(reflect.TypeOf(ScalarResultCell{})))
	}
	values := [3]uint64{0x1122334455667788, 0x99aabbccddeeff00, 0x0123456789abcdef}
	for count := uint8(0); count <= 3; count++ {
		payload := scalarPayloadForTest(t, count, values[:]...)
		if !payload.Valid() || payload.Version() != 1 || payload.Kind() != ScalarResultKindWords ||
			payload.Count() != count || payload.WordCount() != count*2 || payload.Flags() != ScalarResultFlags(0xa5) {
			t.Fatalf("count %d metadata = %#x", count, payload.Meta)
		}
		for index := uint8(0); index < 3; index++ {
			got, ok := payload.Scalar(index)
			if index < count {
				if !ok || got != values[index] || payload.Words[index*2] != uint32(values[index]) ||
					payload.Words[index*2+1] != uint32(values[index]>>32) {
					t.Fatalf("count %d scalar %d = (%#x,%t), words=%#x/%#x", count, index, got, ok,
						payload.Words[index*2], payload.Words[index*2+1])
				}
			} else if ok || got != 0 || payload.Words[index*2] != 0 || payload.Words[index*2+1] != 0 {
				t.Fatalf("count %d exposed unused scalar %d", count, index)
			}
		}
	}
}

func TestScalarResultPayloadV1RejectsInvalidShapes(t *testing.T) {
	valid := scalarPayloadForTest(t, 2, 1, 2)
	invalid := []ScalarResultPayloadV1{{}, valid, valid, valid, valid, valid}
	invalid[1].Meta = invalid[1].Meta&^scalarResultByteMask | 2
	invalid[2].Meta = invalid[2].Meta&^(scalarResultByteMask<<scalarResultKindShift) |
		uint32(ScalarResultKindInvalid)<<scalarResultKindShift
	invalid[3].Meta = invalid[3].Meta&^(scalarResultNibbleMask<<scalarResultCountShift) | 4<<scalarResultCountShift
	invalid[4].Meta = invalid[4].Meta&^(scalarResultNibbleMask<<scalarResultWordsShift) | 2<<scalarResultWordsShift
	invalid[5].Words[5] = 1
	for index, payload := range invalid {
		if payload.Valid() {
			t.Fatalf("invalid payload %d accepted: %#v", index, payload)
		}
	}
	if payload, ok := MakeScalarResultPayloadV1(ScalarResultKindInvalid, 0, 0, 0, 0, 0); ok || payload != (ScalarResultPayloadV1{}) {
		t.Fatal("constructor accepted invalid kind")
	}
	if payload, ok := MakeScalarResultPayloadV1(ScalarResultKindWords, 0, 4, 0, 0, 0); ok || payload != (ScalarResultPayloadV1{}) {
		t.Fatal("constructor accepted four logical scalars")
	}
}

func resolveScalarCommitFake(
	t *testing.T,
	source *commitSelectFakeSource,
	cells []ScalarResultCell,
	payloads []ScalarResultPayloadV1,
) (CompletionResolution, ParkResolveStatus) {
	t.Helper()
	var attempt ParkCommitAttempt
	for step := 0; step <= len(source.records)+1; step++ {
		resolution, request, status := ResolveParkSnapshotStep(source.state, source.ticket, attempt)
		switch status {
		case ParkResolvePending, ParkResolveResolved, ParkResolveInvalid:
			return resolution, status
		case ParkResolveNeedsCommit:
			index := int(request.id.LocalSlot()) - 1
			if index < 0 || index >= len(source.records) || request.record != &source.records[index] ||
				request.id != source.ids[index] || source.specs[index].mode != OperationCommitReadyThenTryCommit {
				t.Fatal("invalid scalar fake commit request")
			}
			source.attempts[index]++
			if source.canCommit[index] {
				source.committed[index] = true
				var ok bool
				attempt, ok = BindScalarParkCommitResult(&cells[index], request, payloads[index])
				if !ok {
					source.committed[index] = false
					t.Fatal("bind scalar fake Ready result")
				}
			} else {
				attempt = request.Failed()
			}
		default:
			t.Fatalf("unknown park resolve status %d", status)
		}
	}
	t.Fatal("scalar fake resolver did not terminate")
	return CompletionResolution{}, ParkResolveInvalid
}

func consumeScalarCommitFake(
	t *testing.T,
	source *commitSelectFakeSource,
	cells []ScalarResultCell,
) (ParkOutcome, uint32, OperationResultLease) {
	t.Helper()
	for index := range source.records {
		record, id := &source.records[index], source.ids[index]
		disposition, ok := OperationDispositionOf(record, id)
		if !ok {
			t.Fatalf("read scalar candidate %d disposition", index)
		}
		if disposition != OperationDispositionWinner && record.resultState == operationResultOwned {
			if AcknowledgeOperationResolution(record, id, disposition) {
				t.Fatalf("candidate %d acknowledged before payload release", index)
			}
			source.released[index] = true
			if !DiscardUnselectedScalarOperationResult(&cells[index], record, id) || cells[index] != (ScalarResultCell{}) ||
				record.resultState != operationResultDiscarded {
				t.Fatalf("candidate %d did not clear payload before discard", index)
			}
		} else if disposition != OperationDispositionWinner && cells[index] != (ScalarResultCell{}) {
			t.Fatalf("candidate %d retained a payload without Owned result", index)
		}
		if !AcknowledgeOperationResolution(record, id, disposition) ||
			!DetachParkWaitOperation(nil, source.state, source.ticket, record, id) || !ConfirmOperationQuiesced(record, id) {
			t.Fatalf("finish scalar candidate %d", index)
		}
	}
	if !ParkReady(source.state, source.ticket) {
		t.Fatal("scalar fake did not cross detach barrier")
	}
	outcome, caseID, lease, ok := ConsumeParkSet(source.state, source.ticket)
	if !ok || !ReleasePreparedWaitSetRecord(&source.wait) {
		t.Fatal("consume scalar fake result")
	}
	return outcome, caseID, lease
}

func recycleScalarCommitFake(t *testing.T, source *commitSelectFakeSource, cells []ScalarResultCell) {
	t.Helper()
	for index := range source.records {
		if cells[index] != (ScalarResultCell{}) || !OperationCanRecycle(&source.records[index], source.ids[index]) ||
			!RecycleOperation(&source.records[index], source.ids[index]) {
			t.Fatalf("recycle scalar candidate %d", index)
		}
	}
}

func TestScalarResultPublicationIsExactAndTransactional(t *testing.T) {
	source := newCommitSelectFakeSource(t, 0x801, []commitSelectCandidateSpec{{caseID: 81}}, []int{0}, false, 0)
	payload := scalarPayloadForTest(t, 3, 0x1111222233334444, 0x5555666677778888, 9)
	var cells [1]ScalarResultCell
	if result := PublishScalarOperationCompletion(&cells[0], &source.records[0], source.ids[0], payload); result != OperationCompletionPublished {
		t.Fatalf("publish scalar result = %d", result)
	}
	retained := cells[0]
	if result := PublishScalarOperationCompletion(&cells[0], &source.records[0], source.ids[0], payload); result != OperationCompletionDuplicate ||
		cells[0] != retained {
		t.Fatalf("duplicate scalar publication = %d, cell changed=%t", result, cells[0] != retained)
	}
	different := payload
	different.Words[0]++
	if result := PublishScalarOperationCompletion(&cells[0], &source.records[0], source.ids[0], different); result != OperationCompletionInvalid ||
		cells[0] != retained {
		t.Fatal("different duplicate rewrote or cleared scalar cell")
	}
	stale := source.ids[0]
	stale.Generation++
	if result := PublishScalarOperationCompletion(&cells[0], &source.records[0], stale, payload); result != OperationCompletionInvalid ||
		cells[0] != retained {
		t.Fatal("stale publication rewrote or cleared scalar cell")
	}
	resolution, status := source.resolve(t)
	if status != ParkResolveResolved || resolution.Completed != 1 {
		t.Fatalf("resolve scalar publication = (%+v,%d)", resolution, status)
	}
	outcome, caseID, lease := consumeScalarCommitFake(t, source, cells[:])
	if outcome != ParkOutcomeCompleted || caseID != 81 || !lease.Valid() {
		t.Fatalf("consume scalar publication = (%d,%d,%+v)", outcome, caseID, lease)
	}
	sentinel := scalarPayloadForTest(t, 1, 0xfeed)
	out := sentinel
	staleLease := lease
	staleLease.ticket.generation++
	if TakeScalarOperationResult(&cells[0], &source.records[0], staleLease, &out) || out != sentinel || cells[0] != retained {
		t.Fatal("stale lease read or cleared scalar winner")
	}
	if !TakeScalarOperationResult(&cells[0], &source.records[0], lease, &out) || out != payload || cells[0] != (ScalarResultCell{}) ||
		source.records[0].resultState != operationResultTaken {
		t.Fatal("exact lease did not take scalar winner")
	}
	recycleScalarCommitFake(t, source, cells[:])
}

func TestScalarResultStaleLeaseCannotTouchRearmedGeneration(t *testing.T) {
	source := newCommitSelectFakeSource(t, 0x802, []commitSelectCandidateSpec{{caseID: 82}}, []int{0}, false, 0)
	oldPayload := scalarPayloadForTest(t, 1, 1)
	var cell ScalarResultCell
	if PublishScalarOperationCompletion(&cell, &source.records[0], source.ids[0], oldPayload) != OperationCompletionPublished {
		t.Fatal("publish old scalar generation")
	}
	if _, status := source.resolve(t); status != ParkResolveResolved {
		t.Fatal("resolve old scalar generation")
	}
	_, _, oldLease := consumeScalarCommitFake(t, source, []ScalarResultCell{cell})
	// consumeScalarCommitFake received a copy of the cell; retain the actual
	// source-owned cell here and release it with the exact old lease.
	var copied ScalarResultPayloadV1
	if !TakeScalarOperationResult(&cell, &source.records[0], oldLease, &copied) || copied != oldPayload ||
		!OperationCanRecycle(&source.records[0], source.ids[0]) || !RecycleOperation(&source.records[0], source.ids[0]) {
		t.Fatal("release old scalar generation")
	}
	oldID := source.ids[0]
	newID, ok := RearmOperation(&source.records[0])
	if !ok || newID.Generation != oldID.Generation+1 {
		t.Fatal("rearm scalar operation generation")
	}
	var state ParkState
	ticket, ok := BeginParkSet(&state, 1, 0x803)
	if !ok || !AttachParkOperation(&state, ticket, &source.records[0], 83) || !SealParkSet(&state, ticket) || !CommitParkSet(&state, ticket) {
		t.Fatal("attach rearmed scalar operation")
	}
	newPayload := scalarPayloadForTest(t, 2, 2, 3)
	if PublishScalarOperationCompletion(&cell, &source.records[0], newID, newPayload) != OperationCompletionPublished {
		t.Fatal("publish rearmed scalar generation")
	}
	retained, out := cell, oldPayload
	if TakeScalarOperationResult(&cell, &source.records[0], oldLease, &out) || DiscardScalarOperationResult(&cell, &source.records[0], oldLease) ||
		cell != retained || out != oldPayload {
		t.Fatal("old lease touched rearmed scalar generation")
	}
	if resolution, resolved := ResolveParkSnapshot(&state, ticket); !resolved || resolution.Completed != 1 {
		t.Fatal("resolve rearmed scalar generation")
	}
	if !AcknowledgeOperationResolution(&source.records[0], newID, OperationDispositionWinner) ||
		!DetachParkOperation(&state, ticket, &source.records[0], newID) || !ConfirmOperationQuiesced(&source.records[0], newID) {
		t.Fatal("detach rearmed scalar generation")
	}
	_, _, newLease, consumed := ConsumeParkSet(&state, ticket)
	if !consumed || !TakeScalarOperationResult(&cell, &source.records[0], newLease, &out) || out != newPayload ||
		!RecycleOperation(&source.records[0], newID) {
		t.Fatal("consume rearmed scalar generation")
	}
}

func TestScalarResultTakeAndDiscardRemainDistinct(t *testing.T) {
	for _, discard := range []bool{false, true} {
		name := "take"
		if discard {
			name = "discard"
		}
		t.Run(name, func(t *testing.T) {
			source := newCommitSelectFakeSource(t, 0x804, []commitSelectCandidateSpec{{caseID: 84}}, []int{0}, false, 0)
			payload := scalarPayloadForTest(t, 2, 7, 8)
			cells := []ScalarResultCell{{}}
			if PublishScalarOperationCompletion(&cells[0], &source.records[0], source.ids[0], payload) != OperationCompletionPublished {
				t.Fatal("publish scalar terminal action fixture")
			}
			if _, status := source.resolve(t); status != ParkResolveResolved {
				t.Fatal("resolve scalar terminal action fixture")
			}
			_, _, lease := consumeScalarCommitFake(t, source, cells)
			if discard {
				if !DiscardScalarOperationResult(&cells[0], &source.records[0], lease) ||
					source.records[0].resultState != operationResultDiscarded {
					t.Fatal("discard scalar result")
				}
			} else {
				var out ScalarResultPayloadV1
				if !TakeScalarOperationResult(&cells[0], &source.records[0], lease, &out) || out != payload ||
					source.records[0].resultState != operationResultTaken {
					t.Fatal("take scalar result")
				}
			}
			if cells[0] != (ScalarResultCell{}) || TakeOperationResult(&source.records[0], lease) ||
				DiscardOperationResult(&source.records[0], lease) {
				t.Fatal("scalar terminal action was not unique")
			}
			recycleScalarCommitFake(t, source, cells)
		})
	}
}

func TestScalarResultLosersClearBeforeResolutionAck(t *testing.T) {
	source := newCommitSelectFakeSource(t, 0x805, []commitSelectCandidateSpec{
		{caseID: 85},
		{caseID: 86, mode: OperationCommitReservable},
	}, []int{0, 1}, false, 0)
	cells := make([]ScalarResultCell, 2)
	payloads := []ScalarResultPayloadV1{scalarPayloadForTest(t, 1, 10), scalarPayloadForTest(t, 3, 11, 12, 13)}
	if PublishScalarOperationCompletion(&cells[0], &source.records[0], source.ids[0], payloads[0]) != OperationCompletionPublished ||
		PublishScalarReservableCandidate(&cells[1], &source.records[1], source.ids[1], payloads[1]) != OperationCompletionPublished ||
		!RequestParkCancel(source.state, source.ticket, ParkCancelTaskAbort) {
		t.Fatal("prepare scalar loser cancellation")
	}
	if mode, ok := OperationCommitModeOf(&source.records[1], source.ids[1]); !ok || mode != OperationCommitReservable ||
		source.records[1].resultState != operationResultOwned || cells[1].id != source.ids[1] || cells[1].payload != payloads[1] {
		t.Fatalf("reservable payload publication = mode(%d,%t) state=%d cell=%+v", mode, ok,
			source.records[1].resultState, cells[1])
	}
	if resolution, status := source.resolve(t); status != ParkResolveResolved || resolution.Canceled != 1 || resolution.Losers != 2 {
		t.Fatalf("resolve scalar loser cancellation = (%+v,%d)", resolution, status)
	}
	outcome, _, lease := consumeScalarCommitFake(t, source, cells)
	if outcome != ParkOutcomeCanceled || lease.Valid() {
		t.Fatal("consume scalar loser cancellation")
	}
	recycleScalarCommitFake(t, source, cells)
}

func TestScalarResultCleanupIgnoresInvalidPayloadMetadata(t *testing.T) {
	t.Run("staged", func(t *testing.T) {
		id, ok := MakeOperationID(OperationSourceManual, 1, 1)
		if !ok {
			t.Fatal("make staged scalar operation ID")
		}
		cell := ScalarResultCell{id: id}
		if cell.payload.Valid() || !clearStagedScalarOperationResult(&cell, id) || cell != (ScalarResultCell{}) {
			t.Fatal("invalid staged payload blocked exact cleanup")
		}
	})

	t.Run("unselected", func(t *testing.T) {
		source := newCommitSelectFakeSource(t, 0x809, []commitSelectCandidateSpec{{caseID: 90}}, []int{0}, false, 0)
		payload := scalarPayloadForTest(t, 1, 51)
		var cell ScalarResultCell
		if PublishScalarOperationCompletion(&cell, &source.records[0], source.ids[0], payload) != OperationCompletionPublished ||
			!RequestParkCancel(source.state, source.ticket, ParkCancelTaskAbort) {
			t.Fatal("prepare invalid unselected scalar payload")
		}
		if resolution, status := source.resolve(t); status != ParkResolveResolved || resolution.Canceled != 1 ||
			resolution.Losers != 1 {
			t.Fatalf("resolve invalid unselected scalar payload = (%+v,%d)", resolution, status)
		}
		cell.payload.Meta = 0
		if cell.payload.Valid() || !DiscardUnselectedScalarOperationResult(&cell, &source.records[0], source.ids[0]) ||
			cell != (ScalarResultCell{}) || source.records[0].resultState != operationResultDiscarded {
			t.Fatal("invalid unselected payload blocked loser cleanup")
		}
		if !AcknowledgeOperationResolution(&source.records[0], source.ids[0], OperationDispositionCanceled) ||
			!DetachParkWaitOperation(nil, source.state, source.ticket, &source.records[0], source.ids[0]) ||
			!ConfirmOperationQuiesced(&source.records[0], source.ids[0]) {
			t.Fatal("finish invalid unselected scalar payload")
		}
		if outcome, _, lease, ok := ConsumeParkSet(source.state, source.ticket); !ok || outcome != ParkOutcomeCanceled ||
			lease.Valid() || !ReleasePreparedWaitSetRecord(&source.wait) || !RecycleOperation(&source.records[0], source.ids[0]) {
			t.Fatal("consume invalid unselected scalar payload")
		}
	})
}

func TestScalarReadyFailureRepublishAndBindDoNotLeak(t *testing.T) {
	source := newCommitSelectFakeSource(t, 0x806, []commitSelectCandidateSpec{{
		caseID: 87, mode: OperationCommitReadyThenTryCommit,
	}}, []int{0}, false, 0)
	payload := scalarPayloadForTest(t, 3, 21, 22, 23)
	cells := make([]ScalarResultCell, 1)
	payloads := []ScalarResultPayloadV1{payload}
	if PublishReadyThenTryCommitCandidate(&source.records[0], source.ids[0]) != OperationCompletionPublished {
		t.Fatal("publish first scalar Ready hint")
	}
	if resolution, status := resolveScalarCommitFake(t, source, cells, payloads); status != ParkResolvePending ||
		resolution != (CompletionResolution{WaitSets: 1}) || cells[0] != (ScalarResultCell{}) ||
		source.records[0].resultState != operationResultEmpty {
		t.Fatalf("failed scalar Ready = (%+v,%d,%+v)", resolution, status, cells[0])
	}
	if PublishReadyThenTryCommitCandidate(&source.records[0], source.ids[0]) != OperationCompletionPublished {
		t.Fatal("republish scalar Ready hint")
	}
	source.canCommit[0] = true
	_, request, status := ResolveParkSnapshotStep(source.state, source.ticket, ParkCommitAttempt{})
	if status != ParkResolveNeedsCommit || !currentParkCommitRequest(request) {
		t.Fatal("republished scalar Ready did not request commit")
	}
	if _, bound := BindScalarParkCommitResult(&cells[0], request, ScalarResultPayloadV1{}); bound ||
		cells[0] != (ScalarResultCell{}) || !currentParkCommitRequest(request) {
		t.Fatal("invalid scalar Ready bind leaked cell or consumed request")
	}
	attempt, bound := BindScalarParkCommitResult(&cells[0], request, payload)
	if !bound || cells[0].id != source.ids[0] || cells[0].payload != payload {
		t.Fatal("bind republished scalar Ready")
	}
	retained := cells[0]
	if _, duplicate := BindScalarParkCommitResult(&cells[0], request, payload); duplicate || cells[0] != retained {
		t.Fatal("duplicate Ready bind changed scalar cell")
	}
	if resolution, _, status := ResolveParkSnapshotStep(source.state, source.ticket, attempt); status != ParkResolveResolved ||
		resolution.Completed != 1 || cells[0] != retained {
		t.Fatalf("resolve bound scalar Ready = (%+v,%d,%+v)", resolution, status, cells[0])
	}
	_, _, lease := consumeScalarCommitFake(t, source, cells)
	var out ScalarResultPayloadV1
	if !TakeScalarOperationResult(&cells[0], &source.records[0], lease, &out) || out != payload {
		t.Fatal("take republished scalar Ready result")
	}
	recycleScalarCommitFake(t, source, cells)
}

func TestScalarResultLostPublicationClearsOnlyNewStage(t *testing.T) {
	source := newCommitSelectFakeSource(t, 0x807, []commitSelectCandidateSpec{{caseID: 88}}, []int{0}, false, 0)
	if !RequestParkCancel(source.state, source.ticket, ParkCancelOperation) {
		t.Fatal("cancel before scalar publication")
	}
	if resolution, status := source.resolve(t); status != ParkResolveResolved || resolution.Canceled != 1 {
		t.Fatal("resolve pre-publication cancellation")
	}
	payload := scalarPayloadForTest(t, 1, 31)
	var cell ScalarResultCell
	if result := PublishScalarOperationCompletion(&cell, &source.records[0], source.ids[0], payload); result != OperationCompletionLost ||
		cell != (ScalarResultCell{}) || source.records[0].resultState != operationResultEmpty {
		t.Fatalf("lost scalar publication = (%d,%+v,%d)", result, cell, source.records[0].resultState)
	}
	if outcome, _, _ := consumeScalarCommitFake(t, source, []ScalarResultCell{cell}); outcome != ParkOutcomeCanceled {
		t.Fatal("consume pre-publication scalar cancellation")
	}
	if !OperationCanRecycle(&source.records[0], source.ids[0]) || !RecycleOperation(&source.records[0], source.ids[0]) {
		t.Fatal("recycle lost scalar publication")
	}
}

func TestScalarResultCoreIsAllocationFree(t *testing.T) {
	source := newCommitSelectFakeSource(t, 0x808, []commitSelectCandidateSpec{{caseID: 89}}, []int{0}, false, 0)
	payload := scalarPayloadForTest(t, 3, 41, 42, 43)
	stateBefore, recordBefore := *source.state, source.records[0]
	failed := false
	allocations := testing.AllocsPerRun(1000, func() {
		*source.state = stateBefore
		source.records[0] = recordBefore
		var cell ScalarResultCell
		if PublishScalarOperationCompletion(&cell, &source.records[0], source.ids[0], payload) != OperationCompletionPublished {
			failed = true
		}
	})
	if failed || allocations != 0 {
		t.Fatalf("scalar publish = failed %t allocations %.2f", failed, allocations)
	}
}
