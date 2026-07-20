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
	"testing"
	"unsafe"
)

func prepareTestTimer(t *testing.T, table *TimerRegistrationTable, p *P, deadline int64) (*WaitToken, WaitTicket, TimerRegistrationHandle) {
	t.Helper()
	token := new(WaitToken)
	ticket, handle, result := PrepareTimerRegistration(p, table, token, deadline)
	if result != TimerRegistrationPrepared || ticket == 0 || handle.Slot == 0 || handle.Generation == 0 {
		t.Fatalf("prepare timer = (%d, %+v, %d)", ticket, handle, result)
	}
	return token, ticket, handle
}

func consumeTimerOutcome(t *testing.T, token *WaitToken, ticket WaitTicket, want WaitOutcome) {
	t.Helper()
	if !claimWait(token, ticket) {
		t.Fatal("claim timer wait")
	}
	outcome, ok := consumeWait(token, ticket)
	if !ok || outcome != want {
		t.Fatalf("consume timer outcome = (%d, %t), want %d", outcome, ok, want)
	}
}

func TestTimerRegistrationAbsoluteDeadlineCompletionAndRetire(t *testing.T) {
	table := new(TimerRegistrationTable)
	p := new(P)
	if !table.CanRelease() {
		t.Fatal("zero timer table is not releasable")
	}
	token, ticket, handle := prepareTestTimer(t, table, p, 100)
	if table.CanRelease() {
		t.Fatal("live timer table reported releasable")
	}
	if deadline, has, ok := table.NextDeadline(); !ok || !has || deadline != 100 {
		t.Fatalf("next deadline = (%d, %t, %t)", deadline, has, ok)
	}
	if completed, deadline, has, ok := table.DrainDue(99); !ok || completed != 0 || !has || deadline != 100 {
		t.Fatalf("early scan = (%d, %d, %t, %t)", completed, deadline, has, ok)
	}
	if outcome, ok := WaitOutcomeOf(token, ticket); ok || outcome != WaitOutcomeInvalid {
		t.Fatal("timer fired before its absolute deadline")
	}
	if !claimWait(token, ticket) {
		t.Fatal("park timer waiter")
	}
	if completed, deadline, has, ok := table.DrainDue(100); !ok || completed != 1 || has || deadline != 0 {
		t.Fatalf("due scan = (%d, %d, %t, %t)", completed, deadline, has, ok)
	}
	if table.Retire(handle) {
		t.Fatal("timer retired before scheduler consumption")
	}
	if outcome, ok := consumeWait(token, ticket); !ok || outcome != WaitOutcomeCompleted {
		t.Fatalf("consume due timer = (%d, %t)", outcome, ok)
	}
	if !table.RetireCompletedTimer(handle, token, ticket) || !table.CanRelease() {
		t.Fatal("consumed timer did not retire cleanly")
	}
}

func TestTimerRegistrationNextDeadlineOrdersAndDrainsAllDue(t *testing.T) {
	table := new(TimerRegistrationTable)
	p := new(P)
	tokens := make([]*WaitToken, 0, 4)
	tickets := make([]WaitTicket, 0, 4)
	handles := make([]TimerRegistrationHandle, 0, 4)
	for _, deadline := range []int64{90, 30, 30, 120} {
		token, ticket, handle := prepareTestTimer(t, table, p, deadline)
		if !claimWait(token, ticket) {
			t.Fatal("claim ordered timer")
		}
		tokens = append(tokens, token)
		tickets = append(tickets, ticket)
		handles = append(handles, handle)
	}
	if deadline, has, ok := table.NextDeadline(); !ok || !has || deadline != 30 {
		t.Fatalf("minimum deadline = (%d, %t, %t)", deadline, has, ok)
	}
	if completed, deadline, has, ok := table.DrainDue(30); !ok || completed != 2 || !has || deadline != 90 {
		t.Fatalf("first ordered scan = (%d, %d, %t, %t)", completed, deadline, has, ok)
	}
	if completed, deadline, has, ok := table.DrainDue(200); !ok || completed != 2 || has || deadline != 0 {
		t.Fatalf("final ordered scan = (%d, %d, %t, %t)", completed, deadline, has, ok)
	}
	for index := range tokens {
		if outcome, ok := consumeWait(tokens[index], tickets[index]); !ok || outcome != WaitOutcomeCompleted ||
			!table.RetireCompletedTimer(handles[index], tokens[index], tickets[index]) {
			t.Fatalf("retire ordered timer %d", index)
		}
	}
	if !table.CanRelease() {
		t.Fatal("ordered timers retained table")
	}
}

