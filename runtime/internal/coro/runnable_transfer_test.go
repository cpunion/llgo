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

func yieldRunnableForTransfer(t *testing.T, p *P, task *yieldingTestG) {
	t.Helper()
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue transfer yield task")
	}
	g, ok := NextRunnable(p)
	if !ok || g != task.g {
		t.Fatalf("dequeue transfer yield task = (%p, %t)", g, ok)
	}
	action, ok := BeginRunG(p, g)
	if !ok || action.Kind != ActionCheckResume {
		t.Fatalf("begin transfer yield task = (%+v, %t)", action, ok)
	}
	action, ok = checkedTestAction(p, g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatalf("check transfer yield task = (%+v, %t)", action, ok)
	}
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(g, task.handle, task.frame.header) {
		t.Fatal("prepare transfer yield task")
	}
	action, ok = Resumed(p, g, action)
	if !ok || action != (Action{Kind: ActionYield}) || !g.queued || p.readyHead != g {
		t.Fatalf("commit transfer yield task = (%+v, %t), queued=%t head=%p", action, ok, g.queued, p.readyHead)
	}
}

func TestRunnableTransferInitialAndYielded(t *testing.T) {
	for _, yielded := range []bool{false, true} {
		name := "initial"
		if yielded {
			name = "yielded"
		}
		t.Run(name, func(t *testing.T) {
			source, target := new(P), new(P)
			task := newYieldingTestG(t, name)
			if yielded {
				yieldRunnableForTransfer(t, source, task)
			} else if !Enqueue(source, task.g) {
				t.Fatal("enqueue initial transfer task")
			}
			var mailbox RunnableTransferMailbox
			if !BindRunnableTransferMailbox(&mailbox, target) {
				t.Fatal("bind runnable transfer mailbox")
			}

			id, ok := PublishPNeutralRunnable(&mailbox, source, task.g)
			if !ok || !id.Valid() || source.readyHead != nil || source.readyTail != nil ||
				task.g.queued || task.g.nextReady != nil || mailbox.count != 1 ||
				mailbox.slots[id.Slot-1].g != task.g || target.readyHead != nil {
				t.Fatalf("publish %s transfer = (%+v, %t), source=(%p,%p) queued=%t slot=%p target=%p",
					name, id, ok, source.readyHead, source.readyTail, task.g.queued,
					mailbox.slots[id.Slot-1].g, target.readyHead)
			}
			if !ImportPNeutralRunnable(&mailbox, target, id) || target.readyHead != task.g ||
				target.readyTail != task.g || !task.g.queued || mailbox.count != 0 ||
				mailbox.slots[id.Slot-1].g != nil {
				t.Fatalf("import %s transfer: head=%p tail=%p queued=%t count=%d slot=%p",
					name, target.readyHead, target.readyTail, task.g.queued, mailbox.count,
					mailbox.slots[id.Slot-1].g)
			}
			if ImportPNeutralRunnable(&mailbox, target, id) {
				t.Fatal("duplicate transfer import succeeded")
			}
			runnable, nextOK := NextRunnable(target)
			if !nextOK || runnable != task.g {
				t.Fatalf("dequeue imported %s transfer = (%p, %t)", name, runnable, nextOK)
			}
			if action, runOK := BeginRunG(target, runnable); !runOK || action.Kind != ActionCheckResume {
				t.Fatalf("run imported %s transfer = (%+v, %t)", name, action, runOK)
			}
		})
	}
}

