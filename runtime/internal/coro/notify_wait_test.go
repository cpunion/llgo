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

func prepareNotifyTestWait(
	t *testing.T,
	p *P,
	waits *WaitRegistrationTable,
	catalog *KeyedWaitCatalog,
	token *WaitToken,
	key uintptr,
	logicalTicket uint32,
) (WaitTicket, KeyedWaitHandle) {
	t.Helper()
	ticket, handle, result := PrepareNotifyWait(p, waits, catalog, token, key, logicalTicket)
	if result != KeyedWaitPrepared || ticket == 0 || handle == (KeyedWaitHandle{}) {
		t.Fatalf("prepare notify ticket %d = (%d, %+v, %d)", logicalTicket, ticket, handle, result)
	}
	return ticket, handle
}

func retireNotifyTestWait(
	t *testing.T,
	waits *WaitRegistrationTable,
	catalog *KeyedWaitCatalog,
	token *WaitToken,
	ticket WaitTicket,
	handle KeyedWaitHandle,
) {
	t.Helper()
	consumeRegisteredOutcome(t, token, ticket, WaitOutcomeCompleted)
	if !RetireCompletedNotifyWait(catalog, waits, handle, token, ticket) {
		t.Fatalf("retire notify wait %+v", handle)
	}
}

func TestKeyedWaitNamespacePreventsSemaphoreNotifyCollision(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	catalog := new(KeyedWaitCatalog)
	const key = uintptr(0x1234)
	var semaToken, notifyToken WaitToken
	semaTicket, semaHandle, semaResult := PrepareSemaphoreWait(p, waits, catalog, &semaToken, key)
	if semaResult != SemaphoreWaitPrepared {
		t.Fatalf("prepare semaphore collision fixture = %d", semaResult)
	}
	notifyTicket, notifyHandle := prepareNotifyTestWait(t, p, waits, catalog, &notifyToken, key, 0)

	if result := PostNotifyWaitOne(catalog, waits, key, 0); result != KeyedWaitPosted {
		t.Fatalf("post colliding notify = %d", result)
	}
	if drained, ok := waits.Drain(); !ok || drained != 1 {
		t.Fatalf("drain colliding notify = (%d, %t)", drained, ok)
	}
	if outcome, ok := WaitOutcomeOf(&semaToken, semaTicket); ok || outcome != WaitOutcomeInvalid {
		t.Fatalf("notify woke semaphore namespace = (%d, %t)", outcome, ok)
	}
	retireNotifyTestWait(t, waits, catalog, &notifyToken, notifyTicket, notifyHandle)

	if result := PostSemaphoreWait(catalog, waits, key); result != SemaphoreWaitPosted {
		t.Fatalf("post colliding semaphore = %d", result)
	}
	if drained, ok := waits.Drain(); !ok || drained != 1 {
		t.Fatalf("drain colliding semaphore = (%d, %t)", drained, ok)
	}
	consumeRegisteredOutcome(t, &semaToken, semaTicket, WaitOutcomeCompleted)
	if !RetireCompletedSemaphoreWait(catalog, waits, semaHandle, &semaToken, semaTicket) {
		t.Fatal("retire colliding semaphore")
	}
}

func TestNotifyWaitOneUsesTicketFIFODespiteRegistrationOrder(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	catalog := new(KeyedWaitCatalog)
	const key = uintptr(0x7788)
	logical := [...]uint32{2, 0, 1}
	var tokens [3]WaitToken
	var tickets [3]WaitTicket
	var handles [3]KeyedWaitHandle
	var retired [3]bool
	for index, target := range logical {
		tickets[index], handles[index] = prepareNotifyTestWait(t, p, waits, catalog, &tokens[index], key, target)
	}
	for target := uint32(0); target < 3; target++ {
		if result := PostNotifyWaitOne(catalog, waits, key, target); result != KeyedWaitPosted {
			t.Fatalf("post notify ticket %d = %d", target, result)
		}
		if drained, ok := waits.Drain(); !ok || drained != 1 {
			t.Fatalf("drain notify ticket %d = (%d, %t)", target, drained, ok)
		}
		for index, logicalTicket := range logical {
			if retired[index] {
				continue
			}
			outcome, ready := WaitOutcomeOf(&tokens[index], tickets[index])
			if logicalTicket == target {
				if !ready || outcome != WaitOutcomeCompleted {
					t.Fatalf("ticket %d was not the one completed: (%d, %t)", target, outcome, ready)
				}
				retireNotifyTestWait(t, waits, catalog, &tokens[index], tickets[index], handles[index])
				retired[index] = true
			} else if ready {
				t.Fatalf("ticket %d woke out of FIFO order while notifying %d", logicalTicket, target)
			}
		}
	}
}

