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
	"sync"
	"testing"
	"unsafe"
)

func TestPollOperationHandlePODLayoutUnaffectedByPaging(t *testing.T) {
	if unsafe.Sizeof(PollOperationHandle{}) != 8 || unsafe.Alignof(PollOperationHandle{}) != 4 ||
		unsafe.Offsetof(PollOperationHandle{}.Generation) != 4 {
		t.Fatalf("poll handle layout changed: size=%d align=%d generation=%d",
			unsafe.Sizeof(PollOperationHandle{}), unsafe.Alignof(PollOperationHandle{}),
			unsafe.Offsetof(PollOperationHandle{}.Generation))
	}
}

func bindPollTestSourceSet(t *testing.T) (*P, *ExecutorSourceSet, *WaitRegistrationTable, *PollOperationSource) {
	t.Helper()
	p := new(P)
	sources := new(ExecutorSourceSet)
	waits := new(WaitRegistrationTable)
	poll := new(PollOperationSource)
	if !bindExecutorSourceSetAtRoute(sources, p, RouteID(7), ExecutorSourceCatalog{Waits: waits, Poll: poll}) {
		t.Fatal("bind poll test source set")
	}
	return p, sources, waits, poll
}

func unbindPollTestSourceSet(t *testing.T, p *P, sources *ExecutorSourceSet, waits *WaitRegistrationTable, poll *PollOperationSource) {
	t.Helper()
	if !unbindExecutorSourceSet(sources, p) {
		t.Fatal("unbind poll test source set")
	}
	if !waits.CanRelease() || !poll.CanRelease() {
		t.Fatal("unbound poll source set retained ownership")
	}
}

func TestPollOperationSourceRequiresBoundExactOwnerAndExposesSnapshot(t *testing.T) {
	var unbound PollOperationSource
	var rejected WaitToken
	if ticket, handle, result := PreparePollOperation(new(P), &unbound, &rejected, 3, PollInterestRead, 10); result != PollOperationPrepareRejected ||
		ticket != 0 || handle != (PollOperationHandle{}) {
		t.Fatalf("unbound prepare = (%d, %+v, %d)", ticket, handle, result)
	}

	p, sources, waits, poll := bindPollTestSourceSet(t)
	var token WaitToken
	ticket, handle, result := PreparePollOperation(p, poll, &token, 42, PollInterestWrite, 1234)
	if result != PollOperationPrepared || !validWaitTicket(ticket) || handle == (PollOperationHandle{}) {
		t.Fatalf("bound prepare = (%d, %+v, %d)", ticket, handle, result)
	}
	id, idOK := MakeOperationIDAtRoute(OperationSourcePoll, RouteID(7), handle.Slot, handle.Generation)
	if !idOK {
		t.Fatal("construct expected poll operation ID")
	}
	want := PollOperationSnapshot{
		Handle:   handle,
		ID:       id,
		Deadline: 1234,
		FD:       42,
		Interest: PollInterestWrite,
	}
	if got, active, ok := poll.SnapshotAt(p, handle.Slot-1); !ok || !active || got != want {
		t.Fatalf("active snapshot = (%+v, %t, %t), want %+v", got, active, ok, want)
	}
	other := new(P)
	if got, active, ok := poll.SnapshotAt(other, handle.Slot-1); ok || active || got != (PollOperationSnapshot{}) {
		t.Fatalf("foreign-owner snapshot = (%+v, %t, %t)", got, active, ok)
	}
	if poll.UpdateDeadline(other, handle, 99) || poll.PostReady(other, handle) != PollOperationPostInvalid {
		t.Fatal("foreign owner mutated bound poll operation")
	}
	if !poll.RollbackPreparedPollOperation(p, handle, &token, ticket) || poll.Pending() {
		t.Fatal("prepared rollback retained source work")
	}
	if poll.PostReady(p, handle) != PollOperationPostClosed {
		t.Fatal("retired exact generation did not reject as closed")
	}
	if got, active, ok := poll.SnapshotAt(p, handle.Slot-1); !ok || active || got != (PollOperationSnapshot{}) {
		t.Fatalf("retired snapshot = (%+v, %t, %t)", got, active, ok)
	}
	unbindPollTestSourceSet(t, p, sources, waits, poll)
}

