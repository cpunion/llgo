//go:build !llgo

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

func TestNonblockingLeaseFixedLayout(t *testing.T) {
	if !nonblockingLeaseAtomic64Bounded {
		t.Fatal("host lease tests require the bounded atomic64 profile")
	}
	var layout NonblockingLeaseGate
	if offset := unsafe.Offsetof(layout.state); offset != 0 {
		t.Fatalf("NonblockingLeaseGate.state offset = %d, want 0", offset)
	}
	word := unsafe.Sizeof(uintptr(0))
	wantGate := uintptr(12) + word
	wantGateAlign := unsafe.Alignof(uint64(0))
	if word > wantGateAlign {
		wantGateAlign = word
	}
	if remainder := wantGate % wantGateAlign; remainder != 0 {
		wantGate += wantGateAlign - remainder
	}
	if got := unsafe.Sizeof(NonblockingLeaseGate{}); got != wantGate || unsafe.Alignof(NonblockingLeaseGate{}) != wantGateAlign {
		t.Fatalf("NonblockingLeaseGate layout = size %d align %d, want %d/%d", got, unsafe.Alignof(NonblockingLeaseGate{}), wantGate, wantGateAlign)
	}
	wantLease := uintptr(12) + 2*word
	if remainder := wantLease % word; remainder != 0 {
		wantLease += word - remainder
	}
	if got := unsafe.Sizeof(NonblockingLease{}); got != wantLease || unsafe.Alignof(NonblockingLease{}) != word {
		t.Fatalf("NonblockingLease layout = size %d align %d, want %d/%d", got, unsafe.Alignof(NonblockingLease{}), wantLease, word)
	}
}

func TestNonblockingLeaseNoCopyMarkerImplementsLockUnlock(t *testing.T) {
	type lockUnlock interface {
		Lock()
		Unlock()
	}
	var marker lockUnlock = (*nonblockingLeaseNoCopy)(nil)
	if marker == nil {
		t.Fatal("nonblocking lease no-copy marker lost its vet-compatible methods")
	}
}

func TestNonblockingLeaseGateRejectsMisalignedAtomicState(t *testing.T) {
	storage := make([]byte, unsafe.Sizeof(NonblockingLeaseGate{})+8)
	var gate *NonblockingLeaseGate
	for offset := 0; offset < 8; offset++ {
		candidate := (*NonblockingLeaseGate)(unsafe.Pointer(&storage[offset]))
		if uintptr(unsafe.Pointer(&candidate.state))&7 != 0 {
			gate = candidate
			break
		}
	}
	if gate == nil {
		t.Fatal("could not construct a deliberately misaligned gate")
	}
	if generation, ok := gate.Init(1, NonblockingReadAttempt); ok || generation != 0 {
		t.Fatalf("misaligned Init = %d, %t; want fail-closed", generation, ok)
	}
	if lease, acquired := gate.Acquire(1, NonblockingReadAttempt); acquired || lease != (NonblockingLease{}) ||
		gate.BeginChange(1) || gate.ChangeQuiesced(1) {
		t.Fatal("misaligned gate accepted an atomic operation")
	}
	if generation, ok := gate.Generation(); ok || generation != 0 {
		t.Fatalf("misaligned Generation = %d, %t; want unavailable", generation, ok)
	}
}

func TestNonblockingLeaseGateExactAttemptAndDuplicateRelease(t *testing.T) {
	var gate NonblockingLeaseGate
	generation, ok := gate.Init(41, NonblockingReadAttempt|NonblockingWriteAttempt)
	if !ok || generation != 1 {
		t.Fatalf("Init = %d, %t", generation, ok)
	}
	lease, acquired := gate.Acquire(41, NonblockingReadAttempt)
	if !acquired || !lease.Valid(41, NonblockingReadAttempt) {
		t.Fatal("exact read attempt did not acquire a valid lease")
	}
	if concurrent, acquired := gate.Acquire(41, NonblockingWriteAttempt); acquired || concurrent != (NonblockingLease{}) {
		t.Fatal("exclusive direction gate admitted a second attempt")
	}
	copyOfLease := lease
	if !lease.Release() || lease.Release() {
		t.Fatal("exact lease was not consumed exactly once")
	}
	if copyOfLease.Release() {
		t.Fatal("copied lease released an unrelated aggregate admission")
	}
	concurrent, acquired := gate.Acquire(41, NonblockingWriteAttempt)
	if !acquired || !concurrent.Release() {
		t.Fatal("gate did not reopen after the exact release")
	}
	for _, test := range []struct {
		resource uintptr
		cap      NonblockingCapability
	}{
		{resource: 42, cap: NonblockingReadAttempt},
		{resource: 41, cap: NonblockingAcceptAttempt},
		{resource: 41, cap: 0},
	} {
		if rejected, acquired := gate.Acquire(test.resource, test.cap); acquired || rejected != (NonblockingLease{}) {
			t.Fatalf("Acquire(%d, %#x) unexpectedly succeeded", test.resource, test.cap)
		}
	}
}

