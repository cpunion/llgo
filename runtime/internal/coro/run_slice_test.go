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
	if pauseExecutorRunAction(p, task.g, action, false) {
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
