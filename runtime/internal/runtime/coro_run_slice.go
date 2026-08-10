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
// Their direct execution is inferred only for the compiler-owned raw
// host-stack island, in this case the scheduler owner. Resume may execute the
// coroutine until its next suspend and therefore is neither a bounded foreign
// leaf nor an ordinary synchronous runtime call. Managed coroutine plans
// retain WaitForeign at such edges.

//go:linkname coroHandleDone C.__llgo_coro_done_v1
func coroHandleDone(unsafe.Pointer) bool

//go:linkname coroHandleResume C.__llgo_coro_resume_v1
func coroHandleResume(unsafe.Pointer)

//go:linkname coroHandleDestroy C.__llgo_coro_destroy_v1
func coroHandleDestroy(unsafe.Pointer)

// __llgo_coro_await_inline_v1 resumes one already prepared child on the
// current executor stack. A synchronous completion is destroyed and committed
// here; a real suspension returns false so generated parents unwind through
// llvm.coro.suspend before the outer scheduler consumes the deepest pending
// transition. No native stack survives that false return path.
//
//export __llgo_coro_await_inline_v1
func __llgo_coro_await_inline_v1(g, parent, child unsafe.Pointer) bool {
	task := (*coro.G)(g)
	switch coro.BeginInlineAwait(task, parent, child) {
	case coro.InlineAwaitDeclined:
		return false
	case coro.InlineAwaitStarted:
	default:
		coroRuntimeAbort("invalid coroutine inline child begin")
		return false
	}

	coroHandleResume(child)
	switch coro.FinishInlineAwait(task, parent, child, coroHandleDone(child)) {
	case coro.InlineAwaitSuspend:
		return false
	case coro.InlineAwaitDestroy:
		coroHandleDestroy(child)
		if !coro.CommitInlineAwaitDestroy(task, parent, child) {
			coroRuntimeAbort("invalid coroutine inline child destroy commit")
			return false
		}
		return true
	default:
		coroRuntimeAbort("invalid coroutine inline child return")
		return false
	}
}

type coroProgramLifecycleV1 uint8

const (
	coroProgramUnusedV1 coroProgramLifecycleV1 = iota
	coroProgramBegunV1
	coroProgramRunningV1
	coroProgramMainReturnRequestedV1
	coroProgramStoppingV1
	coroProgramCompleteV1
	coroProgramFailedV1
)

type coroRunStopV1 uint8

const (
	coroRunInvalidV1 coroRunStopV1 = iota
	coroRunMainDoneV1
	coroRunExecutorSleepV1
	coroRunTerminalExecutorCloseV1
	coroRunPanicCompleteV1
	// The remaining stops are internal to the explicit compatibility loop or
	// to an owner-driven fleet slice. The bounded reducer itself never prepares
	// host sleep or crosses terminal close.
	coroRunSliceBudgetV1
	coroRunIdleV1
	coroRunDestroyCommitV1
	coroRunAgainV1
	coroRunExecutionWaitV1
	// coroRunOSThreadSuspendV1 is a native full-thread target boundary after a
	// locked Yield/Park has committed and the target-neutral P phase detached.
	// The reducer has already released its managed-execution permit before the
	// outer owner handles this stop.
	coroRunOSThreadSuspendV1
	// coroRunForeignReentryCompleteV1 returns one fully destroyed synchronous
	// callback child to the native boundary adapter. The parent LLVM resume is
	// already active below C and must not be resumed by the scheduler.
	coroRunForeignReentryCompleteV1
)

type coroRunResultV1 struct {
	stop        coroRunStopV1
	g           *coro.G
	action      coro.Action
	deadline    int64
	hasDeadline bool
	used        uint32
	sources     uint32
	dispatches  uint32
	resumes     uint32
	destroys    uint32
}

// coroRunPolicyV1 contains only command-root behavior. A nil/zero policy is an
// ordinary executor domain: every completed G is reclaimed and no task is
// interpreted as process main. Program mode retains a pointer to the live
// lifecycle word because a resumed main frame may publish normal-main return
// inside the physical resume being reduced.
type coroRunPolicyV1 struct {
	main      *coro.G
	lifecycle *coroProgramLifecycleV1
}

// coroRuntimeContextActivationV1 distinguishes a physical-thread install from
// a nested resume which borrows the same already-installed logical G. The
// latter occurs when C synchronously calls Go while the parent LLVM resume is
// still active below the C frame.
type coroRuntimeContextActivationV1 struct {
	previous unsafe.Pointer
	borrowed bool
}

func (policy coroRunPolicyV1) valid() bool {
	return policy.main == nil && policy.lifecycle == nil || policy.main != nil && policy.lifecycle != nil
}

func (policy coroRunPolicyV1) commandState() (running, returnRequested, ok bool) {
	if policy.main == nil && policy.lifecycle == nil {
		return false, false, true
	}
	if policy.main == nil || policy.lifecycle == nil {
		return false, false, false
	}
	switch *policy.lifecycle {
	case coroProgramRunningV1:
		return true, false, true
	case coroProgramMainReturnRequestedV1:
		return false, true, true
	default:
		return false, false, false
	}
}

