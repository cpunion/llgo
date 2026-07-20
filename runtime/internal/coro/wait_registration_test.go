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

func registerTestWait(t *testing.T, table *WaitRegistrationTable, p *P) (*WaitToken, WaitTicket, WaitRegistrationHandle) {
	t.Helper()
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm registered wait")
	}
	handle, ok := table.Register(p, token, ticket)
	if !ok || handle.Slot == 0 || handle.Generation == 0 {
		t.Fatalf("register wait = (%+v, %t)", handle, ok)
	}
	return token, ticket, handle
}

func consumeRegisteredOutcome(t *testing.T, token *WaitToken, ticket WaitTicket, want WaitOutcome) {
	t.Helper()
	if !claimWait(token, ticket) {
		t.Fatal("claim registered outcome")
	}
	outcome, ok := consumeWait(token, ticket)
	if !ok || outcome != want {
		t.Fatalf("consume registered outcome = (%d, %t), want %d", outcome, ok, want)
	}
	if stable, ok := WaitOutcomeOf(token, ticket); !ok || stable != want {
		t.Fatalf("stable registered outcome = (%d, %t), want %d", stable, ok, want)
	}
}

func TestWaitRegistrationPostDrainCloseRetire(t *testing.T) {
	table := new(WaitRegistrationTable)
	p := new(P)
	if !table.CanRelease() {
		t.Fatal("zero registration table is not releasable")
	}
	token, ticket, handle := registerTestWait(t, table, p)
	if table.CanRelease() {
		t.Fatal("live registration table reported releasable")
	}
	if result := table.Post(handle); result != WaitRegistrationPosted || !table.Pending() {
		t.Fatalf("post = %d, pending=%t", result, table.Pending())
	}
	if outcome, ok := WaitOutcomeOf(token, ticket); ok || outcome != WaitOutcomeInvalid {
		t.Fatal("producer ingress called CompleteWait directly")
	}
	if result := table.Post(handle); result != WaitRegistrationPostDuplicate {
		t.Fatalf("duplicate post = %d", result)
	}
	if drained, ok := table.Drain(); !ok || drained != 1 {
		t.Fatalf("drain = (%d, %t)", drained, ok)
	}
	if table.Pending() {
		t.Fatal("drain retained doorbell without a concurrent post")
	}
	consumeRegisteredOutcome(t, token, ticket, WaitOutcomeCompleted)
	if table.Retire(handle) {
		t.Fatal("registration retired before physical close")
	}
	if result := table.BeginClose(handle); result != WaitRegistrationCloseStarted {
		t.Fatalf("begin delivered close = %d", result)
	}
	if result, ok := table.ConfirmQuiesced(handle); !ok || result != WaitCancelCompletionWon {
		t.Fatalf("confirm delivered quiescence = (%d, %t)", result, ok)
	}
	if !table.Retire(handle) {
		t.Fatal("retire consumed, quiesced completion")
	}
	if !table.CanRelease() {
		t.Fatal("retired registration table retained ownership")
	}
	if result := table.Post(handle); result != WaitRegistrationPostClosed {
		t.Fatalf("same-generation post after retire = %d", result)
	}
}

func TestWaitRegistrationCancellationRequiresQuiescenceAndConsumption(t *testing.T) {
	table := new(WaitRegistrationTable)
	p := new(P)
	token, ticket, handle := registerTestWait(t, table, p)
	slot, _ := registrationSlot(table, handle)
	if acquireProducerSourceGeneration(&slot.producerSourceSlot, handle.Generation) != producerSourceAcquired {
		t.Fatal("model admitted callback")
	}
	if result := table.BeginClose(handle); result != WaitRegistrationCloseStarted {
		t.Fatalf("begin cancellation close = %d", result)
	}
	if table.Retire(handle) {
		t.Fatal("registration retired before backend quiescence")
	}
	if outcome, ok := WaitOutcomeOf(token, ticket); ok || outcome != WaitOutcomeInvalid {
		t.Fatal("BeginClose published cancellation before backend acknowledgement")
	}
	if result, ok := table.ConfirmQuiesced(handle); ok || result != WaitCancelInvalid {
		t.Fatalf("quiesced with inflight callback = (%d, %t)", result, ok)
	}
	if !producerAdmissionReleaseChecked(&slot.inflight) {
		t.Fatal("release admitted callback")
	}
	if result, ok := table.ConfirmQuiesced(handle); !ok || result != WaitCancelWon {
		t.Fatalf("confirm cancellation quiescence = (%d, %t)", result, ok)
	}
	if result := table.Post(handle); result != WaitRegistrationPostClosed {
		t.Fatalf("late callback after close = %d", result)
	}
	if table.Retire(handle) {
		t.Fatal("registration retired before scheduler consumption")
	}
	consumeRegisteredOutcome(t, token, ticket, WaitOutcomeCanceled)
	if !table.Retire(handle) {
		t.Fatal("retire consumed, quiesced cancellation")
	}
}

