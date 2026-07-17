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
	operationRouteBits  = 9
	operationLocalBits  = 32 - operationSourceBits - operationRouteBits
	operationRouteMask  = uint32(1<<operationRouteBits - 1)
	operationLocalMask  = uint32(1<<operationLocalBits - 1)
	// operationSlotMask is retained for the route-1 compatibility API. A slot
	// is now local to one route rather than globally identifying an owner.
	operationSlotMask = operationLocalMask
)

// RouteID identifies one executor/source catalog in the pointer-free
// OperationID namespace. Zero is invalid. The wire codec reserves 511 routes;
// a target profile may expose a smaller fixed route registry without changing
// producer handles or reusing an ID it has already issued.
type RouteID uint16

func (route RouteID) Valid() bool {
	return route != 0 && uint32(route) <= operationRouteMask
}

// OperationID is the pointer-free physical source identity. SourceSlot has a
// frozen 8/9/15 split: source family, executor route, and one-based local slot.
// Generation is owned by the physical source and must not be reused until that
// source is quiescent. All four identity components are non-zero.
//
// Keeping two explicit uint32 words preserves size 8/alignment 4 on 32-bit,
// WASM, embedded, and C ABI boundaries instead of depending on uint64 layout.
type OperationID struct {
	SourceSlot uint32
	Generation uint32
}

// Keep the producer ABI exact on every target at compile time, including
// 32-bit native, WASM, and bare-metal profiles where tests cannot execute on
// the host. Both subtraction directions reject either a smaller or larger
// layout.
var (
	_ [8 - unsafe.Sizeof(OperationID{})]byte
	_ [unsafe.Sizeof(OperationID{}) - 8]byte
	_ [4 - unsafe.Alignof(OperationID{})]byte
	_ [unsafe.Alignof(OperationID{}) - 4]byte
	_ [4 - unsafe.Offsetof(OperationID{}.Generation)]byte
	_ [unsafe.Offsetof(OperationID{}.Generation) - 4]byte
)

// MakeOperationIDAtRoute constructs an exact source/route/local/generation
// identity. It is the production constructor for route-aware sources.
func MakeOperationIDAtRoute(source OperationSource, route RouteID, local, generation uint32) (OperationID, bool) {
	if !validOperationSource(source) || !route.Valid() || local == 0 || local > operationLocalMask || generation == 0 {
		return OperationID{}, false
	}
	return OperationID{
		SourceSlot: uint32(source)<<(operationRouteBits+operationLocalBits) |
			uint32(route)<<operationLocalBits | local,
		Generation: generation,
	}, true
}

// MakeOperationID is the single-route compatibility constructor. New source
// catalogs must use MakeOperationIDAtRoute and an explicitly allocated route.
func MakeOperationID(source OperationSource, local, generation uint32) (OperationID, bool) {
	return MakeOperationIDAtRoute(source, RouteID(1), local, generation)
}

func (id OperationID) Source() OperationSource {
	return OperationSource(id.SourceSlot >> (operationRouteBits + operationLocalBits))
}

func (id OperationID) Route() RouteID {
	return RouteID(id.SourceSlot >> operationLocalBits & operationRouteMask)
}

func (id OperationID) LocalSlot() uint32 {
	return id.SourceSlot & operationLocalMask
}

// Slot is the route-1 compatibility spelling for LocalSlot.
func (id OperationID) Slot() uint32 {
	return id.LocalSlot()
}

