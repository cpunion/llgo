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

// TaskCancelKind is cooperative execution cancellation. It follows the same
// lightweight shape as a stop token: one sticky owner-P field on G, observed
// at compiler safepoints and suspension boundaries. It is not operation or
// context cancellation, and it never authorizes arbitrary coroutine destroy.
type TaskCancelKind uint8

const (
	TaskCancelNone TaskCancelKind = iota
	// TaskCancelAbort is structured runtime/parent cancellation. It enters the
	// same cleanup lowering but retains a distinct terminal reason.
	TaskCancelAbort
	// TaskCancelShutdown is the strongest request and wins over a simultaneous
	// operation completion at the current park boundary.
	TaskCancelShutdown
)

func validTaskCancelKind(kind TaskCancelKind) bool {
	return kind >= TaskCancelAbort && kind <= TaskCancelShutdown
}

type taskCancelPhase uint8

const (
	taskCancelIdle taskCancelPhase = iota
	taskCancelRequested
	taskCancelCleanup
)

func validTaskCancelState(kind TaskCancelKind, phase taskCancelPhase) bool {
	return kind == TaskCancelNone && phase == taskCancelIdle ||
		validTaskCancelKind(kind) && (phase == taskCancelRequested || phase == taskCancelCleanup)
}

func taskCancelParkKind(kind TaskCancelKind) ParkCancelKind {
	switch kind {
	case TaskCancelAbort:
		return ParkCancelTaskAbort
	case TaskCancelShutdown:
		return ParkCancelShutdown
	default:
		return ParkCancelNone
	}
}

func pQueueContainsReady(p *P, target *G) bool {
	if p == nil || target == nil {
		return false
	}
	for slow, fast := p.readyHead, p.readyHead; fast != nil && fast.nextReady != nil; {
		slow = slow.nextReady
		fast = fast.nextReady.nextReady
		if slow == fast {
			return false
		}
	}
	for g := p.readyHead; g != nil; g = g.nextReady {
		if g == target {
			return true
		}
	}
	return false
}

// pOwnsTaskCancellation proves scheduler ownership for the public arbitrary-G
// APIs without adding a permanent P pointer or external handle to every G.
// Runnable and parked tasks retain a full queue audit: cancellation is rare,
// so a scan is preferable to inflating the hot G/P representation. An active
// locked foreign wait instead has no queue membership; its explicit
// GForeignWaiting state and active-frame/runP invariants prove the retained
// M-local handoff root.
func pOwnsTaskCancellation(p *P, g *G) bool {
	if p == nil || !ValidG(g) {
		return false
	}
	switch g.state {
	case GRunnable:
		return g.queued && pQueueContainsReady(p, g)
	case GRunning, GDispatching:
		return p.current == g && g.runP == p
	case GForeignWaiting:
		return validForeignWaitingExecutorTask(p, g)
	case GWaiting:
		return g.waiting && g.active != nil && g.active.parkWait != nil &&
			validActiveWaitSetRecordFast(p, g.active.parkWait)
	default:
		return false
	}
}

type taskCancellationOwnerProof uint8

const (
	taskCancellationProofFull taskCancellationOwnerProof = iota
	taskCancellationProofRegistered
)

func validRegisteredParkHead(state *ParkState) bool {
	if state == nil || (state.attached == 0) != (state.head == nil) {
		return false
	}
	if state.head == nil {
		return true
	}
	link := state.head
	record := link.operation
	if link.previous != nil || link.park != state || link.ticket != state.ticket || record == nil ||
		&record.link != link || record.link.operation != record || record.phase != operationActive ||
		!record.id.Valid() || !validOperationCandidate(record) {
		return false
	}
	if link.wait != nil && (link.wait.g == nil || &link.wait.g.park != state ||
		link.wait.ticket != state.ticket || link.wait.state == waitSetRecordUnused) {
		return false
	}
	switch state.phase {
	case parkPreparing, parkSealed, parkParked:
		return record.disposition == OperationDispositionPending && !record.resolutionApplied &&
			operationCandidatePendingResultStorageValid(record) && operationCandidatePendingForResolution(record)
	case parkDetaching:
		switch state.outcome {
		case ParkOutcomeCompleted:
			if record.id == state.winnerID {
				if link.caseID != state.winnerCase || record.disposition != OperationDispositionWinner ||
					record.resultTicket != state.ticket || record.resultState != operationResultOwned {
					return false
				}
			} else if record.disposition != OperationDispositionLost || !record.cancelRequested ||
				record.resultTicket != (ParkTicket{}) || !operationUnselectedResultStateValid(record) {
				return false
			}
		case ParkOutcomeDefault:
			if record.disposition != OperationDispositionLost || !record.cancelRequested ||
				record.resultTicket != (ParkTicket{}) || !operationUnselectedResultStateValid(record) {
				return false
			}
		case ParkOutcomeCanceled:
			if record.disposition != OperationDispositionCanceled || !record.cancelRequested ||
				record.resultTicket != (ParkTicket{}) || !operationUnselectedResultStateValid(record) {
				return false
			}
		default:
			return false
		}
		return operationCandidateSettledForDisposition(record, record.disposition)
	default:
		return false
	}
}

