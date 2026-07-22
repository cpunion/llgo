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

// WaitRegistrationPageCapacity is the allocation-free granularity of the
// common producer-facing wait registry. Every table includes one inline page;
// a target may attach more stable pages before binding it.
const WaitRegistrationPageCapacity = 64

// WaitRegistrationCapacity is the default capacity of an unconfigured table.
// It remains the fixed capacity of small/embedded profiles which do not attach
// extra pages.
const WaitRegistrationCapacity = WaitRegistrationPageCapacity

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

// WaitRegistrationPrepareResult distinguishes ordinary caller rejection from
// an impossible rollback failure that poisons token ownership and requires the
// runtime adapter to fail-stop.
type WaitRegistrationPrepareResult uint8

const (
	WaitRegistrationPrepareInvalid WaitRegistrationPrepareResult = iota
	WaitRegistrationPrepared
	WaitRegistrationPrepareRejected
	WaitRegistrationPreparePoisoned
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

type waitRegistrationSlot struct {
	// The producer-visible prefix contains only naturally aligned uint32 words.
	// All accesses to these fields are atomic. WaitRegistration fuses mailbox
	// and lifecycle states after Active, so it must not use the common
	// close/quiesce/recycle helpers: its Posting and Posted values overlap the
	// common Closing and Quiesced values.
	producerSourceSlot

	// The scheduler-only suffix is published before Active and cleared before
	// Free. A producer never reads or writes any of these Go pointers.
	p      *P
	token  *WaitToken
	ticket WaitTicket
}

// WaitRegistrationPage is stable target-provided storage. Its atomic slot
// prefix remains producer-visible only through the existing two-word handle;
// neither this page pointer nor its scheduler suffix crosses an ingress ABI.
type WaitRegistrationPage struct {
	slots [WaitRegistrationPageCapacity]waitRegistrationSlot
}

// WaitRegistrationTable is a paged, allocation-free registry and one-shot
// mailbox set. Its configured pages are frozen while bound. It must live at a
// stable address until every platform backend
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
	pending    uint32
	scanLimit  uint32
	slots      [WaitRegistrationPageCapacity]waitRegistrationSlot
	extraPages []WaitRegistrationPage
	// owner is scheduler-only and is never read by Post. A non-nil owner binds
	// every future registration to one target-neutral single-P driver.
	owner *P
}

// WaitRegistrationConfiguredCapacity returns the exact linear handle and scan
// capacity. Configuration is capped to the common 15-bit source-local domain,
// which also keeps ExecutorDriver's uint16 progress cursor exact.
func WaitRegistrationConfiguredCapacity(table *WaitRegistrationTable) uint32 {
	if table == nil {
		return 0
	}
	return uint32(1+len(table.extraPages)) * WaitRegistrationPageCapacity
}

func waitRegistrationScanLimit(table *WaitRegistrationTable) (uint32, bool) {
	if table == nil {
		return 0, false
	}
	capacity := WaitRegistrationConfiguredCapacity(table)
	return table.scanLimit, validSourceScanLimit(table.scanLimit, capacity)
}

func waitRegistrationSlotAt(table *WaitRegistrationTable, index uint32) (*waitRegistrationSlot, bool) {
	if index >= WaitRegistrationConfiguredCapacity(table) {
		return nil, false
	}
	if index < WaitRegistrationPageCapacity {
		return &table.slots[index], true
	}
	page := index/WaitRegistrationPageCapacity - 1
	offset := index % WaitRegistrationPageCapacity
	return &table.extraPages[page].slots[offset], true
}

func reusableWaitRegistrationSlot(slot *waitRegistrationSlot) bool {
	return slot != nil && producerSourceSlotReusable(&slot.producerSourceSlot) &&
		slot.p == nil && slot.token == nil && slot.ticket == 0
}

// ConfigureWaitRegistrationPages attaches stable pages while the table is
// empty and unbound. Repeating an identical configuration is harmless; an
// empty table may monotonically expose more of the same backing pool. Post may
// safely read the frozen slice header concurrently after bind.
func ConfigureWaitRegistrationPages(table *WaitRegistrationTable, pages []WaitRegistrationPage) bool {
	if table == nil || table.owner != nil || preemptLoad(&table.pending) != 0 ||
		len(pages) > int(operationLocalMask/WaitRegistrationPageCapacity)-1 {
		return false
	}
	existing := len(table.extraPages)
	if existing != 0 && (len(pages) < existing || len(pages) == 0 || &table.extraPages[0] != &pages[0]) {
		return false
	}
	if len(pages) == existing {
		return true
	}
	for index := uint32(0); index < WaitRegistrationConfiguredCapacity(table); index++ {
		slot, ok := waitRegistrationSlotAt(table, index)
		if !ok || !reusableWaitRegistrationSlot(slot) {
			return false
		}
	}
	for page := existing; page < len(pages); page++ {
		for offset := range pages[page].slots {
			if !reusableWaitRegistrationSlot(&pages[page].slots[offset]) {
				return false
			}
		}
	}
	table.extraPages = pages
	return true
}

