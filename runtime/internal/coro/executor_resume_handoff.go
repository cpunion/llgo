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

import "unsafe"

type executorResumeHandoffState uint8

const (
	executorResumeHandoffIdle executorResumeHandoffState = iota
	executorResumeHandoffDetached
	executorResumeHandoffReentering
)

// ExecutorResumeHandoffMode selects the exact reason why one active physical
// LLVM resume may release its execution domain. The mode is scheduler policy,
// not a target/thread implementation detail.
type ExecutorResumeHandoffMode uint8

const (
	ExecutorResumeHandoffInvalid ExecutorResumeHandoffMode = iota
	// ExecutorResumeHandoffLockedForeign is the existing LockOSThread-only
	// blocking call path.
	ExecutorResumeHandoffLockedForeign
	// ExecutorResumeHandoffSameMForeign admits an unlocked synchronous foreign
	// call which must execute on its physical caller M. Its C stack may also
	// call back through an exact managed adapter; callback children temporarily
	// acquire an internal physical affinity.
	ExecutorResumeHandoffSameMForeign
)

type executorResumeHandoffNoCopy struct{}

func (*executorResumeHandoffNoCopy) Lock()   {}
func (*executorResumeHandoffNoCopy) Unlock() {}

// ExecutorResumeHandoff is one physical M's private root for an active LLVM
// resume which entered a thread-affine blocking foreign call. It records only
// the scheduler fields removed by DetachExecutorResume; a compensation M
// never reads or copies it.
//
// The zero value is reusable. A record must stay at a stable address from
// DetachExecutorResume through RestoreExecutorResume. Nested compensation does
// not share a P-local slot: if a replacement M blocks in turn, that M owns a
// separate record, so the chain is bounded only by target M/thread policy.
type ExecutorResumeHandoff struct {
	noCopy executorResumeHandoffNoCopy
	driver *ExecutorDriver
	task   *G
	action Action
	budget uint32
	// inlineAwaitDepth is removed from P together with the active action. A
	// replacement owner must observe an idle P; the blocked owner restores the
	// bounded native-resume ancestry before returning from its foreign call.
	inlineAwaitDepth   uint8
	mode               ExecutorResumeHandoffMode
	state              executorResumeHandoffState
	returnablePrepared bool
}

// Detached reports whether handoff currently roots one active foreign wait.
// It is owner-M-only and is not a synchronization primitive.
func (handoff *ExecutorResumeHandoff) Detached() bool {
	return handoff != nil && handoff.state == executorResumeHandoffDetached
}

func emptyExecutorResumeHandoff(handoff *ExecutorResumeHandoff) bool {
	return handoff != nil &&
		handoff.driver == nil && handoff.task == nil &&
		handoff.action == (Action{}) && handoff.budget == 0 &&
		handoff.inlineAwaitDepth == 0 &&
		handoff.mode == ExecutorResumeHandoffInvalid &&
		handoff.state == executorResumeHandoffIdle && !handoff.returnablePrepared
}

func validForeignWaitingExecutorTask(p *P, task *G) bool {
	if p == nil || !ValidG(task) || task.state != GForeignWaiting ||
		task.runP != p || task.runAction != ActionInvalid ||
		task.transferState != runnableTransferGIdle ||
		task.isQueued() || task.nextReady != nil ||
		task.isWaiting() || task.spawnChild != nil || task.spawnParent != nil ||
		task.spawnP != nil || task.destroyTarget != nil || task.destroysRoot() ||
		task.pending.kind != pendingNone || task.pending.from != nil ||
		task.pending.target != nil || !gPreemptEnabledAtDepthZero(task) ||
		!releasableParkState(&task.park) || !validLiveTaskStorage(task) {
		return false
	}
	active := task.active
	return active != nil && active.owner == task && active.handle != nil &&
		active.header != nil && active.state == FrameActive &&
		active.header.G == unsafe.Pointer(task) &&
		active.header.SuspendReason == uint16(SuspendNone) &&
		active.header.Lifecycle == uint16(FrameActive)
}

