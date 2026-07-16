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

// WaitRegistrationCapacity is the number of one-shot platform waits in one
// fixed registration table. Static-memory profiles may provision multiple
// stable tables; registration never allocates or silently overwrites a live
// slot when capacity is exhausted.
const WaitRegistrationCapacity = 64

// WaitRegistrationHandle is the complete producer-facing ABI. Slot is
// one-based so the all-zero value is invalid. Platform code may retain and
// return these two uint32 values, but never a P, G, WaitToken, Go pointer, or
// LLVM coroutine handle.
type WaitRegistrationHandle struct {
	Slot       uint32
	Generation uint32
}

// WaitRegistrationPostResult classifies a producer ingress attempt. Closed,
// stale, and duplicate callbacks are harmless no-ops and never wake a later
// generation.
type WaitRegistrationPostResult uint8

const (
	WaitRegistrationPostInvalid WaitRegistrationPostResult = iota
	WaitRegistrationPosted
	WaitRegistrationPostDuplicate
	WaitRegistrationPostClosed
	WaitRegistrationPostStale
)

// WaitRegistrationCloseResult classifies BeginClose. A completion that is
// still posting or waiting for scheduler drain must be drained before close is
// retried; cancellation never overwrites that winner.
type WaitRegistrationCloseResult uint8

const (
	WaitRegistrationCloseInvalid WaitRegistrationCloseResult = iota
	WaitRegistrationCloseStarted
	WaitRegistrationCompletionPending
	WaitRegistrationAlreadyClosing
	WaitRegistrationAlreadyQuiesced
)

type waitRegistrationState uint32

const (
	waitRegistrationFree waitRegistrationState = iota
	waitRegistrationInitializing
	waitRegistrationActive
	// Posting reserves the eventual producer payload for exactly one callback.
	// This first slice has no payload words yet, but keeping the state prevents
	// a future duplicate callback from writing before it knows it is the winner.
	waitRegistrationPosting
	waitRegistrationPosted
	waitRegistrationDraining
	waitRegistrationDelivered
	waitRegistrationClosingCancel
	waitRegistrationClosingDelivered
	waitRegistrationQuiescing
	waitRegistrationQuiescedCanceled
	waitRegistrationQuiescedDelivered
)

const (
	waitRegistrationProducerClosed = uint32(1 << 31)
	waitRegistrationProducerMask   = waitRegistrationProducerClosed - 1
)

type waitRegistrationSlot struct {
	// The producer-visible prefix contains only naturally aligned uint32 words.
	// All accesses to these fields are atomic.
	state      uint32
	generation uint32
	inflight   uint32

	// The scheduler-only suffix is published before Active and cleared before
	// Free. A producer never reads or writes any of these Go pointers.
	p      *P
	token  *WaitToken
	ticket WaitTicket
}

// WaitRegistrationTable is a fixed, allocation-free registry and one-shot
// mailbox set. It must live at a stable address until every platform backend
// has acknowledged unregister and every slot is retired; it must not be copied
// after first use. A target ingress shim resolves its stable executor/table ID
// and calls Post with only the POD handle supplied to the platform operation.
// The table pointer itself is not part of the platform ABI.
//
// Posted slots are the source of truth. pending is only a coalesced doorbell:
// pipe/eventfd saturation, an RTOS notification merge, or a redundant host
// callback cannot lose an already-posted registration.
//
// Post and Pending are the only producer-concurrent methods. Register, Drain,
// BeginClose, ConfirmQuiesced, Retire, and CanRelease belong to one scheduler
// owner and must be serialized with each other. A backend unregister
// acknowledgement is delivered to that owner; it never calls ConfirmQuiesced
// directly from a callback/ISR stack.
type WaitRegistrationTable struct {
	pending uint32
	slots   [WaitRegistrationCapacity]waitRegistrationSlot
	// owner is scheduler-only and is never read by Post. A non-nil owner binds
	// every future registration to one target-neutral single-P driver.
	owner *P
}

func registrationSlot(table *WaitRegistrationTable, handle WaitRegistrationHandle) (*waitRegistrationSlot, bool) {
	if table == nil || handle.Slot == 0 || handle.Slot > WaitRegistrationCapacity || handle.Generation == 0 {
		return nil, false
	}
	return &table.slots[handle.Slot-1], true
}

func registrationAcquireProducer(slot *waitRegistrationSlot) bool {
	if slot == nil {
		return false
	}
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&waitRegistrationProducerClosed != 0 || inflight&waitRegistrationProducerMask == waitRegistrationProducerMask {
			return false
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight+1) {
			return true
		}
	}
}

