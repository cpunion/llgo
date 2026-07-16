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

func TestWaitTicketGenerationRejectsDuplicateAndABACompletion(t *testing.T) {
	if ticket, ok := ArmWait(nil); ok || ticket != 0 || CompleteWait(nil, 1) {
		t.Fatal("nil wait token accepted")
	}
	token := new(WaitToken)
	first, ok := ArmWait(token)
	if !ok || first == 0 {
		t.Fatal("arm first wait generation")
	}
	if CompleteWait(token, 0) || !CompleteWait(token, first) || CompleteWait(token, first) {
		t.Fatal("first generation did not enforce one exact completion")
	}
	if ticket, ok := ArmWait(token); ok || ticket != 0 {
		t.Fatal("ready wait token rearmed before scheduler consumption")
	}
	if !claimWait(token, first) || !consumeWait(token, first) {
		t.Fatal("consume first ready generation")
	}
	second, ok := ArmWait(token)
	if !ok || second == 0 || second == first {
		t.Fatalf("second generation = %d, first = %d", second, first)
	}
	if CompleteWait(token, first) {
		t.Fatal("stale first-generation completion woke second generation")
	}
	if !claimWait(token, second) || !CompleteWait(token, second) || !consumeWait(token, second) {
		t.Fatal("complete and consume second generation")
	}

	preemptStore(&token.word, waitWord(waitMaxGen, waitConsumed))
	if ticket, ok := ArmWait(token); ok || ticket != 0 {
		t.Fatal("generation counter wrapped and reopened an ABA window")
	}
}

func TestWaitTicketRejectsTruncatingOutOfRangeAlias(t *testing.T) {
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm alias test token")
	}
	// Before the range check, shifting this value discarded its high bit and
	// produced the exact same atomic word as ticket 1.
	alias := WaitTicket(uint32(ticket) + waitMaxGen + 1)
	if validWaitTicket(alias) || CompleteWait(token, alias) || claimWait(token, alias) || consumeWait(token, alias) {
		t.Fatalf("out-of-range alias ticket %d was accepted", alias)
	}
	if !claimWait(token, ticket) || !CompleteWait(token, ticket) || !consumeWait(token, ticket) {
		t.Fatal("rejecting alias damaged the valid generation")
	}
}

func TestWaitClaimAndCompletionRace(t *testing.T) {
	const iterations = 1000
	for iteration := 0; iteration < iterations; iteration++ {
		token := new(WaitToken)
		ticket, ok := ArmWait(token)
		if !ok {
			t.Fatalf("iteration %d: arm token", iteration)
		}
		start := make(chan struct{})
		results := make(chan bool, 2)
		go func() {
			<-start
			results <- claimWait(token, ticket)
		}()
		go func() {
			<-start
			results <- CompleteWait(token, ticket)
		}()
		close(start)
		if !<-results || !<-results || !consumeWait(token, ticket) {
			t.Fatalf("iteration %d: claim/completion race lost transition", iteration)
		}
	}
}

func TestWaitClaimAllowsExactlyOneConcurrentWaiter(t *testing.T) {
	const iterations = 1000
	for iteration := 0; iteration < iterations; iteration++ {
		token := new(WaitToken)
		ticket, ok := ArmWait(token)
		if !ok {
			t.Fatalf("iteration %d: arm token", iteration)
		}
		start := make(chan struct{})
		results := make(chan bool, 2)
		for waiter := 0; waiter < 2; waiter++ {
			go func() {
				<-start
				results <- claimWait(token, ticket)
			}()
		}
		close(start)
		first, second := <-results, <-results
		if first == second {
			t.Fatalf("iteration %d: claim results = %t, %t; want exactly one", iteration, first, second)
		}
		if !CompleteWait(token, ticket) || !consumeWait(token, ticket) {
			t.Fatalf("iteration %d: winning waiter could not consume completion", iteration)
		}
	}
}

func TestWaitAtomicFieldsAre32BitAligned(t *testing.T) {
	if unsafe.Offsetof(WaitToken{}.word)%4 != 0 || unsafe.Alignof(WaitToken{}) < 4 {
		t.Fatalf("WaitToken atomic word is not 32-bit aligned: offset=%d align=%d", unsafe.Offsetof(WaitToken{}.word), unsafe.Alignof(WaitToken{}))
	}
	if unsafe.Offsetof(G{}.preempt)%4 != 0 || unsafe.Offsetof(P{}.schedule)%4 != 0 {
		t.Fatalf("scheduler atomic words are not 32-bit aligned: G.preempt=%d P.schedule=%d", unsafe.Offsetof(G{}.preempt), unsafe.Offsetof(P{}.schedule))
	}
}

