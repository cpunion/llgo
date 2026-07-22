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

func TestSemaphoreWaitHandlePODLayoutUnaffectedByPaging(t *testing.T) {
	if unsafe.Sizeof(SemaphoreWaitHandle{}) != 8 || unsafe.Alignof(SemaphoreWaitHandle{}) != 4 ||
		unsafe.Offsetof(SemaphoreWaitHandle{}.Generation) != 4 {
		t.Fatalf("semaphore handle layout changed: size=%d align=%d generation=%d",
			unsafe.Sizeof(SemaphoreWaitHandle{}), unsafe.Alignof(SemaphoreWaitHandle{}),
			unsafe.Offsetof(SemaphoreWaitHandle{}.Generation))
	}
}

func TestSemaphoreWaitCatalogPostsOneFIFOAndRetires(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	catalog := new(SemaphoreWaitCatalog)
	if !catalog.CanRelease() {
		t.Fatal("zero semaphore catalog is not releasable")
	}
	const key = uintptr(0x1234)
	var tokens [2]WaitToken
	var tickets [2]WaitTicket
	var handles [2]SemaphoreWaitHandle
	for index := range tokens {
		var result SemaphoreWaitPrepareResult
		tickets[index], handles[index], result = PrepareSemaphoreWait(p, waits, catalog, &tokens[index], key)
		if result != SemaphoreWaitPrepared || tickets[index] == 0 || handles[index] == (SemaphoreWaitHandle{}) {
			t.Fatalf("prepare semaphore waiter %d = (%d, %+v, %d)", index, tickets[index], handles[index], result)
		}
	}
	if catalog.CanRelease() {
		t.Fatal("live semaphore catalog reported releasable")
	}
	if result := PostSemaphoreWait(catalog, waits, key); result != SemaphoreWaitPosted {
		t.Fatalf("post first semaphore waiter = %d", result)
	}
	if result := PostSemaphoreWait(catalog, waits, key); result != SemaphoreWaitPosted {
		t.Fatalf("post second semaphore waiter = %d", result)
	}
	if result := PostSemaphoreWait(catalog, waits, key); result != SemaphoreWaitNoWaiter {
		t.Fatalf("post exhausted semaphore key = %d", result)
	}
	if drained, ok := waits.Drain(); !ok || drained != 2 {
		t.Fatalf("drain semaphore completions = (%d, %t)", drained, ok)
	}
	for index := range tokens {
		consumeRegisteredOutcome(t, &tokens[index], tickets[index], WaitOutcomeCompleted)
		if !RetireCompletedSemaphoreWait(catalog, waits, handles[index], &tokens[index], tickets[index]) {
			t.Fatalf("retire semaphore waiter %d", index)
		}
	}
	if !catalog.CanRelease() || !waits.CanRelease() {
		t.Fatalf("retired semaphore ownership remains: catalog=%t waits=%t", catalog.CanRelease(), waits.CanRelease())
	}
}

