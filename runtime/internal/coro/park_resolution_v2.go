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

// CompletionResolution is intentionally a value summary rather than an event
// buffer. Source-owned OperationRecord storage retains every completion as a
// sticky fact until the owner P resolves the corresponding logical park.
// WaitSets is one for every valid snapshot examined, including one that is
// still pending; Completed+Canceled+Defaulted says whether that snapshot was
// resolved.
type CompletionResolution struct {
	WaitSets  uint32
	Completed uint32
	Canceled  uint32
	Defaulted uint32
	Winners   uint32
	Losers    uint32
}

type ParkResolveStatus uint8

const (
	ParkResolveInvalid ParkResolveStatus = iota
	ParkResolvePending
	ParkResolveNeedsCommit
	ParkResolveResolved
)

// ParkCommitRequest is a transient owner-side handshake token. It binds a
// source TryCommit to the exact logical ticket, physical generation, stable
// source record, and monotonic readiness generation selected by seeded rank.
// The readiness generation prevents an old failed attempt from consuming a
// later publish on the same active physical operation. The request is never
// retained by a producer or allocated per candidate.
type ParkCommitRequest struct {
	ticket      ParkTicket
	id          OperationID
	readyTicket ParkTicket
	record      *OperationRecord
}

func (request ParkCommitRequest) Valid() bool {
	return validParkTicket(request.ticket) && request.id.Valid() && validParkTicket(request.readyTicket) &&
		request.record != nil && request.record.id == request.id
}

func (request ParkCommitRequest) Ticket() (ParkTicket, bool) {
	if !request.Valid() {
		return ParkTicket{}, false
	}
	return request.ticket, true
}

func (request ParkCommitRequest) ID() (OperationID, bool) {
	if !request.Valid() {
		return OperationID{}, false
	}
	return request.id, true
}

type ParkCommitAttemptResult uint8

const (
	ParkCommitAttemptInvalid ParkCommitAttemptResult = iota
	ParkCommitAttemptSucceeded
	ParkCommitAttemptFailed
	// ParkCommitAttemptRetryBudget means the source could not enter its
	// synchronization domain in this reduction. It preserves the exact request,
	// readiness generation, and resolver cursor; it is never a semantic reject.
	ParkCommitAttemptRetryBudget
)

type ParkCommitAttempt struct {
	request ParkCommitRequest
	result  ParkCommitAttemptResult
}

func (request ParkCommitRequest) Failed() ParkCommitAttempt {
	if !currentParkCommitRequest(request) {
		return ParkCommitAttempt{}
	}
	return ParkCommitAttempt{request: request, result: ParkCommitAttemptFailed}
}

func (request ParkCommitRequest) RetryBudget() ParkCommitAttempt {
	if !currentParkCommitRequest(request) {
		return ParkCommitAttempt{}
	}
	return ParkCommitAttempt{request: request, result: ParkCommitAttemptRetryBudget}
}

// BindParkCommitResult is the only successful ReadyThenTryCommit attempt
// constructor. The source gates before its synchronous exact-ID effect, then
// binds the result in the same owner-serialized, non-reentrant handshake. A
// stale or duplicate request cannot manufacture an unowned successful attempt;
// a future reentrant dispatcher must roll back a physical effect if binding
// can fail rather than publishing an unbound success.
func BindParkCommitResult(request ParkCommitRequest) (ParkCommitAttempt, bool) {
	if !currentParkCommitRequest(request) {
		return ParkCommitAttempt{}, false
	}
	request.record.resultState = operationResultOwned
	return ParkCommitAttempt{request: request, result: ParkCommitAttemptSucceeded}, true
}

// parkResolveProgress is private because callers of the compatibility Step API
// still observe only Pending, NeedsCommit, Resolved, or Invalid. The production
// executor persists parkResolutionCursor and charges each Progress transition
// as one reduction, like one Rust-style poll without allocating a Future/Task.
const (
	parkResolveRetryBudget ParkResolveStatus = 254
	parkResolveProgress    ParkResolveStatus = 255
)

type parkResolutionPhase uint8

const (
	parkResolutionIdle parkResolutionPhase = iota
	parkResolutionScan
	parkResolutionDecision
	parkResolutionCommit
	parkResolutionSettle
	parkResolutionFinalize
)

