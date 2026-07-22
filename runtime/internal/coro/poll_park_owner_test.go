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

type pollParkOwnerFixture struct {
	p         *P
	driver    *ExecutorDriver
	registry  *ExecutorRegistry
	poll      *PollOperationSource
	task      *yieldingTestG
	action    Action
	wait      WaitSetRecord
	ticket    ParkTicket
	handle    PollOperationHandle
	operation OperationID
	executor  ExecutorHandle
}

func newPollParkOwnerFixture(t *testing.T, name string, deadline int64) *pollParkOwnerFixture {
	t.Helper()
	fixture := &pollParkOwnerFixture{
		p:        new(P),
		driver:   new(ExecutorDriver),
		registry: new(ExecutorRegistry),
		poll:     new(PollOperationSource),
		task:     newYieldingTestG(t, name),
	}
	registered := registerTestExecutor(t, fixture.registry)
	if !BindExecutorSourceCatalogAtRoute(
		fixture.driver,
		fixture.p,
		fixture.registry,
		registered,
		RouteID(7),
		ExecutorSourceCatalog{Poll: fixture.poll},
	) {
		t.Fatalf("bind %s poll owner", name)
	}
	if !Enqueue(fixture.p, fixture.task.g) {
		t.Fatalf("enqueue %s", name)
	}
	if next, ok := NextRunnableAt(fixture.p, 0); !ok || next != fixture.task.g {
		t.Fatalf("dequeue %s = (%p, %t)", name, next, ok)
	}
	fixture.action = beginWaitTestResume(t, fixture.p, fixture.task)
	fixture.task.frame.header.SuspendReason = uint16(SuspendPark)
	fixture.task.frame.header.Lifecycle = uint16(FrameSuspended)
	driver, executor, route, current := CurrentExecutorPollDriver(fixture.task.g)
	if !current || driver != fixture.driver || route != RouteID(7) {
		t.Fatalf("resolve %s Poll V2 owner = (%p, %+v, %d, %t)", name, driver, executor, route, current)
	}
	var prepared bool
	fixture.ticket, fixture.handle, fixture.operation, fixture.executor, prepared = PrepareCurrentExecutorPollPark(
		driver,
		fixture.task.g,
		fixture.task.handle,
		fixture.task.frame.header,
		&fixture.wait,
		1,
		1,
		42,
		PollInterestRead,
		deadline,
	)
	if !prepared || fixture.executor != executor || fixture.operation.Route() != route {
		t.Fatalf("prepare %s Poll V2 park = (%+v, %+v, %+v, %+v, %t)",
			name, fixture.ticket, fixture.handle, fixture.operation, fixture.executor, prepared)
	}
	parked, ok := Resumed(fixture.p, fixture.task.g, fixture.action)
	if !ok || parked.Kind != ActionPark {
		t.Fatalf("commit %s Poll V2 park = (%+v, %t)", name, parked, ok)
	}
	return fixture
}

func (fixture *pollParkOwnerFixture) beginResume(t *testing.T, now int64) (ParkOutcome, uint32, OperationResultLease, TaskCancelKind) {
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
	fixture.task.frame.header.SuspendReason = uint16(SuspendPark)
	fixture.task.frame.header.Lifecycle = uint16(FrameSuspended)
	outcome, caseID, lease, cancel, taken := TakeRunDecision(fixture.task.g, fixture.ticket)
	if !taken {
		t.Fatalf("take %s Poll V2 decision", fixture.task.name)
	}
	return outcome, caseID, lease, cancel
}

