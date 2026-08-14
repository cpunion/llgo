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

// WorkerOperationPageCapacity is the allocation-free granularity of the
// target-neutral worker catalog. Every source includes one inline page; a
// target may attach additional stable pages before binding the source.
const WorkerOperationPageCapacity = 64

// WorkerOperationSourceCapacity is the default capacity of an unconfigured
// source. Keep this compatibility name for small, embedded, and bare-metal
// profiles; it is not the capacity after ConfigureWorkerOperationPages.
const WorkerOperationSourceCapacity = WorkerOperationPageCapacity

// WorkerOperationMaximumCapacity is the largest complete-page catalog encoded
// by the existing OperationID local field. Physical worker queues may keep a
// smaller target-specific limit.
const WorkerOperationMaximumCapacity = operationCatalogMaximumPageCount * WorkerOperationPageCapacity

type WorkerOperationPostResult uint8

const (
	WorkerOperationPostInvalid WorkerOperationPostResult = iota
	WorkerOperationPosted
	WorkerOperationPostDuplicate
	WorkerOperationPostClosed
	WorkerOperationPostStale
)

type WorkerOperationCloseResult uint8

const (
	WorkerOperationCloseInvalid WorkerOperationCloseResult = iota
	WorkerOperationCloseStarted
	WorkerOperationAlreadyClosing
	WorkerOperationAlreadyQuiesced
)

type workerOperationMailbox uint32

const (
	workerOperationMailboxEmpty workerOperationMailbox = iota
	workerOperationMailboxPosting
	workerOperationMailboxPosted
	workerOperationMailboxDraining
	workerOperationMailboxDelivered
)

type workerOperationSlot struct {
	// Producer-visible, pointer-free stable storage. Post writes payload before
	// release-publishing Posted; owner P alone reads the suffix below it.
	producerSourceSlot
	mailbox uint32
	payload ScalarResultPayloadV1

	record       OperationRecord
	result       ScalarResultCell
	nextAffected uint32
	// submitted is owner-only. Once true, a non-cancellable backend may still
	// publish after logical task cancellation, so ApplyOne must retain the
	// ParkLink until a delivered mailbox proves physical completion.
	submitted bool
}

// WorkerOperationPage is target-provided stable storage. Its producer-visible
// prefix is still reached only through the existing two-word OperationID; no
// page pointer, Go pointer, or coroutine frame address crosses the worker ABI.
type WorkerOperationPage struct {
	slots [WorkerOperationPageCapacity]workerOperationSlot
}

// WorkerOperationSource is the allocation-free scheduler half of a bounded
// asynchronous worker source. It owns no thread, queue, or platform cancel
// mechanism. A backend receives only OperationID, publishes a pointer-free
// result with Post, and requests executor service through the common doorbell.
// Post is producer-concurrent; other mutating methods are owner-P-only.
type WorkerOperationSource struct {
	routedProducerSource
	slots        [WorkerOperationPageCapacity]workerOperationSlot
	extraPages   []WorkerOperationPage
	dynamicPages operationDynamicPageDirectory
	scanLimit    uint32

	affectedHead uint32
	affectedTail uint32
}

// workerOperationReservation is an owner-stack capability for one reusable
// worker slot. It exists only inside a no-suspend park transaction; the
// backend receives the resulting OperationID, never this source or slot.
type workerOperationReservation struct {
	source   *WorkerOperationSource
	owner    *P
	slot     *workerOperationSlot
	index    uint32
	capacity uint32
}

// WorkerOperationConfiguredCapacity returns the exact linear slot and scan
// capacity. Linear one-based slot identities remain encodable in the frozen
// 15-bit OperationID local field.
func WorkerOperationConfiguredCapacity(source *WorkerOperationSource) uint32 {
	if source == nil {
		return 0
	}
	pages := uint32(1+len(source.extraPages)) + source.dynamicPages.published()
	if pages > operationCatalogMaximumPageCount {
		return 0
	}
	return pages * WorkerOperationPageCapacity
}

func workerOperationScanLimit(source *WorkerOperationSource) (uint32, bool) {
	if source == nil {
		return 0, false
	}
	capacity := WorkerOperationConfiguredCapacity(source)
	return source.scanLimit, validSourceScanLimit(source.scanLimit, capacity)
}

func workerOperationSlotAt(source *WorkerOperationSource, index uint32) (*workerOperationSlot, bool) {
	if index >= WorkerOperationConfiguredCapacity(source) {
		return nil, false
	}
	if index < WorkerOperationPageCapacity {
		return &source.slots[index], true
	}
	page := index / WorkerOperationPageCapacity
	offset := index % WorkerOperationPageCapacity
	catalog, ok := workerOperationPageAt(source, page)
	if !ok {
		return nil, false
	}
	return &catalog.slots[offset], true
}