func TestPollOperationSourceReadyDeadlineAndClosing(t *testing.T) {
	p, sources, waits, poll := bindPollTestSourceSet(t)
	var timeoutToken, closingToken, readyToken WaitToken
	timeoutTicket, timeoutHandle, result := PreparePollOperation(p, poll, &timeoutToken, 10, PollInterestRead, 200)
	if result != PollOperationPrepared {
		t.Fatal("prepare timeout operation")
	}
	closingTicket, closingHandle, result := PreparePollOperation(p, poll, &closingToken, 11, PollInterestWrite, 100)
	if result != PollOperationPrepared {
		t.Fatal("prepare closing operation")
	}
	readyTicket, readyHandle, result := PreparePollOperation(p, poll, &readyToken, 12, PollInterestRead, 0)
	if result != PollOperationPrepared {
		t.Fatal("prepare ready operation")
	}
	if !poll.UpdateDeadline(p, timeoutHandle, 75) || !poll.Pending() {
		t.Fatal("update deadline did not make the source observable")
	}
	if deadline, has, ok := sources.nextDeadline(p); !ok || !has || deadline != 75 {
		t.Fatalf("updated next deadline = (%d, %t, %t)", deadline, has, ok)
	}
	if post := poll.PostReady(p, readyHandle); post != PollOperationPosted {
		t.Fatalf("post ready = %d", post)
	}
	if post := poll.PostReady(p, readyHandle); post != PollOperationPostDuplicate {
		t.Fatalf("duplicate ready = %d", post)
	}
	if update := poll.UpdateDeadlineResult(p, readyHandle, 60); update != PollOperationUpdateClosed {
		t.Fatalf("posted deadline update = %d", update)
	}
	if snapshot, active, ok := poll.SnapshotAt(p, readyHandle.Slot-1); !ok || active || snapshot != (PollOperationSnapshot{}) {
		t.Fatalf("posted operation remained reactor-active = (%+v, %t, %t)", snapshot, active, ok)
	}

	scan, ok := sources.publishPass(p, 50, true)
	if !ok || scan.poll != 1 || scan.completed != 1 || !scan.hasDeadline || scan.deadline != 75 || poll.Pending() {
		t.Fatalf("ready publish pass = (%+v, %t), pending=%t", scan, ok, poll.Pending())
	}
	consumeRegisteredOutcome(t, &readyToken, readyTicket, WaitOutcomeCompleted)
	if got, ok := poll.RetireCompletedPollOperation(p, readyHandle, &readyToken, readyTicket); !ok || got != PollOperationReady {
		t.Fatalf("retire ready = (%d, %t)", got, ok)
	}

	scan, ok = sources.publishPass(p, 75, true)
	if !ok || scan.poll != 1 || scan.completed != 1 || !scan.hasDeadline || scan.deadline != 100 {
		t.Fatalf("timeout publish pass = (%+v, %t)", scan, ok)
	}
	consumeRegisteredOutcome(t, &timeoutToken, timeoutTicket, WaitOutcomeCompleted)
	if got, ok := poll.RetireCompletedPollOperation(p, timeoutHandle, &timeoutToken, timeoutTicket); !ok || got != PollOperationTimeout {
		t.Fatalf("retire timeout = (%d, %t)", got, ok)
	}

	if post := poll.PostClosing(p, closingHandle); post != PollOperationPosted {
		t.Fatalf("post closing = %d", post)
	}
	scan, ok = sources.publishPass(p, 80, true)
	if !ok || scan.poll != 1 || scan.completed != 1 || scan.hasDeadline {
		t.Fatalf("closing publish pass = (%+v, %t)", scan, ok)
	}
	consumeRegisteredOutcome(t, &closingToken, closingTicket, WaitOutcomeCompleted)
	if got, ok := poll.RetireCompletedPollOperation(p, closingHandle, &closingToken, closingTicket); !ok || got != PollOperationClosing {
		t.Fatalf("retire closing = (%d, %t)", got, ok)
	}
	unbindPollTestSourceSet(t, p, sources, waits, poll)
}