// parkResolutionCursor is owner-only continuation embedded in the executor's
// published-epoch transaction. It contains no interface, function, allocation,
// or producer-visible pointer. tentative winners live here; ParkState's winner
// fields remain reserved for an exact ReadyThen handshake or the terminal
// completed winner.
type parkResolutionCursor struct {
	link            *ParkLink
	winner          *OperationRecord
	forced          *OperationRecord
	request         ParkCommitRequest
	previousSeed    uint32
	phase           parkResolutionPhase
	defaultSelected bool
	_               [2]byte
}

// validParkResolutionHeader accepts only the deliberately transient Parked
// shape owned by a persisted cursor. It is O(1): SealParkSet performed the full
// structural audit, while each reduction validates its exact link and adjacent
// rank/backlinks before mutation.
func validParkResolutionHeader(state *ParkState, ticket ParkTicket) bool {
	return state != nil && state.phase == parkParked && state.resolving && state.ticket == ticket && validParkTicket(ticket) &&
		validTaskCancelState(state.taskCancelKind, state.taskCancelPhase) && state.cancelKind <= ParkCancelShutdown &&
		state.attached == state.expected && state.seed <= state.attached && state.outcome == ParkOutcomePending &&
		(state.hasDefault || state.winnerCase == 0) &&
		(state.attached == 0) == (state.head == nil) && (state.head == nil || state.head.previous == nil)
}

func validParkResolutionLink(state *ParkState, ticket ParkTicket, link *ParkLink) bool {
	if link == nil || link.park != state || link.ticket != ticket || link.operation == nil ||
		&link.operation.link != link || link.operation.link.operation != link.operation ||
		link.operation.phase != operationActive || !link.operation.id.Valid() || !validOperationCandidate(link.operation) {
		return false
	}
	if link.wait != nil && (link.wait.g == nil || &link.wait.g.park != state || link.wait.ticket != ticket ||
		link.wait.state == waitSetRecordUnused) {
		return false
	}
	if link.previous == nil {
		if state.head != link {
			return false
		}
	} else if link.previous.next != link || link.previous.rank >= link.rank {
		return false
	}
	return link.next == nil || link.next.previous == link && link.rank < link.next.rank
}

func validPendingParkResolutionLink(state *ParkState, ticket ParkTicket, link *ParkLink) bool {
	if !validParkResolutionLink(state, ticket, link) {
		return false
	}
	record := link.operation
	return record.disposition == OperationDispositionPending && !record.resolutionApplied &&
		operationCandidatePendingResultStorageValid(record) && operationCandidatePendingForResolution(record)
}

func validChosenParkResultStorage(record *OperationRecord) bool {
	if record == nil || record.resultState != operationResultOwned {
		return false
	}
	if operationCandidateMode(record) == OperationCommitReadyThenTryCommit {
		return validParkTicket(record.resultTicket)
	}
	return record.resultTicket == (ParkTicket{})
}

func validSettlingParkResolutionLink(state *ParkState, ticket ParkTicket, link *ParkLink, winner *OperationRecord) bool {
	if link == nil || link.operation != winner {
		return validPendingParkResolutionLink(state, ticket, link)
	}
	record := link.operation
	return validParkResolutionLink(state, ticket, link) && record.disposition == OperationDispositionPending &&
		!record.resolutionApplied && validChosenParkResultStorage(record) &&
		operationCandidatePendingForResolution(record)
}

func validForcedCanceledParkLink(state *ParkState, ticket ParkTicket, link *ParkLink, forced *OperationRecord) bool {
	return link != nil && link.operation == forced && validParkResolutionLink(state, ticket, link) &&
		forced.disposition == OperationDispositionPending && !forced.resolutionApplied &&
		operationCandidateExternallyCommitted(forced)
}

func validParkCommitRequest(state *ParkState, ticket ParkTicket, candidate *OperationRecord, request ParkCommitRequest) bool {
	return request.Valid() && request.ticket == ticket && request.record == candidate && request.id == candidate.id &&
		request.readyTicket == candidate.resultTicket &&
		state.winnerRecord == candidate && state.winnerID == candidate.id &&
		candidate.phase == operationActive && candidate.disposition == OperationDispositionPending &&
		candidate.link.park == state && candidate.link.ticket == ticket && candidate.link.operation == candidate &&
		operationCandidateMode(candidate) == OperationCommitReadyThenTryCommit &&
		operationCandidateState(candidate) == OperationCommitReady && operationCandidateIsPublished(candidate) &&
		(candidate.resultState == operationResultEmpty || candidate.resultState == operationResultOwned)
}

