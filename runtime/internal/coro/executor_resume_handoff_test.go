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

type executorResumeHandoffFixture struct {
	p       *P
	driver  *ExecutorDriver
	task    *yieldingTestG
	resume  Action
	handoff ExecutorResumeHandoff
}

func beginExecutorResumeHandoffFixture(
	t *testing.T,
	p *P,
	driver *ExecutorDriver,
	task *yieldingTestG,
) *executorResumeHandoffFixture {
	t.Helper()
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue executor-resume handoff task")
	}
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok := Checked(p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume || resume.Handle != task.handle {
		t.Fatalf("check executor-resume handoff = (%+v, %t)", resume, ok)
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	if !EnterOSThreadLock(task.g) {
		t.Fatal("lock executor-resume handoff task to current M")
	}
	fixture := &executorResumeHandoffFixture{
		p:      p,
		driver: driver,
		task:   task,
		resume: resume,
	}
	if !DetachExecutorResume(
		&fixture.handoff,
		driver,
		task.g,
		ExecutorResumeHandoffLockedForeign,
	) {
		current, _, _, ownerOK := CurrentExecutorDriver(task.g)
		t.Fatalf(
			"detach executor resume: current=%p ownerOK=%t critical=%t issued=%d currentG=%p runP=%p lock=%p depth=%d action=%+v decision=%+v taken=%t budget=%d",
			current,
			ownerOK,
			enterCriticalContext(task.g),
			driver.run.issued,
			p.current,
			task.g.runP,
			p.osThreadLockOwner,
			task.g.osThreadLockDepth,
			p.action,
			p.runDecision,
			p.runDecisionTaken,
			p.servicePreemptBudget,
		)
	}
	return fixture
}

func (fixture *executorResumeHandoffFixture) restore(t *testing.T) {
	t.Helper()
	if !RestoreExecutorResume(&fixture.handoff) {
		t.Fatal("restore executor resume")
	}
	if fixture.handoff.Detached() ||
		fixture.task.g.state != GRunning ||
		fixture.p.current != fixture.task.g || !fixture.p.inResume ||
		fixture.p.action != fixture.resume || !fixture.p.runDecisionTaken ||
		fixture.p.servicePreemptBudget == 0 ||
		fixture.p.osThreadLockOwner != fixture.task.g ||
		fixture.driver.run.issued != ActionCheckResume {
		t.Fatalf(
			"restored executor resume invalid: detached=%t state=%d current=%p inResume=%t action=%+v taken=%t budget=%d lock=%p issued=%d",
			fixture.handoff.Detached(),
			fixture.task.g.state,
			fixture.p.current,
			fixture.p.inResume,
			fixture.p.action,
			fixture.p.runDecisionTaken,
			fixture.p.servicePreemptBudget,
			fixture.p.osThreadLockOwner,
			fixture.driver.run.issued,
		)
	}
}

func TestExecutorResumeHandoffRunsReplacementAndRestoresExactResume(t *testing.T) {
	p := new(P)
	driver, registry, executor := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "foreign-wait-owner")
	peer := newYieldingTestG(t, "foreign-wait-peer")
	fixture := beginExecutorResumeHandoffFixture(t, p, driver, task)
	if !Enqueue(p, peer.g) {
		t.Fatal("enqueue replacement peer")
	}

	if !fixture.handoff.Detached() ||
		task.g.state != GForeignWaiting || task.g.runP != p ||
		p.current != nil || p.inResume || p.action != (Action{}) ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken ||
		p.servicePreemptBudget != 0 || p.osThreadLockOwner != nil ||
		driver.run.issued != ActionInvalid ||
		!ExecutorResumeHandoffReturnable(driver) {
		t.Fatalf(
			"detached executor resume invalid: detached=%t state=%d runP=%p current=%p inResume=%t action=%+v decision=%+v taken=%t budget=%d lock=%p issued=%d returnable=%t",
			fixture.handoff.Detached(),
			task.g.state,
			task.g.runP,
			p.current,
			p.inResume,
			p.action,
			p.runDecision,
			p.runDecisionTaken,
			p.servicePreemptBudget,
			p.osThreadLockOwner,
			driver.run.issued,
			ExecutorResumeHandoffReturnable(driver),
		)
	}
	if DetachExecutorResume(
		&fixture.handoff,
		driver,
		task.g,
		ExecutorResumeHandoffLockedForeign,
	) {
		t.Fatal("detached the same active resume twice")
	}

	if result := registry.Request(executor); result != ExecutorRequestPublished {
		t.Fatalf("request replacement source transaction = %d", result)
	}
	sourceSteps := 0
	sourceUsed := uint32(0)
	for {
		sourceStep, advanced := NextExecutorRunStep(driver)
		if !advanced || sourceStep.Kind != ExecutorRunStepSource ||
			sourceStep.Poll.Used == 0 || sourceStep.Poll.Used > executorRunSourceBatchQuantum {
			t.Fatalf(
				"replacement source step %d = (%+v, %t)",
				sourceSteps,
				sourceStep,
				advanced,
			)
		}
		sourceSteps++
		sourceUsed += sourceStep.Poll.Used
		if sourceStep.Poll.Complete {
			break
		}
		if ExecutorResumeHandoffReturnable(driver) ||
			RestoreExecutorResume(&fixture.handoff) ||
			!fixture.handoff.Detached() {
			t.Fatal("returned across replacement source A/ack/B transaction")
		}
	}
	if want, budgetOK := MinExecutorPollBudget(driver); !budgetOK || sourceUsed != want ||
		!ExecutorResumeHandoffReturnable(driver) {
		t.Fatalf(
			"replacement source transaction = %d reductions in %d steps, want=(%d,%t), returnable=%t",
			sourceUsed,
			sourceSteps,
			want,
			budgetOK,
			ExecutorResumeHandoffReturnable(driver),
		)
	}

	step := runnerNextPhysicalAction(t, driver, peer, ActionCheckResume)
	if ExecutorResumeHandoffReturnable(driver) ||
		RestoreExecutorResume(&fixture.handoff) ||
		!fixture.handoff.Detached() {
		t.Fatal("returned across replacement M's issued physical action")
	}
	runnerYieldAction(t, driver, step, peer)
	if !ExecutorResumeHandoffReturnable(driver) {
		t.Fatal("replacement did not reach stable return boundary")
	}

	fixture.restore(t)
	if RestoreExecutorResume(&fixture.handoff) {
		t.Fatal("restored a consumed executor-resume record twice")
	}
	if !ExitOSThreadLock(task.g) {
		t.Fatal("restored G could not release its original M lock")
	}
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare restored task yield")
	}
	next, ok := Resumed(p, task.g, fixture.resume)
	if !ok || next.Kind != ActionYield ||
		!CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("commit restored task yield = (%+v, %t)", next, ok)
	}
	runtime.KeepAlive(task.frame.memory)
	runtime.KeepAlive(peer.frame.memory)
}

