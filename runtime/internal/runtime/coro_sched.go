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

// These compiler-owned C ABI wrappers are emitted in the program entry module.
// They hide LLVM's post-CoroSplit handle layout from the Go runtime.
// schedulerwait restricts them to a compiler-owned raw host-stack island, in
// this case the scheduler owner. Resume may execute the coroutine until its
// next suspend and therefore is neither a bounded foreign leaf nor an ordinary
// synchronous runtime call. The other permitted island is a worker callback;
// managed coroutine plans retain WaitForeign at such edges.

//llgo:coro schedulerwait
//go:linkname coroHandleDone C.__llgo_coro_done_v1
func coroHandleDone(unsafe.Pointer) bool

//llgo:coro schedulerwait
//go:linkname coroHandleResume C.__llgo_coro_resume_v1
func coroHandleResume(unsafe.Pointer)

//llgo:coro schedulerwait
//go:linkname coroHandleDestroy C.__llgo_coro_destroy_v1
func coroHandleDestroy(unsafe.Pointer)

// Keep the runtime-facing names local while the target-neutral implementation
// remains independently testable.
type coroG = coro.G
type coroP = coro.P

type coroRunStopV1 uint8

const (
	coroRunInvalidV1 coroRunStopV1 = iota
	coroRunMainDoneV1
	coroRunExecutorSleepV1
	coroRunTerminalExecutorCloseV1
	coroRunPanicCompleteV1
	// The remaining stops are internal to the explicit compatibility loop.
	// coroRunSlice itself never prepares host sleep or crosses terminal close.
	coroRunSliceBudgetV1
	coroRunIdleV1
	coroRunDestroyCommitV1
	coroRunAgainV1
)

type coroRunResultV1 struct {
	stop        coroRunStopV1
	g           *coroG
	action      coro.Action
	deadline    int64
	hasDeadline bool
	used        uint32
	sources     uint32
	dispatches  uint32
	resumes     uint32
	destroys    uint32
}

func coroInitG(g *coroG) bool {
	return coro.InitG(g)
}

func coroAdoptRoot(g *coroG, handle unsafe.Pointer) bool {
	return coro.AdoptRoot(g, handle)
}

func coroEnqueue(p *coroP, g *coroG) bool {
	return coro.Enqueue(p, g)
}

// coroRunPhysicalActionV1 is the indivisible runtime half of one runner action
// reduction. Neither Checked's ActionResume/ActionDestroy nor a freed handle is
// observable at a RunSlice return boundary.
func coroRunPhysicalActionV1(p *coroP, g *coroG, action coro.Action) (coro.Action, bool) {
	switch action.Kind {
	case coro.ActionCheckResume:
		next, ok := coro.Checked(p, g, action, coroHandleDone(action.Handle))
		if !ok || next.Kind != coro.ActionResume || next.Handle != action.Handle {
			return coro.Action{}, false
		}
		coroHandleResume(next.Handle)
		return coro.Resumed(p, g, next)
	case coro.ActionCheckDestroy:
		next, ok := coro.Checked(p, g, action, coroHandleDone(action.Handle))
		if !ok || next.Kind != coro.ActionDestroy || next.Handle != action.Handle {
			return coro.Action{}, false
		}
		coroHandleDestroy(next.Handle)
		return coro.DestroyedBounded(p, g, next)
	case coro.ActionPanicDestroy:
		coroHandleDestroy(action.Handle)
		return coro.PanicDestroyedBounded(p, g, action)
	default:
		return coro.Action{}, false
	}
}