func TestPollOperationSourceCancellationAndGenerationIsolation(t *testing.T) {
	p, sources, waits, poll := bindPollTestSourceSet(t)
	var token WaitToken
	ticket, handle, result := PreparePollOperation(p, poll, &token, 20, PollInterestRead, 0)
	if result != PollOperationPrepared || poll.Cancel(p, handle) != WaitCancelWon || poll.Cancel(p, handle) != WaitCancelAlreadyCanceled {
		t.Fatal("cancel poll operation")
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.completed != 0 || poll.Pending() {
		t.Fatalf("cancel publish pass = (%+v, %t), pending=%t", scan, ok, poll.Pending())
	}
	consumeRegisteredOutcome(t, &token, ticket, WaitOutcomeCanceled)
	if poll.RetireCanceledPollOperation(new(P), handle, &token, ticket) ||
		!poll.RetireCanceledPollOperation(p, handle, &token, ticket) {
		t.Fatal("retire canceled operation did not require exact owner")
	}

	newTicket, newHandle, result := PreparePollOperation(p, poll, &token, 20, PollInterestRead, 0)
	if result != PollOperationPrepared || newHandle.Slot != handle.Slot || newHandle.Generation == handle.Generation {
		t.Fatalf("reuse generation = (%d, %+v, %d), old=%+v", newTicket, newHandle, result, handle)
	}
	if post := poll.PostReady(p, handle); post != PollOperationPostStale {
		t.Fatalf("stale generation post = %d", post)
	}
	if update := poll.UpdateDeadlineResult(p, handle, 10); update != PollOperationUpdateStale {
		t.Fatalf("stale generation deadline update = %d", update)
	}
	if !poll.RollbackPreparedPollOperation(p, newHandle, &token, newTicket) || poll.Pending() {
		t.Fatal("rollback reused operation")
	}
	unbindPollTestSourceSet(t, p, sources, waits, poll)
}

func TestPollOperationPagedCatalogAdmitsAndCompletesOneThousand(t *testing.T) {
	const count = 1000
	p := new(P)
	sources := new(ExecutorSourceSet)
	waits := new(WaitRegistrationTable)
	poll := new(PollOperationSource)
	var pages [15]PollOperationPage
	if !ConfigurePollOperationPages(poll, pages[:1]) ||
		PollOperationConfiguredCapacity(poll) != 2*PollOperationPageCapacity ||
		!ConfigurePollOperationPages(poll, pages[:]) ||
		PollOperationConfiguredCapacity(poll) != 16*PollOperationPageCapacity ||
		!ConfigurePollOperationPages(poll, pages[:]) {
		t.Fatal("configure paged poll catalog")
	}
	if !bindExecutorSourceSetAtRoute(sources, p, RouteID(7), ExecutorSourceCatalog{Waits: waits, Poll: poll}) {
		t.Fatal("bind paged poll source")
	}
	if budget, ok := executorMinPollBudget(sources); !ok || budget != 3 {
		t.Fatalf("empty paged poll progress budget = (%d, %t), want 3", budget, ok)
	}
	tokens := make([]WaitToken, count)
	tickets := make([]WaitTicket, count)
	handles := make([]PollOperationHandle, count)
	for index := range tokens {
		ticket, handle, result := PreparePollOperation(p, poll, &tokens[index], int32(index+10), PollInterestRead, 0)
		if result != PollOperationPrepared || !claimWait(&tokens[index], ticket) {
			t.Fatalf("prepare paged poll %d = (%d, %+v, %d)", index, ticket, handle, result)
		}
		tickets[index], handles[index] = ticket, handle
	}
	if handles[count-1].Slot != count || handles[PollOperationPageCapacity].Slot != PollOperationPageCapacity+1 {
		t.Fatalf("paged poll handles did not remain linear: page2=%+v last=%+v", handles[PollOperationPageCapacity], handles[count-1])
	}
	wantBudget := uint32(2*(count+1) + 1)
	if budget, ok := executorMinPollBudget(sources); !ok || budget != wantBudget {
		t.Fatalf("active-prefix paged poll progress budget = (%d, %t), want %d", budget, ok, wantBudget)
	}
	if snapshot, active, ok := poll.SnapshotAt(p, handles[count-1].Slot-1); !ok || !active ||
		snapshot.Handle != handles[count-1] || snapshot.ID.LocalSlot() != count || snapshot.ID.Route() != RouteID(7) {
		t.Fatalf("last paged poll snapshot = (%+v, %t, %t)", snapshot, active, ok)
	}
	for index := range handles {
		if result := poll.PostReady(p, handles[index]); result != PollOperationPosted {
			t.Fatalf("post paged poll %d = %d", index, result)
		}
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.poll != count || scan.completed != count || poll.Pending() {
		t.Fatalf("publish paged polls = (%+v, %t), pending=%t", scan, ok, poll.Pending())
	}
	for index := range tokens {
		if outcome, ok := consumeWait(&tokens[index], tickets[index]); !ok || outcome != WaitOutcomeCompleted {
			t.Fatalf("consume paged poll %d", index)
		}
		if result, ok := poll.RetireCompletedPollOperation(p, handles[index], &tokens[index], tickets[index]); !ok || result != PollOperationReady {
			t.Fatalf("retire paged poll %d = (%d, %t)", index, result, ok)
		}
	}
	unbindPollTestSourceSet(t, p, sources, waits, poll)
}

func TestExecutorDriverPollOperationRetainedWait(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	poll := new(PollOperationSource)
	executor := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, executor, ExecutorSourceCatalog{Waits: waits, Poll: poll}) {
		t.Fatal("bind poll-aware executor")
	}
	if budget, ok := MinExecutorPollBudget(driver); !ok || budget != 3 {
		t.Fatalf("empty poll-aware minimum budget = (%d, %t), want 3", budget, ok)
	}
	if snapshot, active, ok := SnapshotExecutorPollOperation(driver, 0); !ok || active || snapshot != (PollOperationSnapshot{}) {
		t.Fatalf("empty driver snapshot = (%+v, %t, %t)", snapshot, active, ok)
	}

	task := newYieldingTestG(t, "driver-poll")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue poll task")
	}
	if next, ok := NextRunnableAt(p, 0); !ok || next != task.g {
		t.Fatal("dequeue poll task")
	}
	action := beginWaitTestResume(t, p, task)
	var token WaitToken
	ticket, handle, result := PrepareExecutorPollOperation(driver, &token, 33, PollInterestRead, 100)
	if result != PollOperationPrepared {
		t.Fatalf("prepare executor poll = (%d, %+v, %d)", ticket, handle, result)
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, &token, ticket) {
		t.Fatal("prepare executor poll park")
	}
	if parked, ok := Resumed(p, task.g, action); !ok || parked.Kind != ActionPark {
		t.Fatalf("commit executor poll park = (%+v, %t)", parked, ok)
	}
	wantBudget := uint32(5) // one poll slot in each epoch, two resolves, one acknowledgement
	progress, ok := PollExecutorSliceAt(driver, 0, wantBudget)
	if !ok || !progress.Complete || progress.More || !progress.Blocked || progress.Used != wantBudget ||
		progress.Completed != 0 || progress.Epochs != 2 || !progress.HasDeadline || progress.NextDeadline != 100 {
		t.Fatalf("poll catalog progress = (%+v, %t)", progress, ok)
	}
	if deadline, has, ok := NextExecutorTimerDeadline(driver); !ok || !has || deadline != 100 {
		t.Fatalf("poll deadline = (%d, %t, %t)", deadline, has, ok)
	}
	if prepared, ok := PrepareExecutorSleepAt(driver, 0); !ok || !prepared {
		t.Fatalf("prepare poll retained wait = (%t, %t)", prepared, ok)
	}
	if sleep, deadline, has, ok := CommitExecutorSleepAt(driver, 1); !ok || !sleep || !has || deadline != 100 {
		t.Fatalf("commit poll retained wait = (%t, %d, %t, %t)", sleep, deadline, has, ok)
	}
	id, idOK := MakeOperationIDAtRoute(OperationSourcePoll, RouteID(1), handle.Slot, handle.Generation)
	if !idOK {
		t.Fatal("construct driver poll operation ID")
	}
	want := PollOperationSnapshot{Handle: handle, ID: id, Deadline: 100, FD: 33, Interest: PollInterestRead}
	if got, active, ok := SnapshotExecutorPollOperation(driver, handle.Slot-1); !ok || !active || got != want {
		t.Fatalf("sleeping driver snapshot = (%+v, %t, %t), want %+v", got, active, ok, want)
	}
	if post := PostExecutorPollReady(driver, handle); post != PollOperationPosted {
		t.Fatalf("import retained-wait readiness = %d", post)
	}
	if waitCount, timerCount, promoted, ok := WakeExecutorAt(driver, 2); !ok || waitCount != 0 || timerCount != 0 || promoted != 1 {
		t.Fatalf("wake poll executor = (%d, %d, %d, %t)", waitCount, timerCount, promoted, ok)
	}
	if next, ok := NextRunnableAt(p, 2); !ok || next != task.g {
		t.Fatal("dequeue ready poll task")
	}
	action = beginWaitTestResume(t, p, task)
	if got, ok := RetireCompletedExecutorPollOperation(driver, &token, ticket, handle); !ok || got != PollOperationReady {
		t.Fatalf("retire executor poll = (%d, %t)", got, ok)
	}
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !TerminalG(p, task.g) || !waits.CanRelease() || !poll.CanRelease() || !registry.CanRelease() {
		t.Fatal("poll-aware executor cleanup retained state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func bindPollV2TestSources(
	t *testing.T,
	p *P,
	route RouteID,
	timers *TimerRegistrationTable,
) (*ExecutorSourceSet, *WaitRegistrationTable, *PollOperationSource) {
	t.Helper()
	sources := new(ExecutorSourceSet)
	waits := new(WaitRegistrationTable)
	poll := new(PollOperationSource)
	if !bindExecutorSourceSetAtRoute(sources, p, route, ExecutorSourceCatalog{
		Waits: waits, Timers: timers, Poll: poll,
	}) {
		t.Fatal("bind poll V2 source set")
	}
	return sources, waits, poll
}

func finishPollV2Test(
	t *testing.T,
	p *P,
	sources *ExecutorSourceSet,
	waits *WaitRegistrationTable,
	poll *PollOperationSource,
	park *timerV2TestPark,
	action Action,
) {
	t.Helper()
	finishWaitTestTask(t, p, park.task, action)
	if !unbindExecutorSourceSet(sources, p) || !waits.CanRelease() || !poll.CanRelease() {
		t.Fatal("release poll V2 source set")
	}
}

func reservePollV2Test(
	t *testing.T,
	p *P,
	poll *PollOperationSource,
	park *timerV2TestPark,
	caseID uint32,
	fd int32,
) (PollOperationHandle, OperationID) {
	t.Helper()
	handle, id, ok := poll.ReserveAndAttachPollOperationV2(
		p, &park.task.g.park, park.ticket, park.wait, caseID, fd, PollInterestRead, 0,
	)
	if !ok || !handle.Valid() || !id.Valid() || id.LocalSlot() != handle.Slot || id.Generation != handle.Generation {
		t.Fatalf("reserve poll V2 = (%+v, %+v, %t)", handle, id, ok)
	}
	return handle, id
}

func TestPollOperationV2EventCancellationClassesAndLateEvent(t *testing.T) {
	tests := []struct {
		name       string
		taskCancel TaskCancelKind
		want       ParkOutcome
	}{
		{name: "operation", want: ParkOutcomeCompleted},
		{name: "task-abort", taskCancel: TaskCancelAbort, want: ParkOutcomeCanceled},
		{name: "shutdown", taskCancel: TaskCancelShutdown, want: ParkOutcomeCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := new(P)
			sources, waits, poll := bindPollV2TestSources(t, p, RouteID(7), nil)
			park := beginTimerV2TestPark(t, p, "poll-v2-"+test.name, 1, 201)
			handle, id := reservePollV2Test(t, p, poll, park, 41, 50)
			commitTimerV2TestPark(t, p, park)

			wrongRoute, _ := MakeOperationIDAtRoute(OperationSourcePoll, RouteID(6), id.LocalSlot(), id.Generation)
			stale := id
			stale.Generation++
			if poll.PostPollOperationV2(wrongRoute, PollOperationReady) != PollOperationPostInvalid ||
				poll.PostPollOperationV2(stale, PollOperationReady) != PollOperationPostStale ||
				poll.PostPollOperationV2(id, PollOperationReady) != PollOperationPosted ||
				poll.PostPollOperationV2(id, PollOperationClosing) != PollOperationPostDuplicate {
				t.Fatal("poll V2 route/generation/duplicate classification")
			}
			if test.taskCancel == TaskCancelNone {
				if !poll.RequestPollOperationV2Cancel(p, park.wait) {
					t.Fatal("request poll operation cancellation")
				}
			} else if !RequestTaskCancellation(p, park.task.g, test.taskCancel) {
				t.Fatal("request poll task cancellation")
			}
			if scan, ok := sources.publishPass(p, 0, true); !ok || scan.poll != 1 || scan.completed != 1 {
				t.Fatalf("publish poll cancellation race = (%+v, %t)", scan, ok)
			}
			if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
				t.Fatalf("resolve poll cancellation race = (%d, %d, %t)", promoted, visits, ok)
			}
			if got := poll.PostPollOperationV2(id, PollOperationReady); got != PollOperationPostClosed {
				t.Fatalf("late event after detach = %d", got)
			}

			action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
			if outcome != test.want || taskCancel != test.taskCancel {
				t.Fatalf("poll cancellation decision = (%d, %d, %+v, %d)", outcome, caseID, lease, taskCancel)
			}
			if test.want == ParkOutcomeCompleted {
				result, taken := poll.TakePollOperationV2Result(p, handle, lease)
				if caseID != 41 || !taken || result != PollOperationReady {
					t.Fatalf("take poll winner = (%d, %t), case=%d", result, taken, caseID)
				}
			} else if caseID != 0 || lease != (OperationResultLease{}) {
				t.Fatal("strong cancellation retained poll winner lease")
			}
			if !poll.RecyclePollOperationV2(p, handle) {
				t.Fatal("recycle poll cancellation generation")
			}
			finishPollV2Test(t, p, sources, waits, poll, park, action)
			if test.taskCancel != TaskCancelNone && !AcknowledgeTaskCancellation(park.task.g, test.taskCancel) {
				t.Fatal("acknowledge poll task cancellation")
			}
		})
	}
}