// currentParkCommitRequest is the source-side pre-effect gate. Structural
// validity alone is insufficient because a cached request can retain a valid
// record generation after another candidate or a task abort has resolved the
// logical park. The static dispatcher calls this before touching source state;
// the compatibility API rechecks it before accepting the synchronous result.
func currentParkCommitRequest(request ParkCommitRequest) bool {
	if !request.Valid() {
		return false
	}
	state := request.record.link.park
	if !validParkResolutionHeader(state, request.ticket) || state.winnerRecord != request.record || state.winnerID != request.id ||
		state.cancelKind == ParkCancelTaskAbort || state.cancelKind == ParkCancelShutdown ||
		request.record.resultState != operationResultEmpty {
		return false
	}
	return validParkCommitRequest(state, request.ticket, request.record, request)
}

func validParkResolutionChoice(state *ParkState, ticket ParkTicket, cursor *parkResolutionCursor) bool {
	if cursor.forced != nil && cursor.winner == nil {
		return (state.cancelKind == ParkCancelTaskAbort || state.cancelKind == ParkCancelShutdown) &&
			state.winnerRecord == nil && state.winnerID == (OperationID{}) &&
			validParkResolutionLink(state, ticket, &cursor.forced.link) &&
			(cursor.forced.disposition == OperationDispositionPending && operationCandidateExternallyCommitted(cursor.forced) ||
				cursor.forced.disposition == OperationDispositionCanceled && cursor.forced.resultTicket == (ParkTicket{}) &&
					operationCandidateSettledForDisposition(cursor.forced, OperationDispositionCanceled))
	}
	if cursor.defaultSelected {
		return cursor.forced == nil && cursor.winner == nil && state.cancelKind == ParkCancelNone && state.hasDefault &&
			state.winnerRecord == nil && state.winnerID == (OperationID{})
	}
	if cursor.winner == nil {
		return cursor.forced == nil && state.cancelKind != ParkCancelNone && state.winnerRecord == nil && state.winnerID == (OperationID{})
	}
	if state.cancelKind == ParkCancelTaskAbort || state.cancelKind == ParkCancelShutdown ||
		state.winnerRecord != nil || state.winnerID != (OperationID{}) ||
		!validParkResolutionLink(state, ticket, &cursor.winner.link) {
		return false
	}
	switch cursor.winner.disposition {
	case OperationDispositionPending:
		return !cursor.winner.resolutionApplied && operationCandidateIsPublished(cursor.winner) &&
			validChosenParkResultStorage(cursor.winner) && operationCandidatePendingForResolution(cursor.winner)
	case OperationDispositionWinner:
		return !cursor.winner.resolutionApplied && cursor.winner.resultTicket == ticket &&
			operationCandidateSettledForDisposition(cursor.winner, OperationDispositionWinner)
	default:
		return false
	}
}

func validParkResolutionCursor(state *ParkState, ticket ParkTicket, cursor *parkResolutionCursor) bool {
	if cursor == nil {
		return false
	}
	if cursor.phase == parkResolutionIdle {
		return *cursor == (parkResolutionCursor{}) && state != nil && !state.resolving
	}
	if cursor.phase < parkResolutionScan || cursor.phase > parkResolutionFinalize ||
		!validParkResolutionHeader(state, ticket) {
		return false
	}
	switch cursor.phase {
	case parkResolutionScan:
		return cursor.link != nil && cursor.winner == nil && cursor.forced == nil && cursor.request == (ParkCommitRequest{}) &&
			!cursor.defaultSelected && state.winnerRecord == nil && state.winnerID == (OperationID{}) &&
			validPendingParkResolutionLink(state, ticket, cursor.link) &&
			(state.seed == 0) == (cursor.link.previous == nil)
	case parkResolutionDecision:
		return cursor.link == nil && cursor.winner == nil && cursor.forced == nil && cursor.request == (ParkCommitRequest{}) &&
			!cursor.defaultSelected && state.winnerRecord == nil && state.winnerID == (OperationID{})
	case parkResolutionCommit:
		return cursor.forced == nil && !cursor.defaultSelected && cursor.winner != nil && cursor.request.Valid() &&
			cursor.link == cursor.winner.link.next && state.seed != 0 &&
			(cursor.link == nil || validPendingParkResolutionLink(state, ticket, cursor.link)) &&
			validParkCommitRequest(state, ticket, cursor.winner, cursor.request)
	case parkResolutionSettle:
		return cursor.request == (ParkCommitRequest{}) && cursor.link != nil &&
			validParkResolutionChoice(state, ticket, cursor) &&
			(cursor.forced != nil && cursor.winner == nil && cursor.link.operation == cursor.forced &&
				validForcedCanceledParkLink(state, ticket, cursor.link, cursor.forced) ||
				validSettlingParkResolutionLink(state, ticket, cursor.link, cursor.winner)) &&
			(cursor.link.previous == nil || cursor.link.previous.operation != nil &&
				cursor.link.previous.operation.disposition != OperationDispositionPending &&
				operationCandidateSettledForDisposition(cursor.link.previous.operation,
					cursor.link.previous.operation.disposition))
	case parkResolutionFinalize:
		return cursor.request == (ParkCommitRequest{}) && cursor.link == nil &&
			validParkResolutionChoice(state, ticket, cursor)
	default:
		return false
	}
}

