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

func bindTestExecutorDriver(t *testing.T, p *P) (*ExecutorDriver, *ExecutorRegistry, *WaitRegistrationTable, ExecutorHandle) {
	t.Helper()
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	handle := registerTestExecutor(t, registry)
	if !BindExecutor(driver, p, registry, handle, waits) {
		t.Fatal("bind test executor driver")
	}
	return driver, registry, waits, handle
}

func bindTestExecutorDriverWithTimers(t *testing.T, p *P) (*ExecutorDriver, *ExecutorRegistry, *WaitRegistrationTable, *TimerRegistrationTable, ExecutorHandle) {
	t.Helper()
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	timers := new(TimerRegistrationTable)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorWithTimers(driver, p, registry, handle, waits, timers) {
		t.Fatal("bind timer-aware test executor driver")
	}
	return driver, registry, waits, timers, handle
}

func bindTestExecutorDriverWithManual(t *testing.T, p *P) (*ExecutorDriver, *ExecutorRegistry, *WaitRegistrationTable, *ManualOperationSource, ExecutorHandle) {
	t.Helper()
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	manual := new(ManualOperationSource)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{Waits: waits, Manual: manual}) {
		t.Fatal("bind manual-source test executor driver")
	}
	return driver, registry, waits, manual, handle
}

func closeTestExecutorDriver(t *testing.T, driver *ExecutorDriver) {
	t.Helper()
	if !BeginExecutorClose(driver) {
		t.Fatal("begin test executor close")
	}
	if !ConfirmExecutorClose(driver) {
		t.Fatal("confirm test executor close")
	}
}

func parkRegisteredDriverTask(t *testing.T, p *P, waits *WaitRegistrationTable, task *yieldingTestG) (*WaitToken, WaitTicket, WaitRegistrationHandle) {
	t.Helper()
	g, ok := NextRunnable(p)
	if !ok || g != task.g {
		t.Fatalf("dequeue driver task = (%p, %t)", g, ok)
	}
	action := beginWaitTestResume(t, p, task)
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm driver wait")
	}
	wait, ok := waits.Register(p, token, ticket)
	if !ok {
		t.Fatal("register driver wait")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("prepare driver park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit driver park = (%+v, %t)", action, ok)
	}
	return token, ticket, wait
}

func parkRegisteredDriverTimer(t *testing.T, driver *ExecutorDriver, p *P, task *yieldingTestG, now, deadline int64) (*WaitToken, WaitTicket, TimerRegistrationHandle) {
	t.Helper()
	g, ok := NextRunnableAt(p, now)
	if !ok || g != task.g {
		t.Fatalf("dequeue timer driver task = (%p, %t)", g, ok)
	}
	action := beginWaitTestResume(t, p, task)
	token := new(WaitToken)
	ticket, timer, result := PrepareExecutorTimerRegistration(driver, token, deadline)
	if result != TimerRegistrationPrepared {
		t.Fatalf("prepare driver timer = (%d, %+v, %d)", ticket, timer, result)
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("prepare driver timer park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit driver timer park = (%+v, %t)", action, ok)
	}
	return token, ticket, timer
}

func parkRegisteredDriverWaitAt(t *testing.T, driver *ExecutorDriver, p *P, task *yieldingTestG, now int64) (*WaitToken, WaitTicket, WaitRegistrationHandle) {
	t.Helper()
	g, ok := NextRunnableAt(p, now)
	if !ok || g != task.g {
		t.Fatalf("dequeue timed wait driver task = (%p, %t)", g, ok)
	}
	action := beginWaitTestResume(t, p, task)
	token := new(WaitToken)
	ticket, wait, result := PrepareExecutorWaitRegistration(driver, token)
	if result != WaitRegistrationPrepared {
		t.Fatalf("prepare timed driver wait = (%d, %+v, %d)", ticket, wait, result)
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("prepare timed driver wait park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit timed driver wait park = (%+v, %t)", action, ok)
	}
	return token, ticket, wait
}

func yieldRunningDriverTask(t *testing.T, p *P, task *yieldingTestG, action Action) {
	t.Helper()
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatalf("prepare driver yield for G %s", task.name)
	}
	if yielded, ok := Resumed(p, task.g, action); !ok || yielded.Kind != ActionYield {
		t.Fatalf("commit driver yield for G %s = (%+v, %t)", task.name, yielded, ok)
	}
}

func finishReadyDriverTasks(t *testing.T, p *P, tasks map[*G]*yieldingTestG) {
	t.Helper()
	for {
		g, ok := NextRunnable(p)
		if !ok {
			t.Fatal("dequeue driver cleanup task")
		}
		if g == nil {
			return
		}
		task := tasks[g]
		if task == nil {
			t.Fatal("unknown driver cleanup task")
		}
		finishWaitTestTask(t, p, task, beginWaitTestResume(t, p, task))
	}
}

func TestExecutorDriverBindCloseLifecycle(t *testing.T) {
	p := new(P)
	driver, registry, waits, handle := bindTestExecutorDriver(t, p)
	if waits.CanRelease() {
		t.Fatal("bound wait table reported releasable")
	}
	if RequestSchedule(p) || preemptLoad(&p.schedule) != scheduleIdle {
		t.Fatal("legacy P request entered a bound executor")
	}
	if sleep, ok := PrepareExecutorSleep(driver); !ok || sleep {
		t.Fatalf("empty driver sleep = (%t, %t)", sleep, ok)
	}
	if BindExecutor(new(ExecutorDriver), p, registry, handle, new(WaitRegistrationTable)) {
		t.Fatal("P accepted a second executor binding")
	}
	if ConfirmExecutorClose(driver) {
		t.Fatal("confirmed executor close before begin/join")
	}
	main := &G{magic: gMagic, state: GDead}
	if BeginCommandShutdown(p, main) {
		t.Fatal("command shutdown crossed an active executor binding")
	}
	closeTestExecutorDriver(t, driver)
	if preemptLoad(&p.executorMode) != executorModeUnbound || p.executor != nil ||
		!waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("closed driver retained stable ownership")
	}
	if !BeginCommandShutdown(p, main) || !FinishCommandShutdown(p, main) || !TerminalG(p, main) {
		t.Fatal("unbound command shutdown did not reach terminal state")
	}
}

