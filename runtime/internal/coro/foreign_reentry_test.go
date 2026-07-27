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

import (
	"runtime"
	"testing"
	"unsafe"
)

type foreignReentryFixture struct {
	p       *P
	driver  *ExecutorDriver
	task    *yieldingTestG
	parent  Action
	handoff ExecutorResumeHandoff
	record  ForeignReentryRecord
	child   *testFrame
}

func newForeignReentryFixture(t *testing.T, userLocked bool) *foreignReentryFixture {
	t.Helper()
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "foreign-reentry")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue foreign-reentry parent")
	}
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	parent, ok := Checked(p, task.g, step.Action, false)
	if !ok || parent.Kind != ActionResume || parent.Handle != task.handle {
		t.Fatalf("activate foreign-reentry parent = (%+v, %t)", parent, ok)
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	if userLocked && !EnterOSThreadLock(task.g) {
		t.Fatal("lock foreign-reentry parent")
	}

	fixture := &foreignReentryFixture{
		p:      p,
		driver: driver,
		task:   task,
		parent: parent,
	}
	if !DetachExecutorResume(
		&fixture.handoff,
		driver,
		task.g,
		ExecutorResumeHandoffSameMForeign,
	) {
		t.Fatal("detach managed foreign-reentry parent")
	}
	childHandle := unsafe.Pointer(new(byte))
	fixture.child = newTestFrame(t, task.g, childHandle, task.handle)
	if !BeginForeignReentry(&fixture.record, &fixture.handoff, childHandle) {
		t.Fatal("begin managed foreign reentry")
	}
	return fixture
}

func activateForeignReentryChild(
	t *testing.T,
	p *P,
	driver *ExecutorDriver,
	task *yieldingTestG,
	child *testFrame,
) Action {
	t.Helper()
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	if step.Action.Handle != child.handle {
		t.Fatalf("foreign-reentry child dispatch handle = %p, want %p", step.Action.Handle, child.handle)
	}
	resume, ok := Checked(p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume || resume.Handle != child.handle {
		t.Fatalf("activate foreign-reentry child = (%+v, %t)", resume, ok)
	}
	takeNormalRunnerDecision(t, task.g)
	child.header.SuspendReason = uint16(SuspendNone)
	child.header.Lifecycle = uint16(FrameActive)
	return resume
}

func finishForeignReentryChild(
	t *testing.T,
	p *P,
	driver *ExecutorDriver,
	task *yieldingTestG,
	child *testFrame,
	resume Action,
	record *ForeignReentryRecord,
	status CompletionStatus,
	typeWord, dataWord unsafe.Pointer,
) CompletionSnapshot {
	t.Helper()
	switch status {
	case CompletionReturn:
		child.header.SuspendReason = uint16(SuspendFrameComplete)
		child.header.Lifecycle = uint16(FrameFinalSuspended)
		if !PrepareComplete(task.g, child.handle, child.header) {
			t.Fatal("publish foreign-reentry child return")
		}
	case CompletionPanic:
		child.header.SuspendReason = uint16(SuspendPanic)
		child.header.Lifecycle = uint16(FrameFinalSuspended)
		if !PreparePanic(task.g, child.handle, child.header, typeWord, dataWord) {
			t.Fatal("publish foreign-reentry child panic")
		}
	default:
		t.Fatalf("unsupported foreign-reentry test status %d", status)
	}
	next, ok := Resumed(p, task.g, resume)
	if !ok || next.Kind != ActionCheckDestroy ||
		!CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("queue foreign-reentry child destroy = (%+v, %t)", next, ok)
	}

	step := runnerNextPhysicalAction(t, driver, task, ActionCheckDestroy)
	destroy, ok := Checked(p, task.g, step.Action, true)
	if !ok || destroy.Kind != ActionDestroy || destroy.Handle != child.handle {
		t.Fatalf("activate foreign-reentry child destroy = (%+v, %t)", destroy, ok)
	}
	releaseTestFrame(t, task.g, child)
	receipt, ok := DestroyedBounded(p, task.g, destroy)
	if !ok || receipt != (Action{Kind: ActionForeignReentryComplete}) ||
		!CommitExecutorRunAction(driver, task.g, receipt) {
		t.Fatalf("commit foreign-reentry completion = (%+v, %t)", receipt, ok)
	}
	snapshot, ok := ConsumeForeignReentryCompletion(record)
	if !ok {
		t.Fatal("consume foreign-reentry completion")
	}
	return snapshot
}

