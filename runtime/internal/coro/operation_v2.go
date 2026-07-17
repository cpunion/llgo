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

// OperationSource identifies one statically registered physical event-source
// family. Zero is invalid. Source-specific producer ABIs still carry their own
// two uint32 words; this type is the scheduler-side common encoding.
type OperationSource uint8

const (
	OperationSourceInvalid OperationSource = iota
	OperationSourceWait
	OperationSourceTimer
	OperationSourceManual
	OperationSourcePoll
	OperationSourceWorker
	OperationSourceHost
	OperationSourceIRQ
	// OperationSourceControl identifies a generation-stable external task
	// cancellation endpoint. It carries no operation result and is allocated
	// only when a host/export boundary explicitly exposes a task handle; an
	// ordinary G never enters a global handle registry.
	OperationSourceControl
)

const (
	operationSourceBits = 8
	operationSlotBits   = 32 - operationSourceBits
	operationSlotMask   = uint32(1<<operationSlotBits - 1)
)

// OperationID is the pointer-free physical source identity. SourceSlot packs
// an 8-bit source family and a 24-bit one-based slot. Generation is owned by
// the physical source and must not be reused until that source is quiescent.
//
// Keeping two explicit uint32 words preserves size 8/alignment 4 on 32-bit,
// WASM, embedded, and C ABI boundaries instead of depending on uint64 layout.
type OperationID struct {
	SourceSlot uint32
	Generation uint32
}

// MakeOperationID constructs an exact source/slot generation identity.
func MakeOperationID(source OperationSource, slot, generation uint32) (OperationID, bool) {
	if !validOperationSource(source) || slot == 0 || slot > operationSlotMask || generation == 0 {
		return OperationID{}, false
	}
	return OperationID{
		SourceSlot: uint32(source)<<operationSlotBits | slot,
		Generation: generation,
	}, true
}

func (id OperationID) Source() OperationSource {
	return OperationSource(id.SourceSlot >> operationSlotBits)
}

func (id OperationID) Slot() uint32 {
	return id.SourceSlot & operationSlotMask
}

func (id OperationID) Valid() bool {
	return validOperationSource(id.Source()) && id.Slot() != 0 && id.Generation != 0
}

func validOperationSource(source OperationSource) bool {
	switch source {
	case OperationSourceWait, OperationSourceTimer, OperationSourceManual, OperationSourcePoll,
		OperationSourceWorker, OperationSourceHost, OperationSourceIRQ, OperationSourceControl:
		return true
	default:
		return false
	}
}

// NextOperationID advances one exact physical slot generation. Exhaustion
// fails closed: a physical slot may be widened or retired, but never wraps
// while an old callback could still carry the same two POD words.
func NextOperationID(previous OperationID, source OperationSource, slot uint32) (OperationID, bool) {
	if previous == (OperationID{}) {
		return MakeOperationID(source, slot, 1)
	}
	if !previous.Valid() || previous.Source() != source || previous.Slot() != slot || previous.Generation == ^uint32(0) {
		return OperationID{}, false
	}
	return MakeOperationID(source, slot, previous.Generation+1)
}

type operationPhase uint8

const (
	operationUnused operationPhase = iota
	operationReserved
	operationActive
	operationDetached
	operationReusable
)

// OperationDisposition is the logical wait-set decision recorded before a
// physical operation detaches. It is deliberately separate from quiescence.
type OperationDisposition uint8

const (
	OperationDispositionPending OperationDisposition = iota
	OperationDispositionWinner
	OperationDispositionLost
	OperationDispositionCanceled
)

// OperationCompletionResult classifies a scheduler-side completion publish.
// Lost is normal for a select loser or an operation canceled before a late
// backend completion; it must not be treated as runtime corruption.
type OperationCompletionResult uint8

const (
	OperationCompletionInvalid OperationCompletionResult = iota
	OperationCompletionPublished
	OperationCompletionDuplicate
	OperationCompletionLost
)

// OperationCancelResult separates a durable request from the later logical
// winner and from physical quiescence.
type OperationCancelResult uint8

const (
	OperationCancelInvalid OperationCancelResult = iota
	OperationCancelRequested
	OperationCancelAlreadyRequested
	OperationCancelCompletionPending
	OperationCancelAlreadyTerminal
)

// OperationApplyResult is the source-owner result of applying one terminal
// logical disposition reached through an exact ParkLink. Detached means the
// source acknowledged the disposition and removed that link. Deferred means
// the exact operation remains attached and must be retried by a later owner
// epoch; it is not a failed or partially detached operation.
type OperationApplyResult uint8

