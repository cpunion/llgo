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
)

type timerParkOwnerFixture struct {
	p         *P
	driver    *ExecutorDriver
	registry  *ExecutorRegistry
	timers    *TimerRegistrationTable
	task      *yieldingTestG
	action    Action
	wait      WaitSetRecord
	ticket    ParkTicket
	timer     TimerRegistrationHandle
	operation OperationID
	executor  ExecutorHandle
}

func newTimerParkOwnerFixture(t *testing.T, name string, deadline int64) *timerParkOwnerFixture {
	return newTimerParkOwnerFixtureWithControl(t, name, deadline, 0, nil, 0)
}

func newControlledTimerParkOwnerFixture(
	t *testing.T,
	name string,
	deadline int64,
	controller uintptr,
	control *uint32,
	expected uint32,
) *timerParkOwnerFixture {
	return newTimerParkOwnerFixtureWithControl(t, name, deadline, controller, control, expected)
}

func newTimerParkOwnerFixtureWithControl(
	t *testing.T,
	name string,
	deadline int64,
	controller uintptr,
	control *uint32,
	expected uint32,
) *timerParkOwnerFixture {
	t.Helper()
	fixture := &timerParkOwnerFixture{p: new(P), task: newYieldingTestG(t, name)}
	fixture.driver, fixture.registry, fixture.timers, _ = bindTestExecutorDriverWithTimers(t, fixture.p)
	if !Enqueue(fixture.p, fixture.task.g) {
		t.Fatalf("enqueue %s", name)
	}
	if next, ok := NextRunnableAt(fixture.p, 0); !ok || next != fixture.task.g {
		t.Fatalf("dequeue %s = (%p, %t)", name, next, ok)
	}
	fixture.action = beginWaitTestResume(t, fixture.p, fixture.task)
	fixture.task.frame.header.SuspendReason = uint16(SuspendPark)
	fixture.task.frame.header.Lifecycle = uint16(FrameSuspended)
	driver, executor, route, current := CurrentExecutorTimerDriver(fixture.task.g)
	if !current || driver != fixture.driver {
		t.Fatalf("resolve %s Timer V2 owner = (%p, %+v, %d, %t)", name, driver, executor, route, current)
	}
	var prepared bool
	if control == nil {
		fixture.ticket, fixture.timer, fixture.operation, fixture.executor, prepared = PrepareCurrentExecutorTimerPark(
			driver,
			fixture.task.g,
			fixture.task.handle,
			fixture.task.frame.header,
			&fixture.wait,
			1,
			1,
			deadline,
		)
	} else {
		fixture.ticket, fixture.timer, fixture.operation, fixture.executor, prepared = PrepareCurrentExecutorControlledTimerPark(
			driver,
			fixture.task.g,
			fixture.task.handle,
			fixture.task.frame.header,
			&fixture.wait,
			1,
			1,
			deadline,
			controller,
			control,
			expected,
		)
	}
	if !prepared || fixture.executor != executor || fixture.operation.Route() != route {
		t.Fatalf("prepare %s Timer V2 park = (%+v, %+v, %+v, %+v, %t)",
			name, fixture.ticket, fixture.timer, fixture.operation, fixture.executor, prepared)
	}
	parked, ok := Resumed(fixture.p, fixture.task.g, fixture.action)
	if !ok || parked.Kind != ActionPark {
		t.Fatalf("commit %s Timer V2 park = (%+v, %t)", name, parked, ok)
	}
	return fixture
}

func (fixture *timerParkOwnerFixture) beginResume(t *testing.T) (ParkOutcome, uint32, OperationResultLease, TaskCancelKind) {
	return fixture.beginResumeAt(t, int64(^uint64(0)>>1))
}

