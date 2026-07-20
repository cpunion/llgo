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

import "testing"

func TestWorkerParkOwnerPrepareCompleteAndFinish(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	workers := new(WorkerOperationSource)
	executor := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, executor, ExecutorSourceCatalog{Waits: waits, Worker: workers}) {
		t.Fatal("bind worker owner catalog")
	}
	task := newYieldingTestG(t, "worker-owner")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue worker owner task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue worker owner task")
	}
	action := beginWaitTestResume(t, p, task)
	var wait WaitSetRecord
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	current, currentHandle, currentRoute, currentOK := CurrentExecutorWorkerDriver(task.g)
	if !currentOK || current != driver || currentHandle != executor || currentRoute != RouteID(1) {
		t.Fatalf("resolve current worker owner = (%p, %+v, %d, %t)", current, currentHandle, currentRoute, currentOK)
	}
	ticket, id, ok := PrepareCurrentExecutorWorkerPark(
		driver, task.g, task.handle, task.frame.header, &wait, 31, 79,
	)
	if !ok || !CommitCurrentExecutorWorkerSubmission(driver, task.g, id) {
		t.Fatal("prepare worker owner park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit worker owner park = (%+v, %t)", action, ok)
	}
	payload := workerPayloadForTest(t, 11, 111, 222, 0)
	if workers.Post(id, payload) != WorkerOperationPosted {
		t.Fatal("post worker owner result")
	}
	var complete ExecutorPollProgress
	for entries := 0; entries < 1000; entries++ {
		progress, ok := PollExecutorSlice(driver, 1)
		if !ok || progress.Used != 1 {
			t.Fatalf("bounded worker owner poll %d = (%+v, %t)", entries, progress, ok)
		}
		if progress.Complete {
			complete = progress
			break
		}
	}
	if !complete.Complete || complete.Worker != 1 || complete.WorkerLost != 0 ||
		complete.Completed != 1 || complete.Promoted != 1 || complete.ApplyVisits != 1 {
		t.Fatalf("complete bounded worker owner poll = %+v", complete)
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue completed worker owner")
	}
	action = beginWaitTestResume(t, p, task)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(task.g, ticket)
	if !ok || outcome != ParkOutcomeCompleted || caseID != 31 || !lease.Valid() || taskCancel != TaskCancelNone {
		t.Fatalf("take worker owner decision = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, ok)
	}
	var got ScalarResultPayloadV1
	if !FinishCurrentExecutorWorkerPark(driver, task.g, id, lease, false, &got) || got != payload {
		t.Fatalf("finish worker owner result = %+v, want %+v", got, payload)
	}
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare worker owner completion")
	}
	action, ok = Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume worker owner completion = (%+v, %t)", action, ok)
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check worker owner destroy = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	closeAction, ok := Destroyed(p, task.g, action)
	if !ok || closeAction.Kind != ActionTerminalExecutorClose || closeAction.Handle != nil {
		t.Fatalf("begin worker owner terminal close = (%+v, %t)", closeAction, ok)
	}
	closed, terminal, ok := ConfirmTerminalExecutorClose(driver)
	if !ok || closed != task.g || terminal.Kind != ActionComplete || terminal.Handle != nil {
		t.Fatalf("confirm worker owner terminal close = (%p, %+v, %t)", closed, terminal, ok)
	}
	if !workers.CanRelease() || !waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("release worker owner catalog")
	}
}