func registrationReleaseProducer(slot *waitRegistrationSlot) {
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&waitRegistrationProducerMask == 0 {
			return
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight-1) {
			return
		}
	}
}

// registrationSealProducers atomically closes admission while preserving the
// count of callbacks that entered first. An acquire CAS that was prepared from
// an open word either wins before this CAS and is included in the count, or
// loses to the closed bit and cannot enter afterward.
func registrationSealProducers(slot *waitRegistrationSlot) bool {
	if slot == nil {
		return false
	}
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&waitRegistrationProducerClosed != 0 {
			return true
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight|waitRegistrationProducerClosed) {
			return true
		}
	}
}

func registrationProducersQuiesced(slot *waitRegistrationSlot) bool {
	return slot != nil && preemptLoad(&slot.inflight) == waitRegistrationProducerClosed
}

// Register reserves one slot for an armed token. It is scheduler-thread-only
// and must run before the platform operation is submitted. Owner fields are
// initialized before the release publication of Active.
func (table *WaitRegistrationTable) Register(p *P, token *WaitToken, ticket WaitTicket) (WaitRegistrationHandle, bool) {
	if table == nil || p == nil || token == nil || !validWaitTicket(ticket) ||
		(table.owner != nil && table.owner != p) {
		return WaitRegistrationHandle{}, false
	}
	word := preemptLoad(&token.word)
	if waitGeneration(word) != uint32(ticket) || waitWordState(word) != waitArmed {
		return WaitRegistrationHandle{}, false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return WaitRegistrationHandle{}, false
	}
	for index := range table.slots {
		slot := &table.slots[index]
		if preemptLoad(&slot.state) != uint32(waitRegistrationFree) {
			continue
		}
		generation := preemptLoad(&slot.generation)
		if generation == ^uint32(0) {
			continue
		}
		inflight := preemptLoad(&slot.inflight)
		if (generation == 0 && inflight != 0) || (generation != 0 && inflight != waitRegistrationProducerClosed) ||
			!preemptCompareAndSwap(&slot.state, uint32(waitRegistrationFree), uint32(waitRegistrationInitializing)) {
			continue
		}
		if !registrationSealProducers(slot) || !registrationProducersQuiesced(slot) {
			// Initializing remains fail-closed if an invalid pre-registration
			// producer raced the first use of a zero-value slot.
			continue
		}
		generation++
		if generation == 0 {
			return WaitRegistrationHandle{}, false
		}
		slot.p = p
		slot.token = token
		slot.ticket = ticket
		preemptStore(&slot.generation, generation)
		if !preemptCompareAndSwap(&slot.inflight, waitRegistrationProducerClosed, 0) {
			// Initializing is a permanent fail-closed state if the sealed
			// admission word was corrupted by an out-of-contract owner.
			return WaitRegistrationHandle{}, false
		}
		preemptStore(&slot.state, uint32(waitRegistrationActive))
		return WaitRegistrationHandle{Slot: uint32(index) + 1, Generation: generation}, true
	}
	return WaitRegistrationHandle{}, false
}

// Post is the producer ingress leaf. It only touches the producer-visible
// atomic prefix and the table doorbell: it does not call CompleteWait,
// RequestSchedule, allocate, mutate scheduler queues, or inspect an LLVM
// handle. The scheduler later resolves the slot through Drain.
func (table *WaitRegistrationTable) Post(handle WaitRegistrationHandle) WaitRegistrationPostResult {
	slot, ok := registrationSlot(table, handle)
	if !ok {
		return WaitRegistrationPostInvalid
	}
	if !registrationAcquireProducer(slot) {
		return WaitRegistrationPostClosed
	}
	if preemptLoad(&slot.generation) != handle.Generation {
		registrationReleaseProducer(slot)
		return WaitRegistrationPostStale
	}
	for {
		state := waitRegistrationState(preemptLoad(&slot.state))
		switch state {
		case waitRegistrationActive:
			if !preemptCompareAndSwap(&slot.state, uint32(state), uint32(waitRegistrationPosting)) {
				continue
			}
			// A future result payload is written here by the unique Posting
			// owner, before Posted publishes it to the scheduler.
			preemptStore(&slot.state, uint32(waitRegistrationPosted))
			preemptStore(&table.pending, 1)
			registrationReleaseProducer(slot)
			return WaitRegistrationPosted
		case waitRegistrationPosting, waitRegistrationPosted, waitRegistrationDraining, waitRegistrationDelivered:
			registrationReleaseProducer(slot)
			return WaitRegistrationPostDuplicate
		case waitRegistrationClosingCancel, waitRegistrationClosingDelivered, waitRegistrationQuiescing,
			waitRegistrationQuiescedCanceled, waitRegistrationQuiescedDelivered, waitRegistrationInitializing,
			waitRegistrationFree:
			registrationReleaseProducer(slot)
			return WaitRegistrationPostClosed
		default:
			registrationReleaseProducer(slot)
			return WaitRegistrationPostInvalid
		}
	}
}