// beginForcedParkSnapshotResolution starts directly at settlement for the
// exact ReadyThen operation whose peer already committed. Ordinary cancel,
// default, and rank cannot beat it. Strong task stop suppresses continuation
// but retains the physical result for source-side discard during ApplyOne.
func beginForcedParkSnapshotResolution(state *ParkState, ticket ParkTicket, cursor *parkResolutionCursor, forced *OperationRecord) bool {
	if cursor == nil || *cursor != (parkResolutionCursor{}) || state == nil || state.resolving ||
		state.phase != parkParked || state.ticket != ticket || !validParkTicket(ticket) ||
		!validTaskCancelState(state.taskCancelKind, state.taskCancelPhase) || state.cancelKind > ParkCancelShutdown ||
		state.attached != state.expected || state.outcome != ParkOutcomePending ||
		(!state.hasDefault && state.winnerCase != 0) || (state.attached == 0) != (state.head == nil) ||
		state.head == nil || state.head.previous != nil || state.winnerRecord != nil || state.winnerID != (OperationID{}) ||
		forced == nil || !validParkResolutionLink(state, ticket, &forced.link) ||
		!operationCandidateExternallyCommitted(forced) || !validPendingParkResolutionLink(state, ticket, state.head) {
		return false
	}
	cursor.previousSeed = state.seed
	state.seed = 0
	state.resolving = true
	cursor.forced = forced
	if state.cancelKind != ParkCancelTaskAbort && state.cancelKind != ParkCancelShutdown {
		cursor.winner = forced
	}
	cursor.link = state.head
	cursor.phase = parkResolutionSettle
	if validParkResolutionCursor(state, ticket, cursor) {
		return true
	}
	state.seed = cursor.previousSeed
	state.resolving = false
	*cursor = parkResolutionCursor{}
	return false
}

func beginParkSnapshotResolution(state *ParkState, ticket ParkTicket, cursor *parkResolutionCursor, fullAudit bool) bool {
	if cursor == nil || *cursor != (parkResolutionCursor{}) || state == nil || state.resolving ||
		state.phase != parkParked || state.ticket != ticket || !validParkTicket(ticket) ||
		state.winnerRecord != nil || state.winnerID != (OperationID{}) {
		return false
	}
	if fullAudit {
		if !validParkState(state) {
			return false
		}
	} else if !validTaskCancelState(state.taskCancelKind, state.taskCancelPhase) ||
		state.cancelKind > ParkCancelShutdown || state.attached != state.expected ||
		state.outcome != ParkOutcomePending || (!state.hasDefault && state.winnerCase != 0) ||
		(state.attached == 0) != (state.head == nil) || state.head != nil && state.head.previous != nil {
		return false
	}
	if state.head != nil && !validPendingParkResolutionLink(state, ticket, state.head) {
		return false
	}
	previousSeed := state.seed
	cursor.previousSeed = previousSeed
	state.seed = 0
	state.resolving = true
	if state.head == nil || state.cancelKind == ParkCancelTaskAbort || state.cancelKind == ParkCancelShutdown {
		cursor.phase = parkResolutionDecision
	} else {
		cursor.phase = parkResolutionScan
		cursor.link = state.head
	}
	if validParkResolutionCursor(state, ticket, cursor) {
		return true
	}
	state.seed = previousSeed
	state.resolving = false
	*cursor = parkResolutionCursor{}
	return false
}

