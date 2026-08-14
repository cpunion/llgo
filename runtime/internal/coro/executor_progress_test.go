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
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

const wantExecutorPollProgressSize = 60 + (unsafe.Sizeof(uintptr(0))/4-1)*4

var (
	_ [wantExecutorPollProgressSize - unsafe.Sizeof(ExecutorPollProgress{})]byte
	_ [unsafe.Sizeof(ExecutorPollProgress{}) - wantExecutorPollProgressSize]byte
)

func TestExecutorPollProgressPODLayout(t *testing.T) {
	if alignment := unsafe.Alignof(ExecutorPollProgress{}); alignment != 4 && alignment != 8 {
		t.Fatalf("executor progress alignment = %d, want natural uint32/int64 alignment", alignment)
	}
	deadlineOffset := uintptr(44 + (unsafe.Sizeof(uintptr(0))/4-1)*4)
	epochOffset := deadlineOffset + 8
	if unsafe.Offsetof(ExecutorPollProgress{}.Used) != 0 ||
		unsafe.Offsetof(ExecutorPollProgress{}.NextDeadline) != deadlineOffset ||
		unsafe.Offsetof(ExecutorPollProgress{}.Epochs) != epochOffset ||
		unsafe.Offsetof(ExecutorPollProgress{}.Complete) != epochOffset+1 ||
		unsafe.Offsetof(ExecutorPollProgress{}.More) != epochOffset+2 ||
		unsafe.Offsetof(ExecutorPollProgress{}.Blocked) != epochOffset+3 ||
		unsafe.Offsetof(ExecutorPollProgress{}.HasDeadline) != epochOffset+4 ||
		unsafe.Offsetof(ExecutorPollProgress{}.AtomicResolve) != epochOffset+5 ||
		unsafe.Offsetof(ExecutorPollProgress{}.Overshot) != epochOffset+6 {
		t.Fatalf("executor progress layout offsets changed: %+v", ExecutorPollProgress{})
	}
	typeOf := reflect.TypeOf(ExecutorPollProgress{})
	for index := 0; index < typeOf.NumField(); index++ {
		switch typeOf.Field(index).Type.Kind() {
		case reflect.Uint8, reflect.Uint32, reflect.Int64, reflect.Bool, reflect.Array:
		default:
			t.Fatalf("executor progress field %s is not POD scalar storage", typeOf.Field(index).Name)
		}
	}
}

func beginExecutorTimerBatchTestPark(t *testing.T, p *P, seed uint32) *timerV2TestPark {
	t.Helper()
	task := newYieldingTestG(t, "timer-batch")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue batched timer")
	}
	if g, ok := NextRunnableAt(p, 0); !ok || g != task.g {
		t.Fatal("dequeue batched timer")
	}
	action := beginWaitTestResume(t, p, task)
	ticket, ok := BeginParkSet(&task.g.park, 1, seed)
	wait := new(WaitSetRecord)
	if !ok || !PrepareWaitSetRecord(wait, task.g, ticket) {
		t.Fatal("prepare batched timer park")
	}
	return &timerV2TestPark{task: task, ticket: ticket, wait: wait, action: action}
}

func TestExecutorPollEpochBPreservesAExternalBlockOnly(t *testing.T) {
	sources := &ExecutorSourceSet{}
	transaction := executorPollTransaction{
		now:           17,
		phase:         executorPollAcknowledge,
		source:        executorCatalogDone,
		withDeadline:  true,
		retryBudget:   true,
		awaitExternal: true,
	}
	if !beginExecutorPollEpoch(&transaction, sources, executorPollEpochBPublish) {
		t.Fatal("begin B publish epoch")
	}
	if transaction.retryBudget || !transaction.awaitExternal || transaction.resampleNow ||
		transaction.phase != executorPollEpochBPublish || transaction.source != executorCatalogDone || transaction.cursor != 0 {
		t.Fatalf("A-only external block across B transition = %+v", transaction)
	}
}