// coroRunSlice advances at most budget reductions. Source service, dequeue,
// and each complete physical resume/destroy are charged separately. Idle host
// preparation, terminal close, command shutdown, and cost certification are
// intentionally outside this primitive.
func coroRunSlice(p *coroP, main *coroG, driver *coro.ExecutorDriver, budget uint32) coroRunResultV1 {
	if p == nil || main == nil || driver == nil || budget == 0 {
		return coroRunResultV1{}
	}
	result := coroRunResultV1{}
	for result.used < budget {
		step, ok := coroProgramNextRunStepV1(driver)
		if !ok {
			return coroRunResultV1{}
		}
		switch step.Kind {
		case coro.ExecutorRunStepSource:
			result.used++
			result.sources++
		case coro.ExecutorRunStepDispatch:
			if step.G == nil || step.Action.Handle == nil {
				return coroRunResultV1{}
			}
			if coroProgramLifecycleV1State == coroProgramMainReturnRequestedV1 &&
				step.G != main && step.Action.Kind == coro.ActionCheckResume &&
				!coro.RequestTaskCancellation(p, step.G, coro.TaskCancelShutdown) {
				return coroRunResultV1{}
			}
			result.used++
			result.dispatches++
		case coro.ExecutorRunStepAction:
			if step.G == nil || step.Action.Handle == nil {
				return coroRunResultV1{}
			}
			next, advanced := coroRunPhysicalActionV1(p, step.G, step.Action)
			committed := false
			if advanced && step.G == main && coroProgramLifecycleV1State == coroProgramMainReturnRequestedV1 &&
				next.Kind == coro.ActionCheckDestroy {
				committed = coro.CommitExecutorRunCommandRootDestroy(driver, step.G, next)
			} else if advanced && step.G == main && coroProgramLifecycleV1State == coroProgramRunningV1 &&
				coro.CommitExecutorRunCommandBootstrapDirectChildHandoff(driver, step.G, next) {
				committed = true
			} else if advanced {
				committed = coro.CommitExecutorRunAction(driver, step.G, next)
			}
			if !committed {
				return coroRunResultV1{}
			}
			result.used++
			switch step.Action.Kind {
			case coro.ActionCheckResume:
				result.resumes++
			case coro.ActionCheckDestroy, coro.ActionPanicDestroy:
				result.destroys++
			}
			switch next.Kind {
			case coro.ActionCheckResume, coro.ActionCheckDestroy, coro.ActionPanicDestroy:
				if step.G == main && coroProgramLifecycleV1State == coroProgramMainReturnRequestedV1 &&
					next.Kind == coro.ActionCheckResume {
					return coroRunResultV1{}
				}
			case coro.ActionYield, coro.ActionPark:
				if step.G == main && coroProgramLifecycleV1State == coroProgramMainReturnRequestedV1 {
					return coroRunResultV1{}
				}
			case coro.ActionComplete:
				isMain := step.G == main
				if !coroReleaseCompletedTask(step.G) {
					return coroRunResultV1{}
				}
				if isMain {
					result.stop, result.g = coroRunMainDoneV1, main
					return result
				}
			case coro.ActionPanicComplete:
				result.stop, result.g, result.action = coroRunPanicCompleteV1, step.G, next
				return result
			case coro.ActionCommitDestroy:
				result.stop, result.g, result.action = coroRunDestroyCommitV1, step.G, next
				return result
			default:
				return coroRunResultV1{}
			}
		case coro.ExecutorRunStepDestroyCommit:
			if step.G == nil || step.Action.Kind != coro.ActionCommitDestroy || step.Action.Handle != nil {
				return coroRunResultV1{}
			}
			result.stop, result.g, result.action = coroRunDestroyCommitV1, step.G, step.Action
			return result
		case coro.ExecutorRunStepIdle:
			result.stop = coroRunIdleV1
			return result
		default:
			return coroRunResultV1{}
		}
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
	case coroRunSliceBudgetV1, coroRunPanicCompleteV1:
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
		if coroProgramLifecycleV1State == coroProgramMainReturnRequestedV1 && !coro.HasWaiting(p) {
			result.stop = coroRunMainDoneV1
			result.g = main
			return result
		}
		if !coro.HasWaiting(p) {
			return coroRunResultV1{}
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
		next, committed := coro.CommitDestroyedReceiptCompatibility(p, result.g, result.action)
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
			if !coroReleaseCompletedTask(result.g) {
				return coroRunResultV1{}
			}
			if isMain {
				result.stop = coroRunMainDoneV1
				result.g = main
				result.action = coro.Action{}
				return result
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
				coroHandleDestroy(action.Handle)
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
