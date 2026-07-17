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

import "unsafe"

// producerSourceSlot is the common producer-visible POD prefix for a stable
// source slot. Source-specific mailboxes, payload words, physical state, and
// owner pointers follow this prefix and remain under their concrete direct
// dispatcher. The prefix must be embedded as the first field of a source slot.
// A source with a fused mailbox/state word may reuse the prefix, generation,
// and admission helpers without adopting the full common lifecycle after
// Active; only sources whose complete state machine matches these five values
// may use the common close/quiesce/recycle helpers.
type producerSourceSlot struct {
	state      uint32
	generation uint32
	inflight   uint32
}

var (
	_ [12 - unsafe.Sizeof(producerSourceSlot{})]byte
	_ [unsafe.Sizeof(producerSourceSlot{}) - 12]byte
	_ [4 - unsafe.Alignof(producerSourceSlot{})]byte
	_ [unsafe.Alignof(producerSourceSlot{}) - 4]byte
)

type producerSourceLifecycle uint32

const (
	producerSourceFree producerSourceLifecycle = iota
	producerSourceInitializing
	producerSourceActive
	producerSourceClosing
	producerSourceQuiesced
)

type producerSourceAcquireResult uint8

const (
	producerSourceAcquireInvalid producerSourceAcquireResult = iota
	producerSourceAcquired
	producerSourceAcquireStale
	producerSourceAcquireClosed
)

type producerSourceCloseResult uint8

const (
	producerSourceCloseInvalid producerSourceCloseResult = iota
	producerSourceCloseStarted
	producerSourceAlreadyClosing
	producerSourceAlreadyQuiesced
)

// producerSourceSlotReusable validates only the shared atomic header. A source
// must additionally prove that its mailbox, record, payload, and owner suffix
// are in their own canonical reusable state.
func producerSourceSlotReusable(slot *producerSourceSlot) bool {
	if slot == nil || preemptLoad(&slot.state) != uint32(producerSourceFree) {
		return false
	}
	generation := preemptLoad(&slot.generation)
	inflight := preemptLoad(&slot.inflight)
	return generation == 0 && (inflight == 0 || inflight == producerAdmissionClosed) ||
		generation != 0 && inflight == producerAdmissionClosed
}

// beginProducerSourceSlot first seals pristine admission, then reserves and
// advances one reusable physical generation. Sealing before the state CAS lets
// a guessed pre-publication producer drain without either blocking the owner or
// leaking Initializing: the slot remains Free and becomes reusable once the
// aggregate admission count reaches closed-with-zero-inflight. Once
// Initializing is published, any later invariant failure is deliberately
// fail-closed; the caller may restore Free only after consuming a
// source-specific unpublished reservation with resetProducerSourceSlot.
func beginProducerSourceSlot(slot *producerSourceSlot) (uint32, bool) {
	if !producerSourceSlotReusable(slot) {
		return 0, false
	}
	previous := preemptLoad(&slot.generation)
	return sealAndBeginProducerSourceSlot(slot, previous)
}

// sealAndBeginProducerSourceSlot is split out so the reusable-check-to-seal
// race has a deterministic test. The caller has observed a canonical Free
// header with this previous generation; this helper must still tolerate a
// guessed producer entering immediately after that observation.
func sealAndBeginProducerSourceSlot(slot *producerSourceSlot, previous uint32) (uint32, bool) {
	if previous == ^uint32(0) || !producerAdmissionSeal(&slot.inflight) ||
		!producerAdmissionQuiesced(&slot.inflight) ||
		!preemptCompareAndSwap(&slot.state, uint32(producerSourceFree), uint32(producerSourceInitializing)) {
		return 0, false
	}
	generation := previous + 1
	preemptStore(&slot.generation, generation)
	return generation, true
}

// activateProducerSourceSlot release-publishes an already initialized suffix,
// then opens exact-generation producer admission. No legitimate producer can
// know the new generation before the caller returns it.
func activateProducerSourceSlot(slot *producerSourceSlot, generation uint32) bool {
	if slot == nil || generation == 0 || preemptLoad(&slot.generation) != generation ||
		preemptLoad(&slot.inflight) != producerAdmissionClosed ||
		!preemptCompareAndSwap(&slot.state, uint32(producerSourceInitializing), uint32(producerSourceActive)) {
		return false
	}
	return producerAdmissionReopen(&slot.inflight)
}