func workerOperationPageAt(source *WorkerOperationSource, page uint32) (*WorkerOperationPage, bool) {
	if source == nil || page == 0 {
		return nil, false
	}
	extra := page - 1
	if extra < uint32(len(source.extraPages)) {
		return &source.extraPages[extra], true
	}
	dynamic := source.dynamicPages.page(extra - uint32(len(source.extraPages)))
	if dynamic == nil {
		return nil, false
	}
	return (*WorkerOperationPage)(dynamic), true
}

// ConfigureWorkerOperationPages attaches stable allocation-free catalog pages
// while the source is empty and unbound. Repeating an identical configuration
// is harmless, and a source may monotonically expose more of the same backing
// pool. Configured pages are frozen while bound so concurrent Post calls see a
// stable slice header and producer prefix.
func ConfigureWorkerOperationPages(source *WorkerOperationSource, pages []WorkerOperationPage) bool {
	if source == nil || !routedProducerHeaderEmpty(&source.routedProducerSource, nil) ||
		source.affectedHead != 0 || source.affectedTail != 0 ||
		len(pages) > int(operationLocalMask/WorkerOperationPageCapacity)-1 {
		return false
	}
	existing := len(source.extraPages)
	if existing != 0 && (len(pages) < existing || len(pages) == 0 || &source.extraPages[0] != &pages[0]) {
		return false
	}
	if len(pages) == existing {
		return true
	}
	if source.dynamicPages.published() != 0 {
		return false
	}
	for index := uint32(0); index < WorkerOperationConfiguredCapacity(source); index++ {
		slot, ok := workerOperationSlotAt(source, index)
		if !ok || !workerOperationReusableSlot(source, slot, index) {
			return false
		}
	}
	for page := existing; page < len(pages); page++ {
		for offset := range pages[page].slots {
			index := uint32(page+1)*WorkerOperationPageCapacity + uint32(offset)
			if !workerOperationReusableSlot(source, &pages[page].slots[offset], index) {
				return false
			}
		}
	}
	source.extraPages = pages
	return true
}

func (source *WorkerOperationSource) preflightWorkerReservationOwned(
	p *P,
) (workerOperationReservation, bool) {
	capacity := WorkerOperationConfiguredCapacity(source)
	for index := uint32(0); index < capacity; index++ {
		slot, ok := workerOperationSlotAt(source, index)
		if !ok {
			return workerOperationReservation{}, false
		}
		if preemptLoad(&slot.generation) != ^uint32(0) && workerOperationReusableSlot(source, slot, index) {
			return workerOperationReservation{
				source: source, owner: p, slot: slot, index: index, capacity: capacity,
			}, true
		}
	}
	return workerOperationReservation{}, false
}

func validWorkerOperationReservationHeader(
	source *WorkerOperationSource,
	p *P,
	reservation workerOperationReservation,
) bool {
	return source != nil && p != nil && reservation.source == source &&
		reservation.owner == p && reservation.slot != nil &&
		reservation.capacity != 0 && reservation.index < reservation.capacity
}

// preflightWorkerOperationReservation authenticates the owner and selects the
// exact reusable slot once. Physical queue capacity remains an independent
// target responsibility.
func preflightWorkerOperationReservation(
	p *P,
	source *WorkerOperationSource,
) (workerOperationReservation, bool) {
	if !validWorkerOperationOwner(source, p) {
		return workerOperationReservation{}, false
	}
	return source.preflightWorkerReservationOwned(p)
}

// CanReserveWorkerOperation is the allocation-free source-capacity preflight.
func CanReserveWorkerOperation(p *P, source *WorkerOperationSource) bool {
	_, ok := preflightWorkerOperationReservation(p, source)
	return ok
}

// AttachWorkerOperationPage publishes one pristine stable page from the owner
// P. A backend still retains only OperationID and never observes this pointer.
func AttachWorkerOperationPage(
	source *WorkerOperationSource,
	p *P,
	page *WorkerOperationPage,
	directoryBlock *OperationPageDirectoryBlock,
) bool {
	if !validWorkerOperationOwner(source, p) || page == nil {
		return false
	}
	oldCapacity := WorkerOperationConfiguredCapacity(source)
	if oldCapacity == 0 || oldCapacity > WorkerOperationMaximumCapacity-WorkerOperationPageCapacity {
		return false
	}
	for index := range source.extraPages {
		if &source.extraPages[index] == page {
			return false
		}
	}
	for offset := range page.slots {
		if !workerOperationReusableSlot(source, &page.slots[offset], oldCapacity+uint32(offset)) {
			return false
		}
	}
	return source.dynamicPages.publish(unsafe.Pointer(page), directoryBlock)
}

