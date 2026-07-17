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

// executorRunSourceQuantum bounds how long a continuously ready FIFO can avoid
// sampling deadline sources when no producer request is pending. A requested or
// retryable source is serviced immediately; the quantum is only the fallback
// for a hot CPU-only workload.
const executorRunSourceQuantum uint8 = 64

// executorRunCursor is cold, scheduler-owner-only continuation state. It has
// no callback-visible pointer. readyDebt forces one physical ready action after
// a completed source epoch before a hot source can start another A/ack/B epoch.
// issued marks the no-return interval between selecting a stable action and the
// runtime adapter committing its complete physical reduction.
type executorRunCursor struct {
	sourceMore         bool
	readyDebt          bool
	blocked            bool
	actionsSinceSource uint8
	issued             ActionKind
}

func validExecutorRunCursor(cursor *executorRunCursor, p *P) bool {
	if cursor == nil || p == nil || cursor.actionsSinceSource > executorRunSourceQuantum {
		return false
	}
	switch cursor.issued {
	case ActionInvalid:
	case ActionCheckResume, ActionCheckDestroy, ActionPanicDestroy:
	default:
		return false
	}
	return cursor.issued != ActionInvalid || !cursor.readyDebt || p.current != nil || p.readyHead != nil
}

func emptyExecutorRunCursor(driver *ExecutorDriver) bool {
	return driver != nil && driver.run == (executorRunCursor{})
}

// EnterExecutorRunCompatibility is the only supported stable-idle switch from
// the bounded runner to legacy whole-operation poll/sleep/command-close APIs.
// The final-root receipt has its separate CommitDestroyedReceiptCompatibility
// boundary because P intentionally remains current there. This switch discards
// only cold fairness/accounting state; a started source transaction or issued
// physical action cannot cross it.
func EnterExecutorRunCompatibility(driver *ExecutorDriver) bool {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		driver.run.issued != ActionInvalid || driver.poll.phase != executorPollIdle ||
		!idleExecutorScheduler(driver.p) {
		return false
	}
	driver.run = executorRunCursor{}
	return true
}

// ExecutorRunStepKind is one reduction selected by the unified resumable core.
// Dispatch is separate from Action so budget one always has a stable return
// after dequeue/BeginRunG. Source advances exactly one PollExecutorSlice
// reduction. Action must be completed and committed without returning through
// another host boundary.
type ExecutorRunStepKind uint8

const (
	ExecutorRunStepInvalid ExecutorRunStepKind = iota
	ExecutorRunStepSource
	ExecutorRunStepDispatch
	ExecutorRunStepAction
	ExecutorRunStepDestroyCommit
	ExecutorRunStepIdle
)

// ExecutorRunStep carries no callback or interface value. Action handles are
// live only for Dispatch/Action. DestroyCommit is always handle-free.
type ExecutorRunStep struct {
	Kind   ExecutorRunStepKind
	G      *G
	Action Action
	Poll   ExecutorPollProgress
}

func executorRunExternalSourceRequested(driver *ExecutorDriver) bool {
	return driver.sources.pending(driver.p) ||
		driver.registry.ObserveRequested(driver.handle) ||
		preemptLoad(&driver.p.schedule) != scheduleIdle
}

func executorRunSourceRequested(driver *ExecutorDriver) bool {
	return driver.run.sourceMore || executorRunExternalSourceRequested(driver)
}

func serviceExecutorRunSource(driver *ExecutorDriver, now int64, withDeadline bool) (ExecutorRunStep, bool) {
	var progress ExecutorPollProgress
	var ok bool
	if withDeadline {
		_, progress, ok = pollExecutorSliceAt(driver, now, true, 1)
	} else {
		_, progress, ok = pollExecutorSliceAt(driver, 0, false, 1)
	}
	if !ok || progress.Used != 1 {
		return ExecutorRunStep{}, false
	}
	if progress.Complete {
		driver.run.sourceMore = executorRunExternalSourceRequested(driver)
		driver.run.blocked = progress.Blocked
		driver.run.actionsSinceSource = 0
		if driver.p.readyHead != nil {
			driver.run.readyDebt = true
		}
	} else {
		driver.run.sourceMore = true
		driver.run.blocked = false
	}
	return ExecutorRunStep{Kind: ExecutorRunStepSource, Poll: progress}, true
}

func dispatchExecutorRunReady(driver *ExecutorDriver) (ExecutorRunStep, bool) {
	p := driver.p
	if !validReadyQueueHeader(p) {
		return ExecutorRunStep{}, false
	}
	g := dequeue(p)
	if g == nil {
		return ExecutorRunStep{}, false
	}
	action, ok := BeginRunG(p, g)
	if !ok {
		// dequeue only clears the selected head's scheduler-owned queue fields.
		// Restore those exact fields on a fail-closed BeginRunG rejection so a
		// malformed head cannot turn a rejected bounded reduction into a hidden
		// queue mutation.
		prependReadyUnchecked(p, g)
		return ExecutorRunStep{}, false
	}
	return ExecutorRunStep{Kind: ExecutorRunStepDispatch, G: g, Action: action}, true
}