func TestTimerRegistrationCancelAndCompletionWinner(t *testing.T) {
	table := new(TimerRegistrationTable)
	p := new(P)

	canceledToken, canceledTicket, canceled := prepareTestTimer(t, table, p, 10)
	if !claimWait(canceledToken, canceledTicket) {
		t.Fatal("claim canceled timer")
	}
	if result := table.Cancel(canceled); result != WaitCancelWon {
		t.Fatalf("cancel active timer = %d", result)
	}
	if result := table.Cancel(canceled); result != WaitCancelAlreadyCanceled {
		t.Fatalf("duplicate timer cancel = %d", result)
	}
	if completed, _, has, ok := table.DrainDue(100); !ok || completed != 0 || has {
		t.Fatalf("canceled timer fired = (%d, %t, %t)", completed, has, ok)
	}
	if table.Retire(canceled) {
		t.Fatal("canceled timer retired before consumption")
	}
	if outcome, ok := consumeWait(canceledToken, canceledTicket); !ok || outcome != WaitOutcomeCanceled ||
		!table.RetireCanceledTimer(canceled, canceledToken, canceledTicket) {
		t.Fatal("consume and retire canceled timer")
	}

	completedToken, completedTicket, completed := prepareTestTimer(t, table, p, 20)
	if !claimWait(completedToken, completedTicket) {
		t.Fatal("claim completed timer")
	}
	if count, _, has, ok := table.DrainDue(20); !ok || count != 1 || has {
		t.Fatalf("complete timer before cancel = (%d, %t, %t)", count, has, ok)
	}
	if result := table.Cancel(completed); result != WaitCancelCompletionWon {
		t.Fatalf("cancel after due completion = %d", result)
	}
	if outcome, ok := consumeWait(completedToken, completedTicket); !ok || outcome != WaitOutcomeCompleted ||
		!table.RetireCompletedTimer(completed, completedToken, completedTicket) {
		t.Fatal("completion winner did not retire")
	}
	if !table.CanRelease() {
		t.Fatal("winner test retained timer table")
	}
}

func TestTimerRegistrationRollbackPreparedIsTransactional(t *testing.T) {
	table := new(TimerRegistrationTable)
	p := new(P)
	token, ticket, handle := prepareTestTimer(t, table, p, 50)
	if !table.RollbackPreparedTimer(handle, token, ticket) || !table.CanRelease() {
		t.Fatal("rollback prepared timer")
	}
	if outcome, ok := WaitOutcomeOf(token, ticket); !ok || outcome != WaitOutcomeCanceled {
		t.Fatalf("rolled-back outcome = (%d, %t)", outcome, ok)
	}
	next, ok := ArmWait(token)
	if !ok || next == ticket {
		t.Fatalf("rearm rolled-back token = (%d, %t), old=%d", next, ok, ticket)
	}
	if !rollbackArmedWait(token, next) {
		t.Fatal("cleanup rearmed token")
	}
}

