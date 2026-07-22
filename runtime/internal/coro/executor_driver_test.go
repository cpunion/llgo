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