// Pending reports the coalesced registration doorbell. Platform wake state is
// advisory; Drain always scans the bounded slot table for source-of-truth
// Posted states.
func (table *WaitRegistrationTable) Pending() bool {
	return table != nil && preemptLoad(&table.pending) != 0
}

// Drain publishes every posted completion into its WaitToken. It is
// scheduler-thread-only. A standalone table may use Drain directly; a table
// bound to an ExecutorDriver must be serviced by that driver so completion
// publication, executor acknowledgement, and the mandatory source recheck stay
// one scheduler-owned transaction.
func (table *WaitRegistrationTable) Drain() (int, bool) {
	if table == nil || table.owner != nil {
		return 0, false
	}
	return table.drain(nil, false)
}

func (table *WaitRegistrationTable) drainFor(p *P) (int, bool) {
	if table == nil || p == nil || table.owner != p {
		return 0, false
	}
	return table.drain(p, true)
}

func (table *WaitRegistrationTable) drain(owner *P, enforceOwner bool) (int, bool) {
	preemptStore(&table.pending, 0)
	drained := 0
	for index := range table.slots {
		slot := &table.slots[index]
		if waitRegistrationState(preemptLoad(&slot.state)) != waitRegistrationPosted {
			continue
		}
		if !preemptCompareAndSwap(&slot.state, uint32(waitRegistrationPosted), uint32(waitRegistrationDraining)) {
			continue
		}
		p, token, ticket := slot.p, slot.token, slot.ticket
		if p == nil || (enforceOwner && p != owner) || token == nil || !validWaitTicket(ticket) || !CompleteWait(token, ticket) {
			// Keep Draining permanently fail-closed: owner storage cannot be
			// retired after a corrupt or competing raw token transition.
			return drained, false
		}
		preemptStore(&slot.state, uint32(waitRegistrationDelivered))
		drained++
	}
	return drained, true
}

// BeginClose closes producer admission for one registration. The scheduler
// owner must then physically unregister/cancel the platform operation. If a
// post already won, the scheduler must Drain it and retry BeginClose;
// cancellation cannot replace an admitted completion.
func (table *WaitRegistrationTable) BeginClose(handle WaitRegistrationHandle) WaitRegistrationCloseResult {
	slot, ok := registrationSlot(table, handle)
	if !ok || preemptLoad(&slot.generation) != handle.Generation {
		return WaitRegistrationCloseInvalid
	}
	for {
		state := waitRegistrationState(preemptLoad(&slot.state))
		switch state {
		case waitRegistrationActive:
			if preemptCompareAndSwap(&slot.state, uint32(state), uint32(waitRegistrationClosingCancel)) {
				if !registrationSealProducers(slot) {
					return WaitRegistrationCloseInvalid
				}
				return WaitRegistrationCloseStarted
			}
		case waitRegistrationPosting, waitRegistrationPosted, waitRegistrationDraining:
			return WaitRegistrationCompletionPending
		case waitRegistrationDelivered:
			if preemptCompareAndSwap(&slot.state, uint32(state), uint32(waitRegistrationClosingDelivered)) {
				if !registrationSealProducers(slot) {
					return WaitRegistrationCloseInvalid
				}
				return WaitRegistrationCloseStarted
			}
		case waitRegistrationClosingCancel, waitRegistrationClosingDelivered, waitRegistrationQuiescing:
			return WaitRegistrationAlreadyClosing
		case waitRegistrationQuiescedCanceled, waitRegistrationQuiescedDelivered:
			return WaitRegistrationAlreadyQuiesced
		default:
			return WaitRegistrationCloseInvalid
		}
	}
}

