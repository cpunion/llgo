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

// ParkTicket identifies one logical park generation owned by one stable G.
// It never crosses a producer ABI or a cross-thread cancellation queue and is
// intentionally independent of every physical OperationID registered for a
// select/wait-set. Cross-thread cancellation enters through a durable source;
// the owner P alone resolves it against the G's current ParkTicket.
//
// Two explicit uint32 words preserve 32-bit/WASM ABI alignment without a
// uint64 atomic dependency. generation never wraps within an epoch, and epoch
// never wraps at all, so a delayed owner-side ticket cannot alias a later park.
type ParkTicket struct {
	epoch      uint32
	generation uint32
}

func validParkTicket(ticket ParkTicket) bool {
	return ticket.generation != 0
}

// Valid reports whether ticket identifies one live park generation. ParkTicket
// is deliberately opaque outside this package; adapters must use this semantic
// predicate instead of comparing its current physical fields with a zero
// struct. The latter needlessly exposes an internal layout and may lower as an
// aggregate comparison in a stackless coroutine body.
func (ticket ParkTicket) Valid() bool {
	return validParkTicket(ticket)
}

func nextParkTicket(previous ParkTicket) (ParkTicket, bool) {
	if previous == (ParkTicket{}) {
		return ParkTicket{generation: 1}, true
	}
	if !validParkTicket(previous) {
		return ParkTicket{}, false
	}
	if previous.generation != ^uint32(0) {
		previous.generation++
		return previous, true
	}
	if previous.epoch == ^uint32(0) {
		return ParkTicket{}, false
	}
	return ParkTicket{epoch: previous.epoch + 1, generation: 1}, true
}

type parkPhase uint8

const (
	parkIdle parkPhase = iota
	parkPreparing
	parkSealed
	parkParked
	parkDetaching
	parkReady
	// parkMaterialized is a source-neutral runnable park. The old owner copied
	// or discarded the winner result into a frame-local ResumePacket and
	// recycled the exact source generation before entering this phase.
	parkMaterialized
	parkConsumed
	// parkDelivered records that the scheduler's pre-resume gate transferred
	// the logical outcome into its transient RunDecision. Keeping this distinct
	// from parkConsumed prevents a later yield from replaying the old outcome.
	parkDelivered
)

// ParkOutcome is the single logical terminal decision for a wait-set.
type ParkOutcome uint8

const (
	ParkOutcomePending ParkOutcome = iota
	ParkOutcomeCompleted
	ParkOutcomeCanceled
	ParkOutcomeDefault
)

// MaxSelectOperationCases matches the Go 1.26 runtime.selectgo limit on
// send+receive operations. A compiler-lowered default is represented
// separately and therefore does not consume this physical-operation budget.
// reflect.Select applies its separate len(cases) limit before entering core.
const MaxSelectOperationCases uint32 = 1 << 16

// ParkCancelKind separates an API/operation cancellation that still races a
// completed result from task/shutdown abort, which must detach every source
// and transfer control to cleanup instead of resuming the selected case.
type ParkCancelKind uint8

const (
	ParkCancelNone ParkCancelKind = iota
	ParkCancelOperation
	ParkCancelTaskAbort
	ParkCancelShutdown
)

// ParkClaimResult distinguishes a selected completion, an ordinary select
// loser, and a stale/corrupt operation identity.
type ParkClaimResult uint8

const (
	ParkClaimInvalid ParkClaimResult = iota
	ParkClaimWon
	ParkClaimLost
)

// ParkLink is embedded in source-owned OperationRecord storage. While
// attached it is the only source-to-ParkState pointer. Detach removes it from
// the list and clears all pointer fields before decrementing the ready barrier.
type ParkLink struct {
	park      *ParkState
	wait      *WaitSetRecord
	operation *OperationRecord
	previous  *ParkLink
	next      *ParkLink
	ticket    ParkTicket
	caseID    uint32
	rank      uint32
}