// detachExecutorResumeCertified removes one owner-certified issued
// ActionResume from its P and driver while leaving the LLVM frame active on
// the calling M. The caller must publish an ExecutionDomainHandoff only after
// this succeeds.
//
// No coroutine transition or scheduler action is executed here. The task
// remains rooted by handoff, keeps runP as its cancellation ownership domain,
// and enters GForeignWaiting. Clearing P.osThreadLockOwner lets a replacement
// M run unrelated work on the same P without weakening the original G-to-M
// affinity.
func detachExecutorResumeCertified(
	handoff *ExecutorResumeHandoff,
	driver *ExecutorDriver,
	task *G,
	mode ExecutorResumeHandoffMode,
	compilerCertified bool,
) bool {
	p := driver.p
	locked := task.osThreadLockDepth != 0
	if !AuditEnabled && compilerCertified {
		if task.state != GRunning || !gPreemptEnabledAtDepthZero(task) ||
			driver.run.issued != ActionCheckResume || p == nil ||
			p.current != task || task.runP != p ||
			(mode == ExecutorResumeHandoffLockedForeign && !locked) ||
			p.osThreadSuspend != osThreadSuspendAttached ||
			p.action.Kind != ActionResume || p.action.Handle == nil ||
			p.servicePreemptBudget == 0 {
			return false
		}
	} else {
		if !enterCriticalContext(task) ||
			driver.run.issued != ActionCheckResume {
			return false
		}
		if p == nil || p.current != task || task.runP != p ||
			(locked && p.osThreadLockOwner != task) ||
			(!locked && p.osThreadLockOwner != nil) ||
			(mode == ExecutorResumeHandoffLockedForeign && !locked) ||
			p.osThreadSuspend != osThreadSuspendAttached ||
			task.runAction != ActionInvalid || task.isQueued() || task.nextReady != nil ||
			task.isWaiting() || task.spawnParent != nil || task.spawnP != nil ||
			task.destroyTarget != nil || task.destroysRoot() ||
			p.action.Kind != ActionResume || p.action.Handle == nil ||
			task.active == nil || !activeResumeOwnedByAction(task) ||
			p.runDecision != (RunDecision{}) || !p.runDecisionTaken ||
			p.servicePreemptBudget == 0 {
			return false
		}
	}

	handoff.driver = driver
	handoff.task = task
	handoff.action = p.action
	handoff.budget = p.servicePreemptBudget
	handoff.inlineAwaitDepth = p.inlineAwaitDepth
	handoff.mode = mode

	// The issued resume has already started the ready action which satisfied
	// any source-to-ready ordering debt. Ordinary actions clear these advisory
	// cursor bits when they commit; detachment is the only stable boundary
	// exposed in the middle of that physical action, so settle them here before
	// the replacement validates and re-enters the same driver. The action count
	// remains deferred until the restored logical resume actually commits.
	driver.run.issued = ActionInvalid
	driver.run.readyDebt = false
	driver.run.blocked = false
	p.osThreadLockOwner = nil
	p.current = nil
	p.inResume = false
	p.inlineAwaitDepth = 0
	p.action = Action{}
	p.runDecisionTaken = false
	p.servicePreemptBudget = 0
	task.state = GForeignWaiting
	handoff.state = executorResumeHandoffDetached
	return true
}

func validExecutorResumeHandoffDetachRequest(
	handoff *ExecutorResumeHandoff,
	driver *ExecutorDriver,
	task *G,
	mode ExecutorResumeHandoffMode,
) bool {
	return emptyExecutorResumeHandoff(handoff) && driver != nil && task != nil &&
		(mode == ExecutorResumeHandoffLockedForeign ||
			mode == ExecutorResumeHandoffSameMForeign)
}

// DetachExecutorResume removes one exact issued ActionResume after performing
// the complete retained/native owner audit. Targets which do not hold the
// compiler's hidden task capability must use this entry.
func DetachExecutorResume(
	handoff *ExecutorResumeHandoff,
	driver *ExecutorDriver,
	task *G,
	mode ExecutorResumeHandoffMode,
) bool {
	if !validExecutorResumeHandoffDetachRequest(handoff, driver, task, mode) {
		return false
	}
	current, _, _, ownerOK := CurrentExecutorDriver(task)
	return ownerOK && current == driver &&
		detachExecutorResumeCertified(handoff, driver, task, mode, false)
}