// abortParkSnapshotCommit restores the byte-visible ParkState overlay when a
// caller has no static dispatcher for an outstanding Ready hint. No candidate
// has been changed before this phase: only seed, resolving, and the exact
// handshake marker require restoration. The affected FIFO owner restores its
// separate record cursor before returning the fail-closed result.
func abortParkSnapshotCommit(state *ParkState, ticket ParkTicket, cursor *parkResolutionCursor) bool {
	if !validParkResolutionCursor(state, ticket, cursor) || cursor.phase != parkResolutionCommit ||
		!currentParkCommitRequest(cursor.request) {
		return false
	}
	state.winnerID = OperationID{}
	state.winnerRecord = nil
	state.seed = cursor.previousSeed
	state.resolving = false
	*cursor = parkResolutionCursor{}
	return state.phase == parkParked && state.ticket == ticket && state.outcome == ParkOutcomePending &&
		state.winnerID == (OperationID{}) && state.winnerRecord == nil
}

func abortParkCommitCompatibility(state *ParkState, ticket ParkTicket, request ParkCommitRequest, previousSeed uint32) bool {
	if state == nil || state.ticket != ticket || state.winnerRecord != request.record || state.winnerID != request.id ||
		!currentParkCommitRequest(request) {
		return false
	}
	state.winnerID = OperationID{}
	state.winnerRecord = nil
	state.seed = previousSeed
	state.resolving = false
	return validParkState(state)
}

func parkResolutionCommitRequest(state *ParkState, ticket ParkTicket, cursor *parkResolutionCursor) (ParkCommitRequest, bool) {
	if !validParkResolutionCursor(state, ticket, cursor) || cursor.phase != parkResolutionCommit ||
		!currentParkCommitRequest(cursor.request) {
		return ParkCommitRequest{}, false
	}
	return cursor.request, true
}