func TestExecutorResumeHandoffCompensationDemandGate(t *testing.T) {
	t.Run("quiescent", func(t *testing.T) {
		p := new(P)
		driver, _, _ := bindTestExecutorDriver(t, p)
		task := newYieldingTestG(t, "foreign-wait-quiescent")
		fixture := beginExecutorResumeHandoffFixture(t, p, driver, task)
		if required, ok := ExecutorResumeHandoffCompensationRequired(&fixture.handoff); !ok || required {
			t.Fatalf("quiescent compensation = (%t, %t), want false, true", required, ok)
		}
		fixture.restore(t)
	})

	t.Run("runnable", func(t *testing.T) {
		p := new(P)
		driver, _, _ := bindTestExecutorDriver(t, p)
		task := newYieldingTestG(t, "foreign-wait-runnable-owner")
		peer := newYieldingTestG(t, "foreign-wait-runnable-peer")
		fixture := beginExecutorResumeHandoffFixture(t, p, driver, task)
		if !Enqueue(p, peer.g) {
			t.Fatal("enqueue compensation peer")
		}
		if required, ok := ExecutorResumeHandoffCompensationRequired(&fixture.handoff); !ok || !required {
			t.Fatalf("runnable compensation = (%t, %t), want true, true", required, ok)
		}
		fixture.restore(t)
	})

	t.Run("request", func(t *testing.T) {
		p := new(P)
		driver, registry, executor := bindTestExecutorDriver(t, p)
		task := newYieldingTestG(t, "foreign-wait-request")
		fixture := beginExecutorResumeHandoffFixture(t, p, driver, task)
		if result := registry.Request(executor); result != ExecutorRequestPublished {
			t.Fatalf("publish compensation request = %d", result)
		}
		if required, ok := ExecutorResumeHandoffCompensationRequired(&fixture.handoff); !ok || !required {
			t.Fatalf("requested compensation = (%t, %t), want true, true", required, ok)
		}
		fixture.restore(t)
	})

	t.Run("task-control-endpoint", func(t *testing.T) {
		p := new(P)
		driver := new(ExecutorDriver)
		registry := new(ExecutorRegistry)
		control := new(TaskControlSource)
		executor := registerTestExecutor(t, registry)
		if !BindExecutorSourceCatalog(
			driver,
			p,
			registry,
			executor,
			ExecutorSourceCatalog{Control: control},
		) {
			t.Fatal("bind task-control compensation executor")
		}
		task := newYieldingTestG(t, "foreign-wait-control")
		if !Enqueue(p, task.g) {
			t.Fatal("enqueue task-control compensation owner")
		}
		step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
		resume, checked := Checked(p, task.g, step.Action, false)
		if !checked || resume.Kind != ActionResume || resume.Handle != task.handle {
			t.Fatalf("check task-control compensation resume = (%+v, %t)", resume, checked)
		}
		takeNormalRunnerDecision(t, task.g)
		task.frame.header.SuspendReason = uint16(SuspendNone)
		task.frame.header.Lifecycle = uint16(FrameActive)
		id, registered := RegisterCurrentExecutorTaskControl(driver, task.g)
		if !registered || !EnterOSThreadLock(task.g) {
			t.Fatal("register task-control compensation endpoint")
		}
		fixture := &executorResumeHandoffFixture{
			p: p, driver: driver, task: task, resume: resume,
		}
		if !DetachExecutorResume(
			&fixture.handoff,
			driver,
			task.g,
			ExecutorResumeHandoffLockedForeign,
		) {
			t.Fatal("detach task-control compensation owner")
		}
		if required, ok := ExecutorResumeHandoffCompensationRequired(&fixture.handoff); !ok || !required {
			t.Fatalf("task-control compensation = (%t, %t), want true, true", required, ok)
		}
		fixture.restore(t)
		if !BeginCloseCurrentExecutorTaskControl(driver, task.g, id) ||
			!FinishCloseCurrentExecutorTaskControl(driver, task.g, id) {
			t.Fatal("close task-control compensation endpoint")
		}
	})
}

