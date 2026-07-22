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

// KeyedWaitPageCapacity matches the common wait page exactly: every parked
// keyed operation owns one catalog slot and one WaitRegistrationHandle.
const KeyedWaitPageCapacity = WaitRegistrationPageCapacity

// SemaphoreWaitPageCapacity is retained as the source-compatible name for the
// semaphore specialization of the shared keyed-wait catalog.
const SemaphoreWaitPageCapacity = KeyedWaitPageCapacity

// KeyedWaitCapacity is the default capacity of an unconfigured catalog.
const KeyedWaitCapacity = KeyedWaitPageCapacity

// SemaphoreWaitCapacity is the source-compatible specialization name.
const SemaphoreWaitCapacity = SemaphoreWaitPageCapacity

// KeyedWaitNamespace makes equality keys from unrelated runtime facilities
// disjoint even when their uintptr spellings happen to be equal. Zero is never
// a valid namespace.
type KeyedWaitNamespace uint8

const (
	KeyedWaitNamespaceInvalid KeyedWaitNamespace = iota
	KeyedWaitNamespaceSemaphore
	KeyedWaitNamespaceNotifyList
)

// KeyedWaitHandle is private scheduler identity. It is returned to the
// retained coroutine frame as two POD words, but is never exposed to a target
// callback or a notifier.
type KeyedWaitHandle struct {
	Slot       uint32
	Generation uint32
}

type SemaphoreWaitHandle = KeyedWaitHandle

// KeyedWaitPrepareResult distinguishes capacity/admission rejection from
// a failed rollback which leaves ownership poisoned and requires fail-stop.
type KeyedWaitPrepareResult uint8

const (
	KeyedWaitPrepareInvalid KeyedWaitPrepareResult = iota
	KeyedWaitPrepared
	KeyedWaitPrepareRejected
	KeyedWaitPreparePoisoned
)

type SemaphoreWaitPrepareResult = KeyedWaitPrepareResult

const (
	SemaphoreWaitPrepareInvalid  = KeyedWaitPrepareInvalid
	SemaphoreWaitPrepared        = KeyedWaitPrepared
	SemaphoreWaitPrepareRejected = KeyedWaitPrepareRejected
	SemaphoreWaitPreparePoisoned = KeyedWaitPreparePoisoned
)

// KeyedWaitPostResult is the complete result of one owner-side notification.
// NoWaiter is ordinary: a semaphore count or notify counter is sufficient for
// a later prepare-side recheck. Posted means the common wait table durably owns
// the completion and the target must request the executor before returning to
// managed code.
type KeyedWaitPostResult uint8

const (
	KeyedWaitPostInvalid KeyedWaitPostResult = iota
	KeyedWaitNoWaiter
	KeyedWaitPosted
)

type SemaphoreWaitPostResult = KeyedWaitPostResult

const (
	SemaphoreWaitPostInvalid = KeyedWaitPostInvalid
	SemaphoreWaitNoWaiter    = KeyedWaitNoWaiter
	SemaphoreWaitPosted      = KeyedWaitPosted
)

type keyedWaitState uint8

const (
	semaphoreWaitFree keyedWaitState = iota
	semaphoreWaitActive
	semaphoreWaitDelivered
)

type keyedWaitSlot struct {
	generation uint32
	state      keyedWaitState
	namespace  KeyedWaitNamespace
	key        uintptr
	ticket     uint32
	sequence   uint64
	wait       WaitRegistrationHandle
}

type semaphoreWaitSlot = keyedWaitSlot

// KeyedWaitPage is stable target-provided owner-only key storage. It has no
// producer-concurrent fields and retains no Go pointer.
type KeyedWaitPage struct {
	slots [KeyedWaitPageCapacity]keyedWaitSlot
}

type SemaphoreWaitPage = KeyedWaitPage

// KeyedWaitCatalog is a paged, allocation-free owner-side key index. Its
// configured capacity must equal the common WaitRegistrationTable capacity.
// All methods are serialized by one running ExecutorDriver owner. It
// deliberately has no producer-concurrent API: release/notify runs as ordinary
// managed code on the owner, while the common wait table remains the only
// durable event source.
//
// The catalog never retains a Go pointer. The acquiring coroutine frame keeps
// its typed semaphore or notifyList address live across park; this table stores
// only the non-dereferenced equality key used to match a notification.
type KeyedWaitCatalog struct {
	sequence   uint64
	slots      [KeyedWaitPageCapacity]keyedWaitSlot
	extraPages []KeyedWaitPage
}