// ParkState is intended to be embedded in stable G storage. One G owns at
// most one live logical wait-set. expected/attached make early completion,
// N-way select, cancellation, and the detach-to-ready barrier explicit. During
// detaching, attached itself is the remaining barrier count; a duplicate
// counter would add state without carrying independent information.
//
// seed is phase-overlaid without changing the cross-target layout: Preparing
// uses it to assign immutable candidate ranks; Seal sorts the intrusive list
// and resets it; Parked resolution uses it as the current snapshot's exact
// candidate-visit count. resolving occupies existing scalar padding and freezes
// every owner-side publication/cancellation entry while the resumable resolver
// spans host entries; it does not change the 32-bit/WASM or native layout.
// While a ReadyThenTryCommit request is outstanding, the otherwise-terminal
// winnerRecord/winnerID pair is only the exact source handshake marker. The
// private resolver cursor retains scan/settle continuation and tentative
// irreversible/reservable winners.
// All ParkState and ParkLink operations are strictly owner-P-only.
type ParkState struct {
	ticket          ParkTicket
	phase           parkPhase
	hasDefault      bool
	resolving       bool
	expected        uint32
	attached        uint32
	seed            uint32
	taskCancelKind  TaskCancelKind
	taskCancelPhase taskCancelPhase
	cancelKind      ParkCancelKind
	outcome         ParkOutcome
	winnerCase      uint32
	winnerID        OperationID
	winnerRecord    *OperationRecord
	head            *ParkLink
}

func validPendingParkCommitCursor(state *ParkState) bool {
	if state == nil {
		return false
	}
	if state.winnerRecord == nil {
		return state.winnerID == (OperationID{})
	}
	record := state.winnerRecord
	return state.winnerID == record.id && record.id.Valid() && record.phase == operationActive &&
		record.disposition == OperationDispositionPending && record.link.park == state &&
		record.link.operation == record && record.link.ticket == state.ticket &&
		operationCandidateMode(record) == OperationCommitReadyThenTryCommit &&
		operationCandidateState(record) == OperationCommitReady && operationCandidateIsPublished(record) &&
		validParkTicket(record.resultTicket) && record.resultState == operationResultEmpty
}

func validMaterializedParkHeader(state *ParkState) bool {
	if state == nil || !validParkTicket(state.ticket) || state.expected != 0 || state.attached != 0 ||
		state.seed != 0 || state.hasDefault || state.cancelKind != ParkCancelNone ||
		state.winnerID != (OperationID{}) || state.winnerRecord != nil || state.head != nil {
		return false
	}
	switch state.outcome {
	case ParkOutcomeCompleted, ParkOutcomeDefault:
		return state.winnerCase != 0
	case ParkOutcomeCanceled:
		return state.winnerCase == 0
	default:
		return false
	}
}