func (fixture *foreignReentryFixture) completeReturn(
	t *testing.T,
	during func(),
) CompletionSnapshot {
	t.Helper()
	resume := activateForeignReentryChild(
		t,
		fixture.p,
		fixture.driver,
		fixture.task,
		fixture.child,
	)
	if during != nil {
		during()
	}
	return finishForeignReentryChild(
		t,
		fixture.p,
		fixture.driver,
		fixture.task,
		fixture.child,
		resume,
		&fixture.record,
		CompletionReturn,
		nil,
		nil,
	)
}

func (fixture *foreignReentryFixture) restore(t *testing.T) {
	t.Helper()
	if !RestoreExecutorResume(&fixture.handoff) {
		t.Fatal("restore foreign-reentry parent")
	}
}

func (fixture *foreignReentryFixture) keepAlive() {
	runtime.KeepAlive(fixture.task.frame.memory)
	runtime.KeepAlive(fixture.child.memory)
}

func TestForeignReentryRunsChildWithoutResumingPhysicalParent(t *testing.T) {
	fixture := newForeignReentryFixture(t, false)
	if fixture.task.g.osThreadLockDepth != osThreadForeignReentryBit ||
		fixture.p.osThreadLockOwner != fixture.task.g ||
		fixture.p.foreignReentry != &fixture.record ||
		fixture.handoff.state != executorResumeHandoffReentering {
		t.Fatalf(
			"begun foreign reentry = depth:%#x owner:%p record:%p handoff:%d",
			fixture.task.g.osThreadLockDepth,
			fixture.p.osThreadLockOwner,
			fixture.p.foreignReentry,
			fixture.handoff.state,
		)
	}
	if RestoreExecutorResume(&fixture.handoff) {
		t.Fatal("restored physical parent while callback child was active")
	}

	snapshot := fixture.completeReturn(t, func() {
		if !ExitOSThreadLock(fixture.task.g) ||
			fixture.task.g.osThreadLockDepth != osThreadForeignReentryBit {
			t.Fatal("unmatched UnlockOSThread released internal reentry affinity")
		}
	})
	if snapshot != (CompletionSnapshot{Status: CompletionReturn}) ||
		fixture.p.foreignReentry != nil ||
		fixture.task.g.osThreadLockDepth != 0 ||
		fixture.task.g.state != GForeignWaiting ||
		fixture.task.g.active != FrameFromStorage(fixture.task.frame.storage) ||
		fixture.handoff.state != executorResumeHandoffDetached {
		t.Fatalf(
			"completed foreign reentry = snapshot:%+v record:%p depth:%#x state:%d active:%p handoff:%d",
			snapshot,
			fixture.p.foreignReentry,
			fixture.task.g.osThreadLockDepth,
			fixture.task.g.state,
			fixture.task.g.active,
			fixture.handoff.state,
		)
	}
	fixture.restore(t)
	if fixture.p.osThreadLockOwner != nil {
		t.Fatal("unlocked parent restored with an OS-thread owner")
	}
	fixture.keepAlive()
}

func TestForeignReentryPreservesUserLockChanges(t *testing.T) {
	fixture := newForeignReentryFixture(t, true)
	if got := fixture.task.g.osThreadLockDepth; got != osThreadForeignReentryBit|1 {
		t.Fatalf("initial reentry/user lock depth = %#x", got)
	}
	if snapshot := fixture.completeReturn(t, func() {
		if !EnterOSThreadLock(fixture.task.g) ||
			fixture.task.g.osThreadLockDepth != osThreadForeignReentryBit|2 ||
			!ExitOSThreadLock(fixture.task.g) ||
			fixture.task.g.osThreadLockDepth != osThreadForeignReentryBit|1 {
			t.Fatal("callback user lock nesting did not preserve internal affinity")
		}
	}); snapshot.Status != CompletionReturn {
		t.Fatalf("locked callback completion = %+v", snapshot)
	}
	if got := fixture.task.g.osThreadLockDepth; got != 1 {
		t.Fatalf("completed callback lost user lock depth: %#x", got)
	}
	fixture.restore(t)
	if fixture.p.osThreadLockOwner != fixture.task.g ||
		!CurrentOSThreadLocked(fixture.task.g) {
		t.Fatal("managed parent did not restore the callback's user lock")
	}
	fixture.keepAlive()
}

