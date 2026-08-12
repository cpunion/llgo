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

func consumeServicePreemptTestBudget(t *testing.T, p *P, g *G, wantYield bool) {
	t.Helper()
	if p.servicePreemptBudget != servicePreemptSafepointBudget {
		t.Fatalf("initial service preemption budget = %d, want %d", p.servicePreemptBudget, servicePreemptSafepointBudget)
	}
	for safepoint := uint32(1); safepoint < servicePreemptSafepointBudget; safepoint++ {
		if pollCompilerSafepointForTest(t, g) {
			t.Fatalf("service preemption fired at safepoint %d, want %d", safepoint, servicePreemptSafepointBudget)
		}
		charged := safepoint / preemptCheckpointStride * preemptCheckpointStride
		if want := servicePreemptSafepointBudget - charged; p.servicePreemptBudget != want {
			t.Fatalf("service preemption budget after safepoint %d = %d, want %d", safepoint, p.servicePreemptBudget, want)
		}
	}
	if yielded := pollCompilerSafepointForTest(t, g); yielded != wantYield {
		t.Fatalf("service preemption at safepoint %d = %t, want %t",
			servicePreemptSafepointBudget, yielded, wantYield)
	}
	if p.servicePreemptBudget != servicePreemptSafepointBudget {
		t.Fatalf("fired service preemption budget = %d, want reload %d", p.servicePreemptBudget, servicePreemptSafepointBudget)
	}
}

func runServicePreemptTestQuantum(t *testing.T, p *P, task *yieldingTestG, wantServiceYield bool) {
	t.Helper()
	action := beginWaitTestResume(t, p, task)
	consumeServicePreemptTestBudget(t, p, task.g, wantServiceYield)
	if !wantServiceYield && (!RequestPreempt(task.g) || !PollPreemptCompiler(task.g)) {
		t.Fatal("explicit request did not end a bound sole-runnable service slice")
	}
	yieldRunningDriverTask(t, p, task, action)
	if p.servicePreemptBudget != 0 || p.current != nil {
		t.Fatalf("service yield retained run state: budget=%d current=%p", p.servicePreemptBudget, p.current)
	}
}