func validParkState(state *ParkState) bool {
	if state == nil || state.resolving || !validTaskCancelState(state.taskCancelKind, state.taskCancelPhase) || state.cancelKind > ParkCancelShutdown ||
		state.attached > state.expected {
		return false
	}
	links := uint32(0)
	var previous *ParkLink
	var firstPublished *OperationRecord
	for link := state.head; link != nil; link = link.next {
		links++
		if links > state.expected || link.park != state || link.operation == nil || &link.operation.link != link || link.operation.link.park != state ||
			link.operation.link.operation != link.operation || link.ticket != state.ticket ||
			link.operation.phase != operationActive || !validOperationCandidate(link.operation) || link.previous != previous ||
			(link.next != nil && link.next.previous != link) {
			return false
		}
		if previous != nil && (state.phase == parkSealed || state.phase == parkParked) &&
			previous.rank >= link.rank {
			return false
		}
		if firstPublished == nil && operationCandidateIsPublished(link.operation) {
			firstPublished = link.operation
		}
		if link.wait != nil && (link.wait.g == nil || &link.wait.g.park != state || link.wait.ticket != state.ticket ||
			link.wait.state == waitSetRecordUnused) {
			return false
		}
		switch state.phase {
		case parkPreparing, parkSealed, parkParked:
			if link.operation.disposition != OperationDispositionPending || link.operation.resolutionApplied ||
				!operationCandidatePendingResultStorageValid(link.operation) ||
				!operationCandidatePendingForResolution(link.operation) {
				return false
			}
		case parkDetaching:
			switch state.outcome {
			case ParkOutcomeCompleted:
				if link.operation.id == state.winnerID {
					if link.caseID != state.winnerCase || link.operation.disposition != OperationDispositionWinner ||
						link.operation.resultTicket != link.ticket || link.operation.resultState != operationResultOwned {
						return false
					}
				} else if link.operation.disposition != OperationDispositionLost || !link.operation.cancelRequested ||
					link.operation.resultTicket != (ParkTicket{}) || !operationUnselectedResultStateValid(link.operation) {
					return false
				}
			case ParkOutcomeDefault:
				if link.operation.disposition != OperationDispositionLost || !link.operation.cancelRequested ||
					link.operation.resultTicket != (ParkTicket{}) || !operationUnselectedResultStateValid(link.operation) {
					return false
				}
			case ParkOutcomeCanceled:
				if link.operation.disposition != OperationDispositionCanceled || !link.operation.cancelRequested ||
					link.operation.resultTicket != (ParkTicket{}) || !operationUnselectedResultStateValid(link.operation) {
					return false
				}
			default:
				return false
			}
			if !operationCandidateSettledForDisposition(link.operation, link.operation.disposition) {
				return false
			}
		}
		previous = link
	}
	if links != state.attached {
		return false
	}
	switch state.phase {
	case parkIdle:
		return state.ticket == (ParkTicket{}) && state.expected == 0 && state.attached == 0 &&
			state.seed == 0 && !state.hasDefault && state.cancelKind == ParkCancelNone && state.outcome == ParkOutcomePending && state.winnerCase == 0 && state.winnerID == (OperationID{}) &&
			state.winnerRecord == nil && state.head == nil
	case parkPreparing:
		return validParkTicket(state.ticket) && state.attached <= state.expected &&
			state.outcome == ParkOutcomePending && (state.hasDefault || state.winnerCase == 0) &&
			state.winnerID == (OperationID{}) && state.winnerRecord == nil
	case parkSealed:
		return validParkTicket(state.ticket) && state.attached == state.expected &&
			state.seed == 0 && state.outcome == ParkOutcomePending && (state.hasDefault || state.winnerCase == 0) &&
			state.winnerID == (OperationID{}) && state.winnerRecord == nil
	case parkParked:
		return validParkTicket(state.ticket) && state.attached == state.expected &&
			state.outcome == ParkOutcomePending && (state.hasDefault || state.winnerCase == 0) &&
			validPendingParkCommitCursor(state) &&
			(state.winnerRecord == nil || firstPublished == state.winnerRecord)
	case parkDetaching:
		if !validParkTicket(state.ticket) || state.attached == 0 ||
			state.outcome == ParkOutcomePending {
			return false
		}
		return (state.outcome == ParkOutcomeCompleted && !state.hasDefault && state.cancelKind < ParkCancelTaskAbort && state.winnerID.Valid() &&
			state.winnerRecord != nil && state.winnerRecord.id == state.winnerID) ||
			(state.outcome == ParkOutcomeCanceled && !state.hasDefault && state.cancelKind != ParkCancelNone && state.winnerCase == 0 &&
				state.winnerID == (OperationID{}) && state.winnerRecord == nil) ||
			(state.outcome == ParkOutcomeDefault && state.hasDefault && state.cancelKind == ParkCancelNone &&
				state.winnerID == (OperationID{}) && state.winnerRecord == nil)
	case parkReady:
		return validParkTicket(state.ticket) && state.attached == 0 && state.head == nil &&
			((state.outcome == ParkOutcomeCompleted && !state.hasDefault && state.cancelKind < ParkCancelTaskAbort && state.winnerID.Valid() && state.winnerRecord != nil &&
				state.winnerRecord.id == state.winnerID && state.winnerRecord.phase == operationDetached &&
				state.winnerRecord.resultTicket == state.ticket && state.winnerRecord.resultState == operationResultOwned &&
				operationCandidateSettledForDisposition(state.winnerRecord, OperationDispositionWinner)) ||
				(state.outcome == ParkOutcomeCanceled && !state.hasDefault && state.cancelKind != ParkCancelNone && state.winnerCase == 0 &&
					state.winnerID == (OperationID{}) && state.winnerRecord == nil) ||
				(state.outcome == ParkOutcomeDefault && state.hasDefault && state.cancelKind == ParkCancelNone &&
					state.winnerID == (OperationID{}) && state.winnerRecord == nil))
	case parkMaterialized:
		return validMaterializedParkHeader(state)
	case parkConsumed:
		return validParkTicket(state.ticket) && state.attached == 0 && state.head == nil &&
			((state.outcome == ParkOutcomeCompleted && !state.hasDefault && state.cancelKind < ParkCancelTaskAbort && state.winnerID.Valid() && state.winnerRecord == nil) ||
				(state.outcome == ParkOutcomeCanceled && !state.hasDefault && state.cancelKind != ParkCancelNone && state.winnerCase == 0 &&
					state.winnerID == (OperationID{}) && state.winnerRecord == nil) ||
				(state.outcome == ParkOutcomeDefault && state.hasDefault && state.cancelKind == ParkCancelNone &&
					state.winnerID == (OperationID{}) && state.winnerRecord == nil))
	case parkDelivered:
		return validParkTicket(state.ticket) && state.expected == 0 && state.attached == 0 && state.seed == 0 &&
			!state.hasDefault && state.cancelKind == ParkCancelNone && state.outcome == ParkOutcomePending && state.winnerCase == 0 &&
			state.winnerID == (OperationID{}) && state.winnerRecord == nil && state.head == nil
	default:
		return false
	}
}