func workerOperationSlotFor(source *WorkerOperationSource, id OperationID) (*workerOperationSlot, bool) {
	if source == nil || !source.route.Valid() || !id.Valid() || id.Source() != OperationSourceWorker ||
		id.Route() != source.route || id.LocalSlot() == 0 || id.LocalSlot() > WorkerOperationConfiguredCapacity(source) {
		return nil, false
	}
	return workerOperationSlotAt(source, id.LocalSlot()-1)
}

func workerOperationReusableSlot(source *WorkerOperationSource, slot *workerOperationSlot, index uint32) bool {
	if slot == nil || !producerSourceSlotReusable(&slot.producerSourceSlot) ||
		preemptLoad(&slot.mailbox) != uint32(workerOperationMailboxEmpty) ||
		slot.payload != (ScalarResultPayloadV1{}) || slot.result != (ScalarResultCell{}) ||
		slot.nextAffected != 0 || slot.submitted {
		return false
	}
	generation := preemptLoad(&slot.generation)
	if generation == 0 {
		return slot.record == (OperationRecord{})
	}
	if source == nil || !source.route.Valid() {
		return false
	}
	id, ok := MakeOperationIDAtRoute(OperationSourceWorker, source.route, index+1, generation)
	return ok && slot.record == (OperationRecord{id: id, phase: operationReusable})
}

func validWorkerOperationOwner(source *WorkerOperationSource, p *P) bool {
	_, scanOK := workerOperationScanLimit(source)
	return scanOK && validRoutedProducerSource(&source.routedProducerSource, p)
}

func validWorkerOperationLiveSlot(source *WorkerOperationSource, p *P, index uint32) bool {
	if !validWorkerOperationOwner(source, p) || index >= WorkerOperationConfiguredCapacity(source) {
		return false
	}
	slot, ok := workerOperationSlotAt(source, index)
	if !ok {
		return false
	}
	state := producerSourceLifecycle(preemptLoad(&slot.state))
	if state != producerSourceActive && state != producerSourceClosing && state != producerSourceQuiesced {
		return false
	}
	generation := preemptLoad(&slot.generation)
	id, ok := MakeOperationIDAtRoute(OperationSourceWorker, source.route, index+1, generation)
	return ok && slot.record.Matches(id)
}

func (source *WorkerOperationSource) reserveAndAttachWorkerSlot(
	state *ParkState,
	ticket ParkTicket,
	wait *WaitSetRecord,
	caseID uint32,
	reservation workerOperationReservation,
) (OperationID, bool) {
	slot, index := reservation.slot, reservation.index
	if slot == nil || index >= reservation.capacity ||
		reservation.capacity != WorkerOperationConfiguredCapacity(source) {
		return OperationID{}, false
	}
	generation, begun := beginProducerSourceSlot(&slot.producerSourceSlot)
	if !begun {
		return OperationID{}, false
	}
	if !raiseSourceScanLimit(&source.scanLimit, index, reservation.capacity) {
		return OperationID{}, false
	}
	id, ok := MakeOperationIDAtRoute(OperationSourceWorker, source.route, index+1, generation)
	if !ok || !PrepareOperationAtGeneration(&slot.record, id) {
		return OperationID{}, false
	}
	attached := false
	if wait == nil {
		attached = AttachParkOperation(state, ticket, &slot.record, caseID)
	} else {
		attached = AttachParkWaitOperation(state, ticket, wait, &slot.record, caseID)
	}
	if !attached {
		if !AbortReservedOperation(&slot.record, id) ||
			!resetProducerSourceSlot(&slot.producerSourceSlot, generation) {
			return OperationID{}, false
		}
		return OperationID{}, false
	}
	if !activateProducerSourceSlot(&slot.producerSourceSlot, generation) {
		return OperationID{}, false
	}
	return id, true
}

func (source *WorkerOperationSource) reserveAndAttach(
	p *P,
	state *ParkState,
	ticket ParkTicket,
	wait *WaitSetRecord,
	caseID uint32,
) (OperationID, bool) {
	reservation, ok := preflightWorkerOperationReservation(p, source)
	if !ok {
		return OperationID{}, false
	}
	return source.reserveAndAttachWorkerSlot(state, ticket, wait, caseID, reservation)
}

func (source *WorkerOperationSource) ReserveAndAttach(
	p *P,
	state *ParkState,
	ticket ParkTicket,
	caseID uint32,
) (OperationID, bool) {
	return source.reserveAndAttach(p, state, ticket, nil, caseID)
}