func TestMinExecutorPollBudgetCountsActiveProductionCatalog(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	timers := new(TimerRegistrationTable)
	manual := new(ManualOperationSource)
	channel := new(ChannelOperationSource)
	control := new(TaskControlSource)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{
		Timers: timers, Manual: manual, Channel: channel, Control: control,
	}) {
		t.Fatal("bind complete production catalog")
	}
	want := uint32(3)
	if budget, ok := MinExecutorPollBudget(driver); !ok || budget != want {
		t.Fatalf("complete catalog minimum = (%d, %t), want %d", budget, ok, want)
	}
	if progress, ok := PollExecutorSliceAt(driver, 0, want); !ok || !progress.Complete || progress.Used != want ||
		progress.Overshot || progress.AtomicResolve || progress.Epochs != 2 {
		t.Fatalf("complete empty catalog poll = (%+v, %t)", progress, ok)
	}
	closeTestExecutorDriver(t, driver)
}

func TestExecutorPollProgressSkipsConfiguredUnallocatedTails(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	timers := new(TimerRegistrationTable)
	poll := new(PollOperationSource)
	worker := new(WorkerOperationSource)
	var timerPages [15]TimerRegistrationPage
	var pollPages [15]PollOperationPage
	var workerPages [15]WorkerOperationPage
	if !ConfigureTimerRegistrationPages(timers, timerPages[:]) ||
		!ConfigurePollOperationPages(poll, pollPages[:]) ||
		!ConfigureWorkerOperationPages(worker, workerPages[:]) {
		t.Fatal("configure paged progress catalog")
	}
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{
		Timers: timers, Poll: poll, Worker: worker,
	}) {
		t.Fatal("bind paged progress catalog")
	}
	want := uint32(3) // empty A resolve + acknowledge + empty B resolve
	if budget, ok := MinExecutorPollBudget(driver); !ok || budget != want {
		t.Fatalf("paged progress minimum = (%d, %t), want %d", budget, ok, want)
	}
	if progress, ok := PollExecutorSliceAt(driver, 0, want); !ok || !progress.Complete || progress.Used != want ||
		progress.Overshot || progress.AtomicResolve || progress.Epochs != 2 {
		t.Fatalf("paged empty catalog poll = (%+v, %t)", progress, ok)
	}
	closeTestExecutorDriver(t, driver)
	if !timers.CanRelease() || !poll.CanRelease() || !worker.CanRelease() {
		t.Fatal("paged progress catalog retained source storage")
	}
}