func TestSemaphoreWaitCatalogKeyIsolationAndStableGeneration(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	catalog := new(SemaphoreWaitCatalog)
	var first, second WaitToken
	firstTicket, firstHandle, result := PrepareSemaphoreWait(p, waits, catalog, &first, 11)
	if result != SemaphoreWaitPrepared {
		t.Fatalf("prepare first key = %d", result)
	}
	secondTicket, secondHandle, result := PrepareSemaphoreWait(p, waits, catalog, &second, 22)
	if result != SemaphoreWaitPrepared {
		t.Fatalf("prepare second key = %d", result)
	}
	if result := PostSemaphoreWait(catalog, waits, 33); result != SemaphoreWaitNoWaiter || waits.Pending() {
		t.Fatalf("unmatched key post = %d, pending=%t", result, waits.Pending())
	}
	if result := PostSemaphoreWait(catalog, waits, 22); result != SemaphoreWaitPosted {
		t.Fatalf("post second key = %d", result)
	}
	if drained, ok := waits.Drain(); !ok || drained != 1 {
		t.Fatalf("drain second key = (%d, %t)", drained, ok)
	}
	if outcome, ok := WaitOutcomeOf(&first, firstTicket); ok || outcome != WaitOutcomeInvalid {
		t.Fatalf("unmatched first key completed = (%d, %t)", outcome, ok)
	}
	consumeRegisteredOutcome(t, &second, secondTicket, WaitOutcomeCompleted)
	if !RetireCompletedSemaphoreWait(catalog, waits, secondHandle, &second, secondTicket) {
		t.Fatal("retire second key")
	}
	if RetireCompletedSemaphoreWait(catalog, waits, secondHandle, &second, secondTicket) {
		t.Fatal("stale semaphore handle retired twice")
	}
	if result := PostSemaphoreWait(catalog, waits, 11); result != SemaphoreWaitPosted {
		t.Fatalf("post first key = %d", result)
	}
	if drained, ok := waits.Drain(); !ok || drained != 1 {
		t.Fatalf("drain first key = (%d, %t)", drained, ok)
	}
	consumeRegisteredOutcome(t, &first, firstTicket, WaitOutcomeCompleted)
	if !RetireCompletedSemaphoreWait(catalog, waits, firstHandle, &first, firstTicket) {
		t.Fatal("retire first key")
	}

	var reused WaitToken
	reusedTicket, reusedHandle, result := PrepareSemaphoreWait(p, waits, catalog, &reused, 11)
	if result != SemaphoreWaitPrepared || reusedHandle.Slot != firstHandle.Slot || reusedHandle.Generation == firstHandle.Generation {
		t.Fatalf("reused semaphore generation = (%d, %+v, %d), old=%+v", reusedTicket, reusedHandle, result, firstHandle)
	}
	if result := PostSemaphoreWait(catalog, waits, 11); result != SemaphoreWaitPosted {
		t.Fatalf("post reused key = %d", result)
	}
	if drained, ok := waits.Drain(); !ok || drained != 1 {
		t.Fatalf("drain reused key = (%d, %t)", drained, ok)
	}
	consumeRegisteredOutcome(t, &reused, reusedTicket, WaitOutcomeCompleted)
	if !RetireCompletedSemaphoreWait(catalog, waits, reusedHandle, &reused, reusedTicket) {
		t.Fatal("retire reused key")
	}
}

func TestSemaphoreWaitCatalogPublicationFailureRollsBackCommonWait(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	catalog := new(SemaphoreWaitCatalog)
	for index := range catalog.slots {
		catalog.slots[index] = semaphoreWaitSlot{
			generation: 1,
			state:      semaphoreWaitActive,
			key:        uintptr(index + 1),
			sequence:   uint64(index + 1),
			wait:       WaitRegistrationHandle{Slot: 1, Generation: 1},
		}
	}
	var token WaitToken
	if ticket, handle, result := PrepareSemaphoreWait(p, waits, catalog, &token, 999); result != SemaphoreWaitPrepareRejected || ticket != 0 || handle != (SemaphoreWaitHandle{}) {
		t.Fatalf("full semaphore catalog prepare = (%d, %+v, %d)", ticket, handle, result)
	}
	if !waits.CanRelease() {
		t.Fatal("catalog publication failure retained common wait registration")
	}
	if ticket, ok := ArmWait(&token); !ok || ticket == 0 {
		t.Fatalf("rolled-back semaphore token is not reusable = (%d, %t)", ticket, ok)
	}
}

func TestSemaphoreWaitReleaseBeforePrepareRepairPostsExactNewWaiter(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	catalog := new(SemaphoreWaitCatalog)
	const key = uintptr(0x4455)

	// Model a release after the acquire fast path observed zero but before it
	// entered prepare. With no published waiter the release leaves one token.
	counter := uint32(0)
	if result := PostSemaphoreWait(catalog, waits, key); result != SemaphoreWaitNoWaiter {
		t.Fatalf("release before prepare = %d", result)
	}
	counter++

	var token WaitToken
	ticket, handle, result := PrepareSemaphoreWait(p, waits, catalog, &token, key)
	if result != SemaphoreWaitPrepared {
		t.Fatalf("prepare after early release = (%d, %+v, %d)", ticket, handle, result)
	}
	if counter == 0 {
		t.Fatal("model lost early semaphore token")
	}
	if result := PostPreparedSemaphoreWait(catalog, waits, handle); result != SemaphoreWaitPosted {
		t.Fatalf("post-publication counter repair = %d", result)
	}
	if result := PostPreparedSemaphoreWait(catalog, waits, handle); result != SemaphoreWaitPostInvalid {
		t.Fatalf("duplicate exact repair post = %d", result)
	}
	if drained, ok := waits.Drain(); !ok || drained != 1 {
		t.Fatalf("drain repaired waiter = (%d, %t)", drained, ok)
	}
	consumeRegisteredOutcome(t, &token, ticket, WaitOutcomeCompleted)
	if !RetireCompletedSemaphoreWait(catalog, waits, handle, &token, ticket) {
		t.Fatal("retire repaired waiter")
	}
}