func TestExecutorResumeHandoffPreservesInlineAwaitAncestry(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "inline-foreign-owner")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue inline handoff task")
	}
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok := Checked(p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume || resume.Handle != task.handle {
		t.Fatalf("check inline handoff root = (%+v, %t)", resume, ok)
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendCall)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	child := newTestFrame(t, task.g, unsafe.Pointer(new(byte)), task.handle)
	if !PrepareAwaitCompletion(task.g, task.handle, child.handle) ||
		BeginInlineAwait(task.g, task.handle, child.handle) != InlineAwaitStarted {
		t.Fatal("begin inline handoff child")
	}
	if outcome, caseID, lease, cancel, taken := TakeRunDecision(task.g, ParkTicket{}); !taken ||
		outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) ||
		cancel != TaskCancelNone {
		t.Fatal("take inline handoff child initial gate")
	}
	child.header.SuspendReason = uint16(SuspendNone)
	child.header.Lifecycle = uint16(FrameActive)

	var handoff ExecutorResumeHandoff
	if !DetachExecutorResume(
		&handoff, driver, task.g, ExecutorResumeHandoffSameMForeign,
	) {
		t.Fatal("detach inline handoff child")
	}
	contextTask, contextHandle, contextOK := ExecutorResumeHandoffContext(&handoff)
	if !contextOK || contextTask != task.g || contextHandle != child.handle ||
		handoff.inlineAwaitDepth != 1 || p.inlineAwaitDepth != 0 {
		t.Fatalf("detached inline ancestry = (task:%p handle:%p ok:%t saved:%d live:%d)",
			contextTask, contextHandle, contextOK, handoff.inlineAwaitDepth, p.inlineAwaitDepth)
	}
	if !RestoreExecutorResume(&handoff) || p.inlineAwaitDepth != 1 ||
		p.current != task.g || task.g.active != FrameFromStorage(child.storage) {
		t.Fatalf("restore inline ancestry: depth=%d current=%p active=%p",
			p.inlineAwaitDepth, p.current, task.g.active)
	}

	child.header.SuspendReason = uint16(SuspendYield)
	child.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, child.handle, child.header) ||
		FinishInlineAwait(task.g, task.handle, child.handle, false) != InlineAwaitSuspend {
		t.Fatal("unwind restored inline child")
	}
	next, resumed := Resumed(p, task.g, resume)
	if !resumed || next.Kind != ActionYield ||
		!CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("commit restored inline yield = (%+v, %t)", next, resumed)
	}
	runtime.KeepAlive(task.frame.memory)
	runtime.KeepAlive(child.memory)
}