func (source *WorkerOperationSource) ReserveAndAttachWait(
	p *P,
	state *ParkState,
	ticket ParkTicket,
	wait *WaitSetRecord,
	caseID uint32,
) (OperationID, bool) {
	return source.reserveAndAttach(p, state, ticket, wait, caseID)
}

// MarkSubmitted closes the owner-side handoff from a reserved source slot to
// a backend queue. Before this point a failed submission may be canceled and
// recycled immediately. Afterwards logical cancellation is delayed at the
// source apply boundary until Post has made physical completion durable.
func (source *WorkerOperationSource) MarkSubmitted(p *P, id OperationID) bool {
	slot, ok := workerOperationSlotFor(source, id)
	if !ok || !validWorkerOperationOwner(source, p) || slot.submitted ||
		preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.state) != uint32(producerSourceActive) ||
		preemptLoad(&slot.mailbox) != uint32(workerOperationMailboxEmpty) ||
		!slot.record.Matches(id) || slot.record.phase != operationActive {
		return false
	}
	slot.submitted = true
	return true
}

// markWorkerReservationSubmitted closes the compiler-owned worker transaction
// against the slot it just reserved. The owner/source relation and complete
// ParkLink graph were already audited before this no-suspend suffix.
func (source *WorkerOperationSource) markWorkerReservationSubmitted(
	p *P,
	reservation workerOperationReservation,
	id OperationID,
) bool {
	if !validWorkerOperationReservationHeader(source, p, reservation) ||
		id.LocalSlot() != reservation.index+1 || id.Route() != source.route ||
		id.Source() != OperationSourceWorker {
		return false
	}
	slot := reservation.slot
	if slot.submitted || preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.state) != uint32(producerSourceActive) ||
		preemptLoad(&slot.mailbox) != uint32(workerOperationMailboxEmpty) ||
		!slot.record.Matches(id) || slot.record.phase != operationActive {
		return false
	}
	slot.submitted = true
	return true
}

// Post publishes only the first exact-generation result. Later producers are
// coalesced and cannot replace its scalar payload.
func (source *WorkerOperationSource) Post(id OperationID, payload ScalarResultPayloadV1) WorkerOperationPostResult {
	if !payload.Valid() {
		return WorkerOperationPostInvalid
	}
	slot, ok := workerOperationSlotFor(source, id)
	if !ok {
		return WorkerOperationPostInvalid
	}
	switch acquireProducerSourceGeneration(&slot.producerSourceSlot, id.Generation) {
	case producerSourceAcquireClosed:
		return WorkerOperationPostClosed
	case producerSourceAcquireStale:
		return WorkerOperationPostStale
	case producerSourceAcquired:
	default:
		return WorkerOperationPostInvalid
	}
	if preemptLoad(&slot.state) != uint32(producerSourceActive) {
		producerAdmissionRelease(&slot.inflight)
		return WorkerOperationPostClosed
	}
	for {
		switch mailbox := workerOperationMailbox(preemptLoad(&slot.mailbox)); mailbox {
		case workerOperationMailboxEmpty:
			if !preemptCompareAndSwap(&slot.mailbox, uint32(mailbox), uint32(workerOperationMailboxPosting)) {
				continue
			}
			slot.payload = payload
			preemptStore(&slot.mailbox, uint32(workerOperationMailboxPosted))
			preemptStore(&source.pending, 1)
			producerAdmissionRelease(&slot.inflight)
			return WorkerOperationPosted
		case workerOperationMailboxPosting, workerOperationMailboxPosted,
			workerOperationMailboxDraining, workerOperationMailboxDelivered:
			producerAdmissionRelease(&slot.inflight)
			return WorkerOperationPostDuplicate
		default:
			producerAdmissionRelease(&slot.inflight)
			return WorkerOperationPostInvalid
		}
	}
}

func (source *WorkerOperationSource) Pending() bool {
	return source != nil && routedProducerPending(&source.routedProducerSource)
}