func TestExecutorPollReductionBatchesDueTimers(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	timers := new(TimerRegistrationTable)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalogAtRoute(
		driver,
		p,
		registry,
		handle,
		RouteID(1),
		ExecutorSourceCatalog{Timers: timers},
	) {
		t.Fatal("bind batched timer executor")
	}

	const count = executorTimerCatalogBatchQuantum
	parks := make([]*timerV2TestPark, int(count))
	handles := make([]TimerRegistrationHandle, int(count))
	for index := 0; index < int(count); index++ {
		park := beginExecutorTimerBatchTestPark(t, p, uint32(301+index))
		timer, attached := timers.ReserveAndAttachTimerV2(
			p,
			&park.task.g.park,
			park.ticket,
			park.wait,
			uint32(index+1),
			1,
		)
		wantCursor := uint32(index+1) % TimerRegistrationConfiguredCapacity(timers)
		if !attached || timer.Slot != uint32(index+1) || timers.reserveCursor != wantCursor {
			t.Fatalf("reserve batched timer %d = (%+v,%t), cursor=%d want=%d",
				index, timer, attached, timers.reserveCursor, wantCursor)
		}
		commitTimerV2TestPark(t, p, park)
		parks[index], handles[index] = park, timer
	}
	if result := registry.Request(handle); result != ExecutorRequestPublished {
		t.Fatalf("request batched timer executor = %d", result)
	}
	first, ok := PollExecutorSliceAt(driver, 1, 1)
	if !ok || first.Used != 1 || first.Complete || first.Timers != count || first.Completed != count ||
		driver.poll.phase != executorPollEpochAPublish || driver.poll.source != executorCatalogDone {
		t.Fatalf("first batched timer reduction = (%+v,%t), poll=%+v", first, ok, driver.poll)
	}

	var complete ExecutorPollProgress
	for reduction := 0; reduction < 1000; reduction++ {
		progress, advanced := PollExecutorSliceAt(driver, 1, 1)
		if !advanced || progress.Used != 1 {
			t.Fatalf("continue batched timer reduction %d = (%+v,%t)", reduction, progress, advanced)
		}
		if progress.Complete {
			complete = progress
			break
		}
	}
	if !complete.Complete || complete.Timers != count || complete.Promoted != count ||
		driver.poll != (executorPollTransaction{}) {
		t.Fatalf("complete batched timers = %+v poll=%+v", complete, driver.poll)
	}
	sentinel := newYieldingTestG(t, "timer-batch-sentinel")
	if !Enqueue(p, sentinel.g) {
		t.Fatal("enqueue batched timer terminal sentinel")
	}
	parkIndex := make(map[*G]int, len(parks))
	for index, park := range parks {
		parkIndex[park.task.g] = index
	}
	for resumed := 0; resumed < len(parks); resumed++ {
		g, ok := NextRunnableAt(p, 1)
		index, found := parkIndex[g]
		if !ok || !found {
			t.Fatalf("dequeue promoted batched timer %d = (%p,%t)", resumed, g, ok)
		}
		delete(parkIndex, g)
		park := parks[index]
		action := beginWaitTestResume(t, p, park.task)
		outcome, caseID, lease, taskCancel, decisionOK := TakeRunDecision(park.task.g, park.ticket)
		if outcome != ParkOutcomeCompleted || caseID != uint32(index+1) || taskCancel != TaskCancelNone ||
			!decisionOK ||
			!timers.DiscardTimerV2Result(p, handles[index], lease) ||
			!timers.RecycleTimerV2(p, handles[index]) {
			t.Fatalf("consume batched timer %d = outcome:%d case:%d lease:%+v task:%d",
				index, outcome, caseID, lease, taskCancel)
		}
		finishWaitTestTask(t, p, park.task, action)
	}
	closeTestExecutorDriver(t, driver)
	if g, ok := NextRunnable(p); !ok || g != sentinel.g {
		t.Fatalf("dequeue batched timer terminal sentinel = (%p,%t)", g, ok)
	}
	finishWaitTestTask(t, p, sentinel, beginWaitTestResume(t, p, sentinel))
	if !timers.CanRelease() || !registry.CanRelease() {
		t.Fatal("batched timer fixture retained source state")
	}
}

func TestExecutorPollSliceBudgetOneCompletesExactAcknowledgeTransaction(t *testing.T) {
	p := new(P)
	driver, registry, handle := bindTestExecutorDriver(t, p)
	wantBudget := uint32(3)
	if budget, ok := MinExecutorPollBudget(driver); !ok || budget != wantBudget {
		t.Fatalf("minimum wait-only poll budget = (%d, %t), want %d", budget, ok, wantBudget)
	}
	if result := registry.Request(handle); result != ExecutorRequestPublished {
		t.Fatalf("publish initial slice request = %d", result)
	}

	for step := uint32(1); step <= wantBudget; step++ {
		progress, ok := PollExecutorSlice(driver, 1)
		if !ok || progress.Used != 1 || progress.Blocked {
			t.Fatalf("budget-one step %d = (%+v, %t)", step, progress, ok)
		}
		if step < wantBudget && (progress.Complete || !progress.More) {
			t.Fatalf("budget-one step %d returned terminal progress %+v", step, progress)
		}
		switch step {
		case 1:
			if driver.poll.phase != executorPollAcknowledge || driver.poll.total.epochs != 1 ||
				!registry.ObserveRequested(handle) {
				t.Fatalf("A resolve boundary = %+v, requested=%t", driver.poll, registry.ObserveRequested(handle))
			}
		case 2:
			if driver.poll.phase != executorPollEpochBPublish || driver.poll.source != executorCatalogDone ||
				driver.poll.cursor != 0 || registry.ObserveRequested(handle) {
				t.Fatalf("ack/B boundary = %+v, requested=%t", driver.poll, registry.ObserveRequested(handle))
			}
		}
		if step == wantBudget && (!progress.Complete || progress.More || progress.Blocked || progress.Epochs != 2 ||
			driver.poll != (executorPollTransaction{})) {
			t.Fatalf("completed budget-one transaction = %+v, retained=%+v", progress, driver.poll)
		}
	}

	// The compatibility wrapper uses the same cursor engine with the exact
	// minimum budget and still finishes one full transaction in one call.
	if result := registry.Request(handle); result != ExecutorRequestPublished {
		t.Fatalf("publish compatibility request = %d", result)
	}
	if drained, promoted, ok := PollExecutor(driver); !ok || drained != 0 || promoted != 0 ||
		registry.ObserveRequested(handle) || driver.poll != (executorPollTransaction{}) {
		t.Fatalf("compatibility poll = (%d, %d, %t), requested=%t poll=%+v",
			drained, promoted, ok, registry.ObserveRequested(handle), driver.poll)
	}
	closeTestExecutorDriver(t, driver)
}

