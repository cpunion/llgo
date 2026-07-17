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

func pQueueContainsWaiter(p *P, target *G) bool {
	if p == nil || target == nil {
		return false
	}
	for slow, fast := p.waitHead, p.waitHead; fast != nil && fast.nextWait != nil; {
		slow = slow.nextWait
		fast = fast.nextWait.nextWait
		if slow == fast {
			return false
		}
	}
	for g := p.waitHead; g != nil; g = g.nextWait {
		if g == target {
			return true
		}
	}
	return false
}

// pOwnsTaskCancellation proves scheduler ownership without adding a permanent
// P pointer or external handle to every G. Cancellation is rare, so an owner
// queue scan is preferable to inflating the hot G/P representation. A future
// multi-P global injection path routes the request to the owning P first.
func pOwnsTaskCancellation(p *P, g *G) bool {
	if p == nil || !ValidG(g) {
		return false
	}
	switch g.state {
	case GRunnable:
		return g.queued && pQueueContainsReady(p, g)
	case GRunning, GDispatching:
		return p.current == g && g.runP == p
	case GWaiting:
		if !g.waiting {
			return false
		}
		if g.waitToken != nil {
			return pQueueContainsWaiter(p, g)
		}
		if g.active != nil && g.active.parkWait != nil {
			return validActiveWaitSetRecordFast(p, g.active.parkWait)
		}
		// Pure ParkState tests may model scheduler ownership with the legacy
		// list while omitting frame metadata. Production V2 parks always take
		// the record path above.
		return pQueueContainsWaiter(p, g)
	default:
		return false
	}
}

// applyTaskCancellationToPark maps the strongest task request into the current
// logical wait. Preparing/sealed/parked waits receive a sticky logical cancel;
// detaching/ready already have a terminal outcome, so the task token is simply
// observed before the selected continuation executes.
func applyTaskCancellationToPark(g *G, kind TaskCancelKind) bool {
	if !ValidG(g) || !validTaskCancelKind(kind) || !validParkState(&g.park) {
		return false
	}
	switch g.park.phase {
	case parkIdle, parkConsumed, parkDelivered:
		return g.state != GWaiting
	case parkPreparing, parkSealed, parkParked:
		return RequestParkCancel(&g.park, g.park.ticket, taskCancelParkKind(kind))
	case parkDetaching, parkReady:
		return true
	default:
		return false
	}
}

// RequestTaskCancellation is owner-P-only. Cross-thread context/I/O/host
// cancellation first publishes a normal OperationID fact and requests the
// executor; the owner P then calls this function if that fact represents task
// termination. Go does not expose an arbitrary goroutine-kill handle, so the
// base G representation needs no per-task external registry.
func RequestTaskCancellation(p *P, g *G, kind TaskCancelKind) bool {
	if !pOwnsTaskCancellation(p, g) || !validTaskCancelKind(kind) {
		return false
	}
	var wait *WaitSetRecord
	if g.state == GWaiting && g.waitToken == nil && g.active != nil && g.active.parkWait != nil {
		wait = g.active.parkWait
		if !canAppendAffectedWaitSet(p, wait) {
			return false
		}
	} else if !validParkState(&g.park) {
		return false
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
		if !applyTaskCancellationToPark(g, strongest) {
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
	if wait != nil {
		appendAffectedWaitSetUnchecked(p, wait)
	}
	return true
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
		g.state != GDead || preemptLoad(preemptAddress(g)) != preemptDisabled ||
		g.root != nil || g.active != nil || g.frames != nil || g.runP != nil ||
		g.nextReady != nil || g.queued || g.nextWait != nil || g.waiting ||
		g.waitToken != nil || g.waitTicket != 0 ||
		g.pending.kind != pendingNone || g.pending.from != nil || g.pending.target != nil ||
		g.pending.wait != nil || g.pending.ticket != 0 || g.destroyTarget != nil || g.destroyRoot ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		!releasableParkState(&g.park) {
		return false
	}
	g.park.taskCancelKind = TaskCancelNone
	g.park.taskCancelPhase = taskCancelIdle
	return true
}
