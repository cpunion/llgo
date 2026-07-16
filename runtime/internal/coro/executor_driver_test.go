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

func TestExecutorDriverRejectsUnclosedLastGTerminal(t *testing.T) {
	p := new(P)
	_, _, _, _ = bindTestExecutorDriver(t, p)
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
	if next, committed := Destroyed(p, task.g, action); committed || next != (Action{}) {
		t.Fatalf("last G crossed active executor binding = (%+v, %t)", next, committed)
	}
	if AcknowledgeTerminalSchedule(p, task.g, action) || TerminalG(p, task.g) ||
		preemptLoad(&p.executorMode) != executorModeBound {
		t.Fatal("bound terminal failure was misclassified as a legacy request race")
	}
	// This is an intentional fail-closed boundary, not a recoverable test
	// teardown path: the production terminal close/join/retry action is a later
	// phase. Keep the backing allocation alive while checking poisoned state.
	runtime.KeepAlive(task.frame.memory)
}
