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

func consumeServicePreemptTestBudget(t *testing.T, p *P, g *G) {
	t.Helper()
	if p.servicePreemptBudget != servicePreemptPollBudget {
		t.Fatalf("initial service preemption budget = %d, want %d", p.servicePreemptBudget, servicePreemptPollBudget)
	}
	for poll := uint32(1); poll < servicePreemptPollBudget; poll++ {
		if PollPreempt(g) {
			t.Fatalf("service preemption fired at safepoint %d, want %d", poll, servicePreemptPollBudget)
		}
		if want := servicePreemptPollBudget - poll; p.servicePreemptBudget != want {
			t.Fatalf("service preemption budget after safepoint %d = %d, want %d", poll, p.servicePreemptBudget, want)
		}
	}
	if !PollPreempt(g) {
		t.Fatalf("service preemption did not fire at safepoint %d", servicePreemptPollBudget)
	}
	if p.servicePreemptBudget != servicePreemptPollBudget {
		t.Fatalf("fired service preemption budget = %d, want reload %d", p.servicePreemptBudget, servicePreemptPollBudget)
	}
}

func runServicePreemptTestQuantum(t *testing.T, p *P, task *yieldingTestG) {
	t.Helper()
	action := beginWaitTestResume(t, p, task)
	consumeServicePreemptTestBudget(t, p, task.g)
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

func TestServicePreemptQuantumIsEventSourceIndependent(t *testing.T) {
	t.Run("unbound", func(t *testing.T) {
		p := new(P)
		task := newYieldingTestG(t, "service-unbound")
		runServicePreemptTestQuantum(t, p, task)
		finishServicePreemptTestTask(t, p, task)
	})

	t.Run("wait-only-bound", func(t *testing.T) {
		p := new(P)
		driver, registry, waits, _ := bindTestExecutorDriver(t, p)
		task := newYieldingTestG(t, "service-wait-only")
		runServicePreemptTestQuantum(t, p, task)
		closeTestExecutorDriver(t, driver)
		finishServicePreemptTestTask(t, p, task)
		if !waits.CanRelease() || !registry.CanRelease() {
			t.Fatal("wait-only service quantum retained executor state")
		}
	})

	t.Run("timer-bound-empty", func(t *testing.T) {
		p := new(P)
		driver, registry, waits, timers, _ := bindTestExecutorDriverWithTimers(t, p)
		task := newYieldingTestG(t, "service-timer-empty")
		runServicePreemptTestQuantum(t, p, task)
		closeTestExecutorDriver(t, driver)
		finishServicePreemptTestTask(t, p, task)
		if !waits.CanRelease() || !timers.CanRelease() || !registry.CanRelease() {
			t.Fatal("empty timer-bound service quantum retained executor state")
		}
	})

	t.Run("timer-bound-active", func(t *testing.T) {
		p := new(P)
		driver, registry, waits, timers, _ := bindTestExecutorDriverWithTimers(t, p)
		token, ticket, timer := prepareTestTimer(t, timers, p, 100)
		if deadline, has, ok := NextExecutorTimerDeadline(driver); !ok || !has || deadline != 100 {
			t.Fatalf("active timer before service quantum = (%d, %t, %t)", deadline, has, ok)
		}
		task := newYieldingTestG(t, "service-timer-active")
		runServicePreemptTestQuantum(t, p, task)
		if !timers.RollbackPreparedTimer(timer, token, ticket) {
			t.Fatal("rollback active timer after service quantum")
		}
		closeTestExecutorDriver(t, driver)
		finishServicePreemptTestTask(t, p, task)
		if !waits.CanRelease() || !timers.CanRelease() || !registry.CanRelease() {
			t.Fatal("active timer-bound service quantum retained executor state")
		}
	})

	t.Run("timer-bound-retired", func(t *testing.T) {
		p := new(P)
		driver, registry, waits, timers, _ := bindTestExecutorDriverWithTimers(t, p)
		token, ticket, timer := prepareTestTimer(t, timers, p, 100)
		if completed, deadline, has, ok := timers.drainDueFor(p, 100); !ok || completed != 1 || has || deadline != 0 {
			t.Fatalf("complete timer before retirement = (%d, %d, %t, %t)", completed, deadline, has, ok)
		}
		consumeTimerOutcome(t, token, ticket, WaitOutcomeCompleted)
		if !timers.RetireCompletedTimer(timer, token, ticket) {
			t.Fatal("retire timer before service quantum")
		}
		if deadline, has, ok := NextExecutorTimerDeadline(driver); !ok || has || deadline != 0 {
			t.Fatalf("retired timer deadline = (%d, %t, %t)", deadline, has, ok)
		}
		task := newYieldingTestG(t, "service-timer-retired")
		runServicePreemptTestQuantum(t, p, task)
		closeTestExecutorDriver(t, driver)
		finishServicePreemptTestTask(t, p, task)
		if !waits.CanRelease() || !timers.CanRelease() || !registry.CanRelease() {
			t.Fatal("retired timer-bound service quantum retained executor state")
		}
	})
}

func TestServicePreemptBudgetRejectsStaleIdleState(t *testing.T) {
	p := new(P)
	driver, registry, waits, _ := bindTestExecutorDriver(t, p)
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
	if !waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("stale service budget test retained executor state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExplicitRequestsPrecedeServiceBudget(t *testing.T) {
	t.Run("G-local", func(t *testing.T) {
		p := new(P)
		task := newYieldingTestG(t, "service-request-g")
		action := beginWaitTestResume(t, p, task)
		if !RequestPreempt(task.g) || !PollPreempt(task.g) {
			t.Fatal("G-local request was not observed")
		}
		if p.servicePreemptBudget != servicePreemptPollBudget {
			t.Fatalf("G-local request consumed service budget: got %d", p.servicePreemptBudget)
		}
		yieldRunningDriverTask(t, p, task, action)
		finishServicePreemptTestTask(t, p, task)
	})

	t.Run("P-gate", func(t *testing.T) {
		p := new(P)
		task := newYieldingTestG(t, "service-request-p")
		action := beginWaitTestResume(t, p, task)
		if !RequestSchedule(p) || !PollPreempt(task.g) {
			t.Fatal("P scheduling request was not observed")
		}
		if p.servicePreemptBudget != servicePreemptPollBudget {
			t.Fatalf("P scheduling request consumed service budget: got %d", p.servicePreemptBudget)
		}
		yieldRunningDriverTask(t, p, task, action)
		finishServicePreemptTestTask(t, p, task)
	})

	t.Run("executor-gate", func(t *testing.T) {
		p := new(P)
		driver, registry, waits, executor := bindTestExecutorDriver(t, p)
		task := newYieldingTestG(t, "service-request-executor")
		action := beginWaitTestResume(t, p, task)
		if registry.Request(executor) != ExecutorRequestPublished || !PollPreempt(task.g) {
			t.Fatal("executor scheduling request was not observed")
		}
		if p.servicePreemptBudget != servicePreemptPollBudget {
			t.Fatalf("executor request consumed service budget: got %d", p.servicePreemptBudget)
		}
		yieldRunningDriverTask(t, p, task, action)
		if promoted, ok := PollReady(p); !ok || promoted != 0 || registry.ObserveRequested(executor) {
			t.Fatalf("executor request acknowledgment = (%d, %t), requested=%t", promoted, ok, registry.ObserveRequested(executor))
		}
		closeTestExecutorDriver(t, driver)
		finishServicePreemptTestTask(t, p, task)
		if !waits.CanRelease() || !registry.CanRelease() {
			t.Fatal("executor request priority retained executor state")
		}
	})
}