const (
	OperationApplyInvalid OperationApplyResult = iota
	OperationApplyDetached
	OperationApplyDeferred
)

// OperationRecord is stable scheduler/source-owned storage. The producer does
// not receive this pointer: it retains only OperationID and reaches the record
// through its source table after generation validation.
//
// Every OperationRecord method in this file is owner-P-only, including
// PublishOperationCompletion. A callback/ISR writes only its source-specific
// atomic mailbox using OperationID; the source's scheduler drain validates the
// generation and then publishes into this non-atomic suffix.
//
// The ParkLink is cleared by DetachParkOperation before the owner G can become
// runnable. quiesced may become true on either side of detach; only both facts
// together permit RecycleOperation.
type OperationRecord struct {
	id                  OperationID
	phase               operationPhase
	disposition         OperationDisposition
	resolutionApplied   bool
	completionPublished bool
	cancelRequested     bool
	quiesced            bool
	resultConsumable    bool
	resultTaken         bool
	resultTicket        ParkTicket
	link                ParkLink
}

func InitOperation(record *OperationRecord, id OperationID) bool {
	if record == nil || !id.Valid() || id.Generation != 1 || record.phase != operationUnused || record.id != (OperationID{}) ||
		record.link.park != nil || record.link.wait != nil || record.link.operation != nil || record.link.previous != nil || record.link.next != nil {
		return false
	}
	*record = OperationRecord{id: id, phase: operationReserved}
	return true
}

// PrepareOperationAtGeneration aligns a V2 record with the generation owned
// by a physical slot which may also have been used by a legacy protocol. It is
// deliberately more restrictive than initialization: only exact-zero unused
// storage or the canonical reusable residue may be advanced, the physical
// source/slot identity cannot change, and the requested generation must be
// strictly newer than every V2 identity previously retained by the record.
//
// The physical source remains the sole generation authority. This helper does
// not maintain a parallel V2 counter and does not accept a terminal, linked,
// or otherwise partially recycled record.
func PrepareOperationAtGeneration(record *OperationRecord, desired OperationID) bool {
	if record == nil || !desired.Valid() {
		return false
	}
	switch record.phase {
	case operationUnused:
		if *record != (OperationRecord{}) {
			return false
		}
	case operationReusable:
		previous := record.id
		if !previous.Valid() || previous.Source() != desired.Source() || previous.Slot() != desired.Slot() ||
			desired.Generation <= previous.Generation || *record != (OperationRecord{id: previous, phase: operationReusable}) {
			return false
		}
	default:
		return false
	}
	*record = OperationRecord{id: desired, phase: operationReserved}
	return true
}

// RearmOperation is the only way to reuse a recycled physical record. The
// record retains its previous ID, advances generation internally, and refuses
// exhaustion, so a caller cannot reinitialize it with an old callback ID.
func RearmOperation(record *OperationRecord) (OperationID, bool) {
	if record == nil || record.phase != operationReusable || !record.id.Valid() ||
		record.link.park != nil || record.link.wait != nil || record.link.operation != nil || record.link.previous != nil || record.link.next != nil {
		return OperationID{}, false
	}
	next, ok := NextOperationID(record.id, record.id.Source(), record.id.Slot())
	if !ok {
		return OperationID{}, false
	}
	*record = OperationRecord{id: next, phase: operationReserved}
	return next, true
}

// AbortReservedOperation consumes an unpublished reservation generation. It
// is valid only before AttachParkOperation makes the record producer-visible;
// the next use still advances generation so a copied pre-submit ID cannot be
// accepted later.
func AbortReservedOperation(record *OperationRecord, id OperationID) bool {
	if record == nil || record.phase != operationReserved || record.id != id || !id.Valid() ||
		record.link.park != nil || record.link.wait != nil || record.link.operation != nil || record.link.previous != nil || record.link.next != nil {
		return false
	}
	*record = OperationRecord{id: id, phase: operationReusable}
	return true
}

func (record *OperationRecord) ID() (OperationID, bool) {
	if record == nil || !record.id.Valid() || record.phase == operationUnused || record.phase == operationReusable {
		return OperationID{}, false
	}
	return record.id, true
}

func (record *OperationRecord) Matches(id OperationID) bool {
	return record != nil && id.Valid() && record.id == id &&
		(record.phase == operationActive || record.phase == operationDetached)
}