func TestExecutorResumeHandoffSettlesIssuedReadyDebt(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "foreign-ready-debt-owner")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue ready-debt handoff task")
	}
	driver.run.readyDebt = true
	driver.run.blocked = true
	driver.run.actionsSinceSource = 7
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok := Checked(p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check ready-debt handoff resume")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	if !EnterOSThreadLock(task.g) {
		t.Fatal("lock ready-debt handoff task")
	}
	var handoff ExecutorResumeHandoff
	if !DetachExecutorResume(
		&handoff,
		driver,
		task.g,
		ExecutorResumeHandoffLockedForeign,
	) {
		t.Fatal("detach ready-debt handoff task")
	}
	route, routeOK := driver.Route()
	if driver.run.readyDebt || driver.run.blocked ||
		driver.run.actionsSinceSource != 7 ||
		!routeOK || route != RouteID(1) ||
		!ExecutorResumeHandoffReturnable(driver) {
		t.Fatalf(
			"detached ready debt = (ready:%t blocked:%t actions:%d route:%d routeOK:%t returnable:%t)",
			driver.run.readyDebt,
			driver.run.blocked,
			driver.run.actionsSinceSource,
			route,
			routeOK,
			ExecutorResumeHandoffReturnable(driver),
		)
	}
	if !RestoreExecutorResume(&handoff) {
		t.Fatal("restore ready-debt handoff task")
	}
	if !ExitOSThreadLock(task.g) {
		t.Fatal("unlock restored ready-debt handoff task")
	}
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare restored ready-debt task yield")
	}
	next, resumed := Resumed(p, task.g, resume)
	if !resumed || next.Kind != ActionYield ||
		!CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("commit restored ready-debt task yield = (%+v, %t)", next, resumed)
	}
	if driver.run.actionsSinceSource != 8 {
		t.Fatalf(
			"restored ready-debt action count = %d, want one deferred commit",
			driver.run.actionsSinceSource,
		)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorResumeHandoffCompletesLastVisibleReplacementChildWithoutClosingDomain(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	owner := newYieldingTestG(t, "foreign-wait-owner")
	child := newYieldingTestG(t, "replacement-child")
	fixture := beginExecutorResumeHandoffFixture(t, p, driver, owner)
	if !Enqueue(p, child.g) {
		t.Fatal("enqueue replacement child")
	}

	target := queueRunnerCheckDestroy(t, driver, child)
	step := runnerNextPhysicalAction(t, driver, child, ActionCheckDestroy)
	destroy, ok := Checked(p, child.g, step.Action, true)
	if !ok || destroy.Kind != ActionDestroy || child.g.destroyTarget != target {
		t.Fatalf("check replacement child destroy = (%+v, %t)", destroy, ok)
	}
	releaseTestFrame(t, child.g, child.frame)
	receipt, ok := DestroyedBounded(p, child.g, destroy)
	if !ok || receipt.Kind != ActionCommitDestroy ||
		!CommitExecutorRunAction(driver, child.g, receipt) {
		t.Fatalf("publish replacement child destroy = (%+v, %t)", receipt, ok)
	}
	if !fixture.handoff.Detached() ||
		owner.g.state != GForeignWaiting || owner.g.runP != p ||
		ExecutorResumeHandoffReturnable(driver) ||
		driver.state != executorDriverActive {
		t.Fatalf(
			"replacement child receipt disturbed detached owner: detached=%t state=%d runP=%p returnable=%t driver=%d",
			fixture.handoff.Detached(),
			owner.g.state,
			owner.g.runP,
			ExecutorResumeHandoffReturnable(driver),
			driver.state,
		)
	}
	completed, ok := CommitExecutorRunDomainDestroy(driver, child.g, receipt)
	if !ok || completed.Kind != ActionComplete ||
		driver.state != executorDriverActive || child.g.state != GDead ||
		!fixture.handoff.Detached() || owner.g.state != GForeignWaiting ||
		!ExecutorResumeHandoffReturnable(driver) {
		t.Fatalf(
			"commit replacement child destroy = (%+v, %t), driver=%d child=%d detached=%t owner=%d returnable=%t",
			completed,
			ok,
			driver.state,
			child.g.state,
			fixture.handoff.Detached(),
			owner.g.state,
			ExecutorResumeHandoffReturnable(driver),
		)
	}

	fixture.restore(t)
	if !ExitOSThreadLock(owner.g) {
		t.Fatal("restored owner could not release its original M lock")
	}
	owner.frame.header.SuspendReason = uint16(SuspendYield)
	owner.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(owner.g, owner.handle, owner.frame.header) {
		t.Fatal("prepare restored owner yield")
	}
	next, ok := Resumed(p, owner.g, fixture.resume)
	if !ok || next.Kind != ActionYield ||
		!CommitExecutorRunAction(driver, owner.g, next) {
		t.Fatalf("commit restored owner yield = (%+v, %t)", next, ok)
	}
	runtime.KeepAlive(owner.frame.memory)
	runtime.KeepAlive(child.frame.memory)
}

func TestExecutorResumeHandoffKeepsRegisteredCancellationSticky(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	control := new(TaskControlSource)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(
		driver,
		p,
		registry,
		handle,
		ExecutorSourceCatalog{Control: control},
	) {
		t.Fatal("bind task-control executor")
	}
	task := newYieldingTestG(t, "foreign-wait-cancel")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue task-control handoff task")
	}
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok := Checked(p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check task-control handoff resume")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	id, ok := RegisterCurrentExecutorTaskControl(driver, task.g)
	if !ok {
		t.Fatalf(
			"register active task-control endpoint: running=%t owner=%t sourceOwner=%t source=%p",
			validRunningExecutorOwner(driver),
			pOwnsTaskCancellation(p, task.g),
			validTaskControlOwner(control, p),
			driver.sources.control,
		)
	}
	if !EnterOSThreadLock(task.g) {
		t.Fatal("lock task-control handoff task")
	}
	fixture := &executorResumeHandoffFixture{
		p:      p,
		driver: driver,
		task:   task,
		resume: resume,
	}
	if !DetachExecutorResume(
		&fixture.handoff,
		driver,
		task.g,
		ExecutorResumeHandoffLockedForeign,
	) {
		t.Fatal("detach task-control executor resume")
	}

	if posted := control.Post(id, TaskCancelAbort); posted != TaskControlPosted {
		t.Fatalf("post detached task cancellation = %d", posted)
	}
	delivered, discarded, ok := control.PublishPass(p)
	if !ok || delivered != 1 || discarded != 0 {
		t.Fatalf(
			"publish detached task cancellation = (%d, %d, %t)",
			delivered,
			discarded,
			ok,
		)
	}
	if kind, pending := TaskCancellationOf(p, task.g); !pending || kind != TaskCancelAbort {
		t.Fatalf("detached task cancellation = (%d, %t)", kind, pending)
	}

	fixture.restore(t)
	if kind, claimed := ClaimTaskCancellation(p, task.g); !claimed || kind != TaskCancelAbort {
		t.Fatalf("claim restored task cancellation = (%d, %t)", kind, claimed)
	}
	if !BeginCloseTaskControl(control, p, id) ||
		!ConfirmTaskControlQuiesced(control, p, id) ||
		!RetireTaskControl(control, p, id) {
		t.Fatal("close restored task-control endpoint")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorResumeHandoffRejectsNonLockedAndMalformedRestore(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "foreign-wait-reject")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue rejected handoff task")
	}
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok := Checked(p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check rejected handoff resume")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	var handoff ExecutorResumeHandoff
	if DetachExecutorResume(
		&handoff,
		driver,
		task.g,
		ExecutorResumeHandoffLockedForeign,
	) ||
		handoff.Detached() || task.g.state != GRunning {
		t.Fatal("detached an unlocked active resume")
	}
	if !EnterOSThreadLock(task.g) ||
		!DetachExecutorResume(
			&handoff,
			driver,
			task.g,
			ExecutorResumeHandoffLockedForeign,
		) {
		current, _, _, ownerOK := CurrentExecutorDriver(task.g)
		t.Fatalf(
			"detach exact locked resume: current=%p ownerOK=%t critical=%t issued=%d currentG=%p runP=%p lock=%p depth=%d action=%+v decision=%+v taken=%t budget=%d",
			current,
			ownerOK,
			enterCriticalContext(task.g),
			driver.run.issued,
			p.current,
			task.g.runP,
			p.osThreadLockOwner,
			task.g.osThreadLockDepth,
			p.action,
			p.runDecision,
			p.runDecisionTaken,
			p.servicePreemptBudget,
		)
	}
	saved := task.g.active.header.Lifecycle
	task.g.active.header.Lifecycle = uint16(FrameSuspended)
	if RestoreExecutorResume(&handoff) || !handoff.Detached() ||
		task.g.state != GForeignWaiting {
		t.Fatal("malformed active frame was restored or record consumed")
	}
	task.g.active.header.Lifecycle = saved
	if !RestoreExecutorResume(&handoff) {
		t.Fatal("restore corrected active frame")
	}
	runtime.KeepAlive(task.frame.memory)
}
