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

func bindTestExecutorDriver(t *testing.T, p *P) (*ExecutorDriver, *ExecutorRegistry, ExecutorHandle) {
	t.Helper()
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{}) {
		t.Fatal("bind test executor driver")
	}
	return driver, registry, handle
}

func bindTestExecutorDriverWithTimers(t *testing.T, p *P) (*ExecutorDriver, *ExecutorRegistry, *TimerRegistrationTable, ExecutorHandle) {
	t.Helper()
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	timers := new(TimerRegistrationTable)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{Timers: timers}) {
		t.Fatal("bind timer-aware test executor driver")
	}
	return driver, registry, timers, handle
}

func bindTestExecutorDriverWithManual(t *testing.T, p *P) (*ExecutorDriver, *ExecutorRegistry, *ManualOperationSource, ExecutorHandle) {
	t.Helper()
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	manual := new(ManualOperationSource)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{Manual: manual}) {
		t.Fatal("bind manual-source test executor driver")
	}
	return driver, registry, manual, handle
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
	driver, registry, handle := bindTestExecutorDriver(t, p)
	slot, ok := executorSlot(registry, handle)
	if !ok || driver.requestGate != &slot.gate {
		t.Fatal("bound driver did not retain its exact stable request gate")
	}
	if RequestSchedule(p) || preemptLoad(&p.schedule) != scheduleIdle {
		t.Fatal("legacy P request entered a bound executor")
	}
	if sleep, ok := PrepareExecutorSleep(driver); !ok || sleep {
		t.Fatalf("empty driver sleep = (%t, %t)", sleep, ok)
	}
	if BindExecutorSourceCatalog(new(ExecutorDriver), p, registry, handle, ExecutorSourceCatalog{}) {
		t.Fatal("P accepted a second executor binding")
	}
	closeTestExecutorDriver(t, driver)
	if preemptLoad(&p.executorMode) != executorModeUnbound || p.executor != nil || !registry.CanRelease() {
		t.Fatal("closed driver retained stable ownership")
	}
}

func TestExecutorDriverHotHeaderDefersDeepCatalogAndPollAudits(t *testing.T) {
	p := new(P)
	driver, _, manual, _ := bindTestExecutorDriverWithManual(t, p)
	if !validExecutorDriverHeader(driver) || !validExecutorDriver(driver) {
		t.Fatal("fresh driver failed header or complete audit")
	}

	// A selected owner reduction does not walk an unrelated source catalog.
	// The complete diagnostic boundary must still reject its invalid scan tail.
	manual.scanLimit = ManualOperationConfiguredCapacity(manual) + 1
	if !validExecutorDriverHeader(driver) {
		t.Fatal("hot header inspected the manual catalog scan tail")
	}
	if validExecutorDriver(driver) {
		t.Fatal("complete driver audit accepted an invalid manual scan tail")
	}
	if pending, ok := ExecutorRunManagedResumePending(driver); !ok || pending {
		t.Fatalf("observational hot gate over distant catalog damage = (%t, %t)", pending, ok)
	}
	manual.scanLimit = 0

	// The in-progress poll cursor is likewise validated by its exact reducer and
	// by complete lifecycle diagnostics, not by an unrelated owner observation.
	driver.poll = executorPollTransaction{phase: executorPollAcknowledge}
	if !validExecutorDriverHeader(driver) {
		t.Fatal("hot header inspected the logical poll cursor")
	}
	if validExecutorDriver(driver) {
		t.Fatal("complete driver audit accepted an invalid logical poll cursor")
	}
	driver.poll = executorPollTransaction{}

	// Owner-local completion is another selected-work cursor. Its exact
	// publication/resolution gates and complete diagnostics validate payload;
	// an unrelated managed-resume observation keeps the immutable driver hot
	// header independent of dormant local queue state.
	driver.local.resolve = publishedEpochResolveCursor{phase: publishedEpochResolveDiscover}
	if !validExecutorDriverHeader(driver) {
		t.Fatal("hot header inspected the owner-local completion cursor")
	}
	if validExecutorDriver(driver) {
		t.Fatal("complete driver audit accepted an invalid owner-local completion cursor")
	}
	if pending, ok := ExecutorRunManagedResumePending(driver); !ok || pending {
		t.Fatalf("observational hot gate over local cursor damage = (%t, %t)", pending, ok)
	}
	driver.local = ownerLocalCompletionCursor{}
	if !validExecutorDriver(driver) {
		t.Fatal("restored driver failed complete audit")
	}
	closeTestExecutorDriver(t, driver)
}

func TestExecutorDriverHotHeaderRejectsLocalBindingDamage(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)

	driver.sources.magic = 0
	if validExecutorDriverHeader(driver) {
		t.Fatal("hot header accepted damaged source-set identity")
	}
	driver.sources.magic = executorSourceSetMagic

	p.executor = nil
	if validExecutorDriverHeader(driver) {
		t.Fatal("hot header accepted damaged P back-pointer")
	}
	p.executor = driver

	driver.run.actionsSinceSource = executorRunSourceQuantum + 1
	if validExecutorDriverHeader(driver) {
		t.Fatal("hot header accepted damaged local run cursor")
	}
	driver.run.actionsSinceSource = 0

	if !validExecutorDriver(driver) {
		t.Fatal("restored local binding failed complete audit")
	}
	closeTestExecutorDriver(t, driver)
}