func releasableParkState(state *ParkState) bool {
	if !validParkState(state) {
		return false
	}
	return state.phase == parkIdle || state.phase == parkConsumed || state.phase == parkDelivered
}

func materializedParkState(state *ParkState, ticket ParkTicket, outcome ParkOutcome, caseID uint32) bool {
	if state == nil || !validParkState(state) || state.phase != parkConsumed || state.ticket != ticket ||
		outcome == ParkOutcomePending || outcome == ParkOutcomeCanceled && caseID != 0 ||
		(outcome == ParkOutcomeCompleted || outcome == ParkOutcomeDefault) && caseID == 0 {
		return false
	}
	kind, phase := state.taskCancelKind, state.taskCancelPhase
	*state = ParkState{
		ticket:          ticket,
		phase:           parkMaterialized,
		taskCancelKind:  kind,
		taskCancelPhase: phase,
		outcome:         outcome,
		winnerCase:      caseID,
	}
	return validParkState(state)
}

func deliverMaterializedParkResume(state *ParkState, ticket ParkTicket) bool {
	if !validParkState(state) || state.phase != parkMaterialized || state.ticket != ticket {
		return false
	}
	kind, phase := state.taskCancelKind, state.taskCancelPhase
	*state = ParkState{
		ticket:          ticket,
		phase:           parkDelivered,
		taskCancelKind:  kind,
		taskCancelPhase: phase,
	}
	return validParkState(state)
}