func finishServicePreemptTestTask(t *testing.T, p *P, task *yieldingTestG) {
	t.Helper()
	next, ok := NextRunnable(p)
	if !ok || next != task.g {
		t.Fatalf("dequeue service-preempted task = (%p, %t), want %p", next, ok, task.g)
	}
	finishWaitTestTask(t, p, task, beginWaitTestResume(t, p, task))
	if !TerminalG(p, task.g) {
		t.Fatal("service-preempted task retained scheduler state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestServicePreemptQuantumUsesBoundRequestContract(t *testing.T) {
	t.Run("unbound", func(t *testing.T) {
		p := new(P)
		task := newYieldingTestG(t, "service-unbound")
		runServicePreemptTestQuantum(t, p, task, true)
		finishServicePreemptTestTask(t, p, task)
	})

	t.Run("source-empty-bound", func(t *testing.T) {
		p := new(P)
		driver, registry, _ := bindTestExecutorDriver(t, p)
		task := newYieldingTestG(t, "service-source-empty")
		runServicePreemptTestQuantum(t, p, task, false)
		closeTestExecutorDriver(t, driver)
		finishServicePreemptTestTask(t, p, task)
		if !registry.CanRelease() {
			t.Fatal("source-empty service quantum retained executor state")
		}
	})

	t.Run("timer-bound-empty", func(t *testing.T) {
		p := new(P)
		driver, registry, timers, _ := bindTestExecutorDriverWithTimers(t, p)
		task := newYieldingTestG(t, "service-timer-empty")
		runServicePreemptTestQuantum(t, p, task, false)
		closeTestExecutorDriver(t, driver)
		finishServicePreemptTestTask(t, p, task)
		if !timers.CanRelease() || !registry.CanRelease() {
			t.Fatal("empty timer-bound service quantum retained executor state")
		}
	})

	t.Run("bound-waiting-source-audit", func(t *testing.T) {
		p := new(P)
		driver, registry, _ := bindTestExecutorDriver(t, p)
		task := newYieldingTestG(t, "service-waiting-source")
		action := beginWaitTestResume(t, p, task)

		// The source-specific park tests cover the complete queue shape. This
		// fixture isolates the service policy: a non-empty logical wait set must
		// retain periodic source audits even when no runnable peer exists.
		wait := new(WaitSetRecord)
		p.parkWaitHead, p.parkWaitTail = wait, wait
		consumeServicePreemptTestBudget(t, p, task.g, true)
		p.parkWaitHead, p.parkWaitTail = nil, nil

		yieldRunningDriverTask(t, p, task, action)
		closeTestExecutorDriver(t, driver)
		finishServicePreemptTestTask(t, p, task)
		if !registry.CanRelease() {
			t.Fatal("waiting-source service quantum retained executor state")
		}
	})

	t.Run("bound-remote-quota-pressure", func(t *testing.T) {
		p := new(P)
		driver, registry, _ := bindTestExecutorDriver(t, p)
		quota := new(ExecutionQuota)
		if !quota.Start(1) || !BindExecutorServicePressure(driver, quota) ||
			BindExecutorServicePressure(driver, quota) {
			t.Fatal("bind shared execution-quota service pressure")
		}
		if acquired, ok := quota.TryAcquire(1); !acquired || !ok {
			t.Fatal("acquire current route execution quota")
		}
		if acquired, ok := quota.TryAcquire(2); acquired || !ok {
			t.Fatalf("publish remote quota contention = (%t, %t)", acquired, ok)
		}

		task := newYieldingTestG(t, "service-remote-quota-pressure")
		runServicePreemptTestQuantum(t, p, task, true)

		if wake, ok := quota.Release(1); !wake || !ok {
			t.Fatalf("release current route quota = (%t, %t)", wake, ok)
		}
		if acquired, ok := quota.TryAcquire(2); !acquired || !ok {
			t.Fatal("remote route did not acquire released execution quota")
		}
		if _, ok := quota.Release(2); !ok {
			t.Fatal("release remote route execution quota")
		}
		if _, ok := quota.Seal(); !ok || !quota.Quiesced() || !quota.Retire() {
			t.Fatal("retire shared execution quota")
		}

		closeTestExecutorDriver(t, driver)
		finishServicePreemptTestTask(t, p, task)
		if !registry.CanRelease() {
			t.Fatal("remote-pressure service quantum retained executor state")
		}
	})

}

func TestCompilerPreemptPollObservesConcurrentExecutorRequest(t *testing.T) {
	p := new(P)
	driver, registry, executor := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "concurrent-executor-request")
	action := beginWaitTestResume(t, p, task)

	start := make(chan struct{})
	result := make(chan ExecutorRequestResult, 1)
	go func() {
		<-start
		result <- registry.Request(executor)
	}()
	close(start)

	observed := false
	for attempt := 0; attempt != 100000 && !observed; attempt++ {
		observed = PollPreemptCompiler(task.g)
		if !observed {
			runtime.Gosched()
		}
	}
	if request := <-result; request != ExecutorRequestPublished {
		t.Fatalf("concurrent executor request = %d, want published", request)
	}
	if !observed {
		t.Fatal("compiler poll missed a concurrent sticky executor request")
	}

	yieldRunningDriverTask(t, p, task, action)
	if promoted, ok := PollReady(p); !ok || promoted != 0 || registry.ObserveRequested(executor) {
		t.Fatalf("concurrent executor request acknowledgment = (%d, %t), requested=%t",
			promoted, ok, registry.ObserveRequested(executor))
	}
	closeTestExecutorDriver(t, driver)
	finishServicePreemptTestTask(t, p, task)
	if !registry.CanRelease() {
		t.Fatal("concurrent executor request retained executor state")
	}
}

func TestServicePreemptBudgetRejectsStaleIdleState(t *testing.T) {
	p := new(P)
	driver, registry, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "service-stale-budget")
	p.servicePreemptBudget = 1
	if action, ok := BeginRunG(p, task.g); ok || action != (Action{}) || p.current != nil || task.g.state != GRunnable {
		t.Fatalf("begin accepted stale service budget = (%+v, %t)", action, ok)
	}
	if BeginExecutorClose(driver) {
		t.Fatal("executor close accepted stale service preemption budget")
	}
	p.servicePreemptBudget = 0
	closeTestExecutorDriver(t, driver)
	if !registry.CanRelease() {
		t.Fatal("stale service budget test retained executor state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExplicitRequestsPrecedeServiceBudget(t *testing.T) {
	t.Run("G-local", func(t *testing.T) {
		p := new(P)
		task := newYieldingTestG(t, "service-request-g")
		action := beginWaitTestResume(t, p, task)
		if !RequestPreempt(task.g) || !PollPreemptCompiler(task.g) {
			t.Fatal("G-local request was not observed")
		}
		if p.servicePreemptBudget != servicePreemptSafepointBudget {
			t.Fatalf("G-local request consumed service budget: got %d", p.servicePreemptBudget)
		}
		yieldRunningDriverTask(t, p, task, action)
		finishServicePreemptTestTask(t, p, task)
	})

	t.Run("P-gate", func(t *testing.T) {
		p := new(P)
		task := newYieldingTestG(t, "service-request-p")
		action := beginWaitTestResume(t, p, task)
		if !RequestSchedule(p) || !PollPreemptCompiler(task.g) {
			t.Fatal("P scheduling request was not observed")
		}
		if p.servicePreemptBudget != servicePreemptSafepointBudget {
			t.Fatalf("P scheduling request consumed service budget: got %d", p.servicePreemptBudget)
		}
		yieldRunningDriverTask(t, p, task, action)
		finishServicePreemptTestTask(t, p, task)
	})

	t.Run("executor-gate", func(t *testing.T) {
		p := new(P)
		driver, registry, executor := bindTestExecutorDriver(t, p)
		task := newYieldingTestG(t, "service-request-executor")
		action := beginWaitTestResume(t, p, task)
		if registry.Request(executor) != ExecutorRequestPublished || !PollPreemptCompiler(task.g) {
			t.Fatal("executor scheduling request was not observed")
		}
		if p.servicePreemptBudget != servicePreemptSafepointBudget {
			t.Fatalf("executor request consumed service budget: got %d", p.servicePreemptBudget)
		}
		yieldRunningDriverTask(t, p, task, action)
		if promoted, ok := PollReady(p); !ok || promoted != 0 || registry.ObserveRequested(executor) {
			t.Fatalf("executor request acknowledgment = (%d, %t), requested=%t", promoted, ok, registry.ObserveRequested(executor))
		}
		closeTestExecutorDriver(t, driver)
		finishServicePreemptTestTask(t, p, task)
		if !registry.CanRelease() {
			t.Fatal("executor request priority retained executor state")
		}
	})
}