func TestTimerRegistrationCapacityFailureRollsBackTicket(t *testing.T) {
	table := new(TimerRegistrationTable)
	p := new(P)
	tokens := make([]*WaitToken, TimerRegistrationCapacity)
	tickets := make([]WaitTicket, TimerRegistrationCapacity)
	handles := make([]TimerRegistrationHandle, TimerRegistrationCapacity)
	for index := range tokens {
		tokens[index], tickets[index], handles[index] = prepareTestTimer(t, table, p, int64(index+1))
	}
	extra := new(WaitToken)
	if ticket, handle, result := PrepareTimerRegistration(p, table, extra, 1000); result != TimerRegistrationPrepareRejected ||
		ticket != 0 || handle != (TimerRegistrationHandle{}) {
		t.Fatalf("full table prepare = (%d, %+v, %d)", ticket, handle, result)
	}
	rolledBackGeneration := waitGeneration(preemptLoad(&extra.word))
	if waitWordState(preemptLoad(&extra.word)) != waitConsumedCanceled || rolledBackGeneration == 0 {
		t.Fatal("capacity failure did not consume unpublished ticket")
	}
	if ticket, ok := ArmWait(extra); !ok || uint32(ticket) != rolledBackGeneration+1 {
		t.Fatalf("capacity-rejected token did not advance generation = (%d, %t)", ticket, ok)
	} else if !rollbackArmedWait(extra, ticket) {
		t.Fatal("cleanup capacity-rejected token")
	}
	for index := range tokens {
		if result := table.Cancel(handles[index]); result != WaitCancelWon ||
			!consumeUnclaimedCanceledWait(tokens[index], tickets[index]) ||
			!table.RetireCanceledTimer(handles[index], tokens[index], tickets[index]) {
			t.Fatalf("retire capacity timer %d", index)
		}
	}
	if !table.CanRelease() {
		t.Fatal("capacity cleanup retained table")
	}
}

func TestTimerRegistrationPagedCatalogAdmitsAndDrainsNativeFourThousandNinetySix(t *testing.T) {
	const count = 64 * TimerRegistrationPageCapacity
	table := new(TimerRegistrationTable)
	var pages [63]TimerRegistrationPage
	if !ConfigureTimerRegistrationPages(table, pages[:1]) ||
		TimerRegistrationConfiguredCapacity(table) != 2*TimerRegistrationPageCapacity ||
		!ConfigureTimerRegistrationPages(table, pages[:]) ||
		TimerRegistrationConfiguredCapacity(table) != count {
		t.Fatal("configure paged timer catalog")
	}
	// Rebinding a singleton target with the same pages must not invalidate
	// generations or require allocation.
	if !ConfigureTimerRegistrationPages(table, pages[:]) {
		t.Fatal("repeat paged timer configuration")
	}
	p := new(P)
	tokens := make([]WaitToken, count)
	tickets := make([]WaitTicket, count)
	handles := make([]TimerRegistrationHandle, count)
	for index := range tokens {
		ticket, handle, result := PrepareTimerRegistration(p, table, &tokens[index], int64(index+1))
		if result != TimerRegistrationPrepared || !claimWait(&tokens[index], ticket) {
			t.Fatalf("prepare paged timer %d = (%d, %+v, %d)", index, ticket, handle, result)
		}
		tickets[index], handles[index] = ticket, handle
	}
	if handles[count-1].Slot != count || handles[TimerRegistrationPageCapacity].Slot != TimerRegistrationPageCapacity+1 {
		t.Fatalf("paged timer handles did not remain linear: page2=%+v last=%+v", handles[TimerRegistrationPageCapacity], handles[count-1])
	}
	var overflow WaitToken
	if ticket, handle, result := PrepareTimerRegistration(p, table, &overflow, count+1); result != TimerRegistrationPrepareRejected ||
		ticket != 0 || handle != (TimerRegistrationHandle{}) {
		t.Fatalf("full native timer catalog overflow = (%d, %+v, %d)", ticket, handle, result)
	}
	if completed, _, hasDeadline, ok := table.DrainDue(count); !ok || completed != count || hasDeadline {
		t.Fatalf("drain paged timers = (%d, %t, %t)", completed, hasDeadline, ok)
	}
	for index := range tokens {
		if outcome, ok := consumeWait(&tokens[index], tickets[index]); !ok || outcome != WaitOutcomeCompleted ||
			!table.RetireCompletedTimer(handles[index], &tokens[index], tickets[index]) {
			t.Fatalf("retire paged timer %d", index)
		}
	}
	if !table.CanRelease() {
		t.Fatal("paged timer catalog retained state")
	}
}

