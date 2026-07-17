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
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func activatePreemptTestFrame(t *testing.T, p *P, task *yieldingTestG, action Action) Action {
	t.Helper()
	if action.Kind != ActionCheckResume {
		t.Fatalf("initial action for G %s = %d, want check-resume", task.name, action.Kind)
	}
	action, ok := checkedTestAction(p, task.g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatalf("activate G %s = (%+v, %t), want resume", task.name, action, ok)
	}
	// LLVM coroutine entry/resume publishes this state before executing a poll.
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	return action
}

func TestPreemptPollFailsClosedAndConsumesOnlyActiveRequest(t *testing.T) {
	if RequestPreempt(nil) || PollPreempt(nil) || RequestPreempt(new(G)) || PollPreempt(new(G)) {
		t.Fatal("nil or uninitialized G accepted a preemption operation")
	}
	newG := new(G)
	if !InitG(newG) {
		t.Fatal("initialize validation G")
	}
	if !RequestPreempt(newG) {
		t.Fatal("initialized G did not publish its preemption gate")
	}
	if PollPreempt(newG) || preemptLoad(preemptAddress(newG)) != preemptRequested {
		t.Fatal("new-G poll consumed a request outside an active frame")
	}
	preemptStore(preemptAddress(newG), preemptDisabled)
	if RequestPreempt(newG) {
		t.Fatal("preemption requested through a disabled terminal gate")
	}
	dirtyG := new(G)
	preemptStore(preemptAddress(dirtyG), preemptRequested)
	if InitG(dirtyG) {
		t.Fatal("G initialized with a residual preemption request")
	}

	task := newYieldingTestG(t, "poll-validation")
	if !RequestPreempt(task.g) {
		t.Fatal("request runnable G")
	}
	if !RequestPreempt(task.g) {
		t.Fatal("coalesce duplicate runnable-G request")
	}
	if PollPreempt(task.g) || preemptLoad(preemptAddress(task.g)) != preemptRequested {
		t.Fatal("runnable poll consumed a request outside an active frame")
	}

	p := new(P)
	action, ok := BeginRunG(p, task.g)
	if !ok {
		t.Fatal("begin requested G")
	}
	if PollPreempt(task.g) || preemptLoad(preemptAddress(task.g)) != preemptRequested {
		t.Fatal("pre-resume poll consumed a request")
	}
	action, ok = checkedTestAction(p, task.g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatal("enter active resume")
	}
	if PollPreempt(task.g) || preemptLoad(preemptAddress(task.g)) != preemptRequested {
		t.Fatal("poll accepted an active frame before the compiler lifecycle state")
	}
	task.frame.header.Lifecycle = uint16(FrameActive)
	if !PollPreempt(task.g) || preemptLoad(preemptAddress(task.g)) != preemptIdle {
		t.Fatal("legal active poll did not consume the request")
	}
	if PollPreempt(task.g) {
		t.Fatal("one preemption request was consumed twice")
	}

	if !RequestPreempt(task.g) {
		t.Fatal("request running G")
	}
	task.g.pending = pendingTransition{kind: pendingAwait, from: task.g.active}
	if PollPreempt(task.g) || preemptLoad(preemptAddress(task.g)) != preemptRequested {
		t.Fatal("transitional poll consumed a request")
	}
	task.g.pending = pendingTransition{}
	if !PollPreempt(task.g) {
		t.Fatal("request did not survive the invalid transitional poll")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestBeginRunGDoesNotImmediatelyPreemptWithoutCompetitor(t *testing.T) {
	task := newYieldingTestG(t, "single")
	p := new(P)
	action, ok := BeginRunG(p, task.g)
	if !ok {
		t.Fatal("begin sole runnable G")
	}
	activatePreemptTestFrame(t, p, task, action)
	if PollPreempt(task.g) {
		t.Fatal("sole runnable G was preempted before its service quantum")
	}
	if p.servicePreemptBudget != servicePreemptPollBudget-1 {
		t.Fatalf("sole runnable G budget = %d, want %d", p.servicePreemptBudget, servicePreemptPollBudget-1)
	}
	runtime.KeepAlive(task.frame.memory)
}

// TestSinglePRoundRobinTwoGPreemptPoll models compiler polls driving the
// existing SuspendYield handoff. BeginRunG requests a cut only while another G
// remains ready, and each consumed request moves the current G to the tail.
func TestSinglePRoundRobinTwoGPreemptPoll(t *testing.T) {
	p := new(P)
	a := newYieldingTestG(t, "a")
	b := newYieldingTestG(t, "b")
	tasks := map[*G]*yieldingTestG{a.g: a, b.g: b}
	if !Enqueue(p, a.g) || !Enqueue(p, b.g) {
		t.Fatal("enqueue preemptible Gs")
	}

	var events []string
	for {
		g, ok := NextRunnable(p)
		if !ok {
			t.Fatal("dequeue preemptible G")
		}
		if g == nil {
			break
		}
		task := tasks[g]
		action, ok := BeginRunG(p, g)
		if !ok {
			t.Fatalf("begin G %s", task.name)
		}

	runSlice:
		for {
			switch action.Kind {
			case ActionCheckResume:
				action = activatePreemptTestFrame(t, p, task, action)
			case ActionResume:
				task.resumes++
				if task.resumes <= 2 {
					if !PollPreempt(g) {
						t.Fatalf("G %s slice %d missed competitor preemption", task.name, task.resumes)
					}
					events = append(events, fmt.Sprintf("%s:preempt:%d", task.name, task.resumes))
					task.frame.header.SuspendReason = uint16(SuspendYield)
					task.frame.header.Lifecycle = uint16(FrameSuspended)
					if !PrepareYield(g, task.handle, task.frame.header) {
						t.Fatalf("prepare preemptive yield for G %s", task.name)
					}
				} else {
					events = append(events, task.name+":complete")
					task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
					task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
					if !PrepareComplete(g, task.handle, task.frame.header) {
						t.Fatalf("prepare completion for G %s", task.name)
					}
				}
				action, ok = Resumed(p, g, action)
			case ActionCheckDestroy:
				action, ok = Checked(p, g, action, true)
			case ActionDestroy:
				releaseTestFrame(t, g, task.frame)
				action, ok = Destroyed(p, g, action)
			case ActionYield, ActionComplete:
				break runSlice
			default:
				t.Fatalf("unexpected action %d for G %s", action.Kind, task.name)
			}
			if !ok {
				t.Fatalf("preemptive action protocol failed for G %s", task.name)
			}
		}
	}

	want := []string{
		"a:preempt:1", "b:preempt:1",
		"a:preempt:2", "b:preempt:2",
		"a:complete", "b:complete",
	}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("preemptive round-robin events = %v, want %v", events, want)
	}
	if !TerminalG(p, a.g) || !TerminalG(p, b.g) {
		t.Fatal("preemptive round-robin retained scheduler state")
	}
	runtime.KeepAlive(a.frame.memory)
	runtime.KeepAlive(b.frame.memory)
}

func TestRequestPreemptConcurrentWithTerminalDisable(t *testing.T) {
	task := newYieldingTestG(t, "concurrent-terminal")
	p := new(P)
	action, ok := BeginRunG(p, task.g)
	if !ok {
		t.Fatal("begin concurrently requested G")
	}
	action = activatePreemptTestFrame(t, p, task, action)

	const workers = 8
	start := make(chan struct{})
	stop := make(chan struct{})
	accepted := make(chan struct{}, 1)
	var requests atomic.Uint64
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wg.Done()
			<-start
			for {
				select {
				case <-stop:
					return
				default:
				}
				if RequestPreempt(task.g) {
					requests.Add(1)
					select {
					case accepted <- struct{}{}:
					default:
					}
				}
			}
		}()
	}
	close(start)
	<-accepted

	// Complete and destroy the root while requesters are still racing with the
	// gate. They may coalesce requests before the terminal store, but none may
	// re-enable the gate after destruction disables it.
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare concurrently requested G completion")
	}
	action, ok = Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("complete concurrently requested G = (%+v, %t), want check-destroy", action, ok)
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check concurrently requested G destruction = (%+v, %t), want destroy", action, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	action, ok = Destroyed(p, task.g, action)
	if !ok || action.Kind != ActionComplete {
		t.Fatalf("destroy concurrently requested G = (%+v, %t), want complete", action, ok)
	}
	close(stop)
	wg.Wait()

	if requests.Load() == 0 {
		t.Fatal("concurrent requesters never observed the enabled gate")
	}
	if gate := preemptLoad(preemptAddress(task.g)); gate != preemptDisabled {
		t.Fatalf("terminal preemption gate = %d, want disabled", gate)
	}
	for attempt := 0; attempt < 1024; attempt++ {
		if RequestPreempt(task.g) {
			t.Fatal("terminal preemption gate was re-enabled by a late requester")
		}
	}
	if !TerminalG(p, task.g) {
		t.Fatal("concurrently requested G retained terminal scheduler state")
	}
	runtime.KeepAlive(task.frame.memory)
}