// PrepareWaitRegistration arms token and publishes the matching stable table
// slot as one owner-side transaction. If registration fails, it consumes that
// unpublished ticket as a cancellation while preserving its generation, so
// token remains reusable and no stale ticket can be accepted later.
//
// The returned ticket and handle may be copied into a platform operation only
// when the result is WaitRegistrationPrepared. If submission then fails before
// the operation can start a callback, the owner must call
// RollbackPreparedWait after proving that source quiesced.
func PrepareWaitRegistration(p *P, table *WaitRegistrationTable, token *WaitToken) (WaitTicket, WaitRegistrationHandle, WaitRegistrationPrepareResult) {
	ticket, ok := ArmWait(token)
	if !ok {
		return 0, WaitRegistrationHandle{}, WaitRegistrationPrepareInvalid
	}
	handle, ok := table.Register(p, token, ticket)
	if !ok {
		if !rollbackArmedWait(token, ticket) {
			return 0, WaitRegistrationHandle{}, WaitRegistrationPreparePoisoned
		}
		return 0, WaitRegistrationHandle{}, WaitRegistrationPrepareRejected
	}
	return ticket, handle, WaitRegistrationPrepared
}

func registrationSlot(table *WaitRegistrationTable, handle WaitRegistrationHandle) (*waitRegistrationSlot, bool) {
	if table == nil || handle.Slot == 0 || handle.Generation == 0 {
		return nil, false
	}
	return waitRegistrationSlotAt(table, handle.Slot-1)
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
	for index := uint32(0); index < WaitRegistrationConfiguredCapacity(table); index++ {
		slot, slotOK := waitRegistrationSlotAt(table, index)
		if !slotOK {
			return WaitRegistrationHandle{}, false
		}
		if !producerSourceSlotReusable(&slot.producerSourceSlot) || preemptLoad(&slot.generation) == ^uint32(0) {
			continue
		}
		generation, begun := beginProducerSourceSlot(&slot.producerSourceSlot)
		if !begun {
			continue
		}
		if !raiseSourceScanLimit(&table.scanLimit, index, WaitRegistrationConfiguredCapacity(table)) {
			return WaitRegistrationHandle{}, false
		}
		slot.p = p
		slot.token = token
		slot.ticket = ticket
		if !activateProducerSourceSlot(&slot.producerSourceSlot, generation) {
			// The slot remains non-reusable and fail-closed if an out-of-contract
			// owner corrupted either the lifecycle or sealed admission word.
			return WaitRegistrationHandle{}, false
		}
		return WaitRegistrationHandle{Slot: index + 1, Generation: generation}, true
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
	switch acquireProducerSourceGeneration(&slot.producerSourceSlot, handle.Generation) {
	case producerSourceAcquireClosed:
		return WaitRegistrationPostClosed
	case producerSourceAcquireStale:
		return WaitRegistrationPostStale
	case producerSourceAcquired:
	default:
		return WaitRegistrationPostInvalid
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
			producerAdmissionRelease(&slot.inflight)
			return WaitRegistrationPosted
		case waitRegistrationPosting, waitRegistrationPosted, waitRegistrationDraining, waitRegistrationDelivered:
			producerAdmissionRelease(&slot.inflight)
			return WaitRegistrationPostDuplicate
		case waitRegistrationClosingCancel, waitRegistrationClosingDelivered, waitRegistrationQuiescing,
			waitRegistrationQuiescedCanceled, waitRegistrationQuiescedDelivered, waitRegistrationInitializing,
			waitRegistrationFree:
			producerAdmissionRelease(&slot.inflight)
			return WaitRegistrationPostClosed
		default:
			producerAdmissionRelease(&slot.inflight)
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
// bound into an ExecutorSourceSet must be serviced by its ExecutorDriver so
// completion publication, executor acknowledgement, and the mandatory source
// recheck stay one scheduler-owned transaction.
func (table *WaitRegistrationTable) Drain() (int, bool) {
	if table == nil || table.owner != nil {
		return 0, false
	}
	return table.drain(nil)
}

func (table *WaitRegistrationTable) drainFor(p *P) (int, bool) {
	if table == nil || p == nil || table.owner != p {
		return 0, false
	}
	return table.drain(p)
}

// beginDrainPass clears only the coalesced producer hint. Posted slot states
// remain the source of truth, so a bounded ExecutorSourceSet pass may visit one
// slot at a time across several host entries without losing a callback which
// races either side of this store.
func (table *WaitRegistrationTable) beginDrainPass(owner *P) bool {
	if table == nil || table.owner != owner {
		return false
	}
	preemptStore(&table.pending, 0)
	return true
}

// drainSlot publishes at most one exact physical slot. index is an owner-side
// cursor, never a producer ABI. Keeping this operation O(1) lets the common
// executor charge every real catalog entry to its reduction budget.
func (table *WaitRegistrationTable) drainSlot(owner *P, index uint32) (int, bool) {
	if table == nil || table.owner != owner || index >= WaitRegistrationConfiguredCapacity(table) {
		return 0, false
	}
	slot, slotOK := waitRegistrationSlotAt(table, index)
	if !slotOK {
		return 0, false
	}
	if waitRegistrationState(preemptLoad(&slot.state)) != waitRegistrationPosted {
		return 0, true
	}
	if !preemptCompareAndSwap(&slot.state, uint32(waitRegistrationPosted), uint32(waitRegistrationDraining)) {
		// Only the serialized owner performs Posted -> Draining. A failed CAS
		// after observing Posted is therefore a second owner or corruption, not
		// a benign producer race; fail closed instead of silently skipping it.
		return 0, false
	}
	p, token, ticket := slot.p, slot.token, slot.ticket
	if p == nil || (owner != nil && p != owner) || token == nil || !validWaitTicket(ticket) || !CompleteWait(token, ticket) {
		// Keep Draining permanently fail-closed: owner storage cannot be
		// retired after a corrupt or competing raw token transition.
		return 0, false
	}
	preemptStore(&slot.state, uint32(waitRegistrationDelivered))
	return 1, true
}

func (table *WaitRegistrationTable) drain(owner *P) (int, bool) {
	if !table.beginDrainPass(owner) {
		return 0, false
	}
	limit, valid := waitRegistrationScanLimit(table)
	if !valid {
		return 0, false
	}
	drained := 0
	for index := uint32(0); index < limit; index++ {
		one, ok := table.drainSlot(owner, index)
		drained += one
		if !ok {
			return drained, false
		}
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
				if !producerAdmissionSeal(&slot.inflight) {
					return WaitRegistrationCloseInvalid
				}
				return WaitRegistrationCloseStarted
			}
		case waitRegistrationPosting, waitRegistrationPosted, waitRegistrationDraining:
			return WaitRegistrationCompletionPending
		case waitRegistrationDelivered:
			if preemptCompareAndSwap(&slot.state, uint32(state), uint32(waitRegistrationClosingDelivered)) {
				if !producerAdmissionSeal(&slot.inflight) {
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
	if !ok || preemptLoad(&slot.generation) != handle.Generation ||
		!producerSourceSlotQuiesced(&slot.producerSourceSlot) ||
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
	if !ok || preemptLoad(&slot.generation) != handle.Generation ||
		!producerSourceSlotQuiesced(&slot.producerSourceSlot) {
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

// RollbackPreparedWait releases a registration whose external submission
// failed before any callback could start and before a G claimed the ticket.
// The caller supplies that strong quiescence guarantee; this method closes the
// slot admission gate, publishes and consumes cancellation, then retires the
// exact generation. It fails closed once a post or park has won.
func (table *WaitRegistrationTable) RollbackPreparedWait(handle WaitRegistrationHandle, token *WaitToken, ticket WaitTicket) bool {
	slot, ok := registrationSlot(table, handle)
	if !ok || token == nil || !validWaitTicket(ticket) ||
		preemptLoad(&slot.generation) != handle.Generation || slot.token != token || slot.ticket != ticket ||
		table.BeginClose(handle) != WaitRegistrationCloseStarted {
		return false
	}
	result, quiesced := table.ConfirmQuiesced(handle)
	if !quiesced || result != WaitCancelWon || !consumeUnclaimedCanceledWait(token, ticket) {
		return false
	}
	return table.Retire(handle)
}

// RetireCompletedWait releases an exact delivered registration after the
// resumed owner has strongly joined/unregistered its external source. The
// matching completion must already have been consumed by the scheduler.
func (table *WaitRegistrationTable) RetireCompletedWait(handle WaitRegistrationHandle, token *WaitToken, ticket WaitTicket) bool {
	slot, ok := registrationSlot(table, handle)
	if !ok || token == nil || !validWaitTicket(ticket) ||
		preemptLoad(&slot.generation) != handle.Generation || slot.token != token || slot.ticket != ticket ||
		table.BeginClose(handle) != WaitRegistrationCloseStarted {
		return false
	}
	result, quiesced := table.ConfirmQuiesced(handle)
	if !quiesced || result != WaitCancelCompletionWon {
		return false
	}
	return table.Retire(handle)
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
	for index := uint32(0); index < WaitRegistrationConfiguredCapacity(table); index++ {
		slot, slotOK := waitRegistrationSlotAt(table, index)
		if !slotOK || !reusableWaitRegistrationSlot(slot) {
			return false
		}
	}
	return true
}

func bindRegistrationTable(table *WaitRegistrationTable, p *P) bool {
	if p == nil || !registrationTableEmpty(table, nil) {
		return false
	}
	table.scanLimit = 0
	table.owner = p
	return true
}

func unbindRegistrationTable(table *WaitRegistrationTable, p *P) bool {
	if p == nil || !registrationTableEmpty(table, p) {
		return false
	}
	table.owner = nil
	table.scanLimit = 0
	return true
}

func (table *WaitRegistrationTable) CanRelease() bool {
	return registrationTableEmpty(table, nil)
}