type SemaphoreWaitCatalog = KeyedWaitCatalog

func KeyedWaitConfiguredCapacity(catalog *KeyedWaitCatalog) uint32 {
	if catalog == nil {
		return 0
	}
	return uint32(1+len(catalog.extraPages)) * KeyedWaitPageCapacity
}

func SemaphoreWaitConfiguredCapacity(catalog *SemaphoreWaitCatalog) uint32 {
	return KeyedWaitConfiguredCapacity(catalog)
}

func keyedWaitSlotAtIndex(catalog *KeyedWaitCatalog, index uint32) (*keyedWaitSlot, bool) {
	if index >= KeyedWaitConfiguredCapacity(catalog) {
		return nil, false
	}
	if index < KeyedWaitPageCapacity {
		return &catalog.slots[index], true
	}
	page := index/KeyedWaitPageCapacity - 1
	offset := index % KeyedWaitPageCapacity
	return &catalog.extraPages[page].slots[offset], true
}

func semaphoreWaitSlotAtIndex(catalog *SemaphoreWaitCatalog, index uint32) (*semaphoreWaitSlot, bool) {
	return keyedWaitSlotAtIndex(catalog, index)
}

func reusableKeyedWaitSlot(slot *keyedWaitSlot) bool {
	return slot != nil && slot.state == semaphoreWaitFree && slot.namespace == KeyedWaitNamespaceInvalid &&
		slot.key == 0 && slot.ticket == 0 && slot.sequence == 0 &&
		slot.wait == (WaitRegistrationHandle{})
}

func reusableSemaphoreWaitSlot(slot *semaphoreWaitSlot) bool { return reusableKeyedWaitSlot(slot) }

// ConfigureKeyedWaitPages attaches an allocation-free page pool while the
// catalog is empty. It supports the same idempotent/monotonic configuration
// lifecycle as WaitRegistrationTable and never changes the two-word handle.
func ConfigureKeyedWaitPages(catalog *KeyedWaitCatalog, pages []KeyedWaitPage) bool {
	if catalog == nil || len(pages) > int(operationLocalMask/KeyedWaitPageCapacity)-1 {
		return false
	}
	existing := len(catalog.extraPages)
	if existing != 0 && (len(pages) < existing || len(pages) == 0 || &catalog.extraPages[0] != &pages[0]) {
		return false
	}
	if len(pages) == existing {
		return true
	}
	if !catalog.CanRelease() {
		return false
	}
	for page := existing; page < len(pages); page++ {
		for offset := range pages[page].slots {
			if !reusableKeyedWaitSlot(&pages[page].slots[offset]) {
				return false
			}
		}
	}
	catalog.extraPages = pages
	return true
}

func ConfigureSemaphoreWaitPages(catalog *SemaphoreWaitCatalog, pages []SemaphoreWaitPage) bool {
	return ConfigureKeyedWaitPages(catalog, pages)
}

func keyedWaitSlotAt(catalog *KeyedWaitCatalog, handle KeyedWaitHandle) (*keyedWaitSlot, bool) {
	if catalog == nil || handle.Slot == 0 || handle.Generation == 0 {
		return nil, false
	}
	return keyedWaitSlotAtIndex(catalog, handle.Slot-1)
}

func semaphoreWaitSlotAt(catalog *SemaphoreWaitCatalog, handle SemaphoreWaitHandle) (*semaphoreWaitSlot, bool) {
	return keyedWaitSlotAt(catalog, handle)
}