func TestRunnableTransferStateUsesExistingGPadding(t *testing.T) {
	transferOffset := unsafe.Offsetof(G{}.transferState)
	rootOffset := unsafe.Offsetof(G{}.root)
	if transferOffset != 11 || unsafe.Sizeof(G{}.transferState) != 1 ||
		unsafe.Sizeof(G{}) != wantSchedulerGSize ||
		rootOffset != uintptr(12) && rootOffset != uintptr(16) {
		t.Fatalf("G transfer layout changed: transfer=%d/%d root=%d size=%d want=%d",
			transferOffset, unsafe.Sizeof(G{}.transferState), rootOffset, unsafe.Sizeof(G{}), wantSchedulerGSize)
	}
	if unsafe.Sizeof(RunnableTransferID{}) != 8 || unsafe.Alignof(RunnableTransferID{}) != 4 ||
		unsafe.Offsetof(RunnableTransferID{}.Slot) != 0 || unsafe.Offsetof(RunnableTransferID{}.Generation) != 4 {
		t.Fatalf("transfer ID is not two-word POD: size=%d align=%d slot=%d generation=%d",
			unsafe.Sizeof(RunnableTransferID{}), unsafe.Alignof(RunnableTransferID{}),
			unsafe.Offsetof(RunnableTransferID{}.Slot), unsafe.Offsetof(RunnableTransferID{}.Generation))
	}
	if unsafe.Offsetof(RunnableTransferMailbox{}.gate)%4 != 0 ||
		unsafe.Sizeof(RunnableTransferMailbox{}.gate) != 4 {
		t.Fatalf("transfer gate is not an aligned 32-bit atomic: offset=%d size=%d",
			unsafe.Offsetof(RunnableTransferMailbox{}.gate), unsafe.Sizeof(RunnableTransferMailbox{}.gate))
	}
	corruptInit := &G{transferState: runnableTransferGPublished}
	if InitG(corruptInit) {
		t.Fatal("InitG accepted a pre-published transfer state")
	}
	terminal := &G{magic: gMagic, state: GDead, transferState: runnableTransferGPublished}
	terminalP := new(P)
	preemptStore(&terminalP.schedule, scheduleDisabled)
	if ReclaimableG(terminal) || TerminalG(terminalP, terminal) {
		t.Fatal("terminal/reclaim accepted a published transfer state")
	}
}

func TestRunnableTransferEligibilityFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*yieldingTestG)
	}{
		{"run action", func(task *yieldingTestG) { task.g.runAction = ActionCheckResume }},
		{"pending transition", func(task *yieldingTestG) {
			task.g.pending = pendingTransition{kind: pendingYield, from: task.g.active}
		}},
		{"source result history", func(task *yieldingTestG) {
			task.g.park.ticket = ParkTicket{generation: 1}
			task.g.park.phase = parkDelivered
		}},
		{"task control lease", func(task *yieldingTestG) { task.g.taskControlLeases = 1 }},
		{"panic payload", func(task *yieldingTestG) {
			task.g.panicRecord.status = uint32(ExplicitStatusPanic)
			task.g.panicRecord.typeWord = unsafe.Pointer(new(byte))
		}},
		{"task cancellation", func(task *yieldingTestG) {
			task.g.park.taskCancelKind = TaskCancelAbort
			task.g.park.taskCancelPhase = taskCancelRequested
		}},
		{"spawn pin", func(task *yieldingTestG) { task.g.spawnParent = new(G) }},
		{"park source", func(task *yieldingTestG) { task.g.active.parkWait = new(WaitSetRecord) }},
		{"completion result", func(task *yieldingTestG) {
			task.g.active.completion.status = CompletionReturn
			task.g.active.completion.child = unsafe.Pointer(new(byte))
		}},
		{"preempt request", func(task *yieldingTestG) {
			preemptStore(preemptAddress(task.g), preemptRequested)
		}},
		{"non ordinary suspension", func(task *yieldingTestG) {
			task.g.active.state = FrameSuspended
			task.g.active.header.SuspendReason = uint16(SuspendCall)
			task.g.active.header.Lifecycle = uint16(FrameSuspended)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, target := new(P), new(P)
			task := newYieldingTestG(t, test.name)
			if !Enqueue(source, task.g) {
				t.Fatal("enqueue transfer eligibility task")
			}
			var mailbox RunnableTransferMailbox
			if !BindRunnableTransferMailbox(&mailbox, target) {
				t.Fatal("bind transfer eligibility mailbox")
			}
			test.mutate(task)
			before := mailbox
			if id, ok := PublishPNeutralRunnable(&mailbox, source, task.g); ok || id != (RunnableTransferID{}) {
				t.Fatalf("ineligible transfer published = (%+v, %t)", id, ok)
			}
			if mailbox != before || source.readyHead != task.g || source.readyTail != task.g ||
				!task.g.queued || task.g.nextReady != nil || task.g.transferState != runnableTransferGIdle {
				t.Fatalf("rejected transfer mutated ownership: mailbox=%+v source=(%p,%p) queued=%t next=%p",
					mailbox, source.readyHead, source.readyTail, task.g.queued, task.g.nextReady)
			}
		})
	}
}