// BeginParkSet starts one logical N-candidate wait. The two-word ticket only
// advances from a fully consumed, pointer-free state and fails closed at full
// exhaustion; it never aliases an old owner-side ticket.
func BeginParkSet(state *ParkState, expected, seed uint32) (ParkTicket, bool) {
	if state == nil || expected > MaxSelectOperationCases {
		return ParkTicket{}, false
	}
	if state.phase == parkIdle {
		if !validParkState(state) {
			return ParkTicket{}, false
		}
	} else if (state.phase != parkConsumed && state.phase != parkDelivered) || !validParkState(state) ||
		state.attached != 0 || state.head != nil {
		return ParkTicket{}, false
	}
	ticket, ok := nextParkTicket(state.ticket)
	if !ok {
		return ParkTicket{}, false
	}
	*state = ParkState{
		ticket:          ticket,
		phase:           parkPreparing,
		expected:        expected,
		seed:            seed ^ ticket.generation*0x9e3779b9 ^ ticket.epoch*0x85ebca6b,
		taskCancelKind:  state.taskCancelKind,
		taskCancelPhase: state.taskCancelPhase,
	}
	return ticket, true
}

// parkCaseRank is a keyed permutation of uint32 case IDs. For one seed,
// distinct case IDs therefore have distinct ranks, and resolution can compare
// ranks without falling back to source or fact order.
func parkCaseRank(seed, caseID uint32) uint32 {
	x := caseID ^ seed
	x ^= x >> 16
	x *= 0x7feb352d
	x ^= x >> 15
	x *= 0x846ca68b
	x ^= x >> 16
	return x
}

func validPreparingParkStateHeader(state *ParkState, ticket ParkTicket) bool {
	return state != nil && state.phase == parkPreparing && state.ticket == ticket && validParkTicket(ticket) &&
		validTaskCancelState(state.taskCancelKind, state.taskCancelPhase) && state.cancelKind <= ParkCancelShutdown &&
		state.attached <= state.expected && state.outcome == ParkOutcomePending && (state.hasDefault || state.winnerCase == 0) &&
		state.winnerID == (OperationID{}) &&
		state.winnerRecord == nil && (state.attached == 0) == (state.head == nil) &&
		(state.head == nil || state.head.previous == nil)
}

// SetParkDefault records a compiler-provided default continuation without
// allocating or attaching a synthetic physical operation. The default case is
// considered only after every ready candidate with a better or worse seeded
// rank has either failed TryCommit or disappeared from this exact snapshot.
func SetParkDefault(state *ParkState, ticket ParkTicket, caseID uint32) bool {
	if !validPreparingParkStateHeader(state, ticket) || state.hasDefault {
		return false
	}
	for link := state.head; link != nil; link = link.next {
		if link.caseID == caseID {
			return false
		}
	}
	state.hasDefault = true
	state.winnerCase = caseID
	return validPreparingParkStateHeader(state, ticket)
}

func BeginParkSetWithDefault(state *ParkState, expected, seed, defaultCaseID uint32) (ParkTicket, bool) {
	if expected > MaxSelectOperationCases {
		return ParkTicket{}, false
	}
	ticket, ok := BeginParkSet(state, expected, seed)
	if !ok || !SetParkDefault(state, ticket, defaultCaseID) {
		return ParkTicket{}, false
	}
	return ticket, true
}

func attachParkOperation(state *ParkState, ticket ParkTicket, wait *WaitSetRecord, record *OperationRecord, caseID uint32) bool {
	validState := wait != nil && validPreparingParkStateHeader(state, ticket) || wait == nil && validParkState(state)
	if !validState || state.phase != parkPreparing || ticket != state.ticket ||
		!validParkTicket(ticket) || state.attached >= state.expected || record == nil || record.phase != operationReserved ||
		!record.id.Valid() || record.disposition != OperationDispositionPending || record.link.park != nil || record.link.wait != nil ||
		record.link.operation != nil || record.link.previous != nil || record.link.next != nil ||
		state.hasDefault && state.winnerCase == caseID {
		return false
	}
	if wait != nil && !validPreparingWaitSetRecord(wait, state, ticket) {
		return false
	}
	if wait == nil {
		for link := state.head; link != nil; link = link.next {
			if link.caseID == caseID || link.operation.id == record.id {
				return false
			}
		}
	}
	record.link = ParkLink{
		park:      state,
		wait:      wait,
		operation: record,
		next:      state.head,
		ticket:    ticket,
		caseID:    caseID,
		rank:      parkCaseRank(state.seed, caseID),
	}
	if state.head != nil {
		state.head.previous = &record.link
	}
	record.phase = operationActive
	state.head = &record.link
	state.attached++
	validAttached := false
	if wait == nil {
		validAttached = validParkState(state)
	} else {
		validAttached = validPreparingParkStateHeader(state, ticket) && state.head == &record.link &&
			record.link.park == state && record.link.wait == wait && record.link.operation == record &&
			record.link.ticket == ticket && record.phase == operationActive
	}
	if !validAttached {
		if record.link.next != nil {
			record.link.next.previous = nil
		}
		state.head = record.link.next
		state.attached--
		record.link = ParkLink{}
		record.phase = operationReserved
		return false
	}
	return true
}