func TestForeignReentryOrdinaryNestedChildResumesCallback(t *testing.T) {
	fixture := newForeignReentryFixture(t, false)
	callbackResume := activateForeignReentryChild(
		t,
		fixture.p,
		fixture.driver,
		fixture.task,
		fixture.child,
	)

	nestedHandle := unsafe.Pointer(new(byte))
	nested := newTestFrame(t, fixture.task.g, nestedHandle, fixture.child.handle)
	fixture.child.header.SuspendReason = uint16(SuspendCall)
	fixture.child.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareAwaitCompletion(fixture.task.g, fixture.child.handle, nestedHandle) {
		t.Fatal("prepare ordinary nested child under foreign callback")
	}
	next, ok := Resumed(fixture.p, fixture.task.g, callbackResume)
	if !ok || next.Kind != ActionCheckResume || next.Handle != nestedHandle ||
		!CommitExecutorRunAction(fixture.driver, fixture.task.g, next) {
		t.Fatalf("queue ordinary nested child = (%+v, %t)", next, ok)
	}

	nestedResume := activateForeignReentryChild(
		t,
		fixture.p,
		fixture.driver,
		fixture.task,
		nested,
	)
	nested.header.SuspendReason = uint16(SuspendFrameComplete)
	nested.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(fixture.task.g, nestedHandle, nested.header) {
		t.Fatal("publish ordinary nested child return")
	}
	next, ok = Resumed(fixture.p, fixture.task.g, nestedResume)
	if !ok || next.Kind != ActionCheckDestroy || next.Handle != nestedHandle ||
		!CommitExecutorRunAction(fixture.driver, fixture.task.g, next) {
		t.Fatalf("queue ordinary nested child destroy = (%+v, %t)", next, ok)
	}
	step := runnerNextPhysicalAction(
		t,
		fixture.driver,
		fixture.task,
		ActionCheckDestroy,
	)
	destroy, ok := Checked(fixture.p, fixture.task.g, step.Action, true)
	if !ok || destroy.Kind != ActionDestroy || destroy.Handle != nestedHandle {
		t.Fatalf("activate ordinary nested child destroy = (%+v, %t)", destroy, ok)
	}
	releaseTestFrame(t, fixture.task.g, nested)
	next, ok = DestroyedBounded(fixture.p, fixture.task.g, destroy)
	if !ok || next.Kind != ActionCheckResume ||
		next.Handle != fixture.child.handle ||
		!CommitExecutorRunAction(fixture.driver, fixture.task.g, next) {
		t.Fatalf(
			"ordinary nested child was confused with the callback receipt = (%+v, %t)",
			next,
			ok,
		)
	}

	step = runnerNextPhysicalAction(
		t,
		fixture.driver,
		fixture.task,
		ActionCheckResume,
	)
	callbackResume, ok = Checked(fixture.p, fixture.task.g, step.Action, false)
	if !ok || callbackResume.Kind != ActionResume ||
		callbackResume.Handle != fixture.child.handle {
		t.Fatalf("resume callback after ordinary child = (%+v, %t)", callbackResume, ok)
	}
	takeNormalRunnerDecision(t, fixture.task.g)
	fixture.child.header.SuspendReason = uint16(SuspendNone)
	fixture.child.header.Lifecycle = uint16(FrameActive)
	if snapshot, consumed := ConsumeAwaitCompletion(
		fixture.task.g,
		fixture.child.handle,
	); !consumed || snapshot.Status != CompletionReturn {
		t.Fatalf("ordinary nested child completion = (%+v, %t)", snapshot, consumed)
	}

	snapshot := finishForeignReentryChild(
		t,
		fixture.p,
		fixture.driver,
		fixture.task,
		fixture.child,
		callbackResume,
		&fixture.record,
		CompletionReturn,
		nil,
		nil,
	)
	if snapshot.Status != CompletionReturn {
		t.Fatalf("foreign callback completion after ordinary child = %+v", snapshot)
	}
	fixture.restore(t)
	runtime.KeepAlive(nested.memory)
	fixture.keepAlive()
}

