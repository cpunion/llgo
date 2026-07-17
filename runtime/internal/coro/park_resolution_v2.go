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
)

type ParkCommitAttempt struct {
	request ParkCommitRequest
	result  ParkCommitAttemptResult
}

func (request ParkCommitRequest) Succeeded() ParkCommitAttempt {
	if !request.Valid() {
		return ParkCommitAttempt{}
	}
	return ParkCommitAttempt{request: request, result: ParkCommitAttemptSucceeded}
}

func (request ParkCommitRequest) Failed() ParkCommitAttempt {
	if !request.Valid() {
		return ParkCommitAttempt{}
	}
	return ParkCommitAttempt{request: request, result: ParkCommitAttemptFailed}
}

func validParkResolutionHeader(state *ParkState, ticket ParkTicket) bool {
	return state != nil && state.phase == parkParked && state.ticket == ticket && validParkTicket(ticket) &&
		validTaskCancelState(state.taskCancelKind, state.taskCancelPhase) && state.cancelKind <= ParkCancelShutdown &&
		state.attached == state.expected && state.outcome == ParkOutcomePending &&
		(state.hasDefault || state.winnerCase == 0) && validPendingParkCommitCursor(state) &&
		(state.attached == 0) == (state.head == nil) && (state.head == nil || state.head.previous == nil)
}

// nextPublishedParkCandidateFrom walks the rank-sorted intrusive list exactly
// once from cursor. visits is persisted in ParkState.seed for white-box work
// accounting and for the large-N regression which locks one snapshot to O(N).
func nextPublishedParkCandidateFrom(cursor *ParkLink) (candidate *OperationRecord, visits uint32, ok bool) {
	for link := cursor; link != nil; link = link.next {
		visits++
		if link.operation == nil || &link.operation.link != link || link.operation.link.operation != link.operation {
			return nil, visits, false
		}
		if operationCandidateIsPublished(link.operation) {
			return link.operation, visits, true
		}
	}
	return nil, visits, true
}

func addParkResolutionVisits(state *ParkState, visits uint32) bool {
	if state == nil || visits > ^uint32(0)-state.seed {
		return false
	}
	state.seed += visits
	return true
}

func validParkCommitRequest(state *ParkState, ticket ParkTicket, candidate *OperationRecord, request ParkCommitRequest) bool {
	return request.Valid() && request.ticket == ticket && request.record == candidate && request.id == candidate.id &&
		request.readyTicket == candidate.resultTicket &&
		state.winnerRecord == candidate && state.winnerID == candidate.id &&
		candidate.phase == operationActive && candidate.disposition == OperationDispositionPending &&
		candidate.link.park == state && candidate.link.ticket == ticket && candidate.link.operation == candidate &&
		operationCandidateMode(candidate) == OperationCommitReadyThenTryCommit &&
		operationCandidateState(candidate) == OperationCommitReady && operationCandidateIsPublished(candidate)
}

// currentParkCommitRequest is the source-side pre-effect gate. Structural
// validity alone is insufficient because a cached request can retain a valid
// record generation after another candidate or a task abort has resolved the
// logical park. The static dispatcher calls this immediately before touching
// source state; ResolveParkSnapshotStep repeats the exact check when accepting
// the synchronous result.
func currentParkCommitRequest(request ParkCommitRequest) bool {
	if !request.Valid() {
		return false
	}
	state := request.record.link.park
	if !validParkResolutionHeader(state, request.ticket) || state.winnerRecord != request.record || state.winnerID != request.id ||
		state.cancelKind == ParkCancelTaskAbort || state.cancelKind == ParkCancelShutdown {
		return false
	}
	return validParkCommitRequest(state, request.ticket, request.record, request)
}

func settleParkCandidates(state *ParkState, winner *OperationRecord) bool {
	for link := state.head; link != nil; link = link.next {
		if link.operation == winner {
			if !commitOperationCandidate(link.operation) {
				return false
			}
			continue
		}
		if !rollBackOperationCandidate(link.operation) {
			return false
		}
	}
	return true
}