// DetachExecutorResumeForCompilerTask consumes the hidden task capability
// carried by generated physical coroutine code. CurrentExecutorDriverForCompilerTask
// has already frozen the exact no-suspend G/P/driver relation, so this path does
// not repeat the registry, complete source-catalog, or owner-local queue audits
// required when a target recovers a task through TLS or retained native state.
// The concrete detach transition still validates every mutable task, action,
// lock, park and preemption field which it changes.
func DetachExecutorResumeForCompilerTask(
	handoff *ExecutorResumeHandoff,
	driver *ExecutorDriver,
	task *G,
	mode ExecutorResumeHandoffMode,
) bool {
	if !validExecutorResumeHandoffDetachRequest(handoff, driver, task, mode) {
		return false
	}
	current, _, ownerOK := CurrentExecutorDriverForCompilerTask(task)
	return ownerOK && current == driver &&
		detachExecutorResumeCertified(handoff, driver, task, mode, true)
}

// ExecutorResumeHandoffReturnable reports the necessary target-neutral
// physical-owner boundary. A replacement owner may finish its
// ExecutionDomainHandoff return only here: no physical action or source
// A/ack/B transaction is split, while queued runnable work and durable source
// requests may remain for the returning M. A target must additionally settle
// its route mailbox, admission and physical-owner directory before FinishReturn.
func ExecutorResumeHandoffReturnable(driver *ExecutorDriver) bool {
	if !validExecutorDriverHeader(driver) || driver.state != executorDriverActive ||
		driver.run.issued != ActionInvalid || driver.poll.phase != executorPollIdle {
		return false
	}
	p := driver.p
	if !idleExecutorScheduler(p) || p.osThreadLockOwner != nil {
		return false
	}
	schedule := preemptLoad(&p.schedule)
	return schedule == scheduleIdle || schedule == scheduleRequested
}

// ExecutorResumeHandoffCompensationRequired classifies the exact detached
// owner boundary at which a native syscall decides whether another physical M
// must service this route. All timer, poll, manual, worker, and channel source
// operations owned by another task are rooted by the committed park queue at
// this stable boundary. The detached task itself has already proved a
// releasable park state. Task control is the sole externally posted source
// which does not require a park, so its aggregate live-endpoint count is
// checked separately. Durable facts and requests which have already arrived
// retain their independent O(1) gates below. The caller must still exclusively
// own the idle P: do not call this after publishing replacement admission.
func ExecutorResumeHandoffCompensationRequired(
	handoff *ExecutorResumeHandoff,
) (required, ok bool) {
	if handoff == nil || handoff.state != executorResumeHandoffDetached ||
		handoff.driver == nil || handoff.task == nil ||
		handoff.action.Kind != ActionResume || handoff.action.Handle == nil {
		return false, false
	}
	driver := handoff.driver
	if AuditEnabled {
		if !ExecutorResumeHandoffReturnable(driver) {
			return false, false
		}
	} else {
		p := driver.p
		if driver.state != executorDriverActive || driver.run.issued != ActionInvalid ||
			driver.poll.phase != executorPollIdle || p == nil ||
			p.current != nil || p.inResume || p.osThreadLockOwner != nil {
			return false, false
		}
		schedule := preemptLoad(&p.schedule)
		if schedule != scheduleIdle && schedule != scheduleRequested {
			return false, false
		}
	}
	p := driver.p
	control := driver.sources.control
	handoff.returnablePrepared = true
	return runnableForOSThreadOwner(p) ||
		p.externalWaitCount != 0 ||
		control != nil && control.activeCount != 0 ||
		ownerLocalCompletionPending(driver) ||
		executorRunSourceRequested(driver), true
}

// SoloSyscallEligibleTask resolves task's active executor route and verifies
// SoloSyscallEligible in one owner-only check without duplicate P/driver audits.
func SoloSyscallEligibleTask(task *G) (RouteID, bool) {
	if task == nil {
		return 0, false
	}
	p := task.runP
	if p == nil || p.current != task || !p.inResume {
		return 0, false
	}
	driver := p.executor
	if driver == nil || driver.p != p || !driver.route.Valid() ||
		driver.sources.route != driver.route {
		return 0, false
	}
	if !SoloSyscallEligible(driver, task) {
		return 0, false
	}
	return driver.route, true
}