func AttachParkOperation(state *ParkState, ticket ParkTicket, record *OperationRecord, caseID uint32) bool {
	return attachParkOperation(state, ticket, nil, record, caseID)
}

// AttachParkWaitOperation associates a physical operation with the transient
// frame-local record used by scheduler-integrated V2 promotion. Pure logical
// ParkState tests may continue to use AttachParkOperation without a record.
// Candidate case-ID uniqueness is a compiler/preparation preflight invariant;
// avoiding a repeated link scan keeps N candidate attachments O(N). SealParkSet
// performs the one complete structural audit before scheduler commit.
func AttachParkWaitOperation(state *ParkState, ticket ParkTicket, wait *WaitSetRecord, record *OperationRecord, caseID uint32) bool {
	return attachParkOperation(state, ticket, wait, record, caseID)
}

// sortParkLinksByRank performs an allocation-free bottom-up merge sort over
// the existing intrusive links. Attach remains O(1) on the production
// record-aware path; Seal pays O(N log N) once so commit retries can advance a
// single monotonic cursor instead of selecting the next rank with O(N) rescans.
func sortParkLinksByRank(state *ParkState) bool {
	if state == nil || state.phase != parkPreparing || state.attached != state.expected ||
		(state.attached == 0) != (state.head == nil) {
		return false
	}
	for width := uint32(1); width < state.attached; {
		remaining := state.head
		var sortedHead, sortedTail *ParkLink
		for remaining != nil {
			left := remaining
			leftCount := uint32(0)
			right := left
			for leftCount < width && right != nil {
				right = right.next
				leftCount++
			}
			rightCount := uint32(0)
			nextRun := right
			for rightCount < width && nextRun != nil {
				nextRun = nextRun.next
				rightCount++
			}

			for leftCount != 0 || rightCount != 0 {
				fromLeft := rightCount == 0 || leftCount != 0 && left.rank < right.rank
				var selected *ParkLink
				if fromLeft {
					selected = left
					left = left.next
					leftCount--
				} else {
					selected = right
					right = right.next
					rightCount--
				}
				selected.previous = sortedTail
				if sortedTail == nil {
					sortedHead = selected
				} else {
					sortedTail.next = selected
				}
				sortedTail = selected
			}
			remaining = nextRun
		}
		if sortedTail == nil {
			return false
		}
		sortedTail.next = nil
		state.head = sortedHead
		if width > state.attached/2 {
			break
		}
		width *= 2
	}
	return state.head == nil || state.head.previous == nil
}

func SealParkSet(state *ParkState, ticket ParkTicket) bool {
	if !validParkState(state) || state.phase != parkPreparing || ticket != state.ticket || state.attached != state.expected {
		return false
	}
	if !sortParkLinksByRank(state) {
		return false
	}
	// Record-aware O(1) attachment deliberately defers case/rank uniqueness.
	// Preflight it while the phase and mixed seed still describe a valid,
	// abortable Preparing state; failure must not strand producer-visible links
	// in an invalid Sealed state.
	var previous *ParkLink
	for link := state.head; link != nil; link = link.next {
		if previous != nil && previous.rank >= link.rank {
			return false
		}
		previous = link
	}
	// Every rank is now retained in its ParkLink, so the mixed preparation seed
	// can become the exact per-snapshot visit count without adding ParkState
	// storage. A duplicate case/rank is rejected by the sealed invariant.
	state.seed = 0
	state.phase = parkSealed
	return validParkState(state)
}