func TestCommandShutdownDrainAwaitsSubmittedWorkerCompletion(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	workers := new(WorkerOperationSource)
	executor := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, executor, ExecutorSourceCatalog{Waits: waits, Worker: workers}) {
		t.Fatal("bind command-drain worker catalog")
	}
	task := newYieldingTestG(t, "worker-command-drain")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue command-drain worker task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue command-drain worker task")
	}
	action := beginWaitTestResume(t, p, task)
	var wait WaitSetRecord
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	ticket, id, ok := PrepareCurrentExecutorWorkerPark(
		driver, task.g, task.handle, task.frame.header, &wait, 43, 83,
	)
	if !ok || !CommitCurrentExecutorWorkerSubmission(driver, task.g, id) {
		t.Fatal("prepare submitted command-drain worker")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit command-drain worker park = (%+v, %t)", action, ok)
	}

	main := &G{magic: gMagic, state: GDead}
	if needed, ok := RequestCommandShutdownDrain(p, main); !ok || !needed ||
		task.g.park.taskCancelKind != TaskCancelShutdown ||
		task.g.park.taskCancelPhase != taskCancelRequested {
		t.Fatalf("request command-drain worker cancellation = needed:%t ok:%t cancel:(%d,%d)",
			needed, ok, task.g.park.taskCancelKind, task.g.park.taskCancelPhase)
	}
	if drained, promoted, ok := PollExecutor(driver); !ok || drained != 0 || promoted != 0 || p.readyHead != nil {
		t.Fatalf("poll submitted command-drain worker = drained:%d promoted:%d ok:%t ready:%p",
			drained, promoted, ok, p.readyHead)
	}
	if BeginExecutorClose(driver) {
		t.Fatal("executor closed while submitted command-drain worker still owned a physical completion")
	}

	payload := workerPayloadForTest(t, 13, 1300, 1301)
	if workers.Post(id, payload) != WorkerOperationPosted {
		t.Fatal("post command-drain worker physical completion")
	}
	if drained, promoted, ok := PollExecutor(driver); !ok || drained != 1 || promoted != 1 {
		t.Fatalf("poll completed command-drain worker = drained:%d promoted:%d ok:%t", drained, promoted, ok)
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue completed command-drain worker task")
	}
	action = beginWaitTestResume(t, p, task)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	outcome, caseID, lease, cancel, taken := TakeRunDecision(task.g, ticket)
	if !taken || outcome != ParkOutcomeCanceled || caseID != 0 || lease.Valid() || cancel != TaskCancelShutdown {
		t.Fatalf("take command-drain worker decision = (%d,%d,%+v,%d,%t)",
			outcome, caseID, lease, cancel, taken)
	}
	if !FinishCurrentExecutorWorkerPark(driver, task.g, id, lease, true, nil) {
		t.Fatal("finish command-drain worker cleanup")
	}
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare command-drain worker completion")
	}
	action, ok = Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume command-drain worker completion = (%+v,%t)", action, ok)
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check command-drain worker destroy = (%+v,%t)", action, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	receipt, ok := DestroyedBounded(p, task.g, action)
	if !ok || receipt.Kind != ActionCommitDestroy || receipt.Handle != nil {
		t.Fatalf("publish command-drain worker destroy receipt = (%+v,%t)", receipt, ok)
	}
	closeAction, ok := CommitDestroyedReceiptCompatibility(p, task.g, receipt)
	if !ok || closeAction.Kind != ActionTerminalExecutorClose || closeAction.Handle != nil {
		t.Fatalf("begin command-drain worker terminal close = (%+v,%t)", closeAction, ok)
	}
	closedG, complete, ok := ConfirmTerminalExecutorClose(driver)
	if !ok || closedG != task.g || complete.Kind != ActionComplete || complete.Handle != nil {
		t.Fatalf("confirm command-drain worker terminal close = (%p,%+v,%t)", closedG, complete, ok)
	}
	if !AcknowledgeTaskCancellation(task.g, TaskCancelShutdown) {
		t.Fatal("acknowledge command-drain worker cancellation")
	}
	if !workers.CanRelease() || !waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("command-drain worker cleanup retained stable source state")
	}
}

type currentWorkerOwnerFixture struct {
	p         *P
	driver    *ExecutorDriver
	waits     *WaitRegistrationTable
	workers   *WorkerOperationSource
	handle    ExecutorHandle
	route     RouteID
	task      *yieldingTestG
	action    Action
	wait      WaitSetRecord
	ticket    ParkTicket
	operation OperationID
}

func bindCurrentWorkerOwnerFixture(
	t *testing.T,
	registry *ExecutorRegistry,
	route RouteID,
	name string,
) *currentWorkerOwnerFixture {
	t.Helper()
	fixture := &currentWorkerOwnerFixture{
		p:       new(P),
		driver:  new(ExecutorDriver),
		waits:   new(WaitRegistrationTable),
		workers: new(WorkerOperationSource),
		route:   route,
		task:    newYieldingTestG(t, name),
	}
	fixture.handle = registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalogAtRoute(
		fixture.driver,
		fixture.p,
		registry,
		fixture.handle,
		route,
		ExecutorSourceCatalog{Waits: fixture.waits, Worker: fixture.workers},
	) {
		t.Fatalf("bind current worker owner %q", name)
	}
	if !Enqueue(fixture.p, fixture.task.g) {
		t.Fatalf("enqueue current worker owner %q", name)
	}
	if got, ok := NextRunnable(fixture.p); !ok || got != fixture.task.g {
		t.Fatalf("dequeue current worker owner %q = (%p, %t)", name, got, ok)
	}
	fixture.action = beginWaitTestResume(t, fixture.p, fixture.task)
	fixture.task.frame.header.SuspendReason = uint16(SuspendPark)
	fixture.task.frame.header.Lifecycle = uint16(FrameSuspended)
	return fixture
}