// submittedCompletionState is the owner-side observation used by a physical
// executor immediately before it arms a retained wait. awaiting reports that
// at least one backend owns an exact submitted generation whose completion is
// not yet durable; ready reports the source's durable completion hint. The
// observation never changes source state and never waits for a producer.
func (source *WorkerOperationSource) submittedCompletionState(p *P) (awaiting, ready, ok bool) {
	if !validWorkerOperationOwner(source, p) {
		return false, false, false
	}
	ready = source.Pending()
	limit, valid := workerOperationScanLimit(source)
	if !valid {
		return false, false, false
	}
	for index := uint32(0); index < limit; index++ {
		slot, found := workerOperationSlotAt(source, index)
		if !found {
			return false, false, false
		}
		if !slot.submitted {
			continue
		}
		if !validWorkerOperationLiveSlot(source, p, index) {
			return false, false, false
		}
		switch workerOperationMailbox(preemptLoad(&slot.mailbox)) {
		case workerOperationMailboxEmpty, workerOperationMailboxPosting:
			awaiting = true
		case workerOperationMailboxPosted, workerOperationMailboxDraining,
			workerOperationMailboxDelivered:
		default:
			return false, false, false
		}
	}
	return awaiting, ready, true
}

func (source *WorkerOperationSource) RequestCancel(p *P, wait *WaitSetRecord) bool {
	return validWorkerOperationOwner(source, p) && RequestWaitSetCancel(p, wait, ParkCancelOperation)
}

// RequestCancelID is the scalar-keyed owner counterpart used by an
// operation-specific controller such as internal/poll. The controller retains
// only the exact two-word generation; the worker catalog resolves its private
// WaitSetRecord and applies the ordinary logical-cancel transaction.
func (source *WorkerOperationSource) RequestCancelID(p *P, id OperationID) bool {
	slot, ok := workerOperationSlotFor(source, id)
	return ok && validWorkerOperationOwner(source, p) &&
		preemptLoad(&slot.generation) == id.Generation &&
		preemptLoad(&slot.state) == uint32(producerSourceActive) &&
		slot.record.Matches(id) && slot.record.phase == operationActive &&
		slot.record.link.wait != nil &&
		RequestWaitSetCancel(p, slot.record.link.wait, ParkCancelOperation)
}

// WorkerOperationPhysicalCancelRequested exposes only the owner-side physical
// cancellation bit for an exact submitted generation. A backend adapter uses
// it after the common apply phase has frozen a loser/canceled disposition; it
// does not expose the OperationRecord, ParkLink, wait set, or G.
func WorkerOperationPhysicalCancelRequested(
	source *WorkerOperationSource,
	p *P,
	id OperationID,
) (requested bool, ok bool) {
	slot, found := workerOperationSlotFor(source, id)
	if !found || !validWorkerOperationOwner(source, p) ||
		preemptLoad(&slot.generation) != id.Generation ||
		!slot.submitted || !slot.record.Matches(id) ||
		slot.record.phase != operationActive {
		return false, false
	}
	return slot.record.cancelRequested, true
}

func (source *WorkerOperationSource) appendAffected(index uint32) bool {
	oneBased := index + 1
	if source.affectedHead == 0 {
		if source.affectedTail != 0 {
			return false
		}
		source.affectedHead, source.affectedTail = oneBased, oneBased
		return true
	}
	if source.affectedTail == 0 || source.affectedTail > WorkerOperationConfiguredCapacity(source) {
		return false
	}
	tail, ok := workerOperationSlotAt(source, source.affectedTail-1)
	if !ok {
		return false
	}
	if tail.nextAffected != 0 {
		return false
	}
	tail.nextAffected = oneBased
	source.affectedTail = oneBased
	return true
}

func (source *WorkerOperationSource) beginPublishPass(p *P) bool {
	return source != nil && beginRoutedProducerPass(&source.routedProducerSource, p)
}