func (fixture *timerParkOwnerFixture) beginResumeAt(t *testing.T, now int64) (ParkOutcome, uint32, OperationResultLease, TaskCancelKind) {
	t.Helper()
	if next, ok := NextRunnableAt(fixture.p, now); !ok || next != fixture.task.g {
		t.Fatalf("dequeue resumed %s = (%p, %t)", fixture.task.name, next, ok)
	}
	action, ok := BeginRunG(fixture.p, fixture.task.g)
	if !ok || action.Kind != ActionCheckResume {
		t.Fatalf("begin resumed %s = (%+v, %t)", fixture.task.name, action, ok)
	}
	fixture.action, ok = Checked(fixture.p, fixture.task.g, action, false)
	if !ok || fixture.action.Kind != ActionResume {
		t.Fatalf("activate resumed %s = (%+v, %t)", fixture.task.name, fixture.action, ok)
	}
	outcome, caseID, lease, cancel, taken := TakeRunDecision(fixture.task.g, fixture.ticket)
	if !taken {
		t.Fatalf("take %s Timer V2 decision", fixture.task.name)
	}
	return outcome, caseID, lease, cancel
}

func (fixture *timerParkOwnerFixture) finishShutdown(t *testing.T) {
	t.Helper()
	fixture.task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	fixture.task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(fixture.task.g, fixture.task.handle, fixture.task.frame.header) {
		t.Fatal("prepare Timer V2 shutdown completion")
	}
	action, ok := Resumed(fixture.p, fixture.task.g, fixture.action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume Timer V2 shutdown completion = (%+v, %t)", action, ok)
	}
	action, ok = Checked(fixture.p, fixture.task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check Timer V2 shutdown destroy = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, fixture.task.g, fixture.task.frame)
	action, ok = Destroyed(fixture.p, fixture.task.g, action)
	if !ok || action.Kind != ActionTerminalExecutorClose || action.Handle != nil {
		t.Fatalf("begin Timer V2 shutdown terminal close = (%+v, %t)", action, ok)
	}
	closed, terminal, ok := ConfirmTerminalExecutorClose(fixture.driver)
	if !ok || closed != fixture.task.g || terminal.Kind != ActionComplete || terminal.Handle != nil {
		t.Fatalf("confirm Timer V2 shutdown terminal close = (%p, %+v, %t)", closed, terminal, ok)
	}
	if !AcknowledgeTaskCancellation(fixture.task.g, TaskCancelShutdown) ||
		!fixture.timers.CanRelease() || !fixture.registry.CanRelease() {
		t.Fatal("Timer V2 shutdown cleanup retained task or source state")
	}
	runtime.KeepAlive(fixture.task.frame.memory)
}

func (fixture *timerParkOwnerFixture) finishPark(t *testing.T, lease OperationResultLease, discard bool) {
	t.Helper()
	driver, executor, route, current := CurrentExecutorTimerDriver(fixture.task.g)
	if !current || driver != fixture.driver || executor != fixture.executor || route != fixture.operation.Route() {
		t.Fatalf("resolve resumed %s Timer V2 source = (%p, %+v, %d, %t)",
			fixture.task.name, driver, executor, route, current)
	}
	stale := fixture.executor
	stale.Generation++
	if FinishCurrentExecutorTimerPark(
		driver, fixture.task.g, stale, fixture.timer, fixture.operation, lease, discard,
	) {
		t.Fatalf("%s Timer V2 cleanup accepted stale executor", fixture.task.name)
	}
	if !FinishCurrentExecutorTimerPark(
		driver, fixture.task.g, fixture.executor, fixture.timer, fixture.operation, lease, discard,
	) {
		t.Fatalf("finish %s Timer V2 cleanup", fixture.task.name)
	}
}

func (fixture *timerParkOwnerFixture) yieldCloseAndFinish(t *testing.T) {
	t.Helper()
	fixture.task.frame.header.SuspendReason = uint16(SuspendNone)
	fixture.task.frame.header.Lifecycle = uint16(FrameActive)
	yieldRunningDriverTask(t, fixture.p, fixture.task, fixture.action)
	closeTestExecutorDriver(t, fixture.driver)
	finishReadyDriverTasks(t, fixture.p, map[*G]*yieldingTestG{fixture.task.g: fixture.task})
	if !TerminalG(fixture.p, fixture.task.g) ||
		!fixture.timers.CanRelease() || !fixture.registry.CanRelease() {
		t.Fatalf("%s Timer V2 owner retained terminal state", fixture.task.name)
	}
	runtime.KeepAlive(fixture.task.frame.memory)
}

