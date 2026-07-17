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

func TestProducerSourceSlotLayoutAndEmbedding(t *testing.T) {
	if unsafe.Sizeof(producerSourceSlot{}) != 12 || unsafe.Alignof(producerSourceSlot{}) != 4 ||
		unsafe.Offsetof(producerSourceSlot{}.state) != 0 || unsafe.Offsetof(producerSourceSlot{}.generation) != 4 ||
		unsafe.Offsetof(producerSourceSlot{}.inflight) != 8 {
		t.Fatalf("producer source slot layout: size=%d align=%d state=%d generation=%d inflight=%d",
			unsafe.Sizeof(producerSourceSlot{}), unsafe.Alignof(producerSourceSlot{}),
			unsafe.Offsetof(producerSourceSlot{}.state), unsafe.Offsetof(producerSourceSlot{}.generation),
			unsafe.Offsetof(producerSourceSlot{}.inflight))
	}
	if unsafe.Offsetof(manualOperationSlot{}.producerSourceSlot) != 0 ||
		unsafe.Offsetof(taskControlSlot{}.producerSourceSlot) != 0 ||
		unsafe.Offsetof(waitRegistrationSlot{}.producerSourceSlot) != 0 ||
		unsafe.Offsetof(channelOperationSlot{}.producerSourceSlot) != 0 {
		t.Fatal("producer source slot is not the first concrete slot field")
	}
	if uint32(waitRegistrationFree) != uint32(producerSourceFree) ||
		uint32(waitRegistrationInitializing) != uint32(producerSourceInitializing) ||
		uint32(waitRegistrationActive) != uint32(producerSourceActive) {
		t.Fatal("wait registration admission prefix changed lifecycle values")
	}
}

func TestProducerAdmissionCheckedRelease(t *testing.T) {
	if producerAdmissionReleaseChecked(nil) {
		t.Fatal("released nil admission")
	}
	var word uint32
	if producerAdmissionReleaseChecked(&word) || !producerAdmissionAcquire(&word) ||
		!producerAdmissionReleaseChecked(&word) || producerAdmissionReleaseChecked(&word) || preemptLoad(&word) != 0 {
		t.Fatalf("checked open release = %#x", preemptLoad(&word))
	}
	if !producerAdmissionAcquire(&word) || !producerAdmissionSeal(&word) ||
		!producerAdmissionReleaseChecked(&word) || producerAdmissionReleaseChecked(&word) ||
		preemptLoad(&word) != producerAdmissionClosed {
		t.Fatalf("checked sealed release = %#x", preemptLoad(&word))
	}
}

func TestProducerSourceSlotPristineAdmissionDoesNotLeakInitializing(t *testing.T) {
	var slot producerSourceSlot
	if !producerSourceSlotReusable(&slot) {
		t.Fatal("observe pristine reusable slot")
	}
	if !producerAdmissionAcquire(&slot.inflight) {
		t.Fatal("acquire guessed pristine admission")
	}
	if generation, ok := sealAndBeginProducerSourceSlot(&slot, 0); ok || generation != 0 ||
		preemptLoad(&slot.state) != uint32(producerSourceFree) ||
		preemptLoad(&slot.inflight) != producerAdmissionClosed|1 {
		t.Fatalf("begin with guessed admission: generation=%d ok=%t slot=%+v", generation, ok, slot)
	}
	if !producerAdmissionReleaseChecked(&slot.inflight) || !producerSourceSlotReusable(&slot) {
		t.Fatalf("drain guessed pristine admission: slot=%+v", slot)
	}
	generation, ok := beginProducerSourceSlot(&slot)
	if !ok || generation != 1 || !activateProducerSourceSlot(&slot, generation) {
		t.Fatalf("reuse drained pristine slot: generation=%d ok=%t slot=%+v", generation, ok, slot)
	}
}