func TestForeignReentryNestingPreservesExactParentAndPanicOutcome(t *testing.T) {
	outer := newForeignReentryFixture(t, false)
	outerResume := activateForeignReentryChild(
		t,
		outer.p,
		outer.driver,
		outer.task,
		outer.child,
	)

	var innerHandoff ExecutorResumeHandoff
	if !DetachExecutorResume(
		&innerHandoff,
		outer.driver,
		outer.task.g,
		ExecutorResumeHandoffSameMForeign,
	) {
		t.Fatal("detach nested foreign-reentry parent")
	}
	innerHandle := unsafe.Pointer(new(byte))
	innerChild := newTestFrame(t, outer.task.g, innerHandle, outer.child.handle)
	var innerRecord ForeignReentryRecord
	if !BeginForeignReentry(&innerRecord, &innerHandoff, innerHandle) {
		t.Fatal("begin nested foreign reentry")
	}
	if innerRecord.previous != &outer.record ||
		outer.p.foreignReentry != &innerRecord {
		t.Fatal("nested foreign reentry did not push the owner stack")
	}

	innerResume := activateForeignReentryChild(
		t,
		outer.p,
		outer.driver,
		outer.task,
		innerChild,
	)
	typeWord, dataWord := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	innerSnapshot := finishForeignReentryChild(
		t,
		outer.p,
		outer.driver,
		outer.task,
		innerChild,
		innerResume,
		&innerRecord,
		CompletionPanic,
		typeWord,
		dataWord,
	)
	if innerSnapshot != (CompletionSnapshot{
		Status: CompletionPanic, TypeWord: typeWord, DataWord: dataWord,
	}) ||
		outer.p.foreignReentry != &outer.record ||
		outer.task.g.active != FrameFromStorage(outer.child.storage) ||
		outer.task.g.osThreadLockDepth != osThreadForeignReentryBit {
		t.Fatalf(
			"nested completion = snapshot:%+v top:%p active:%p depth:%#x",
			innerSnapshot,
			outer.p.foreignReentry,
			outer.task.g.active,
			outer.task.g.osThreadLockDepth,
		)
	}
	if duplicate, ok := ConsumeForeignReentryCompletion(&innerRecord); ok ||
		duplicate != (CompletionSnapshot{}) {
		t.Fatal("nested completion was consumable twice")
	}
	if !RestoreExecutorResume(&innerHandoff) ||
		outer.p.osThreadLockOwner != outer.task.g {
		t.Fatal("nested callback did not restore its exact physical parent")
	}

	outerSnapshot := finishForeignReentryChild(
		t,
		outer.p,
		outer.driver,
		outer.task,
		outer.child,
		outerResume,
		&outer.record,
		CompletionReturn,
		nil,
		nil,
	)
	if outerSnapshot.Status != CompletionReturn ||
		outer.p.foreignReentry != nil ||
		outer.task.g.osThreadLockDepth != 0 {
		t.Fatalf("outer completion after nesting = %+v", outerSnapshot)
	}
	outer.restore(t)
	runtime.KeepAlive(innerChild.memory)
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	outer.keepAlive()
}

func TestForeignReentryReceiptsAndExecutorBindingFailClosed(t *testing.T) {
	if validActionFlags(Action{
		Kind:  ActionForeignReentryComplete,
		Flags: ActionRetirePhysicalOwner,
	}) {
		t.Fatal("foreign-reentry receipt accepted a physical-owner flag")
	}

	p := new(P)
	p.foreignReentry = new(ForeignReentryRecord)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	handle := registerTestExecutor(t, registry)
	if BindExecutorSourceCatalog(
		driver,
		p,
		registry,
		handle,
		ExecutorSourceCatalog{},
	) {
		t.Fatal("executor bound a P with residual foreign-reentry state")
	}
}