func TestRunnableTransferOwnerAndBoundedFIFO(t *testing.T) {
	target, wrongOwner := new(P), new(P)
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind bounded transfer mailbox")
	}

	tasks := make([]*yieldingTestG, 3)
	ids := make([]RunnableTransferID, len(tasks))
	for index := range tasks {
		source := new(P)
		tasks[index] = newYieldingTestG(t, "bounded")
		if !Enqueue(source, tasks[index].g) {
			t.Fatalf("enqueue bounded transfer %d", index)
		}
		var ok bool
		ids[index], ok = PublishPNeutralRunnable(&mailbox, source, tasks[index].g)
		if !ok {
			t.Fatalf("publish bounded transfer %d", index)
		}
	}

	if ImportPNeutralRunnable(&mailbox, wrongOwner, ids[0]) || mailbox.count != 3 ||
		mailbox.slots[mailbox.head].g != tasks[0].g || wrongOwner.readyHead != nil {
		t.Fatal("wrong owner consumed or mutated transfer")
	}
	if tasks[0].g.transferState != runnableTransferGPublished ||
		preemptLoad(preemptAddress(tasks[0].g)) != preemptDisabled {
		t.Fatal("failed import released durable transfer state")
	}
	moved, more, ok := DrainPNeutralRunnables(&mailbox, target, 2)
	if !ok || moved != 2 || !more || mailbox.count != 1 || target.readyHead != tasks[0].g ||
		tasks[0].g.nextReady != tasks[1].g || target.readyTail != tasks[1].g {
		t.Fatalf("first bounded drain = (%d, %t, %t), count=%d queue=(%p,%p,%p)",
			moved, more, ok, mailbox.count, target.readyHead, tasks[0].g.nextReady, target.readyTail)
	}
	moved, more, ok = DrainPNeutralRunnables(&mailbox, target, 2)
	if !ok || moved != 1 || more || mailbox.count != 0 || target.readyTail != tasks[2].g ||
		tasks[1].g.nextReady != tasks[2].g || tasks[2].g.nextReady != nil {
		t.Fatalf("second bounded drain = (%d, %t, %t), count=%d tail=%p links=(%p,%p)",
			moved, more, ok, mailbox.count, target.readyTail, tasks[1].g.nextReady, tasks[2].g.nextReady)
	}
}

func TestRunnableTransferFullDoesNotLoseSourceRoot(t *testing.T) {
	target := new(P)
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind full transfer mailbox")
	}
	for index := uint32(0); index < RunnableTransferMailboxCapacity; index++ {
		source := new(P)
		task := newYieldingTestG(t, "fill")
		if !Enqueue(source, task.g) {
			t.Fatalf("enqueue fill transfer %d", index)
		}
		if _, ok := PublishPNeutralRunnable(&mailbox, source, task.g); !ok {
			t.Fatalf("publish fill transfer %d", index)
		}
	}

	source := new(P)
	overflow := newYieldingTestG(t, "overflow")
	if !Enqueue(source, overflow.g) {
		t.Fatal("enqueue overflow transfer")
	}
	before := mailbox
	if id, ok := PublishPNeutralRunnable(&mailbox, source, overflow.g); ok || id != (RunnableTransferID{}) {
		t.Fatalf("full mailbox accepted transfer = (%+v, %t)", id, ok)
	}
	if mailbox != before || mailbox.count != RunnableTransferMailboxCapacity ||
		source.readyHead != overflow.g || source.readyTail != overflow.g || !overflow.g.queued ||
		overflow.g.transferState != runnableTransferGIdle || preemptLoad(preemptAddress(overflow.g)) != preemptIdle {
		t.Fatalf("full rejection lost ownership: count=%d source=(%p,%p) queued=%t",
			mailbox.count, source.readyHead, source.readyTail, overflow.g.queued)
	}
	moved, more, ok := DrainPNeutralRunnables(&mailbox, target, RunnableTransferMailboxCapacity)
	if !ok || moved != RunnableTransferMailboxCapacity || more || mailbox.count != 0 {
		t.Fatalf("drain full mailbox = (%d, %t, %t), count=%d", moved, more, ok, mailbox.count)
	}
}