func parkCurrentWorkerOwnerFixture(t *testing.T, fixture *currentWorkerOwnerFixture, caseID, seed uint32) {
	t.Helper()
	ticket, operation, ok := PrepareCurrentExecutorWorkerPark(
		fixture.driver,
		fixture.task.g,
		fixture.task.handle,
		fixture.task.frame.header,
		&fixture.wait,
		caseID,
		seed,
	)
	if !ok || !CommitCurrentExecutorWorkerSubmission(fixture.driver, fixture.task.g, operation) {
		t.Fatalf("prepare current worker owner %s", fixture.task.name)
	}
	fixture.ticket = ticket
	fixture.operation = operation
	action, ok := Resumed(fixture.p, fixture.task.g, fixture.action)
	if !ok || action.Kind != ActionPark {
		t.Fatalf("park current worker owner %s = (%+v, %t)", fixture.task.name, action, ok)
	}
	fixture.action = action
}

func pollCurrentWorkerOwnerFixture(t *testing.T, fixture *currentWorkerOwnerFixture) {
	t.Helper()
	for step := 0; step < 1000; step++ {
		progress, ok := PollExecutorSlice(fixture.driver, 1)
		if !ok || progress.Used != 1 {
			t.Fatalf("poll current worker owner %s at %d = (%+v, %t)", fixture.task.name, step, progress, ok)
		}
		if progress.Complete {
			if progress.Worker != 1 || progress.WorkerLost != 0 || progress.Completed != 1 ||
				progress.Promoted != 1 || progress.ApplyVisits != 1 {
				t.Fatalf("complete current worker owner %s = %+v", fixture.task.name, progress)
			}
			return
		}
	}
	t.Fatalf("current worker owner %s did not complete", fixture.task.name)
}

func finishCurrentWorkerOwnerFixture(
	t *testing.T,
	fixture *currentWorkerOwnerFixture,
	wrong *ExecutorDriver,
	want ScalarResultPayloadV1,
) {
	t.Helper()
	if got, ok := NextRunnable(fixture.p); !ok || got != fixture.task.g {
		t.Fatalf("dequeue completed current worker owner %s = (%p, %t)", fixture.task.name, got, ok)
	}
	fixture.action = beginWaitTestResume(t, fixture.p, fixture.task)
	fixture.task.frame.header.SuspendReason = uint16(SuspendPark)
	fixture.task.frame.header.Lifecycle = uint16(FrameSuspended)
	outcome, caseID, lease, cancel, ok := TakeRunDecision(fixture.task.g, fixture.ticket)
	if !ok || outcome != ParkOutcomeCompleted || caseID == 0 || !lease.Valid() || cancel != TaskCancelNone {
		t.Fatalf("take current worker owner %s = (%d, %d, %+v, %d, %t)",
			fixture.task.name, outcome, caseID, lease, cancel, ok)
	}
	var got ScalarResultPayloadV1
	if FinishCurrentExecutorWorkerPark(wrong, fixture.task.g, fixture.operation, lease, false, &got) ||
		got != (ScalarResultPayloadV1{}) {
		t.Fatalf("wrong owner consumed current worker result %s: %+v", fixture.task.name, got)
	}
	if !FinishCurrentExecutorWorkerPark(fixture.driver, fixture.task.g, fixture.operation, lease, false, &got) || got != want {
		t.Fatalf("finish current worker owner %s = %+v, want %+v", fixture.task.name, got, want)
	}
	fixture.task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	fixture.task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(fixture.task.g, fixture.task.handle, fixture.task.frame.header) {
		t.Fatalf("prepare current worker completion %s", fixture.task.name)
	}
	action, ok := Resumed(fixture.p, fixture.task.g, fixture.action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume current worker completion %s = (%+v, %t)", fixture.task.name, action, ok)
	}
	action, ok = Checked(fixture.p, fixture.task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check current worker destroy %s = (%+v, %t)", fixture.task.name, action, ok)
	}
	releaseTestFrame(t, fixture.task.g, fixture.task.frame)
	closeAction, ok := Destroyed(fixture.p, fixture.task.g, action)
	if !ok || closeAction.Kind != ActionTerminalExecutorClose || closeAction.Handle != nil {
		t.Fatalf("begin current worker terminal close %s = (%+v, %t)", fixture.task.name, closeAction, ok)
	}
	closed, terminal, ok := ConfirmTerminalExecutorClose(fixture.driver)
	if !ok || closed != fixture.task.g || terminal.Kind != ActionComplete || terminal.Handle != nil {
		t.Fatalf("confirm current worker terminal close %s = (%p, %+v, %t)", fixture.task.name, closed, terminal, ok)
	}
}

