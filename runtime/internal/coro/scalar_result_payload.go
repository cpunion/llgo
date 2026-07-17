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

import "unsafe"

// ScalarResultKind identifies a fixed scalar tuple's static interpretation.
// A source adapter fixes each opaque word's meaning without storing a pointer.
type ScalarResultKind uint8

const (
	ScalarResultKindInvalid ScalarResultKind = iota
	ScalarResultKindWords
)

// ScalarResultFlags is an opaque source-kind byte with no V1 global semantics.
type ScalarResultFlags uint8

const scalarResultPayloadVersionV1 = uint8(1)

const (
	scalarResultVersionShift = 0
	scalarResultKindShift    = 8
	scalarResultCountShift   = 16
	scalarResultWordsShift   = 20
	scalarResultFlagsShift   = 24

	scalarResultByteMask   = uint32(0xff)
	scalarResultNibbleMask = uint32(0x0f)
	scalarResultMaxCount   = uint8(3)
)

// ScalarResultPayloadV1 is a versioned pointer-free tuple for syscall workers,
// IOCP/io_uring, WASI, JS host callbacks, IRQ adapters, and similar sources:
//
//	0..7 version=1; 8..15 kind=Words; 16..19 logical count=0..3
//	20..23 physical uint32 count=logical*2; 24..31 source-kind flags
//
// Each scalar uses low word first: Words[2*i] is bits 0..31 and Words[2*i+1]
// is bits 32..63. This is a value-level encoding, not a serialized byte
// stream, so it has the same meaning on little/big endian, 32-bit, WASM, and
// native targets. Every unused word must be zero.
type ScalarResultPayloadV1 struct {
	Meta  uint32
	Words [6]uint32
}

var (
	_ [28 - unsafe.Sizeof(ScalarResultPayloadV1{})]byte
	_ [unsafe.Sizeof(ScalarResultPayloadV1{}) - 28]byte
	_ [4 - unsafe.Alignof(ScalarResultPayloadV1{})]byte
	_ [unsafe.Alignof(ScalarResultPayloadV1{}) - 4]byte
	_ [4 - unsafe.Offsetof(ScalarResultPayloadV1{}.Words)]byte
	_ [unsafe.Offsetof(ScalarResultPayloadV1{}.Words) - 4]byte
)

func MakeScalarResultPayloadV1(
	kind ScalarResultKind,
	flags ScalarResultFlags,
	count uint8,
	first, second, third uint64,
) (ScalarResultPayloadV1, bool) {
	if kind != ScalarResultKindWords || count > scalarResultMaxCount {
		return ScalarResultPayloadV1{}, false
	}
	values := [3]uint64{first, second, third}
	payload := ScalarResultPayloadV1{Meta: uint32(scalarResultPayloadVersionV1)<<scalarResultVersionShift |
		uint32(kind)<<scalarResultKindShift | uint32(count)<<scalarResultCountShift |
		uint32(count*2)<<scalarResultWordsShift | uint32(flags)<<scalarResultFlagsShift}
	for index := uint8(0); index < count; index++ {
		value := values[index]
		payload.Words[index*2] = uint32(value)
		payload.Words[index*2+1] = uint32(value >> 32)
	}
	return payload, true
}

func (payload ScalarResultPayloadV1) Version() uint8 {
	return uint8(payload.Meta >> scalarResultVersionShift & scalarResultByteMask)
}

func (payload ScalarResultPayloadV1) Kind() ScalarResultKind {
	return ScalarResultKind(payload.Meta >> scalarResultKindShift & scalarResultByteMask)
}

func (payload ScalarResultPayloadV1) Count() uint8 {
	return uint8(payload.Meta >> scalarResultCountShift & scalarResultNibbleMask)
}

func (payload ScalarResultPayloadV1) WordCount() uint8 {
	return uint8(payload.Meta >> scalarResultWordsShift & scalarResultNibbleMask)
}

func (payload ScalarResultPayloadV1) Flags() ScalarResultFlags {
	return ScalarResultFlags(payload.Meta >> scalarResultFlagsShift & scalarResultByteMask)
}

func (payload ScalarResultPayloadV1) Valid() bool {
	count, words := payload.Count(), payload.WordCount()
	if payload.Version() != scalarResultPayloadVersionV1 || payload.Kind() != ScalarResultKindWords ||
		count > scalarResultMaxCount || words != count*2 {
		return false
	}
	for index := words; index < uint8(len(payload.Words)); index++ {
		if payload.Words[index] != 0 {
			return false
		}
	}
	return true
}

func (payload ScalarResultPayloadV1) Scalar(index uint8) (uint64, bool) {
	if !payload.Valid() || index >= payload.Count() {
		return 0, false
	}
	word := index * 2
	return uint64(payload.Words[word]) | uint64(payload.Words[word+1])<<32, true
}

// ScalarResultCell is source-owned stable slot storage, not a producer mailbox.
// An asynchronous producer release-publishes its source-specific atomic
// mailbox; the owner acquire-drain copies here before OperationRecord publish
// or bind. Exact OperationID rejects a reused physical generation, while a
// winner's OperationResultLease separately validates the logical ParkTicket.
type ScalarResultCell struct {
	id      OperationID
	payload ScalarResultPayloadV1
}

var (
	_ [36 - unsafe.Sizeof(ScalarResultCell{})]byte
	_ [unsafe.Sizeof(ScalarResultCell{}) - 36]byte
	_ [4 - unsafe.Alignof(ScalarResultCell{})]byte
	_ [unsafe.Alignof(ScalarResultCell{}) - 4]byte
)