func (catalog *KeyedWaitCatalog) register(
	namespace KeyedWaitNamespace,
	key uintptr,
	ticket uint32,
	wait WaitRegistrationHandle,
) (KeyedWaitHandle, bool) {
	if catalog == nil || namespace == KeyedWaitNamespaceInvalid || key == 0 ||
		wait == (WaitRegistrationHandle{}) || catalog.sequence == ^uint64(0) {
		return KeyedWaitHandle{}, false
	}
	for index := uint32(0); index < KeyedWaitConfiguredCapacity(catalog); index++ {
		slot, slotOK := keyedWaitSlotAtIndex(catalog, index)
		if !slotOK {
			return KeyedWaitHandle{}, false
		}
		if slot.state != semaphoreWaitFree || slot.namespace != KeyedWaitNamespaceInvalid ||
			slot.key != 0 || slot.ticket != 0 || slot.sequence != 0 ||
			slot.wait != (WaitRegistrationHandle{}) || slot.generation == ^uint32(0) {
			continue
		}
		generation := slot.generation + 1
		if generation == 0 {
			return KeyedWaitHandle{}, false
		}
		catalog.sequence++
		if catalog.sequence == 0 {
			return KeyedWaitHandle{}, false
		}
		slot.generation = generation
		slot.namespace = namespace
		slot.key = key
		slot.ticket = ticket
		slot.sequence = catalog.sequence
		slot.wait = wait
		slot.state = semaphoreWaitActive
		return KeyedWaitHandle{Slot: index + 1, Generation: generation}, true
	}
	return KeyedWaitHandle{}, false
}

// PrepareKeyedWait arms the common token registration and publishes its
// namespaced logical key as one owner transaction. Publication failure rolls
// the common registration back before this function returns, so a successful
// return is the only state in which the caller may immediately enter coroPark.
func PrepareKeyedWait(
	p *P,
	waits *WaitRegistrationTable,
	catalog *KeyedWaitCatalog,
	token *WaitToken,
	namespace KeyedWaitNamespace,
	key uintptr,
	logicalTicket uint32,
) (WaitTicket, KeyedWaitHandle, KeyedWaitPrepareResult) {
	if p == nil || waits == nil || catalog == nil || token == nil ||
		namespace == KeyedWaitNamespaceInvalid || key == 0 ||
		KeyedWaitConfiguredCapacity(catalog) != WaitRegistrationConfiguredCapacity(waits) {
		return 0, KeyedWaitHandle{}, KeyedWaitPrepareInvalid
	}
	ticket, wait, result := PrepareWaitRegistration(p, waits, token)
	switch result {
	case WaitRegistrationPrepared:
	case WaitRegistrationPreparePoisoned:
		return 0, KeyedWaitHandle{}, KeyedWaitPreparePoisoned
	case WaitRegistrationPrepareRejected:
		return 0, KeyedWaitHandle{}, KeyedWaitPrepareRejected
	default:
		return 0, KeyedWaitHandle{}, KeyedWaitPrepareInvalid
	}
	handle, ok := catalog.register(namespace, key, logicalTicket, wait)
	if ok {
		return ticket, handle, KeyedWaitPrepared
	}
	if !waits.RollbackPreparedWait(wait, token, ticket) {
		return 0, KeyedWaitHandle{}, KeyedWaitPreparePoisoned
	}
	return 0, KeyedWaitHandle{}, KeyedWaitPrepareRejected
}

// PrepareSemaphoreWait is the address-keyed semaphore specialization.
func PrepareSemaphoreWait(
	p *P,
	waits *WaitRegistrationTable,
	catalog *SemaphoreWaitCatalog,
	token *WaitToken,
	key uintptr,
) (WaitTicket, SemaphoreWaitHandle, SemaphoreWaitPrepareResult) {
	return PrepareKeyedWait(p, waits, catalog, token, KeyedWaitNamespaceSemaphore, key, 0)
}

// KeyedWaitTicketLess compares wrapped 32-bit ticket counters under the same
// bounded-distance rule used by the Go runtime notifyList implementation. A
// caller must keep the unwrapped distance below 2^31.
func KeyedWaitTicketLess(a, b uint32) bool {
	return int32(a-b) < 0
}