func beginWaitTestResume(t *testing.T, p *P, task *yieldingTestG) Action {
	t.Helper()
	action, ok := BeginRunG(p, task.g)
	if !ok || action.Kind != ActionCheckResume {
		t.Fatalf("begin G %s = (%+v, %t)", task.name, action, ok)
	}
	action, ok = Checked(p, task.g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatalf("activate G %s = (%+v, %t)", task.name, action, ok)
	}
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	return action
}

func finishWaitTestTask(t *testing.T, p *P, task *yieldingTestG, action Action) {
	t.Helper()
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatalf("prepare completion for G %s", task.name)
	}
	action, ok := Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume completion for G %s = (%+v, %t)", task.name, action, ok)
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check destroy for G %s = (%+v, %t)", task.name, action, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	action, ok = Destroyed(p, task.g, action)
	if !ok || action.Kind != ActionComplete {
		t.Fatalf("destroy G %s = (%+v, %t)", task.name, action, ok)
	}
}

func prepareWaitTestRootDestroy(t *testing.T, p *P, task *yieldingTestG, action Action) Action {
	t.Helper()
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare root completion")
	}
	action, ok := Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume root completion = (%+v, %t)", action, ok)
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check root destroy = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	return action
}

func TestSinglePParkWakeHandlesEarlyCompletionWithoutLostWake(t *testing.T) {
	p := new(P)
	parked := newYieldingTestG(t, "parked")
	competitor := newYieldingTestG(t, "competitor")
	if !Enqueue(p, parked.g) || !Enqueue(p, competitor.g) {
		t.Fatal("enqueue park/wake tasks")
	}

	g, ok := NextRunnable(p)
	if !ok || g != parked.g {
		t.Fatalf("first runnable = %p, want parked G %p", g, parked.g)
	}
	action := beginWaitTestResume(t, p, parked)
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm early-completion token")
	}
	// Complete before the coroutine publishes its park transition. PreparePark
	// must accept the exact ready generation, and NextRunnable must promote it
	// behind the already-runnable competitor.
	if !CompleteWait(token, ticket) {
		t.Fatal("publish early completion")
	}
	parked.frame.header.SuspendReason = uint16(SuspendPark)
	parked.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(parked.g, parked.handle, parked.frame.header, token, ticket) {
		t.Fatal("prepare already-completed park")
	}
	action, ok = Resumed(p, parked.g, action)
	if !ok || action.Kind != ActionPark || action.Handle != nil || parked.g.state != GWaiting || !HasWaiting(p) {
		t.Fatalf("park action = (%+v, %t), state=%d waiting=%t", action, ok, parked.g.state, HasWaiting(p))
	}

	g, ok = NextRunnable(p)
	if !ok || g != competitor.g {
		t.Fatalf("runnable after early completion = %p, want competitor %p", g, competitor.g)
	}
	finishWaitTestTask(t, p, competitor, beginWaitTestResume(t, p, competitor))
	g, ok = NextRunnable(p)
	if !ok || g != parked.g || HasWaiting(p) {
		t.Fatalf("promoted parked G = %p, ok=%t waiting=%t", g, ok, HasWaiting(p))
	}
	finishWaitTestTask(t, p, parked, beginWaitTestResume(t, p, parked))
	if next, ok := NextRunnable(p); !ok || next != nil {
		t.Fatalf("terminal ready queue = (%p, %t)", next, ok)
	}
	if !TerminalG(p, parked.g) || !TerminalG(p, competitor.g) {
		t.Fatal("park/wake run retained scheduler state")
	}
	runtime.KeepAlive(parked.frame.memory)
	runtime.KeepAlive(competitor.frame.memory)
}