func TestSemaphoreAndCommonWaitPagedCatalogsCompleteOneThousandFIFO(t *testing.T) {
	const count = 1000
	p := new(P)
	waits := new(WaitRegistrationTable)
	catalog := new(SemaphoreWaitCatalog)
	var waitPages [15]WaitRegistrationPage
	var semaphorePages [15]SemaphoreWaitPage
	if !ConfigureWaitRegistrationPages(waits, waitPages[:]) {
		t.Fatal("configure paged semaphore common waits")
	}
	var mismatch WaitToken
	if ticket, handle, result := PrepareSemaphoreWait(p, waits, catalog, &mismatch, 1); result != SemaphoreWaitPrepareInvalid || ticket != 0 || handle != (SemaphoreWaitHandle{}) {
		t.Fatalf("mismatched semaphore/wait capacity = (%d, %+v, %d)", ticket, handle, result)
	}
	if !ConfigureSemaphoreWaitPages(catalog, semaphorePages[:1]) ||
		SemaphoreWaitConfiguredCapacity(catalog) != 2*SemaphoreWaitPageCapacity ||
		!ConfigureSemaphoreWaitPages(catalog, semaphorePages[:]) ||
		SemaphoreWaitConfiguredCapacity(catalog) != WaitRegistrationConfiguredCapacity(waits) {
		t.Fatal("configure paged semaphore key catalog")
	}
	const key = uintptr(0x1234)
	tokens := make([]WaitToken, count)
	tickets := make([]WaitTicket, count)
	handles := make([]SemaphoreWaitHandle, count)
	for index := range tokens {
		ticket, handle, result := PrepareSemaphoreWait(p, waits, catalog, &tokens[index], key)
		if result != SemaphoreWaitPrepared || !claimWait(&tokens[index], ticket) {
			t.Fatalf("prepare paged semaphore %d = (%d, %+v, %d)", index, ticket, handle, result)
		}
		tickets[index], handles[index] = ticket, handle
	}
	if handles[SemaphoreWaitPageCapacity].Slot != SemaphoreWaitPageCapacity+1 || handles[count-1].Slot != count {
		t.Fatalf("paged semaphore handles did not remain linear: page2=%+v last=%+v", handles[SemaphoreWaitPageCapacity], handles[count-1])
	}
	var replacement [15]SemaphoreWaitPage
	if ConfigureSemaphoreWaitPages(catalog, replacement[:]) || ConfigureSemaphoreWaitPages(catalog, semaphorePages[:14]) {
		t.Fatal("live paged semaphore catalog allowed replacement or shrink")
	}
	for index := 0; index < count; index++ {
		if result := PostSemaphoreWait(catalog, waits, key); result != SemaphoreWaitPosted {
			t.Fatalf("post paged semaphore %d = %d", index, result)
		}
	}
	if result := PostSemaphoreWait(catalog, waits, key); result != SemaphoreWaitNoWaiter {
		t.Fatalf("post beyond paged semaphore FIFO = %d", result)
	}
	if drained, ok := waits.Drain(); !ok || drained != count {
		t.Fatalf("drain paged semaphore waits = (%d, %t)", drained, ok)
	}
	for index := range tokens {
		if outcome, ok := consumeWait(&tokens[index], tickets[index]); !ok || outcome != WaitOutcomeCompleted ||
			!RetireCompletedSemaphoreWait(catalog, waits, handles[index], &tokens[index], tickets[index]) {
			t.Fatalf("retire paged semaphore %d", index)
		}
	}
	if !catalog.CanRelease() || !waits.CanRelease() ||
		!ConfigureSemaphoreWaitPages(catalog, semaphorePages[:]) ||
		!ConfigureWaitRegistrationPages(waits, waitPages[:]) {
		t.Fatal("paged semaphore catalogs did not return to reusable configured state")
	}
}