// PostExactKeyedWait publishes the one active waiter with the exact
// namespace/key/logical-ticket tuple. A duplicate tuple is an invariant
// violation rather than an arbitrary wake choice.
func PostExactKeyedWait(
	catalog *KeyedWaitCatalog,
	waits *WaitRegistrationTable,
	namespace KeyedWaitNamespace,
	key uintptr,
	logicalTicket uint32,
) KeyedWaitPostResult {
	if catalog == nil || waits == nil || namespace == KeyedWaitNamespaceInvalid || key == 0 ||
		KeyedWaitConfiguredCapacity(catalog) != WaitRegistrationConfiguredCapacity(waits) {
		return KeyedWaitPostInvalid
	}
	var selected *keyedWaitSlot
	for index := uint32(0); index < KeyedWaitConfiguredCapacity(catalog); index++ {
		slot, ok := keyedWaitSlotAtIndex(catalog, index)
		if !ok {
			return KeyedWaitPostInvalid
		}
		if slot.state != semaphoreWaitActive || slot.namespace != namespace || slot.key != key ||
			slot.ticket != logicalTicket || slot.sequence == 0 || slot.wait == (WaitRegistrationHandle{}) {
			continue
		}
		if selected != nil {
			return KeyedWaitPostInvalid
		}
		selected = slot
	}
	if selected == nil {
		return KeyedWaitNoWaiter
	}
	if waits.Post(selected.wait) != WaitRegistrationPosted {
		return KeyedWaitPostInvalid
	}
	selected.state = semaphoreWaitDelivered
	return KeyedWaitPosted
}

// PostKeyedWaitTicketRange publishes every active ticket in [first, next),
// using wrapped bounded comparison. It is the NotifyAll primitive: tickets
// added after the caller's wait snapshot are outside the interval.
func PostKeyedWaitTicketRange(
	catalog *KeyedWaitCatalog,
	waits *WaitRegistrationTable,
	namespace KeyedWaitNamespace,
	key uintptr,
	first, next uint32,
) (uint32, bool) {
	if catalog == nil || waits == nil || namespace == KeyedWaitNamespaceInvalid || key == 0 ||
		KeyedWaitConfiguredCapacity(catalog) != WaitRegistrationConfiguredCapacity(waits) {
		return 0, false
	}
	if first == next {
		return 0, true
	}
	var posted uint32
	for index := uint32(0); index < KeyedWaitConfiguredCapacity(catalog); index++ {
		slot, ok := keyedWaitSlotAtIndex(catalog, index)
		if !ok {
			return posted, false
		}
		if slot.state != semaphoreWaitActive || slot.namespace != namespace || slot.key != key ||
			KeyedWaitTicketLess(slot.ticket, first) || !KeyedWaitTicketLess(slot.ticket, next) ||
			slot.sequence == 0 || slot.wait == (WaitRegistrationHandle{}) {
			continue
		}
		if waits.Post(slot.wait) != WaitRegistrationPosted {
			return posted, false
		}
		slot.state = semaphoreWaitDelivered
		posted++
	}
	return posted, true
}

// PostSemaphoreWait selects and posts the oldest waiter for key. The selected
// catalog entry remains live until its resumed continuation retires the exact
// token generation; a semaphore token stolen by another runnable goroutine is
// handled by that continuation registering again after retirement.
func PostSemaphoreWait(catalog *SemaphoreWaitCatalog, waits *WaitRegistrationTable, key uintptr) SemaphoreWaitPostResult {
	if catalog == nil || waits == nil || key == 0 ||
		SemaphoreWaitConfiguredCapacity(catalog) != WaitRegistrationConfiguredCapacity(waits) {
		return SemaphoreWaitPostInvalid
	}
	var selected *semaphoreWaitSlot
	for index := uint32(0); index < SemaphoreWaitConfiguredCapacity(catalog); index++ {
		slot, slotOK := semaphoreWaitSlotAtIndex(catalog, index)
		if !slotOK {
			return SemaphoreWaitPostInvalid
		}
		if slot.state != semaphoreWaitActive || slot.namespace != KeyedWaitNamespaceSemaphore ||
			slot.key != key || slot.ticket != 0 || slot.sequence == 0 ||
			slot.wait == (WaitRegistrationHandle{}) {
			continue
		}
		if selected == nil || slot.sequence < selected.sequence {
			selected = slot
		}
	}
	if selected == nil {
		return SemaphoreWaitNoWaiter
	}
	if waits.Post(selected.wait) != WaitRegistrationPosted {
		return SemaphoreWaitPostInvalid
	}
	selected.state = semaphoreWaitDelivered
	return SemaphoreWaitPosted
}