func TestSinglePParkWakeLateConcurrentCompletion(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "late")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue late-completion task")
	}
	g, ok := NextRunnable(p)
	if !ok || g != task.g {
		t.Fatal("dequeue late-completion task")
	}
	action := beginWaitTestResume(t, p, task)
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm late-completion token")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("prepare late park")
	}
	action, ok = Resumed(p, task.g, action)
	if !ok || action.Kind != ActionPark || !HasWaiting(p) {
		t.Fatal("commit late park")
	}
	if next, ok := NextRunnable(p); !ok || next != nil || !HasWaiting(p) {
		t.Fatalf("armed wait appeared runnable: (%p, %t), waiting=%t", next, ok, HasWaiting(p))
	}
	done := make(chan bool, 1)
	go func() {
		done <- CompleteWait(token, ticket)
	}()
	if !<-done {
		t.Fatal("concurrent late completion rejected")
	}
	if count, ok := PollReady(p); !ok || count != 1 || HasWaiting(p) {
		t.Fatalf("poll ready = (%d, %t), waiting=%t", count, ok, HasWaiting(p))
	}
	g, ok = NextRunnable(p)
	if !ok || g != task.g {
		t.Fatal("late-completed G not promoted")
	}
	finishWaitTestTask(t, p, task, beginWaitTestResume(t, p, task))
	if !TerminalG(p, task.g) {
		t.Fatal("late park/wake retained scheduler state")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestWaitCompletionPublishesResultAcrossThreads(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "publication")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue publication task")
	}
	g, ok := NextRunnable(p)
	if !ok || g != task.g {
		t.Fatal("dequeue publication task")
	}
	action := beginWaitTestResume(t, p, task)
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm publication token")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("prepare publication park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatal("commit publication park")
	}

	type resultRecord struct {
		sequence uint64
		inverse  uint64
	}
	const sequence = uint64(0x1020304050607080)
	result := new(resultRecord)
	producerDone := make(chan struct{})
	go func() {
		result.sequence = sequence
		result.inverse = ^sequence
		if !CompleteWait(token, ticket) {
			panic("completion publication rejected")
		}
		if !RequestSchedule(p) {
			panic("schedule request rejected")
		}
		close(producerDone)
	}()

	// Do not receive producerDone before reading the result: the only
	// happens-before edge publishing these ordinary fields is the wait token's
	// atomic completion/consumption transition. This is also a race-detector
	// regression test for the runtime ABI contract.
	deadline := 100000
	for ; deadline > 0; deadline-- {
		count, pollOK := PollReady(p)
		if !pollOK {
			t.Fatal("poll publication wait")
		}
		if count == 1 {
			break
		}
		runtime.Gosched()
	}
	if deadline == 0 {
		t.Fatal("publication wait did not become ready")
	}
	if result.sequence != sequence || result.inverse != ^sequence {
		t.Fatalf("published result = (%#x, %#x)", result.sequence, result.inverse)
	}
	<-producerDone
	// The schedule request may race just after the idle poll that promoted the
	// G. A second idle observation acknowledges that harmless notification.
	if _, ok := PollReady(p); !ok {
		t.Fatal("acknowledge publication schedule request")
	}
	g, ok = NextRunnable(p)
	if !ok || g != task.g {
		t.Fatal("published waiter not runnable")
	}
	finishWaitTestTask(t, p, task, beginWaitTestResume(t, p, task))
	runtime.KeepAlive(task.frame.memory)
}

func TestCompletedWaitRequestsPreemptionWithoutReadingCurrentG(t *testing.T) {
	p := new(P)
	parked := newYieldingTestG(t, "wake-target")
	competitor := newYieldingTestG(t, "competitor")
	if !Enqueue(p, parked.g) || !Enqueue(p, competitor.g) {
		t.Fatal("enqueue wake/preempt tasks")
	}

	g, ok := NextRunnable(p)
	if !ok || g != parked.g {
		t.Fatal("dequeue wake target")
	}
	action := beginWaitTestResume(t, p, parked)
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm wake target")
	}
	parked.frame.header.SuspendReason = uint16(SuspendPark)
	parked.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(parked.g, parked.handle, parked.frame.header, token, ticket) {
		t.Fatal("prepare wake target park")
	}
	if action, ok = Resumed(p, parked.g, action); !ok || action.Kind != ActionPark {
		t.Fatal("commit wake target park")
	}

	g, ok = NextRunnable(p)
	if !ok || g != competitor.g {
		t.Fatal("dequeue competitor")
	}
	action = beginWaitTestResume(t, p, competitor)
	// No ready G remains, so the competitor starts with both its G-local gate
	// and P's independent scheduling gate idle.
	if PollPreempt(competitor.g) {
		t.Fatal("competitor started with a residual preemption request")
	}

	done := make(chan struct{})
	go func() {
		if !CompleteWait(token, ticket) || !RequestSchedule(p) {
			panic("complete/request-schedule wake")
		}
		close(done)
	}()
	<-done
	observed := false
	for poll := 0; poll < 64; poll++ {
		if PollPreempt(competitor.g) {
			observed = true
			break
		}
	}
	if !observed || PollPreempt(competitor.g) {
		t.Fatal("running competitor did not consume exactly one P-level wake request")
	}
	competitor.frame.header.SuspendReason = uint16(SuspendYield)
	competitor.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(competitor.g, competitor.handle, competitor.frame.header) {
		t.Fatal("prepare competitor yield")
	}
	if action, ok = Resumed(p, competitor.g, action); !ok || action.Kind != ActionYield {
		t.Fatal("commit competitor yield")
	}
	if count, ok := PollReady(p); !ok || count != 1 {
		t.Fatalf("promote completed wake target = (%d, %t)", count, ok)
	}
	if !parked.g.queued || !competitor.g.queued || HasWaiting(p) {
		t.Fatal("wake/preempt transition lost a runnable G or retained a waiter")
	}
	runtime.KeepAlive(parked.frame.memory)
	runtime.KeepAlive(competitor.frame.memory)
}