func TestRunnableTransferPublishedRejectsThirdPOwnership(t *testing.T) {
	source, target, third := new(P), new(P), new(P)
	task := newYieldingTestG(t, "unique-owner")
	if !Enqueue(source, task.g) {
		t.Fatal("enqueue unique-owner transfer")
	}
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind unique-owner mailbox")
	}
	id, ok := PublishPNeutralRunnable(&mailbox, source, task.g)
	if !ok {
		t.Fatal("publish unique-owner transfer")
	}
	if task.g.transferState != runnableTransferGPublished ||
		preemptLoad(preemptAddress(task.g)) != preemptDisabled || Enqueue(third, task.g) ||
		RequestPreempt(task.g) || third.readyHead != nil || third.readyTail != nil ||
		mailbox.count != 1 || mailbox.slots[id.Slot-1].g != task.g {
		t.Fatalf("published transfer acquired a second owner: state=%d preempt=%d third=(%p,%p) count=%d slot=%p",
			task.g.transferState, preemptLoad(preemptAddress(task.g)), third.readyHead, third.readyTail,
			mailbox.count, mailbox.slots[id.Slot-1].g)
	}
	if !ImportPNeutralRunnable(&mailbox, target, id) || task.g.transferState != runnableTransferGImported ||
		preemptLoad(preemptAddress(task.g)) != preemptIdle || target.readyHead != task.g || !task.g.queued ||
		mailbox.slots[id.Slot-1].g != nil {
		t.Fatal("owner import did not complete unique handoff")
	}
	var rebound RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&rebound, third) {
		t.Fatal("bind imported rebound mailbox")
	}
	if reboundID, reboundOK := PublishPNeutralRunnable(&rebound, target, task.g); reboundOK || reboundID != (RunnableTransferID{}) ||
		target.readyHead != task.g || task.g.transferState != runnableTransferGImported {
		t.Fatalf("imported initial rebounded before execution = (%+v,%t), head=%p state=%d",
			reboundID, reboundOK, target.readyHead, task.g.transferState)
	}
	if runnable, nextOK := NextRunnable(target); !nextOK || runnable != task.g ||
		task.g.transferState != runnableTransferGIdle {
		t.Fatalf("dequeue imported initial = (%p,%t), state=%d",
			runnable, nextOK, task.g.transferState)
	}
}

