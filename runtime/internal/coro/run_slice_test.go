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
	resume, ok := Checked(driver.p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume || resume.Handle != task.handle {
		t.Fatalf("runner yield check = (%+v, %t)", resume, ok)
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare runner yield")
	}
	next, ok := Resumed(driver.p, task.g, resume)
	if !ok || next.Kind != ActionYield || !CommitExecutorRunAction(driver, task.g, next) {
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
	driver, _, _, _ := bindTestExecutorDriver(t, p)
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

func TestExecutorRunCommandBootstrapDirectChildHandoffPrecedesTwoPeers(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriver(t, p)
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
	driver, _, _, _ := bindTestExecutorDriver(t, p)
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
		task.g.queued || task.g.nextReady != nil || p.servicePreemptBudget != servicePreemptPollBudget {
		t.Fatalf("failed pause partially committed: current=%p action=%+v head=%p tail=%p state=%d runP=%p runAction=%d queued=%t budget=%d",
			p.current, p.action, p.readyHead, p.readyTail, task.g.state, task.g.runP,
			task.g.runAction, task.g.queued, p.servicePreemptBudget)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunDispatchFailureRestoresReadyHead(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriver(t, p)
	a := newYieldingTestG(t, "dispatch-atomic-a")
	b := newYieldingTestG(t, "dispatch-atomic-b")
	if !Enqueue(p, a.g) || !Enqueue(p, b.g) {
		t.Fatal("enqueue dispatch atomic tasks")
	}

	// Corrupt only the selected element, leaving the O(1) queue header valid.
	// The bounded dispatcher must fail closed without silently dropping it.
	a.g.state = GWaiting
	if step, ok := NextExecutorRunStep(driver); ok || step != (ExecutorRunStep{}) {
		t.Fatalf("invalid ready head dispatched = (%+v, %t)", step, ok)
	}
	if p.readyHead != a.g || p.readyTail != b.g || !a.g.queued || a.g.nextReady != b.g ||
		!b.g.queued || b.g.nextReady != nil || p.current != nil || p.action != (Action{}) {
		t.Fatalf("failed dispatch mutated queue: head=%p tail=%p a={queued:%t next:%p} b={queued:%t next:%p} current=%p action=%+v",
			p.readyHead, p.readyTail, a.g.queued, a.g.nextReady, b.g.queued, b.g.nextReady, p.current, p.action)
	}
	runtime.KeepAlive(a.frame.memory)
	runtime.KeepAlive(b.frame.memory)
}

func TestExecutorRunStartedEpochPrecedesReadyAndHotSourceAlternates(t *testing.T) {
	p := new(P)
	driver, registry, _, handle := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "source-fair")
	if !Enqueue(p, task.g) || registry.Request(handle) != ExecutorRequestPublished {
		t.Fatal("prepare hot source runner")
	}

	sourceSteps := uint32(0)
	for {
		step, ok := NextExecutorRunStep(driver)
		if !ok || step.Kind != ExecutorRunStepSource || step.Poll.Used != 1 {
			t.Fatalf("started epoch step %d = (%+v, %t)", sourceSteps, step, ok)
		}
		sourceSteps++
		if step.Poll.Complete {
			break
		}
		if p.current != nil || p.readyHead != task.g {
			t.Fatal("ready G interrupted a started A/ack/B epoch")
		}
	}
	if want, ok := MinExecutorPollBudget(driver); !ok || sourceSteps != want {
		t.Fatalf("budget-one source transaction used %d, want (%d, %t)", sourceSteps, want, ok)
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
	if !ok || step.Kind != ExecutorRunStepSource || step.Poll.Used != 1 {
		t.Fatalf("hot source did not alternate after one G action = (%+v, %t)", step, ok)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorRunCursorRejectsImplicitLegacySwitch(t *testing.T) {
	p := new(P)
	driver, registry, _, handle := bindTestExecutorDriver(t, p)
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
				driver, registry, _, _, handle = bindTestExecutorDriverWithTimers(t, p)
			} else {
				driver, registry, _, handle = bindTestExecutorDriver(t, p)
			}
			task := newYieldingTestG(t, "poll-slice-cursor")
			if !Enqueue(p, task.g) || registry.Request(handle) != ExecutorRequestPublished {
				t.Fatal("prepare mixed hot-source/ready-debt cursor")
			}

			requestedBehindA := false
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
				if !requestedBehindA && driver.poll.phase >= executorPollEpochBPublish {
					if registry.Request(handle) != ExecutorRequestPublished {
						t.Fatal("publish hot source behind acknowledged epoch A")
					}
					requestedBehindA = true
				}
				if step.Poll.Complete {
					break
				}
			}
			if !requestedBehindA || !driver.run.sourceMore || !driver.run.readyDebt ||
				driver.poll != (executorPollTransaction{}) || p.readyHead != task.g {
				t.Fatalf("mixed cursor precondition = requested:%t run:%+v poll:%+v head:%p",
					requestedBehindA, driver.run, driver.poll, p.readyHead)
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
			waits := new(WaitRegistrationTable)
			control := new(TaskControlSource)
			handle := registerTestExecutor(t, registry)
			if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{Waits: waits, Control: control}) {
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
	driver, _, _, _ := bindTestExecutorDriver(t, p)
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
	driver, _, _, _ := bindTestExecutorDriver(t, p)
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
	driver, _, _, _ := bindTestExecutorDriver(t, p)
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
	driver, _, _, _ := bindTestExecutorDriver(t, p)
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
	driver, _, _, _ := bindTestExecutorDriver(t, p)
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
	if !ok || receipt.Kind != ActionCommitDestroy || receipt.Handle != nil ||
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
	runtime.KeepAlive(task.frame.memory)
}