func validRegisteredActiveParkHeader(state *ParkState) bool {
	if state == nil || !validActiveParkStateHeader(state, state.ticket) || !validRegisteredParkHead(state) {
		return false
	}
	if state.phase != parkReady || state.outcome != ParkOutcomeCompleted {
		return true
	}
	record := state.winnerRecord
	return record != nil && record.phase == operationDetached && record.resultTicket == state.ticket &&
		record.resultState == operationResultOwned &&
		operationCandidateSettledForDisposition(record, OperationDispositionWinner)
}

func validRegisteredReleasableParkHeader(state *ParkState) bool {
	if state == nil || state.resolving || !validTaskCancelState(state.taskCancelKind, state.taskCancelPhase) ||
		state.cancelKind > ParkCancelShutdown || state.attached > state.expected ||
		state.directChannel && state.phase != parkMaterialized {
		return false
	}
	switch state.phase {
	case parkIdle:
		return state.ticket == (ParkTicket{}) && state.expected == 0 && state.attached == 0 &&
			state.seed == 0 && !state.hasDefault && state.cancelKind == ParkCancelNone &&
			state.outcome == ParkOutcomePending && state.winnerCase == 0 &&
			state.winnerID == (OperationID{}) && state.winnerRecord == nil && state.head == nil
	case parkConsumed:
		if !validParkTicket(state.ticket) || state.attached != 0 || state.head != nil {
			return false
		}
		switch state.outcome {
		case ParkOutcomeCompleted:
			return !state.hasDefault && state.cancelKind < ParkCancelTaskAbort &&
				state.winnerID.Valid() && state.winnerRecord == nil
		case ParkOutcomeCanceled:
			return !state.hasDefault && state.cancelKind != ParkCancelNone && state.winnerCase == 0 &&
				state.winnerID == (OperationID{}) && state.winnerRecord == nil
		case ParkOutcomeDefault:
			return state.hasDefault && state.cancelKind == ParkCancelNone &&
				state.winnerID == (OperationID{}) && state.winnerRecord == nil
		default:
			return false
		}
	case parkMaterialized:
		return validMaterializedParkHeader(state)
	case parkDelivered:
		return validParkTicket(state.ticket) && state.expected == 0 && state.attached == 0 &&
			state.seed == 0 && !state.hasDefault && state.cancelKind == ParkCancelNone &&
			state.outcome == ParkOutcomePending && state.winnerCase == 0 &&
			state.winnerID == (OperationID{}) && state.winnerRecord == nil && state.head == nil
	default:
		return false
	}
}

// validRegisteredRunningParkHeader is deliberately scalar/adjacent-only. A
// preparing or sealed candidate chain was structurally audited by its owner
// transition; registered delivery must not hide another candidate traversal
// in what is only an ownership proof.
func validRegisteredRunningParkHeader(state *ParkState) bool {
	if validRegisteredReleasableParkHeader(state) {
		return true
	}
	if state == nil || state.resolving {
		return false
	}
	switch state.phase {
	case parkPreparing:
		return validPreparingParkStateHeader(state, state.ticket) && validRegisteredParkHead(state)
	case parkSealed:
		return validParkTicket(state.ticket) &&
			validTaskCancelState(state.taskCancelKind, state.taskCancelPhase) &&
			state.cancelKind <= ParkCancelShutdown && state.attached == state.expected &&
			state.seed == 0 && state.outcome == ParkOutcomePending &&
			(state.hasDefault || state.winnerCase == 0) && state.winnerID == (OperationID{}) &&
			state.winnerRecord == nil && validRegisteredParkHead(state)
	case parkParked, parkDetaching, parkReady:
		return validRegisteredActiveParkHeader(state)
	default:
		return false
	}
}

func validRegisteredRunnableParkHeader(state *ParkState) bool {
	return validRegisteredReleasableParkHeader(state) ||
		state != nil && state.phase == parkReady && validRegisteredActiveParkHeader(state)
}