func TestNotifyBeforeParkRepairPostsExactPublishedWait(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	catalog := new(KeyedWaitCatalog)
	const key = uintptr(0x8899)
	var token WaitToken
	ticket, handle := prepareNotifyTestWait(t, p, waits, catalog, &token, key, 41)
	if result := PostPreparedNotifyWait(catalog, waits, handle, key, 42); result != KeyedWaitPostInvalid {
		t.Fatalf("wrong logical-ticket repair = %d", result)
	}
	if result := PostPreparedNotifyWait(catalog, waits, handle, key, 41); result != KeyedWaitPosted {
		t.Fatalf("exact notify-before-park repair = %d", result)
	}
	if result := PostPreparedNotifyWait(catalog, waits, handle, key, 41); result != KeyedWaitPostInvalid {
		t.Fatalf("duplicate notify-before-park repair = %d", result)
	}
	if drained, ok := waits.Drain(); !ok || drained != 1 {
		t.Fatalf("drain repaired notify = (%d, %t)", drained, ok)
	}
	retireNotifyTestWait(t, waits, catalog, &token, ticket, handle)
}

func TestNotifyAllUsesBoundedWrappedTicketRange(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	catalog := new(KeyedWaitCatalog)
	const key = uintptr(0x99aa)
	logical := [...]uint32{^uint32(0) - 1, ^uint32(0), 0, 1}
	var tokens [4]WaitToken
	var tickets [4]WaitTicket
	var handles [4]KeyedWaitHandle
	for index, target := range logical {
		tickets[index], handles[index] = prepareNotifyTestWait(t, p, waits, catalog, &tokens[index], key, target)
	}
	posted, ok := PostNotifyWaitAll(catalog, waits, key, ^uint32(0)-1, 1)
	if !ok || posted != 3 {
		t.Fatalf("wrapped notify-all = (%d, %t), want (3,true)", posted, ok)
	}
	if drained, ok := waits.Drain(); !ok || drained != 3 {
		t.Fatalf("drain wrapped notify-all = (%d, %t)", drained, ok)
	}
	for index := 0; index < 3; index++ {
		retireNotifyTestWait(t, waits, catalog, &tokens[index], tickets[index], handles[index])
	}
	if outcome, ready := WaitOutcomeOf(&tokens[3], tickets[3]); ready || outcome != WaitOutcomeInvalid {
		t.Fatalf("ticket at wrapped range end completed = (%d, %t)", outcome, ready)
	}
	if result := PostNotifyWaitOne(catalog, waits, key, 1); result != KeyedWaitPosted {
		t.Fatalf("post wrapped range end = %d", result)
	}
	if drained, ok := waits.Drain(); !ok || drained != 1 {
		t.Fatalf("drain wrapped range end = (%d, %t)", drained, ok)
	}
	retireNotifyTestWait(t, waits, catalog, &tokens[3], tickets[3], handles[3])
}

func TestNotifyWaitPagedCatalogCompletesOneThousand(t *testing.T) {
	const count = 1000
	p := new(P)
	waits := new(WaitRegistrationTable)
	catalog := new(KeyedWaitCatalog)
	var waitPages [15]WaitRegistrationPage
	var keyedPages [15]KeyedWaitPage
	if !ConfigureWaitRegistrationPages(waits, waitPages[:]) ||
		!ConfigureKeyedWaitPages(catalog, keyedPages[:]) {
		t.Fatal("configure paged notify waits")
	}
	const key = uintptr(0xaabb)
	tokens := make([]WaitToken, count)
	tickets := make([]WaitTicket, count)
	handles := make([]KeyedWaitHandle, count)
	for index := range tokens {
		tickets[index], handles[index] = prepareNotifyTestWait(
			t, p, waits, catalog, &tokens[index], key, uint32(index),
		)
	}
	posted, ok := PostNotifyWaitAll(catalog, waits, key, 0, count)
	if !ok || posted != count {
		t.Fatalf("paged notify-all = (%d, %t)", posted, ok)
	}
	if drained, ok := waits.Drain(); !ok || drained != count {
		t.Fatalf("drain paged notify-all = (%d, %t)", drained, ok)
	}
	for index := range tokens {
		retireNotifyTestWait(t, waits, catalog, &tokens[index], tickets[index], handles[index])
	}
	if !catalog.CanRelease() || !waits.CanRelease() {
		t.Fatal("paged notify waits retained ownership")
	}
}
