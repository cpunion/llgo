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

type foreignReentryState uint8

const (
	foreignReentryIdle foreignReentryState = iota
	foreignReentryRunning
	foreignReentryDestroyCommitted
	foreignReentryCompleted
)

type foreignReentryNoCopy struct{}

func (*foreignReentryNoCopy) Lock()   {}
func (*foreignReentryNoCopy) Unlock() {}

// ForeignReentryRecord is stable owner-M stack storage for one synchronous
// C-to-Go callback. It roots no target function and contains no reverse code
// address mapping: compiler lowering passes the exact callback child handle.
//
// A nested callback-capable C call owns another record and links it through
// previous. P retains only the top while the callback child participates in
// the ordinary scheduler. The zero value is reusable after
// ConsumeForeignReentryCompletion.
type ForeignReentryRecord struct {
	noCopy     foreignReentryNoCopy
	handoff    *ExecutorResumeHandoff
	p          *P
	task       *G
	parent     *Frame
	child      unsafe.Pointer
	previous   *ForeignReentryRecord
	completion CompletionSnapshot
	state      foreignReentryState
}

func emptyForeignReentryRecord(record *ForeignReentryRecord) bool {
	return record != nil &&
		record.handoff == nil && record.p == nil && record.task == nil &&
		record.parent == nil && record.child == nil && record.previous == nil &&
		record.completion == (CompletionSnapshot{}) &&
		record.state == foreignReentryIdle
}

func validCompletionSnapshot(snapshot CompletionSnapshot) bool {
	switch snapshot.Status {
	case CompletionReturn, CompletionReturnRecovered, CompletionAbort,
		CompletionShutdown, CompletionGoexit:
		return snapshot.TypeWord == nil && snapshot.DataWord == nil
	case CompletionPanic:
		return snapshot.TypeWord != nil
	default:
		return false
	}
}

func completionSnapshot(record *CompletionRecord) (CompletionSnapshot, bool) {
	if record == nil || record.child == nil {
		return CompletionSnapshot{}, false
	}
	snapshot := CompletionSnapshot{
		Status:   record.status,
		TypeWord: record.typeWord,
		DataWord: record.dataWord,
	}
	return snapshot, validCompletionSnapshot(snapshot)
}

func validRunningForeignReentryRecord(record *ForeignReentryRecord, p *P, task *G) bool {
	if record == nil || record.state != foreignReentryRunning ||
		record.handoff == nil || record.handoff.state != executorResumeHandoffReentering ||
		record.handoff.mode != ExecutorResumeHandoffManagedReentry ||
		record.handoff.driver == nil || record.handoff.driver.p != p ||
		record.handoff.task != task || record.p != p || record.task != task ||
		record.parent == nil || record.child == nil ||
		record.parent.owner != task || record.parent.handle == nil ||
		record.parent.header == nil || record.parent.state != FrameSuspended ||
		record.parent.header.G != unsafe.Pointer(task) ||
		record.parent.header.SuspendReason != uint16(SuspendCall) ||
		record.parent.header.Lifecycle != uint16(FrameSuspended) {
		return false
	}
	child := findFrame(task, record.child)
	return child != nil && child == task.active && child.parent == record.parent &&
		child.header != nil && child.header.Parent == record.parent.handle &&
		record.parent.completion.child == record.child
}

