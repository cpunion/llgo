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

package runtime

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

// Keep the runtime-facing names local while the target-neutral implementation
// remains independently testable.
type coroG = coro.G
type coroP = coro.P

func coroInitG(g *coroG) bool {
	return coro.InitG(g)
}

func coroAdoptRoot(g *coroG, handle unsafe.Pointer) bool {
	return coro.AdoptRoot(g, handle)
}

func coroEnqueue(p *coroP, g *coroG) bool {
	return coro.Enqueue(p, g)
}

// coroRunSlice advances at most budget reductions. Source service, dequeue,
// and each complete physical resume/destroy are charged separately. Idle host
// preparation, terminal close, command shutdown, and cost certification are
// intentionally outside this primitive.
func coroRunSlice(p *coroP, main *coroG, driver *coro.ExecutorDriver, budget uint32) coroRunResultV1 {
	if p == nil || main == nil || driver == nil || budget == 0 {
		return coroRunResultV1{}
	}
	if !coroTargetBeforeProgramRunSliceV1(p, driver) {
		return coroRunResultV1{}
	}
	run, runOK := coro.BeginExecutorRunSlice(driver)
	if !runOK {
		return coroRunResultV1{}
	}
	result := coroRunResultV1{}
	held := false
	for result.used < budget {
		var wait, permitOK bool
		held, wait, permitOK = coroPrepareManagedExecutionV1(driver, held)
		if !permitOK {
			_ = coroFinishManagedExecutionV1(driver, held)
			return coroRunResultV1{}
		}
		if wait {
			result.stop = coroRunExecutionWaitV1
			return result
		}
		combineDispatch := held && budget-result.used >= 2
		var actionStep coro.ExecutorRunActionStep
		var actionSelected, ok bool
		if combineDispatch {
			actionStep, actionSelected, ok = run.NextActionCombined()
		} else {
			actionStep, actionSelected, ok = run.NextAction()
		}
		if !ok || actionSelected && !coroActionStepMatchesManagedExecutionV1(actionStep, held) {
			_ = coroFinishManagedExecutionV1(driver, held)
			return coroRunResultV1{}
		}
		policy := coroRunPolicyV1{main: main, lifecycle: &coroProgramLifecycleV1State}
		var terminal, reduced bool
		if actionSelected {
			terminal, reduced = coroReduceExecutorRunActionV1(
				p, driver, policy, actionStep, &result,
			)
		} else {
			step, nextOK := coroProgramNextRunStepV1(driver, &run, combineDispatch)
			if !nextOK || !coroStepMatchesManagedExecutionV1(step, held) {
				_ = coroFinishManagedExecutionV1(driver, held)
				return coroRunResultV1{}
			}
			terminal, reduced = coroReduceExecutorRunStepV1(
				p, driver, policy, step, &result,
			)
		}
		if !reduced {
			_ = coroFinishManagedExecutionV1(driver, held)
			return coroRunResultV1{}
		}
		if terminal {
			if !coroFinishManagedExecutionV1(driver, held) {
				return coroRunResultV1{}
			}
			return result
		}
		stopForReturn, returnOK := coroStopAfterStableReductionV1(
			driver, &result,
		)
		if !returnOK {
			_ = coroFinishManagedExecutionV1(driver, held)
			return coroRunResultV1{}
		}
		if stopForReturn {
			if !coroFinishManagedExecutionV1(driver, held) {
				return coroRunResultV1{}
			}
			return result
		}
	}
	if !coroFinishManagedExecutionV1(driver, held) {
		return coroRunResultV1{}
	}
	result.stop = coroRunSliceBudgetV1
	return result
}

const coroCompatibilityRunBudgetV1 uint32 = 64