// SoloSyscallEligible reports whether driver and task are in a clean,
// uncontended active resume with zero local runnable work, zero external
// waits, zero active timers, zero control operations, zero owner-local or
// direct-channel completions, and zero pending scheduler or executor requests.
// Combined with an execution-quota solo-holder check, zero detached foreign
// calls, and quiescent peer routes, a synchronous llgo.syscall may execute
// directly on the calling M without detaching or arming a deferred replacement.
func SoloSyscallEligible(driver *ExecutorDriver, task *G) bool {
	if driver == nil || task == nil || driver.state != executorDriverActive ||
		driver.run.issued != ActionCheckResume || driver.poll.phase != executorPollIdle {
		return false
	}
	p := driver.p
	if p == nil || p.current != task || !p.inResume || p.readyCount != 0 ||
		p.externalWaitCount != 0 || p.affectedWaitHead != nil ||
		p.osThreadLockOwner != nil || p.osThreadSuspend != osThreadSuspendAttached {
		return false
	}
	if timers := driver.sources.timers; timers != nil && timers.activeCount != 0 {
		return false
	}
	if control := driver.sources.control; control != nil && control.activeCount != 0 {
		return false
	}
	if ownerLocalCompletionPending(driver) ||
		!executorDirectChannelInboxIdle(driver) ||
		driver.run.sourceMore ||
		executorRunExternalSourceRequestedNoDirect(driver) {
		return false
	}
	return true
}

// DriverQuiescentForSleep reports whether a sleeping executor driver holds
// zero ready tasks, zero external wake obligations, zero active timers, zero
// active task-control endpoints, zero pending completions, and an unrequested
// idle gate. Pure logical channel waiters leave externalWaitCount zero because
// only an executing Go producer can wake them.
func DriverQuiescentForSleep(driver *ExecutorDriver) bool {
	if driver == nil || driver.state != executorDriverSleeping || driver.p == nil {
		return false
	}
	p := driver.p
	if p.readyCount != 0 || p.externalWaitCount != 0 || p.affectedWaitHead != nil {
		return false
	}
	if timers := driver.sources.timers; timers != nil && timers.activeCount != 0 {
		return false
	}
	if control := driver.sources.control; control != nil && control.activeCount != 0 {
		return false
	}
	if ownerLocalCompletionPending(driver) || !executorDirectChannelInboxIdle(driver) {
		return false
	}
	return driver.registry != nil && driver.registry.ObserveIdleUnrequested(driver.handle)
}

// DriverIdleUnrequested observes the atomic executor gate for driver without
// inspecting owner-only scheduler fields.
func DriverIdleUnrequested(driver *ExecutorDriver) bool {
	if driver == nil {
		return false
	}
	if !AuditEnabled {
		return driver.requestGate != nil && preemptLoad(driver.requestGate) == executorGateIdleArmed
	}
	return driver.registry != nil &&
		driver.registry.ObserveIdleUnrequested(driver.handle)
}

// ExecutorResumeHandoffRequestPending closes the request-before-admission
// window after the owner has sampled CompensationRequired and published a
// replacement gate. A replacement may already be running on the P. Only the
// parent-owned handoff, immutable binding pointers/handle, and atomic request
// words may be read here; no P queue, driver cursor, source catalog, or task
// state is inspected. The detached handoff keeps the binding alive until the
// replacement is strongly joined and the original resume is restored.
func ExecutorResumeHandoffRequestPending(
	handoff *ExecutorResumeHandoff,
) (pending, ok bool) {
	if handoff == nil || handoff.state != executorResumeHandoffDetached ||
		handoff.driver == nil || handoff.task == nil ||
		handoff.action.Kind != ActionResume || handoff.action.Handle == nil {
		return false, false
	}
	driver := handoff.driver
	if driver.p == nil || driver.registry == nil ||
		driver.handle.Slot == 0 || driver.handle.Slot > ExecutorRequestCapacity ||
		driver.handle.Generation == 0 {
		return false, false
	}
	schedule := preemptLoad(&driver.p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return false, false
	}
	return driver.registry.ObserveRequested(driver.handle) ||
		schedule == scheduleRequested, true
}

// ExecutorResumeHandoffContext returns the exact logical task and physical
// parent handle for a detached same-M boundary which actually reentered
// through a managed callback. It exposes no target function identity and
// performs no address lookup; a compiler-generated callback adapter uses these
// values only to construct its child ramp.
func ExecutorResumeHandoffContext(
	handoff *ExecutorResumeHandoff,
) (task *G, parent unsafe.Pointer, ok bool) {
	if handoff == nil || handoff.state != executorResumeHandoffDetached ||
		handoff.mode != ExecutorResumeHandoffSameMForeign ||
		handoff.driver == nil || handoff.task == nil ||
		handoff.action.Kind != ActionResume || handoff.action.Handle == nil ||
		handoff.inlineAwaitDepth > maxInlineAwaitDepth ||
		!ExecutorResumeHandoffReturnable(handoff.driver) ||
		!validForeignWaitingExecutorTask(handoff.driver.p, handoff.task) ||
		!resumeActionOwnsActive(handoff.task, handoff.action, handoff.inlineAwaitDepth) {
		return nil, nil, false
	}
	return handoff.task, handoff.task.active.handle, true
}

