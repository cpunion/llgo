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

// RunDecision is one P-owned transient resume gate. It exists only between
// Checked selecting ActionResume and compiler-generated code at the resumed
// continuation taking the decision. Keeping it on P avoids permanent per-G
// result/cancellation fields.
//
// A park decision carries the exact logical ticket, selected case/outcome and
// winner result lease. A task-only decision has a zero ticket. The zero value
// means the resume has no park or task control event.
type RunDecision struct {
	g       *G
	ticket  ParkTicket
	caseID  uint32
	outcome ParkOutcome
	task    TaskCancelKind
	// materialized selects a frame-local ResumePacket rather than an
	// old-source OperationResultLease. It occupies existing scalar padding.
	materialized bool
	lease        OperationResultLease
}

func validRunDecision(decision RunDecision) bool {
	if decision == (RunDecision{}) {
		return true
	}
	if !ValidG(decision.g) || decision.outcome > ParkOutcomeDefault ||
		(decision.task != TaskCancelNone && !validTaskCancelKind(decision.task)) {
		return false
	}
	hasPark := validParkTicket(decision.ticket)
	if !hasPark {
		return decision.outcome == ParkOutcomePending && decision.caseID == 0 &&
			decision.lease == (OperationResultLease{}) && decision.task != TaskCancelNone &&
			!decision.materialized
	}
	if decision.outcome == ParkOutcomePending {
		return false
	}
	if decision.materialized {
		if decision.lease != (OperationResultLease{}) {
			return false
		}
		switch decision.outcome {
		case ParkOutcomeCompleted, ParkOutcomeDefault:
			return decision.task == TaskCancelNone && decision.caseID != 0
		case ParkOutcomeCanceled:
			return decision.caseID == 0
		default:
			return false
		}
	}
	if decision.outcome == ParkOutcomeCompleted {
		return decision.task == TaskCancelNone && decision.lease.Valid() && decision.lease.ticket == decision.ticket
	}
	if decision.outcome == ParkOutcomeDefault {
		return decision.task == TaskCancelNone && decision.lease == (OperationResultLease{})
	}
	// A canceled logical park normally has no winner lease. Prompt task
	// cancellation may suppress an already selected completion, in which case
	// cleanup still owns the valid lease and must discard/copy its payload.
	return decision.caseID == 0 && (decision.lease == (OperationResultLease{}) ||
		(decision.task != TaskCancelNone && decision.lease.Valid() && decision.lease.ticket == decision.ticket))
}

// resumeGateStructurallyTaken proves that compiler-generated code consumed
// exactly the current P/G resume decision. Critical enter/exit use this shape
// at any nested depth; every ordinary coroutine transition goes through
// resumeGateTaken and is therefore rejected while preemption is masked.
func resumeGateStructurallyTaken(g *G) bool {
	if !ValidG(g) || g.runP == nil {
		return false
	}
	p := g.runP
	return p.current == g && p.inResume && g.state == GRunning &&
		expectedAction(p, g, p.action, ActionResume) &&
		p.runDecision == (RunDecision{}) && p.runDecisionTaken
}

// resumeGateTaken is the suspension-capable compiler hook gate. A non-zero
// critical depth may not await, yield, park, complete, panic, spawn, or publish
// source state that can lead to suspension.
func resumeGateTaken(g *G) bool {
	return resumeGateStructurallyTaken(g) && gPreemptEnabledAtDepthZero(g)
}