func (fixture *pollParkOwnerFixture) finishPark(
	t *testing.T,
	lease OperationResultLease,
	discard bool,
) PollOperationResult {
	t.Helper()
	driver, executor, route, current := CurrentExecutorPollDriver(fixture.task.g)
	if !current || driver != fixture.driver || executor != fixture.executor || route != fixture.operation.Route() {
		t.Fatalf("resolve resumed %s Poll V2 source = (%p, %+v, %d, %t)",
			fixture.task.name, driver, executor, route, current)
	}
	stale := fixture.executor
	stale.Generation++
	if result, ok := FinishCurrentExecutorPollPark(
		driver, fixture.task.g, stale, fixture.handle, fixture.operation, lease, discard,
	); ok || result != PollOperationResultInvalid {
		t.Fatalf("%s Poll V2 cleanup accepted stale executor", fixture.task.name)
	}
	result, ok := FinishCurrentExecutorPollPark(
		driver, fixture.task.g, fixture.executor, fixture.handle, fixture.operation, lease, discard,
	)
	if !ok {
		t.Fatalf("finish %s Poll V2 cleanup", fixture.task.name)
	}
	return result
}

func (fixture *pollParkOwnerFixture) yieldCloseAndFinish(t *testing.T) {
	t.Helper()
	fixture.task.frame.header.SuspendReason = uint16(SuspendNone)
	fixture.task.frame.header.Lifecycle = uint16(FrameActive)
	yieldRunningDriverTask(t, fixture.p, fixture.task, fixture.action)
	closeTestExecutorDriver(t, fixture.driver)
	finishReadyDriverTasks(t, fixture.p, map[*G]*yieldingTestG{fixture.task.g: fixture.task})
	if !TerminalG(fixture.p, fixture.task.g) ||
		!fixture.poll.CanRelease() || !fixture.registry.CanRelease() {
		t.Fatalf("%s Poll V2 owner retained terminal state", fixture.task.name)
	}
	runtime.KeepAlive(fixture.task.frame.memory)
}

func TestCurrentExecutorPollParkEventTimeoutAndClosing(t *testing.T) {
	tests := []struct {
		name     string
		deadline int64
		event    PollOperationResult
		now      int64
		want     PollOperationResult
	}{
		{name: "ready", event: PollOperationReady, want: PollOperationReady},
		{name: "closing", event: PollOperationClosing, want: PollOperationClosing},
		{name: "timeout", deadline: 50, now: 50, want: PollOperationTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPollParkOwnerFixture(t, "poll-current-owner-"+test.name, test.deadline)
			wrongRoute, _ := MakeOperationIDAtRoute(
				OperationSourcePoll,
				RouteID(6),
				fixture.operation.LocalSlot(),
				fixture.operation.Generation,
			)
			if PostExecutorPollEvent(fixture.driver, wrongRoute, PollOperationReady) != PollOperationPostInvalid ||
				PostExecutorPollEvent(new(ExecutorDriver), fixture.operation, PollOperationReady) != PollOperationPostInvalid {
				t.Fatal("poll retained reactor accepted wrong executor/route")
			}
			if test.event != PollOperationResultInvalid &&
				PostExecutorPollEvent(fixture.driver, fixture.operation, test.event) != PollOperationPosted {
				t.Fatal("post retained poll event")
			}
			if timers, promoted, ok := PollExecutorAt(fixture.driver, test.now); !ok ||
				timers != 0 || promoted != 1 {
				t.Fatalf("poll retained event = (%d, %d, %t)", timers, promoted, ok)
			}
			if BeginExecutorClose(fixture.driver) {
				t.Fatal("closed Poll V2 owner before winner lease retirement")
			}
			outcome, caseID, lease, cancel := fixture.beginResume(t, test.now)
			if outcome != ParkOutcomeCompleted || caseID != 1 || !lease.Valid() || cancel != TaskCancelNone {
				t.Fatalf("current-owner poll decision = (%d, %d, %+v, %d)", outcome, caseID, lease, cancel)
			}
			if result := fixture.finishPark(t, lease, false); result != test.want {
				t.Fatalf("current-owner poll result = %d, want %d", result, test.want)
			}
			fixture.yieldCloseAndFinish(t)
		})
	}
}