func TestExecutorDriverManualSourceUsesUnifiedQuietCutAndParkGate(t *testing.T) {
	p := new(P)
	driver, registry, waits, manual, executor := bindTestExecutorDriverWithManual(t, p)
	task := newYieldingTestG(t, "driver-manual")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue manual-source driver task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue manual-source driver task")
	}
	action := beginWaitTestResume(t, p, task)
	ticket, ok := BeginParkSet(&task.g.park, 2, 73)
	if !ok {
		t.Fatal("begin manual-source driver park")
	}
	var wait WaitSetRecord
	if !PrepareWaitSetRecord(&wait, task.g, ticket) {
		t.Fatal("prepare manual-source driver wait record")
	}
	first, firstOK := manual.ReserveAndAttachWait(p, &task.g.park, ticket, &wait, 101)
	second, secondOK := manual.ReserveAndAttachWait(p, &task.g.park, ticket, &wait, 202)
	if !firstOK || !secondOK || !SealParkSet(&task.g.park, ticket) {
		t.Fatal("attach manual-source driver candidates")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(task.g, task.handle, task.frame.header, ticket, &wait) {
		t.Fatal("prepare manual-source driver park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit manual-source driver park = (%+v, %t)", action, ok)
	}

	if posted := manual.Post(first); posted != ManualOperationPosted {
		t.Fatalf("post manual-source driver completion = %d", posted)
	}
	if requested := registry.Request(executor); requested != ExecutorRequestPublished {
		t.Fatalf("request manual-source driver poll = %d", requested)
	}
	if drained, promoted, ok := PollExecutor(driver); !ok || drained != 1 || promoted != 1 {
		t.Fatalf("poll manual-source driver = (%d, %d, %t)", drained, promoted, ok)
	}
	firstSlot, _ := manualOperationSlotFor(manual, first)
	secondSlot, _ := manualOperationSlotFor(manual, second)
	if firstSlot.record.disposition != OperationDispositionWinner || secondSlot.record.disposition != OperationDispositionLost ||
		firstSlot.record.phase != operationDetached || secondSlot.record.phase != operationDetached || HasWaiting(p) {
		t.Fatal("unified manual-source transaction did not resolve and detach every candidate")
	}

	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue manual-source promoted task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(task.g, ticket)
	leaseID, leaseOK := lease.ID()
	if !ok || outcome != ParkOutcomeCompleted || caseID != 101 || taskCancel != TaskCancelNone || !leaseOK || leaseID != first {
		t.Fatalf("take manual-source driver decision = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, ok)
	}
	if !manual.ConfirmQuiesced(p, first) || !manual.ConfirmQuiesced(p, second) ||
		!manual.TakeResult(p, lease) || !manual.Recycle(p, first) || !manual.Recycle(p, second) {
		t.Fatal("release manual-source driver operations")
	}
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !TerminalG(p, task.g) || !manual.CanRelease() || !waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("manual-source driver cleanup retained state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorDriverManualCancellationMarksFrameLocalWaitSet(t *testing.T) {
	p := new(P)
	driver, registry, waits, manual, _ := bindTestExecutorDriverWithManual(t, p)
	task := newYieldingTestG(t, "driver-manual-cancel")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue manual-cancel task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue manual-cancel task")
	}
	action := beginWaitTestResume(t, p, task)
	ticket, ok := BeginParkSet(&task.g.park, 1, 79)
	var wait WaitSetRecord
	if !ok || !PrepareWaitSetRecord(&wait, task.g, ticket) {
		t.Fatal("begin manual-cancel wait-set")
	}
	id, attached := manual.ReserveAndAttachWait(p, &task.g.park, ticket, &wait, 303)
	if !attached || !SealParkSet(&task.g.park, ticket) {
		t.Fatal("attach manual-cancel operation")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(task.g, task.handle, task.frame.header, ticket, &wait) {
		t.Fatal("prepare manual-cancel park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark ||
		p.parkWaitHead != &wait || p.parkWaitTail != &wait || FrameFromStorage(task.frame.storage).parkWait != &wait {
		t.Fatalf("commit manual-cancel park = (%+v, %t)", action, ok)
	}
	if !manual.RequestCancel(p, &wait) {
		t.Fatal("mark manual cancellation")
	}
	if drained, promoted, pollOK := PollExecutor(driver); !pollOK || drained != 0 || promoted != 1 ||
		wait != (WaitSetRecord{}) || p.parkWaitHead != nil || p.parkWaitTail != nil {
		t.Fatalf("poll manual cancellation = (%d, %d, %t)", drained, promoted, pollOK)
	}
	if g, nextOK := NextRunnable(p); !nextOK || g != task.g {
		t.Fatal("dequeue manual-canceled task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskCancel, decisionOK := TakeRunDecision(task.g, ticket)
	if !decisionOK || outcome != ParkOutcomeCanceled || caseID != 0 || lease != (OperationResultLease{}) || taskCancel != TaskCancelNone {
		t.Fatalf("take manual cancellation = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, decisionOK)
	}
	if !manual.ConfirmQuiesced(p, id) || !manual.Recycle(p, id) {
		t.Fatal("release manual-canceled operation")
	}
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !TerminalG(p, task.g) || !manual.CanRelease() || !waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("manual-canceled task retained state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorDriverTimerBindingIsTransactionalAndAPIFamiliesDoNotMix(t *testing.T) {
	legacyP := new(P)
	legacy, _, _, _ := bindTestExecutorDriver(t, legacyP)
	if waits, timers, promoted, ok := PollExecutorAt(legacy, 0); ok || waits != 0 || timers != 0 || promoted != 0 {
		t.Fatalf("timed poll accepted legacy binding = (%d, %d, %d, %t)", waits, timers, promoted, ok)
	}
	if prepared, ok := PrepareExecutorSleepAt(legacy, 0); ok || prepared {
		t.Fatalf("timed sleep prepare accepted legacy binding = (%t, %t)", prepared, ok)
	}
	if deadline, has, ok := NextExecutorTimerDeadline(legacy); ok || has || deadline != 0 {
		t.Fatalf("legacy timer deadline query = (%d, %t, %t)", deadline, has, ok)
	}
	closeTestExecutorDriver(t, legacy)

	badTimers := new(TimerRegistrationTable)
	other := new(P)
	if !bindTimerRegistrationTable(badTimers, other) {
		t.Fatal("bind conflicting timer owner")
	}
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	executor := registerTestExecutor(t, registry)
	if BindExecutorWithTimers(driver, p, registry, executor, waits, badTimers) {
		t.Fatal("bound driver to an already-owned timer table")
	}
	if *driver != (ExecutorDriver{}) || p.executor != nil || preemptLoad(&p.executorMode) != executorModeUnbound ||
		!waits.CanRelease() || badTimers.owner != other {
		t.Fatal("rejected timer bind retained a partial wait/P binding")
	}
	if !unbindTimerRegistrationTable(badTimers, other) {
		t.Fatal("unbind conflicting timer owner")
	}
	retireTestExecutor(t, registry, executor)

	timedP := new(P)
	timed, timedRegistry, timedWaits, timedTimers, timedExecutor := bindTestExecutorDriverWithTimers(t, timedP)
	if drained, promoted, ok := PollExecutor(timed); ok || drained != 0 || promoted != 0 {
		t.Fatalf("legacy poll accepted timer binding = (%d, %d, %t)", drained, promoted, ok)
	}
	if sleep, ok := PrepareExecutorSleep(timed); ok || sleep {
		t.Fatalf("legacy sleep accepted timer binding = (%t, %t)", sleep, ok)
	}
	if drained, promoted, ok := WakeExecutor(timed); ok || drained != 0 || promoted != 0 {
		t.Fatalf("legacy wake accepted timer binding = (%d, %d, %t)", drained, promoted, ok)
	}
	if g, ok := NextRunnable(timedP); ok || g != nil {
		t.Fatalf("legacy dequeue crossed timer binding = (%p, %t)", g, ok)
	}
	if g, ok := NextRunnableAt(timedP, -1); ok || g != nil {
		t.Fatalf("negative timed dequeue = (%p, %t)", g, ok)
	}
	if g, ok := NextRunnableAt(timedP, 0); !ok || g != nil {
		t.Fatalf("empty timed dequeue = (%p, %t)", g, ok)
	}
	closeTestExecutorDriver(t, timed)
	if !timedWaits.CanRelease() || !timedTimers.CanRelease() || !timedRegistry.CanRelease() ||
		timedExecutor == (ExecutorHandle{}) {
		t.Fatal("timer-aware close retained stable ownership")
	}
}

func TestExecutorDriverTimerOwnerPrepareRollbackCancelAndRetire(t *testing.T) {
	p := new(P)
	driver, registry, waits, timers, _ := bindTestExecutorDriverWithTimers(t, p)
	task := newYieldingTestG(t, "driver-timer-owner")
	var token WaitToken
	if ticket, timer, result := PrepareExecutorTimerRegistration(driver, &token, 100); result != TimerRegistrationPrepareInvalid ||
		ticket != 0 || timer != (TimerRegistrationHandle{}) {
		t.Fatalf("idle timer owner prepare = (%d, %+v, %d)", ticket, timer, result)
	}
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue timer-owner task")
	}
	if next, ok := NextRunnableAt(p, 0); !ok || next != task.g {
		t.Fatal("dequeue timer-owner task")
	}
	action := beginWaitTestResume(t, p, task)
	savedAction := p.action
	p.action.Kind = ActionCheckResume
	if ticket, timer, result := PrepareExecutorTimerRegistration(driver, &token, 100); result != TimerRegistrationPrepareInvalid ||
		ticket != 0 || timer != (TimerRegistrationHandle{}) {
		t.Fatalf("wrong-action timer owner prepare = (%d, %+v, %d)", ticket, timer, result)
	}
	p.action = savedAction
	ticket, timer, result := PrepareExecutorTimerRegistration(driver, &token, 100)
	if result != TimerRegistrationPrepared || ticket != 1 || timer == (TimerRegistrationHandle{}) {
		t.Fatalf("running timer owner prepare = (%d, %+v, %d)", ticket, timer, result)
	}
	if !RollbackExecutorTimerRegistration(driver, &token, ticket, timer) {
		t.Fatal("running timer owner rollback")
	}
	ticket, timer, result = PrepareExecutorTimerRegistration(driver, &token, 100)
	if result != TimerRegistrationPrepared || ticket != 2 || timer == (TimerRegistrationHandle{}) {
		t.Fatalf("second timer owner prepare = (%d, %+v, %d)", ticket, timer, result)
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, &token, ticket) {
		t.Fatal("prepare timer-owner park")
	}
	if parked, ok := Resumed(p, task.g, action); !ok || parked.Kind != ActionPark {
		t.Fatalf("commit timer-owner park = (%+v, %t)", parked, ok)
	}
	if RetireCompletedExecutorTimer(driver, &token, ticket, timer) || BeginExecutorClose(driver) {
		t.Fatal("idle owner retired or closed a live timer")
	}
	if deadline, has, ok := NextExecutorTimerDeadline(driver); !ok || !has || deadline != 100 {
		t.Fatalf("active timer deadline = (%d, %t, %t)", deadline, has, ok)
	}
	if waitCount, timerCount, promoted, ok := PollExecutorAt(driver, 100); !ok || waitCount != 0 || timerCount != 1 || promoted != 1 {
		t.Fatalf("complete owner timer = (%d, %d, %d, %t)", waitCount, timerCount, promoted, ok)
	}
	if BeginExecutorClose(driver) {
		t.Fatal("closed with delivered but unretired timer")
	}
	if next, ok := NextRunnableAt(p, 100); !ok || next != task.g {
		t.Fatal("dequeue completed timer-owner task")
	}
	action = beginWaitTestResume(t, p, task)
	if !RetireCompletedExecutorTimer(driver, &token, ticket, timer) {
		t.Fatal("retire completed timer from resumed owner")
	}

	var canceledToken WaitToken
	canceledTicket, canceledTimer, result := PrepareExecutorTimerRegistration(driver, &canceledToken, 200)
	if result != TimerRegistrationPrepared || CancelExecutorTimerRegistration(driver, canceledTimer) != WaitCancelWon {
		t.Fatal("prepare and cancel running-owner timer")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, &canceledToken, canceledTicket) {
		t.Fatal("prepare canceled timer park")
	}
	if parked, ok := Resumed(p, task.g, action); !ok || parked.Kind != ActionPark {
		t.Fatalf("commit canceled timer park = (%+v, %t)", parked, ok)
	}
	if BeginExecutorClose(driver) {
		t.Fatal("closed with canceled but unretired timer")
	}
	if waitCount, timerCount, promoted, ok := PollExecutorAt(driver, 100); !ok || waitCount != 0 || timerCount != 0 || promoted != 1 {
		t.Fatalf("promote canceled owner timer = (%d, %d, %d, %t)", waitCount, timerCount, promoted, ok)
	}
	if next, ok := NextRunnableAt(p, 100); !ok || next != task.g {
		t.Fatal("dequeue canceled timer-owner task")
	}
	action = beginWaitTestResume(t, p, task)
	if !RetireCanceledExecutorTimer(driver, &canceledToken, canceledTicket, canceledTimer) {
		t.Fatal("retire canceled timer from resumed owner")
	}
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !TerminalG(p, task.g) || !waits.CanRelease() || !timers.CanRelease() || !registry.CanRelease() {
		t.Fatal("timer owner ABI cleanup retained state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestWaitRegistrationSchedulerDrainDoesNotRequestLegacyP(t *testing.T) {
	p := new(P)
	table := new(WaitRegistrationTable)
	token, ticket, wait := registerTestWait(t, table, p)
	if result := table.Post(wait); result != WaitRegistrationPosted {
		t.Fatalf("post standalone completion = %d", result)
	}
	if drained, ok := table.Drain(); !ok || drained != 1 {
		t.Fatalf("standalone drain = (%d, %t)", drained, ok)
	}
	if preemptLoad(&p.schedule) != scheduleIdle {
		t.Fatal("scheduler-owned drain published a legacy P request")
	}
	consumeRegisteredOutcome(t, token, ticket, WaitOutcomeCompleted)
	retireCompletedRegistration(t, table, wait)

	cancelToken, cancelTicket, cancelWait := registerTestWait(t, table, p)
	if table.BeginClose(cancelWait) != WaitRegistrationCloseStarted {
		t.Fatal("begin standalone cancellation")
	}
	if result, ok := table.ConfirmQuiesced(cancelWait); !ok || result != WaitCancelWon {
		t.Fatalf("confirm standalone cancellation = (%d, %t)", result, ok)
	}
	if preemptLoad(&p.schedule) != scheduleIdle {
		t.Fatal("scheduler-owned cancellation published a legacy P request")
	}
	consumeRegisteredOutcome(t, cancelToken, cancelTicket, WaitOutcomeCanceled)
	if !table.Retire(cancelWait) {
		t.Fatal("retire standalone cancellation")
	}
}

func TestExecutorDriverPollPreemptObservesUntilSchedulerAck(t *testing.T) {
	p := new(P)
	driver, registry, waits, executor := bindTestExecutorDriver(t, p)
	parked := newYieldingTestG(t, "driver-parked")
	competitor := newYieldingTestG(t, "driver-competitor")
	if !Enqueue(p, parked.g) || !Enqueue(p, competitor.g) {
		t.Fatal("enqueue driver tasks")
	}
	token, ticket, wait := parkRegisteredDriverTask(t, p, waits, parked)

	g, ok := NextRunnable(p)
	if !ok || g != competitor.g {
		t.Fatal("dequeue driver competitor")
	}
	action := beginWaitTestResume(t, p, competitor)
	result := PostWaitAndRequest(waits, wait, registry, executor)
	if result.Wait != WaitRegistrationPosted || result.Executor != ExecutorRequestPublished {
		t.Fatalf("running driver ingress = %+v", result)
	}
	if !PollPreempt(competitor.g) || !PollPreempt(competitor.g) || !registry.ObserveRequested(executor) {
		t.Fatal("running polls consumed the executor request before handoff")
	}
	competitor.frame.header.SuspendReason = uint16(SuspendYield)
	competitor.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(competitor.g, competitor.handle, competitor.frame.header) {
		t.Fatal("prepare executor-request yield")
	}
	if action, ok = Resumed(p, competitor.g, action); !ok || action.Kind != ActionYield {
		t.Fatalf("commit executor-request yield = (%+v, %t)", action, ok)
	}
	if promoted, ok := PollReady(p); !ok || promoted != 1 {
		t.Fatalf("bound scheduler poll = (%d, %t)", promoted, ok)
	}
	if registry.ObserveRequested(executor) || HasWaiting(p) {
		t.Fatal("scheduler handoff did not drain and acknowledge executor")
	}
	if outcome, ok := WaitOutcomeOf(token, ticket); !ok || outcome != WaitOutcomeCompleted {
		t.Fatalf("driver completion outcome = (%d, %t)", outcome, ok)
	}
	retireCompletedRegistration(t, waits, wait)
	closeTestExecutorDriver(t, driver)

	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{parked.g: parked, competitor.g: competitor})
	if !TerminalG(p, parked.g) || !TerminalG(p, competitor.g) {
		t.Fatal("driver poll cleanup retained scheduler state")
	}
	runtime.KeepAlive(parked.frame.memory)
	runtime.KeepAlive(competitor.frame.memory)
}

func TestExecutorDriverRetainedSleepAndSpuriousWake(t *testing.T) {
	p := new(P)
	driver, registry, waits, executor := bindTestExecutorDriver(t, p)
	parked := newYieldingTestG(t, "driver-idle")
	if !Enqueue(p, parked.g) {
		t.Fatal("enqueue idle driver task")
	}
	_, _, wait := parkRegisteredDriverTask(t, p, waits, parked)
	if BeginExecutorClose(driver) {
		t.Fatal("closed driver with a live parked registration")
	}

	if sleep, ok := PrepareExecutorSleep(driver); !ok || !sleep || driver.state != executorDriverSleeping {
		t.Fatalf("prepare first driver sleep = (%t, %t), state=%d", sleep, ok, driver.state)
	}
	if drained, promoted, ok := WakeExecutor(driver); !ok || drained != 0 || promoted != 0 || !HasWaiting(p) {
		t.Fatalf("spurious driver wake = (%d, %d, %t)", drained, promoted, ok)
	}
	if sleep, ok := PrepareExecutorSleep(driver); !ok || !sleep {
		t.Fatalf("prepare second driver sleep = (%t, %t)", sleep, ok)
	}

	doorbell := make(chan struct{}, 1)
	result := PostWaitAndRequest(waits, wait, registry, executor)
	if result.Wait != WaitRegistrationPosted || result.Executor != ExecutorRequestIdleWake ||
		!ExecutorRequestNeedsDoorbell(result.Executor) {
		t.Fatalf("sleeping driver ingress = %+v", result)
	}
	select {
	case doorbell <- struct{}{}:
	default:
	}
	select {
	case <-doorbell:
	default:
		t.Fatal("driver doorbell delivered before block was not retained")
	}
	if drained, promoted, ok := WakeExecutor(driver); !ok || drained != 1 || promoted != 1 || HasWaiting(p) {
		t.Fatalf("completion driver wake = (%d, %d, %t)", drained, promoted, ok)
	}
	retireCompletedRegistration(t, waits, wait)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{parked.g: parked})
	if !TerminalG(p, parked.g) {
		t.Fatal("idle driver cleanup retained scheduler state")
	}
	runtime.KeepAlive(parked.frame.memory)
}

func TestExecutorDriverTimerSleepUsesFreshFinalAndWakeSamples(t *testing.T) {
	p := new(P)
	driver, registry, waits, timers, _ := bindTestExecutorDriverWithTimers(t, p)
	task := newYieldingTestG(t, "driver-timer-sleep")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue timer-sleep task")
	}
	token, ticket, timer := parkRegisteredDriverTimer(t, driver, p, task, 0, 100)

	if prepared, ok := PrepareExecutorSleepAt(driver, 90); !ok || !prepared || driver.state != executorDriverIdlePreparing {
		t.Fatalf("prepare timer sleep = (%t, %t), state=%d", prepared, ok, driver.state)
	}
	if _, _, _, ok := PollExecutorAt(driver, 90); ok || BeginExecutorClose(driver) {
		t.Fatal("idle-preparing state admitted poll or close")
	}
	if sleep, deadline, has, ok := CommitExecutorSleepAt(driver, 90); !ok || !sleep || !has || deadline != 100 ||
		driver.state != executorDriverSleeping {
		t.Fatalf("commit future timer sleep = (%t, %d, %t, %t), state=%d", sleep, deadline, has, ok, driver.state)
	}
	if waitCount, timerCount, promoted, ok := WakeExecutorAt(driver, 90); !ok || waitCount != 0 || timerCount != 0 ||
		promoted != 0 || !HasWaiting(p) || driver.state != executorDriverActive {
		t.Fatalf("fresh spurious timer wake = (%d, %d, %d, %t), state=%d", waitCount, timerCount, promoted, ok, driver.state)
	}

	if prepared, ok := PrepareExecutorSleepAt(driver, 95); !ok || !prepared {
		t.Fatalf("prepare abortable timer sleep = (%t, %t)", prepared, ok)
	}
	if sleep, deadline, has, ok := CommitExecutorSleepAt(driver, 94); ok || sleep || has || deadline != 0 ||
		driver.state != executorDriverActive {
		t.Fatalf("backward final sample did not abort = (%t, %d, %t, %t), state=%d", sleep, deadline, has, ok, driver.state)
	}

	if prepared, ok := PrepareExecutorSleepAt(driver, 99); !ok || !prepared {
		t.Fatalf("prepare final-due timer sleep = (%t, %t)", prepared, ok)
	}
	if sleep, deadline, has, ok := CommitExecutorSleepAt(driver, 100); !ok || sleep || has || deadline != 0 ||
		driver.state != executorDriverActive || HasWaiting(p) || p.readyHead != task.g {
		t.Fatalf("timer due in final scan = (%t, %d, %t, %t), state=%d", sleep, deadline, has, ok, driver.state)
	}
	if next, ok := NextRunnableAt(p, 100); !ok || next != task.g {
		t.Fatal("dequeue timer after final scan")
	}
	action := beginWaitTestResume(t, p, task)
	if !RetireCompletedExecutorTimer(driver, token, ticket, timer) {
		t.Fatal("retire final-scan timer")
	}
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !TerminalG(p, task.g) || !waits.CanRelease() || !timers.CanRelease() || !registry.CanRelease() {
		t.Fatal("timer sleep cleanup retained state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorDriverTimerDueBeforeIdleArmRefusesSleep(t *testing.T) {
	p := new(P)
	driver, _, _, timers, _ := bindTestExecutorDriverWithTimers(t, p)
	task := newYieldingTestG(t, "driver-timer-due-before-arm")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue due-before-arm task")
	}
	token, ticket, timer := parkRegisteredDriverTimer(t, driver, p, task, 0, 50)
	if prepared, ok := PrepareExecutorSleepAt(driver, 50); !ok || prepared || driver.state != executorDriverActive ||
		HasWaiting(p) || p.readyHead != task.g {
		t.Fatalf("due-before-arm sleep = (%t, %t), state=%d", prepared, ok, driver.state)
	}
	if deadline, has, ok := NextExecutorTimerDeadline(driver); !ok || has || deadline != 0 {
		t.Fatalf("delivered timer remained an active deadline = (%d, %t, %t)", deadline, has, ok)
	}
	if next, ok := NextRunnableAt(p, 50); !ok || next != task.g {
		t.Fatal("dequeue due-before-arm task")
	}
	action := beginWaitTestResume(t, p, task)
	if !RetireCompletedExecutorTimer(driver, token, ticket, timer) {
		t.Fatal("retire due-before-arm timer")
	}
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !TerminalG(p, task.g) || !timers.CanRelease() {
		t.Fatal("due-before-arm cleanup retained state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorDriverPostBetweenTimedSleepPhasesWinsCommit(t *testing.T) {
	p := new(P)
	driver, registry, waits, timers, executor := bindTestExecutorDriverWithTimers(t, p)
	waitTask := newYieldingTestG(t, "driver-between-phase-wait")
	timerTask := newYieldingTestG(t, "driver-between-phase-timer")
	if !Enqueue(p, waitTask.g) || !Enqueue(p, timerTask.g) {
		t.Fatal("enqueue between-phase tasks")
	}
	waitToken, waitTicket, wait := parkRegisteredDriverWaitAt(t, driver, p, waitTask, 0)
	timerToken, timerTicket, timer := parkRegisteredDriverTimer(t, driver, p, timerTask, 0, 100)
	if prepared, ok := PrepareExecutorSleepAt(driver, 90); !ok || !prepared {
		t.Fatalf("prepare between-phase sleep = (%t, %t)", prepared, ok)
	}
	posted := PostWaitAndRequest(waits, wait, registry, executor)
	if posted.Wait != WaitRegistrationPosted || posted.Executor != ExecutorRequestIdleWake {
		t.Fatalf("between-phase post = %+v", posted)
	}
	if sleep, deadline, has, ok := CommitExecutorSleepAt(driver, 90); !ok || sleep || has || deadline != 0 ||
		driver.state != executorDriverActive || p.readyHead != waitTask.g || !HasWaiting(p) {
		t.Fatalf("post won timed commit = (%t, %d, %t, %t), state=%d", sleep, deadline, has, ok, driver.state)
	}
	if deadline, has, ok := NextExecutorTimerDeadline(driver); !ok || !has || deadline != 100 {
		t.Fatalf("future timer lost after post won = (%d, %t, %t)", deadline, has, ok)
	}
	if next, ok := NextRunnableAt(p, 90); !ok || next != waitTask.g {
		t.Fatal("dequeue between-phase wait task")
	}
	action := beginWaitTestResume(t, p, waitTask)
	if !RetireCompletedExecutorWait(driver, waitToken, waitTicket, wait) {
		t.Fatal("retire between-phase wait")
	}
	finishWaitTestTask(t, p, waitTask, action)

	if waitCount, timerCount, promoted, ok := PollExecutorAt(driver, 100); !ok || waitCount != 0 || timerCount != 1 || promoted != 1 {
		t.Fatalf("complete preserved future timer = (%d, %d, %d, %t)", waitCount, timerCount, promoted, ok)
	}
	if next, ok := NextRunnableAt(p, 100); !ok || next != timerTask.g {
		t.Fatal("dequeue preserved timer task")
	}
	action = beginWaitTestResume(t, p, timerTask)
	if !RetireCompletedExecutorTimer(driver, timerToken, timerTicket, timer) {
		t.Fatal("retire preserved timer")
	}
	yieldRunningDriverTask(t, p, timerTask, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{timerTask.g: timerTask})
	if !TerminalG(p, waitTask.g) || !TerminalG(p, timerTask.g) || !waits.CanRelease() || !timers.CanRelease() {
		t.Fatal("between-phase cleanup retained state")
	}
	runtime.KeepAlive(waitTask.frame.memory)
	runtime.KeepAlive(timerTask.frame.memory)
}

func TestExecutorDriverWaitPostAndDueTimerDrainInOneTransaction(t *testing.T) {
	p := new(P)
	driver, registry, waits, timers, executor := bindTestExecutorDriverWithTimers(t, p)
	waitTask := newYieldingTestG(t, "driver-mixed-wait")
	timerTask := newYieldingTestG(t, "driver-mixed-timer")
	if !Enqueue(p, waitTask.g) || !Enqueue(p, timerTask.g) {
		t.Fatal("enqueue mixed-source tasks")
	}
	waitToken, waitTicket, wait := parkRegisteredDriverWaitAt(t, driver, p, waitTask, 0)
	timerToken, timerTicket, timer := parkRegisteredDriverTimer(t, driver, p, timerTask, 0, 100)
	if posted := PostWaitAndRequest(waits, wait, registry, executor); posted.Wait != WaitRegistrationPosted ||
		posted.Executor != ExecutorRequestPublished {
		t.Fatalf("mixed source post = %+v", posted)
	}
	if waitCount, timerCount, promoted, ok := PollExecutorAt(driver, 100); !ok || waitCount != 1 || timerCount != 1 || promoted != 2 {
		t.Fatalf("mixed source transaction = (%d, %d, %d, %t)", waitCount, timerCount, promoted, ok)
	}
	if HasWaiting(p) || p.readyHead != waitTask.g || p.readyTail != timerTask.g {
		t.Fatal("mixed source promotion lost wait insertion order")
	}
	if next, ok := NextRunnableAt(p, 100); !ok || next != waitTask.g {
		t.Fatal("dequeue mixed wait task")
	}
	action := beginWaitTestResume(t, p, waitTask)
	if !RetireCompletedExecutorWait(driver, waitToken, waitTicket, wait) {
		t.Fatal("retire mixed wait")
	}
	finishWaitTestTask(t, p, waitTask, action)
	if next, ok := NextRunnableAt(p, 100); !ok || next != timerTask.g {
		t.Fatal("dequeue mixed timer task")
	}
	action = beginWaitTestResume(t, p, timerTask)
	if !RetireCompletedExecutorTimer(driver, timerToken, timerTicket, timer) {
		t.Fatal("retire mixed timer")
	}
	yieldRunningDriverTask(t, p, timerTask, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{timerTask.g: timerTask})
	if !TerminalG(p, waitTask.g) || !TerminalG(p, timerTask.g) || !waits.CanRelease() || !timers.CanRelease() {
		t.Fatal("mixed source cleanup retained state")
	}
	runtime.KeepAlive(waitTask.frame.memory)
	runtime.KeepAlive(timerTask.frame.memory)
}

func TestExecutorDriverEnforcesWaitTableOwner(t *testing.T) {
	p := new(P)
	driver, registry, waits, executor := bindTestExecutorDriver(t, p)
	wrongP := new(P)
	wrongToken := new(WaitToken)
	wrongTicket, ok := ArmWait(wrongToken)
	if !ok {
		t.Fatal("arm wrong-owner wait")
	}
	if wait, ok := waits.Register(wrongP, wrongToken, wrongTicket); ok || wait != (WaitRegistrationHandle{}) {
		t.Fatalf("register wrong-owner wait = (%+v, %t)", wait, ok)
	}

	token, ticket, wait := registerTestWait(t, waits, p)
	if result := PostWaitAndRequest(waits, wait, registry, executor); result.Wait != WaitRegistrationPosted || result.Executor != ExecutorRequestPublished {
		t.Fatalf("owned ingress = %+v", result)
	}
	if drained, ok := waits.Drain(); ok || drained != 0 {
		t.Fatalf("direct drain bypassed bound driver = (%d, %t)", drained, ok)
	}
	if drained, promoted, ok := PollExecutor(driver); !ok || drained != 1 || promoted != 0 {
		t.Fatalf("owned driver poll = (%d, %d, %t)", drained, promoted, ok)
	}
	consumeRegisteredOutcome(t, token, ticket, WaitOutcomeCompleted)
	retireCompletedRegistration(t, waits, wait)
	closeTestExecutorDriver(t, driver)
}

func TestExecutorDriverWaitOwnerABIPrepareRollbackAndRetire(t *testing.T) {
	p := new(P)
	driver, registry, waits, executor := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "driver-wait-owner-abi")
	var token WaitToken
	if ticket, wait, result := PrepareExecutorWaitRegistration(new(ExecutorDriver), &token); result != WaitRegistrationPrepareInvalid ||
		ticket != 0 || wait != (WaitRegistrationHandle{}) {
		t.Fatalf("unbound owner prepare = (%d, %+v, %d)", ticket, wait, result)
	}
	if ticket, wait, result := PrepareExecutorWaitRegistration(driver, &token); result != WaitRegistrationPrepareInvalid ||
		ticket != 0 || wait != (WaitRegistrationHandle{}) {
		t.Fatalf("idle owner prepare = (%d, %+v, %d)", ticket, wait, result)
	}
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue wait-owner task")
	}
	if next, ok := NextRunnable(p); !ok || next != task.g {
		t.Fatal("dequeue wait-owner task")
	}
	action := beginWaitTestResume(t, p, task)
	savedAction := p.action
	p.action.Kind = ActionCheckResume
	if ticket, wait, result := PrepareExecutorWaitRegistration(driver, &token); result != WaitRegistrationPrepareInvalid ||
		ticket != 0 || wait != (WaitRegistrationHandle{}) {
		t.Fatalf("wrong-action owner prepare = (%d, %+v, %d)", ticket, wait, result)
	}
	p.action = savedAction
	ticket, wait, result := PrepareExecutorWaitRegistration(driver, &token)
	if result != WaitRegistrationPrepared || ticket != 1 || wait == (WaitRegistrationHandle{}) {
		t.Fatalf("running owner prepare = (%d, %+v, %d)", ticket, wait, result)
	}
	if !RollbackExecutorWaitRegistration(driver, &token, ticket, wait) {
		t.Fatal("running owner rollback")
	}
	ticket, wait, result = PrepareExecutorWaitRegistration(driver, &token)
	if result != WaitRegistrationPrepared || ticket != 2 || wait == (WaitRegistrationHandle{}) {
		t.Fatalf("second running owner prepare = (%d, %+v, %d)", ticket, wait, result)
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, &token, ticket) {
		t.Fatal("prepare wait-owner park")
	}
	if parked, ok := Resumed(p, task.g, action); !ok || parked.Kind != ActionPark {
		t.Fatalf("commit wait-owner park = (%+v, %t)", parked, ok)
	}
	if RetireCompletedExecutorWait(driver, &token, ticket, wait) {
		t.Fatal("retired wait outside resumed owner")
	}
	posted := PostWaitAndRequest(waits, wait, registry, executor)
	if posted.Wait != WaitRegistrationPosted || posted.Executor != ExecutorRequestPublished {
		t.Fatalf("post wait-owner completion = %+v", posted)
	}
	if drained, promoted, ok := PollExecutor(driver); !ok || drained != 1 || promoted != 1 {
		t.Fatalf("poll wait-owner completion = (%d, %d, %t)", drained, promoted, ok)
	}
	if next, ok := NextRunnable(p); !ok || next != task.g {
		t.Fatal("dequeue resumed wait-owner task")
	}
	action = beginWaitTestResume(t, p, task)
	if !RetireCompletedExecutorWait(driver, &token, ticket, wait) {
		t.Fatal("retire completed wait from resumed owner")
	}
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare wait-owner close yield")
	}
	if yielded, ok := Resumed(p, task.g, action); !ok || yielded.Kind != ActionYield {
		t.Fatalf("commit wait-owner close yield = (%+v, %t)", yielded, ok)
	}
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !TerminalG(p, task.g) || !waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("wait-owner ABI cleanup retained state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorDriverFindsPostBeforeDelayedRequest(t *testing.T) {
	p := new(P)
	driver, registry, waits, executor := bindTestExecutorDriver(t, p)
	parked := newYieldingTestG(t, "driver-post-window")
	if !Enqueue(p, parked.g) {
		t.Fatal("enqueue post-window task")
	}
	_, _, wait := parkRegisteredDriverTask(t, p, waits, parked)

	// Model a platform shim paused after durable Post and before Request. The
	// driver scans Posted states directly, so it promotes the waiter and refuses
	// to sleep without relying on the delayed advisory request.
	if result := waits.Post(wait); result != WaitRegistrationPosted {
		t.Fatalf("post before delayed request = %d", result)
	}
	if sleep, ok := PrepareExecutorSleep(driver); !ok || sleep || HasWaiting(p) || p.readyHead != parked.g {
		t.Fatalf("prepare sleep across Post/Request window = (%t, %t)", sleep, ok)
	}
	if result := registry.Request(executor); result != ExecutorRequestPublished {
		t.Fatalf("delayed executor request = %d", result)
	}
	if _, _, ok := PollExecutor(driver); !ok || registry.ObserveRequested(executor) {
		t.Fatal("settle delayed executor request")
	}
	retireCompletedRegistration(t, waits, wait)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{parked.g: parked})
	if !TerminalG(p, parked.g) {
		t.Fatal("post-window cleanup retained state")
	}
	runtime.KeepAlive(parked.frame.memory)
}

func TestExecutorDriverPostSleepRace(t *testing.T) {
	const iterations = 300
	for iteration := 0; iteration < iterations; iteration++ {
		p := new(P)
		driver, registry, waits, executor := bindTestExecutorDriver(t, p)
		parked := newYieldingTestG(t, "driver-race")
		if !Enqueue(p, parked.g) {
			t.Fatalf("iteration %d: enqueue race task", iteration)
		}
		_, _, wait := parkRegisteredDriverTask(t, p, waits, parked)
		start := make(chan struct{})
		sleepResult := make(chan [2]bool, 1)
		postResult := make(chan WaitExecutorPostResult, 1)
		go func() {
			<-start
			sleep, ok := PrepareExecutorSleep(driver)
			sleepResult <- [2]bool{sleep, ok}
		}()
		go func() {
			<-start
			postResult <- PostWaitAndRequest(waits, wait, registry, executor)
		}()
		close(start)
		sleep := <-sleepResult
		posted := <-postResult
		if !sleep[1] || posted.Wait != WaitRegistrationPosted ||
			(posted.Executor != ExecutorRequestPublished && posted.Executor != ExecutorRequestIdleWake) {
			t.Fatalf("iteration %d: sleep/post race = (%v, %+v)", iteration, sleep, posted)
		}
		if sleep[0] {
			if posted.Executor != ExecutorRequestIdleWake {
				t.Fatalf("iteration %d: committed sleep without idle wake = %d", iteration, posted.Executor)
			}
			if drained, promoted, ok := WakeExecutor(driver); !ok || drained != 1 || promoted != 1 {
				t.Fatalf("iteration %d: wake race = (%d, %d, %t)", iteration, drained, promoted, ok)
			}
		} else {
			// This also acknowledges a producer paused after durable Post until
			// PrepareExecutorSleep had already found and promoted its wait.
			if _, _, ok := PollExecutor(driver); !ok {
				t.Fatalf("iteration %d: settle delayed request", iteration)
			}
		}
		if HasWaiting(p) || p.readyHead != parked.g || registry.ObserveRequested(executor) {
			t.Fatalf("iteration %d: race lost work or retained request", iteration)
		}
		retireCompletedRegistration(t, waits, wait)
		closeTestExecutorDriver(t, driver)
		finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{parked.g: parked})
		if !TerminalG(p, parked.g) {
			t.Fatalf("iteration %d: race cleanup retained state", iteration)
		}
		runtime.KeepAlive(parked.frame.memory)
	}
}

func TestExecutorDriverLiveTimerRejectsTerminalClose(t *testing.T) {
	p := new(P)
	driver, registry, waits, timers, _ := bindTestExecutorDriverWithTimers(t, p)
	task := newYieldingTestG(t, "driver-live-timer-terminal")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue live-timer terminal task")
	}
	if next, ok := NextRunnableAt(p, 0); !ok || next != task.g {
		t.Fatal("dequeue live-timer terminal task")
	}
	action := beginWaitTestResume(t, p, task)
	token := new(WaitToken)
	ticket, timer, result := PrepareExecutorTimerRegistration(driver, token, 100)
	if result != TimerRegistrationPrepared {
		t.Fatalf("prepare leaked terminal timer = (%d, %+v, %d)", ticket, timer, result)
	}
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare live-timer terminal completion")
	}
	action, ok := Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatal("resume live-timer terminal completion")
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatal("check live-timer terminal destroy")
	}
	releaseTestFrame(t, task.g, task.frame)
	if closeAction, committed := Destroyed(p, task.g, action); committed || closeAction != (Action{}) ||
		driver.state != executorDriverActive {
		t.Fatalf("live timer crossed terminal close = (%+v, %t), state=%d", closeAction, committed, driver.state)
	}
	if timers.Cancel(timer) != WaitCancelWon || !consumeUnclaimedCanceledWait(token, ticket) || !timers.Retire(timer) {
		t.Fatal("clean leaked terminal timer after rejection")
	}
	closeAction, committed := Destroyed(p, task.g, action)
	if !committed || closeAction.Kind != ActionTerminalExecutorClose || closeAction.Handle != nil {
		t.Fatalf("terminal close after timer retirement = (%+v, %t)", closeAction, committed)
	}
	completed, terminal, ok := ConfirmTerminalExecutorClose(driver)
	if !ok || completed != task.g || terminal.Kind != ActionComplete || !TerminalG(p, task.g) ||
		!waits.CanRelease() || !timers.CanRelease() || !registry.CanRelease() {
		t.Fatalf("confirm terminal close after timer retirement = (%p, %+v, %t)", completed, terminal, ok)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorDriverTerminalCloseDoesNotRedestroy(t *testing.T) {
	p := new(P)
	driver, registry, waits, executor := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "driver-terminal-boundary")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue bound terminal task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue bound terminal task")
	}
	action := beginWaitTestResume(t, p, task)
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare bound terminal completion")
	}
	action, ok := Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatal("resume bound terminal completion")
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatal("check bound terminal destroy")
	}
	releaseTestFrame(t, task.g, task.frame)
	slot, slotOK := executorSlot(registry, executor)
	if !slotOK || !executorAcquireProducer(slot) {
		t.Fatal("pin terminal executor producer")
	}
	if result := registry.Request(executor); result != ExecutorRequestPublished {
		t.Fatalf("request terminal executor before close = %d", result)
	}
	closeAction, committed := Destroyed(p, task.g, action)
	if !committed || closeAction.Kind != ActionTerminalExecutorClose || closeAction.Handle != nil {
		t.Fatalf("begin last-G executor close = (%+v, %t)", closeAction, committed)
	}
	if p.action != closeAction || driver.state != executorDriverTerminalClosing ||
		driver.terminalKind != action.Kind || task.g.root != nil || registry.ObserveRequested(executor) {
		t.Fatal("terminal close did not hide the destroyed handle and settle requests")
	}
	if got, ok := TerminalExecutorCloseDriver(p, task.g, closeAction); !ok || got != driver {
		t.Fatalf("terminal close driver = (%p, %t), want %p", got, ok, driver)
	}
	if got, ok := TerminalExecutorCloseDriver(p, new(G), closeAction); ok || got != nil ||
		ConfirmExecutorClose(driver) {
		t.Fatal("terminal close accepted the wrong G or the generic close path")
	}
	if stale, ok := Destroyed(p, task.g, action); ok || stale != (Action{}) ||
		AcknowledgeTerminalSchedule(p, task.g, action) || TerminalG(p, task.g) {
		t.Fatal("stale destroyed action crossed the handle-free close marker")
	}
	if result := registry.Request(executor); result != ExecutorRequestClosed {
		t.Fatalf("request after terminal seal = %d", result)
	}
	if completed, terminal, ok := ConfirmTerminalExecutorClose(driver); ok || completed != nil || terminal != (Action{}) {
		t.Fatalf("terminal close confirmed before producer join = (%p, %+v, %t)", completed, terminal, ok)
	}
	executorReleaseProducer(slot)
	completed, terminal, ok := ConfirmTerminalExecutorClose(driver)
	if !ok || completed != task.g || terminal.Kind != ActionComplete || terminal.Handle != nil || !TerminalG(p, task.g) {
		t.Fatalf("confirm last-G executor close = (%p, %+v, %t), terminal=%t", completed, terminal, ok, TerminalG(p, task.g))
	}
	if *driver != (ExecutorDriver{}) || !waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("terminal close retained stable executor ownership")
	}
	if completed, repeated, ok := ConfirmTerminalExecutorClose(driver); ok || completed != nil || repeated != (Action{}) {
		t.Fatalf("terminal close confirmed twice = (%p, %+v, %t)", completed, repeated, ok)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorDriverTerminalCloseRequestRace(t *testing.T) {
	const iterations = 300
	type destroyResult struct {
		action Action
		ok     bool
	}
	for iteration := 0; iteration < iterations; iteration++ {
		p := new(P)
		driver, registry, waits, executor := bindTestExecutorDriver(t, p)
		task := newYieldingTestG(t, "driver-terminal-request-race")
		if !Enqueue(p, task.g) {
			t.Fatalf("iteration %d: enqueue terminal race G", iteration)
		}
		if next, ok := NextRunnable(p); !ok || next != task.g {
			t.Fatalf("iteration %d: dequeue terminal race G", iteration)
		}
		action := beginWaitTestResume(t, p, task)
		task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
		task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
		if !PrepareComplete(task.g, task.handle, task.frame.header) {
			t.Fatalf("iteration %d: prepare terminal race completion", iteration)
		}
		action, ok := Resumed(p, task.g, action)
		if !ok || action.Kind != ActionCheckDestroy {
			t.Fatalf("iteration %d: resume terminal race completion", iteration)
		}
		action, ok = Checked(p, task.g, action, true)
		if !ok || action.Kind != ActionDestroy {
			t.Fatalf("iteration %d: check terminal race destroy", iteration)
		}
		releaseTestFrame(t, task.g, task.frame)

		start := make(chan struct{})
		destroyed := make(chan destroyResult, 1)
		requested := make(chan ExecutorRequestResult, 1)
		go func() {
			<-start
			next, committed := Destroyed(p, task.g, action)
			destroyed <- destroyResult{action: next, ok: committed}
		}()
		go func() {
			<-start
			requested <- registry.Request(executor)
		}()
		close(start)
		closed, request := <-destroyed, <-requested
		if !closed.ok || closed.action.Kind != ActionTerminalExecutorClose || closed.action.Handle != nil ||
			(request != ExecutorRequestPublished && request != ExecutorRequestClosed) {
			t.Fatalf("iteration %d: terminal close/request race = (%+v, %d)", iteration, closed, request)
		}
		completed, terminal, ok := ConfirmTerminalExecutorClose(driver)
		if !ok || completed != task.g || terminal.Kind != ActionComplete || terminal.Handle != nil || !TerminalG(p, task.g) ||
			*driver != (ExecutorDriver{}) || !waits.CanRelease() || !registry.CanRelease() {
			t.Fatalf("iteration %d: confirm terminal request race = (%p, %+v, %t)", iteration, completed, terminal, ok)
		}
		runtime.KeepAlive(task.frame.memory)
	}
}

func TestExecutorDriverPanicTerminalCloseDoesNotRedestroy(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	control := new(TaskControlSource)
	executor := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, executor, ExecutorSourceCatalog{Waits: waits, Control: control}) {
		t.Fatal("bind panic terminal control-source executor")
	}
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize panic terminal G")
	}
	rootHandle, leafHandle := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	root := newTestFrame(t, g, rootHandle, nil)
	leaf := newTestFrame(t, g, leafHandle, rootHandle)
	if !AdoptRoot(g, rootHandle) || !Enqueue(p, g) {
		t.Fatal("adopt and enqueue panic terminal G")
	}
	if next, ok := NextRunnable(p); !ok || next != g {
		t.Fatal("dequeue panic terminal G")
	}
	action, ok := BeginRunG(p, g)
	if !ok {
		t.Fatal("begin panic terminal G")
	}
	action, ok = Checked(p, g, action, false)
	if !ok || action.Kind != ActionResume || action.Handle != rootHandle {
		t.Fatal("resume panic terminal root")
	}
	controlID, controlOK := RegisterTaskControl(control, p, g)
	if !controlOK || g.taskControlLeases != 1 {
		t.Fatalf("register panic terminal control = (%+v, %t), leases=%d", controlID, controlOK, g.taskControlLeases)
	}
	root.header.SuspendReason = uint16(SuspendCall)
	root.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareAwait(g, rootHandle, leafHandle) {
		t.Fatal("prepare panic terminal child")
	}
	action, ok = Resumed(p, g, action)
	if !ok || action.Kind != ActionCheckResume || action.Handle != leafHandle {
		t.Fatal("dispatch panic terminal child")
	}
	action, ok = Checked(p, g, action, false)
	if !ok || action.Kind != ActionResume || action.Handle != leafHandle {
		t.Fatal("resume panic terminal child")
	}
	typeWord, dataWord := new(byte), new(byte)
	leaf.header.SuspendReason = uint16(SuspendPanic)
	leaf.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PreparePanic(g, leafHandle, leaf.header, unsafe.Pointer(typeWord), unsafe.Pointer(dataWord)) {
		t.Fatal("publish panic terminal record")
	}
	action, ok = Resumed(p, g, action)
	if !ok || action.Kind != ActionCheckDestroy || action.Handle != leafHandle {
		t.Fatal("prepare panic leaf destroy")
	}
	action, ok = Checked(p, g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatal("check panic leaf destroy")
	}
	releaseTestFrame(t, g, leaf)
	action, ok = Destroyed(p, g, action)
	if !ok || action.Kind != ActionPanicDestroy || action.Handle != rootHandle {
		t.Fatalf("panic ancestor action = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, g, root)
	posted := PostTaskControlAndRequest(control, controlID, TaskCancelShutdown, registry, executor)
	if posted.Control != TaskControlPosted || posted.Executor != ExecutorRequestPublished {
		t.Fatalf("post panic terminal-late control = (%d, %d)", posted.Control, posted.Executor)
	}
	closeAction, ok := PanicDestroyed(p, g, action)
	if !ok || closeAction.Kind != ActionTerminalExecutorClose || closeAction.Handle != nil ||
		driver.terminalKind != action.Kind || g.root != nil || g.taskControlLeases != 1 ||
		g.park.taskCancelKind != TaskCancelNone || g.park.taskCancelPhase != taskCancelIdle {
		t.Fatalf("panic terminal close action = (%+v, %t)", closeAction, ok)
	}
	if result := control.Post(controlID, TaskCancelAbort); result != TaskControlPostClosed {
		t.Fatalf("panic control post after terminal seal = %d", result)
	}
	completed, terminal, ok := ConfirmTerminalExecutorClose(driver)
	if !ok || completed != g || terminal.Kind != ActionPanicComplete || terminal.Handle != nil {
		t.Fatalf("confirm panic terminal close = (%p, %+v, %t)", completed, terminal, ok)
	}
	record, published := LoadPanicRecord(g)
	if !published || record.TypeWord != unsafe.Pointer(typeWord) || record.DataWord != unsafe.Pointer(dataWord) ||
		g.state != GDead || g.panicUnwind || preemptLoad(&p.schedule) != scheduleDisabled ||
		preemptLoad(&p.executorMode) != executorModeUnbound || p.executor != nil ||
		g.taskControlLeases != 0 || g.park.taskCancelKind != TaskCancelNone || g.park.taskCancelPhase != taskCancelIdle ||
		!control.CanRelease() || !waits.CanRelease() || !registry.CanRelease() || *driver != (ExecutorDriver{}) {
		t.Fatalf("panic terminal close state = record:(%+v,%t) g:%d unwind:%t schedule:%d mode:%d",
			record, published, g.state, g.panicUnwind, preemptLoad(&p.schedule), preemptLoad(&p.executorMode))
	}
	if TerminalG(p, g) || ReclaimableG(g) {
		t.Fatal("panic terminal close was misclassified as normal completion")
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	runtime.KeepAlive(root.memory)
	runtime.KeepAlive(leaf.memory)
}

func TestExecutorDriverInitialPanicTerminalCloseDoesNotRedestroy(t *testing.T) {
	p := new(P)
	driver, registry, waits, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "driver-initial-panic-terminal")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue initial panic terminal G")
	}
	if next, ok := NextRunnable(p); !ok || next != task.g {
		t.Fatal("dequeue initial panic terminal G")
	}
	action := beginWaitTestResume(t, p, task)
	typeWord, dataWord := new(byte), new(byte)
	task.frame.header.SuspendReason = uint16(SuspendPanic)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PreparePanic(task.g, task.handle, task.frame.header, unsafe.Pointer(typeWord), unsafe.Pointer(dataWord)) {
		t.Fatal("publish initial panic terminal record")
	}
	action, ok := Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy || action.Handle != task.handle {
		t.Fatal("prepare initial panic terminal destroy")
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatal("check initial panic terminal destroy")
	}
	releaseTestFrame(t, task.g, task.frame)
	closeAction, ok := Destroyed(p, task.g, action)
	if !ok || closeAction.Kind != ActionTerminalExecutorClose || closeAction.Handle != nil ||
		driver.terminalKind != action.Kind || task.g.root != nil || !task.g.panicUnwind {
		t.Fatalf("initial panic terminal close = (%+v, %t)", closeAction, ok)
	}
	if stale, ok := Destroyed(p, task.g, action); ok || stale != (Action{}) {
		t.Fatal("initial panic stale destroy crossed close marker")
	}
	completed, terminal, ok := ConfirmTerminalExecutorClose(driver)
	if !ok || completed != task.g || terminal.Kind != ActionPanicComplete || terminal.Handle != nil {
		t.Fatalf("confirm initial panic terminal close = (%p, %+v, %t)", completed, terminal, ok)
	}
	record, published := LoadPanicRecord(task.g)
	if !published || record.TypeWord != unsafe.Pointer(typeWord) || record.DataWord != unsafe.Pointer(dataWord) ||
		task.g.state != GDead || task.g.panicUnwind || preemptLoad(&p.schedule) != scheduleDisabled ||
		!waits.CanRelease() || !registry.CanRelease() || *driver != (ExecutorDriver{}) {
		t.Fatalf("initial panic terminal state = record:(%+v,%t) g:%d unwind:%t schedule:%d",
			record, published, task.g.state, task.g.panicUnwind, preemptLoad(&p.schedule))
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	runtime.KeepAlive(task.frame.memory)
}