// pOwnsRegisteredTaskCancellation is the O(1) local ownership predicate used
// only after a TaskControlSource has proved an exact registered endpoint. The
// endpoint's owner and taskControlLeases pin the task to this P. Waiting has an
// exact frame-local record and therefore keeps constant-time neighbour/link
// validation.
//
// This predicate is not a public arbitrary-G ownership query. Multi-P task
// migration must transfer or forward the registered control lease locator
// before changing ownership; merely reusing this single-P proof would be a
// use-after-migration bug.
func pOwnsRegisteredTaskCancellation(p *P, g *G) bool {
	if p == nil || !ValidG(g) || g.taskControlLeases == 0 {
		return false
	}
	if !gPreemptEnabled(g) {
		return false
	}
	switch g.state {
	case GRunnable:
		return g.queued && !g.waiting && g.runP == nil &&
			validRegisteredRunnableParkHeader(&g.park) &&
			g.spawnChild == nil && g.spawnParent == nil && g.spawnP == nil &&
			(g.active == nil || g.active.parkWait == nil) &&
			p.readyHead != nil && p.readyTail != nil
	case GRunning, GDispatching:
		return p.current == g && g.runP == p && !g.queued && g.nextReady == nil &&
			!g.waiting &&
			validRegisteredRunningParkHeader(&g.park)
	case GForeignWaiting:
		return validForeignWaitingExecutorTask(p, g) &&
			validRegisteredRunningParkHeader(&g.park)
	case GWaiting:
		if !g.waiting || g.queued || g.nextReady != nil || g.runP != nil ||
			g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil {
			return false
		}
		return g.active != nil && g.active.parkWait != nil &&
			validActiveWaitSetRecordFast(p, g.active.parkWait) && validRegisteredActiveParkHeader(&g.park)
	default:
		return false
	}
}

// applyTaskCancellationToPark maps the strongest task request into the current
// logical wait. Preparing/sealed/parked waits receive a sticky logical cancel;
// detaching/ready already have a terminal outcome, so the task token is simply
// observed before the selected continuation executes.
func applyTaskCancellationToParkOwned(g *G, kind TaskCancelKind, proof taskCancellationOwnerProof) bool {
	if !ValidG(g) || !validTaskCancelKind(kind) || g.park.resolving {
		return false
	}
	if proof == taskCancellationProofRegistered {
		if !validRegisteredRunningParkHeader(&g.park) {
			return false
		}
	} else if proof != taskCancellationProofFull || !validParkState(&g.park) {
		return false
	}
	switch g.park.phase {
	case parkIdle, parkConsumed, parkDelivered:
		return g.state != GWaiting
	case parkPreparing, parkSealed, parkParked:
		parkKind := taskCancelParkKind(kind)
		if proof == taskCancellationProofFull {
			return RequestParkCancel(&g.park, g.park.ticket, parkKind)
		}
		if parkKind == ParkCancelNone || g.park.phase == parkParked && g.park.winnerRecord != nil {
			return false
		}
		if parkKind > g.park.cancelKind {
			g.park.cancelKind = parkKind
		}
		return true
	case parkDetaching, parkReady, parkMaterialized:
		return true
	default:
		return false
	}
}

func applyTaskCancellationToPark(g *G, kind TaskCancelKind) bool {
	return applyTaskCancellationToParkOwned(g, kind, taskCancellationProofFull)
}

// requestTaskCancellationOwned is the single cancellation mutation core. Its
// caller must first prove owner-P authority either through the public full
// queue audit or through one exact registered TaskControl endpoint.
func requestTaskCancellationOwned(p *P, g *G, kind TaskCancelKind, proof taskCancellationOwnerProof) bool {
	if p == nil || !ValidG(g) || !validTaskCancelKind(kind) ||
		(proof != taskCancellationProofFull && proof != taskCancellationProofRegistered) {
		return false
	}
	// ActionDestroy is already the indivisible physical half of a checked
	// reduction. No owner callback may publish a new stop after that point and
	// then let the selected handle be destroyed. A request admitted while the
	// preceding CheckDestroy is still selected is instead caught by Checked.
	if p.current == g && g.runP == p && p.action.Kind == ActionDestroy {
		return false
	}
	var wait *WaitSetRecord
	var direct *DirectChannelCompletion
	if g.state == GWaiting && g.active != nil && g.active.parkWait != nil {
		wait = g.active.parkWait
		if g.park.resolving || g.park.winnerRecord != nil ||
			proof == taskCancellationProofRegistered && !validRegisteredActiveParkHeader(&g.park) {
			return false
		}
		if completion, compact := directChannelCompletionForWait(wait); compact {
			direct = completion
		} else if !canAppendAffectedWaitSet(p, wait) {
			return false
		}
	} else {
		if proof == taskCancellationProofRegistered {
			if !validRegisteredRunningParkHeader(&g.park) {
				return false
			}
		} else if !validParkState(&g.park) {
			return false
		}
	}
	if g.park.taskCancelPhase == taskCancelCleanup {
		// The first cleanup claim freezes the terminal cause. An old stop token
		// must not re-enter or upgrade while a defer itself calls or parks.
		return true
	}
	strongest := kind
	if g.park.taskCancelKind > strongest {
		strongest = g.park.taskCancelKind
	}
	if wait == nil {
		if !applyTaskCancellationToParkOwned(g, strongest, proof) {
			return false
		}
	} else {
		switch g.park.phase {
		case parkParked:
			parkKind := taskCancelParkKind(strongest)
			if parkKind == ParkCancelNone {
				return false
			}
			if parkKind > g.park.cancelKind {
				g.park.cancelKind = parkKind
			}
		case parkDetaching, parkReady:
		default:
			return false
		}
	}
	g.park.taskCancelKind = strongest
	g.park.taskCancelPhase = taskCancelRequested
	if direct != nil {
		return requestDirectChannelCancellation(direct)
	}
	if wait != nil {
		appendAffectedWaitSetUnchecked(p, wait)
	}
	return true
}

