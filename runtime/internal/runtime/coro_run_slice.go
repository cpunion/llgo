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

func coroHandleDestroyCommitted(g *coro.G, handle unsafe.Pointer) bool {
	if g == nil || handle == nil {
		return false
	}
	coroHandleDestroy(handle)
	return coro.CommitFrameDestroyCompiler(g, handle)
}

//export __llgo_coro_await_inline_finish_v2
func __llgo_coro_await_inline_finish_v2(g, parent, child unsafe.Pointer, done bool) bool {
	switch coro.FinishInlineAwaitCompiler((*coro.G)(g), parent, child, done) {
	case coro.InlineAwaitSuspend:
		return false
	case coro.InlineAwaitDestroy:
		return true
	default:
		coroRuntimeAbort("invalid coroutine inline child finish")
		return false
	}
}

type coroProgramLifecycleV1 uint8

const (
	coroProgramUnusedV1 coroProgramLifecycleV1 = iota
	coroProgramBegunV1
	coroProgramRunningV1
	// coroProgramMainGoexitV1 keeps the command executor alive after the main
	// logical G has completed through runtime.Goexit. Unlike normal main return,
	// background goroutines are not canceled: the last registered G owns the
	// standard main-Goexit deadlock decision.
	coroProgramMainGoexitV1
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
	// The bounded run slice releases its managed-execution P lease before the
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

// coroRunTargetCapabilityV1 is sampled once at the stable entry to a bounded
// run slice. readyDistribution certifies immutable fleet-domain ownership and
// policyEpoch identifies its placement-policy observation. Target glue leaves
// policyEpoch zero when it has no dynamically mutable placement policy.
// physicalReturn is an exact negative capability: it is enabled only for an
// attached lock island or while this M is a claimed replacement. Ordinary
// owners therefore pay no post-reduction return-policy hook.
type coroRunTargetCapabilityV1 struct {
	readyDistribution bool
	physicalReturn    bool
	policyEpoch       uint32
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

// coroRunPhysicalActionV1 is the indivisible runtime half of one runner action
// reduction. Neither Checked's ActionResume/ActionDestroy nor a freed handle is
// observable at a reducer return boundary.
func coroRunPhysicalActionV1(
	p *coro.P,
	driver *coro.ExecutorDriver,
	g *coro.G,
	action coro.Action,
	runtimeContext unsafe.Pointer,
) (next coro.Action, advanced, committed bool) {
	switch action.Kind {
	case coro.ActionCheckResume:
		next, needsRuntimeContext, ok := coro.BeginIssuedExecutorResumeRuntimeContext(driver, g)
		if !ok {
			return coro.Action{}, false, false
		}
		if needsRuntimeContext {
			activation, entered := coroEnterRuntimeContextFrom(g, runtimeContext)
			if !entered {
				return coro.Action{}, false, false
			}
			coroHandleResume(next.Handle)
			if !coroLeaveRuntimeContext(g, activation) {
				return coro.Action{}, false, false
			}
		} else {
			coroHandleResume(next.Handle)
		}
		next, committed, advanced = coro.ResumedExecutorRun(driver, p, g, next)
		return next, advanced, committed
	case coro.ActionCheckDestroy:
		next, ok := coro.CheckedExecutorRun(driver, g, action, coroHandleDone(action.Handle))
		if !ok || next.Kind != coro.ActionDestroy || next.Handle != action.Handle {
			return coro.Action{}, false, false
		}
		if !coroHandleDestroyCommitted(g, next.Handle) {
			return coro.Action{}, false, false
		}
		next, advanced = coro.DestroyedBounded(p, g, next)
		return next, advanced, false
	case coro.ActionPanicDestroy:
		if !coroHandleDestroyCommitted(g, action.Handle) {
			return coro.Action{}, false, false
		}
		next, advanced = coro.PanicDestroyedBounded(p, g, action)
		return next, advanced, false
	default:
		return coro.Action{}, false, false
	}
}

// coroPrepareManagedExecutionV1 acquires the target's process-level P lease
// before NextExecutorRunStep opens an issued physical Action interval.
// A lease already held by this bounded run slice is retained across source,
// dispatch, and later physical-action reductions: it represents an M owning a
// P, rather than a fresh quota transaction around every llvm.coro.resume.
// wait=true is an ordinary stable scheduler-stack return: the route keeps all
// runnable and source ownership and waits only for another P publication.
func coroPrepareManagedExecutionV1(driver *coro.ExecutorDriver) (nextHeld, wait, ok bool) {
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
	return !resume || held
}

func coroActionStepMatchesManagedExecutionV1(step coro.ExecutorRunActionStep, held bool) bool {
	return step.Action.Kind != coro.ActionCheckResume || held
}

// coroStopAfterStableReductionV1 is the common post-reducer target gate for
// both the adopted program P and ordinary fleet Ps. It is called only after
// the complete reduction at a stable scheduler boundary. The bounded run slice
// may still retain its P lease; a requested target return releases that lease
// immediately before crossing the outer boundary. Keeping the gate shared
// prevents either runner from crossing a detached locked owner's exact return
// boundary.
func coroStopAfterStableReductionV1(
	driver *coro.ExecutorDriver,
	result *coroRunResultV1,
) (stop, ok bool) {
	if driver == nil || result == nil {
		return false, false
	}
	stop, ok = coroTargetStopForPhysicalReturnV1(driver)
	if !ok || !stop {
		return stop, ok
	}
	result.stop = coroRunAgainV1
	return true, true
}

// coroReduceExecutorRunActionPreparedV1 consumes the compact hot action ABI.
// The caller has already sampled command-return state for the dispatch gate;
// the physical resume may change it, so the post-resume sample remains the
// commit authority exactly as in the general reducer.
func coroReduceExecutorRunActionPreparedV1(
	p *coro.P,
	driver *coro.ExecutorDriver,
	policy coroRunPolicyV1,
	target coroRunTargetCapabilityV1,
	g *coro.G,
	action coro.Action,
	dispatched bool,
	returnRequested bool,
	runtimeContext unsafe.Pointer,
	result *coroRunResultV1,
) (terminal, ok bool) {
	if g == nil || action.Handle == nil || runtimeContext == nil {
		return false, false
	}
	if dispatched {
		if returnRequested && g != policy.main && action.Kind == coro.ActionCheckResume &&
			!coro.RequestTaskCancellation(p, g, coro.TaskCancelShutdown) {
			return false, false
		}
		result.dispatches++
	}
	next, advanced, committed := coroRunPhysicalActionV1(p, driver, g, action, runtimeContext)
	// The physical resume may have changed program lifecycle. Re-read the live
	// policy before selecting the scheduler commit placement.
	running, returnRequested := false, false
	if policy.main != nil {
		if policy.lifecycle == nil {
			return false, false
		}
		switch *policy.lifecycle {
		case coroProgramRunningV1, coroProgramMainGoexitV1:
			running = true
		case coroProgramMainReturnRequestedV1:
			returnRequested = true
		default:
			return false, false
		}
	} else if policy.lifecycle != nil {
		return false, false
	}
	if advanced && !committed && g == policy.main && returnRequested && next.Kind == coro.ActionCheckDestroy {
		committed = coro.CommitExecutorRunCommandRootDestroy(driver, g, next)
	} else if advanced && !committed && g == policy.main && running &&
		coro.CommitExecutorRunCommandBootstrapDirectChildHandoff(driver, g, next) {
		committed = true
	} else if advanced && !committed {
		committed = coro.CommitExecutorRunAction(driver, g, next)
	}
	if !committed {
		return false, false
	}
	if next.Kind == coro.ActionForeignReentryComplete {
		result.used++
		if dispatched {
			result.used++
		}
		result.destroys++
		result.stop = coroRunForeignReentryCompleteV1
		result.g = g
		result.action = next
		return true, true
	}
	// A locked ordinary suspension must decide whether to detach before ready
	// distribution can move the peer which justifies a Yield handoff. The
	// physical resume may itself call LockOSThread, so the slice-entry target
	// capability is not sufficient here. The committed G is nevertheless the
	// exact candidate: avoid the target adapter for the common zero-depth task,
	// and retain its complete validation for every dynamically locked task.
	osThreadSuspend := false
	if coro.OSThreadSuspendHandoffCandidate(g) {
		var suspendOK bool
		osThreadSuspend, suspendOK = coroTargetPrepareOSThreadSuspendV1(
			p, driver, g, next,
		)
		if !suspendOK {
			return false, false
		}
	}
	stopAfterStable := false
	if !osThreadSuspend && (target.readyDistribution || next.Kind == coro.ActionYield) {
		distribute, stop, distributionOK := false, false, false
		if next.Kind == coro.ActionYield {
			distribute, stop, distributionOK = coroTargetRefreshRunSliceV1(target)
		} else {
			distribute, stop, distributionOK = coroTargetReadyDistributionV1(target)
		}
		if !distributionOK || distribute && !coroTargetAfterStableRunActionV1(p, driver) {
			return false, false
		}
		stopAfterStable = stop
	}
	result.used++
	if dispatched {
		result.used++
	}
	switch action.Kind {
	case coro.ActionCheckResume:
		result.resumes++
	case coro.ActionCheckDestroy, coro.ActionPanicDestroy:
		result.destroys++
	}
	if osThreadSuspend {
		result.stop = coroRunOSThreadSuspendV1
		result.g = g
		result.action = next
		return true, true
	}
	switch next.Kind {
	case coro.ActionCheckResume, coro.ActionCheckDestroy, coro.ActionPanicDestroy:
		if g == policy.main && returnRequested && next.Kind == coro.ActionCheckResume {
			return false, false
		}
	case coro.ActionYield, coro.ActionPark:
		if g == policy.main && returnRequested {
			return false, false
		}
	case coro.ActionComplete:
		isMain := g == policy.main
		retireOwner := coro.ActionRetiresPhysicalOwner(next)
		if !coroReleaseCompletedTask(g) {
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
		result.stop, result.g, result.action = coroRunPanicCompleteV1, g, next
		return true, true
	case coro.ActionCommitDestroy:
		result.stop, result.g, result.action = coroRunDestroyCommitV1, g, next
		return true, true
	default:
		return false, false
	}
	if stopAfterStable {
		// The native fleet uses the same already-paid target observation that
		// controls ready distribution to expose its durable stop boundary. Return
		// only after this complete reduction so the physical owner can publish
		// sticky shutdown cancellation before any user continuation is resumed.
		result.stop = coroRunAgainV1
		return true, true
	}
	return false, true
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
	target coroRunTargetCapabilityV1,
	step coro.ExecutorRunStep,
	runtimeContext unsafe.Pointer,
	result *coroRunResultV1,
) (terminal, ok bool) {
	if p == nil || driver == nil || runtimeContext == nil || result == nil || !policy.valid() {
		return false, false
	}
	returnRequested := false
	if policy.main != nil {
		switch *policy.lifecycle {
		case coroProgramRunningV1, coroProgramMainGoexitV1:
		case coroProgramMainReturnRequestedV1:
			returnRequested = true
		default:
			return false, false
		}
	}
	switch step.Kind {
	case coro.ExecutorRunStepSource:
		distributed, targetOK := false, true
		distribute, stopAfterStable, distributionOK := coroTargetReadyDistributionV1(target)
		if !distributionOK {
			return false, false
		}
		if distribute {
			distributed, targetOK = coroTargetAfterSourceReductionV1(p, driver, step.Poll)
		}
		if !targetOK || distributed && !step.Poll.Complete ||
			step.Poll.Complete && !coro.CommitExecutorRunSourceDistribution(driver, distributed) {
			return false, false
		}
		result.used++
		result.sources++
		if stopAfterStable {
			result.stop = coroRunAgainV1
			return true, true
		}
		return false, true
	case coro.ExecutorRunStepMaterialize:
		if !coroMaterializeResumeCleanupStepV1(step.Cleanup) {
			return false, false
		}
		result.used++
		return false, true
	case coro.ExecutorRunStepDirectChannel:
		if step.Direct == nil || !coroMaterializeDirectChannelCompletionV1(step.Direct) {
			return false, false
		}
		result.used++
		return false, true
	case coro.ExecutorRunStepDispatch:
		if step.Dispatched || step.G == nil || step.Action.Handle == nil {
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
		return coroReduceExecutorRunActionPreparedV1(
			p, driver, policy, target, step.G, step.Action, step.Dispatched,
			returnRequested, runtimeContext, result,
		)
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
	runtimeContext := coroCaptureRuntimeContextV1()
	if runtimeContext == nil {
		return coroRunResultV1{}
	}
	run, runOK := coro.BeginExecutorRunSlice(driver)
	if !runOK {
		return coroRunResultV1{}
	}
	target, targetOK := coroTargetBeginRunSliceV1(p, driver)
	if !targetOK {
		return coroRunResultV1{}
	}
	result := coroRunResultV1{}
	held := false
	for result.used < budget {
		// A slice-held lease already proves that this M owns one managed-
		// execution slot. Probe/acquire only until that lease exists; calling a
		// helper merely to return the same true bit on every action was visible in
		// the handoff profile.
		if !held {
			var wait, permitOK bool
			held, wait, permitOK = coroPrepareManagedExecutionV1(driver)
			if !permitOK {
				_ = coroFinishManagedExecutionV1(driver, held)
				return coroRunResultV1{}
			}
			if wait {
				result.stop = coroRunExecutionWaitV1
				return result
			}
		}
		combineDispatch := held && budget-result.used >= 2
		var actionStep coro.ExecutorRunActionStep
		var actionSelected, nextOK bool
		if combineDispatch {
			actionStep, actionSelected, nextOK = run.NextActionCombined()
		} else {
			actionStep, actionSelected, nextOK = run.NextAction()
		}
		if !nextOK || actionSelected && !coroActionStepMatchesManagedExecutionV1(actionStep, held) {
			_ = coroFinishManagedExecutionV1(driver, held)
			return coroRunResultV1{}
		}
		var terminal, reduced bool
		if actionSelected {
			terminal, reduced = coroReduceExecutorRunActionPreparedV1(
				p, driver, coroRunPolicyV1{}, target,
				actionStep.G, actionStep.Action, actionStep.Dispatched,
				false, runtimeContext, &result,
			)
		} else {
			var step coro.ExecutorRunStep
			if combineDispatch {
				step, nextOK = run.NextAtCombined(now)
			} else {
				step, nextOK = run.NextAt(now)
			}
			if !nextOK || !coroStepMatchesManagedExecutionV1(step, held) {
				_ = coroFinishManagedExecutionV1(driver, held)
				return coroRunResultV1{}
			}
			terminal, reduced = coroReduceExecutorRunStepV1(
				p, driver, coroRunPolicyV1{}, target, step, runtimeContext, &result,
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
		if target.physicalReturn {
			stopForReturn, returnOK := coroStopAfterStableReductionV1(driver, &result)
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
	}
	if !coroFinishManagedExecutionV1(driver, held) {
		return coroRunResultV1{}
	}
	result.stop = coroRunSliceBudgetV1
	return result
}