func TestTimerRegistrationGenerationRejectsABAHandle(t *testing.T) {
	table := new(TimerRegistrationTable)
	p := new(P)
	oldToken, oldTicket, old := prepareTestTimer(t, table, p, 1)
	if !claimWait(oldToken, oldTicket) {
		t.Fatal("claim old timer")
	}
	if completed, _, _, ok := table.DrainDue(1); !ok || completed != 1 {
		t.Fatal("complete old timer")
	}
	if outcome, ok := consumeWait(oldToken, oldTicket); !ok || outcome != WaitOutcomeCompleted || !table.Retire(old) {
		t.Fatal("retire old timer")
	}

	newToken, newTicket, next := prepareTestTimer(t, table, p, 2)
	if next.Slot != old.Slot || next.Generation == old.Generation {
		t.Fatalf("timer generation was not advanced: old=%+v next=%+v", old, next)
	}
	if result := table.Cancel(old); result != WaitCancelInvalid || table.Retire(old) {
		t.Fatal("stale timer handle affected next generation")
	}
	if outcome, ok := WaitOutcomeOf(newToken, newTicket); ok || outcome != WaitOutcomeInvalid {
		t.Fatal("stale handle changed new timer outcome")
	}
	if result := table.Cancel(next); result != WaitCancelWon || !consumeUnclaimedCanceledWait(newToken, newTicket) ||
		!table.RetireCanceledTimer(next, newToken, newTicket) {
		t.Fatal("cleanup next timer generation")
	}
}

func TestTimerRegistrationOwnerBindingAndTerminalEmpty(t *testing.T) {
	table := new(TimerRegistrationTable)
	owner := new(P)
	other := new(P)
	if !bindTimerRegistrationTable(table, owner) || table.CanRelease() || bindTimerRegistrationTable(table, owner) {
		t.Fatal("bind timer table owner")
	}
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm bound timer")
	}
	if handle, ok := table.Register(other, token, ticket, 10); ok || handle != (TimerRegistrationHandle{}) {
		t.Fatal("bound timer table accepted wrong owner")
	}
	if !rollbackArmedWait(token, ticket) {
		t.Fatal("cleanup wrong-owner ticket")
	}

	token, ticket, handle := prepareTestTimer(t, table, owner, 10)
	if deadline, has, ok := table.NextDeadline(); ok || has || deadline != 0 {
		t.Fatalf("bound table exposed standalone deadline = (%d, %t, %t)", deadline, has, ok)
	}
	if deadline, has, ok := table.nextDeadlineFor(owner); !ok || !has || deadline != 10 {
		t.Fatalf("bound owner deadline = (%d, %t, %t)", deadline, has, ok)
	}
	if _, _, _, ok := table.drainDueFor(other, 10); ok {
		t.Fatal("wrong owner drained bound timer table")
	}
	if completed, _, has, ok := table.drainDueFor(owner, 10); !ok || completed != 1 || has {
		t.Fatalf("owner due scan = (%d, %t, %t)", completed, has, ok)
	}
	if unbindTimerRegistrationTable(table, owner) || timerRegistrationTableEmpty(table, owner) {
		t.Fatal("live delivered timer passed terminal empty check")
	}
	consumeTimerOutcome(t, token, ticket, WaitOutcomeCompleted)
	if !table.RetireCompletedTimer(handle, token, ticket) || !timerRegistrationTableEmpty(table, owner) ||
		!unbindTimerRegistrationTable(table, owner) || !table.CanRelease() {
		t.Fatal("retired timer did not permit terminal unbind")
	}
}

func TestTimerRegistrationValidationAndHandleLayout(t *testing.T) {
	table := new(TimerRegistrationTable)
	p := new(P)
	if _, _, result := PrepareTimerRegistration(p, table, new(WaitToken), -1); result != TimerRegistrationPrepareRejected {
		t.Fatalf("negative absolute deadline result = %d", result)
	}
	if completed, deadline, has, ok := table.DrainDue(-1); ok || completed != 0 || deadline != 0 || has {
		t.Fatalf("negative scan = (%d, %d, %t, %t)", completed, deadline, has, ok)
	}
	if unsafe.Sizeof(TimerRegistrationHandle{}) != 8 || unsafe.Alignof(TimerRegistrationHandle{}) != 4 {
		t.Fatalf("timer handle layout = size %d align %d", unsafe.Sizeof(TimerRegistrationHandle{}), unsafe.Alignof(TimerRegistrationHandle{}))
	}
}