func TestNonblockingLeaseGateStaleCopyCannotReleaseNextAttempt(t *testing.T) {
	var gate NonblockingLeaseGate
	if _, ok := gate.Init(41, NonblockingReadAttempt|NonblockingWriteAttempt); !ok {
		t.Fatal("Init failed")
	}
	first, acquired := gate.Acquire(41, NonblockingReadAttempt)
	if !acquired {
		t.Fatal("first Acquire failed")
	}
	stale := first
	if !first.Release() {
		t.Fatal("first Release failed")
	}
	second, acquired := gate.Acquire(41, NonblockingWriteAttempt)
	if !acquired {
		t.Fatal("second Acquire failed")
	}
	for attempt := 0; attempt < 3; attempt++ {
		if stale.Release() {
			t.Fatal("stale copied lease released a later attempt")
		}
	}
	if !second.Valid(41, NonblockingWriteAttempt) || !second.Release() {
		t.Fatal("later attempt was invalidated by a stale copied lease")
	}
}

func TestNonblockingLeaseGateWholeStateCASRejectsReusedToken(t *testing.T) {
	var gate NonblockingLeaseGate
	if _, ok := gate.Init(51, NonblockingReadAttempt); !ok {
		t.Fatal("Init failed")
	}
	old, acquired := gate.Acquire(51, NonblockingReadAttempt)
	if !acquired {
		t.Fatal("old-generation Acquire failed")
	}
	staleState := preemptLoad64(&gate.state)
	oldGeneration := old.generation
	oldToken := old.admission
	if !old.Release() || !gate.BeginChange(oldGeneration) {
		t.Fatal("old generation did not release and withdraw")
	}
	newGeneration, ok := gate.FinishChange(oldGeneration, 52, NonblockingReadAttempt)
	if !ok {
		t.Fatal("FinishChange failed")
	}
	current, acquired := gate.Acquire(52, NonblockingReadAttempt)
	if !acquired ||
		current.generation != newGeneration || current.admission != oldToken {
		t.Fatalf(
			"new lease = generation:%d token:%d, want %d/%d",
			current.generation,
			current.admission,
			newGeneration,
			oldToken,
		)
	}
	// This is the exact pause point that defeated the split generation/admission
	// and helping-claim designs: an old releaser resumes after a new generation
	// has recreated Held with the same token. Its whole-state CAS must fail.
	if preemptCompareAndSwap64(&gate.state, staleState, staleState&^nonblockingLeaseHeld) {
		t.Fatal("stale whole-state CAS released a reused token")
	}
	if !current.Valid(52, NonblockingReadAttempt) || !current.Release() {
		t.Fatal("stale CAS invalidated the current holder")
	}
}

func TestNonblockingLeaseGateReleaseCoversActiveToTransitionConflict(t *testing.T) {
	var gate NonblockingLeaseGate
	generation, ok := gate.Init(61, NonblockingReadAttempt)
	if !ok {
		t.Fatal("Init failed")
	}
	lease, acquired := gate.Acquire(61, NonblockingReadAttempt)
	if !acquired {
		t.Fatal("Acquire failed")
	}
	activeState := preemptLoad64(&gate.state)
	admission := lease.admission
	if !gate.BeginChange(generation) {
		t.Fatal("BeginChange failed")
	}
	if !releaseNonblockingLeaseAdmissionFromState(&gate, generation, admission, activeState) {
		t.Fatal("fixed second release CAS did not cover Active-to-Transition conflict")
	}
	if !gate.ChangeQuiesced(generation) {
		t.Fatal("transition did not quiesce after bounded release")
	}
}