func TestProducerSourceSlotGenerationCannotAlias(t *testing.T) {
	var slot producerSourceSlot
	first, ok := beginProducerSourceSlot(&slot)
	if !ok || first != 1 || !resetProducerSourceSlot(&slot, first) ||
		!producerSourceSlotReusable(&slot) {
		t.Fatalf("reset first unpublished generation: generation=%d slot=%+v", first, slot)
	}
	second, ok := beginProducerSourceSlot(&slot)
	if !ok || second != 2 || !activateProducerSourceSlot(&slot, second) {
		t.Fatalf("activate second generation: generation=%d slot=%+v", second, slot)
	}
	if result := acquireProducerSourceGeneration(&slot, first); result != producerSourceAcquireStale ||
		preemptLoad(&slot.inflight) != 0 {
		t.Fatalf("stale generation = %d, inflight=%#x", result, preemptLoad(&slot.inflight))
	}
	if result := acquireProducerSourceGeneration(&slot, second); result != producerSourceAcquired {
		t.Fatalf("exact generation = %d", result)
	}
	if closeResult := beginProducerSourceClose(&slot); closeResult != producerSourceCloseStarted ||
		preemptLoad(&slot.inflight) != producerAdmissionClosed|1 || markProducerSourceQuiesced(&slot) {
		t.Fatalf("close with retained producer: result=%d slot=%+v", closeResult, slot)
	}
	if !producerAdmissionReleaseChecked(&slot.inflight) || !markProducerSourceQuiesced(&slot) ||
		!recycleProducerSourceSlot(&slot) || !producerSourceSlotReusable(&slot) {
		t.Fatalf("recycle exact generation: slot=%+v", slot)
	}
}

func TestProducerSourceCloseRaceJoinsExactGeneration(t *testing.T) {
	const producers = 64

	var slot producerSourceSlot
	generation, ok := beginProducerSourceSlot(&slot)
	if !ok || !activateProducerSourceSlot(&slot, generation) {
		t.Fatal("activate producer source slot")
	}
	start := make(chan struct{})
	accepted := make(chan bool, producers)
	release := make(chan struct{})
	done := make(chan struct{}, producers)
	for index := 0; index < producers; index++ {
		go func() {
			<-start
			entered := acquireProducerSourceGeneration(&slot, generation) == producerSourceAcquired
			accepted <- entered
			if entered {
				<-release
				producerAdmissionRelease(&slot.inflight)
			}
			done <- struct{}{}
		}()
	}
	close(start)
	if result := beginProducerSourceClose(&slot); result != producerSourceCloseStarted ||
		acquireProducerSourceGeneration(&slot, generation) != producerSourceAcquireClosed {
		t.Fatalf("close exact generation = %d", result)
	}
	acceptedCount := uint32(0)
	for index := 0; index < producers; index++ {
		if <-accepted {
			acceptedCount++
		}
	}
	if got := preemptLoad(&slot.inflight); got != producerAdmissionClosed|acceptedCount {
		t.Fatalf("sealed exact count = %#x, want %#x", got, producerAdmissionClosed|acceptedCount)
	}
	close(release)
	for index := 0; index < producers; index++ {
		<-done
	}
	if !markProducerSourceQuiesced(&slot) || !recycleProducerSourceSlot(&slot) {
		t.Fatalf("joined exact generation = %+v", slot)
	}
}

func TestRoutedProducerSourceLifecycle(t *testing.T) {
	var source routedProducerSource
	p := new(P)
	other := new(P)
	if !routedProducerHeaderEmpty(&source, nil) || validRoutedProducerSource(&source, p) ||
		!bindRoutedProducerSource(&source, p, RouteID(7)) ||
		!validRoutedProducerSource(&source, p) || validRoutedProducerSource(&source, other) {
		t.Fatalf("bind routed source = %+v", source)
	}
	preemptStore(&source.pending, 1)
	if !routedProducerPending(&source) || routedProducerHeaderEmpty(&source, p) ||
		!beginRoutedProducerPass(&source, p) || routedProducerPending(&source) ||
		!routedProducerHeaderEmpty(&source, p) {
		t.Fatalf("drain routed source hint = %+v", source)
	}
	if !unbindRoutedProducerSource(&source, p) || !routedProducerHeaderEmpty(&source, nil) {
		t.Fatalf("unbind routed source = %+v", source)
	}
	if bindRoutedProducerSource(&source, p, RouteID(8)) ||
		!bindRoutedProducerSource(&source, p, RouteID(7)) {
		t.Fatalf("route identity changed = %+v", source)
	}
	if route, ok := routedProducerRoute(&source); !ok || route != RouteID(7) {
		t.Fatalf("routed source route = (%d, %t)", route, ok)
	}
}