// RequestTaskCancellation is owner-P-only. Cross-thread context/I/O/host
// cancellation first publishes a normal OperationID fact and requests the
// executor; the owner P then calls this function if that fact represents task
// termination. The public arbitrary-G API deliberately retains its complete
// ready/wait queue ownership audit. Go does not expose an arbitrary
// goroutine-kill handle, so the base G representation needs no per-task
// external registry.
func RequestTaskCancellation(p *P, g *G, kind TaskCancelKind) bool {
	return pOwnsTaskCancellation(p, g) &&
		requestTaskCancellationOwned(p, g, kind, taskCancellationProofFull)
}

// TaskCancellationOf is a non-consuming owner/current-G observation.
func TaskCancellationOf(p *P, g *G) (TaskCancelKind, bool) {
	if !pOwnsTaskCancellation(p, g) || !validTaskCancelState(g.park.taskCancelKind, g.park.taskCancelPhase) ||
		g.park.taskCancelKind == TaskCancelNone {
		return TaskCancelNone, false
	}
	return g.park.taskCancelKind, true
}

// ClaimTaskCancellation is the one transition from a stop request into
// cleanup. Later safepoints inside defer cleanup see Cleanup and cannot
// re-enter. Goexit is a separate synchronous current-G cleanup entry, not an
// injectable stop kind.
func ClaimTaskCancellation(p *P, g *G) (TaskCancelKind, bool) {
	if !pOwnsTaskCancellation(p, g) ||
		(g.state != GRunnable && g.state != GRunning && g.state != GDispatching) ||
		g.runAction != ActionInvalid || !gPreemptEnabledAtDepthZero(g) ||
		g.park.taskCancelPhase != taskCancelRequested || !validTaskCancelKind(g.park.taskCancelKind) {
		return TaskCancelNone, false
	}
	g.park.taskCancelPhase = taskCancelCleanup
	return g.park.taskCancelKind, true
}

// ConsumeTaskParkSet is the G resume gate. It consumes the park before
// claiming task cleanup, so a late stop suppresses a selected continuation but
// still returns the winner result lease for source-specific discard/recycle.
func ConsumeTaskParkSet(
	p *P,
	g *G,
	ticket ParkTicket,
) (outcome ParkOutcome, caseID uint32, lease OperationResultLease, task TaskCancelKind, ok bool) {
	if !pOwnsTaskCancellation(p, g) {
		return ParkOutcomePending, 0, OperationResultLease{}, TaskCancelNone, false
	}
	outcome, caseID, lease, ok = ConsumeParkSet(&g.park, ticket)
	if !ok {
		return ParkOutcomePending, 0, OperationResultLease{}, TaskCancelNone, false
	}
	if g.park.taskCancelPhase == taskCancelRequested {
		task, ok = ClaimTaskCancellation(p, g)
		if !ok {
			return ParkOutcomePending, 0, OperationResultLease{}, TaskCancelNone, false
		}
	}
	return outcome, caseID, lease, task, true
}

// AcknowledgeTaskCancellation clears terminal bookkeeping only after the task
// has no frame, queue, park, or producer-visible ownership left. It is not a
// safepoint consume operation.
func AcknowledgeTaskCancellation(g *G, kind TaskCancelKind) bool {
	if !ValidG(g) || !validTaskCancelKind(kind) || g.park.taskCancelKind != kind ||
		g.park.taskCancelPhase != taskCancelCleanup ||
		g.state != GDead || !gPreemptStateAtDepthZero(g, preemptDisabled) ||
		g.runAction != ActionInvalid || g.transferState != runnableTransferGIdle ||
		g.root != nil || g.active != nil || g.frames != nil || g.runP != nil ||
		g.nextReady != nil || g.queued || g.waiting ||
		g.pending.kind != pendingNone || g.pending.directChannel || g.pending.from != nil || g.pending.target != nil ||
		g.destroyTarget != nil || g.destroyRoot ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		!releasableParkState(&g.park) {
		return false
	}
	g.park.taskCancelKind = TaskCancelNone
	g.park.taskCancelPhase = taskCancelIdle
	return true
}