// resolveParkSnapshotBoundedStep performs exactly one candidate scan, one
// TryCommit result consumption, one terminal decision, one candidate settle,
// or one scalar finalize. Administrative cursor changes are folded into that
// action. The caller owns source dispatch and snapshot serialization.
func resolveParkSnapshotBoundedStep(
	state *ParkState,
	ticket ParkTicket,
	cursor *parkResolutionCursor,
	attempt ParkCommitAttempt,
) (resolution CompletionResolution, request ParkCommitRequest, status ParkResolveStatus) {
	if !validParkResolutionCursor(state, ticket, cursor) {
		return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
	}
	switch cursor.phase {
	case parkResolutionScan:
		if attempt != (ParkCommitAttempt{}) || state.seed == ^uint32(0) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		link := cursor.link
		record, next := link.operation, link.next
		state.seed++
		cursor.link = next
		if !operationCandidateIsPublished(record) {
			if next == nil {
				cursor.phase = parkResolutionDecision
			}
			return CompletionResolution{}, ParkCommitRequest{}, parkResolveProgress
		}
		switch operationCandidateMode(record) {
		case OperationCommitIrreversibleCompletion, OperationCommitReservable:
			cursor.winner = record
			cursor.link = state.head
			cursor.phase = parkResolutionSettle
			return CompletionResolution{}, ParkCommitRequest{}, parkResolveProgress
		case OperationCommitReadyThenTryCommit:
			state.winnerID = record.id
			state.winnerRecord = record
			cursor.winner = record
			cursor.request = ParkCommitRequest{ticket: ticket, id: record.id, readyTicket: record.resultTicket, record: record}
			cursor.phase = parkResolutionCommit
			if !validParkResolutionCursor(state, ticket, cursor) || !currentParkCommitRequest(cursor.request) {
				return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
			}
			return CompletionResolution{}, cursor.request, ParkResolveNeedsCommit
		default:
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
	case parkResolutionDecision:
		if attempt != (ParkCommitAttempt{}) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		if state.cancelKind != ParkCancelNone {
			cursor.link = state.head
			if cursor.link == nil {
				cursor.phase = parkResolutionFinalize
			} else {
				cursor.phase = parkResolutionSettle
			}
			return CompletionResolution{}, ParkCommitRequest{}, parkResolveProgress
		}
		if state.hasDefault {
			cursor.defaultSelected = true
			cursor.link = state.head
			if cursor.link == nil {
				cursor.phase = parkResolutionFinalize
			} else {
				cursor.phase = parkResolutionSettle
			}
			return CompletionResolution{}, ParkCommitRequest{}, parkResolveProgress
		}
		state.resolving = false
		*cursor = parkResolutionCursor{}
		return CompletionResolution{WaitSets: 1}, ParkCommitRequest{}, ParkResolvePending
	case parkResolutionCommit:
		if (attempt.result != ParkCommitAttemptSucceeded && attempt.result != ParkCommitAttemptFailed &&
			attempt.result != ParkCommitAttemptRetryBudget) ||
			attempt.request != cursor.request ||
			!validParkCommitRequest(state, ticket, cursor.winner, attempt.request) ||
			(attempt.result == ParkCommitAttemptSucceeded && cursor.winner.resultState != operationResultOwned) ||
			(attempt.result != ParkCommitAttemptSucceeded && !currentParkCommitRequest(attempt.request)) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		if attempt.result == ParkCommitAttemptRetryBudget {
			return CompletionResolution{}, cursor.request, parkResolveRetryBudget
		}
		candidate := cursor.winner
		state.winnerID = OperationID{}
		state.winnerRecord = nil
		cursor.request = ParkCommitRequest{}
		if attempt.result == ParkCommitAttemptFailed {
			if !rejectReadyThenTryCommitCandidate(candidate) {
				return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
			}
			cursor.winner = nil
			if cursor.link == nil {
				cursor.phase = parkResolutionDecision
			} else {
				cursor.phase = parkResolutionScan
			}
			return CompletionResolution{}, ParkCommitRequest{}, parkResolveProgress
		}
		cursor.link = state.head
		cursor.phase = parkResolutionSettle
		return CompletionResolution{}, ParkCommitRequest{}, parkResolveProgress
	case parkResolutionSettle:
		if attempt != (ParkCommitAttempt{}) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		link := cursor.link
		record, next := link.operation, link.next
		if record == cursor.forced && cursor.winner == nil {
			if state.cancelKind != ParkCancelTaskAbort && state.cancelKind != ParkCancelShutdown ||
				!operationCandidateExternallyCommitted(record) {
				return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
			}
			record.resultTicket = ParkTicket{}
			record.cancelRequested = true
			record.disposition = OperationDispositionCanceled
		} else if record == cursor.winner {
			if record.resultState != operationResultOwned || !commitOperationCandidate(record) {
				return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
			}
			record.resultTicket = ticket
			record.disposition = OperationDispositionWinner
		} else {
			if !rollBackOperationCandidate(record) {
				return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
			}
			record.cancelRequested = true
			if cursor.winner == nil && !cursor.defaultSelected {
				record.disposition = OperationDispositionCanceled
			} else {
				record.disposition = OperationDispositionLost
			}
		}
		cursor.link = next
		if next == nil {
			cursor.phase = parkResolutionFinalize
		}
		return CompletionResolution{}, ParkCommitRequest{}, parkResolveProgress
	case parkResolutionFinalize:
		if attempt != (ParkCommitAttempt{}) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		state.phase = parkDetaching
		switch {
		case cursor.defaultSelected:
			state.outcome = ParkOutcomeDefault
			state.winnerID = OperationID{}
			state.winnerRecord = nil
			resolution = CompletionResolution{WaitSets: 1, Defaulted: 1, Losers: state.attached}
		case cursor.winner == nil:
			state.outcome = ParkOutcomeCanceled
			state.hasDefault = false
			state.winnerCase = 0
			state.winnerID = OperationID{}
			state.winnerRecord = nil
			resolution = CompletionResolution{WaitSets: 1, Canceled: 1, Losers: state.attached}
		default:
			state.outcome = ParkOutcomeCompleted
			state.hasDefault = false
			state.winnerCase = cursor.winner.link.caseID
			state.winnerID = cursor.winner.id
			state.winnerRecord = cursor.winner
			resolution = CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1, Losers: state.attached - 1}
		}
		if state.attached == 0 {
			state.phase = parkReady
		}
		state.resolving = false
		*cursor = parkResolutionCursor{}
		if !validActiveParkStateHeader(state, ticket) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		return resolution, ParkCommitRequest{}, ParkResolveResolved
	default:
		return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
	}
}