func TestPollOperationV2CancellationBeforeEventAndABA(t *testing.T) {
	p := new(P)
	sources, waits, poll := bindPollV2TestSources(t, p, RouteID(7), nil)
	park := beginTimerV2TestPark(t, p, "poll-v2-cancel-first", 1, 203)
	firstHandle, firstID := reservePollV2Test(t, p, poll, park, 51, 51)
	commitTimerV2TestPark(t, p, park)
	if !poll.RequestPollOperationV2Cancel(p, park.wait) {
		t.Fatal("request cancel before event")
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.poll != 0 {
		t.Fatalf("event-free cancel publication = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("resolve event-free cancel = (%d, %d, %t)", promoted, visits, ok)
	}
	if got := poll.PostPollOperationV2(firstID, PollOperationReady); got != PollOperationPostClosed {
		t.Fatalf("event after cancellation = %d", got)
	}
	action, outcome, _, lease, _ := resumeTimerV2TestPark(t, p, park)
	if outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) ||
		!poll.RecyclePollOperationV2(p, firstHandle) {
		t.Fatal("finish cancel-before-event generation")
	}

	park = rebeginTimerV2TestPark(t, park.task, action, 1, 205)
	secondHandle, secondID := reservePollV2Test(t, p, poll, park, 52, 52)
	if secondHandle.Slot != firstHandle.Slot || secondHandle.Generation <= firstHandle.Generation || secondID == firstID {
		t.Fatalf("poll V2 generation did not advance: first=%+v second=%+v", firstHandle, secondHandle)
	}
	commitTimerV2TestPark(t, p, park)
	if got := poll.PostPollOperationV2(firstID, PollOperationReady); got != PollOperationPostStale {
		t.Fatalf("ABA event classification = %d", got)
	}
	if !poll.RequestPollOperationV2Cancel(p, park.wait) {
		t.Fatal("cancel second generation")
	}
	if _, ok := sources.publishPass(p, 0, true); !ok {
		t.Fatal("publish second cancellation")
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("resolve second cancellation = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, _, lease, _ = resumeTimerV2TestPark(t, p, park)
	if outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) ||
		poll.RecyclePollOperationV2(p, firstHandle) || !poll.RecyclePollOperationV2(p, secondHandle) {
		t.Fatal("finish exact second poll generation")
	}
	finishPollV2Test(t, p, sources, waits, poll, park, action)
}

func TestPollOperationV2ShutdownWaitsForAdmittedProducerThenCleansUp(t *testing.T) {
	p := new(P)
	sources, waits, poll := bindPollV2TestSources(t, p, RouteID(7), nil)
	park := beginTimerV2TestPark(t, p, "poll-v2-shutdown-admitted", 1, 206)
	handle, id := reservePollV2Test(t, p, poll, park, 53, 53)
	commitTimerV2TestPark(t, p, park)
	slot, ok := pollOperationSlotAt(poll, id.LocalSlot()-1)
	if !ok || !producerAdmissionAcquire(&slot.v2Producer.inflight) {
		t.Fatal("admit poll producer before shutdown")
	}
	if !RequestTaskCancellation(p, park.task.g, TaskCancelShutdown) {
		t.Fatal("request shutdown with admitted poll producer")
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.poll != 0 {
		t.Fatalf("publish pre-event shutdown = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 0 || visits != 1 ||
		park.wait.work != waitSetWorkAwaitingExternal {
		t.Fatalf("shutdown must await admitted producer = (%d, %d, %t), work=%d", promoted, visits, ok, park.wait.work)
	}
	// Complete the exact producer admission after Close has sealed new entries.
	if !preemptCompareAndSwap(
		&slot.v2Mailbox,
		uint32(pollOperationMailboxEmpty),
		uint32(pollOperationMailboxPosting),
	) {
		t.Fatal("claim late admitted poll mailbox")
	}
	slot.v2Result = PollOperationReady
	preemptStore(&slot.v2Mailbox, uint32(pollOperationMailboxPosted))
	preemptStore(&poll.pending, 1)
	producerAdmissionRelease(&slot.v2Producer.inflight)

	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.poll != 1 {
		t.Fatalf("publish admitted late event = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("finish shutdown cleanup = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
	if outcome != ParkOutcomeCanceled || caseID != 0 || lease != (OperationResultLease{}) ||
		taskCancel != TaskCancelShutdown || !poll.RecyclePollOperationV2(p, handle) {
		t.Fatalf("shutdown cleanup decision = (%d, %d, %+v, %d)", outcome, caseID, lease, taskCancel)
	}
	finishPollV2Test(t, p, sources, waits, poll, park, action)
	if !AcknowledgeTaskCancellation(park.task.g, TaskCancelShutdown) {
		t.Fatal("acknowledge poll shutdown cancellation")
	}
}

func TestPollOperationV2ConcurrentProducerCoalescesDuringTaskAbort(t *testing.T) {
	p := new(P)
	sources, waits, poll := bindPollV2TestSources(t, p, RouteID(7), nil)
	park := beginTimerV2TestPark(t, p, "poll-v2-concurrent-producer", 1, 209)
	handle, id := reservePollV2Test(t, p, poll, park, 54, 54)
	commitTimerV2TestPark(t, p, park)

	const producers = 16
	results := make([]PollOperationPostResult, producers)
	var group sync.WaitGroup
	group.Add(producers)
	for index := range results {
		go func(index int) {
			defer group.Done()
			result := PollOperationReady
			if index&1 != 0 {
				result = PollOperationClosing
			}
			results[index] = poll.PostPollOperationV2(id, result)
		}(index)
	}
	if !RequestTaskCancellation(p, park.task.g, TaskCancelAbort) {
		t.Fatal("request abort during concurrent poll publication")
	}
	group.Wait()
	posted, duplicates := 0, 0
	for _, result := range results {
		switch result {
		case PollOperationPosted:
			posted++
		case PollOperationPostDuplicate:
			duplicates++
		default:
			t.Fatalf("concurrent post classification = %d", result)
		}
	}
	if posted != 1 || duplicates != producers-1 {
		t.Fatalf("concurrent post counts = posted:%d duplicate:%d", posted, duplicates)
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.poll != 1 {
		t.Fatalf("publish concurrent producer = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("resolve concurrent producer abort = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
	if outcome != ParkOutcomeCanceled || caseID != 0 || lease != (OperationResultLease{}) ||
		taskCancel != TaskCancelAbort || !poll.RecyclePollOperationV2(p, handle) {
		t.Fatalf("concurrent producer abort decision = (%d, %d, %+v, %d)", outcome, caseID, lease, taskCancel)
	}
	finishPollV2Test(t, p, sources, waits, poll, park, action)
	if !AcknowledgeTaskCancellation(park.task.g, TaskCancelAbort) {
		t.Fatal("acknowledge concurrent producer abort")
	}
}

func TestPollOperationV2TwoReadyEventsChooseOneWinner(t *testing.T) {
	p := new(P)
	sources, waits, poll := bindPollV2TestSources(t, p, RouteID(7), nil)
	park := beginTimerV2TestPark(t, p, "poll-v2-two-ready", 2, 207)
	firstCase, secondCase := uint32(61), uint32(62)
	firstHandle, firstID := reservePollV2Test(t, p, poll, park, firstCase, 61)
	secondHandle, secondID := reservePollV2Test(t, p, poll, park, secondCase, 62)
	commitTimerV2TestPark(t, p, park)
	if poll.PostPollOperationV2(firstID, PollOperationReady) != PollOperationPosted ||
		poll.PostPollOperationV2(secondID, PollOperationClosing) != PollOperationPosted {
		t.Fatal("post two poll candidates")
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.poll != 2 || scan.completed != 2 {
		t.Fatalf("publish two poll candidates = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 2 {
		t.Fatalf("resolve two poll candidates = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
	winnerHandle, loserHandle := firstHandle, secondHandle
	wantResult := PollOperationReady
	if parkCaseRank(207^park.ticket.generation*0x9e3779b9^park.ticket.epoch*0x85ebca6b, secondCase) <
		parkCaseRank(207^park.ticket.generation*0x9e3779b9^park.ticket.epoch*0x85ebca6b, firstCase) {
		winnerHandle, loserHandle = secondHandle, firstHandle
		wantResult = PollOperationClosing
		firstCase = secondCase
	}
	result, taken := poll.TakePollOperationV2Result(p, winnerHandle, lease)
	if outcome != ParkOutcomeCompleted || caseID != firstCase || taskCancel != TaskCancelNone ||
		!taken || result != wantResult || !poll.RecyclePollOperationV2(p, winnerHandle) ||
		!poll.RecyclePollOperationV2(p, loserHandle) {
		t.Fatalf("two-event winner = outcome:%d case:%d result:%d taken:%t", outcome, caseID, result, taken)
	}
	finishPollV2Test(t, p, sources, waits, poll, park, action)
}

func TestPollOperationV2DeadlinePublishesTimeoutResult(t *testing.T) {
	p := new(P)
	sources, waits, poll := bindPollV2TestSources(t, p, RouteID(7), nil)
	park := beginTimerV2TestPark(t, p, "poll-v2-deadline", 1, 210)
	handle, id, reserved := poll.ReserveAndAttachPollOperationV2(
		p, &park.task.g.park, park.ticket, park.wait, 63, 63, PollInterestWrite, 50,
	)
	if !reserved || !handle.Valid() || !id.Valid() {
		t.Fatal("reserve deadline poll V2")
	}
	commitTimerV2TestPark(t, p, park)
	if scan, ok := sources.publishPass(p, 49, true); !ok || scan.poll != 0 ||
		!scan.hasDeadline || scan.deadline != 50 {
		t.Fatalf("publish before poll deadline = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 0 || visits != 0 {
		t.Fatalf("resolve before poll deadline = (%d, %d, %t)", promoted, visits, ok)
	}
	if scan, ok := sources.publishPass(p, 50, true); !ok || scan.poll != 1 || scan.hasDeadline {
		t.Fatalf("publish poll timeout = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("resolve poll timeout = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
	result, taken := poll.TakePollOperationV2Result(p, handle, lease)
	if outcome != ParkOutcomeCompleted || caseID != 63 || taskCancel != TaskCancelNone ||
		!taken || result != PollOperationTimeout || !poll.RecyclePollOperationV2(p, handle) {
		t.Fatalf("poll timeout decision = (%d, %d, %d, %t, %d)", outcome, caseID, result, taken, taskCancel)
	}
	finishPollV2Test(t, p, sources, waits, poll, park, action)
}

func TestPollOperationV2DeadlineOrdersQueuedReadiness(t *testing.T) {
	for _, test := range []struct {
		name string
		now  int64
		want PollOperationResult
	}{
		{name: "readiness-before-deadline", now: 49, want: PollOperationReady},
		{name: "deadline-before-delayed-readiness", now: 50, want: PollOperationTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := new(P)
			sources, waits, poll := bindPollV2TestSources(t, p, RouteID(7), nil)
			park := beginTimerV2TestPark(t, p, "poll-v2-readiness-deadline-order", 1, 214)
			handle, id, reserved := poll.ReserveAndAttachPollOperationV2(
				p, &park.task.g.park, park.ticket, park.wait, 66, 66, PollInterestRead, 50,
			)
			if !reserved || !handle.Valid() || !id.Valid() {
				t.Fatal("reserve readiness/deadline poll V2")
			}
			commitTimerV2TestPark(t, p, park)
			if event := poll.PostPollOperationV2(id, PollOperationReady); event != PollOperationPosted {
				t.Fatalf("post queued readiness = %d", event)
			}
			if scan, ok := sources.publishPass(p, test.now, true); !ok || scan.poll != 1 ||
				scan.completed != 1 || scan.hasDeadline {
				t.Fatalf("publish queued readiness at %d = (%+v, %t)", test.now, scan, ok)
			}
			if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
				t.Fatalf("resolve queued readiness at %d = (%d, %d, %t)", test.now, promoted, visits, ok)
			}
			action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
			result, taken := poll.TakePollOperationV2Result(p, handle, lease)
			if outcome != ParkOutcomeCompleted || caseID != 66 || taskCancel != TaskCancelNone ||
				!taken || result != test.want || !poll.RecyclePollOperationV2(p, handle) {
				t.Fatalf("queued readiness at %d = (%d, %d, %d, %t, %d), want result %d",
					test.now, outcome, caseID, result, taken, taskCancel, test.want)
			}
			finishPollV2Test(t, p, sources, waits, poll, park, action)
		})
	}
}

func TestPollOperationV2ActiveDeadlineUpdateAndExactClosingWake(t *testing.T) {
	p := new(P)
	sources, waits, poll := bindPollV2TestSources(t, p, RouteID(7), nil)
	park := beginTimerV2TestPark(t, p, "poll-v2-updated-deadline-close", 1, 212)
	firstHandle, firstID, reserved := poll.ReserveAndAttachPollOperationV2(
		p, &park.task.g.park, park.ticket, park.wait, 64, 64, PollInterestRead, 100,
	)
	if !reserved || !firstHandle.Valid() || !firstID.Valid() {
		t.Fatal("reserve first deadline poll V2")
	}
	commitTimerV2TestPark(t, p, park)

	staleFuture := firstID
	staleFuture.Generation++
	if update := poll.UpdatePollOperationV2Deadline(p, firstID, 40); update != PollOperationUpdated {
		t.Fatalf("update active poll V2 deadline = %d", update)
	}
	if update := poll.UpdatePollOperationV2Deadline(p, staleFuture, 30); update != PollOperationUpdateStale {
		t.Fatalf("update stale future generation = %d", update)
	}
	if event := poll.PostPollOperationV2(staleFuture, PollOperationClosing); event != PollOperationPostStale {
		t.Fatalf("post stale future close = %d", event)
	}
	if scan, ok := sources.publishPass(p, 39, true); !ok || scan.poll != 0 ||
		!scan.hasDeadline || scan.deadline != 40 {
		t.Fatalf("scan updated active deadline after stale close = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 0 || visits != 0 {
		t.Fatalf("stale future close affected wait = (%d, %d, %t)", promoted, visits, ok)
	}
	if event := poll.PostPollOperationV2(firstID, PollOperationClosing); event != PollOperationPosted {
		t.Fatalf("post exact first-generation close = %d", event)
	}
	if update := poll.UpdatePollOperationV2Deadline(p, firstID, 20); update != PollOperationUpdateClosed {
		t.Fatalf("update after exact close publication = %d", update)
	}
	if scan, ok := sources.publishPass(p, 39, true); !ok || scan.poll != 1 || scan.completed != 1 || scan.hasDeadline {
		t.Fatalf("publish exact first-generation close = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("resolve exact first-generation close = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
	result, taken := poll.TakePollOperationV2Result(p, firstHandle, lease)
	if outcome != ParkOutcomeCompleted || caseID != 64 || taskCancel != TaskCancelNone ||
		!taken || result != PollOperationClosing || !poll.RecyclePollOperationV2(p, firstHandle) {
		t.Fatalf("take exact first-generation close = (%d, %d, %d, %t, %d)",
			outcome, caseID, result, taken, taskCancel)
	}

	park = rebeginTimerV2TestPark(t, park.task, action, 1, 213)
	secondHandle, secondID, reserved := poll.ReserveAndAttachPollOperationV2(
		p, &park.task.g.park, park.ticket, park.wait, 65, 65, PollInterestWrite, 80,
	)
	if !reserved || secondHandle.Slot != firstHandle.Slot ||
		secondHandle.Generation <= firstHandle.Generation || secondID == firstID {
		t.Fatalf("reserve reused poll generation: first=%+v second=%+v", firstHandle, secondHandle)
	}
	commitTimerV2TestPark(t, p, park)
	if event := poll.PostPollOperationV2(firstID, PollOperationClosing); event != PollOperationPostStale {
		t.Fatalf("post retired generation close = %d", event)
	}
	if update := poll.UpdatePollOperationV2Deadline(p, firstID, 60); update != PollOperationUpdateStale {
		t.Fatalf("update retired generation deadline = %d", update)
	}
	if scan, ok := sources.publishPass(p, 79, true); !ok || scan.poll != 0 ||
		!scan.hasDeadline || scan.deadline != 80 {
		t.Fatalf("retired close affected reused generation = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 0 || visits != 0 {
		t.Fatalf("retired close woke reused generation = (%d, %d, %t)", promoted, visits, ok)
	}
	if event := poll.PostPollOperationV2(secondID, PollOperationClosing); event != PollOperationPosted {
		t.Fatalf("post exact reused-generation close = %d", event)
	}
	if scan, ok := sources.publishPass(p, 79, true); !ok || scan.poll != 1 || scan.completed != 1 {
		t.Fatalf("publish exact reused-generation close = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("resolve exact reused-generation close = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, caseID, lease, taskCancel = resumeTimerV2TestPark(t, p, park)
	result, taken = poll.TakePollOperationV2Result(p, secondHandle, lease)
	if outcome != ParkOutcomeCompleted || caseID != 65 || taskCancel != TaskCancelNone ||
		!taken || result != PollOperationClosing || !poll.RecyclePollOperationV2(p, secondHandle) {
		t.Fatalf("take exact reused-generation close = (%d, %d, %d, %t, %d)",
			outcome, caseID, result, taken, taskCancel)
	}
	finishPollV2Test(t, p, sources, waits, poll, park, action)
}

func TestPollOperationV2SharesWaitSetWithTimerV2(t *testing.T) {
	p := new(P)
	timers := new(TimerRegistrationTable)
	sources, waits, poll := bindPollV2TestSources(t, p, RouteID(7), timers)
	park := beginTimerV2TestPark(t, p, "poll-v2-timer-select", 2, 211)
	timerCase, pollCase := uint32(71), uint32(72)
	// Make Timer the lower seeded rank to prove source-catalog order is not the
	// decision: Timer publishes before Poll, but rank remains authoritative.
	if parkCaseRank(park.task.g.park.seed, timerCase) > parkCaseRank(park.task.g.park.seed, pollCase) {
		timerCase, pollCase = pollCase, timerCase
	}
	timerHandle, timerOK := timers.ReserveAndAttachTimerV2(
		p, &park.task.g.park, park.ticket, park.wait, timerCase, 0,
	)
	pollHandle, pollID := reservePollV2Test(t, p, poll, park, pollCase, 72)
	if !timerOK {
		t.Fatal("reserve mixed timer")
	}
	commitTimerV2TestPark(t, p, park)
	if poll.PostPollOperationV2(pollID, PollOperationReady) != PollOperationPosted {
		t.Fatal("post mixed poll candidate")
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.timers != 1 || scan.poll != 1 {
		t.Fatalf("publish mixed timer/poll = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 2 {
		t.Fatalf("resolve mixed timer/poll = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, caseID, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
	if outcome != ParkOutcomeCompleted || caseID != timerCase || taskCancel != TaskCancelNone ||
		!timers.TakeTimerV2Result(p, timerHandle, lease) || !timers.RecycleTimerV2(p, timerHandle) ||
		!poll.RecyclePollOperationV2(p, pollHandle) {
		t.Fatalf("mixed timer/poll winner = (%d, %d, %+v, %d)", outcome, caseID, lease, taskCancel)
	}
	finishWaitTestTask(t, p, park.task, action)
	if !unbindExecutorSourceSet(sources, p) || !waits.CanRelease() || !poll.CanRelease() || !timers.CanRelease() {
		t.Fatal("release mixed timer/poll source set")
	}
}