func nextExecutorRunStepAt(driver *ExecutorDriver, now int64, withDeadline bool) (ExecutorRunStep, bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		driver.sources.usesMonotonicTime() != withDeadline || withDeadline && now < 0 ||
		driver.run.issued != ActionInvalid {
		return ExecutorRunStep{}, false
	}
	p := driver.p
	if p.current != nil {
		action, g := p.action, p.current
		if action.Kind == ActionCommitDestroy {
			if !validDestroyCommitReceipt(p, g, action) {
				return ExecutorRunStep{}, false
			}
			return ExecutorRunStep{Kind: ExecutorRunStepDestroyCommit, G: g, Action: action}, true
		}
		if action.Handle == nil ||
			(action.Kind != ActionCheckResume && action.Kind != ActionCheckDestroy && action.Kind != ActionPanicDestroy) {
			return ExecutorRunStep{}, false
		}
		driver.run.issued = action.Kind
		return ExecutorRunStep{Kind: ExecutorRunStepAction, G: g, Action: action}, true
	}
	if p.inResume || p.action != (Action{}) || p.runDecision != (RunDecision{}) ||
		p.runDecisionTaken || p.servicePreemptBudget != 0 {
		return ExecutorRunStep{}, false
	}

	// Once epoch A starts, acknowledgement and epoch B finish before any G.
	if driver.poll.phase != executorPollIdle {
		return serviceExecutorRunSource(driver, now, withDeadline)
	}
	if driver.run.readyDebt {
		if p.readyHead != nil {
			return dispatchExecutorRunReady(driver)
		}
		driver.run.readyDebt = false
	}
	if executorRunSourceRequested(driver) ||
		driver.run.actionsSinceSource == executorRunSourceQuantum ||
		p.readyHead == nil && HasWaiting(p) && !driver.run.blocked {
		return serviceExecutorRunSource(driver, now, withDeadline)
	}
	if p.readyHead != nil {
		return dispatchExecutorRunReady(driver)
	}
	return ExecutorRunStep{Kind: ExecutorRunStepIdle}, true
}

// NextExecutorRunStep selects one no-deadline runner reduction. It never calls
// PollExecutor, PollReady, or NextRunnable; all source work goes through the
// budget-one PollExecutorSlice cursor.
func NextExecutorRunStep(driver *ExecutorDriver) (ExecutorRunStep, bool) {
	if driver == nil || driver.sources.usesMonotonicTime() {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAt(driver, 0, false)
}

// NextExecutorRunStepAt is the deadline-capable counterpart. A fresh sample is
// accepted at each source reduction; PollExecutorSliceAt freezes the correct
// sample across each logical A or B epoch.
func NextExecutorRunStepAt(driver *ExecutorDriver, now int64) (ExecutorRunStep, bool) {
	if driver == nil || !driver.sources.usesMonotonicTime() {
		return ExecutorRunStep{}, false
	}
	return nextExecutorRunStepAt(driver, now, true)
}

func completedExecutorRunAction(p *P, g *G, action Action) bool {
	if p == nil || g == nil || action.Handle != nil || p.current != nil || p.inResume ||
		p.action != (Action{}) || p.runDecision != (RunDecision{}) || p.runDecisionTaken ||
		p.servicePreemptBudget != 0 || g.runP != nil || g.runAction != ActionInvalid {
		return false
	}
	switch action.Kind {
	case ActionYield:
		return g.state == GRunnable && g.queued
	case ActionPark:
		return g.state == GWaiting && (g.waiting || g.active != nil && g.active.parkWait != nil)
	case ActionComplete:
		return g.state == GDead && !g.panicUnwind
	case ActionPanicComplete:
		return g.state == GDead && publishedPanicRecord(&g.panicRecord)
	default:
		return false
	}
}

// CommitExecutorRunAction closes the no-return physical interval opened by an
// Action step. A live continuation is moved to the ready tail; terminal and
// yield/park control actions are already stable. The function retains neither
// the completed G nor its old handle, so a runtime may reclaim a dynamic G
// immediately after a successful ActionComplete commit.
func commitExecutorRunAction(driver *ExecutorDriver, g *G, next Action, first bool) bool {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		driver.run.issued == ActionInvalid || g == nil {
		return false
	}
	p := driver.p
	committed := false
	switch next.Kind {
	case ActionCheckResume, ActionCheckDestroy, ActionPanicDestroy:
		committed = pauseExecutorRunAction(p, g, next, first)
	case ActionYield, ActionPark, ActionComplete, ActionPanicComplete:
		if first {
			return false
		}
		committed = completedExecutorRunAction(p, g, next)
	case ActionCommitDestroy:
		if first {
			return false
		}
		committed = validDestroyCommitReceipt(p, g, next)
	}
	if !committed {
		return false
	}
	driver.run.issued = ActionInvalid
	driver.run.readyDebt = false
	driver.run.blocked = false
	if driver.run.actionsSinceSource < executorRunSourceQuantum {
		driver.run.actionsSinceSource++
	}
	return true
}

// CommitExecutorRunAction closes an ordinary physical action and retains FIFO
// ordering for every live continuation.
func CommitExecutorRunAction(driver *ExecutorDriver, g *G, next Action) bool {
	return commitExecutorRunAction(driver, g, next, false)
}

// CommitExecutorRunCommandRootDestroy is the sole non-FIFO continuation. It is
// valid only for the one final root destroy after command main published its
// normal-return marker; running another user G first would violate Go process
// exit semantics. The destroy remains a separately charged later reduction.
func CommitExecutorRunCommandRootDestroy(driver *ExecutorDriver, g *G, next Action) bool {
	if g == nil || next.Kind != ActionCheckDestroy || g.destroyTarget == nil ||
		g.destroyTarget != g.root || !g.destroyRoot || g.active != nil || g.panicUnwind ||
		!emptyPanicRecord(&g.panicRecord) {
		return false
	}
	return commitExecutorRunAction(driver, g, next, true)
}