// prepareRunDecision is the scheduler's last gate before llvm.coro.resume.
// It runs after the complete SourceSet snapshot and while P exclusively owns
// G. A ready park is consumed here, not in a producer callback or PollReady.
func prepareRunDecision(p *P, g *G) bool {
	if p == nil || !ValidG(g) || p.current != g || g.runP != p || g.state != GRunning ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken || !gPreemptEnabledAtDepthZero(g) {
		return false
	}
	// BeginRunG is the sole producer of this exact P/G/ActionCheckResume
	// episode and already audited the runnable ParkState before publishing it.
	// No producer can mutate a materialized frame packet or scheduler-owned
	// ParkState between that action and Checked. ConsumeTaskParkSet retains its
	// complete validator for source-backed Ready parks; releasable phases retain
	// their complete O(1) validator below. Repeating the materialized audit here
	// would not establish an independent boundary.
	decision := RunDecision{}
	if g.park.phase == parkReady {
		ticket := g.park.ticket
		outcome, caseID, lease, task, ok := ConsumeTaskParkSet(p, g, ticket)
		if !ok {
			return false
		}
		decision = RunDecision{
			g:       g,
			ticket:  ticket,
			outcome: outcome,
			caseID:  caseID,
			lease:   lease,
			task:    task,
		}
	} else if g.park.phase == parkMaterialized {
		decision = RunDecision{
			g:            g,
			ticket:       g.park.ticket,
			outcome:      g.park.outcome,
			caseID:       g.park.winnerCase,
			materialized: true,
		}
	} else if !releasableParkState(&g.park) {
		return false
	}
	if g.park.taskCancelPhase == taskCancelRequested {
		kind, ok := ClaimTaskCancellation(p, g)
		if !ok {
			return false
		}
		if decision == (RunDecision{}) {
			decision = RunDecision{g: g, task: kind}
		} else {
			if decision.materialized {
				decision.outcome = ParkOutcomeCanceled
				decision.caseID = 0
			}
			if decision.task != TaskCancelNone && decision.task != kind {
				return false
			}
			decision.task = kind
		}
	}
	// Every non-zero shape above is produced by an exact consuming helper:
	// ConsumeTaskParkSet, the materialized ParkState certificate, or
	// ClaimTaskCancellation. None accepts caller-supplied decision fields, so a
	// second generic union validation here would only replay those branches.
	// The compiler prologue still correlates the private slot with G/ticket and
	// consumes it exactly once.
	p.runDecision = decision
	return true
}

// TakeRunDecision is the compiler resume prologue. expected is the exact
// ParkTicket retained across a park suspension, or zero at a non-park resume
// point. A stale expectation or wrong G leaves the P slot untouched. The
// returned lease must be copied/discarded through its source before user code
// starts; the physical OperationRecord independently prevents early recycle.
//
// A normal resume has no stored decision and succeeds only with a zero
// expected ticket, returning the all-zero fast path.
func TakeRunDecision(
	g *G,
	expected ParkTicket,
) (outcome ParkOutcome, caseID uint32, lease OperationResultLease, task TaskCancelKind, ok bool) {
	if !ValidG(g) || g.runP == nil {
		return ParkOutcomePending, 0, OperationResultLease{}, TaskCancelNone, false
	}
	p := g.runP
	if p.runDecisionTaken && takeInlineAwaitInitialDecision(g, expected) {
		return ParkOutcomePending, 0, OperationResultLease{}, TaskCancelNone, true
	}
	if p.current != g || !p.inResume || g.state != GRunning ||
		p.runDecisionTaken || !expectedAction(p, g, p.action, ActionResume) || !validRunDecision(p.runDecision) {
		return ParkOutcomePending, 0, OperationResultLease{}, TaskCancelNone, false
	}
	// Keep the P-owned slot addressed instead of copying the complete decision.
	// The copy creates unnecessary frame pressure on register-constrained
	// targets such as Xtensa.
	decision := &p.runDecision
	if *decision == (RunDecision{}) {
		if expected != (ParkTicket{}) {
			return ParkOutcomePending, 0, OperationResultLease{}, TaskCancelNone, false
		}
		p.runDecisionTaken = true
		return ParkOutcomePending, 0, OperationResultLease{}, TaskCancelNone, true
	}
	if decision.g != g || decision.ticket != expected {
		return ParkOutcomePending, 0, OperationResultLease{}, TaskCancelNone, false
	}
	if decision.materialized {
		return ParkOutcomePending, 0, OperationResultLease{}, TaskCancelNone, false
	}
	if validParkTicket(decision.ticket) && !DeliverParkResume(&g.park, decision.ticket) {
		return ParkOutcomePending, 0, OperationResultLease{}, TaskCancelNone, false
	}
	outcome, caseID, lease, task = decision.outcome, decision.caseID, decision.lease, decision.task
	p.runDecision = RunDecision{}
	p.runDecisionTaken = true
	return outcome, caseID, lease, task, true
}
