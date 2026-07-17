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

func consumeTimerPreemptTestBudget(t *testing.T, p *P, g *G) {
	t.Helper()
	if p.timerPreemptBudget != timerPreemptPollBudget {
		t.Fatalf("initial timer preemption budget = %d, want %d", p.timerPreemptBudget, timerPreemptPollBudget)
	}
	for poll := uint32(1); poll < timerPreemptPollBudget; poll++ {
		if PollPreempt(g) {
			t.Fatalf("timer preemption fired at safepoint %d, want %d", poll, timerPreemptPollBudget)
		}
		if want := timerPreemptPollBudget - poll; p.timerPreemptBudget != want {
			t.Fatalf("timer preemption budget after safepoint %d = %d, want %d", poll, p.timerPreemptBudget, want)
		}
	}
	if !PollPreempt(g) {
		t.Fatalf("timer preemption did not fire at safepoint %d", timerPreemptPollBudget)
	}
	if p.timerPreemptBudget != timerPreemptPollBudget {
		t.Fatalf("fired timer preemption budget = %d, want reload %d", p.timerPreemptBudget, timerPreemptPollBudget)
	}
}

func TestTimerPreemptBudgetLeavesNoTimerSemanticsUnchanged(t *testing.T) {
	p := new(P)
	driver, registry, waits, timers, _ := bindTestExecutorDriverWithTimers(t, p)
	task := newYieldingTestG(t, "timer-budget-empty")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue empty-timer task")
	}
	if next, ok := NextRunnableAt(p, 0); !ok || next != task.g {
		t.Fatal("dequeue empty-timer task")
	}

	// A stale idle budget is an ownership violation. BeginRunG must reject it
	// without publishing current or changing the G, and close must not accept
	// an idle P with that residual run-slice state.
	p.timerPreemptBudget = 1
	if action, ok := BeginRunG(p, task.g); ok || action != (Action{}) || p.current != nil || task.g.state != GRunnable {
		t.Fatalf("begin accepted stale timer budget = (%+v, %t)", action, ok)
	}
	if BeginExecutorClose(driver) {
		t.Fatal("executor close accepted stale timer preemption budget")
	}
	p.timerPreemptBudget = 0

	action := beginWaitTestResume(t, p, task)
	if p.timerPreemptBudget != 0 {
		t.Fatalf("empty timer table armed budget %d", p.timerPreemptBudget)
	}
	for poll := uint32(0); poll < timerPreemptPollBudget*2; poll++ {
		if PollPreempt(task.g) {
			t.Fatalf("empty timer table requested periodic preemption at poll %d", poll+1)
		}
	}
	yieldRunningDriverTask(t, p, task, action)
	if p.timerPreemptBudget != 0 {
		t.Fatalf("yield retained empty-timer budget %d", p.timerPreemptBudget)
	}
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !TerminalG(p, task.g) || !waits.CanRelease() || !timers.CanRelease() || !registry.CanRelease() {
		t.Fatal("empty timer budget cleanup retained state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestTimerPreemptBudgetPublishesDueTimerBehindSoleCPUG(t *testing.T) {
	p := new(P)
	driver, registry, waits, timers, _ := bindTestExecutorDriverWithTimers(t, p)
	timerTask := newYieldingTestG(t, "timer-budget-waiter")
	cpuTask := newYieldingTestG(t, "timer-budget-cpu")
	if !Enqueue(p, timerTask.g) || !Enqueue(p, cpuTask.g) {
		t.Fatal("enqueue timer-budget tasks")
	}
	token, ticket, timer := parkRegisteredDriverTimer(t, driver, p, timerTask, 0, 100)

	if next, ok := NextRunnableAt(p, 0); !ok || next != cpuTask.g {
		t.Fatal("dequeue sole CPU task before timer deadline")
	}
	action := beginWaitTestResume(t, p, cpuTask)
	consumeTimerPreemptTestBudget(t, p, cpuTask.g)
	yieldRunningDriverTask(t, p, cpuTask, action)
	if p.timerPreemptBudget != 0 || p.current != nil {
		t.Fatalf("timer-budget yield retained run state: budget=%d current=%p", p.timerPreemptBudget, p.current)
	}

	// The outer executor loop supplies the fresh sample after the budgeted
	// yield. The CPU G was queued first, but the same NextRunnableAt transaction
	// must publish the due timer and append its waiter to the ready queue.
	if next, ok := NextRunnableAt(p, 100); !ok || next != cpuTask.g || p.readyHead != timerTask.g || p.readyTail != timerTask.g {
		t.Fatalf("due timer was not published behind CPU G: next=%p ok=%t ready=(%p,%p)", next, ok, p.readyHead, p.readyTail)
	}
	action = beginWaitTestResume(t, p, cpuTask)
	if p.timerPreemptBudget != 0 {
		t.Fatalf("delivered timer incorrectly armed budget %d", p.timerPreemptBudget)
	}
	if !PollPreempt(cpuTask.g) || PollPreempt(cpuTask.g) {
		t.Fatal("due timer competitor did not produce exactly one ordinary preemption")
	}
	yieldRunningDriverTask(t, p, cpuTask, action)

	if next, ok := NextRunnableAt(p, 100); !ok || next != timerTask.g {
		t.Fatal("dequeue published timer waiter")
	}
	action = beginWaitTestResume(t, p, timerTask)
	if !RetireCompletedExecutorTimer(driver, token, ticket, timer) {
		t.Fatal("retire budget-published timer")
	}
	finishWaitTestTask(t, p, timerTask, action)

	if next, ok := NextRunnableAt(p, 100); !ok || next != cpuTask.g {
		t.Fatal("dequeue CPU task for cleanup")
	}
	action = beginWaitTestResume(t, p, cpuTask)
	if p.timerPreemptBudget != 0 || PollPreempt(cpuTask.g) {
		t.Fatal("retired timer retained preemption pressure")
	}
	yieldRunningDriverTask(t, p, cpuTask, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{cpuTask.g: cpuTask})
	if !TerminalG(p, timerTask.g) || !TerminalG(p, cpuTask.g) ||
		!waits.CanRelease() || !timers.CanRelease() || !registry.CanRelease() {
		t.Fatal("timer-budget cleanup retained state")
	}
	runtime.KeepAlive(timerTask.frame.memory)
	runtime.KeepAlive(cpuTask.frame.memory)
}