func TestRunnableTransferConcurrentTryPublishIsBounded(t *testing.T) {
	const producers = int(RunnableTransferMailboxCapacity) * 4
	target := new(P)
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind concurrent try mailbox")
	}
	sources := make([]*P, producers)
	tasks := make([]*yieldingTestG, producers)
	results := make([]struct {
		id RunnableTransferID
		ok bool
	}, producers)
	for index := range tasks {
		sources[index] = new(P)
		tasks[index] = newYieldingTestG(t, "concurrent-try")
		if !Enqueue(sources[index], tasks[index].g) {
			t.Fatalf("enqueue concurrent try %d", index)
		}
	}

	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(producers)
	for index := range tasks {
		go func(index int) {
			defer group.Done()
			<-start
			results[index].id, results[index].ok = PublishPNeutralRunnable(&mailbox, sources[index], tasks[index].g)
		}(index)
	}
	close(start)
	group.Wait()

	succeeded := uint32(0)
	ids := make(map[RunnableTransferID]bool)
	for index, result := range results {
		if result.ok {
			succeeded++
			if !result.id.Valid() || ids[result.id] || sources[index].readyHead != nil ||
				tasks[index].g.queued || tasks[index].g.transferState != runnableTransferGPublished {
				t.Fatalf("invalid successful concurrent try %d: id=%+v source=%p queued=%t state=%d",
					index, result.id, sources[index].readyHead, tasks[index].g.queued, tasks[index].g.transferState)
			}
			ids[result.id] = true
			continue
		}
		if result.id != (RunnableTransferID{}) || sources[index].readyHead != tasks[index].g ||
			sources[index].readyTail != tasks[index].g || !tasks[index].g.queued ||
			tasks[index].g.transferState != runnableTransferGIdle ||
			preemptLoad(preemptAddress(tasks[index].g)) != preemptIdle {
			t.Fatalf("contended try %d lost source ownership: id=%+v source=(%p,%p) queued=%t state=%d preempt=%d",
				index, result.id, sources[index].readyHead, sources[index].readyTail, tasks[index].g.queued,
				tasks[index].g.transferState, preemptLoad(preemptAddress(tasks[index].g)))
		}
	}
	if succeeded == 0 || succeeded > RunnableTransferMailboxCapacity || mailbox.count != succeeded {
		t.Fatalf("concurrent try successes=%d count=%d capacity=%d", succeeded, mailbox.count, RunnableTransferMailboxCapacity)
	}
	moved, more, ok := DrainPNeutralRunnables(&mailbox, target, RunnableTransferMailboxCapacity)
	if !ok || moved != succeeded || more {
		t.Fatalf("drain concurrent tries = (%d, %t, %t), want %d", moved, more, ok, succeeded)
	}
}

func TestRunnableTransferConcurrentPublishImportNoLoss(t *testing.T) {
	const producers = int(RunnableTransferMailboxCapacity) * 3
	target := new(P)
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind concurrent publish/import mailbox")
	}
	sources := make([]*P, producers)
	tasks := make([]*yieldingTestG, producers)
	for index := range tasks {
		sources[index] = new(P)
		tasks[index] = newYieldingTestG(t, "concurrent-publish-import")
		if !Enqueue(sources[index], tasks[index].g) {
			t.Fatalf("enqueue interleaved transfer %d", index)
		}
	}

	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(producers + 1)
	for index := range tasks {
		go func(index int) {
			defer group.Done()
			<-start
			for {
				if _, ok := PublishPNeutralRunnable(&mailbox, sources[index], tasks[index].g); ok {
					return
				}
				runtime.Gosched()
			}
		}(index)
	}
	imported := uint32(0)
	go func() {
		defer group.Done()
		<-start
		for imported < uint32(producers) {
			moved, _, ok := DrainPNeutralRunnables(&mailbox, target, 1)
			if ok {
				imported += moved
			}
			runtime.Gosched()
		}
	}()
	close(start)
	group.Wait()

	if imported != uint32(producers) || mailbox.count != 0 {
		t.Fatalf("interleaved transfer imported=%d count=%d", imported, mailbox.count)
	}
	seen := make(map[*G]bool, producers)
	count := 0
	for g := target.readyHead; g != nil; g = g.nextReady {
		if seen[g] {
			t.Fatalf("interleaved target contains duplicate G %p", g)
		}
		seen[g] = true
		count++
	}
	if count != producers || target.readyTail == nil || target.readyTail.nextReady != nil {
		t.Fatalf("interleaved target count=%d tail=%p", count, target.readyTail)
	}
	for index, task := range tasks {
		if !seen[task.g] || sources[index].readyHead != nil || sources[index].readyTail != nil ||
			!task.g.queued || task.g.transferState != runnableTransferGImported ||
			preemptLoad(preemptAddress(task.g)) != preemptIdle {
			t.Fatalf("interleaved task %d lost/duplicated: seen=%t source=(%p,%p) queued=%t state=%d preempt=%d",
				index, seen[task.g], sources[index].readyHead, sources[index].readyTail, task.g.queued,
				task.g.transferState, preemptLoad(preemptAddress(task.g)))
		}
	}
}