// BeginForeignReentry attaches one already initial-suspended compiler-generated
// callback child to the active physical parent below a synchronous native C
// stack. The parent frame is marked logically suspended, but is not resumed or
// destroyed: the child alone enters the ordinary scheduler.
func BeginForeignReentry(
	record *ForeignReentryRecord,
	handoff *ExecutorResumeHandoff,
	childHandle unsafe.Pointer,
) bool {
	if !emptyForeignReentryRecord(record) || handoff == nil ||
		handoff.state != executorResumeHandoffDetached ||
		handoff.mode != ExecutorResumeHandoffManagedReentry ||
		handoff.driver == nil || handoff.task == nil || childHandle == nil {
		return false
	}
	driver, task := handoff.driver, handoff.task
	p := driver.p
	if !ExecutorResumeHandoffReturnable(driver) ||
		!validForeignWaitingExecutorTask(p, task) ||
		task.active.handle != handoff.action.Handle ||
		p.readyCount == ^uint32(0) {
		return false
	}
	previous := p.foreignReentry
	if previous == nil {
		if osThreadForeignReentryAffined(task) {
			return false
		}
	} else if !validRunningForeignReentryRecord(previous, p, task) ||
		!osThreadForeignReentryAffined(task) {
		return false
	}
	parent := task.active
	child := findFrame(task, childHandle)
	if parent == nil || parent.owner != task || parent.handle == nil ||
		parent.header == nil || parent.state != FrameActive ||
		parent.header.G != unsafe.Pointer(task) ||
		parent.header.SuspendReason != uint16(SuspendNone) ||
		parent.header.Lifecycle != uint16(FrameActive) ||
		!emptyCompletionRecord(&parent.completion) ||
		child == nil || child == parent || child.owner != task ||
		child.header == nil || child.state != FrameInitialSuspended ||
		child.header.G != unsafe.Pointer(task) ||
		child.header.Parent != parent.handle || child.parent != nil ||
		!validReadyQueueHeader(p) || !validSchedulerWaitQueues(p) ||
		p.current != nil || p.inResume || p.action != (Action{}) ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken ||
		p.servicePreemptBudget != 0 ||
		p.osThreadSuspend != osThreadSuspendAttached ||
		p.osThreadLockOwner != nil {
		return false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return false
	}
	if !armAwaitCompletion(parent, child, nil, nil) {
		return false
	}
	if previous == nil {
		if !enterOSThreadForeignReentryAffinity(p, task) {
			parent.completion = CompletionRecord{}
			return false
		}
	} else {
		p.osThreadLockOwner = task
	}

	parent.state = FrameSuspended
	parent.header.SuspendReason = uint16(SuspendCall)
	parent.header.Lifecycle = uint16(FrameSuspended)
	child.parent = parent
	task.active = child
	task.state = GRunnable
	task.runP = nil
	appendReadyUnchecked(p, task)

	record.handoff = handoff
	record.p = p
	record.task = task
	record.parent = parent
	record.child = childHandle
	record.previous = previous
	record.state = foreignReentryRunning
	p.foreignReentry = record
	handoff.state = executorResumeHandoffReentering
	return true
}

func foreignReentryDestroyedBounded(p *P, task *G) (Action, bool) {
	record := p.foreignReentry
	if !validRunningForeignReentryRecordAfterDestroy(record, p, task) {
		return Action{}, false
	}
	record.state = foreignReentryDestroyCommitted
	receipt := Action{Kind: ActionForeignReentryComplete}
	p.action = receipt
	return receipt, true
}

func validRunningForeignReentryRecordAfterDestroy(
	record *ForeignReentryRecord,
	p *P,
	task *G,
) bool {
	if record == nil || record.state != foreignReentryRunning ||
		record.handoff == nil || record.handoff.state != executorResumeHandoffReentering ||
		record.handoff.mode != ExecutorResumeHandoffManagedReentry ||
		record.handoff.driver == nil || record.handoff.driver.p != p ||
		record.handoff.task != task || record.p != p || record.task != task ||
		record.parent == nil || record.parent != task.active ||
		record.parent.owner != task || record.parent.handle == nil ||
		record.parent.header == nil || record.parent.state != FrameSuspended ||
		record.parent.header.G != unsafe.Pointer(task) ||
		record.parent.header.SuspendReason != uint16(SuspendCall) ||
		record.parent.header.Lifecycle != uint16(FrameSuspended) ||
		record.child == nil || findFrame(task, record.child) != nil ||
		record.parent.completion.child != record.child ||
		task.destroyRoot || task.destroyTarget != nil ||
		!osThreadForeignReentryAffined(task) ||
		p.osThreadLockOwner != task ||
		p.osThreadSuspend != osThreadSuspendAttached {
		return false
	}
	_, ok := completionSnapshot(&record.parent.completion)
	return ok
}