func TestCommandShutdownDrainAwaitsAdmittedPollV2Producer(t *testing.T) {
	fixture := newPollParkOwnerFixture(t, "poll-current-owner-shutdown", 0)
	slot, ok := pollOperationSlotAt(fixture.poll, fixture.operation.LocalSlot()-1)
	if !ok || !producerAdmissionAcquire(&slot.v2Producer.inflight) {
		t.Fatal("admit retained poll producer before shutdown")
	}
	main := &G{magic: gMagic, state: GDead}
	if needed, ok := RequestCommandShutdownDrain(fixture.p, main); !ok || !needed {
		t.Fatalf("request Poll V2 command shutdown = (%t, %t)", needed, ok)
	}
	if timers, promoted, ok := PollExecutorAt(fixture.driver, 0); !ok ||
		timers != 0 || promoted != 0 || fixture.wait.work != waitSetWorkAwaitingExternal {
		t.Fatalf("shutdown must await poll producer = (%d, %d, %t), work=%d",
			timers, promoted, ok, fixture.wait.work)
	}
	if BeginExecutorClose(fixture.driver) {
		t.Fatal("closed executor while Poll V2 producer admission remained live")
	}
	if !preemptCompareAndSwap(
		&slot.v2Mailbox,
		uint32(pollOperationMailboxEmpty),
		uint32(pollOperationMailboxPosting),
	) {
		t.Fatal("claim admitted retained poll mailbox")
	}
	slot.v2Result = PollOperationReady
	preemptStore(&slot.v2Mailbox, uint32(pollOperationMailboxPosted))
	preemptStore(&fixture.poll.pending, 1)
	producerAdmissionRelease(&slot.v2Producer.inflight)
	if timers, promoted, ok := PollExecutorAt(fixture.driver, 0); !ok ||
		timers != 0 || promoted != 1 {
		t.Fatalf("finish retained poll shutdown fact = (%d, %d, %t)", timers, promoted, ok)
	}
	outcome, caseID, lease, cancel := fixture.beginResume(t, 0)
	if outcome != ParkOutcomeCanceled || caseID != 0 || lease.Valid() || cancel != TaskCancelShutdown {
		t.Fatalf("shutdown poll decision = (%d, %d, %+v, %d)", outcome, caseID, lease, cancel)
	}
	if result := fixture.finishPark(t, lease, true); result != PollOperationResultInvalid {
		t.Fatalf("canceled poll cleanup exposed result %d", result)
	}

	fixture.task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	fixture.task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(fixture.task.g, fixture.task.handle, fixture.task.frame.header) {
		t.Fatal("prepare Poll V2 shutdown completion")
	}
	action, ok := Resumed(fixture.p, fixture.task.g, fixture.action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume Poll V2 shutdown completion = (%+v, %t)", action, ok)
	}
	action, ok = Checked(fixture.p, fixture.task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check Poll V2 shutdown destroy = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, fixture.task.g, fixture.task.frame)
	action, ok = Destroyed(fixture.p, fixture.task.g, action)
	if !ok || action.Kind != ActionTerminalExecutorClose || action.Handle != nil {
		t.Fatalf("begin Poll V2 shutdown terminal close = (%+v, %t)", action, ok)
	}
	closed, terminal, ok := ConfirmTerminalExecutorClose(fixture.driver)
	if !ok || closed != fixture.task.g || terminal.Kind != ActionComplete || terminal.Handle != nil {
		t.Fatalf("confirm Poll V2 shutdown terminal close = (%p, %+v, %t)", closed, terminal, ok)
	}
	if !AcknowledgeTaskCancellation(fixture.task.g, TaskCancelShutdown) ||
		!fixture.poll.CanRelease() || !fixture.registry.CanRelease() {
		t.Fatal("Poll V2 shutdown cleanup retained task or source state")
	}
	runtime.KeepAlive(fixture.task.frame.memory)
}