func TestRequestScheduleConcurrentCoalescing(t *testing.T) {
	p := new(P)
	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wg.Done()
			for iteration := 0; iteration < 1000; iteration++ {
				if !RequestSchedule(p) {
					t.Error("coalesced schedule request rejected")
					return
				}
			}
		}()
	}
	wg.Wait()
	if got := preemptLoad(&p.schedule); got != scheduleRequested {
		t.Fatalf("coalesced schedule gate = %d, want requested", got)
	}
	if count, ok := PollReady(p); !ok || count != 0 || preemptLoad(&p.schedule) != scheduleIdle {
		t.Fatalf("idle schedule acknowledgement = (%d, %t), gate=%d", count, ok, preemptLoad(&p.schedule))
	}
	preemptStore(&p.schedule, scheduleRequested+1)
	if RequestSchedule(p) {
		t.Fatal("corrupt schedule gate accepted")
	}
	if count, ok := PollReady(p); ok || count != 0 {
		t.Fatal("corrupt schedule gate did not fail closed")
	}
}

func TestTerminalDisableLinearizesWithLateScheduleRequest(t *testing.T) {
	const iterations = 250
	for iteration := 0; iteration < iterations; iteration++ {
		p := new(P)
		task := newYieldingTestG(t, "terminal-race")
		if !Enqueue(p, task.g) {
			t.Fatalf("iteration %d: enqueue task", iteration)
		}
		g, ok := NextRunnable(p)
		if !ok || g != task.g {
			t.Fatalf("iteration %d: dequeue task", iteration)
		}
		action := prepareWaitTestRootDestroy(t, p, task, beginWaitTestResume(t, p, task))

		start := make(chan struct{})
		requestResult := make(chan bool, 1)
		go func() {
			<-start
			requestResult <- RequestSchedule(p)
		}()
		close(start)
		terminalAction, terminalOK := Destroyed(p, task.g, action)
		requestOK := <-requestResult
		if terminalOK {
			if terminalAction.Kind != ActionComplete || requestOK || preemptLoad(&p.schedule) != scheduleDisabled ||
				!TerminalG(p, task.g) {
				t.Fatalf("iteration %d: terminal won race inconsistently: action=%+v request=%t gate=%d", iteration, terminalAction, requestOK, preemptLoad(&p.schedule))
			}
		} else {
			if !requestOK || preemptLoad(&p.schedule) != scheduleRequested || !task.g.destroyRoot ||
				task.g.state != GDispatching || p.current != task.g || p.action != action {
				t.Fatalf("iteration %d: request won race but terminal partially committed: request=%t gate=%d state=%d", iteration, requestOK, preemptLoad(&p.schedule), task.g.state)
			}
			if !AcknowledgeTerminalSchedule(p, task.g, action) {
				t.Fatalf("iteration %d: acknowledge winning late request", iteration)
			}
			terminalAction, terminalOK = Destroyed(p, task.g, action)
			if !terminalOK || terminalAction.Kind != ActionComplete || !TerminalG(p, task.g) {
				t.Fatalf("iteration %d: terminal retry = (%+v, %t)", iteration, terminalAction, terminalOK)
			}
		}
		for repeat := 0; repeat < 4; repeat++ {
			if RequestSchedule(p) || preemptLoad(&p.schedule) != scheduleDisabled {
				t.Fatalf("iteration %d: post-terminal request %d reopened gate", iteration, repeat)
			}
		}
		runtime.KeepAlive(task.frame.memory)
	}
}