func TestCurrentExecutorWorkerOwnerIsExactAcrossTwoP(t *testing.T) {
	registry := new(ExecutorRegistry)
	first := bindCurrentWorkerOwnerFixture(t, registry, RouteID(3), "current-worker-first")
	second := bindCurrentWorkerOwnerFixture(t, registry, RouteID(7), "current-worker-second")

	firstDriver, firstHandle, firstRoute, firstOK := CurrentExecutorWorkerDriver(first.task.g)
	secondDriver, secondHandle, secondRoute, secondOK := CurrentExecutorWorkerDriver(second.task.g)
	if !firstOK || !secondOK || firstDriver != first.driver || secondDriver != second.driver ||
		firstHandle != first.handle || secondHandle != second.handle || firstRoute != first.route ||
		secondRoute != second.route || firstHandle == secondHandle || firstRoute == secondRoute {
		t.Fatalf("resolve two current worker owners = first(%p,%+v,%d,%t) second(%p,%+v,%d,%t)",
			firstDriver, firstHandle, firstRoute, firstOK, secondDriver, secondHandle, secondRoute, secondOK)
	}

	savedP := first.task.g.runP
	first.task.g.runP = second.p
	if driver, handle, route, ok := CurrentExecutorWorkerDriver(first.task.g); ok || driver != nil ||
		handle != (ExecutorHandle{}) || route != 0 {
		t.Fatalf("wrong P resolved current worker owner = (%p, %+v, %d, %t)", driver, handle, route, ok)
	}
	first.task.g.runP = savedP
	savedCurrent := first.p.current
	first.p.current = second.task.g
	if _, _, _, ok := CurrentExecutorWorkerDriver(first.task.g); ok {
		t.Fatal("wrong current G resolved a worker owner")
	}
	first.p.current = savedCurrent
	savedAction := first.p.action
	first.p.action.Handle = second.task.handle
	if _, _, _, ok := CurrentExecutorWorkerDriver(first.task.g); ok {
		t.Fatal("wrong physical handle resolved a worker owner")
	}
	first.p.action = savedAction

	if ticket, operation, ok := PrepareCurrentExecutorWorkerPark(
		second.driver, first.task.g, first.task.handle, first.task.frame.header, &first.wait, 11, 101,
	); ok || ticket != (ParkTicket{}) || operation != (OperationID{}) || first.wait != (WaitSetRecord{}) ||
		!workerOperationSourceEmpty(first.workers, first.p) || !workerOperationSourceEmpty(second.workers, second.p) {
		t.Fatal("wrong driver mutated a current worker owner")
	}
	if ticket, operation, ok := PrepareCurrentExecutorWorkerPark(
		first.driver, first.task.g, second.task.handle, first.task.frame.header, &first.wait, 11, 101,
	); ok || ticket != (ParkTicket{}) || operation != (OperationID{}) || first.wait != (WaitSetRecord{}) ||
		!workerOperationSourceEmpty(first.workers, first.p) {
		t.Fatal("wrong frame handle mutated a current worker owner")
	}

	ticket, operation, ok := PrepareCurrentExecutorWorkerPark(
		first.driver, first.task.g, first.task.handle, first.task.frame.header, &first.wait, 11, 101,
	)
	if !ok {
		t.Fatal("prepare first current worker owner")
	}
	first.ticket, first.operation = ticket, operation
	if CommitCurrentExecutorWorkerSubmission(second.driver, first.task.g, operation) {
		t.Fatal("wrong driver committed first current worker operation")
	}
	if !CommitCurrentExecutorWorkerSubmission(first.driver, first.task.g, operation) {
		t.Fatal("commit first current worker operation")
	}
	if action, ok := Resumed(first.p, first.task.g, first.action); !ok || action.Kind != ActionPark {
		t.Fatalf("park first current worker owner = (%+v, %t)", action, ok)
	} else {
		first.action = action
	}
	parkCurrentWorkerOwnerFixture(t, second, 17, 107)
	if first.operation.Route() != first.route || second.operation.Route() != second.route ||
		first.operation.LocalSlot() != second.operation.LocalSlot() ||
		first.operation.Generation != second.operation.Generation || first.operation == second.operation {
		t.Fatalf("two-P worker identities alias: %+v %+v", first.operation, second.operation)
	}

	payload1 := workerPayloadForTest(t, 21, 2101, 2102)
	payload2 := workerPayloadForTest(t, 22, 2201, 2202)
	if second.workers.Post(first.operation, payload1) != WorkerOperationPostInvalid || second.workers.Pending() {
		t.Fatal("wrong worker source accepted another P's operation")
	}
	if first.workers.Post(first.operation, payload1) != WorkerOperationPosted ||
		second.workers.Post(second.operation, payload2) != WorkerOperationPosted {
		t.Fatal("post exact two-P worker completions")
	}
	pollCurrentWorkerOwnerFixture(t, first)
	pollCurrentWorkerOwnerFixture(t, second)
	finishCurrentWorkerOwnerFixture(t, first, second.driver, payload1)
	finishCurrentWorkerOwnerFixture(t, second, first.driver, payload2)
	if !first.workers.CanRelease() || !second.workers.CanRelease() ||
		!first.waits.CanRelease() || !second.waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("two-P current worker owner retained source or executor state")
	}
}