func drainTimerAwareExecutorRunSources(t *testing.T, driver *ExecutorDriver, now int64) {
	t.Helper()
	for reduction := 0; reduction < 4096; reduction++ {
		step, ok := NextExecutorRunStepAt(driver, now)
		if !ok {
			t.Fatalf("drain timer-aware executor source reduction %d", reduction)
		}
		switch step.Kind {
		case ExecutorRunStepSource:
			continue
		case ExecutorRunStepIdle:
			return
		default:
			t.Fatalf("timer-aware source drain reduction %d = %d", reduction, step.Kind)
		}
	}
	t.Fatal("timer-aware executor source drain exceeded reduction bound")
}

func TestExecutorRunWakeDefersSourceService(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithTimers(t, p)
	prepared, ok := PrepareExecutorStandbyAt(driver, 10)
	if !ok || !prepared {
		t.Fatalf("prepare empty timer-aware standby = (%t, %t)", prepared, ok)
	}
	sleep, deadline, hasDeadline, ok := CommitExecutorSleepAt(driver, 11)
	if !ok || !sleep || hasDeadline || deadline != 0 {
		t.Fatalf("commit empty timer-aware standby = (%t, %d, %t, %t)", sleep, deadline, hasDeadline, ok)
	}
	if !WakeExecutorAt(driver, 12) || driver.state != executorDriverActive ||
		!driver.run.sourceMore || driver.poll.phase != executorPollIdle {
		t.Fatal("run wake did not retain source work for the unified reducer")
	}
	drainTimerAwareExecutorRunSources(t, driver, 13)
	if driver.run != (executorRunCursor{}) {
		t.Fatalf("drained run cursor = %+v", driver.run)
	}
	closeTestExecutorDriver(t, driver)
}

func TestExecutorRunWakeAcceptsDirectChannelArrivalAfterSleep(t *testing.T) {
	p := new(P)
	driver, registry, _, handle := bindTestExecutorDriverWithTimers(t, p)
	prepared, ok := PrepareExecutorStandbyAt(driver, 10)
	if !ok || !prepared {
		t.Fatalf("prepare direct-channel standby = (%t, %t)", prepared, ok)
	}
	sleep, deadline, hasDeadline, ok := CommitExecutorSleepAt(driver, 11)
	if !ok || !sleep || hasDeadline || deadline != 0 {
		t.Fatalf("commit direct-channel standby = (%t, %d, %t, %t)", sleep, deadline, hasDeadline, ok)
	}

	completion := &DirectChannelCompletion{
		owner: driver,
		route: driver.route,
		state: uint32(directChannelCompletionMatched),
	}
	if !PublishExecutorDirectChannelCompletion(driver, completion) {
		t.Fatal("publish direct-channel completion after executor sleep")
	}
	if request := registry.Request(handle); request != ExecutorRequestIdleWake {
		t.Fatalf("request sleeping executor after direct completion = %d", request)
	}
	if !WakeExecutorAt(driver, 12) || driver.state != executorDriverActive ||
		!driver.run.sourceMore {
		t.Fatalf("wake over direct-channel arrival: state=%d run=%+v", driver.state, driver.run)
	}
	if got, takeOK := takeExecutorDirectChannelCompletion(driver); !takeOK || got != completion {
		t.Fatalf("take post-sleep direct completion = (%p, %t), want (%p, true)", got, takeOK, completion)
	}
	drainTimerAwareExecutorRunSources(t, driver, 13)
	closeTestExecutorDriver(t, driver)
}

func TestPrepareExecutorStandbyDefersDirectChannelIngress(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithTimers(t, p)
	completion := &DirectChannelCompletion{
		owner: driver,
		route: driver.route,
		state: uint32(directChannelCompletionMatched),
	}
	if !PublishExecutorDirectChannelCompletion(driver, completion) {
		t.Fatal("publish direct-channel completion before standby")
	}
	if prepared, ok := PrepareExecutorStandbyAt(driver, 10); !ok || prepared {
		t.Fatalf("standby over direct-channel ingress = (%t, %t), want (false, true)", prepared, ok)
	}
	if got, ok := takeExecutorDirectChannelCompletion(driver); !ok || got != completion {
		t.Fatalf("take deferred standby completion = (%p, %t), want (%p, true)", got, ok, completion)
	}
	closeTestExecutorDriver(t, driver)
}