// CommitParkSet represents the scheduler accepting the exact logical ticket
// as the current G park. Completions may already be published in operation
// records, but they are resolved only after this transition.
func CommitParkSet(state *ParkState, ticket ParkTicket) bool {
	if !validParkState(state) || state.phase != parkSealed || ticket != state.ticket {
		return false
	}
	if state.taskCancelPhase == taskCancelRequested &&
		!RequestParkCancel(state, ticket, taskCancelParkKind(state.taskCancelKind)) {
		return false
	}
	state.phase = parkParked
	return true
}

func RequestParkCancel(state *ParkState, ticket ParkTicket, kind ParkCancelKind) bool {
	if state == nil || state.resolving || !validParkState(state) || ticket != state.ticket ||
		(state.phase != parkPreparing && state.phase != parkSealed && state.phase != parkParked) ||
		kind < ParkCancelOperation || kind > ParkCancelShutdown ||
		state.phase == parkParked && state.winnerRecord != nil {
		return false
	}
	if kind <= state.cancelKind {
		return true
	}
	state.cancelKind = kind
	return true
}

func ParkCancelKindOf(state *ParkState, ticket ParkTicket) (ParkCancelKind, bool) {
	if !validParkState(state) || ticket != state.ticket || state.cancelKind == ParkCancelNone {
		return ParkCancelNone, false
	}
	return state.cancelKind, true
}

// AbortParkSet transactionally fails a preparation that has not been committed
// to GWaiting. It remains a terminal path after a partially attached source
// publishes an early completion: the source retains and explicitly discards
// that result while applying Canceled, then detaches through the normal barrier.
// This prevents a later candidate admission/submission failure from stranding
// the ParkState in Preparing. A source whose completed side effect cannot be
// discarded must reserve all fallible resources before producer admission.
// A zero-candidate abort becomes ready immediately for owner consumption.
func AbortParkSet(state *ParkState, ticket ParkTicket) bool {
	if !validParkState(state) || ticket != state.ticket ||
		(state.phase != parkPreparing && state.phase != parkSealed) {
		return false
	}
	if state.cancelKind == ParkCancelNone {
		state.cancelKind = ParkCancelOperation
	}
	state.hasDefault = false
	state.winnerCase = 0
	state.outcome = ParkOutcomeCanceled
	for link := state.head; link != nil; link = link.next {
		if !rollBackOperationCandidate(link.operation) {
			return false
		}
		link.operation.cancelRequested = true
		link.operation.disposition = OperationDispositionCanceled
	}
	if state.attached == 0 {
		state.phase = parkReady
	} else {
		state.phase = parkDetaching
	}
	return validParkState(state)
}

func ParkReady(state *ParkState, ticket ParkTicket) bool {
	return validParkState(state) && state.phase == parkReady && ticket == state.ticket
}

func ParkWinner(state *ParkState, ticket ParkTicket) (caseID uint32, id OperationID, ok bool) {
	if !validParkState(state) || ticket != state.ticket || state.outcome != ParkOutcomeCompleted || !state.winnerID.Valid() ||
		(state.phase != parkDetaching && state.phase != parkReady) {
		return 0, OperationID{}, false
	}
	return state.winnerCase, state.winnerID, true
}

func ParkOperationClaim(record *OperationRecord, id OperationID) ParkClaimResult {
	if record == nil || !record.Matches(id) || record.disposition == OperationDispositionPending {
		return ParkClaimInvalid
	}
	if record.disposition == OperationDispositionWinner {
		return ParkClaimWon
	}
	return ParkClaimLost
}

