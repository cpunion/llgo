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

var (
	_ [56 - unsafe.Sizeof(ExecutorPollProgress{})]byte
	_ [unsafe.Sizeof(ExecutorPollProgress{}) - 56]byte
)

func TestExecutorPollProgressPODLayout(t *testing.T) {
	if alignment := unsafe.Alignof(ExecutorPollProgress{}); alignment != 4 && alignment != 8 {
		t.Fatalf("executor progress alignment = %d, want natural uint32/int64 alignment", alignment)
	}
	if unsafe.Offsetof(ExecutorPollProgress{}.Used) != 0 ||
		unsafe.Offsetof(ExecutorPollProgress{}.NextDeadline) != 40 ||
		unsafe.Offsetof(ExecutorPollProgress{}.Epochs) != 48 ||
		unsafe.Offsetof(ExecutorPollProgress{}.Complete) != 49 ||
		unsafe.Offsetof(ExecutorPollProgress{}.More) != 50 ||
		unsafe.Offsetof(ExecutorPollProgress{}.Blocked) != 51 ||
		unsafe.Offsetof(ExecutorPollProgress{}.HasDeadline) != 52 ||
		unsafe.Offsetof(ExecutorPollProgress{}.AtomicResolve) != 53 ||
		unsafe.Offsetof(ExecutorPollProgress{}.Overshot) != 54 {
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

func TestExecutorPollEpochBPreservesAExternalBlockOnly(t *testing.T) {
	transaction := executorPollTransaction{
		now:           17,
		phase:         executorPollAcknowledge,
		source:        executorCatalogDone,
		withDeadline:  true,
		retryBudget:   true,
		awaitExternal: true,
	}
	beginExecutorPollEpoch(&transaction, executorPollEpochBPublish)
	if transaction.retryBudget || !transaction.awaitExternal || !transaction.resampleNow ||
		transaction.phase != executorPollEpochBPublish || transaction.source != executorCatalogWaits || transaction.cursor != 0 {
		t.Fatalf("A-only external block across B transition = %+v", transaction)
	}
}

func TestMinExecutorPollBudgetCountsCompleteProductionCatalog(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	timers := new(TimerRegistrationTable)
	manual := new(ManualOperationSource)
	control := new(TaskControlSource)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{
		Waits: waits, Timers: timers, Manual: manual, Control: control,
	}) {
		t.Fatal("bind complete production catalog")
	}
	want := uint32(2*(WaitRegistrationCapacity+TimerRegistrationCapacity+ManualOperationSourceCapacity+TaskControlSourceCapacity+1) + 1)
	if budget, ok := MinExecutorPollBudget(driver); !ok || budget != want {
		t.Fatalf("complete catalog minimum = (%d, %t), want %d", budget, ok, want)
	}
	if progress, ok := PollExecutorSliceAt(driver, 0, want); !ok || !progress.Complete || progress.Used != want ||
		progress.Overshot || !progress.AtomicResolve || progress.Epochs != 2 {
		t.Fatalf("complete empty catalog poll = (%+v, %t)", progress, ok)
	}
	closeTestExecutorDriver(t, driver)
}

func TestExecutorPollSliceBudgetOneCompletesExactAcknowledgeTransaction(t *testing.T) {
	p := new(P)
	driver, registry, _, handle := bindTestExecutorDriver(t, p)
	wantBudget := uint32(2*(WaitRegistrationCapacity+1) + 1)
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
		case WaitRegistrationCapacity:
			if driver.poll.phase != executorPollEpochAPublish || driver.poll.source != executorCatalogDone ||
				driver.poll.total.epochs != 0 || !registry.ObserveRequested(handle) {
				t.Fatalf("A catalog boundary = %+v, requested=%t", driver.poll, registry.ObserveRequested(handle))
			}
		case WaitRegistrationCapacity + 1:
			if driver.poll.phase != executorPollAcknowledge || driver.poll.total.epochs != 1 ||
				!registry.ObserveRequested(handle) {
				t.Fatalf("A resolve boundary = %+v, requested=%t", driver.poll, registry.ObserveRequested(handle))
			}
		case WaitRegistrationCapacity + 2:
			if driver.poll.phase != executorPollEpochBPublish || driver.poll.source != executorCatalogWaits ||
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

func TestExecutorPollSliceDoesNotResolveBeforeCompleteEpochA(t *testing.T) {
	p := new(P)
	driver, registry, waits, handle := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "bounded-A")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue bounded-A task")
	}
	_, _, wait := parkRegisteredDriverTask(t, p, waits, task)
	if result := waits.Post(wait); result != WaitRegistrationPosted {
		t.Fatalf("post bounded-A wait = %d", result)
	}
	if result := registry.Request(handle); result != ExecutorRequestPublished {
		t.Fatalf("request bounded-A poll = %d", result)
	}

	progress, ok := PollExecutorSlice(driver, WaitRegistrationCapacity)
	if !ok || progress.Complete || !progress.More || progress.AtomicResolve || progress.Waits != 1 || progress.Promoted != 0 ||
		p.readyHead != nil || !HasWaiting(p) || !registry.ObserveRequested(handle) {
		t.Fatalf("A publication boundary = (%+v, %t), ready=%p waiting=%t requested=%t",
			progress, ok, p.readyHead, HasWaiting(p), registry.ObserveRequested(handle))
	}
	progress, ok = PollExecutorSlice(driver, 1)
	if !ok || progress.Complete || !progress.AtomicResolve || progress.Promoted != 1 || p.readyHead != task.g || HasWaiting(p) ||
		!registry.ObserveRequested(handle) {
		t.Fatalf("A resolution boundary = (%+v, %t), ready=%p waiting=%t requested=%t",
			progress, ok, p.readyHead, HasWaiting(p), registry.ObserveRequested(handle))
	}
	progress, ok = PollExecutorSlice(driver, 1)
	if !ok || progress.Complete || registry.ObserveRequested(handle) || driver.poll.phase != executorPollEpochBPublish {
		t.Fatalf("ack boundary = (%+v, %t), poll=%+v requested=%t",
			progress, ok, driver.poll, registry.ObserveRequested(handle))
	}
	progress, ok = PollExecutorSlice(driver, WaitRegistrationCapacity+1)
	if !ok || !progress.Complete || !progress.More || progress.Blocked || progress.Epochs != 2 || progress.Promoted != 1 {
		t.Fatalf("B completion boundary = (%+v, %t)", progress, ok)
	}

	retireCompletedRegistration(t, waits, wait)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !TerminalG(p, task.g) {
		t.Fatal("bounded-A task retained scheduler state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorPollSliceBlockedWaitDoesNotRequestBusyRetry(t *testing.T) {
	p := new(P)
	driver, registry, waits, handle := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "bounded-blocked")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue blocked task")
	}
	_, _, wait := parkRegisteredDriverTask(t, p, waits, task)
	budget, ok := MinExecutorPollBudget(driver)
	if !ok {
		t.Fatal("minimum blocked poll budget")
	}
	progress, ok := PollExecutorSlice(driver, budget)
	if !ok || !progress.Complete || progress.More || !progress.Blocked || progress.Completed != 0 ||
		!HasWaiting(p) || driver.poll != (executorPollTransaction{}) {
		t.Fatalf("blocked poll = (%+v, %t), waiting=%t poll=%+v", progress, ok, HasWaiting(p), driver.poll)
	}
	if result := PostWaitAndRequest(waits, wait, registry, handle); result.Wait != WaitRegistrationPosted ||
		result.Executor != ExecutorRequestPublished {
		t.Fatalf("wake blocked poll = %+v", result)
	}
	progress, ok = PollExecutorSlice(driver, budget)
	if !ok || !progress.Complete || !progress.More || progress.Blocked || progress.Promoted != 1 {
		t.Fatalf("woken blocked poll = (%+v, %t)", progress, ok)
	}
	retireCompletedRegistration(t, waits, wait)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorPollSliceFreezesTimerSampleAcrossHostEntries(t *testing.T) {
	p := new(P)
	driver, _, _, timers, _ := bindTestExecutorDriverWithTimers(t, p)
	firstToken, firstTicket := new(WaitToken), WaitTicket(0)
	secondToken, secondTicket := new(WaitToken), WaitTicket(0)
	var ok bool
	if firstTicket, ok = ArmWait(firstToken); !ok {
		t.Fatal("arm first bounded timer")
	}
	first, registered := timers.Register(p, firstToken, firstTicket, 10)
	if !registered {
		t.Fatal("register first bounded timer")
	}
	if secondTicket, ok = ArmWait(secondToken); !ok {
		t.Fatal("arm second bounded timer")
	}
	second, registered := timers.Register(p, secondToken, secondTicket, 20)
	if !registered {
		t.Fatal("register second bounded timer")
	}
	budget, ok := MinExecutorPollBudget(driver)
	if !ok {
		t.Fatal("minimum timer poll budget")
	}

	// Visit wait slots and timer slot zero at now=15, then yield. Slot one must
	// not observe a newer timestamp inside the same A/ack/B transaction.
	progress, ok := PollExecutorSliceAt(driver, 15, WaitRegistrationCapacity+1)
	if !ok || progress.Complete || progress.Timers != 1 || driver.poll.now != 15 || driver.poll.cursor != 1 {
		t.Fatalf("partial timed poll = (%+v, %t), state=%+v", progress, ok, driver.poll)
	}
	progress, ok = PollExecutorSliceAt(driver, 25, 1)
	if !ok || progress.Complete || driver.poll.now != 15 || driver.poll.cursor != 2 || progress.Timers != 1 {
		t.Fatalf("later sample changed frozen A epoch = (%+v, %t), state=%+v", progress, ok, driver.poll)
	}
	// Finish the rest of A and its acknowledgement exactly. Since B has not
	// visited a source slot yet, the next host entry may freeze a newer sample
	// for B without mixing timestamps within either epoch.
	remainingAAndAck := uint32(TimerRegistrationCapacity)
	progress, ok = PollExecutorSliceAt(driver, 99, remainingAAndAck)
	if !ok || progress.Complete || !driver.poll.resampleNow || driver.poll.phase != executorPollEpochBPublish {
		t.Fatalf("timed A/ack boundary = (%+v, %t), state=%+v", progress, ok, driver.poll)
	}
	progress, ok = PollExecutorSliceAt(driver, 25, budget)
	if !ok || !progress.Complete || progress.Timers != 2 || progress.HasDeadline {
		t.Fatalf("fresh B timed epoch = (%+v, %t)", progress, ok)
	}
	consumeRegisteredOutcome(t, firstToken, firstTicket, WaitOutcomeCompleted)
	if !timers.Retire(first) {
		t.Fatal("retire first bounded timer")
	}
	consumeRegisteredOutcome(t, secondToken, secondTicket, WaitOutcomeCompleted)
	if !timers.Retire(second) {
		t.Fatal("retire second bounded timer")
	}
	closeTestExecutorDriver(t, driver)
}

func TestExecutorPollSliceHotControlSourceCannotExtendCatalogPass(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	control := new(TaskControlSource)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{Waits: waits, Control: control}) {
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
			driver.poll.source == executorCatalogControl && driver.poll.cursor == 1 {
			if result := control.Post(id, TaskCancelShutdown); result != TaskControlPosted {
				t.Fatalf("post behind A cursor = %d", result)
			}
			if result := registry.Request(handle); result != ExecutorRequestCoalesced {
				t.Fatalf("request behind A cursor = %d", result)
			}
			postedBehindA = true
		}
		if !postedBehindB && driver.poll.phase == executorPollEpochBPublish &&
			driver.poll.source == executorCatalogControl && driver.poll.cursor == 1 {
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