func TestWaitRegistrationAdmittedOldProducerPinsSlotGeneration(t *testing.T) {
	table := new(WaitRegistrationTable)
	p := new(P)
	token, ticket, old := registerTestWait(t, table, p)
	slot, _ := registrationSlot(table, old)
	// Model an old callback immediately after it acquired a producer lease and
	// validated the old generation, but before it attempted Active->Posting.
	if acquireProducerSourceGeneration(&slot.producerSourceSlot, old.Generation) != producerSourceAcquired ||
		preemptLoad(&slot.generation) != old.Generation {
		t.Fatal("admit old producer")
	}
	if table.BeginClose(old) != WaitRegistrationCloseStarted {
		t.Fatal("close registration with admitted producer")
	}
	if result, ok := table.ConfirmQuiesced(old); ok || result != WaitCancelInvalid || table.Retire(old) {
		t.Fatal("admitted producer did not pin closing generation")
	}
	if state := waitRegistrationState(preemptLoad(&slot.state)); state != waitRegistrationClosingCancel {
		t.Fatalf("closing state = %d", state)
	}
	if !producerAdmissionReleaseChecked(&slot.inflight) {
		t.Fatal("release old producer")
	}
	if result, ok := table.ConfirmQuiesced(old); !ok || result != WaitCancelWon {
		t.Fatalf("confirm pinned generation = (%d, %t)", result, ok)
	}
	consumeRegisteredOutcome(t, token, ticket, WaitOutcomeCanceled)
	if !table.Retire(old) {
		t.Fatal("retire old generation after producer release")
	}
	newToken, newTicket, next := registerTestWait(t, table, p)
	if next.Slot != old.Slot || next.Generation == old.Generation {
		t.Fatalf("next generation = %+v, old=%+v", next, old)
	}
	if result := table.Post(old); result != WaitRegistrationPostStale {
		t.Fatalf("released old producer affected new generation: %d", result)
	}
	if table.BeginClose(next) != WaitRegistrationCloseStarted {
		t.Fatal("close next generation")
	}
	if _, ok := table.ConfirmQuiesced(next); !ok {
		t.Fatal("quiesce next generation")
	}
	consumeRegisteredOutcome(t, newToken, newTicket, WaitOutcomeCanceled)
	if !table.Retire(next) {
		t.Fatal("retire next generation")
	}
}

func TestWaitRegistrationQuiescingPinsOwnerAfterTokenConsumption(t *testing.T) {
	table := new(WaitRegistrationTable)
	p := new(P)
	token, ticket, handle := registerTestWait(t, table, p)
	if table.BeginClose(handle) != WaitRegistrationCloseStarted {
		t.Fatal("begin quiescing handoff")
	}
	slot, _ := registrationSlot(table, handle)
	if !preemptCompareAndSwap(&slot.state, uint32(waitRegistrationClosingCancel), uint32(waitRegistrationQuiescing)) {
		t.Fatal("enter quiescing handoff")
	}
	cachedP := slot.p
	if publishWaitCancellation(slot.token, slot.ticket) != WaitCancelWon {
		t.Fatal("publish handoff cancellation")
	}
	consumeRegisteredOutcome(t, token, ticket, WaitOutcomeCanceled)
	if table.Retire(handle) {
		t.Fatal("retired owner while ConfirmQuiesced still held cached P")
	}
	RequestSchedule(cachedP)
	preemptStore(&slot.state, uint32(waitRegistrationQuiescedCanceled))
	if !table.Retire(handle) {
		t.Fatal("retire owner after quiescing handoff")
	}
}