func TestNonblockingLeaseGateAdmissionTokenRollsGeneration(t *testing.T) {
	var gate NonblockingLeaseGate
	generation, ok := gate.Init(17, NonblockingReadAttempt)
	if !ok {
		t.Fatal("Init failed")
	}
	preemptStore64(
		&gate.state,
		nonblockingLeasePackState(
			nonblockingLeaseActive,
			generation,
			uint32(nonblockingLeaseTokenMask),
			false,
		),
	)
	rolled, acquired := gate.Acquire(17, NonblockingReadAttempt)
	if !acquired {
		t.Fatal("exhausted admission token did not roll to a fresh generation")
	}
	if rolled.generation != generation+1 || rolled.admission != 1 {
		t.Fatalf(
			"rolled lease = generation:%d admission:%d, want %d/1",
			rolled.generation, rolled.admission, generation+1,
		)
	}
	if !rolled.Release() {
		t.Fatal("rolled admission could not release")
	}
}

func TestNonblockingLeaseGateGenerationOverflowRetires(t *testing.T) {
	var gate NonblockingLeaseGate
	if _, ok := gate.Init(17, NonblockingReadAttempt); !ok {
		t.Fatal("Init failed")
	}
	preemptStore64(
		&gate.state,
		nonblockingLeasePackState(
			nonblockingLeaseActive,
			^uint32(0),
			uint32(nonblockingLeaseTokenMask),
			false,
		),
	)
	if exhausted, acquired := gate.Acquire(17, NonblockingReadAttempt); acquired || exhausted != (NonblockingLease{}) {
		t.Fatal("generation overflow admitted a reused token pair")
	}
	if generation, ok := gate.Generation(); ok || generation != 0 {
		t.Fatalf("overflowed gate Generation = %d, %t; want retired", generation, ok)
	}
}

func TestNonblockingLeaseGateFinishChangeGenerationOverflowRetiresClaim(t *testing.T) {
	var gate NonblockingLeaseGate
	if _, ok := gate.Init(18, NonblockingReadAttempt); !ok {
		t.Fatal("Init failed")
	}
	const generation = ^uint32(0)
	preemptStore64(
		&gate.state,
		nonblockingLeasePackState(nonblockingLeaseActive, generation, 7, false),
	)
	if !gate.BeginChange(generation) {
		t.Fatal("BeginChange failed")
	}
	if next, changed := gate.FinishChange(generation, 19, NonblockingWriteAttempt); changed || next != 0 {
		t.Fatalf("overflowing FinishChange = %d, %t; want retired failure", next, changed)
	}
	state := preemptLoad64(&gate.state)
	if nonblockingLeaseStateLifecycle(state) != nonblockingLeaseRetired ||
		nonblockingLeaseStateHeld(state) || nonblockingLeaseStateToken(state) != 0 {
		t.Fatalf("overflowing publish state = %#x; want unheld token-zero tombstone", state)
	}
	if preemptLoadWord(&gate.resource) != 0 || preemptLoad(&gate.capabilities) != 0 {
		t.Fatal("overflowing publishing claim retained resource fields")
	}
}

func TestNonblockingLeaseGateAdmissionAfterGenerationChange(t *testing.T) {
	var gate NonblockingLeaseGate
	generation, ok := gate.Init(71, NonblockingReadAttempt)
	if !ok {
		t.Fatal("Init failed")
	}
	old, acquired := gate.Acquire(71, NonblockingReadAttempt)
	if !acquired {
		t.Fatal("old-generation Acquire failed")
	}
	stale := old
	if !old.Release() || !gate.BeginChange(generation) {
		t.Fatal("initial transition failed")
	}
	next, ok := gate.FinishChange(generation, 72, NonblockingReadAttempt)
	if !ok {
		t.Fatal("FinishChange failed")
	}
	// Admission happens after the transition and must bind the newly published
	// generation. In particular, rollback must never strand its exact token just
	// because an observer captured the old generation before admission.
	lease, acquired := gate.Acquire(72, NonblockingReadAttempt)
	if !acquired || lease.generation != next {
		t.Fatalf("Acquire bound generation %d, want %d", lease.generation, next)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if stale.Release() {
			t.Fatal("stale prior-generation lease released a reused attempt token")
		}
	}
	if !lease.Valid(72, NonblockingReadAttempt) {
		t.Fatal("stale prior-generation release invalidated the new lease")
	}
	if !lease.Release() {
		t.Fatal("new-generation admission could not release")
	}
}