func TestCurrentExecutorTimerParkDueResumeRetiresExactSource(t *testing.T) {
	fixture := newTimerParkOwnerFixture(t, "timer-current-owner-due", 50)
	if timers, promoted, ok := PollExecutorAt(fixture.driver, 50); !ok || timers != 1 || promoted != 1 {
		t.Fatalf("publish current-owner timer deadline = (%d, %d, %t)", timers, promoted, ok)
	}
	if BeginExecutorClose(fixture.driver) {
		t.Fatal("closed Timer V2 owner before winner lease retirement")
	}
	outcome, caseID, lease, cancel := fixture.beginResume(t)
	if outcome != ParkOutcomeCompleted || caseID != 1 || !lease.Valid() || cancel != TaskCancelNone {
		t.Fatalf("current-owner timer decision = (%d, %d, %+v, %d)", outcome, caseID, lease, cancel)
	}
	fixture.finishPark(t, lease, false)
	fixture.yieldCloseAndFinish(t)
}

func TestCommandShutdownDrainResumesTimerCleanupBeforeExecutorClose(t *testing.T) {
	fixture := newTimerParkOwnerFixture(t, "timer-current-owner-shutdown", 70)
	if timers, promoted, ok := PollExecutorAt(fixture.driver, 70); !ok || timers != 1 || promoted != 1 {
		t.Fatalf("publish shutdown timer winner = (%d, %d, %t)", timers, promoted, ok)
	}
	main := &G{magic: gMagic, state: GDead}
	if needed, ok := RequestCommandShutdownDrain(fixture.p, main); !ok || !needed ||
		fixture.task.g.park.taskCancelKind != TaskCancelShutdown ||
		fixture.task.g.park.taskCancelPhase != taskCancelRequested {
		t.Fatalf("request Timer V2 shutdown drain = needed:%t ok:%t cancel:(%d,%d)",
			needed, ok, fixture.task.g.park.taskCancelKind, fixture.task.g.park.taskCancelPhase)
	}
	if BeginExecutorClose(fixture.driver) {
		t.Fatal("closed executor before Timer V2 shutdown resume cleanup")
	}
	outcome, caseID, lease, cancel := fixture.beginResume(t)
	if outcome != ParkOutcomeCanceled || caseID != 0 || !lease.Valid() || cancel != TaskCancelShutdown {
		t.Fatalf("shutdown timer decision = (%d, %d, %+v, %d)", outcome, caseID, lease, cancel)
	}
	fixture.finishPark(t, lease, true)
	fixture.finishShutdown(t)
}

func TestCommandShutdownDrainCancelsControlledTimerBeforeDeadline(t *testing.T) {
	control := uint32(17)
	fixture := newControlledTimerParkOwnerFixture(
		t, "timer-current-owner-controlled-shutdown", 250, uintptr(0x1234), &control, control,
	)
	main := &G{magic: gMagic, state: GDead}
	if needed, ok := RequestCommandShutdownDrain(fixture.p, main); !ok || !needed ||
		fixture.task.g.park.taskCancelKind != TaskCancelShutdown ||
		fixture.task.g.park.taskCancelPhase != taskCancelRequested {
		t.Fatalf("request controlled Timer V2 shutdown drain = needed:%t ok:%t cancel:(%d,%d)",
			needed, ok, fixture.task.g.park.taskCancelKind, fixture.task.g.park.taskCancelPhase)
	}
	outcome, caseID, lease, cancel := fixture.beginResumeAt(t, 0)
	if outcome != ParkOutcomeCanceled || caseID != 0 || lease.Valid() || cancel != TaskCancelShutdown {
		t.Fatalf("controlled shutdown timer decision = (%d, %d, %+v, %d)", outcome, caseID, lease, cancel)
	}
	fixture.finishPark(t, lease, true)
	fixture.finishShutdown(t)
}