func TestExecutorPollSliceHotControlSourceCannotExtendCatalogPass(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	control := new(TaskControlSource)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{Control: control}) {
		t.Fatal("bind hot control source")
	}
	task := newYieldingTestG(t, "hot-control")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue hot-control task")
	}
	id, ok := RegisterTaskControl(control, p, task.g)
	if !ok {
		t.Fatal("register hot-control endpoint")
	}
	if result := control.Post(id, TaskCancelAbort); result != TaskControlPosted {
		t.Fatalf("post initial hot-control request = %d", result)
	}
	if result := registry.Request(handle); result != ExecutorRequestPublished {
		t.Fatalf("request initial hot-control poll = %d", result)
	}
	budget, ok := MinExecutorPollBudget(driver)
	if !ok {
		t.Fatal("minimum hot-control poll budget")
	}
	postedBehindA, postedBehindB := false, false
	var complete ExecutorPollProgress
	for step := uint32(0); step < budget; step++ {
		progress, polled := PollExecutorSlice(driver, 1)
		if !polled || progress.Used != 1 {
			t.Fatalf("hot-control step %d = (%+v, %t)", step, progress, polled)
		}
		if !postedBehindA && driver.poll.phase == executorPollEpochAPublish &&
			driver.poll.source == executorCatalogDone {
			if result := control.Post(id, TaskCancelShutdown); result != TaskControlPosted {
				t.Fatalf("post behind A cursor = %d", result)
			}
			if result := registry.Request(handle); result != ExecutorRequestCoalesced {
				t.Fatalf("request behind A cursor = %d", result)
			}
			postedBehindA = true
		}
		if !postedBehindB && driver.poll.phase == executorPollEpochBPublish &&
			driver.poll.source == executorCatalogDone {
			if result := control.Post(id, TaskCancelAbort); result != TaskControlPosted {
				t.Fatalf("post behind B cursor = %d", result)
			}
			if result := registry.Request(handle); result != ExecutorRequestPublished {
				t.Fatalf("request behind B cursor = %d", result)
			}
			postedBehindB = true
		}
		if progress.Complete {
			complete = progress
			break
		}
	}
	if !postedBehindA || !postedBehindB || !complete.Complete || !complete.More || complete.Blocked ||
		complete.Epochs != 2 || complete.Control != 2 || driver.poll != (executorPollTransaction{}) {
		t.Fatalf("hot source transaction = A:%t B:%t progress:%+v poll:%+v",
			postedBehindA, postedBehindB, complete, driver.poll)
	}
	if task.g.park.taskCancelKind != TaskCancelShutdown {
		t.Fatalf("hot source cancellation kind = %d, want shutdown", task.g.park.taskCancelKind)
	}

	// The request posted behind B belongs to a later transaction. Closing the
	// endpoint does not erase it; one final production poll drains it before
	// strong quiescence and retirement.
	if !BeginCloseTaskControl(control, p, id) {
		t.Fatal("begin hot-control close")
	}
	if _, _, ok := PollExecutor(driver); !ok {
		t.Fatal("drain hot-control request behind B")
	}
	if !ConfirmTaskControlQuiesced(control, p, id) || !RetireTaskControl(control, p, id) {
		t.Fatal("retire hot-control endpoint")
	}
	closeTestExecutorDriver(t, driver)
	runtime.KeepAlive(task.frame.memory)
}