func (source *WorkerOperationSource) publishSlot(p *P, index uint32) (published, lost uint32, ok bool) {
	if !validWorkerOperationOwner(source, p) || index >= WorkerOperationConfiguredCapacity(source) {
		return 0, 0, false
	}
	slot, slotOK := workerOperationSlotAt(source, index)
	if !slotOK {
		return 0, 0, false
	}
	mailbox := workerOperationMailbox(preemptLoad(&slot.mailbox))
	if mailbox == workerOperationMailboxPosting || mailbox == workerOperationMailboxEmpty ||
		mailbox == workerOperationMailboxDelivered {
		return 0, 0, true
	}
	if mailbox != workerOperationMailboxPosted || !validWorkerOperationLiveSlot(source, p, index) ||
		!slot.payload.Valid() {
		return 0, 0, false
	}
	id := slot.record.id
	// Posted is durable before the producer releases its admission. An executor
	// already running on another M may observe that fact before the callback
	// reaches its release-and-request tail. Seal the one-shot generation now and
	// do not publish a resumable ParkState winner until every admitted Post has
	// left; otherwise the resumed wrapper can race the final producer release.
	switch closeResult := source.beginCloseSlot(p, id); closeResult {
	case WorkerOperationCloseStarted, WorkerOperationAlreadyClosing, WorkerOperationAlreadyQuiesced:
	default:
		return 0, 0, false
	}
	if !producerSourceSlotQuiesced(&slot.producerSourceSlot) {
		// The route callback requests the exact executor only after Post returns.
		// Keep the source hint sticky as well so an owner which is already running
		// retries without depending on that later advisory request.
		preemptStore(&source.pending, 1)
		return 0, 0, true
	}
	if !preemptCompareAndSwap(
		&slot.mailbox,
		uint32(workerOperationMailboxPosted),
		uint32(workerOperationMailboxDraining),
	) {
		return 0, 0, false
	}
	switch result := PublishScalarOperationCompletion(&slot.result, &slot.record, id, slot.payload); result {
	case OperationCompletionPublished:
		if slot.record.link.wait != nil {
			if !MarkWaitSetAffected(p, slot.record.link.wait) {
				return 0, 0, false
			}
		} else if !source.appendAffected(index) {
			return 0, 0, false
		}
		published = 1
	case OperationCompletionLost:
		// A non-cancellable submitted worker may have kept the resolved wait-set
		// in AwaitExternal. Completion is the sticky fact that makes its source
		// link detachable; requeue exactly that wait-set for the next apply pass.
		if slot.submitted && slot.record.link.wait != nil &&
			!MarkWaitSetAffected(p, slot.record.link.wait) {
			return 0, 0, false
		}
		lost = 1
	case OperationCompletionDeferred:
		// A bounded resolver owns a frozen ParkState snapshot. Keep the exact
		// producer fact sticky for the next owner epoch; the scalar helper has
		// already rolled back its temporary result cell.
		if !preemptCompareAndSwap(&slot.mailbox, uint32(workerOperationMailboxDraining), uint32(workerOperationMailboxPosted)) {
			return 0, 0, false
		}
		preemptStore(&source.pending, 1)
		return 0, 0, true
	default:
		return 0, 0, false
	}
	preemptStore(&slot.mailbox, uint32(workerOperationMailboxDelivered))
	return published, lost, true
}

func (source *WorkerOperationSource) PublishPass(p *P) (published, lost uint32, ok bool) {
	if !source.beginPublishPass(p) {
		return 0, 0, false
	}
	limit, valid := workerOperationScanLimit(source)
	if !valid {
		return 0, 0, false
	}
	for index := uint32(0); index < limit; index++ {
		onePublished, oneLost, slotOK := source.publishSlot(p, index)
		published += onePublished
		lost += oneLost
		if !slotOK {
			return published, lost, false
		}
	}
	return published, lost, true
}

func addWorkerOperationResolution(total *CompletionResolution, one CompletionResolution) {
	total.WaitSets += one.WaitSets
	total.Completed += one.Completed
	total.Canceled += one.Canceled
	total.Defaulted += one.Defaulted
	total.Winners += one.Winners
	total.Losers += one.Losers
}

func (source *WorkerOperationSource) ResolveAffectedPublishedEpoch(
	p *P,
) (total CompletionResolution, duplicates uint32, ok bool) {
	if !validWorkerOperationOwner(source, p) {
		return CompletionResolution{}, 0, false
	}
	for source.affectedHead != 0 {
		if source.affectedHead > WorkerOperationConfiguredCapacity(source) {
			return total, duplicates, false
		}
		index := source.affectedHead - 1
		slot, slotOK := workerOperationSlotAt(source, index)
		if !slotOK {
			return total, duplicates, false
		}
		if !validWorkerOperationLiveSlot(source, p, index) {
			return total, duplicates, false
		}
		resolution, result := resolveAffectedOperationPublishedEpoch(&slot.record, slot.record.id)
		if result == affectedOperationResolveInvalid {
			return total, duplicates, false
		}
		source.affectedHead = slot.nextAffected
		slot.nextAffected = 0
		if source.affectedHead == 0 {
			source.affectedTail = 0
		}
		switch result {
		case affectedOperationResolved:
			addWorkerOperationResolution(&total, resolution)
		case affectedOperationAlreadyResolved:
			duplicates++
		}
	}
	return total, duplicates, source.affectedTail == 0
}

func (source *WorkerOperationSource) standaloneAffected(p *P) (affected, ok bool) {
	if !validWorkerOperationOwner(source, p) || (source.affectedHead == 0) != (source.affectedTail == 0) {
		return false, false
	}
	return source.affectedHead != 0, true
}