func TestNonblockingLeaseGatePublishingClaimRejectsStaleReleaseAndOtherOwners(t *testing.T) {
	var gate NonblockingLeaseGate
	generation, ok := gate.Init(75, NonblockingReadAttempt)
	if !ok {
		t.Fatal("Init failed")
	}
	lease, acquired := gate.Acquire(75, NonblockingReadAttempt)
	if !acquired {
		t.Fatal("Acquire failed")
	}
	stale := lease
	if !lease.Release() || !gate.BeginChange(generation) {
		t.Fatal("prepare transition failed")
	}
	transition := preemptLoad64(&gate.state)
	claim := nonblockingLeasePackState(nonblockingLeaseTransition, generation, 0, true)
	if !preemptCompareAndSwap64(&gate.state, transition, claim) {
		t.Fatal("could not install publishing claim")
	}
	if stale.Release() {
		t.Fatal("stale nonzero admission cleared the token-zero publishing claim")
	}
	if releaseNonblockingLeaseAdmissionFromState(&gate, generation, 0, claim) {
		t.Fatal("zero admission cleared the reserved publishing claim")
	}
	if state := preemptLoad64(&gate.state); state != claim {
		t.Fatalf("stale release changed publishing claim: got %#x, want %#x", state, claim)
	}
	if gate.ChangeQuiesced(generation) || gate.FinishRetire(generation) {
		t.Fatal("another owner entered a live publishing claim")
	}
	if next, changed := gate.FinishChange(generation, 76, NonblockingWriteAttempt); changed || next != 0 {
		t.Fatalf("second FinishChange entered publishing claim: %d, %t", next, changed)
	}
	if state := preemptLoad64(&gate.state); state != claim {
		t.Fatalf("rejected owner changed publishing claim: got %#x, want %#x", state, claim)
	}

	preemptStoreWord(&gate.resource, 76)
	preemptStore(&gate.capabilities, uint32(NonblockingWriteAttempt))
	nextGeneration := generation + 1
	active := nonblockingLeasePackState(nonblockingLeaseActive, nextGeneration, 0, false)
	if !preemptCompareAndSwap64(&gate.state, claim, active) {
		t.Fatal("publishing owner could not complete claimed generation")
	}
	fresh, acquired := gate.Acquire(76, NonblockingWriteAttempt)
	if !acquired || fresh.generation != nextGeneration || !fresh.Release() {
		t.Fatal("claimed replacement generation was not published atomically")
	}
}