func TestWaitRegistrationConcurrentPostsHaveOneWinner(t *testing.T) {
	const workers = 32
	table := new(WaitRegistrationTable)
	p := new(P)
	token, ticket, handle := registerTestWait(t, table, p)
	start := make(chan struct{})
	results := make(chan WaitRegistrationPostResult, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer wg.Done()
			<-start
			results <- table.Post(handle)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	posted, duplicate := 0, 0
	for result := range results {
		switch result {
		case WaitRegistrationPosted:
			posted++
		case WaitRegistrationPostDuplicate:
			duplicate++
		default:
			t.Fatalf("concurrent post result = %d", result)
		}
	}
	if posted != 1 || duplicate != workers-1 {
		t.Fatalf("post winners=%d duplicates=%d", posted, duplicate)
	}
	if drained, ok := table.Drain(); !ok || drained != 1 {
		t.Fatalf("drain concurrent winner = (%d, %t)", drained, ok)
	}
	consumeRegisteredOutcome(t, token, ticket, WaitOutcomeCompleted)
	if table.BeginClose(handle) != WaitRegistrationCloseStarted {
		t.Fatal("close concurrent winner")
	}
	if _, ok := table.ConfirmQuiesced(handle); !ok || !table.Retire(handle) {
		t.Fatal("quiesce and retire concurrent winner")
	}
}

func TestWaitRegistrationPostCloseRace(t *testing.T) {
	const iterations = 500
	for iteration := 0; iteration < iterations; iteration++ {
		table := new(WaitRegistrationTable)
		p := new(P)
		token, ticket, handle := registerTestWait(t, table, p)
		start := make(chan struct{})
		posted := make(chan WaitRegistrationPostResult, 1)
		closed := make(chan WaitRegistrationCloseResult, 1)
		go func() {
			<-start
			posted <- table.Post(handle)
		}()
		go func() {
			<-start
			closed <- table.BeginClose(handle)
		}()
		close(start)
		postResult, closeResult := <-posted, <-closed
		switch {
		case postResult == WaitRegistrationPosted && closeResult == WaitRegistrationCompletionPending:
			if drained, ok := table.Drain(); !ok || drained != 1 {
				t.Fatalf("iteration %d: drain post winner = (%d, %t)", iteration, drained, ok)
			}
			consumeRegisteredOutcome(t, token, ticket, WaitOutcomeCompleted)
			if table.BeginClose(handle) != WaitRegistrationCloseStarted {
				t.Fatalf("iteration %d: close delivered winner", iteration)
			}
			if result, ok := table.ConfirmQuiesced(handle); !ok || result != WaitCancelCompletionWon {
				t.Fatalf("iteration %d: confirm completion = (%d, %t)", iteration, result, ok)
			}
		case postResult == WaitRegistrationPostClosed && closeResult == WaitRegistrationCloseStarted:
			if result, ok := table.ConfirmQuiesced(handle); !ok || result != WaitCancelWon {
				t.Fatalf("iteration %d: confirm cancellation = (%d, %t)", iteration, result, ok)
			}
			consumeRegisteredOutcome(t, token, ticket, WaitOutcomeCanceled)
		default:
			t.Fatalf("iteration %d: post=%d close=%d", iteration, postResult, closeResult)
		}
		if !table.Retire(handle) {
			t.Fatalf("iteration %d: retire race winner", iteration)
		}
	}
}

func TestWaitRegistrationPostingBlocksCloseUntilDrain(t *testing.T) {
	table := new(WaitRegistrationTable)
	p := new(P)
	token, ticket, handle := registerTestWait(t, table, p)
	slot, _ := registrationSlot(table, handle)
	if !preemptCompareAndSwap(&slot.state, uint32(waitRegistrationActive), uint32(waitRegistrationPosting)) {
		t.Fatal("reserve posting state")
	}
	if result := table.BeginClose(handle); result != WaitRegistrationCompletionPending {
		t.Fatalf("close while posting = %d", result)
	}
	if result, ok := table.ConfirmQuiesced(handle); ok || result != WaitCancelInvalid {
		t.Fatalf("quiesce while posting = (%d, %t)", result, ok)
	}
	preemptStore(&slot.state, uint32(waitRegistrationPosted))
	preemptStore(&table.pending, 1)
	if drained, ok := table.Drain(); !ok || drained != 1 {
		t.Fatalf("drain reserved post = (%d, %t)", drained, ok)
	}
	consumeRegisteredOutcome(t, token, ticket, WaitOutcomeCompleted)
	if table.BeginClose(handle) != WaitRegistrationCloseStarted {
		t.Fatal("close after posting drain")
	}
	if _, ok := table.ConfirmQuiesced(handle); !ok || !table.Retire(handle) {
		t.Fatal("retire drained posting state")
	}
}

func TestWaitRegistrationCapacityAndStaleGeneration(t *testing.T) {
	table := new(WaitRegistrationTable)
	p := new(P)
	tokens := make([]*WaitToken, WaitRegistrationCapacity)
	tickets := make([]WaitTicket, WaitRegistrationCapacity)
	handles := make([]WaitRegistrationHandle, WaitRegistrationCapacity)
	for index := range tokens {
		tokens[index], tickets[index], handles[index] = registerTestWait(t, table, p)
	}
	extra := new(WaitToken)
	extraTicket, ok := ArmWait(extra)
	if !ok {
		t.Fatal("arm capacity overflow token")
	}
	if handle, ok := table.Register(p, extra, extraTicket); ok || handle != (WaitRegistrationHandle{}) {
		t.Fatalf("capacity overflow = (%+v, %t)", handle, ok)
	}
	old := handles[0]
	for index := range handles {
		if table.BeginClose(handles[index]) != WaitRegistrationCloseStarted {
			t.Fatalf("begin close slot %d", index)
		}
		if result, ok := table.ConfirmQuiesced(handles[index]); !ok || result != WaitCancelWon {
			t.Fatalf("confirm slot %d = (%d, %t)", index, result, ok)
		}
		consumeRegisteredOutcome(t, tokens[index], tickets[index], WaitOutcomeCanceled)
		if !table.Retire(handles[index]) {
			t.Fatalf("retire slot %d", index)
		}
	}
	newToken, newTicket, next := registerTestWait(t, table, p)
	if next.Slot != old.Slot || next.Generation == old.Generation {
		t.Fatalf("reused handle = %+v, old=%+v", next, old)
	}
	if result := table.Post(old); result != WaitRegistrationPostStale {
		t.Fatalf("old generation post = %d", result)
	}
	if table.BeginClose(next) != WaitRegistrationCloseStarted {
		t.Fatal("close reused slot")
	}
	if _, ok := table.ConfirmQuiesced(next); !ok {
		t.Fatal("quiesce reused slot")
	}
	consumeRegisteredOutcome(t, newToken, newTicket, WaitOutcomeCanceled)
	if !table.Retire(next) {
		t.Fatal("retire reused slot")
	}
}

func TestWaitRegistrationPagedCatalogAdmitsAndCompletesOneThousand(t *testing.T) {
	const count = 1000
	table := new(WaitRegistrationTable)
	var pages [15]WaitRegistrationPage
	if !ConfigureWaitRegistrationPages(table, pages[:1]) ||
		WaitRegistrationConfiguredCapacity(table) != 2*WaitRegistrationPageCapacity ||
		!ConfigureWaitRegistrationPages(table, pages[:]) ||
		WaitRegistrationConfiguredCapacity(table) != 16*WaitRegistrationPageCapacity {
		t.Fatal("configure paged wait catalog")
	}
	p := new(P)
	tokens := make([]WaitToken, count)
	tickets := make([]WaitTicket, count)
	handles := make([]WaitRegistrationHandle, count)
	for index := range tokens {
		ticket, handle, result := PrepareWaitRegistration(p, table, &tokens[index])
		if result != WaitRegistrationPrepared || !claimWait(&tokens[index], ticket) {
			t.Fatalf("prepare paged wait %d = (%d, %+v, %d)", index, ticket, handle, result)
		}
		tickets[index], handles[index] = ticket, handle
	}
	if handles[WaitRegistrationPageCapacity].Slot != WaitRegistrationPageCapacity+1 || handles[count-1].Slot != count {
		t.Fatalf("paged wait handles did not remain linear: page2=%+v last=%+v", handles[WaitRegistrationPageCapacity], handles[count-1])
	}
	var otherPages [16]WaitRegistrationPage
	if ConfigureWaitRegistrationPages(table, otherPages[:]) || ConfigureWaitRegistrationPages(table, pages[:14]) {
		t.Fatal("live paged wait catalog allowed replacement or shrink")
	}
	for index := range handles {
		if result := table.Post(handles[index]); result != WaitRegistrationPosted {
			t.Fatalf("post paged wait %d = %d", index, result)
		}
	}
	if drained, ok := table.Drain(); !ok || drained != count || table.Pending() {
		t.Fatalf("drain paged waits = (%d, %t), pending=%t", drained, ok, table.Pending())
	}
	for index := range tokens {
		if outcome, ok := consumeWait(&tokens[index], tickets[index]); !ok || outcome != WaitOutcomeCompleted ||
			!table.RetireCompletedWait(handles[index], &tokens[index], tickets[index]) {
			t.Fatalf("retire paged wait %d", index)
		}
	}
	if !table.CanRelease() || !ConfigureWaitRegistrationPages(table, pages[:]) {
		t.Fatal("paged wait catalog did not return to reusable configured state")
	}
}

func TestWaitRegistrationCancellationBeforeParkResumesOnce(t *testing.T) {
	table := new(WaitRegistrationTable)
	p := new(P)
	task := newYieldingTestG(t, "registered-cancel")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue registered cancellation task")
	}
	g, ok := NextRunnable(p)
	if !ok || g != task.g {
		t.Fatal("dequeue registered cancellation task")
	}
	action := beginWaitTestResume(t, p, task)
	token, ticket, handle := registerTestWait(t, table, p)
	if table.BeginClose(handle) != WaitRegistrationCloseStarted {
		t.Fatal("close before park")
	}
	if result, ok := table.ConfirmQuiesced(handle); !ok || result != WaitCancelWon {
		t.Fatalf("quiesce before park = (%d, %t)", result, ok)
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("prepare safely canceled park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatal("commit safely canceled park")
	}
	if count, ok := PollReady(p); !ok || count != 1 {
		t.Fatalf("promote safely canceled park = (%d, %t)", count, ok)
	}
	if outcome, ok := WaitOutcomeOf(token, ticket); !ok || outcome != WaitOutcomeCanceled {
		t.Fatalf("resumed outcome = (%d, %t)", outcome, ok)
	}
	if !table.Retire(handle) {
		t.Fatal("retire safely canceled park")
	}
	g, ok = NextRunnable(p)
	if !ok || g != task.g {
		t.Fatal("safely canceled G not runnable")
	}
	finishWaitTestTask(t, p, task, beginWaitTestResume(t, p, task))
	runtime.KeepAlive(task.frame.memory)
}

func TestPrepareWaitRegistrationRollsBackArmFailure(t *testing.T) {
	var table WaitRegistrationTable
	var token WaitToken
	if ticket, handle, result := PrepareWaitRegistration(nil, &table, &token); result != WaitRegistrationPrepareRejected || ticket != 0 || handle != (WaitRegistrationHandle{}) {
		t.Fatalf("invalid prepare = (%d, %+v, %d)", ticket, handle, result)
	}
	if ticket, ok := ArmWait(&token); !ok || ticket != 2 {
		t.Fatalf("arm after failed registration = (%d, %t), want fresh generation 2", ticket, ok)
	}
}

func TestPreparedWaitRollbackAndCompletedRetire(t *testing.T) {
	p := new(P)
	var table WaitRegistrationTable
	if !bindRegistrationTable(&table, p) {
		t.Fatal("bind prepared-wait table")
	}
	var token WaitToken
	ticket, handle, result := PrepareWaitRegistration(p, &table, &token)
	if result != WaitRegistrationPrepared || ticket != 1 || handle == (WaitRegistrationHandle{}) {
		t.Fatalf("prepare rollback wait = (%d, %+v, %d)", ticket, handle, result)
	}
	if !table.RollbackPreparedWait(handle, &token, ticket) {
		t.Fatal("rollback prepared wait")
	}
	if outcome, ok := WaitOutcomeOf(&token, ticket); !ok || outcome != WaitOutcomeCanceled {
		t.Fatalf("rolled-back outcome = (%d, %t)", outcome, ok)
	}

	ticket, handle, result = PrepareWaitRegistration(p, &table, &token)
	if result != WaitRegistrationPrepared || ticket != 2 || handle == (WaitRegistrationHandle{}) {
		t.Fatalf("prepare completed wait = (%d, %+v, %d)", ticket, handle, result)
	}
	if result := table.Post(handle); result != WaitRegistrationPosted {
		t.Fatalf("post completed wait = %d", result)
	}
	if drained, ok := table.drainFor(p); !ok || drained != 1 {
		t.Fatalf("drain completed wait = (%d, %t)", drained, ok)
	}
	if !claimWait(&token, ticket) {
		t.Fatal("claim completed wait")
	}
	if outcome, ok := consumeWait(&token, ticket); !ok || outcome != WaitOutcomeCompleted {
		t.Fatalf("consume completed wait = (%d, %t)", outcome, ok)
	}
	if !table.RetireCompletedWait(handle, &token, ticket) {
		t.Fatal("retire completed wait")
	}
	if !registrationTableEmpty(&table, p) || !unbindRegistrationTable(&table, p) || !table.CanRelease() {
		t.Fatal("prepared-wait table retained state")
	}
}

func TestPrepareWaitRegistrationFullTableRollsBackTicket(t *testing.T) {
	p := new(P)
	var table WaitRegistrationTable
	if !bindRegistrationTable(&table, p) {
		t.Fatal("bind full prepared-wait table")
	}
	tokens := make([]WaitToken, WaitRegistrationCapacity)
	tickets := make([]WaitTicket, WaitRegistrationCapacity)
	handles := make([]WaitRegistrationHandle, WaitRegistrationCapacity)
	for index := range tokens {
		var result WaitRegistrationPrepareResult
		tickets[index], handles[index], result = PrepareWaitRegistration(p, &table, &tokens[index])
		if result != WaitRegistrationPrepared {
			t.Fatalf("fill prepared wait %d = %d", index, result)
		}
	}
	var extra WaitToken
	if ticket, handle, result := PrepareWaitRegistration(p, &table, &extra); result != WaitRegistrationPrepareRejected ||
		ticket != 0 || handle != (WaitRegistrationHandle{}) {
		t.Fatalf("full-table prepare = (%d, %+v, %d)", ticket, handle, result)
	}
	if ticket, ok := ArmWait(&extra); !ok || ticket != 2 || !rollbackArmedWait(&extra, ticket) {
		t.Fatalf("arm after full-table rejection = (%d, %t)", ticket, ok)
	}
	for index := range tokens {
		if !table.RollbackPreparedWait(handles[index], &tokens[index], tickets[index]) {
			t.Fatalf("rollback filled prepared wait %d", index)
		}
	}
	if !registrationTableEmpty(&table, p) || !unbindRegistrationTable(&table, p) || !table.CanRelease() {
		t.Fatal("full prepared-wait table retained state")
	}
}

func TestWaitRegistrationAtomicPrefixAlignment(t *testing.T) {
	if unsafe.Sizeof(WaitRegistrationHandle{}) != 8 || unsafe.Alignof(WaitRegistrationHandle{}) != 4 {
		t.Fatalf("producer handle layout = size %d align %d", unsafe.Sizeof(WaitRegistrationHandle{}), unsafe.Alignof(WaitRegistrationHandle{}))
	}
	if unsafe.Offsetof(WaitRegistrationTable{}.pending)%4 != 0 ||
		unsafe.Offsetof(WaitRegistrationTable{}.slots)%4 != 0 ||
		unsafe.Offsetof(waitRegistrationSlot{}.state)%4 != 0 ||
		unsafe.Offsetof(waitRegistrationSlot{}.generation)%4 != 0 ||
		unsafe.Offsetof(waitRegistrationSlot{}.inflight)%4 != 0 {
		t.Fatal("wait registration atomic prefix is not uint32 aligned")
	}
}