func (id OperationID) Valid() bool {
	return validOperationSource(id.Source()) && id.Route().Valid() && id.LocalSlot() != 0 && id.Generation != 0
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

// NextOperationIDAtRoute advances one exact physical slot generation.
// Exhaustion
// fails closed: a physical slot may be widened or retired, but never wraps
// while an old callback could still carry the same two POD words.
func NextOperationIDAtRoute(previous OperationID, source OperationSource, route RouteID, local uint32) (OperationID, bool) {
	if previous == (OperationID{}) {
		return MakeOperationIDAtRoute(source, route, local, 1)
	}
	if !previous.Valid() || previous.Source() != source || previous.Route() != route || previous.LocalSlot() != local || previous.Generation == ^uint32(0) {
		return OperationID{}, false
	}
	return MakeOperationIDAtRoute(source, route, local, previous.Generation+1)
}

// NextOperationID is the explicit route-1 compatibility helper.
func NextOperationID(previous OperationID, source OperationSource, local uint32) (OperationID, bool) {
	return NextOperationIDAtRoute(previous, source, RouteID(1), local)
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
	// OperationCompletionDeferred means another ReadyThen TryCommit owns this
	// ParkState snapshot. The source must retain its mailbox fact and retry it
	// in the next owner epoch; no OperationRecord field was changed.
	OperationCompletionDeferred
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
// source acknowledged the disposition and removed that link. RetryBudget and
// AwaitExternalFact both retain the exact link, but deliberately have opposite
// scheduling consequences: RetryBudget requires another executor slice,
// whereas AwaitExternalFact must stay off the affected queue until its source
// publishes the missing acknowledgement/quiescence fact. Keeping those states
// distinct prevents a physically blocked operation from busy-spinning. The
// current timer and manual sources always detach synchronously and therefore
// return neither deferred result. Any future source which returns
// AwaitExternalFact must make its later sticky acknowledgement call
// MarkWaitSetAffected for the retained WaitSetRecord.
type OperationApplyResult uint8

const (
	OperationApplyInvalid OperationApplyResult = iota
	OperationApplyDetached
	OperationApplyRetryBudget
	OperationApplyAwaitExternalFact
)

// OperationCommitMode describes how a ready select candidate becomes the one
// logical winner. The zero value preserves existing timer/manual behavior.
//
// IrreversibleCompletion means publication itself already committed the
// physical result. ReadyThenTryCommit is only a readiness hint: the resolver
// must ask the owning source to perform one synchronous exact-ID TryCommit.
// Reservable means publication owns a reversible reservation which the
// resolver can logically commit for the winner or roll back for every loser.
type OperationCommitMode uint8

const (
	OperationCommitIrreversibleCompletion OperationCommitMode = iota
	OperationCommitReadyThenTryCommit
	OperationCommitReservable
)

// OperationCommitState is an owner-side diagnostic view of the compact
// candidate byte. Committed and RolledBack record the resolver's immutable
// logical decision; the owning source still performs the corresponding
// physical effect before AcknowledgeOperationResolution and detach. This byte
// does not cross producer or platform ABIs.
type OperationCommitState uint8

const (
	OperationCommitIdle OperationCommitState = iota
	OperationCommitReady
	OperationCommitReserved
	OperationCommitCommitted
	OperationCommitRolledBack
)

const (
	operationCandidatePublished  = uint8(1 << 0)
	operationCandidateModeShift  = 1
	operationCandidateModeBits   = uint8(3 << operationCandidateModeShift)
	operationCandidateStateShift = operationCandidateModeShift + 2
	operationCandidateStateBits  = uint8(7 << operationCandidateStateShift)
)

func operationCandidateMode(record *OperationRecord) OperationCommitMode {
	if record == nil {
		return OperationCommitMode(255)
	}
	return OperationCommitMode(record.candidate&operationCandidateModeBits) >> operationCandidateModeShift
}

func operationCandidateState(record *OperationRecord) OperationCommitState {
	if record == nil {
		return OperationCommitState(255)
	}
	return OperationCommitState(record.candidate&operationCandidateStateBits) >> operationCandidateStateShift
}

func operationCandidateIsPublished(record *OperationRecord) bool {
	return record != nil && record.candidate&operationCandidatePublished != 0
}

func setOperationCandidate(record *OperationRecord, mode OperationCommitMode, state OperationCommitState, published bool) {
	record.candidate = uint8(mode)<<operationCandidateModeShift | uint8(state)<<operationCandidateStateShift
	if published {
		record.candidate |= operationCandidatePublished
	}
}

func validOperationCandidate(record *OperationRecord) bool {
	if record == nil || record.candidate&^(operationCandidatePublished|operationCandidateModeBits|operationCandidateStateBits) != 0 {
		return false
	}
	mode, state, published := operationCandidateMode(record), operationCandidateState(record), operationCandidateIsPublished(record)
	switch mode {
	case OperationCommitIrreversibleCompletion:
		return !published && state == OperationCommitIdle || published && state == OperationCommitCommitted
	case OperationCommitReadyThenTryCommit:
		return !published && state == OperationCommitIdle || published &&
			(state == OperationCommitReady || state == OperationCommitCommitted || state == OperationCommitRolledBack)
	case OperationCommitReservable:
		return !published && state == OperationCommitIdle || published &&
			(state == OperationCommitReserved || state == OperationCommitCommitted || state == OperationCommitRolledBack)
	default:
		return false
	}
}

// operationCandidatePendingForResolution is the strict shape admitted while
// a ParkState can still choose a winner. Logical commit/rollback states appear
// only after resolution has atomically frozen every candidate disposition.
func operationCandidatePendingForResolution(record *OperationRecord) bool {
	if !validOperationCandidate(record) {
		return false
	}
	mode, state, published := operationCandidateMode(record), operationCandidateState(record), operationCandidateIsPublished(record)
	switch mode {
	case OperationCommitIrreversibleCompletion:
		return !published && state == OperationCommitIdle || published && state == OperationCommitCommitted
	case OperationCommitReadyThenTryCommit:
		return !published && state == OperationCommitIdle || published && state == OperationCommitReady
	case OperationCommitReservable:
		return !published && state == OperationCommitIdle || published && state == OperationCommitReserved
	default:
		return false
	}
}

// operationCandidatePendingResultStorageValid permits ReadyThenTryCommit to
// reuse resultTicket as an owner-local readiness generation while the logical
// park is pending. A failed hint retains its generation so the next publish
// must advance it; terminal settlement clears loser tokens, while the winner
// replaces its token with the logical ParkTicket used by the result lease.
// Other candidate modes keep resultTicket at zero until they win.
func operationCandidatePendingResultStorageValid(record *OperationRecord) bool {
	if record == nil {
		return false
	}
	if operationCandidateMode(record) != OperationCommitReadyThenTryCommit {
		return record.resultTicket == (ParkTicket{})
	}
	if record.resultTicket == (ParkTicket{}) {
		return !operationCandidateIsPublished(record) && operationCandidateState(record) == OperationCommitIdle
	}
	return validParkTicket(record.resultTicket)
}

func operationCandidateSettledForDisposition(record *OperationRecord, disposition OperationDisposition) bool {
	if !validOperationCandidate(record) {
		return false
	}
	mode, state, published := operationCandidateMode(record), operationCandidateState(record), operationCandidateIsPublished(record)
	if disposition == OperationDispositionWinner {
		return published && state == OperationCommitCommitted && validParkTicket(record.resultTicket)
	}
	if disposition != OperationDispositionLost && disposition != OperationDispositionCanceled {
		return false
	}
	if record.resultTicket != (ParkTicket{}) {
		return false
	}
	switch mode {
	case OperationCommitIrreversibleCompletion:
		return !published && state == OperationCommitIdle || published && state == OperationCommitCommitted
	case OperationCommitReadyThenTryCommit, OperationCommitReservable:
		return !published && state == OperationCommitIdle || published && state == OperationCommitRolledBack
	default:
		return false
	}
}

func commitOperationCandidate(record *OperationRecord) bool {
	if !validOperationCandidate(record) || !operationCandidateIsPublished(record) {
		return false
	}
	mode, state := operationCandidateMode(record), operationCandidateState(record)
	switch mode {
	case OperationCommitIrreversibleCompletion:
		return state == OperationCommitCommitted
	case OperationCommitReadyThenTryCommit:
		if state != OperationCommitReady && state != OperationCommitCommitted {
			return false
		}
	case OperationCommitReservable:
		if state != OperationCommitReserved && state != OperationCommitCommitted {
			return false
		}
	default:
		return false
	}
	setOperationCandidate(record, mode, OperationCommitCommitted, true)
	return true
}

func rejectReadyThenTryCommitCandidate(record *OperationRecord) bool {
	if !validOperationCandidate(record) || operationCandidateMode(record) != OperationCommitReadyThenTryCommit ||
		!operationCandidateIsPublished(record) || operationCandidateState(record) != OperationCommitReady ||
		!validParkTicket(record.resultTicket) {
		return false
	}
	// Failure consumes only this exact ready hint. The operation remains active
	// and can become a candidate again solely through a later source publish.
	setOperationCandidate(record, OperationCommitReadyThenTryCommit, OperationCommitIdle, false)
	return true
}

func rollBackOperationCandidate(record *OperationRecord) bool {
	if !validOperationCandidate(record) {
		return false
	}
	mode, state, published := operationCandidateMode(record), operationCandidateState(record), operationCandidateIsPublished(record)
	switch mode {
	case OperationCommitIrreversibleCompletion:
		return !published && state == OperationCommitIdle || published && state == OperationCommitCommitted
	case OperationCommitReadyThenTryCommit:
		if !published {
			if state != OperationCommitIdle {
				return false
			}
			record.resultTicket = ParkTicket{}
			return true
		}
		if state != OperationCommitReady {
			if state != OperationCommitRolledBack {
				return false
			}
			record.resultTicket = ParkTicket{}
			return true
		}
	case OperationCommitReservable:
		if !published {
			return state == OperationCommitIdle
		}
		if state != OperationCommitReserved {
			return state == OperationCommitRolledBack
		}
	default:
		return false
	}
	setOperationCandidate(record, mode, OperationCommitRolledBack, true)
	if mode == OperationCommitReadyThenTryCommit {
		record.resultTicket = ParkTicket{}
	}
	return true
}

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
	id                OperationID
	phase             operationPhase
	disposition       OperationDisposition
	resolutionApplied bool
	candidate         uint8
	cancelRequested   bool
	quiesced          bool
	resultConsumable  bool
	resultTaken       bool
	resultTicket      ParkTicket
	link              ParkLink
}

// DeclareOperationCommitMode changes one unlinked reservation from the
// default irreversible mode. It is intentionally separate from Init/Rearm so
// existing sources retain their exact preparation API and generated sources
// can choose a mode without allocating a candidate object.
func DeclareOperationCommitMode(record *OperationRecord, mode OperationCommitMode) bool {
	if record == nil || record.phase != operationReserved || !record.id.Valid() || record.disposition != OperationDispositionPending ||
		record.candidate != 0 || record.link != (ParkLink{}) || mode > OperationCommitReservable {
		return false
	}
	setOperationCandidate(record, mode, OperationCommitIdle, false)
	return true
}

func OperationCommitModeOf(record *OperationRecord, id OperationID) (OperationCommitMode, bool) {
	if record == nil || !record.Matches(id) || !validOperationCandidate(record) {
		return OperationCommitIrreversibleCompletion, false
	}
	return operationCandidateMode(record), true
}

func OperationCommitStateOf(record *OperationRecord, id OperationID) (OperationCommitState, bool) {
	if record == nil || !record.Matches(id) || !validOperationCandidate(record) {
		return OperationCommitIdle, false
	}
	return operationCandidateState(record), true
}

func InitOperation(record *OperationRecord, id OperationID) bool {
	if record == nil || !id.Valid() || id.Generation != 1 || record.phase != operationUnused || record.id != (OperationID{}) ||
		record.candidate != 0 ||
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
		if !previous.Valid() || previous.SourceSlot != desired.SourceSlot ||
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
	next, ok := NextOperationIDAtRoute(record.id, record.id.Source(), record.id.Route(), record.id.LocalSlot())
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

func publishOperationCandidate(record *OperationRecord, id OperationID, mode OperationCommitMode, state OperationCommitState) OperationCompletionResult {
	if record == nil || !record.Matches(id) {
		return OperationCompletionInvalid
	}
	if !validOperationCandidate(record) || operationCandidateMode(record) != mode {
		return OperationCompletionInvalid
	}
	if record.disposition == OperationDispositionWinner || operationCandidateIsPublished(record) {
		return OperationCompletionDuplicate
	}
	if record.disposition == OperationDispositionLost || record.disposition == OperationDispositionCanceled || record.phase == operationDetached {
		return OperationCompletionLost
	}
	if record.link.park == nil || record.link.operation != record || record.link.ticket == (ParkTicket{}) {
		return OperationCompletionInvalid
	}
	// A bounded logical resolution owns a frozen source snapshot across host
	// entries. Retain a newly publishable re-entrant/behind-cursor fact in its
	// source mailbox; already-published and terminal facts keep their stable
	// Duplicate/Lost classification above.
	if record.link.park.phase == parkParked && record.link.park.resolving {
		return OperationCompletionDeferred
	}
	if mode == OperationCommitReadyThenTryCommit {
		readyTicket, ok := nextParkTicket(record.resultTicket)
		if !ok {
			return OperationCompletionInvalid
		}
		record.resultTicket = readyTicket
	}
	setOperationCandidate(record, mode, state, true)
	return OperationCompletionPublished
}

func PublishOperationCompletion(record *OperationRecord, id OperationID) OperationCompletionResult {
	return publishOperationCandidate(record, id, OperationCommitIrreversibleCompletion, OperationCommitCommitted)
}

// PublishReadyThenTryCommitCandidate publishes only a readiness hint. The
// owner resolver later returns an exact ParkCommitRequest to the source; no
// irreversible effect may occur in this call. Every accepted republish first
// advances the record's non-wrapping readiness generation.
func PublishReadyThenTryCommitCandidate(record *OperationRecord, id OperationID) OperationCompletionResult {
	return publishOperationCandidate(record, id, OperationCommitReadyThenTryCommit, OperationCommitReady)
}

// PublishReservableCandidate publishes one source-owned reversible
// reservation. The resolver freezes its commit/rollback decision before any
// source applies and detaches the resolved wait-set.
func PublishReservableCandidate(record *OperationRecord, id OperationID) OperationCompletionResult {
	return publishOperationCandidate(record, id, OperationCommitReservable, OperationCommitReserved)
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
	if record.link.park != nil && record.link.park.resolving {
		return OperationCancelInvalid
	}
	if record.cancelRequested {
		return OperationCancelAlreadyRequested
	}
	record.cancelRequested = true
	if operationCandidateIsPublished(record) {
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
		disposition == OperationDispositionPending || record.disposition != disposition || record.resolutionApplied ||
		!operationCandidateSettledForDisposition(record, disposition) {
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
		operationCandidateSettledForDisposition(record, record.disposition) &&
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
