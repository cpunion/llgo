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
	inlineAwaitDepth uint8
	mode             ExecutorResumeHandoffMode
	state            executorResumeHandoffState
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
		handoff.state == executorResumeHandoffIdle
}

func validForeignWaitingExecutorTask(p *P, task *G) bool {
	if p == nil || !ValidG(task) || task.state != GForeignWaiting ||
		task.runP != p || task.runAction != ActionInvalid ||
		task.transferState != runnableTransferGIdle ||
		task.queued || task.nextReady != nil ||
		task.waiting || task.spawnChild != nil || task.spawnParent != nil ||
		task.spawnP != nil || task.destroyTarget != nil || task.destroyRoot ||
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

// DetachExecutorResume removes one exact issued ActionResume from its P and
// driver while leaving the LLVM frame active on the calling locked M. The
// caller must publish an ExecutionDomainHandoff only after this succeeds.
//
// No coroutine transition or scheduler action is executed here. The task
// remains rooted by handoff, keeps runP as its cancellation ownership domain,
// and enters GForeignWaiting. Clearing P.osThreadLockOwner lets a replacement
// M run unrelated work on the same P without weakening the original G-to-M
// affinity.
func DetachExecutorResume(
	handoff *ExecutorResumeHandoff,
	driver *ExecutorDriver,
	task *G,
	mode ExecutorResumeHandoffMode,
) bool {
	if !emptyExecutorResumeHandoff(handoff) || driver == nil || task == nil ||
		(mode != ExecutorResumeHandoffLockedForeign &&
			mode != ExecutorResumeHandoffSameMForeign) {
		return false
	}
	current, _, _, ownerOK := CurrentExecutorDriver(task)
	if !ownerOK || current != driver || !enterCriticalContext(task) ||
		driver.run.issued != ActionCheckResume {
		return false
	}
	p := driver.p
	locked := task.osThreadLockDepth != 0
	if p == nil || p.current != task || task.runP != p ||
		(locked && p.osThreadLockOwner != task) ||
		(!locked && p.osThreadLockOwner != nil) ||
		(mode == ExecutorResumeHandoffLockedForeign && !locked) ||
		p.osThreadSuspend != osThreadSuspendAttached ||
		task.runAction != ActionInvalid || task.queued || task.nextReady != nil ||
		task.waiting || task.spawnParent != nil || task.spawnP != nil ||
		task.destroyTarget != nil || task.destroyRoot ||
		p.action.Kind != ActionResume || p.action.Handle == nil ||
		task.active == nil || !activeResumeOwnedByAction(task) ||
		p.runDecision != (RunDecision{}) || !p.runDecisionTaken ||
		p.servicePreemptBudget == 0 {
		return false
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
	if !ExecutorResumeHandoffReturnable(driver) ||
		!validForeignWaitingExecutorTask(p, task) ||
		(handoff.mode == ExecutorResumeHandoffLockedForeign &&
			task.osThreadLockDepth == 0) ||
		(p.foreignReentry != nil && p.foreignReentry.handoff == handoff) ||
		!resumeActionOwnsActive(task, handoff.action, handoff.inlineAwaitDepth) ||
		task.runP != p || driver.run.issued != ActionInvalid {
		return false
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
	return true
}