// PostPreparedSemaphoreWait posts one exact active registration. It closes the
// release-before-prepare race: after publishing a waiter, the prepare owner
// rechecks the semaphore counter and uses this operation when a token is
// already available. The exact waiter must be made durable before its caller
// enters the compiler-certified park span.
func PostPreparedKeyedWait(
	catalog *KeyedWaitCatalog,
	waits *WaitRegistrationTable,
	handle KeyedWaitHandle,
	namespace KeyedWaitNamespace,
	key uintptr,
	logicalTicket uint32,
) KeyedWaitPostResult {
	slot, ok := keyedWaitSlotAt(catalog, handle)
	if !ok || waits == nil || namespace == KeyedWaitNamespaceInvalid || key == 0 ||
		KeyedWaitConfiguredCapacity(catalog) != WaitRegistrationConfiguredCapacity(waits) ||
		slot.generation != handle.Generation ||
		slot.state != semaphoreWaitActive || slot.namespace != namespace ||
		slot.key != key || slot.ticket != logicalTicket || slot.sequence == 0 ||
		slot.wait == (WaitRegistrationHandle{}) {
		return KeyedWaitPostInvalid
	}
	if waits.Post(slot.wait) != WaitRegistrationPosted {
		return KeyedWaitPostInvalid
	}
	slot.state = semaphoreWaitDelivered
	return KeyedWaitPosted
}

func PostPreparedSemaphoreWait(
	catalog *SemaphoreWaitCatalog,
	waits *WaitRegistrationTable,
	handle SemaphoreWaitHandle,
) SemaphoreWaitPostResult {
	slot, ok := semaphoreWaitSlotAt(catalog, handle)
	if !ok {
		return SemaphoreWaitPostInvalid
	}
	return PostPreparedKeyedWait(catalog, waits, handle, KeyedWaitNamespaceSemaphore, slot.key, 0)
}

// RetireCompletedSemaphoreWait joins the owner-only key publication and the
// common delivered wait. It clears the key only after WaitRegistrationTable
// has proved completion consumption and physical producer quiescence.
func RetireCompletedKeyedWait(
	catalog *KeyedWaitCatalog,
	waits *WaitRegistrationTable,
	handle KeyedWaitHandle,
	token *WaitToken,
	ticket WaitTicket,
	namespace KeyedWaitNamespace,
	key uintptr,
	logicalTicket uint32,
) bool {
	slot, ok := keyedWaitSlotAt(catalog, handle)
	if !ok || waits == nil || token == nil || !validWaitTicket(ticket) ||
		namespace == KeyedWaitNamespaceInvalid || key == 0 ||
		KeyedWaitConfiguredCapacity(catalog) != WaitRegistrationConfiguredCapacity(waits) ||
		slot.generation != handle.Generation || slot.state != semaphoreWaitDelivered ||
		slot.namespace != namespace || slot.key != key || slot.ticket != logicalTicket ||
		slot.sequence == 0 || slot.wait == (WaitRegistrationHandle{}) ||
		!waits.RetireCompletedWait(slot.wait, token, ticket) {
		return false
	}
	slot.state = semaphoreWaitFree
	slot.namespace = KeyedWaitNamespaceInvalid
	slot.key = 0
	slot.ticket = 0
	slot.sequence = 0
	slot.wait = WaitRegistrationHandle{}
	return true
}

func RetireCompletedSemaphoreWait(
	catalog *SemaphoreWaitCatalog,
	waits *WaitRegistrationTable,
	handle SemaphoreWaitHandle,
	token *WaitToken,
	ticket WaitTicket,
) bool {
	slot, ok := semaphoreWaitSlotAt(catalog, handle)
	if !ok {
		return false
	}
	return RetireCompletedKeyedWait(
		catalog, waits, handle, token, ticket,
		KeyedWaitNamespaceSemaphore, slot.key, 0,
	)
}

// CanRelease reports whether no parked frame is indexed by the catalog. The
// generation and monotonic sequence counters intentionally survive reuse.
func (catalog *KeyedWaitCatalog) CanRelease() bool {
	if catalog == nil {
		return false
	}
	for index := uint32(0); index < KeyedWaitConfiguredCapacity(catalog); index++ {
		slot, slotOK := keyedWaitSlotAtIndex(catalog, index)
		if !slotOK || !reusableKeyedWaitSlot(slot) {
			return false
		}
	}
	return true
}