// DetachParkOperation clears the only physical-source pointer path to the
// logical wait before publishing the ready transition. Physical quiescence is
// intentionally not required here.
func detachParkOperation(state *ParkState, ticket ParkTicket, record *OperationRecord, id OperationID, fast bool) bool {
	validState := fast && validActiveParkStateHeader(state, ticket) || !fast && validParkState(state)
	if !validState || state.phase != parkDetaching || ticket != state.ticket ||
		record == nil || !record.Matches(id) || record.phase != operationActive || record.disposition == OperationDispositionPending ||
		!record.resolutionApplied || record.link.park != state || record.link.operation != record || record.link.ticket != ticket {
		return false
	}
	link := &record.link
	if fast && link.wait == nil {
		return false
	}
	previous, next := link.previous, link.next
	if previous == nil {
		if state.head != link {
			return false
		}
	} else if previous.next != link {
		return false
	}
	if next != nil && next.previous != link {
		return false
	}
	if previous == nil {
		state.head = next
	} else {
		previous.next = next
	}
	if next != nil {
		next.previous = previous
	}
	record.phase = operationDetached
	record.link = ParkLink{}
	state.attached--
	if state.attached == 0 {
		if state.head != nil {
			return false
		}
		state.phase = parkReady
	}
	if fast {
		return validActiveParkStateHeader(state, ticket)
	}
	return validParkState(state)
}

func DetachParkOperation(state *ParkState, ticket ParkTicket, record *OperationRecord, id OperationID) bool {
	return detachParkOperation(state, ticket, record, id, false)
}

// DetachParkWaitOperation is the O(1) scheduler-integrated detach path. Its
// transient ParkLink carries the predecessor, and the complete wait-set was
// already audited once by published-epoch resolution.
func DetachParkWaitOperation(state *ParkState, ticket ParkTicket, record *OperationRecord, id OperationID) bool {
	return detachParkOperation(state, ticket, record, id, true)
}

func ConsumeParkSet(state *ParkState, ticket ParkTicket) (outcome ParkOutcome, caseID uint32, lease OperationResultLease, ok bool) {
	if !validParkState(state) || state.phase != parkReady || ticket != state.ticket {
		return ParkOutcomePending, 0, OperationResultLease{}, false
	}
	outcome = state.outcome
	if state.taskCancelPhase != taskCancelRequested {
		caseID = state.winnerCase
	} else {
		// A task stop that arrives after winner resolution cannot undo the
		// physical completion. It does suppress the selected continuation; the
		// lease below lets cleanup discard/copy the winner payload before recycle.
		outcome = ParkOutcomeCanceled
	}
	if state.outcome == ParkOutcomeCompleted {
		if state.winnerRecord == nil || state.winnerRecord.id != state.winnerID || state.winnerRecord.phase != operationDetached ||
			state.winnerRecord.resultState != operationResultOwned {
			return ParkOutcomePending, 0, OperationResultLease{}, false
		}
		state.winnerRecord.resultState = operationResultLeased
		lease = OperationResultLease{id: state.winnerID, ticket: ticket}
		state.winnerRecord = nil
	}
	state.phase = parkConsumed
	return outcome, caseID, lease, true
}

// DeliverParkResume is the scheduler-side acknowledgement that a consumed
// park outcome has been copied into the transient pre-resume decision. Direct
// unit/runtime consumers may begin the next park from parkConsumed; scheduler
// integration uses parkDelivered so an unrelated later yield cannot replay the
// old case or result lease.
func DeliverParkResume(state *ParkState, ticket ParkTicket) bool {
	if !validParkState(state) || state.phase != parkConsumed || ticket != state.ticket {
		return false
	}
	kind, phase := state.taskCancelKind, state.taskCancelPhase
	*state = ParkState{
		ticket:          ticket,
		phase:           parkDelivered,
		taskCancelKind:  kind,
		taskCancelPhase: phase,
	}
	return validParkState(state)
}