func TestPollReadyRejectsCorruptQueuesBeforeConsumingWake(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "queue-validation")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue queue-validation task")
	}
	g, ok := NextRunnable(p)
	if !ok || g != task.g {
		t.Fatal("dequeue queue-validation task")
	}
	action := beginWaitTestResume(t, p, task)
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm queue-validation token")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("prepare queue-validation park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatal("commit queue-validation park")
	}
	if !CompleteWait(token, ticket) {
		t.Fatal("complete queue-validation wait")
	}

	// A detached ready tail used to let Enqueue report success while losing the
	// promoted G. Validation must reject before consuming the ready ticket.
	detached := &G{magic: gMagic, state: GRunnable, queued: true}
	p.readyTail = detached
	if count, ok := PollReady(p); ok || count != 0 {
		t.Fatalf("corrupt ready queue poll = (%d, %t)", count, ok)
	}
	if word := preemptLoad(&token.word); waitWordState(word) != waitParkedReady || task.g.state != GWaiting {
		t.Fatal("failed queue validation partially consumed the waiter")
	}
	p.readyTail = nil
	if count, ok := PollReady(p); !ok || count != 1 {
		t.Fatalf("repaired queue poll = (%d, %t)", count, ok)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestWaitQueueCycleFailsClosed(t *testing.T) {
	newWaitNode := func() *G {
		token := new(WaitToken)
		ticket, ok := ArmWait(token)
		if !ok || !claimWait(token, ticket) {
			t.Fatal("build claimed wait node")
		}
		return &G{
			magic:      gMagic,
			state:      GWaiting,
			waitToken:  token,
			waitTicket: ticket,
			waiting:    true,
		}
	}
	a, b, detachedTail := newWaitNode(), newWaitNode(), newWaitNode()
	a.nextWait = b
	b.nextWait = a
	p := &P{waitHead: a, waitTail: detachedTail}
	if count, ok := PollReady(p); ok || count != 0 {
		t.Fatalf("cyclic wait queue poll = (%d, %t)", count, ok)
	}
}

func TestPrepareParkFailsClosed(t *testing.T) {
	task := newYieldingTestG(t, "park-validation")
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm validation token")
	}
	if PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("park accepted outside active resume")
	}
	task.g.state = GRunning
	frame := FrameFromStorage(task.frame.storage)
	frame.state = FrameActive
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if PreparePark(task.g, task.handle, task.frame.header, token, ticket+1) {
		t.Fatal("park accepted a stale/unarmed ticket")
	}
	if !PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("valid park transition rejected")
	}
	if PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("duplicate park transition accepted")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestPrepareParkSameTicketAllowsExactlyOneG(t *testing.T) {
	first := newYieldingTestG(t, "first-waiter")
	second := newYieldingTestG(t, "second-waiter")
	for _, task := range []*yieldingTestG{first, second} {
		task.g.state = GRunning
		FrameFromStorage(task.frame.storage).state = FrameActive
		task.frame.header.SuspendReason = uint16(SuspendPark)
		task.frame.header.Lifecycle = uint16(FrameSuspended)
	}
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm shared wait ticket")
	}
	if !PreparePark(first.g, first.handle, first.frame.header, token, ticket) {
		t.Fatal("first G did not claim shared ticket")
	}
	if PreparePark(second.g, second.handle, second.frame.header, token, ticket) {
		t.Fatal("second G claimed the same token/ticket")
	}
	if second.g.pending.kind != pendingNone || second.g.pending.wait != nil || second.g.pending.ticket != 0 {
		t.Fatal("rejected second G retained a partial park transition")
	}
	if first.g.pending.kind != pendingPark || first.g.pending.wait != token || first.g.pending.ticket != ticket ||
		!validClaimedWait(token, ticket) {
		t.Fatal("winning G lost exact claimed wait ownership")
	}
	if !CompleteWait(token, ticket) || !consumeWait(token, ticket) {
		t.Fatal("winning G's claimed ticket could not complete")
	}
	runtime.KeepAlive(first.frame.memory)
	runtime.KeepAlive(second.frame.memory)
}