func PublishOperationCompletion(record *OperationRecord, id OperationID) OperationCompletionResult {
	if record == nil || !record.Matches(id) {
		return OperationCompletionInvalid
	}
	if record.disposition == OperationDispositionWinner || record.completionPublished {
		return OperationCompletionDuplicate
	}
	if record.disposition == OperationDispositionLost || record.disposition == OperationDispositionCanceled || record.phase == operationDetached {
		return OperationCompletionLost
	}
	if record.link.park == nil || record.link.operation != record || record.link.ticket == (ParkTicket{}) {
		return OperationCompletionInvalid
	}
	record.completionPublished = true
	return OperationCompletionPublished
}

// RequestPhysicalOperationCancel asks one backend operation to stop. It does
// not choose the logical ParkState outcome: operation/context cancellation
// must also publish a ParkCancelOperation request, while select-loser cleanup
// uses only this physical request.
func RequestPhysicalOperationCancel(record *OperationRecord, id OperationID) OperationCancelResult {
	if record == nil || !record.Matches(id) || record.phase != operationActive {
		return OperationCancelInvalid
	}
	if record.disposition != OperationDispositionPending {
		return OperationCancelAlreadyTerminal
	}
	if record.cancelRequested {
		return OperationCancelAlreadyRequested
	}
	record.cancelRequested = true
	if record.completionPublished {
		return OperationCancelCompletionPending
	}
	return OperationCancelRequested
}

func OperationDispositionOf(record *OperationRecord, id OperationID) (OperationDisposition, bool) {
	if record == nil || !record.Matches(id) || record.disposition == OperationDispositionPending {
		return OperationDispositionPending, false
	}
	return record.disposition, true
}

// AcknowledgeOperationResolution is called by a source owner only after it has
// applied the logical decision: commit a winner reservation/result, or
// abort/cancel a loser. It is the protocol gate before DetachParkOperation;
// physical backend quiescence remains a separate acknowledgement.
func AcknowledgeOperationResolution(record *OperationRecord, id OperationID, disposition OperationDisposition) bool {
	if record == nil || !record.Matches(id) || record.phase != operationActive ||
		disposition == OperationDispositionPending || record.disposition != disposition || record.resolutionApplied {
		return false
	}
	record.resolutionApplied = true
	return true
}

// ConfirmOperationQuiesced records a strong backend unregister/join or the
// absence of an external producer. It neither detaches a waiter nor makes a G
// runnable.
func ConfirmOperationQuiesced(record *OperationRecord, id OperationID) bool {
	if record == nil || !record.Matches(id) || record.quiesced {
		return false
	}
	record.quiesced = true
	return true
}

func OperationCanRecycle(record *OperationRecord, id OperationID) bool {
	return record != nil && record.Matches(id) && record.phase == operationDetached && record.quiesced &&
		record.link.park == nil && record.link.wait == nil && record.link.operation == nil && record.link.previous == nil && record.link.next == nil &&
		record.disposition != OperationDispositionPending && record.resolutionApplied &&
		(record.disposition != OperationDispositionWinner || record.resultTaken)
}

// OperationResultLease is issued only by ConsumeParkSet. A resumed wrapper
// presents it after copying the source-owned winner payload; an OperationID
// alone is intentionally insufficient to release the result.
type OperationResultLease struct {
	id     OperationID
	ticket ParkTicket
}

func (lease OperationResultLease) Valid() bool {
	return lease.id.Valid() && lease.ticket != (ParkTicket{})
}

func (lease OperationResultLease) ID() (OperationID, bool) {
	if !lease.Valid() {
		return OperationID{}, false
	}
	return lease.id, true
}

// TakeOperationResult ends the winner's source-owned result lease. Losers
// have no result lease; detach plus quiescence is sufficient for them.
func TakeOperationResult(record *OperationRecord, lease OperationResultLease) bool {
	if record == nil || !lease.Valid() || !record.Matches(lease.id) || record.phase != operationDetached ||
		record.disposition != OperationDispositionWinner || !record.resultConsumable || record.resultTaken || record.resultTicket != lease.ticket {
		return false
	}
	record.resultTaken = true
	return true
}

func RecycleOperation(record *OperationRecord, id OperationID) bool {
	if !OperationCanRecycle(record, id) {
		return false
	}
	last := record.id
	*record = OperationRecord{id: last, phase: operationReusable}
	return true
}