func TestRunnableTransferOwnerDrainReportsProducerContention(t *testing.T) {
	target := new(P)
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind contended drain mailbox")
	}
	preemptStore(&mailbox.gate, runnableTransferGateHeld)
	moved, more, status := TryDrainPNeutralRunnables(&mailbox, target, 1)
	if moved != 0 || more || status != RunnableTransferDrainContended {
		t.Fatalf("contended drain = (%d, %t, %d)", moved, more, status)
	}
	preemptStore(&mailbox.gate, runnableTransferGateIdle)
	moved, more, status = TryDrainPNeutralRunnables(&mailbox, target, 1)
	if moved != 0 || more || status != RunnableTransferDrainComplete {
		t.Fatalf("released drain = (%d, %t, %d)", moved, more, status)
	}
}

func TestRunnableTransferOwnerDrainDefersAcrossDispatchedAction(t *testing.T) {
	target := new(P)
	task := newYieldingTestG(t, "drain-dispatched-action")
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) || !Enqueue(target, task.g) {
		t.Fatal("prepare dispatched-action drain")
	}
	g, ok := NextRunnable(target)
	if !ok || g != task.g {
		t.Fatalf("dequeue dispatched-action task = (%p, %t)", g, ok)
	}
	action, ok := BeginRunG(target, g)
	if !ok || action.Kind != ActionCheckResume || action.Handle != task.handle {
		t.Fatalf("dispatch task = (%+v, %t)", action, ok)
	}
	moved, more, status := TryDrainPNeutralRunnables(&mailbox, target, 1)
	if moved != 0 || more || status != RunnableTransferDrainOwnerUnstable {
		t.Fatalf("dispatched-action drain = (%d, %t, %d)", moved, more, status)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestRunnableTransferGateContentionFailsImmediately(t *testing.T) {
	source, target := new(P), new(P)
	task := newYieldingTestG(t, "gate-contention")
	if !Enqueue(source, task.g) {
		t.Fatal("enqueue gate-contention transfer")
	}
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind gate-contention mailbox")
	}
	if !preemptCompareAndSwap(&mailbox.gate, runnableTransferGateIdle, runnableTransferGateHeld) {
		t.Fatal("hold transfer gate")
	}
	if id, ok := PublishPNeutralRunnable(&mailbox, source, task.g); ok || id != (RunnableTransferID{}) ||
		preemptLoad(&mailbox.gate) != runnableTransferGateHeld || source.readyHead != task.g ||
		!task.g.queued || task.g.transferState != runnableTransferGIdle {
		t.Fatalf("contended publish mutated state: id=%+v ok=%t gate=%d source=%p queued=%t state=%d",
			id, ok, preemptLoad(&mailbox.gate), source.readyHead, task.g.queued, task.g.transferState)
	}
	releaseRunnableTransferGate(&mailbox)
	id, ok := PublishPNeutralRunnable(&mailbox, source, task.g)
	if !ok {
		t.Fatal("publish after releasing gate")
	}
	if !preemptCompareAndSwap(&mailbox.gate, runnableTransferGateIdle, runnableTransferGateHeld) {
		t.Fatal("hold transfer gate for consumer")
	}
	if ImportPNeutralRunnable(&mailbox, target, id) {
		t.Fatal("contended import succeeded")
	}
	if moved, more, drainOK := DrainPNeutralRunnables(&mailbox, target, 1); drainOK || moved != 0 || more ||
		preemptLoad(&mailbox.gate) != runnableTransferGateHeld || mailbox.count != 1 ||
		task.g.transferState != runnableTransferGPublished || mailbox.slots[id.Slot-1].g != task.g {
		t.Fatalf("contended consumer mutated state: drain=(%d,%t,%t) gate=%d count=%d state=%d slot=%p",
			moved, more, drainOK, preemptLoad(&mailbox.gate), mailbox.count,
			task.g.transferState, mailbox.slots[id.Slot-1].g)
	}
	releaseRunnableTransferGate(&mailbox)
	if !ImportPNeutralRunnable(&mailbox, target, id) {
		t.Fatal("import after releasing gate")
	}
}