// ResolveParkSnapshotStep is the allocation-free commit-capable resolver.
// A zero attempt starts or continues resolution. ReadyThenTryCommit returns an
// exact request and freezes the transient ParkState cursor; the static source
// dispatcher performs its non-reentrant synchronous TryCommit and calls this
// function again with request.Succeeded or request.Failed before any other
// owner publication/cancellation. A failure consumes that one ready hint and
// immediately continues from the next seeded-rank link without a rescan.
func ResolveParkSnapshotStep(
	state *ParkState,
	ticket ParkTicket,
	attempt ParkCommitAttempt,
) (resolution CompletionResolution, request ParkCommitRequest, status ParkResolveStatus) {
	var cursor *ParkLink
	if attempt == (ParkCommitAttempt{}) {
		// A zero step begins one complete source snapshot. Full structural audit
		// happens once here; every synchronous attempt continuation below uses
		// only the O(1) exact cursor/header gate.
		if !validParkState(state) || state.phase != parkParked || ticket != state.ticket || state.winnerRecord != nil {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		state.seed = 0
		cursor = state.head
	} else {
		if !validParkResolutionHeader(state, ticket) || state.winnerRecord == nil ||
			(attempt.result != ParkCommitAttemptSucceeded && attempt.result != ParkCommitAttemptFailed) ||
			!validParkCommitRequest(state, ticket, state.winnerRecord, attempt.request) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		// A strong cancellation cannot interleave the owner-serialized source
		// call. Observing one with an outstanding result is corruption and must
		// not reinterpret an already attempted physical commit.
		if state.cancelKind == ParkCancelTaskAbort || state.cancelKind == ParkCancelShutdown {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		candidate := state.winnerRecord
		if attempt.result == ParkCommitAttemptSucceeded {
			if !resolveParkSet(state, ticket, candidate, false) {
				return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
			}
			resolution = CompletionResolution{
				WaitSets:  1,
				Completed: 1,
				Winners:   1,
				Losers:    state.attached - 1,
			}
			return resolution, ParkCommitRequest{}, ParkResolveResolved
		}
		cursor = candidate.link.next
		if !rejectReadyThenTryCommitCandidate(candidate) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		state.winnerID = OperationID{}
		state.winnerRecord = nil
	}
	resolution.WaitSets = 1

	strongCancel := state.cancelKind == ParkCancelTaskAbort || state.cancelKind == ParkCancelShutdown
	for !strongCancel {
		candidate, visits, ok := nextPublishedParkCandidateFrom(cursor)
		if !ok || !addParkResolutionVisits(state, visits) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		if candidate == nil {
			break
		}
		switch operationCandidateMode(candidate) {
		case OperationCommitIrreversibleCompletion, OperationCommitReservable:
			if !resolveParkSet(state, ticket, candidate, false) {
				return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
			}
			resolution.Completed = 1
			resolution.Winners = 1
			resolution.Losers = state.attached - 1
			return resolution, ParkCommitRequest{}, ParkResolveResolved
		case OperationCommitReadyThenTryCommit:
			state.winnerID = candidate.id
			state.winnerRecord = candidate
			request = ParkCommitRequest{ticket: ticket, id: candidate.id, readyTicket: candidate.resultTicket, record: candidate}
			if !request.Valid() || !validParkCommitRequest(state, ticket, candidate, request) {
				state.winnerID = OperationID{}
				state.winnerRecord = nil
				return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
			}
			return resolution, request, ParkResolveNeedsCommit
		default:
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
	}

	if state.cancelKind != ParkCancelNone {
		if !resolveParkSet(state, ticket, nil, false) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		resolution.Canceled = 1
		resolution.Losers = state.attached
		return resolution, ParkCommitRequest{}, ParkResolveResolved
	}
	if state.hasDefault {
		if !resolveParkSet(state, ticket, nil, true) {
			return CompletionResolution{}, ParkCommitRequest{}, ParkResolveInvalid
		}
		resolution.Defaulted = 1
		resolution.Losers = state.attached
		return resolution, ParkCommitRequest{}, ParkResolveResolved
	}
	return resolution, ParkCommitRequest{}, ParkResolvePending
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
		// transient atomic cursor/visit overlay; the ready hint and its monotonic
		// generation remain untouched for a later production Step handshake.
		if state == nil || state.phase != parkParked || state.ticket != ticket ||
			state.winnerRecord != request.record || state.winnerID != request.id {
			return CompletionResolution{}, false
		}
		state.winnerID = OperationID{}
		state.winnerRecord = nil
		state.seed = previousVisits
		if !validParkState(state) {
			return CompletionResolution{}, false
		}
		return CompletionResolution{}, false
	}
	return resolution, status == ParkResolvePending || status == ParkResolveResolved
}
