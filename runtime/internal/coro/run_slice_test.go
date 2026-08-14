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

func takeNormalRunnerDecision(t *testing.T, g *G) {
	t.Helper()
	outcome, caseID, lease, task, ok := TakeRunDecision(g, ParkTicket{})
	if !ok || outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) || task != TaskCancelNone {
		t.Fatalf("normal runner decision = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, task, ok)
	}
}

func runnerYieldAction(t *testing.T, driver *ExecutorDriver, step ExecutorRunStep, task *yieldingTestG) {
	t.Helper()
	if step.Kind != ExecutorRunStepAction || step.G != task.g || step.Action.Kind != ActionCheckResume {
		t.Fatalf("runner yield action = %+v", step)
	}
	resume, _, ok := BeginIssuedExecutorResumeRuntimeContext(driver, task.g)
	if !ok || resume.Kind != ActionResume || resume.Handle != task.handle {
		t.Fatalf("runner yield check = (%+v, %t)", resume, ok)
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare runner yield")
	}
	next, committed, ok := ResumedExecutorRun(driver, driver.p, task.g, resume)
	if !ok || !committed || next.Kind != ActionYield {
		t.Fatalf("commit runner yield = (%+v, %t)", next, ok)
	}
}

func runnerNextPhysicalAction(t *testing.T, driver *ExecutorDriver, task *yieldingTestG, want ActionKind) ExecutorRunStep {
	t.Helper()
	step, ok := NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != task.g || step.Action.Kind != want {
		t.Fatalf("runner dispatch %d = (%+v, %t)", want, step, ok)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.G != task.g || step.Action.Kind != want {
		t.Fatalf("runner action %d = (%+v, %t)", want, step, ok)
	}
	return step
}

func TestExecutorRunResumeRuntimeContextDescriptorCapability(t *testing.T) {
	for _, test := range []struct {
		name      string
		flags     uint32
		anonymous bool
		wantNeeds bool
		wantOK    bool
	}{
		{name: "ordinary frame", wantNeeds: true, wantOK: true},
		{name: "anonymous legacy frame", anonymous: true, wantNeeds: true, wantOK: true},
		{name: "context independent", flags: FrameDescriptorNoRuntimeContextV1, wantOK: true},
		{name: "hidden context independent", flags: FrameDescriptorTraceHiddenV1 | FrameDescriptorNoRuntimeContextV1, wantOK: true},
		{name: "unknown capability", flags: 1 << 31},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := new(P)
			driver, _, _ := bindTestExecutorDriver(t, p)
			task := newYieldingTestG(t, test.name)
			descriptor := (*FrameDescriptorV1)(task.frame.descriptor)
			descriptor.Flags = test.flags
			if test.anonymous {
				descriptor.Function = ""
			}
			if !Enqueue(p, task.g) {
				t.Fatal("enqueue descriptor-capability task")
			}
			step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
			_, needs, modeOK := CheckedExecutorRunRuntimeContext(driver, task.g, step.Action, false)
			if needs != test.wantNeeds || modeOK != test.wantOK {
				t.Fatalf("runtime context mode = (%t, %t), want (%t, %t)", needs, modeOK, test.wantNeeds, test.wantOK)
			}
		})
	}
}

func queueRunnerCheckDestroy(t *testing.T, driver *ExecutorDriver, task *yieldingTestG) *Frame {
	t.Helper()
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok := Checked(driver.p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check completing runner root")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare completing runner root")
	}
	next, ok := Resumed(driver.p, task.g, resume)
	if !ok || next.Kind != ActionCheckDestroy || !CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("queue runner check-destroy = (%+v, %t)", next, ok)
	}
	return task.g.destroyTarget
}

func queueRunnerPanicDestroy(t *testing.T, driver *ExecutorDriver, task *yieldingTestG) (*Frame, *testFrame) {
	t.Helper()
	leafHandle := unsafe.Pointer(new(byte))
	leaf := newTestFrame(t, task.g, leafHandle, task.handle)
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok := Checked(driver.p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check panicking runner root")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendCall)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareAwait(task.g, task.handle, leafHandle) {
		t.Fatal("prepare panicking runner leaf")
	}
	next, ok := Resumed(driver.p, task.g, resume)
	if !ok || next.Kind != ActionCheckResume || !CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("queue panicking runner leaf = (%+v, %t)", next, ok)
	}

	step = runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok = Checked(driver.p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check panicking runner leaf")
	}
	takeNormalRunnerDecision(t, task.g)
	typeWord, dataWord := new(byte), new(byte)
	leaf.header.SuspendReason = uint16(SuspendPanic)
	leaf.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PreparePanic(task.g, leafHandle, leaf.header, unsafe.Pointer(typeWord), unsafe.Pointer(dataWord)) {
		t.Fatal("publish runner panic")
	}
	next, ok = Resumed(driver.p, task.g, resume)
	if !ok || next.Kind != ActionCheckDestroy || !CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("queue runner panic leaf destroy = (%+v, %t)", next, ok)
	}

	step = runnerNextPhysicalAction(t, driver, task, ActionCheckDestroy)
	destroy, ok := Checked(driver.p, task.g, step.Action, true)
	if !ok || destroy.Kind != ActionDestroy {
		t.Fatal("check runner panic leaf destroy")
	}
	releaseTestFrame(t, task.g, leaf)
	next, ok = DestroyedBounded(driver.p, task.g, destroy)
	if !ok || next.Kind != ActionPanicDestroy || !CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("queue runner panic ancestor destroy = (%+v, %t)", next, ok)
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	return task.g.destroyTarget, leaf
}