func stageScalarOperationResult(cell *ScalarResultCell, id OperationID, payload ScalarResultPayloadV1) bool {
	if cell == nil || *cell != (ScalarResultCell{}) || !id.Valid() || !payload.Valid() {
		return false
	}
	*cell = ScalarResultCell{id: id, payload: payload}
	return true
}

func clearStagedScalarOperationResult(cell *ScalarResultCell, id OperationID) bool {
	if cell == nil || cell.id != id || !id.Valid() {
		return false
	}
	*cell = ScalarResultCell{}
	return true
}

func publishScalarOperationResult(
	cell *ScalarResultCell,
	record *OperationRecord,
	id OperationID,
	payload ScalarResultPayloadV1,
	reservable bool,
) OperationCompletionResult {
	if cell == nil || !id.Valid() || !payload.Valid() {
		return OperationCompletionInvalid
	}
	if *cell != (ScalarResultCell{}) {
		// An exact duplicate may ask the OperationRecord for its stable
		// classification, but it cannot rewrite or clear the existing cell.
		if cell.id != id || cell.payload != payload {
			return OperationCompletionInvalid
		}
		if reservable {
			return PublishReservableCandidate(record, id)
		}
		return PublishOperationCompletion(record, id)
	}
	if !stageScalarOperationResult(cell, id, payload) {
		return OperationCompletionInvalid
	}
	var result OperationCompletionResult
	if reservable {
		result = PublishReservableCandidate(record, id)
	} else {
		result = PublishOperationCompletion(record, id)
	}
	if result != OperationCompletionPublished && !clearStagedScalarOperationResult(cell, id) {
		return OperationCompletionInvalid
	}
	return result
}

func PublishScalarOperationCompletion(
	cell *ScalarResultCell,
	record *OperationRecord,
	id OperationID,
	payload ScalarResultPayloadV1,
) OperationCompletionResult {
	return publishScalarOperationResult(cell, record, id, payload, false)
}

func PublishScalarReservableCandidate(
	cell *ScalarResultCell,
	record *OperationRecord,
	id OperationID,
	payload ScalarResultPayloadV1,
) OperationCompletionResult {
	return publishScalarOperationResult(cell, record, id, payload, true)
}

// BindScalarParkCommitResult stages a ReadyThenTryCommit result after its
// synchronous effect and binds the same exact, non-reentrant request. Failure
// clears the cell and requires source-specific physical rollback.
func BindScalarParkCommitResult(
	cell *ScalarResultCell,
	request ParkCommitRequest,
	payload ScalarResultPayloadV1,
) (ParkCommitAttempt, bool) {
	if !currentParkCommitRequest(request) || !stageScalarOperationResult(cell, request.id, payload) {
		return ParkCommitAttempt{}, false
	}
	attempt, bound := BindParkCommitResult(request)
	if !bound {
		clearStagedScalarOperationResult(cell, request.id)
		return ParkCommitAttempt{}, false
	}
	return attempt, true
}

func scalarOperationResultLeaseMatches(cell *ScalarResultCell, record *OperationRecord, lease OperationResultLease) bool {
	return cell != nil && record != nil && lease.Valid() && cell.id == lease.id && cell.payload.Valid() &&
		record.Matches(lease.id) && record.phase == operationDetached && record.disposition == OperationDispositionWinner &&
		record.resultState == operationResultLeased && record.resultTicket == lease.ticket
}

// TakeScalarOperationResult copies locally, ends the exact winner lease, then
// clears/exposes the copy without an owner-P re-entrant observation point.
func TakeScalarOperationResult(
	cell *ScalarResultCell,
	record *OperationRecord,
	lease OperationResultLease,
	out *ScalarResultPayloadV1,
) bool {
	if out == nil || !scalarOperationResultLeaseMatches(cell, record, lease) {
		return false
	}
	payload := cell.payload
	if !TakeOperationResult(record, lease) {
		return false
	}
	*cell = ScalarResultCell{}
	*out = payload
	return true
}

// DiscardScalarOperationResult clears a selected payload before recording the
// cleanup intent on its exact winner lease.
func DiscardScalarOperationResult(cell *ScalarResultCell, record *OperationRecord, lease OperationResultLease) bool {
	if !scalarOperationResultLeaseMatches(cell, record, lease) {
		return false
	}
	previous := *cell
	*cell = ScalarResultCell{}
	if !DiscardOperationResult(record, lease) {
		*cell = previous
		return false
	}
	return true
}

// DiscardUnselectedScalarOperationResult is used only after source-specific
// cancel/rollback has released the physical payload. It clears the stable cell
// before the generic Owned -> Discarded transition; only then may the source
// acknowledge and detach this loser.
func DiscardUnselectedScalarOperationResult(cell *ScalarResultCell, record *OperationRecord, id OperationID) bool {
	if cell == nil || record == nil || cell.id != id || !record.Matches(id) ||
		record.phase != operationActive || record.resolutionApplied ||
		(record.disposition != OperationDispositionLost && record.disposition != OperationDispositionCanceled) ||
		!operationCandidateSettledForDisposition(record, record.disposition) || record.resultState != operationResultOwned {
		return false
	}
	previous := *cell
	*cell = ScalarResultCell{}
	if !DiscardUnselectedOperationResult(record, id) {
		*cell = previous
		return false
	}
	return true
}