func commitForeignReentryCompletion(
	driver *ExecutorDriver,
	task *G,
	receipt Action,
) bool {
	if driver == nil || task == nil ||
		receipt != (Action{Kind: ActionForeignReentryComplete}) ||
		driver.run.issued != ActionCheckDestroy {
		return false
	}
	p := driver.p
	record := p.foreignReentry
	if record == nil || record.state != foreignReentryDestroyCommitted ||
		record.handoff == nil || record.handoff.state != executorResumeHandoffReentering ||
		record.handoff.driver != driver || record.handoff.task != task ||
		record.p != p || record.task != task || p.current != task ||
		p.action != receipt || p.inResume ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken ||
		p.servicePreemptBudget == 0 || task.runP != p ||
		task.state != GDispatching || task.runAction != ActionInvalid ||
		task.active != record.parent || task.pending != (pendingTransition{}) ||
		task.destroyTarget != nil || task.destroyRoot ||
		task.queued || task.nextReady != nil || task.waiting ||
		task.spawnChild != nil || task.spawnParent != nil || task.spawnP != nil ||
		!osThreadForeignReentryAffined(task) ||
		p.osThreadLockOwner != task ||
		p.osThreadSuspend != osThreadSuspendAttached {
		return false
	}
	snapshot, ok := completionSnapshot(&record.parent.completion)
	if !ok || record.parent.completion.child != record.child ||
		findFrame(task, record.child) != nil {
		return false
	}

	record.completion = snapshot
	record.parent.completion = CompletionRecord{}
	record.parent.state = FrameActive
	record.parent.header.SuspendReason = uint16(SuspendNone)
	record.parent.header.Lifecycle = uint16(FrameActive)
	task.state = GForeignWaiting
	p.current = nil
	p.inResume = false
	p.action = Action{}
	p.runDecision = RunDecision{}
	p.runDecisionTaken = false
	p.servicePreemptBudget = 0
	p.osThreadLockOwner = nil
	record.handoff.state = executorResumeHandoffDetached
	record.state = foreignReentryCompleted
	return true
}

// ConsumeForeignReentryCompletion pops one completed synchronous callback and
// returns its Go outcome to the exact native adapter. No physical parent resume
// is issued: returning from the adapter continues the already-active C stack.
func ConsumeForeignReentryCompletion(
	record *ForeignReentryRecord,
) (CompletionSnapshot, bool) {
	if record == nil || record.state != foreignReentryCompleted ||
		record.handoff == nil || record.handoff.state != executorResumeHandoffDetached ||
		record.handoff.mode != ExecutorResumeHandoffManagedReentry ||
		record.p == nil || record.task == nil || record.parent == nil ||
		record.child == nil || !validCompletionSnapshot(record.completion) {
		return CompletionSnapshot{}, false
	}
	p, task := record.p, record.task
	if p.foreignReentry != record ||
		!ExecutorResumeHandoffReturnable(record.handoff.driver) ||
		!validForeignWaitingExecutorTask(p, task) ||
		task.active != record.parent ||
		!emptyCompletionRecord(&record.parent.completion) ||
		findFrame(task, record.child) != nil ||
		!osThreadForeignReentryAffined(task) ||
		p.osThreadLockOwner != nil {
		return CompletionSnapshot{}, false
	}
	if record.previous == nil {
		if !exitOSThreadForeignReentryAffinity(p, task) {
			return CompletionSnapshot{}, false
		}
	} else if !validRunningForeignReentryRecord(record.previous, p, task) {
		return CompletionSnapshot{}, false
	}

	snapshot := record.completion
	p.foreignReentry = record.previous
	*record = ForeignReentryRecord{}
	return snapshot, true
}