func TestNonblockingLeaseGateConcurrentFinishChangeHasOnePublisher(t *testing.T) {
	const iterations = 200
	type result struct {
		resource   uintptr
		capability NonblockingCapability
		generation uint32
		ok         bool
	}
	for iteration := 0; iteration < iterations; iteration++ {
		var gate NonblockingLeaseGate
		generation, ok := gate.Init(80, NonblockingReadAttempt)
		if !ok || !gate.BeginChange(generation) {
			t.Fatalf("iteration %d: prepare transition failed", iteration)
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		for _, candidate := range []struct {
			resource   uintptr
			capability NonblockingCapability
		}{
			{resource: 81, capability: NonblockingReadAttempt},
			{resource: 82, capability: NonblockingWriteAttempt},
		} {
			candidate := candidate
			go func() {
				<-start
				next, changed := gate.FinishChange(generation, candidate.resource, candidate.capability)
				results <- result{
					resource: candidate.resource, capability: candidate.capability,
					generation: next, ok: changed,
				}
			}()
		}
		close(start)
		first, second := <-results, <-results
		winnerCount := 0
		var winner result
		for _, candidate := range []result{first, second} {
			if candidate.ok {
				winnerCount++
				winner = candidate
			} else if candidate.generation != 0 {
				t.Fatalf("iteration %d: losing owner returned generation %d", iteration, candidate.generation)
			}
		}
		if winnerCount != 1 || winner.generation != generation+1 {
			t.Fatalf("iteration %d: results = %+v, %+v; want one generation %d publisher", iteration, first, second, generation+1)
		}
		lease, acquired := gate.Acquire(winner.resource, winner.capability)
		if !acquired || !lease.Release() {
			t.Fatalf("iteration %d: winning resource/capability was not published", iteration)
		}
		loser := first
		if loser.ok {
			loser = second
		}
		if lease, acquired := gate.Acquire(loser.resource, loser.capability); acquired || lease != (NonblockingLease{}) {
			t.Fatalf("iteration %d: losing owner contaminated published fields", iteration)
		}
	}
}

func TestNonblockingLeaseGateStaleBeginChangeDoesNotSealNewGeneration(t *testing.T) {
	var gate NonblockingLeaseGate
	old, ok := gate.Init(81, NonblockingReadAttempt)
	if !ok || !gate.BeginChange(old) {
		t.Fatal("initial transition failed")
	}
	current, ok := gate.FinishChange(old, 82, NonblockingReadAttempt)
	if !ok {
		t.Fatal("FinishChange failed")
	}
	if gate.BeginChange(old) {
		t.Fatal("stale generation acquired transition ownership")
	}
	lease, acquired := gate.Acquire(82, NonblockingReadAttempt)
	if !acquired || lease.generation != current || !lease.Release() {
		t.Fatal("stale BeginChange left the current generation sealed")
	}
}

func TestNonblockingLeaseGateChangeWaitsForOldGeneration(t *testing.T) {
	var gate NonblockingLeaseGate
	generation, ok := gate.Init(7, NonblockingReadAttempt)
	if !ok {
		t.Fatal("Init failed")
	}
	old, acquired := gate.Acquire(7, NonblockingReadAttempt)
	if !acquired {
		t.Fatal("old generation Acquire failed")
	}
	if !gate.BeginChange(generation) {
		t.Fatal("BeginChange failed")
	}
	if blocked, acquired := gate.Acquire(7, NonblockingReadAttempt); acquired || blocked != (NonblockingLease{}) {
		t.Fatal("transition admitted a new attempt")
	}
	if next, ok := gate.FinishChange(generation, 8, NonblockingWriteAttempt); ok || next != 0 {
		t.Fatalf("FinishChange before quiescence = %d, %t", next, ok)
	}
	if gate.ChangeQuiesced(generation) {
		t.Fatal("transition reported quiescence while the old lease was held")
	}
	if old.Valid(7, NonblockingReadAttempt) {
		t.Fatal("lease remained valid after transition withdrawal")
	}
	if !old.Release() {
		t.Fatal("old lease could not release into sealed transition")
	}
	if !gate.ChangeQuiesced(generation) {
		t.Fatal("transition did not report quiescence after exact release")
	}
	next, ok := gate.FinishChange(generation, 8, NonblockingWriteAttempt)
	if !ok || next != generation+1 {
		t.Fatalf("FinishChange = %d, %t", next, ok)
	}
	if current, ok := gate.Generation(); !ok || current != next {
		t.Fatalf("Generation = %d, %t; want %d", current, ok, next)
	}
	if fresh, acquired := gate.Acquire(7, NonblockingReadAttempt); acquired || fresh != (NonblockingLease{}) {
		t.Fatal("old resource/generation reacquired after replacement")
	}
	fresh, acquired := gate.Acquire(8, NonblockingWriteAttempt)
	if !acquired || !fresh.Release() {
		t.Fatal("fresh generation did not acquire/release")
	}
}

func TestNonblockingLeaseGateRetireLeavesPermanentTombstone(t *testing.T) {
	var gate NonblockingLeaseGate
	generation, ok := gate.Init(5, NonblockingConnectAttempt)
	if !ok || !gate.BeginChange(generation) || !gate.FinishRetire(generation) {
		t.Fatal("retire sequence failed")
	}
	if current, ok := gate.Generation(); ok || current != 0 {
		t.Fatalf("retired Generation = %d, %t", current, ok)
	}
	if lease, acquired := gate.Acquire(5, NonblockingConnectAttempt); acquired || lease != (NonblockingLease{}) ||
		gate.BeginChange(generation) {
		t.Fatal("retired gate accepted activity")
	}
	if next, ok := gate.Init(5, NonblockingConnectAttempt); ok || next != 0 {
		t.Fatalf("retired Init = %d, %t", next, ok)
	}
}

func TestNonblockingLeaseGateConcurrentTransitionJoinsAttempt(t *testing.T) {
	var gate NonblockingLeaseGate
	generation, ok := gate.Init(91, NonblockingReadAttempt)
	if !ok {
		t.Fatal("Init failed")
	}
	held := make(chan struct{})
	release := make(chan struct{})
	released := make(chan bool, 1)
	go func() {
		lease, acquired := gate.Acquire(91, NonblockingReadAttempt)
		if !acquired {
			released <- false
			return
		}
		close(held)
		<-release
		released <- lease.Release()
	}()
	<-held
	if !gate.BeginChange(generation) || gate.ChangeQuiesced(generation) {
		t.Fatal("transition did not retain the concurrent attempt")
	}
	close(release)
	if !<-released || !gate.ChangeQuiesced(generation) {
		t.Fatal("transition did not strongly join the concurrent attempt")
	}
	if next, ok := gate.FinishChange(generation, 92, NonblockingReadAttempt); !ok || next != generation+1 {
		t.Fatalf("FinishChange = %d, %t", next, ok)
	}
}