func TestExecutorRunBudgetOneStableProgressAndFIFO(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	a := newYieldingTestG(t, "runner-a")
	b := newYieldingTestG(t, "runner-b")
	if !Enqueue(p, a.g) || !Enqueue(p, b.g) {
		t.Fatal("enqueue budget-one tasks")
	}

	step, ok := NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != a.g ||
		p.current != a.g || p.action.Kind != ActionCheckResume || driver.run.issued != ActionInvalid {
		t.Fatalf("budget-one dispatch A = (%+v, %t), action=%+v cursor=%+v", step, ok, p.action, driver.run)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.G != a.g || driver.run.issued != ActionCheckResume {
		t.Fatalf("budget-one action A = (%+v, %t), cursor=%+v", step, ok, driver.run)
	}
	runnerYieldAction(t, driver, step, a)
	if p.current != nil || p.readyHead != b.g || p.readyTail != a.g || a.g.runAction != ActionInvalid ||
		driver.run.issued != ActionInvalid {
		t.Fatalf("stable A return = current:%p head:%p tail:%p cursor:%+v", p.current, p.readyHead, p.readyTail, driver.run)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != b.g {
		t.Fatalf("FIFO dispatch B = (%+v, %t)", step, ok)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.G != b.g {
		t.Fatalf("FIFO action B = (%+v, %t)", step, ok)
	}
	runnerYieldAction(t, driver, step, b)
	runtime.KeepAlive(a.frame.memory)
	runtime.KeepAlive(b.frame.memory)
}

func TestExecutorRunManagedResumePendingIsObservational(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "managed-resume-pending")

	if pending, ok := ExecutorRunManagedResumePending(driver); !ok || pending {
		t.Fatalf("empty managed-resume observation = (%t, %t)", pending, ok)
	}
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue managed-resume task")
	}
	if pending, ok := ExecutorRunManagedResumePending(driver); !ok || pending {
		t.Fatalf("queued managed-resume observation = (%t, %t)", pending, ok)
	}

	step, ok := NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != task.g ||
		p.current != task.g || p.action.Kind != ActionCheckResume ||
		driver.run.issued != ActionInvalid {
		t.Fatalf("managed-resume dispatch = (%+v, %t), action=%+v cursor=%+v", step, ok, p.action, driver.run)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if pending, observed := ExecutorRunManagedResumePending(driver); !observed || !pending {
			t.Fatalf("managed-resume observation %d = (%t, %t)", attempt, pending, observed)
		}
		if p.current != task.g || p.action.Kind != ActionCheckResume ||
			driver.run.issued != ActionInvalid {
			t.Fatalf("managed-resume observation %d mutated action state", attempt)
		}
	}

	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.G != task.g ||
		step.Action.Kind != ActionCheckResume || driver.run.issued != ActionCheckResume {
		t.Fatalf("managed-resume action = (%+v, %t), cursor=%+v", step, ok, driver.run)
	}
	if pending, observed := ExecutorRunManagedResumePending(driver); observed || pending {
		t.Fatalf("issued managed-resume observation = (%t, %t)", pending, observed)
	}
	runnerYieldAction(t, driver, step, task)
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunSliceCapabilityRetainsExactOwnerAcrossBoundedSteps(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "run-slice-capability")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue run-slice capability task")
	}
	run, ok := BeginExecutorRunSlice(driver)
	if !ok {
		t.Fatal("begin executor run-slice capability")
	}
	dispatch, ok := run.Next()
	if !ok || dispatch.Kind != ExecutorRunStepDispatch || dispatch.G != task.g ||
		dispatch.Action.Kind != ActionCheckResume || driver.run.issued != ActionInvalid {
		t.Fatalf("capability dispatch = (%+v, %t), cursor=%+v", dispatch, ok, driver.run)
	}
	step, ok := run.Next()
	if !ok || step.Kind != ExecutorRunStepAction || step.G != task.g ||
		step.Action != dispatch.Action || driver.run.issued != ActionCheckResume {
		t.Fatalf("capability action = (%+v, %t), cursor=%+v", step, ok, driver.run)
	}
	runnerYieldAction(t, driver, step, task)
	if next, selected := run.Next(); !selected || next.Kind != ExecutorRunStepDispatch ||
		next.G != task.g || next.Action.Kind != ActionCheckResume {
		t.Fatalf("capability reuse after stable reduction = (%+v, %t)", next, selected)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunSliceCompactActionPreservesSelectionAndBudgetBoundaries(t *testing.T) {
	if compact, full := unsafe.Sizeof(ExecutorRunActionStep{}), unsafe.Sizeof(ExecutorRunStep{}); compact >= full {
		t.Fatalf("compact action ABI size = %d, full step = %d", compact, full)
	}
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "run-slice-compact-action")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue compact-action task")
	}
	run, ok := BeginExecutorRunSlice(driver)
	if !ok {
		t.Fatal("begin compact-action run slice")
	}

	// A one-unit caller may not combine dequeue with the physical action. The
	// compact probe must be observational and leave the complete selector's
	// ordinary Dispatch boundary intact.
	if step, selected, valid := run.NextAction(); !valid || selected || step != (ExecutorRunActionStep{}) {
		t.Fatalf("one-unit compact probe = (%+v, %t, %t)", step, selected, valid)
	}
	if p.readyHead != task.g || p.readyTail != task.g || p.readyCount != 1 || p.current != nil ||
		driver.run.issued != ActionInvalid {
		t.Fatalf("one-unit compact probe mutated scheduler: head=%p tail=%p count=%d current=%p cursor=%+v",
			p.readyHead, p.readyTail, p.readyCount, p.current, driver.run)
	}
	dispatch, dispatched := run.Next()
	if !dispatched || dispatch.Kind != ExecutorRunStepDispatch || dispatch.G != task.g ||
		dispatch.Action.Kind != ActionCheckResume || driver.run.issued != ActionInvalid {
		t.Fatalf("compact fallback dispatch = (%+v, %t), cursor=%+v", dispatch, dispatched, driver.run)
	}
	action, selected, valid := run.NextAction()
	if !valid || !selected || action.Dispatched || action.G != task.g ||
		action.Action != dispatch.Action || driver.run.issued != ActionCheckResume {
		t.Fatalf("compact issued action = (%+v, %t, %t), cursor=%+v", action, selected, valid, driver.run)
	}
	runnerYieldAction(t, driver, ExecutorRunStep{
		Kind: ExecutorRunStepAction, G: action.G, Action: action.Action,
	}, task)

	// A two-unit caller owns the managed-execution lease and may collapse the
	// same dequeue/action pair without constructing the cold event union.
	action, selected, valid = run.NextActionCombined()
	if !valid || !selected || !action.Dispatched || action.G != task.g ||
		action.Action.Kind != ActionCheckResume || driver.run.issued != ActionCheckResume {
		t.Fatalf("combined compact action = (%+v, %t, %t), cursor=%+v", action, selected, valid, driver.run)
	}
	runnerYieldAction(t, driver, ExecutorRunStep{
		Kind: ExecutorRunStepAction, Dispatched: true, G: action.G, Action: action.Action,
	}, task)
	runtime.KeepAlive(task.frame.memory)
}