// ConfirmQuiesced records the platform backend's strong unregister
// acknowledgement. Calling it is the external contract that no new Post call
// can start and every callback that had already entered Post has returned,
// including one paused before it acquired a slot lease. The closed admission
// word additionally verifies that every admitted Post call released its lease.
// Only then may logical cancellation be published and the G be resumed, so a
// losing completion producer cannot still access the table or frame storage.
func (table *WaitRegistrationTable) ConfirmQuiesced(handle WaitRegistrationHandle) (WaitCancelResult, bool) {
	slot, ok := registrationSlot(table, handle)
	if !ok || preemptLoad(&slot.generation) != handle.Generation || !registrationProducersQuiesced(slot) ||
		slot.p == nil || (table.owner != nil && table.owner != slot.p) {
		return WaitCancelInvalid, false
	}
	state := waitRegistrationState(preemptLoad(&slot.state))
	if state != waitRegistrationClosingCancel && state != waitRegistrationClosingDelivered {
		return WaitCancelInvalid, false
	}
	if !preemptCompareAndSwap(&slot.state, uint32(state), uint32(waitRegistrationQuiescing)) {
		return WaitCancelInvalid, false
	}
	if state == waitRegistrationClosingDelivered {
		preemptStore(&slot.state, uint32(waitRegistrationQuiescedDelivered))
		return WaitCancelCompletionWon, true
	}
	result := publishWaitCancellation(slot.token, slot.ticket)
	finalState := waitRegistrationState(0)
	switch result {
	case WaitCancelWon, WaitCancelAlreadyCanceled:
		finalState = waitRegistrationQuiescedCanceled
	case WaitCancelCompletionWon:
		// A direct token producer is outside the registration contract, but
		// retaining the known outcome is safer than making storage reusable.
		finalState = waitRegistrationQuiescedDelivered
	default:
		// Quiescing is deliberately unrecoverable without owner diagnosis.
		return result, false
	}
	// Publish Quiesced only after the last slot-owner access. A concurrent
	// scheduler may consume the token earlier, but Retire must keep failing on
	// Quiescing until this call no longer reads token/ticket.
	preemptStore(&slot.state, uint32(finalState))
	return result, true
}

// Retire releases scheduler ownership only after physical quiescence and
// scheduler consumption of the matching logical outcome. It clears every Go
// pointer before publishing Free; a later registration receives a new
// generation, and stale handles remain harmless.
func (table *WaitRegistrationTable) Retire(handle WaitRegistrationHandle) bool {
	slot, ok := registrationSlot(table, handle)
	if !ok || preemptLoad(&slot.generation) != handle.Generation || !registrationProducersQuiesced(slot) {
		return false
	}
	state := waitRegistrationState(preemptLoad(&slot.state))
	want := WaitOutcomeInvalid
	switch state {
	case waitRegistrationQuiescedCanceled:
		want = WaitOutcomeCanceled
	case waitRegistrationQuiescedDelivered:
		want = WaitOutcomeCompleted
	default:
		return false
	}
	outcome, consumed := consumedWait(slot.token, slot.ticket)
	if !consumed || outcome != want {
		return false
	}
	slot.p = nil
	slot.token = nil
	slot.ticket = 0
	preemptStore(&slot.state, uint32(waitRegistrationFree))
	return true
}

// CanRelease reports whether an unbound table has no live registration or
// producer. A table attached to ExecutorDriver remains non-releasable even when
// its slot set is empty. The owner may use this after its platform backend has
// been shut down; it must not race control methods. A false result requires
// retaining the table at its stable address.
func registrationTableEmpty(table *WaitRegistrationTable, owner *P) bool {
	if table == nil || table.owner != owner || preemptLoad(&table.pending) != 0 {
		return false
	}
	for index := range table.slots {
		slot := &table.slots[index]
		inflight := preemptLoad(&slot.inflight)
		generation := preemptLoad(&slot.generation)
		if preemptLoad(&slot.state) != uint32(waitRegistrationFree) ||
			(generation == 0 && inflight != 0) || (generation != 0 && inflight != waitRegistrationProducerClosed) ||
			slot.p != nil || slot.token != nil || slot.ticket != 0 {
			return false
		}
	}
	return true
}

func bindRegistrationTable(table *WaitRegistrationTable, p *P) bool {
	if p == nil || !registrationTableEmpty(table, nil) {
		return false
	}
	table.owner = p
	return true
}

func unbindRegistrationTable(table *WaitRegistrationTable, p *P) bool {
	if p == nil || !registrationTableEmpty(table, p) {
		return false
	}
	table.owner = nil
	return true
}

func (table *WaitRegistrationTable) CanRelease() bool {
	return registrationTableEmpty(table, nil)
}