func (source *WorkerOperationSource) beginCloseSlot(p *P, id OperationID) WorkerOperationCloseResult {
	slot, ok := workerOperationSlotFor(source, id)
	if !ok || !validWorkerOperationOwner(source, p) || preemptLoad(&slot.generation) != id.Generation ||
		!slot.record.Matches(id) {
		return WorkerOperationCloseInvalid
	}
	switch beginProducerSourceClose(&slot.producerSourceSlot) {
	case producerSourceCloseStarted:
		return WorkerOperationCloseStarted
	case producerSourceAlreadyClosing:
		return WorkerOperationAlreadyClosing
	case producerSourceAlreadyQuiesced:
		return WorkerOperationAlreadyQuiesced
	default:
		return WorkerOperationCloseInvalid
	}
}

func (source *WorkerOperationSource) BeginClose(p *P, id OperationID) WorkerOperationCloseResult {
	return source.beginCloseSlot(p, id)
}

func requestWorkerPhysicalCancel(record *OperationRecord, id OperationID) bool {
	if record == nil || !record.Matches(id) || record.phase != operationActive ||
		record.disposition == OperationDispositionPending {
		return false
	}
	// The common helper is normally called before logical resolution. Worker
	// apply necessarily observes the already-frozen loser/canceled disposition,
	// so record the same owner-only request after validating that exact state.
	if record.cancelRequested {
		return true
	}
	record.cancelRequested = true
	return true
}

func (source *WorkerOperationSource) ApplyOne(p *P, id OperationID, record *OperationRecord) OperationApplyResult {
	slot, ok := workerOperationSlotFor(source, id)
	if !ok || !validWorkerOperationOwner(source, p) || preemptLoad(&slot.generation) != id.Generation ||
		&slot.record != record || !slot.record.Matches(id) || slot.record.phase != operationActive {
		return OperationApplyInvalid
	}
	state := producerSourceLifecycle(preemptLoad(&slot.state))
	if state != producerSourceActive && state != producerSourceClosing && state != producerSourceQuiesced {
		return OperationApplyInvalid
	}
	disposition, terminal := OperationDispositionOf(&slot.record, id)
	if !terminal || slot.record.link.park == nil || slot.record.link.operation != &slot.record ||
		slot.record.link.ticket == (ParkTicket{}) {
		return OperationApplyInvalid
	}
	mailbox := workerOperationMailbox(preemptLoad(&slot.mailbox))
	if disposition != OperationDispositionWinner && slot.submitted &&
		mailbox != workerOperationMailboxDelivered {
		if requestWorkerPhysicalCancel(&slot.record, id) {
			return OperationApplyAwaitExternalFact
		}
		return OperationApplyInvalid
	}
	closeResult := source.beginCloseSlot(p, id)
	if closeResult != WorkerOperationCloseStarted && closeResult != WorkerOperationAlreadyClosing &&
		closeResult != WorkerOperationAlreadyQuiesced {
		return OperationApplyInvalid
	}
	if disposition != OperationDispositionWinner && slot.record.resultState == operationResultOwned &&
		!DiscardUnselectedScalarOperationResult(&slot.result, &slot.record, id) {
		return OperationApplyInvalid
	}
	if !slot.record.resolutionApplied && !AcknowledgeOperationResolution(&slot.record, id, disposition) {
		return OperationApplyInvalid
	}
	park, ticket, wait := slot.record.link.park, slot.record.link.ticket, slot.record.link.wait
	detached := wait != nil && DetachParkWaitOperation(p, park, ticket, &slot.record, id) ||
		wait == nil && DetachParkOperation(park, ticket, &slot.record, id)
	if !detached {
		return OperationApplyInvalid
	}
	return OperationApplyDetached
}

func (source *WorkerOperationSource) ApplyAndDetach(p *P) (applied, detached uint32, ok bool) {
	if !validWorkerOperationOwner(source, p) || source.affectedHead != 0 || source.affectedTail != 0 {
		return 0, 0, false
	}
	limit, valid := workerOperationScanLimit(source)
	if !valid {
		return 0, 0, false
	}
	for index := uint32(0); index < limit; index++ {
		slot, slotOK := workerOperationSlotAt(source, index)
		if !slotOK {
			return applied, detached, false
		}
		state := producerSourceLifecycle(preemptLoad(&slot.state))
		if state == producerSourceFree {
			if !workerOperationReusableSlot(source, slot, index) {
				return applied, detached, false
			}
			continue
		}
		if !validWorkerOperationLiveSlot(source, p, index) {
			return applied, detached, false
		}
		id := slot.record.id
		if slot.record.phase == operationDetached {
			if state != producerSourceClosing && state != producerSourceQuiesced {
				return applied, detached, false
			}
			continue
		}
		_, terminal := OperationDispositionOf(&slot.record, id)
		if !terminal {
			continue
		}
		wasApplied := slot.record.resolutionApplied
		if source.ApplyOne(p, id, &slot.record) != OperationApplyDetached {
			return applied, detached, false
		}
		if !wasApplied {
			applied++
		}
		detached++
	}
	return applied, detached, true
}