func TestCurrentExecutorDriverForActiveResumeUsesIssuedCapability(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "active-resume-capability")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue active-resume-capability task")
	}
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok := Checked(p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("enter active-resume capability")
	}
	takeNormalRunnerDecision(t, task.g)
	if current, route, valid := CurrentExecutorDriverForActiveResume(task.g); !valid || current != driver || route != driver.route {
		t.Fatalf("active-resume driver = (%p, %d, %t)", current, route, valid)
	}
	savedExecutor := p.executor
	p.executor = nil
	if current, route, valid := CurrentExecutorDriverForActiveResume(task.g); valid || current != nil || route != 0 {
		t.Fatalf("ownerless active-resume driver = (%p, %d, %t)", current, route, valid)
	}
	p.executor = savedExecutor
	savedIssued := driver.run.issued
	driver.run.issued = ActionInvalid
	if current, route, valid := CurrentExecutorDriverForActiveResume(task.g); valid || current != nil || route != 0 {
		t.Fatalf("unissued active-resume driver = (%p, %d, %t)", current, route, valid)
	}
	driver.run.issued = savedIssued
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare active-resume capability yield")
	}
	next, resumed := Resumed(p, task.g, resume)
	if !resumed || next.Kind != ActionYield || !CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("finish active-resume capability = (%+v, %t)", next, resumed)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestCurrentExecutorDriverForCompilerTaskUsesHiddenTaskCapability(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "compiler-task-capability")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue compiler-task-capability task")
	}
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok := Checked(p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("enter compiler-task capability")
	}
	takeNormalRunnerDecision(t, task.g)
	if current, route, valid := CurrentExecutorDriverForCompilerTask(task.g); !valid || current != driver || route != driver.route {
		t.Fatalf("compiler-task driver = (%p, %d, %t)", current, route, valid)
	}
	// The hidden task is the capability; this boundary does not require the
	// host runner's optional issued-action marker.
	savedIssued := driver.run.issued
	driver.run.issued = ActionInvalid
	if current, route, valid := CurrentExecutorDriverForCompilerTask(task.g); !valid || current != driver || route != driver.route {
		t.Fatalf("unissued compiler-task driver = (%p, %d, %t)", current, route, valid)
	}
	driver.run.issued = savedIssued
	savedCurrent := p.current
	p.current = nil
	if current, route, valid := CurrentExecutorDriverForCompilerTask(task.g); valid || current != nil || route != 0 {
		t.Fatalf("detached compiler-task driver = (%p, %d, %t)", current, route, valid)
	}
	p.current = savedCurrent
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare compiler-task capability yield")
	}
	next, resumed := Resumed(p, task.g, resume)
	if !resumed || next.Kind != ActionYield || !CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("finish compiler-task capability = (%+v, %t)", next, resumed)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunIssuedCommitRetainsExactOwnerGate(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "issued-owner-gate")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue issued-owner-gate task")
	}
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok := Checked(p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check issued-owner-gate resume")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare issued-owner-gate yield")
	}
	next, ok := Resumed(p, task.g, resume)
	if !ok || next.Kind != ActionYield {
		t.Fatalf("resume issued-owner-gate task = (%+v, %t)", next, ok)
	}

	// run.issued alone is not authority: the exact P back-pointer and bound
	// executor mode remain mandatory at the commit boundary.
	p.executor = nil
	if CommitExecutorRunAction(driver, task.g, next) {
		t.Fatal("issued commit accepted a missing P owner")
	}
	p.executor = driver
	preemptStore(&p.executorMode, executorModeUnbound)
	if CommitExecutorRunAction(driver, task.g, next) {
		t.Fatal("issued commit accepted an unbound P")
	}
	preemptStore(&p.executorMode, executorModeBound)
	if !CommitExecutorRunAction(driver, task.g, next) {
		t.Fatal("issued commit rejected restored exact owner")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunCommandBootstrapDirectChildHandoffPrecedesTwoPeers(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	command := new(G)
	if !InitG(command) {
		t.Fatal("initialize command bootstrap G")
	}
	rootHandle := unsafe.Pointer(new(byte))
	childHandle := unsafe.Pointer(new(byte))
	root := newTestFrame(t, command, rootHandle, nil)
	child := newTestFrame(t, command, childHandle, rootHandle)
	if !AdoptRoot(command, rootHandle) || !Enqueue(p, command) {
		t.Fatal("publish command bootstrap G")
	}

	step, ok := NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != command || step.Action.Handle != rootHandle {
		t.Fatalf("dispatch command bootstrap root = (%+v, %t)", step, ok)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.G != command ||
		step.Action.Kind != ActionCheckResume || step.Action.Handle != rootHandle {
		t.Fatalf("run command bootstrap root = (%+v, %t)", step, ok)
	}
	resume, ok := Checked(p, command, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check command bootstrap root")
	}
	takeNormalRunnerDecision(t, command)
	root.header.SuspendReason = uint16(SuspendCall)
	root.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareAwait(command, rootHandle, childHandle) {
		t.Fatal("prepare command bootstrap direct child")
	}
	next, ok := Resumed(p, command, resume)
	if !ok || next.Kind != ActionCheckResume || next.Handle != childHandle ||
		!CommitExecutorRunAction(driver, command, next) {
		t.Fatalf("queue command bootstrap direct child = (%+v, %t)", next, ok)
	}

	peerA := newYieldingTestG(t, "command-exit-peer-a")
	peerB := newYieldingTestG(t, "command-exit-peer-b")
	if !Enqueue(p, peerA.g) || !Enqueue(p, peerB.g) {
		t.Fatal("enqueue command-exit peers")
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != command || step.Action.Handle != childHandle {
		t.Fatalf("dispatch command direct child = (%+v, %t)", step, ok)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.G != command ||
		step.Action.Kind != ActionCheckResume || step.Action.Handle != childHandle {
		t.Fatalf("run command direct child = (%+v, %t)", step, ok)
	}
	resume, ok = Checked(p, command, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check command direct child")
	}
	takeNormalRunnerDecision(t, command)
	child.header.SuspendReason = uint16(SuspendFrameComplete)
	child.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(command, childHandle, child.header) {
		t.Fatal("prepare command direct-child completion")
	}
	next, ok = Resumed(p, command, resume)
	if !ok || next.Kind != ActionCheckDestroy || next.Handle != childHandle ||
		!CommitExecutorRunCommandBootstrapDirectChildHandoff(driver, command, next) {
		t.Fatalf("commit command direct-child destroy handoff = (%+v, %t)", next, ok)
	}
	if p.readyHead != command || command.nextReady != peerA.g || peerA.g.nextReady != peerB.g ||
		p.readyTail != peerB.g {
		t.Fatalf("direct-child destroy handoff queue = head:%p commandNext:%p peerANext:%p tail:%p",
			p.readyHead, command.nextReady, peerA.g.nextReady, p.readyTail)
	}

	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != command || step.Action.Handle != childHandle {
		t.Fatalf("dispatch command direct-child destroy = (%+v, %t)", step, ok)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.G != command ||
		step.Action.Kind != ActionCheckDestroy || step.Action.Handle != childHandle {
		t.Fatalf("run command direct-child destroy = (%+v, %t)", step, ok)
	}
	destroy, ok := Checked(p, command, step.Action, true)
	if !ok || destroy.Kind != ActionDestroy {
		t.Fatal("check command direct-child destroy")
	}
	releaseTestFrame(t, command, child)
	next, ok = DestroyedBounded(p, command, destroy)
	if !ok || next.Kind != ActionCheckResume || next.Handle != rootHandle ||
		!CommitExecutorRunCommandBootstrapDirectChildHandoff(driver, command, next) {
		t.Fatalf("commit command exact-root resume handoff = (%+v, %t)", next, ok)
	}
	if p.readyHead != command || command.nextReady != peerA.g || peerA.g.nextReady != peerB.g ||
		p.readyTail != peerB.g {
		t.Fatalf("exact-root resume handoff queue = head:%p commandNext:%p peerANext:%p tail:%p",
			p.readyHead, command.nextReady, peerA.g.nextReady, p.readyTail)
	}
	runtime.KeepAlive(root.memory)
	runtime.KeepAlive(child.memory)
	runtime.KeepAlive(peerA.frame.memory)
	runtime.KeepAlive(peerB.frame.memory)
}

func TestExecutorRunCommandBootstrapDirectChildHandoffKeepsNestedChildFIFO(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	command := new(G)
	if !InitG(command) {
		t.Fatal("initialize nested command G")
	}
	rootHandle := unsafe.Pointer(new(byte))
	parentHandle := unsafe.Pointer(new(byte))
	nestedHandle := unsafe.Pointer(new(byte))
	root := newTestFrame(t, command, rootHandle, nil)
	parent := newTestFrame(t, command, parentHandle, rootHandle)
	nested := newTestFrame(t, command, nestedHandle, parentHandle)
	if !AdoptRoot(command, rootHandle) || !Enqueue(p, command) {
		t.Fatal("publish nested command G")
	}

	await := func(from *testFrame, fromHandle, toHandle unsafe.Pointer) {
		t.Helper()
		step, ok := NextExecutorRunStep(driver)
		if !ok || step.Kind != ExecutorRunStepDispatch || step.G != command || step.Action.Handle != fromHandle {
			t.Fatalf("dispatch await frame %p = (%+v, %t)", fromHandle, step, ok)
		}
		step, ok = NextExecutorRunStep(driver)
		if !ok || step.Kind != ExecutorRunStepAction || step.G != command ||
			step.Action.Kind != ActionCheckResume || step.Action.Handle != fromHandle {
			t.Fatalf("run await frame %p = (%+v, %t)", fromHandle, step, ok)
		}
		resume, ok := Checked(p, command, step.Action, false)
		if !ok || resume.Kind != ActionResume {
			t.Fatalf("check await frame %p", fromHandle)
		}
		takeNormalRunnerDecision(t, command)
		from.header.SuspendReason = uint16(SuspendCall)
		from.header.Lifecycle = uint16(FrameSuspended)
		if !PrepareAwait(command, fromHandle, toHandle) {
			t.Fatalf("prepare await %p -> %p", fromHandle, toHandle)
		}
		next, ok := Resumed(p, command, resume)
		if !ok || next.Kind != ActionCheckResume || next.Handle != toHandle ||
			!CommitExecutorRunAction(driver, command, next) {
			t.Fatalf("commit await %p -> %p = (%+v, %t)", fromHandle, toHandle, next, ok)
		}
	}
	await(root, rootHandle, parentHandle)
	await(parent, parentHandle, nestedHandle)

	peerA := newYieldingTestG(t, "nested-fifo-peer-a")
	peerB := newYieldingTestG(t, "nested-fifo-peer-b")
	if !Enqueue(p, peerA.g) || !Enqueue(p, peerB.g) {
		t.Fatal("enqueue nested FIFO peers")
	}
	step, ok := NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != command || step.Action.Handle != nestedHandle {
		t.Fatalf("dispatch nested child = (%+v, %t)", step, ok)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.G != command ||
		step.Action.Kind != ActionCheckResume || step.Action.Handle != nestedHandle {
		t.Fatalf("run nested child = (%+v, %t)", step, ok)
	}
	resume, ok := Checked(p, command, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check nested child")
	}
	takeNormalRunnerDecision(t, command)
	nested.header.SuspendReason = uint16(SuspendFrameComplete)
	nested.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(command, nestedHandle, nested.header) {
		t.Fatal("prepare nested child completion")
	}
	next, ok := Resumed(p, command, resume)
	if !ok || next.Kind != ActionCheckDestroy || next.Handle != nestedHandle {
		t.Fatalf("nested child completion = (%+v, %t)", next, ok)
	}
	if CommitExecutorRunCommandBootstrapDirectChildHandoff(driver, command, next) {
		t.Fatal("nested child destroy accepted as command bootstrap exit handoff")
	}
	if p.current != command || p.readyHead != peerA.g || driver.run.issued != ActionCheckResume ||
		!CommitExecutorRunAction(driver, command, next) {
		t.Fatal("rejected nested child handoff was not atomic")
	}
	if p.readyHead != peerA.g || peerA.g.nextReady != peerB.g || peerB.g.nextReady != command ||
		p.readyTail != command {
		t.Fatalf("nested destroy FIFO queue = head:%p peerANext:%p peerBNext:%p tail:%p",
			p.readyHead, peerA.g.nextReady, peerB.g.nextReady, p.readyTail)
	}
	if dequeue(p) != peerA.g || dequeue(p) != peerB.g {
		t.Fatal("remove nested FIFO peers")
	}

	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != command || step.Action.Handle != nestedHandle {
		t.Fatalf("dispatch nested destroy = (%+v, %t)", step, ok)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.G != command ||
		step.Action.Kind != ActionCheckDestroy || step.Action.Handle != nestedHandle {
		t.Fatalf("run nested destroy = (%+v, %t)", step, ok)
	}
	destroy, ok := Checked(p, command, step.Action, true)
	if !ok || destroy.Kind != ActionDestroy {
		t.Fatal("check nested destroy")
	}
	releaseTestFrame(t, command, nested)
	next, ok = DestroyedBounded(p, command, destroy)
	if !ok || next.Kind != ActionCheckResume || next.Handle != parentHandle {
		t.Fatalf("nested parent resume = (%+v, %t)", next, ok)
	}
	if !Enqueue(p, peerA.g) || !Enqueue(p, peerB.g) {
		t.Fatal("re-enqueue nested FIFO peers")
	}
	if CommitExecutorRunCommandBootstrapDirectChildHandoff(driver, command, next) {
		t.Fatal("non-root parent resume accepted as command bootstrap exit handoff")
	}
	if p.current != command || p.readyHead != peerA.g || driver.run.issued != ActionCheckDestroy ||
		!CommitExecutorRunAction(driver, command, next) {
		t.Fatal("rejected nested parent handoff was not atomic")
	}
	if p.readyHead != peerA.g || peerA.g.nextReady != peerB.g || peerB.g.nextReady != command ||
		p.readyTail != command {
		t.Fatalf("nested parent resume FIFO queue = head:%p peerANext:%p peerBNext:%p tail:%p",
			p.readyHead, peerA.g.nextReady, peerB.g.nextReady, p.readyTail)
	}
	runtime.KeepAlive(root.memory)
	runtime.KeepAlive(parent.memory)
	runtime.KeepAlive(nested.memory)
	runtime.KeepAlive(peerA.frame.memory)
	runtime.KeepAlive(peerB.frame.memory)
}

func TestPauseExecutorRunActionFailureIsAtomic(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "pause-atomic")
	if !Enqueue(p, task.g) || dequeue(p) != task.g {
		t.Fatal("prepare pause atomic task")
	}
	action, ok := BeginRunG(p, task.g)
	if !ok {
		t.Fatal("begin pause atomic task")
	}
	preemptStore(&p.schedule, scheduleDisabled)
	if pauseExecutorRunAction(p, task.g, action, executorRunQueueTail) {
		t.Fatal("pause accepted disabled queue")
	}
	if p.current != task.g || p.action != action || p.readyHead != nil || p.readyTail != nil ||
		task.g.state != GRunning || task.g.runP != p || task.g.runAction != ActionInvalid ||
		task.g.queued || task.g.nextReady != nil || p.servicePreemptBudget != servicePreemptSafepointBudget {
		t.Fatalf("failed pause partially committed: current=%p action=%+v head=%p tail=%p state=%d runP=%p runAction=%d queued=%t budget=%d",
			p.current, p.action, p.readyHead, p.readyTail, task.g.state, task.g.runP,
			task.g.runAction, task.g.queued, p.servicePreemptBudget)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunDispatchFailureRestoresReadyHead(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	a := newYieldingTestG(t, "dispatch-atomic-a")
	b := newYieldingTestG(t, "dispatch-atomic-b")
	if !Enqueue(p, a.g) || !Enqueue(p, b.g) {
		t.Fatal("enqueue dispatch atomic tasks")
	}
	a.g.transferState = runnableTransferGImported

	// Corrupt only the selected element, leaving the O(1) queue header valid.
	// The bounded dispatcher must fail closed without silently dropping it or
	// losing the imported no-retransfer ownership state.
	a.g.state = GWaiting
	if step, ok := NextExecutorRunStep(driver); ok || step != (ExecutorRunStep{}) {
		t.Fatalf("invalid ready head dispatched = (%+v, %t)", step, ok)
	}
	if p.readyHead != a.g || p.readyTail != b.g || !a.g.queued || a.g.nextReady != b.g ||
		a.g.transferState != runnableTransferGImported ||
		!b.g.queued || b.g.nextReady != nil || p.current != nil || p.action != (Action{}) {
		t.Fatalf("failed dispatch mutated queue: head=%p tail=%p a={queued:%t next:%p transfer:%d} b={queued:%t next:%p} current=%p action=%+v",
			p.readyHead, p.readyTail, a.g.queued, a.g.nextReady, a.g.transferState,
			b.g.queued, b.g.nextReady, p.current, p.action)
	}
	runtime.KeepAlive(a.frame.memory)
	runtime.KeepAlive(b.frame.memory)
}

func TestExecutorRunStartedEpochPrecedesReadyAndHotSourceAlternates(t *testing.T) {
	p := new(P)
	driver, registry, handle := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "source-fair")
	if !Enqueue(p, task.g) || registry.Request(handle) != ExecutorRequestPublished {
		t.Fatal("prepare hot source runner")
	}

	sourceSteps, sourceUsed := uint32(0), uint32(0)
	for {
		step, ok := NextExecutorRunStep(driver)
		if !ok || step.Kind != ExecutorRunStepSource || step.Poll.Used == 0 ||
			step.Poll.Used > executorRunSourceBatchQuantum {
			t.Fatalf("started epoch step %d = (%+v, %t)", sourceSteps, step, ok)
		}
		sourceSteps++
		sourceUsed += step.Poll.Used
		if step.Poll.Complete {
			break
		}
		if p.current != nil || p.readyHead != task.g {
			t.Fatal("ready G interrupted a started A/ack/B epoch")
		}
	}
	if want, ok := MinExecutorPollBudget(driver); !ok || sourceUsed != want {
		t.Fatalf("batched source transaction used %d reductions in %d steps, want (%d, %t)",
			sourceUsed, sourceSteps, want, ok)
	}
	// Publish the next hot epoch before paying the ready debt. Dispatch and one
	// complete physical G action must still precede that epoch.
	if registry.Request(handle) != ExecutorRequestPublished {
		t.Fatal("publish next hot source epoch")
	}
	step, ok := NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != task.g {
		t.Fatalf("ready debt dispatch = (%+v, %t)", step, ok)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.G != task.g {
		t.Fatalf("ready debt action = (%+v, %t)", step, ok)
	}
	runnerYieldAction(t, driver, step, task)
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepSource || step.Poll.Used == 0 ||
		step.Poll.Used > executorRunSourceBatchQuantum {
		t.Fatalf("hot source did not alternate after one G action = (%+v, %t)", step, ok)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunActionPreservesReadyDebtPublishedDuringResume(t *testing.T) {
	p := new(P)
	driver, registry, handle := bindTestExecutorDriver(t, p)
	current := newYieldingTestG(t, "ready-debt-current")
	peer := newYieldingTestG(t, "ready-debt-peer")
	if !Enqueue(p, current.g) || registry.Request(handle) != ExecutorRequestPublished {
		t.Fatal("prepare ready-debt action")
	}
	// This debt selected current and is paid when its physical Action starts.
	// The pending source remains observable behind that action.
	driver.run.readyDebt = true
	step := runnerNextPhysicalAction(t, driver, current, ActionCheckResume)
	if driver.run.readyDebt || driver.run.issued != ActionCheckResume ||
		!registry.ObserveRequested(handle) {
		t.Fatalf("issued action retained old debt: run=%+v requested=%t",
			driver.run, registry.ObserveRequested(handle))
	}
	resume, ok := Checked(p, current.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check ready-debt current")
	}
	takeNormalRunnerDecision(t, current.g)

	// Model a same-owner completion produced inside llvm.coro.resume: it makes
	// a peer runnable and publishes a new debt before the current Action receipt.
	if !Enqueue(p, peer.g) {
		t.Fatal("publish peer during physical resume")
	}
	driver.run.readyDebt = true
	current.frame.header.SuspendReason = uint16(SuspendYield)
	current.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(current.g, current.handle, current.frame.header) {
		t.Fatal("prepare current yield")
	}
	next, resumed := Resumed(p, current.g, resume)
	if !resumed || next.Kind != ActionYield ||
		!CommitExecutorRunAction(driver, current.g, next) {
		t.Fatalf("commit action with newly published ready debt = (%+v, %t)", next, resumed)
	}
	if !driver.run.readyDebt || driver.run.issued != ActionInvalid ||
		p.readyHead != peer.g || p.readyTail != current.g ||
		!registry.ObserveRequested(handle) {
		t.Fatalf("action receipt lost new debt: run=%+v head=%p tail=%p requested=%t",
			driver.run, p.readyHead, p.readyTail, registry.ObserveRequested(handle))
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != peer.g {
		t.Fatalf("new peer did not precede pending source = (%+v, %t)", step, ok)
	}
	runtime.KeepAlive(current.frame.memory)
	runtime.KeepAlive(peer.frame.memory)
}

func TestExecutorRunReadyDebtPrecedesNewDirectCompletion(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "ready-debt-before-direct")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue ready-debt task")
	}
	driver.run.readyDebt = true
	completion := &DirectChannelCompletion{
		owner: driver,
		route: driver.route,
	}
	preemptStore(&completion.state, uint32(directChannelCompletionMatched))
	if !PublishExecutorDirectChannelCompletion(driver, completion) {
		t.Fatal("publish direct completion behind ready debt")
	}

	step, selected, ok := nextExecutorRunActionValidated(driver, true)
	if !ok || !selected || !step.Dispatched || step.G != task.g ||
		!executorDirectChannelCompletionPending(driver) {
		t.Fatalf("compact ready debt/direct completion ordering = (%+v, %t, %t), pending=%t",
			step, selected, ok, executorDirectChannelCompletionPending(driver))
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunCursorRejectsImplicitLegacySwitch(t *testing.T) {
	p := new(P)
	driver, registry, handle := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "legacy-switch")
	if !Enqueue(p, task.g) || registry.Request(handle) != ExecutorRequestPublished {
		t.Fatal("prepare legacy switch")
	}
	for {
		step, ok := NextExecutorRunStep(driver)
		if !ok || step.Kind != ExecutorRunStepSource {
			t.Fatalf("legacy-switch source = (%+v, %t)", step, ok)
		}
		if step.Poll.Complete {
			break
		}
	}
	if !driver.run.readyDebt || p.readyHead != task.g {
		t.Fatalf("legacy-switch precondition = cursor:%+v head:%p", driver.run, p.readyHead)
	}
	beforeRun, beforePoll := driver.run, driver.poll
	beforeHead, beforeTail := p.readyHead, p.readyTail
	if g, ok := NextRunnable(p); ok || g != nil {
		t.Fatalf("legacy NextRunnable crossed bounded cursor = (%p, %t)", g, ok)
	}
	if driver.run != beforeRun || driver.poll != beforePoll || p.readyHead != beforeHead || p.readyTail != beforeTail {
		t.Fatalf("rejected legacy switch mutated state: run=%+v poll=%+v head=%p tail=%p",
			driver.run, driver.poll, p.readyHead, p.readyTail)
	}
	if !EnterExecutorRunCompatibility(driver) || driver.run != (executorRunCursor{}) {
		t.Fatalf("explicit legacy switch = cursor:%+v", driver.run)
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatalf("explicit legacy dequeue = (%p, %t)", g, ok)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunStandbyCompatibilityDefersDirectIngress(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	completion := &DirectChannelCompletion{
		owner: driver,
		route: driver.route,
		state: uint32(directChannelCompletionMatched),
	}
	if !PublishExecutorDirectChannelCompletion(driver, completion) {
		t.Fatal("publish direct completion before standby compatibility")
	}
	if entered, ok := EnterExecutorRunStandbyCompatibility(driver); !ok || entered {
		t.Fatalf("standby compatibility over direct ingress = (%t, %t), want (false, true)", entered, ok)
	}
	if got, ok := takeExecutorDirectChannelCompletion(driver); !ok || got != completion {
		t.Fatalf("take deferred direct completion = (%p, %t), want (%p, true)", got, ok, completion)
	}
	if !executorDirectChannelInboxIdle(driver) {
		t.Fatal("deferred direct completion did not restore idle inbox")
	}
	if entered, ok := EnterExecutorRunStandbyCompatibility(driver); !ok || !entered {
		t.Fatalf("stable standby compatibility = (%t, %t), want (true, true)", entered, ok)
	}
	closeTestExecutorDriver(t, driver)
}

func TestExecutorRunCursorRejectsExportedPollSlice(t *testing.T) {
	for _, timed := range []bool{false, true} {
		name := "plain"
		if timed {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			p := new(P)
			var driver *ExecutorDriver
			var registry *ExecutorRegistry
			var handle ExecutorHandle
			if timed {
				driver, registry, _, handle = bindTestExecutorDriverWithTimers(t, p)
			} else {
				driver, registry, handle = bindTestExecutorDriver(t, p)
			}
			task := newYieldingTestG(t, "poll-slice-cursor")
			if !Enqueue(p, task.g) || registry.Request(handle) != ExecutorRequestPublished {
				t.Fatal("prepare mixed hot-source/ready-debt cursor")
			}

			now := int64(1)
			for {
				var step ExecutorRunStep
				var ok bool
				if timed {
					step, ok = NextExecutorRunStepAt(driver, now)
					now++
				} else {
					step, ok = NextExecutorRunStep(driver)
				}
				if !ok || step.Kind != ExecutorRunStepSource {
					t.Fatalf("mixed cursor source = (%+v, %t)", step, ok)
				}
				if step.Poll.Complete {
					break
				}
			}
			// A small A/ack/B transaction may complete in one bounded Source
			// step. Publish the next hot epoch after completion but before
			// ready-debt dispatch; the exported compatibility poll must still
			// reject this mixed bounded-runner state.
			if registry.Request(handle) != ExecutorRequestPublished {
				t.Fatal("publish hot source behind completed batched epoch")
			}
			if !driver.run.readyDebt ||
				driver.poll != (executorPollTransaction{}) || p.readyHead != task.g {
				t.Fatalf("mixed cursor precondition = run:%+v poll:%+v head:%p",
					driver.run, driver.poll, p.readyHead)
			}
			beforeRun, beforePoll := driver.run, driver.poll
			var progress ExecutorPollProgress
			var ok bool
			if timed {
				progress, ok = PollExecutorSliceAt(driver, now, 1)
			} else {
				progress, ok = PollExecutorSlice(driver, 1)
			}
			if ok || progress != (ExecutorPollProgress{}) {
				t.Fatalf("exported poll crossed bounded cursor = (%+v, %t)", progress, ok)
			}
			if driver.run != beforeRun || driver.poll != beforePoll || p.readyHead != task.g ||
				!registry.ObserveRequested(handle) {
				t.Fatalf("rejected poll mutated mixed cursor: run=%+v poll=%+v head=%p requested=%t",
					driver.run, driver.poll, p.readyHead, registry.ObserveRequested(handle))
			}
			var step ExecutorRunStep
			if timed {
				step, ok = NextExecutorRunStepAt(driver, now)
			} else {
				step, ok = NextExecutorRunStep(driver)
			}
			if !ok || step.Kind != ExecutorRunStepDispatch || step.G != task.g {
				t.Fatalf("runner lost ready-debt priority after rejected poll = (%+v, %t)", step, ok)
			}
			runtime.KeepAlive(task.frame.memory)
		})
	}
}

func TestRequestExecutorSourceServiceResumesBlockedOwnerTransaction(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithTimers(t, p)
	driver.run.blocked = true
	if !RequestExecutorSourceService(driver) ||
		!driver.run.sourceMore || driver.run.blocked {
		t.Fatalf("request owner source service = %+v", driver.run)
	}
	step, ok := NextExecutorRunStepAt(driver, 1)
	if !ok || step.Kind != ExecutorRunStepSource {
		t.Fatalf("requested owner source step = (%+v, %t)", step, ok)
	}
}

func TestNextExecutorRunStepBeforeTimeDefersOnlyTimedSource(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithTimers(t, p)
	task := newYieldingTestG(t, "before-time-ready")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue before-time runnable")
	}
	step, ok := NextExecutorRunStepBeforeTime(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != task.g {
		t.Fatalf("before-time dispatch = (%+v, %t)", step, ok)
	}

	// A separate stable driver isolates the time-required source decision. The
	// before-time probe must reject it without cursor mutation.
	sourceP := new(P)
	sourceDriver, registry, _, handle := bindTestExecutorDriverWithTimers(t, sourceP)
	if result := registry.Request(handle); result != ExecutorRequestPublished {
		t.Fatalf("publish timed source request = %d", result)
	}
	beforeRun, beforePoll := sourceDriver.run, sourceDriver.poll
	if step, ok := NextExecutorRunStepBeforeTime(sourceDriver); ok || step != (ExecutorRunStep{}) {
		t.Fatalf("before-time source probe = (%+v, %t)", step, ok)
	}
	if sourceDriver.run != beforeRun || sourceDriver.poll != beforePoll {
		t.Fatalf("before-time source probe mutated state: run=%+v poll=%+v", sourceDriver.run, sourceDriver.poll)
	}
	step, ok = NextExecutorRunStepAt(sourceDriver, 1)
	if !ok || step.Kind != ExecutorRunStepSource {
		t.Fatalf("timed retry source = (%+v, %t)", step, ok)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestRequestExecutorSourceServiceRejectsUnstableOwner(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "owner-service-unstable")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue unstable owner")
	}
	step, ok := NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.G != task.g {
		t.Fatalf("dispatch unstable owner = (%+v, %t)", step, ok)
	}
	before := driver.run
	if RequestExecutorSourceService(driver) || driver.run != before {
		t.Fatalf("unstable owner source request mutated cursor: before=%+v after=%+v", before, driver.run)
	}
}

func TestExecutorOwnerWaitPendingClosesPublishedWakeWindow(t *testing.T) {
	p := new(P)
	driver, registry, handle := bindTestExecutorDriver(t, p)
	if pending, ok := ExecutorOwnerWaitPending(driver); !ok || pending {
		t.Fatalf("empty owner wait pending = (%t, %t)", pending, ok)
	}
	if result := registry.Request(handle); result != ExecutorRequestPublished {
		t.Fatalf("publish running-owner request = %d", result)
	}
	if pending, ok := ExecutorOwnerWaitPending(driver); !ok || !pending {
		t.Fatalf("requested owner wait pending = (%t, %t)", pending, ok)
	}
	task := newYieldingTestG(t, "owner-wait-pending")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue owner-wait-pending task")
	}
	if pending, ok := ExecutorOwnerWaitPending(driver); !ok || !pending {
		t.Fatalf("runnable owner wait pending = (%t, %t)", pending, ok)
	}
}

func TestExecutorRunTaskControlBlocksQueuedDestroy(t *testing.T) {
	for _, kind := range []ActionKind{ActionCheckDestroy, ActionPanicDestroy} {
		name := "check-destroy"
		if kind == ActionPanicDestroy {
			name = "panic-destroy"
		}
		t.Run(name, func(t *testing.T) {
			p := new(P)
			driver := new(ExecutorDriver)
			registry := new(ExecutorRegistry)
			control := new(TaskControlSource)
			handle := registerTestExecutor(t, registry)
			if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{Control: control}) {
				t.Fatal("bind runner task-control source")
			}
			task := newYieldingTestG(t, "late-source-cancel")
			if !Enqueue(p, task.g) {
				t.Fatal("enqueue late-source-cancel task")
			}
			var target *Frame
			var releasedLeaf *testFrame
			if kind == ActionCheckDestroy {
				target = queueRunnerCheckDestroy(t, driver, task)
			} else {
				target, releasedLeaf = queueRunnerPanicDestroy(t, driver, task)
			}
			if target == nil || target.handle == nil || target.state != FrameDestroyPending || task.g.runAction != kind {
				t.Fatalf("queued destroy precondition = target:%p action:%d", target, task.g.runAction)
			}
			controlID, ok := RegisterTaskControl(control, p, task.g)
			if !ok {
				t.Fatal("register queued destroy task control")
			}
			post := PostTaskControlAndRequest(control, controlID, TaskCancelAbort, registry, handle)
			if post.Control != TaskControlPosted || post.Executor != ExecutorRequestPublished {
				t.Fatalf("post queued destroy task control = (%d, %d)", post.Control, post.Executor)
			}
			for {
				step, advanced := NextExecutorRunStep(driver)
				if !advanced || step.Kind != ExecutorRunStepSource {
					t.Fatalf("deliver queued destroy task control = (%+v, %t)", step, advanced)
				}
				if step.Poll.Complete {
					break
				}
			}
			if task.g.park.taskCancelKind != TaskCancelAbort || task.g.park.taskCancelPhase != taskCancelRequested {
				t.Fatalf("queued destroy cancellation = (%d, %d)", task.g.park.taskCancelKind, task.g.park.taskCancelPhase)
			}
			if claimed, ok := ClaimTaskCancellation(p, task.g); ok || claimed != TaskCancelNone ||
				task.g.park.taskCancelKind != TaskCancelAbort || task.g.park.taskCancelPhase != taskCancelRequested {
				t.Fatalf("queued destroy cancellation was claimable = (%d, %t), token=(%d,%d)",
					claimed, ok, task.g.park.taskCancelKind, task.g.park.taskCancelPhase)
			}

			destroyCount := 0
			step, advanced := NextExecutorRunStep(driver)
			if advanced && step.Kind == ExecutorRunStepAction &&
				(step.Action.Kind == ActionCheckDestroy || step.Action.Kind == ActionPanicDestroy) {
				destroyCount++
			}
			if advanced || step != (ExecutorRunStep{}) || destroyCount != 0 ||
				p.current != nil || p.action != (Action{}) || driver.run.issued != ActionInvalid ||
				p.readyHead != task.g || p.readyTail != task.g || !task.g.queued || task.g.nextReady != nil ||
				task.g.runAction != kind || task.g.destroyTarget != target || target.handle == nil ||
				target.state != FrameDestroyPending {
				t.Fatalf("late cancellation crossed queued destroy: step=(%+v,%t) destroys=%d current=%p action=%+v cursor=%+v head=%p tail=%p queued=%t runAction=%d target=%p state=%d",
					step, advanced, destroyCount, p.current, p.action, driver.run, p.readyHead, p.readyTail,
					task.g.queued, task.g.runAction, task.g.destroyTarget, target.state)
			}
			closeTaskControlFixture(t, control, p, controlID)
			runtime.KeepAlive(task.frame.memory)
			if releasedLeaf != nil {
				runtime.KeepAlive(releasedLeaf.memory)
			}
		})
	}
}

func TestExecutorRunOwnerCancellationBlocksCheckedDestroy(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "late-owner-cancel")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue late owner cancellation task")
	}
	target := queueRunnerCheckDestroy(t, driver, task)
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckDestroy)
	if !RequestTaskCancellation(p, task.g, TaskCancelAbort) {
		t.Fatal("insert owner cancellation before checked destroy")
	}
	destroyCount := 0
	if destroy, ok := Checked(p, task.g, step.Action, true); ok || destroy != (Action{}) {
		destroyCount++
	}
	if destroyCount != 0 || p.current != task.g || p.action != step.Action || driver.run.issued != ActionCheckDestroy ||
		task.g.runP != p || task.g.state != GDispatching || task.g.destroyTarget != target ||
		target.handle != step.Action.Handle || target.state != FrameDestroyPending ||
		task.g.park.taskCancelKind != TaskCancelAbort || task.g.park.taskCancelPhase != taskCancelRequested {
		t.Fatalf("owner cancellation crossed checked destroy: destroys=%d current=%p action=%+v cursor=%+v state=%d target=%p handle=%p cancel=(%d,%d)",
			destroyCount, p.current, p.action, driver.run, task.g.state, task.g.destroyTarget,
			target.handle, task.g.park.taskCancelKind, task.g.park.taskCancelPhase)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunRejectsCancellationAfterDestroyIssued(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "issued-destroy-cancel")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue issued destroy cancellation task")
	}
	target := queueRunnerCheckDestroy(t, driver, task)
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckDestroy)
	destroy, ok := Checked(p, task.g, step.Action, true)
	if !ok || destroy.Kind != ActionDestroy || RequestTaskCancellation(p, task.g, TaskCancelAbort) ||
		task.g.park.taskCancelKind != TaskCancelNone || task.g.park.taskCancelPhase != taskCancelIdle ||
		p.action != destroy || task.g.destroyTarget != target || target.state != FrameDestroyPending {
		t.Fatalf("post-issue cancellation boundary = destroy:(%+v,%t) paction:%+v target:%p state:%d cancel:(%d,%d)",
			destroy, ok, p.action, task.g.destroyTarget, target.state,
			task.g.park.taskCancelKind, task.g.park.taskCancelPhase)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunPanicDestroyHasNoLateOwnerInjectionPoint(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "panic-destroy-owner-cancel")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue panic destroy owner cancellation task")
	}
	target, leaf := queueRunnerPanicDestroy(t, driver, task)
	step := runnerNextPhysicalAction(t, driver, task, ActionPanicDestroy)
	if RequestTaskCancellation(p, task.g, TaskCancelAbort) || task.g.state != GPanicking ||
		task.g.park.taskCancelKind != TaskCancelNone || task.g.park.taskCancelPhase != taskCancelIdle ||
		p.current != task.g || p.action != step.Action || driver.run.issued != ActionPanicDestroy ||
		task.g.destroyTarget != target || target.state != FrameDestroyPending {
		t.Fatalf("panic destroy accepted late owner injection: state=%d cancel=(%d,%d) current=%p action=%+v cursor=%+v target=%p targetState=%d",
			task.g.state, task.g.park.taskCancelKind, task.g.park.taskCancelPhase, p.current,
			p.action, driver.run, task.g.destroyTarget, target.state)
	}
	runtime.KeepAlive(task.frame.memory)
	runtime.KeepAlive(leaf.memory)
}

func TestExecutorRun2048SynchronousAwaitsAreIterative(t *testing.T) {
	const resumeCount = 2048
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize deep runner G")
	}
	frames := make([]*testFrame, resumeCount)
	handles := make([]unsafe.Pointer, resumeCount)
	indexByHandle := make(map[unsafe.Pointer]int, resumeCount)
	for index := range frames {
		handles[index] = unsafe.Pointer(new(byte))
		parent := unsafe.Pointer(nil)
		if index != 0 {
			parent = handles[index-1]
		}
		frames[index] = newTestFrame(t, g, handles[index], parent)
		indexByHandle[handles[index]] = index
	}
	if !AdoptRoot(g, handles[0]) || !Enqueue(p, g) {
		t.Fatal("publish deep runner G")
	}

	resumes := 0
	steps := uint32(0)
	for resumes < resumeCount {
		step, ok := NextExecutorRunStep(driver)
		if !ok {
			t.Fatalf("deep runner step %d failed", steps)
		}
		steps++
		if step.Kind != ExecutorRunStepAction {
			continue
		}
		if step.G != g || step.Action.Kind != ActionCheckResume {
			t.Fatalf("deep runner action %d = %+v", resumes, step)
		}
		resume, ok := Checked(p, g, step.Action, false)
		if !ok || resume.Kind != ActionResume {
			t.Fatalf("deep check %d = (%+v, %t)", resumes, resume, ok)
		}
		takeNormalRunnerDecision(t, g)
		index, found := indexByHandle[resume.Handle]
		if !found || index != resumes {
			t.Fatalf("deep resume order %d = handle:%p index:%d found:%t", resumes, resume.Handle, index, found)
		}
		frame := frames[index]
		if index+1 < resumeCount {
			frame.header.SuspendReason = uint16(SuspendCall)
			frame.header.Lifecycle = uint16(FrameSuspended)
			if !PrepareAwait(g, handles[index], handles[index+1]) {
				t.Fatalf("prepare deep await %d", index)
			}
		} else {
			frame.header.SuspendReason = uint16(SuspendYield)
			frame.header.Lifecycle = uint16(FrameSuspended)
			if !PrepareYield(g, handles[index], frame.header) {
				t.Fatal("prepare final deep yield")
			}
		}
		next, ok := Resumed(p, g, resume)
		if !ok || !CommitExecutorRunAction(driver, g, next) {
			t.Fatalf("commit deep resume %d = (%+v, %t)", index, next, ok)
		}
		resumes++
	}
	if resumes != resumeCount || driver.run.issued != ActionInvalid || p.current != nil ||
		!g.queued || g.runAction != ActionInvalid {
		t.Fatalf("deep runner result = resumes:%d steps:%d cursor:%+v current:%p queued:%t runAction:%d",
			resumes, steps, driver.run, p.current, g.queued, g.runAction)
	}
	for _, frame := range frames {
		runtime.KeepAlive(frame.memory)
	}
}

func TestExecutorRunDestroyReceiptIsStableAndHandleFree(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "destroy-receipt")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue destroy receipt task")
	}
	step, ok := NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch {
		t.Fatal("dispatch destroy receipt task")
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.Action.Kind != ActionCheckResume {
		t.Fatal("select destroy receipt resume")
	}
	resume, ok := Checked(p, task.g, step.Action, false)
	if !ok {
		t.Fatal("check destroy receipt resume")
	}
	takeNormalRunnerDecision(t, task.g)
	if boundary, boundaryOK := NextExecutorRunStep(driver); boundaryOK || boundary != (ExecutorRunStep{}) {
		t.Fatalf("runner exposed boundary after Checked/ActionResume = (%+v, %t)", boundary, boundaryOK)
	}
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	if !EnterOSThreadLock(task.g) {
		t.Fatal("lock bounded-destroy physical owner")
	}
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare destroy receipt completion")
	}
	next, ok := Resumed(p, task.g, resume)
	if !ok || next.Kind != ActionCheckDestroy || !CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("queue destroy receipt check = (%+v, %t)", next, ok)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepDispatch || step.Action.Kind != ActionCheckDestroy {
		t.Fatalf("dispatch destroy check = (%+v, %t)", step, ok)
	}
	step, ok = NextExecutorRunStep(driver)
	if !ok || step.Kind != ExecutorRunStepAction || step.Action.Kind != ActionCheckDestroy {
		t.Fatalf("select destroy check = (%+v, %t)", step, ok)
	}
	destroy, ok := Checked(p, task.g, step.Action, true)
	if !ok || destroy.Kind != ActionDestroy || destroy.Handle != task.handle {
		t.Fatalf("physical destroy check = (%+v, %t)", destroy, ok)
	}
	if boundary, boundaryOK := NextExecutorRunStep(driver); boundaryOK || boundary != (ExecutorRunStep{}) {
		t.Fatalf("runner exposed boundary after Checked/ActionDestroy = (%+v, %t)", boundary, boundaryOK)
	}
	oldHandle := destroy.Handle
	releaseTestFrame(t, task.g, task.frame)
	receipt, ok := DestroyedBounded(p, task.g, destroy)
	if !ok || receipt.Kind != ActionCommitDestroy || receipt.Flags != ActionRetirePhysicalOwner ||
		!ActionRetiresPhysicalOwner(receipt) || receipt.Handle != nil ||
		!CommitExecutorRunAction(driver, task.g, receipt) {
		t.Fatalf("bounded destroy receipt = (%+v, %t)", receipt, ok)
	}
	if oldHandle == nil || task.g.root != nil || task.g.destroyTarget != nil || p.current != task.g ||
		p.action != receipt || p.readyHead != nil || p.readyTail != nil || task.g.queued ||
		task.g.runAction != ActionInvalid {
		t.Fatalf("post-destroy stable state retained handle/queue: old=%p root=%p target=%p current=%p action=%+v head=%p tail=%p queued=%t runAction=%d",
			oldHandle, task.g.root, task.g.destroyTarget, p.current, p.action, p.readyHead, p.readyTail,
			task.g.queued, task.g.runAction)
	}
	first, ok := NextExecutorRunStep(driver)
	if !ok || first.Kind != ExecutorRunStepDestroyCommit || first.Action != receipt {
		t.Fatalf("first stable receipt = (%+v, %t)", first, ok)
	}
	second, ok := NextExecutorRunStep(driver)
	if !ok || second != first {
		t.Fatalf("repeated stable receipt = (%+v, %t), first %+v", second, ok, first)
	}
	completed, ok := CommitExecutorRunDomainDestroy(driver, task.g, receipt)
	if !ok || completed.Kind != ActionComplete || completed.Flags != ActionRetirePhysicalOwner ||
		!ActionRetiresPhysicalOwner(completed) || completed.Handle != nil ||
		p.current != nil || p.action != (Action{}) || task.g.state != GDead ||
		task.g.runP != nil || driver.state != executorDriverActive {
		t.Fatalf("ordinary domain destroy commit = (%+v, %t), current=%p action=%+v state=%d runP=%p driver=%d",
			completed, ok, p.current, p.action, task.g.state, task.g.runP, driver.state)
	}
	if !EnterExecutorRunCompatibility(driver) {
		t.Fatal("enter executor compatibility after ordinary domain completion")
	}
	closeTestExecutorDriver(t, driver)
	runtime.KeepAlive(task.frame.memory)
}