// coroRunPhysicalActionV1 is the indivisible runtime half of one runner action
// reduction. Neither Checked's ActionResume/ActionDestroy nor a freed handle is
// observable at a reducer return boundary.
func coroRunPhysicalActionV1(p *coro.P, g *coro.G, action coro.Action) (coro.Action, bool) {
	switch action.Kind {
	case coro.ActionCheckResume:
		next, ok := coro.Checked(p, g, action, coroHandleDone(action.Handle))
		if !ok || next.Kind != coro.ActionResume || next.Handle != action.Handle {
			return coro.Action{}, false
		}
		activation, entered := coroEnterRuntimeContext(g)
		if !entered {
			return coro.Action{}, false
		}
		coroHandleResume(next.Handle)
		if !coroLeaveRuntimeContext(g, activation) {
			return coro.Action{}, false
		}
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

// coroPrepareManagedExecutionV1 acquires the target's process-level execution
// permit before NextExecutorRunStep opens an issued physical Action interval.
// wait=true is an ordinary stable scheduler-stack return: the route keeps all
// runnable and source ownership and waits only for another permit publication.
func coroPrepareManagedExecutionV1(driver *coro.ExecutorDriver) (held, wait, ok bool) {
	pending, valid := coro.ExecutorRunManagedResumePending(driver)
	if !valid {
		return false, false, false
	}
	if !pending {
		return false, false, true
	}
	acquired, valid := coroTargetAcquireManagedExecutionV1(driver)
	if !valid {
		return false, false, false
	}
	return acquired, !acquired, true
}

func coroFinishManagedExecutionV1(driver *coro.ExecutorDriver, held bool) bool {
	return !held || coroTargetReleaseManagedExecutionV1(driver)
}

func coroStepMatchesManagedExecutionV1(step coro.ExecutorRunStep, held bool) bool {
	resume := step.Kind == coro.ExecutorRunStepAction &&
		step.Action.Kind == coro.ActionCheckResume
	return resume == held
}

// coroStopAfterStableReductionV1 is the common post-reducer target gate for
// both the adopted program P and ordinary fleet Ps. It is called only after
// the complete reduction and its managed-execution permit release. Keeping the
// gate shared prevents either outer runner from crossing a detached locked
// owner's exact return boundary.
func coroStopAfterStableReductionV1(
	driver *coro.ExecutorDriver,
	result *coroRunResultV1,
) (stop, ok bool) {
	if driver == nil || result == nil {
		return false, false
	}
	stop, ok = coroTargetStopForOSThreadReturnV1(driver)
	if !ok || !stop {
		return stop, ok
	}
	result.stop = coroRunAgainV1
	return true, true
}

// coroReduceExecutorRunStepV1 is the single physical scheduler reducer shared
// by the process program and every fleet domain. It consumes exactly one step;
// terminal reports a stable slice boundary, while ok=false invalidates the
// whole caller result. Source selection and monotonic-clock ownership stay in
// the thin outer loop for each target.
func coroReduceExecutorRunStepV1(
	p *coro.P,
	driver *coro.ExecutorDriver,
	policy coroRunPolicyV1,
	step coro.ExecutorRunStep,
	result *coroRunResultV1,
) (terminal, ok bool) {
	if p == nil || driver == nil || result == nil || !policy.valid() {
		return false, false
	}
	_, returnRequested, stateOK := policy.commandState()
	if !stateOK {
		return false, false
	}
	switch step.Kind {
	case coro.ExecutorRunStepSource:
		distributed, targetOK := coroTargetAfterSourceReductionV1(p, driver, step.Poll)
		if !targetOK || distributed && !step.Poll.Complete ||
			step.Poll.Complete && !coro.CommitExecutorRunSourceDistribution(driver, distributed) {
			return false, false
		}
		result.used++
		result.sources++
		return false, true
	case coro.ExecutorRunStepMaterialize:
		if !coroMaterializeResumeCleanupStepV1(step.Cleanup) {
			return false, false
		}
		result.used++
		return false, true
	case coro.ExecutorRunStepDispatch:
		if step.G == nil || step.Action.Handle == nil {
			return false, false
		}
		if returnRequested && step.G != policy.main && step.Action.Kind == coro.ActionCheckResume &&
			!coro.RequestTaskCancellation(p, step.G, coro.TaskCancelShutdown) {
			return false, false
		}
		result.used++
		result.dispatches++
		return false, true
	case coro.ExecutorRunStepAction:
		if step.G == nil || step.Action.Handle == nil {
			return false, false
		}
		next, advanced := coroRunPhysicalActionV1(p, step.G, step.Action)
		// The physical resume may have changed program lifecycle. Re-read the
		// live policy before selecting the scheduler commit placement.
		running, returnRequested, stateOK := policy.commandState()
		if !stateOK {
			return false, false
		}
		committed := false
		if advanced && step.G == policy.main && returnRequested && next.Kind == coro.ActionCheckDestroy {
			committed = coro.CommitExecutorRunCommandRootDestroy(driver, step.G, next)
		} else if advanced && step.G == policy.main && running &&
			coro.CommitExecutorRunCommandBootstrapDirectChildHandoff(driver, step.G, next) {
			committed = true
		} else if advanced {
			committed = coro.CommitExecutorRunAction(driver, step.G, next)
		}
		if !committed {
			return false, false
		}
		if next.Kind == coro.ActionForeignReentryComplete {
			result.used++
			result.destroys++
			result.stop = coroRunForeignReentryCompleteV1
			result.g = step.G
			result.action = next
			return true, true
		}
		// A locked ordinary suspension must decide whether to detach before
		// ready distribution can move the peer which justifies a Yield handoff.
		// Non-native targets compile this observation to a no-op.
		osThreadSuspend, suspendOK := coroTargetPrepareOSThreadSuspendV1(
			p, driver, step.G, next,
		)
		if !suspendOK {
			return false, false
		}
		// A resume commit is the first stable scheduler-stack boundary after a
		// managed `go` statement publishes its initial child. Native fleet targets
		// may opportunistically hand that exact ready head to another P here; all
		// other targets compile this call to a no-op. Failure after an actual
		// publication is fatal because the mailbox has become the child's sole root.
		if !osThreadSuspend && !coroTargetAfterStableRunActionV1(p, driver) {
			return false, false
		}
		result.used++
		switch step.Action.Kind {
		case coro.ActionCheckResume:
			result.resumes++
		case coro.ActionCheckDestroy, coro.ActionPanicDestroy:
			result.destroys++
		}
		if osThreadSuspend {
			result.stop = coroRunOSThreadSuspendV1
			result.g = step.G
			result.action = next
			return true, true
		}
		switch next.Kind {
		case coro.ActionCheckResume, coro.ActionCheckDestroy, coro.ActionPanicDestroy:
			if step.G == policy.main && returnRequested && next.Kind == coro.ActionCheckResume {
				return false, false
			}
		case coro.ActionYield, coro.ActionPark:
			if step.G == policy.main && returnRequested {
				return false, false
			}
		case coro.ActionComplete:
			isMain := step.G == policy.main
			retireOwner := coro.ActionRetiresPhysicalOwner(next)
			if !coroReleaseCompletedTask(step.G) {
				return false, false
			}
			if isMain {
				result.stop, result.g = coroRunMainDoneV1, policy.main
				return true, true
			}
			if retireOwner && !coroTargetRetirePhysicalOwnerV1(p, driver) {
				return false, false
			}
		case coro.ActionPanicComplete:
			result.stop, result.g, result.action = coroRunPanicCompleteV1, step.G, next
			return true, true
		case coro.ActionCommitDestroy:
			result.stop, result.g, result.action = coroRunDestroyCommitV1, step.G, next
			return true, true
		default:
			return false, false
		}
		return false, true
	case coro.ExecutorRunStepDestroyCommit:
		if step.G == nil || step.Action.Kind != coro.ActionCommitDestroy || step.Action.Handle != nil {
			return false, false
		}
		result.stop, result.g, result.action = coroRunDestroyCommitV1, step.G, step.Action
		return true, true
	case coro.ExecutorRunStepIdle:
		result.stop = coroRunIdleV1
		return true, true
	default:
		return false, false
	}
}

// coroRunSliceAtV1 advances one ordinary fleet domain using a host-owned,
// explicit monotonic sample. The same sample may span the bounded call (as in
// WASM/embedded host turns); a native owner supplies a fresh sample on its next
// entry. Command-main placement is deliberately unavailable in this variant.
func coroRunSliceAtV1(p *coro.P, driver *coro.ExecutorDriver, now int64, budget uint32) coroRunResultV1 {
	if p == nil || driver == nil || now < 0 || budget == 0 {
		return coroRunResultV1{}
	}
	result := coroRunResultV1{}
	for result.used < budget {
		held, wait, permitOK := coroPrepareManagedExecutionV1(driver)
		if !permitOK {
			return coroRunResultV1{}
		}
		if wait {
			result.stop = coroRunExecutionWaitV1
			return result
		}
		step, nextOK := coro.NextExecutorRunStepAt(driver, now)
		if !nextOK || !coroStepMatchesManagedExecutionV1(step, held) {
			_ = coroFinishManagedExecutionV1(driver, held)
			return coroRunResultV1{}
		}
		terminal, reduced := coroReduceExecutorRunStepV1(
			p, driver, coroRunPolicyV1{}, step, &result,
		)
		if !coroFinishManagedExecutionV1(driver, held) || !reduced {
			return coroRunResultV1{}
		}
		if terminal {
			return result
		}
		stopForReturn, returnOK := coroStopAfterStableReductionV1(
			driver, &result,
		)
		if !returnOK {
			return coroRunResultV1{}
		}
		if stopForReturn {
			return result
		}
	}
	result.stop = coroRunSliceBudgetV1
	return result
}