// RestoreExecutorResume reattaches the exact active LLVM resume after the
// replacement owner has finished and the target has strongly joined it. The
// caller must reacquire its managed-execution P lease before calling Restore.
// Success consumes and zeroes handoff; a duplicate or mismatched restore is
// rejected without changing scheduler state.
func RestoreExecutorResume(handoff *ExecutorResumeHandoff) bool {
	return restoreExecutorResume(handoff, false)
}

// RestoreExecutorResumeAfterWithdraw restores a native syscall whose owner
// successfully withdrew the deferred dispatch and reacquired its P lease.
// The caller must have excluded both replacement execution and reentry for
// this exact handoff. Preparation already checked the idle scheduler, and
// detachment checked the inline ancestry. Sticky requests can arrive in the
// meantime, so their atomic state is still checked and left intact.
func RestoreExecutorResumeAfterWithdraw(handoff *ExecutorResumeHandoff) bool {
	return restoreExecutorResume(handoff, true)
}

func restoreExecutorResume(handoff *ExecutorResumeHandoff, withdrawn bool) bool {
	if handoff == nil || handoff.state != executorResumeHandoffDetached ||
		handoff.driver == nil || handoff.task == nil ||
		handoff.action.Kind != ActionResume || handoff.action.Handle == nil ||
		handoff.budget == 0 || handoff.inlineAwaitDepth > maxInlineAwaitDepth ||
		(handoff.mode != ExecutorResumeHandoffLockedForeign &&
			handoff.mode != ExecutorResumeHandoffSameMForeign) {
		return false
	}
	driver, task := handoff.driver, handoff.task
	p := driver.p
	if withdrawn {
		if !handoff.returnablePrepared || handoff.mode != ExecutorResumeHandoffSameMForeign ||
			p == nil || p.executor != driver || driver.state != executorDriverActive ||
			driver.poll.phase != executorPollIdle || p.current != nil || p.inResume ||
			p.osThreadLockOwner != nil {
			return false
		}
		schedule := preemptLoad(&p.schedule)
		if schedule != scheduleIdle && schedule != scheduleRequested {
			return false
		}
		if AuditEnabled {
			if !validForeignWaitingExecutorTask(p, task) {
				return false
			}
		} else if task.state != GForeignWaiting || task.runP != p || task.active == nil {
			return false
		}
		if (p.foreignReentry != nil && p.foreignReentry.handoff == handoff) ||
			task.runP != p || driver.run.issued != ActionInvalid {
			return false
		}
	} else {
		if !ExecutorResumeHandoffReturnable(driver) ||
			!validForeignWaitingExecutorTask(p, task) ||
			(handoff.mode == ExecutorResumeHandoffLockedForeign &&
				task.osThreadLockDepth == 0) ||
			(p.foreignReentry != nil && p.foreignReentry.handoff == handoff) ||
			!resumeActionOwnsActive(task, handoff.action, handoff.inlineAwaitDepth) ||
			task.runP != p || driver.run.issued != ActionInvalid {
			return false
		}
	}

	p.current = task
	p.inResume = true
	p.inlineAwaitDepth = handoff.inlineAwaitDepth
	p.action = handoff.action
	p.runDecision = RunDecision{}
	p.runDecisionTaken = true
	p.servicePreemptBudget = handoff.budget
	if task.osThreadLockDepth != 0 {
		p.osThreadLockOwner = task
	} else {
		p.osThreadLockOwner = nil
	}
	p.osThreadSuspend = osThreadSuspendAttached
	driver.run.issued = ActionCheckResume
	task.state = GRunning
	handoff.driver = nil
	handoff.task = nil
	handoff.action = Action{}
	handoff.budget = 0
	handoff.inlineAwaitDepth = 0
	handoff.mode = ExecutorResumeHandoffInvalid
	handoff.state = executorResumeHandoffIdle
	handoff.returnablePrepared = false
	return true
}