// resetProducerSourceSlot is the pre-publication rollback boundary. The source
// must first consume any OperationRecord reservation or other suffix identity;
// the advanced generation remains authoritative and cannot be reused.
func resetProducerSourceSlot(slot *producerSourceSlot, generation uint32) bool {
	return slot != nil && generation != 0 && preemptLoad(&slot.generation) == generation &&
		producerAdmissionQuiesced(&slot.inflight) &&
		preemptCompareAndSwap(&slot.state, uint32(producerSourceInitializing), uint32(producerSourceFree))
}

// acquireProducerSourceGeneration joins the stable slot before validating its
// generation. Success retains one admission which the concrete source must
// release after its mailbox transaction; stale attempts are released here.
func acquireProducerSourceGeneration(slot *producerSourceSlot, generation uint32) producerSourceAcquireResult {
	if slot == nil || generation == 0 {
		return producerSourceAcquireInvalid
	}
	if !producerAdmissionAcquire(&slot.inflight) {
		return producerSourceAcquireClosed
	}
	if preemptLoad(&slot.generation) != generation {
		if !producerAdmissionReleaseChecked(&slot.inflight) {
			return producerSourceAcquireInvalid
		}
		return producerSourceAcquireStale
	}
	return producerSourceAcquired
}

func beginProducerSourceClose(slot *producerSourceSlot) producerSourceCloseResult {
	if slot == nil {
		return producerSourceCloseInvalid
	}
	for {
		switch state := producerSourceLifecycle(preemptLoad(&slot.state)); state {
		case producerSourceActive:
			if !preemptCompareAndSwap(&slot.state, uint32(state), uint32(producerSourceClosing)) {
				continue
			}
			if !producerAdmissionSeal(&slot.inflight) {
				return producerSourceCloseInvalid
			}
			return producerSourceCloseStarted
		case producerSourceClosing:
			return producerSourceAlreadyClosing
		case producerSourceQuiesced:
			return producerSourceAlreadyQuiesced
		default:
			return producerSourceCloseInvalid
		}
	}
}

func producerSourceSlotQuiesced(slot *producerSourceSlot) bool {
	return slot != nil && producerAdmissionQuiesced(&slot.inflight)
}

// markProducerSourceQuiesced and recycleProducerSourceSlot are terminal header
// gates. Concrete sources clear every owner pointer/result/mailbox prerequisite
// before calling them; neither helper performs source-specific cleanup.
func markProducerSourceQuiesced(slot *producerSourceSlot) bool {
	return producerSourceSlotQuiesced(slot) &&
		preemptCompareAndSwap(&slot.state, uint32(producerSourceClosing), uint32(producerSourceQuiesced))
}

func recycleProducerSourceSlot(slot *producerSourceSlot) bool {
	return producerSourceSlotQuiesced(slot) &&
		preemptCompareAndSwap(&slot.state, uint32(producerSourceQuiesced), uint32(producerSourceFree))
}

// routedProducerSource is the scheduler-owned binding plus the producer's
// coalesced hint. It contains no dispatcher, interface, function value, or
// source-specific state. Durable work always remains in a concrete mailbox.
type routedProducerSource struct {
	pending uint32
	owner   *P
	route   RouteID
}

func validRoutedProducerSource(source *routedProducerSource, p *P) bool {
	return source != nil && p != nil && source.owner == p && source.route.Valid()
}

func beginRoutedProducerPass(source *routedProducerSource, p *P) bool {
	if !validRoutedProducerSource(source, p) {
		return false
	}
	preemptStore(&source.pending, 0)
	return true
}

func routedProducerPending(source *routedProducerSource) bool {
	return source != nil && preemptLoad(&source.pending) != 0
}

func routedProducerHeaderEmpty(source *routedProducerSource, owner *P) bool {
	return source != nil && source.owner == owner && preemptLoad(&source.pending) == 0
}

// bindRoutedProducerSource mutates only the common binding. The caller must
// first validate every concrete slot against this candidate route.
func bindRoutedProducerSource(source *routedProducerSource, p *P, route RouteID) bool {
	if source == nil || p == nil || !route.Valid() || source.owner != nil || preemptLoad(&source.pending) != 0 ||
		source.route != 0 && source.route != route {
		return false
	}
	source.route = route
	source.owner = p
	return true
}

func unbindRoutedProducerSource(source *routedProducerSource, p *P) bool {
	if !validRoutedProducerSource(source, p) || preemptLoad(&source.pending) != 0 {
		return false
	}
	source.owner = nil
	return true
}

func routedProducerRoute(source *routedProducerSource) (RouteID, bool) {
	if source == nil || !source.route.Valid() {
		return 0, false
	}
	return source.route, true
}
