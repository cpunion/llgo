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
	"sync"
	"testing"
	"unsafe"
)

func newActiveCriticalTestG(t *testing.T, name string) (*P, *yieldingTestG, Action) {
	t.Helper()
	p := new(P)
	task := newYieldingTestG(t, name)
	action, ok := BeginRunG(p, task.g)
	if !ok {
		t.Fatalf("begin critical G %s", name)
	}
	action = activatePreemptTestFrame(t, p, task, action)
	return p, task, action
}

func TestCriticalDepthNestingPreservesAndConsumesStickyRequests(t *testing.T) {
	p, task, _ := newActiveCriticalTestG(t, "critical-nesting")
	initialBudget := p.servicePreemptBudget
	if !EnterCritical(task.g) || !EnterCritical(task.g) {
		t.Fatal("enter nested critical region")
	}
	if !RequestPreempt(task.g) || !RequestSchedule(p) {
		t.Fatal("publish G/P request inside critical region")
	}
	before := loadGPreempt(task.g)
	if preemptWordDepth(before) != 2 || preemptWordState(before) != preemptRequested {
		t.Fatalf("nested packed word = %#x, depth=%d state=%d", before, preemptWordDepth(before), preemptWordState(before))
	}
	if PollPreempt(task.g) || loadGPreempt(task.g) != before ||
		preemptLoad(&p.schedule) != scheduleRequested || p.servicePreemptBudget != initialBudget {
		t.Fatal("masked poll consumed a G/P request or service budget")
	}
	if mustYield, ok := ExitCritical(task.g); !ok || mustYield ||
		preemptWordDepth(loadGPreempt(task.g)) != 1 ||
		preemptWordState(loadGPreempt(task.g)) != preemptRequested ||
		preemptLoad(&p.schedule) != scheduleRequested || p.servicePreemptBudget != initialBudget {
		t.Fatalf("nested exit = (%t,%t), word=%#x schedule=%d budget=%d",
			mustYield, ok, loadGPreempt(task.g), preemptLoad(&p.schedule), p.servicePreemptBudget)
	}
	if mustYield, ok := ExitCritical(task.g); !ok || !mustYield {
		t.Fatalf("outer request exit = (%t,%t)", mustYield, ok)
	}
	if word := loadGPreempt(task.g); preemptWordDepth(word) != 0 || preemptWordState(word) != preemptIdle ||
		preemptLoad(&p.schedule) != scheduleIdle || p.servicePreemptBudget != initialBudget {
		t.Fatalf("outer exit did not consume exact G/P requests: word=%#x schedule=%d budget=%d",
			word, preemptLoad(&p.schedule), p.servicePreemptBudget)
	}
	if PollPreempt(task.g) || p.servicePreemptBudget != initialBudget-1 {
		t.Fatal("consumed request replayed or ordinary budget poll was skipped")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestCriticalPollDoesNotConsumeBoundExecutorRequest(t *testing.T) {
	p := new(P)
	_, registry, _, executor := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "critical-executor")
	action, ok := BeginRunG(p, task.g)
	if !ok {
		t.Fatal("begin bound-executor critical G")
	}
	_ = activatePreemptTestFrame(t, p, task, action)
	if !EnterCritical(task.g) {
		t.Fatal("enter bound-executor critical region")
	}
	initialBudget := p.servicePreemptBudget
	if registry.Request(executor) != ExecutorRequestPublished {
		t.Fatal("publish bound executor request")
	}
	if PollPreempt(task.g) || !registry.ObserveRequested(executor) || p.servicePreemptBudget != initialBudget {
		t.Fatal("masked poll consumed a bound executor request or budget")
	}
	if mustYield, exitOK := ExitCritical(task.g); !exitOK || !mustYield {
		t.Fatalf("outer executor exit = (%t,%t)", mustYield, exitOK)
	}
	if !registry.ObserveRequested(executor) || p.servicePreemptBudget != initialBudget {
		t.Fatal("outer exit consumed scheduler-owned executor request")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestCriticalExitObservesButDoesNotClaimTaskCancellation(t *testing.T) {
	p, task, _ := newActiveCriticalTestG(t, "critical-cancel")
	if !EnterCritical(task.g) || !RequestTaskCancellation(p, task.g, TaskCancelAbort) {
		t.Fatal("enter critical region and publish task cancellation")
	}
	initialBudget := p.servicePreemptBudget
	if PollPreempt(task.g) || p.servicePreemptBudget != initialBudget ||
		task.g.park.taskCancelPhase != taskCancelRequested {
		t.Fatal("masked poll consumed budget or task cancellation")
	}
	if kind, claimed := ClaimTaskCancellation(p, task.g); claimed || kind != TaskCancelNone {
		t.Fatalf("critical cancellation claim = (%d,%t)", kind, claimed)
	}
	if mustYield, ok := ExitCritical(task.g); !ok || !mustYield {
		t.Fatalf("outer cancellation exit = (%t,%t)", mustYield, ok)
	}
	if task.g.park.taskCancelPhase != taskCancelRequested ||
		p.servicePreemptBudget != initialBudget-1 {
		t.Fatal("outer exit claimed cancellation or failed to charge its legal poll")
	}
	if kind, claimed := ClaimTaskCancellation(p, task.g); !claimed || kind != TaskCancelAbort ||
		task.g.park.taskCancelPhase != taskCancelCleanup {
		t.Fatalf("post-exit cancellation claim = (%d,%t), phase=%d",
			kind, claimed, task.g.park.taskCancelPhase)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestCriticalDepthOverflowUnderflowFailsWithoutMutation(t *testing.T) {
	p, task, _ := newActiveCriticalTestG(t, "critical-bounds")
	word, ok := preemptWordAtDepth(preemptRequested, preemptDepthLimit)
	if !ok {
		t.Fatal("construct maximum packed critical word")
	}
	beforeBudget := p.servicePreemptBudget
	before := loadGPreempt(task.g)
	if mustYield, exitOK := ExitCritical(task.g); exitOK || mustYield ||
		loadGPreempt(task.g) != before || p.servicePreemptBudget != beforeBudget {
		t.Fatal("zero-depth exit did not fail without mutation")
	}
	preemptStore(preemptAddress(task.g), word)
	if EnterCritical(task.g) || loadGPreempt(task.g) != word || p.servicePreemptBudget != beforeBudget {
		t.Fatal("critical depth overflow mutated packed state")
	}
	preemptStore(preemptAddress(task.g), preemptIdle)
	if !EnterCritical(task.g) {
		t.Fatal("enter critical region after restoring valid gate")
	}
	if mustYield, exitOK := ExitCritical(task.g); !exitOK || mustYield {
		t.Fatalf("balanced outer exit = (%t,%t)", mustYield, exitOK)
	}
	balanced := loadGPreempt(task.g)
	if mustYield, exitOK := ExitCritical(task.g); exitOK || mustYield || loadGPreempt(task.g) != balanced {
		t.Fatal("second exit underflow mutated packed state")
	}
	dirty := new(G)
	preemptStore(preemptAddress(dirty), preemptDepthUnit|preemptDisabled)
	if InitG(dirty) {
		t.Fatal("InitG accepted a residual critical depth")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestCriticalRequestRacesEnterAndExitWithoutLosingRequest(t *testing.T) {
	_, task, _ := newActiveCriticalTestG(t, "critical-request-race")
	const iterations = 500
	for iteration := 0; iteration < iterations; iteration++ {
		start := make(chan struct{})
		var accepted bool
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			accepted = RequestPreempt(task.g)
		}()
		close(start)
		if !EnterCritical(task.g) {
			t.Fatalf("iteration %d: enter racing critical region", iteration)
		}
		wg.Wait()
		word := loadGPreempt(task.g)
		if !accepted || preemptWordDepth(word) != 1 || preemptWordState(word) != preemptRequested {
			t.Fatalf("iteration %d: enter race word=%#x accepted=%t", iteration, word, accepted)
		}
		if mustYield, exitOK := ExitCritical(task.g); !exitOK || !mustYield {
			t.Fatalf("iteration %d: consume enter-race request = (%t,%t)", iteration, mustYield, exitOK)
		}
	}

	for iteration := 0; iteration < iterations; iteration++ {
		if !EnterCritical(task.g) {
			t.Fatalf("iteration %d: enter exit-race critical region", iteration)
		}
		start := make(chan struct{})
		var accepted bool
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			accepted = RequestPreempt(task.g)
		}()
		close(start)
		mustYield, exitOK := ExitCritical(task.g)
		wg.Wait()
		if !exitOK || !accepted {
			t.Fatalf("iteration %d: exit race = (%t,%t), accepted=%t", iteration, mustYield, exitOK, accepted)
		}
		word := loadGPreempt(task.g)
		if preemptWordDepth(word) != 0 {
			t.Fatalf("iteration %d: exit race retained depth in %#x", iteration, word)
		}
		if preemptWordState(word) == preemptRequested {
			if !PollPreempt(task.g) {
				t.Fatalf("iteration %d: late exit-race request was not sticky", iteration)
			}
		} else if preemptWordState(word) != preemptIdle || !mustYield {
			t.Fatalf("iteration %d: exit race lost request: word=%#x yielded=%t", iteration, word, mustYield)
		}
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestCriticalDepthRejectsSuspendTransferAndTerminalPaths(t *testing.T) {
	p, task, action := newActiveCriticalTestG(t, "critical-rejections")
	if !EnterCritical(task.g) {
		t.Fatal("enter transition-rejection critical region")
	}
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if PrepareYield(task.g, task.handle, task.frame.header) || task.g.pending != (pendingTransition{}) {
		t.Fatal("critical region admitted yield")
	}
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if PrepareComplete(task.g, task.handle, task.frame.header) || task.g.pending != (pendingTransition{}) {
		t.Fatal("critical region admitted completion")
	}
	token := new(WaitToken)
	ticket, armed := ArmWait(token)
	beforeToken := preemptLoad(&token.word)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !armed || PreparePark(task.g, task.handle, task.frame.header, token, ticket) ||
		preemptLoad(&token.word) != beforeToken || task.g.pending != (pendingTransition{}) {
		t.Fatal("critical region admitted park or consumed its wait token")
	}
	task.frame.header.SuspendReason = uint16(SuspendPanic)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if PreparePanic(task.g, task.handle, task.frame.header, unsafe.Pointer(new(byte)), nil) ||
		!emptyPanicRecord(&task.g.panicRecord) || task.g.pending != (pendingTransition{}) {
		t.Fatal("critical region admitted terminal panic")
	}
	if _, ok := Resumed(p, task.g, action); ok {
		t.Fatal("critical region admitted resume-return commit")
	}
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	if _, ok := ExitCritical(task.g); !ok {
		t.Fatal("exit transition-rejection critical region")
	}

	source, target := new(P), new(P)
	transfer := newYieldingTestG(t, "critical-transfer")
	if !Enqueue(source, transfer.g) {
		t.Fatal("enqueue transfer rejection G")
	}
	masked, _ := preemptWordAtDepth(preemptIdle, 1)
	preemptStore(preemptAddress(transfer.g), masked)
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind transfer rejection mailbox")
	}
	if id, ok := PublishPNeutralRunnable(&mailbox, source, transfer.g); ok || id != (RunnableTransferID{}) ||
		source.readyHead != transfer.g || !transfer.g.queued || mailbox.count != 0 {
		t.Fatal("non-zero-depth G crossed P-neutral transfer")
	}

	terminal := &G{magic: gMagic, state: GDead}
	preemptStore(preemptAddress(terminal), preemptDepthUnit|preemptDisabled)
	terminalP := new(P)
	preemptStore(&terminalP.schedule, scheduleDisabled)
	if ReclaimableG(terminal) || TerminalG(terminalP, terminal) || disableGPreempt(terminal) {
		t.Fatal("non-zero-depth G crossed terminal/reclaim boundary")
	}
	runtime.KeepAlive(task.frame.memory)
	runtime.KeepAlive(transfer.frame.memory)
}