// coroFinishRunSliceCompatibility crosses one still-uncertified runner
// boundary without hiding another RunSlice invocation. Keeping this as one
// explicit step lets the host-facing driver return a budget or handoff stop,
// while the legacy whole-episode wrapper below may continue iteratively.
func coroFinishRunSliceCompatibility(
	p *coroP,
	main *coroG,
	driver *coro.ExecutorDriver,
	result coroRunResultV1,
) coroRunResultV1 {
	switch result.stop {
	case coroRunSliceBudgetV1, coroRunPanicCompleteV1, coroRunAgainV1:
		return result
	case coroRunExecutionWaitV1:
		if !coroTargetWaitManagedExecutionV1(driver) {
			return coroRunResultV1{}
		}
		result.stop = coroRunAgainV1
		return result
	case coroRunOSThreadSuspendV1:
		if !coroTargetHandleOSThreadSuspendV1(
			p, driver, result.g, result.action,
		) {
			return coroRunResultV1{}
		}
		result.stop = coroRunAgainV1
		result.g = nil
		result.action = coro.Action{}
		return result
	case coroRunMainDoneV1:
		if !coro.EnterExecutorRunCompatibility(driver) {
			return coroRunResultV1{}
		}
		return result
	case coroRunIdleV1:
		if !coro.EnterExecutorRunCompatibility(driver) {
			return coroRunResultV1{}
		}
		more, drained := coroTargetDrainProgramTransfersV1(p, driver)
		if !drained {
			return coroRunResultV1{}
		}
		if more {
			result.stop = coroRunAgainV1
			return result
		}
		if coroProgramLifecycleV1State == coroProgramMainReturnRequestedV1 && !coro.HasWaiting(p) {
			result.stop = coroRunMainDoneV1
			result.g = main
			return result
		}
		if !coro.HasWaiting(p) && coroProgramLifecycleV1State != coroProgramMainGoexitV1 {
			return coroRunResultV1{}
		}
		// Once command main has Goexited, every remaining G may be executing in
		// another fleet domain. An empty adopted P is therefore a legitimate
		// remote-work wait, not program completion or scheduler corruption. The
		// runnable demand below lets a peer return work here; if the peer instead
		// terminates, its runtime-context release owns the last-G deadlock report.
		if !coroTargetRequestProgramRunnableV1(p, driver) {
			return coroRunResultV1{}
		}
		more, drained = coroTargetDrainProgramTransfersV1(p, driver)
		if !drained {
			return coroRunResultV1{}
		}
		if more {
			result.stop = coroRunAgainV1
			return result
		}
		sleep, deadline, hasDeadline, prepared := coroProgramPrepareExecutorSleepV1(driver)
		if !prepared {
			return coroRunResultV1{}
		}
		if sleep {
			result.stop = coroRunExecutorSleepV1
			result.deadline = deadline
			result.hasDeadline = hasDeadline
			return result
		}
		result.stop = coroRunAgainV1
		return result
	case coroRunDestroyCommitV1:
		var next coro.Action
		var committed bool
		if result.g == main && coroProgramLifecycleV1State == coroProgramMainGoexitV1 {
			// A command root which ended through Goexit is no longer runnable, but
			// its executor remains the event/routing domain for background Gs. The
			// ordinary domain commit finalizes only this G and deliberately avoids
			// the last-local-root terminal-close transaction.
			next, committed = coro.CommitExecutorRunDomainDestroy(
				driver,
				result.g,
				result.action,
			)
		} else {
			next, committed = coro.CommitDestroyedReceiptCompatibility(p, result.g, result.action)
		}
		if !committed {
			return coroRunResultV1{}
		}
		result.action = next
		switch next.Kind {
		case coro.ActionCommitDestroy:
			result.stop = coroRunAgainV1
			return result
		case coro.ActionTerminalExecutorClose:
			result.stop = coroRunTerminalExecutorCloseV1
			return result
		case coro.ActionPanicComplete:
			result.stop = coroRunPanicCompleteV1
			return result
		case coro.ActionComplete:
			isMain := result.g == main
			retireOwner := coro.ActionRetiresPhysicalOwner(next)
			if !coroReleaseCompletedTask(result.g) {
				return coroRunResultV1{}
			}
			if isMain {
				result.stop = coroRunMainDoneV1
				result.g = main
				result.action = coro.Action{}
				return result
			}
			if retireOwner && !coroTargetRetirePhysicalOwnerV1(p, driver) {
				return coroRunResultV1{}
			}
			result.stop = coroRunAgainV1
			return result
		default:
			return coroRunResultV1{}
		}
	default:
		return coroRunResultV1{}
	}
}

// coroRun is the legacy whole-episode compatibility loop. The resumable runner
// above is the production ordering primitive; physical resume wall-work is not
// yet cost-certified. This wrapper explicitly owns the still-unbounded idle
// preparation and terminal-close boundaries.
func coroRun(p *coroP, main *coroG, driver *coro.ExecutorDriver) coroRunResultV1 {
	for {
		result := coroFinishRunSliceCompatibility(
			p,
			main,
			driver,
			coroRunSlice(p, main, driver, coroCompatibilityRunBudgetV1),
		)
		switch result.stop {
		case coroRunSliceBudgetV1, coroRunAgainV1:
			continue
		default:
			return result
		}
	}
}

// coroCancelReady destroys every ready child deepest-to-root. It deliberately
// never calls coro.done or coro.resume: command shutdown owns only suspended
// YieldOnly/AwaitStructured frame chains.
func coroCancelReady(p *coroP) bool {
	for {
		g, action, ok := coro.NextCommandCancel(p)
		if !ok {
			return false
		}
		if g == nil {
			return action.Kind == coro.ActionInvalid && action.Handle == nil
		}
		for {
			switch action.Kind {
			case coro.ActionCancelDestroy:
				if !coroHandleDestroyCommitted(g, action.Handle) {
					return false
				}
				action, ok = coro.CancelDestroyed(p, g, action)
				if !ok {
					return false
				}
			case coro.ActionCancelComplete:
				if action.Handle != nil || !coroReleaseCompletedTask(g) {
					return false
				}
				// g may have been physically freed. Never inspect it again.
				g = nil
				break
			default:
				return false
			}
			if g == nil {
				break
			}
		}
	}
}