// ResolveParkSnapshotStep is the allocation-free commit-capable resolver.
// A zero attempt starts or continues resolution. ReadyThenTryCommit returns an
// exact request and freezes the transient ParkState cursor; the static source
// dispatcher performs its non-reentrant synchronous TryCommit and calls this
// function again with BindParkCommitResult(request), request.Failed, or
// request.RetryBudget before any other owner publication/cancellation. A
// failure consumes that one ready hint and immediately continues from the next
// seeded-rank link without a rescan; RetryBudget preserves the exact request.
func ResolveParkSnapshotStep(
	state *ParkState,
	ticket ParkTicket,
	attempt ParkCommitAttempt,
) (resolution CompletionResolution, request ParkCommitRequest, status ParkResolveStatus) {
	var cursor parkResolutionCursor
	if attempt == (ParkCommitAttempt{}) {
		// Compatibility begins with the retained full diagnostic audit, then loop-
		// drives the same bounded primitive used by the production executor.
		if !beginParkSnapshotResolution(state, ticket, &cursor, true) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
	} else {
		if !validParkResolutionHeader(state, ticket) || state.winnerRecord == nil ||
			(attempt.result != ParkCommitAttemptSucceeded && attempt.result != ParkCommitAttemptFailed &&
				attempt.result != ParkCommitAttemptRetryBudget) ||
			!validParkCommitRequest(state, ticket, state.winnerRecord, attempt.request) ||
			(attempt.result == ParkCommitAttemptSucceeded && state.winnerRecord.resultState != operationResultOwned) ||
			(attempt.result == ParkCommitAttemptFailed && !currentParkCommitRequest(attempt.request)) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		// A strong cancellation cannot interleave the owner-serialized source
		// call. Observing one with an outstanding result is corruption and must
		// not reinterpret an already attempted physical commit.
		if state.cancelKind == ParkCancelTaskAbort || state.cancelKind == ParkCancelShutdown {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		candidate := state.winnerRecord
		cursor = parkResolutionCursor{
			link:    candidate.link.next,
			winner:  candidate,
			request: attempt.request,
			phase:   parkResolutionCommit,
		}
		if !validParkResolutionCursor(state, ticket, &cursor) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
	}
	for {
		resolution, request, status = resolveParkSnapshotBoundedStep(state, ticket, &cursor, attempt)
		attempt = ParkCommitAttempt{}
		switch status {
		case parkResolveProgress:
			continue
		case parkResolveRetryBudget:
			resolution.WaitSets = 1
			return resolution, request, ParkResolveNeedsCommit
		case ParkResolveNeedsCommit:
			resolution.WaitSets = 1
			return resolution, request, status
		case ParkResolvePending, ParkResolveResolved:
			if !validParkState(state) {
				return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
			}
			return resolution, request, status
		default:
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
	}
}

// ResolveParkSnapshot resolves one logical wait-set after the executor has
// completely drained every source in its SourceSet. No per-P fact array is
// needed: the compact published candidate bit and cancelKind are the durable
// snapshot.
//
// A valid snapshot without a completion or cancellation returns
// {WaitSets: 1}, true and leaves the park untouched. Ordinary operation
// cancellation still loses to a completion published in the same complete
// source snapshot. Task abort and shutdown suppress every completion.
//
// Calling this function before a complete SourceSet drain is a caller error:
// the resolver deliberately has no second source-specific bookkeeping layer
// with which to detect a partial drain. This compatibility entry has no source
// dispatcher and therefore fails closed when a ReadyThenTryCommit candidate
// needs a handshake; production SourceSet resolution uses the step API above.
func ResolveParkSnapshot(state *ParkState, ticket ParkTicket) (resolution CompletionResolution, ok bool) {
	previousVisits := uint32(0)
	if state != nil {
		previousVisits = state.seed
	}
	resolution, request, status := ResolveParkSnapshotStep(state, ticket, ParkCommitAttempt{})
	if status == ParkResolveNeedsCommit {
		// The compatibility caller has no source dispatcher. Undo only the
		// transient owner cursor/visit overlay; the ready hint and its monotonic
		// generation remain untouched for a later production Step handshake.
		if !abortParkCommitCompatibility(state, ticket, request, previousVisits) {
			return CompletionResolution{}, false
		}
		return CompletionResolution{}, false
	}
	return resolution, status == ParkResolvePending || status == ParkResolveResolved
}