func TestWorkerCompletionProbeDefersDirectChannelIngress(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	// A fleet peer may still own cold fairness/source state here; only an
	// issued physical action or active source transaction would make the
	// completion window unstable.
	driver.run.sourceMore = true
	completion := &DirectChannelCompletion{
		owner: driver,
		route: driver.route,
		state: uint32(directChannelCompletionMatched),
	}
	if !PublishExecutorDirectChannelCompletion(driver, completion) {
		t.Fatal("publish direct-channel completion before worker probe")
	}
	probe, awaiting, ready, ok := PrepareExecutorWorkerCompletionProbe(driver)
	if !ok || awaiting || !ready || probe.Valid() {
		t.Fatalf(
			"worker probe over direct-channel ingress = (%+v, %t, %t, %t), want (invalid, false, true, true)",
			probe, awaiting, ready, ok,
		)
	}
	if got, ok := takeExecutorDirectChannelCompletion(driver); !ok || got != completion {
		t.Fatalf("take probe-deferred completion = (%p, %t), want (%p, true)", got, ok, completion)
	}
	if !EnterExecutorRunCompatibility(driver) {
		t.Fatal("settle cold cursor after probe-deferred completion")
	}
	closeTestExecutorDriver(t, driver)
}

func TestWorkerCompletionProbeRejectsInvalidDirectChannelHeader(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	tail := driver.directChannelTail
	driver.directChannelTail = nil
	probe, awaiting, ready, ok := PrepareExecutorWorkerCompletionProbe(driver)
	if ok || awaiting || ready || probe.Valid() {
		t.Fatalf(
			"worker probe with invalid direct-channel header = (%+v, %t, %t, %t), want zero invalid result",
			probe, awaiting, ready, ok,
		)
	}
	driver.directChannelTail = tail
	closeTestExecutorDriver(t, driver)
}

func TestCommitExecutorStandbyDefersDirectChannelIngress(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithTimers(t, p)
	if prepared, ok := PrepareExecutorStandbyAt(driver, 10); !ok || !prepared {
		t.Fatalf("prepare standby before direct ingress = (%t, %t)", prepared, ok)
	}
	completion := &DirectChannelCompletion{
		owner: driver,
		route: driver.route,
		state: uint32(directChannelCompletionMatched),
	}
	if !PublishExecutorDirectChannelCompletion(driver, completion) {
		t.Fatal("publish direct-channel completion during standby preparation")
	}
	sleep, deadline, hasDeadline, ok := CommitExecutorSleepAt(driver, 11)
	if !ok || sleep || deadline != 0 || hasDeadline || driver.state != executorDriverActive ||
		!driver.run.sourceMore {
		t.Fatalf("commit standby over direct ingress = (%t, %d, %t, %t), state=%d run=%+v",
			sleep, deadline, hasDeadline, ok, driver.state, driver.run)
	}
	if got, ok := takeExecutorDirectChannelCompletion(driver); !ok || got != completion {
		t.Fatalf("take commit-deferred completion = (%p, %t), want (%p, true)", got, ok, completion)
	}
	drainTimerAwareExecutorRunSources(t, driver, 12)
	closeTestExecutorDriver(t, driver)
}

func TestExecutorStandbySourceScanAllowsDirectChannelIngress(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithTimers(t, p)
	if !driver.registry.ArmIdle(driver.handle) {
		t.Fatal("arm executor idle before source scan")
	}
	completion := &DirectChannelCompletion{
		owner: driver,
		route: driver.route,
		state: uint32(directChannelCompletionMatched),
	}
	if !PublishExecutorDirectChannelCompletion(driver, completion) {
		t.Fatal("publish direct-channel completion after idle arm")
	}
	if scan, ok := publishExecutorSourcesAt(driver, 10, true); !ok || scan != (executorSourceScan{}) {
		t.Fatalf("source scan over direct-channel ingress = (%+v, %t)", scan, ok)
	}
	if !leaveExecutorIdleForRun(driver) {
		t.Fatal("leave idle after direct-channel ingress")
	}
	if got, ok := takeExecutorDirectChannelCompletion(driver); !ok || got != completion {
		t.Fatalf("take scan-racing completion = (%p, %t), want (%p, true)", got, ok, completion)
	}
	drainTimerAwareExecutorRunSources(t, driver, 11)
	closeTestExecutorDriver(t, driver)
}

func TestPrepareExecutorStandbyDefersRacingRequest(t *testing.T) {
	p := new(P)
	driver, registry, _, handle := bindTestExecutorDriverWithTimers(t, p)
	if result := registry.Request(handle); result != ExecutorRequestPublished {
		t.Fatalf("publish pre-standby executor request = %d", result)
	}
	prepared, ok := PrepareExecutorStandbyAt(driver, 20)
	if !ok || prepared || driver.state != executorDriverActive || !driver.run.sourceMore {
		t.Fatalf("prepare over racing request = (%t, %t), state=%d run=%+v",
			prepared, ok, driver.state, driver.run)
	}
	drainTimerAwareExecutorRunSources(t, driver, 21)
	if registry.ObserveRequested(handle) {
		t.Fatal("unified source reducer retained the racing executor request")
	}
	closeTestExecutorDriver(t, driver)
}