func (source *WorkerOperationSource) ConfirmQuiesced(p *P, id OperationID) bool {
	slot, ok := workerOperationSlotFor(source, id)
	mailbox := workerOperationMailbox(0)
	if ok {
		mailbox = workerOperationMailbox(preemptLoad(&slot.mailbox))
	}
	if !ok || !validWorkerOperationOwner(source, p) || preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.state) != uint32(producerSourceClosing) ||
		!producerSourceSlotQuiesced(&slot.producerSourceSlot) ||
		(mailbox != workerOperationMailboxEmpty && mailbox != workerOperationMailboxDelivered) ||
		!ConfirmOperationQuiesced(&slot.record, id) {
		return false
	}
	return markProducerSourceQuiesced(&slot.producerSourceSlot)
}

func (source *WorkerOperationSource) TakeResult(
	p *P,
	lease OperationResultLease,
	out *ScalarResultPayloadV1,
) bool {
	id, ok := lease.ID()
	if !ok || !validWorkerOperationOwner(source, p) {
		return false
	}
	slot, ok := workerOperationSlotFor(source, id)
	return ok && preemptLoad(&slot.generation) == id.Generation &&
		TakeScalarOperationResult(&slot.result, &slot.record, lease, out)
}

func (source *WorkerOperationSource) DiscardResult(p *P, lease OperationResultLease) bool {
	id, ok := lease.ID()
	if !ok || !validWorkerOperationOwner(source, p) {
		return false
	}
	slot, ok := workerOperationSlotFor(source, id)
	return ok && preemptLoad(&slot.generation) == id.Generation &&
		DiscardScalarOperationResult(&slot.result, &slot.record, lease)
}

func (source *WorkerOperationSource) Recycle(p *P, id OperationID) bool {
	slot, ok := workerOperationSlotFor(source, id)
	if !ok || !validWorkerOperationOwner(source, p) || source.affectedHead != 0 || source.affectedTail != 0 ||
		preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.state) != uint32(producerSourceQuiesced) ||
		!producerSourceSlotQuiesced(&slot.producerSourceSlot) || slot.result != (ScalarResultCell{}) {
		return false
	}
	mailbox := workerOperationMailbox(preemptLoad(&slot.mailbox))
	if (mailbox != workerOperationMailboxEmpty && mailbox != workerOperationMailboxDelivered) ||
		!OperationCanRecycle(&slot.record, id) || !RecycleOperation(&slot.record, id) {
		return false
	}
	slot.payload = ScalarResultPayloadV1{}
	slot.nextAffected = 0
	slot.submitted = false
	preemptStore(&slot.mailbox, uint32(workerOperationMailboxEmpty))
	return recycleProducerSourceSlot(&slot.producerSourceSlot)
}

func workerOperationSourceEmpty(source *WorkerOperationSource, owner *P) bool {
	if source == nil || !routedProducerHeaderEmpty(&source.routedProducerSource, owner) ||
		source.affectedHead != 0 || source.affectedTail != 0 {
		return false
	}
	for index := uint32(0); index < WorkerOperationConfiguredCapacity(source); index++ {
		slot, ok := workerOperationSlotAt(source, index)
		if !ok || !workerOperationReusableSlot(source, slot, index) {
			return false
		}
	}
	return true
}

func BindWorkerOperationSourceAtRoute(source *WorkerOperationSource, p *P, route RouteID) bool {
	if !workerOperationSourceEmpty(source, nil) {
		return false
	}
	if !bindRoutedProducerSource(&source.routedProducerSource, p, route) {
		return false
	}
	source.scanLimit = 0
	return true
}

func BindWorkerOperationSource(source *WorkerOperationSource, p *P) bool {
	return BindWorkerOperationSourceAtRoute(source, p, RouteID(1))
}

func UnbindWorkerOperationSource(source *WorkerOperationSource, p *P) bool {
	if p == nil || !workerOperationSourceEmpty(source, p) {
		return false
	}
	if !unbindRoutedProducerSource(&source.routedProducerSource, p) {
		return false
	}
	source.scanLimit = 0
	return true
}

func (source *WorkerOperationSource) CanRelease() bool {
	return workerOperationSourceEmpty(source, nil)
}

func (source *WorkerOperationSource) Route() (RouteID, bool) {
	if source == nil {
		return 0, false
	}
	return routedProducerRoute(&source.routedProducerSource)
}
