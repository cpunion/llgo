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

// ManualOperationPageCapacity matches the other paged operation catalogs. The
// inline page keeps small/embedded executors allocation-free; native targets
// may attach stable extra pages without changing OperationID or ParkState.
const ManualOperationPageCapacity = 64

// ManualOperationSourceCapacity is the inline capacity retained for source
// compatibility. Configured capacity may be larger when extra pages are
// attached before bind.
const ManualOperationSourceCapacity = ManualOperationPageCapacity

// ManualOperationMaximumCapacity is the complete-page limit of the frozen
// OperationID local field. Hosted targets may grow toward it without changing
// the producer ABI; small targets keep the inline page.
const ManualOperationMaximumCapacity = operationCatalogMaximumPageCount * ManualOperationPageCapacity

type ManualOperationPostResult uint8

const (
	ManualOperationPostInvalid ManualOperationPostResult = iota
	ManualOperationPosted
	ManualOperationPostDuplicate
	ManualOperationPostClosed
	ManualOperationPostStale
)

type ManualOperationCloseResult uint8

const (
	ManualOperationCloseInvalid ManualOperationCloseResult = iota
	ManualOperationCloseStarted
	ManualOperationAlreadyClosing
	ManualOperationAlreadyQuiesced
)

type manualOperationMailbox uint32

const (
	manualOperationMailboxEmpty manualOperationMailbox = iota
	manualOperationMailboxPosting
	manualOperationMailboxPosted
	manualOperationMailboxDraining
	manualOperationMailboxDelivered
)

type manualOperationSlot struct {
	// Producer-visible prefix. A target ingress shim resolves the stable source
	// internally, then touches only these aligned atomic uint32 words using the
	// POD OperationID supplied to the backend.
	producerSourceSlot
	mailbox uint32

	// Owner-P-only suffix. A producer never reads an OperationRecord, ParkState,
	// Go pointer, affected link, or coroutine handle.
	record       OperationRecord
	nextAffected uint32
}

// ManualOperationPage is stable owner storage. It may be statically allocated
// by a target and attached only while the source is empty and unbound.
type ManualOperationPage struct {
	slots [ManualOperationPageCapacity]manualOperationSlot
}

// ManualOperationSource is a fixed-capacity, one-shot completion source. It is
// a concrete reference for the four source phases: mailbox publish, affected
// wait-set resolution after a published epoch, logical apply/detach, and physical
// quiescence/recycle. It must remain at a stable address from bind until every
// producer has been strongly joined and UnbindManualOperationSource succeeds.
// It must not be copied after first use.
//
// Post is the only producer-concurrent method. All other mutating methods are
// serialized by owner P. Post does not wake an executor itself: the target shim
// must publish this durable mailbox first and then use the common executor
// request/doorbell path.
type ManualOperationSource struct {
	routedProducerSource
	slots [ManualOperationSourceCapacity]manualOperationSlot
	// extraPages and scanLimit are owner-only. Producer ingress resolves an
	// exact slot from OperationID while an admission lease prevents reuse.
	extraPages   []ManualOperationPage
	dynamicPages operationDynamicPageDirectory
	scanLimit    uint32

	affectedHead uint32
	affectedTail uint32
}

func ManualOperationConfiguredCapacity(source *ManualOperationSource) uint32 {
	if source == nil {
		return 0
	}
	pages := uint32(1+len(source.extraPages)) + source.dynamicPages.published()
	if pages > operationCatalogMaximumPageCount {
		return 0
	}
	return pages * ManualOperationPageCapacity
}

func manualOperationSlotAt(source *ManualOperationSource, index uint32) (*manualOperationSlot, bool) {
	if source == nil || index >= ManualOperationConfiguredCapacity(source) {
		return nil, false
	}
	if index < ManualOperationPageCapacity {
		return &source.slots[index], true
	}
	page := index / ManualOperationPageCapacity
	offset := index % ManualOperationPageCapacity
	catalog, ok := manualOperationPageAt(source, page)
	if !ok {
		return nil, false
	}
	return &catalog.slots[offset], true
}

func manualOperationPageAt(source *ManualOperationSource, page uint32) (*ManualOperationPage, bool) {
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
	return (*ManualOperationPage)(dynamic), true
}

func ManualOperationScanLimit(source *ManualOperationSource) (uint32, bool) {
	if source == nil || source.scanLimit > ManualOperationConfiguredCapacity(source) {
		return 0, false
	}
	return source.scanLimit, true
}

func manualOperationSlotFor(source *ManualOperationSource, id OperationID) (*manualOperationSlot, bool) {
	if source == nil || !source.route.Valid() || !id.Valid() || id.Source() != OperationSourceManual ||
		id.Route() != source.route || id.LocalSlot() == 0 || id.LocalSlot() > ManualOperationConfiguredCapacity(source) {
		return nil, false
	}
	return manualOperationSlotAt(source, id.LocalSlot()-1)
}

func manualOperationReusableSlot(source *ManualOperationSource, slot *manualOperationSlot, index uint32) bool {
	if slot == nil || !producerSourceSlotReusable(&slot.producerSourceSlot) ||
		preemptLoad(&slot.mailbox) != uint32(manualOperationMailboxEmpty) || slot.nextAffected != 0 {
		return false
	}
	generation := preemptLoad(&slot.generation)
	if generation == 0 {
		return slot.record == (OperationRecord{})
	}
	if source == nil || !source.route.Valid() {
		return false
	}
	id, ok := MakeOperationIDAtRoute(OperationSourceManual, source.route, index+1, generation)
	return ok && slot.record == (OperationRecord{id: id, phase: operationReusable})
}

// ConfigureManualOperationPages attaches stable target-owned pages before the
// source is bound. Configuration is monotonic and idempotent for the same
// backing array, matching the timer/poll/worker catalog lifecycle.
func ConfigureManualOperationPages(source *ManualOperationSource, pages []ManualOperationPage) bool {
	if source == nil || len(pages) > int(operationLocalMask/ManualOperationPageCapacity)-1 {
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
	if source.route.Valid() || source.owner != nil || source.pending != 0 || source.scanLimit != 0 ||
		source.affectedHead != 0 || source.affectedTail != 0 {
		return false
	}
	for page := existing; page < len(pages); page++ {
		for offset := range pages[page].slots {
			if pages[page].slots[offset] != (manualOperationSlot{}) {
				return false
			}
		}
	}
	source.extraPages = pages
	return true
}

// CanReserveManualOperation is the allocation-free owner preflight used before
// BeginParkSet mutates a logical wait transaction.
func CanReserveManualOperation(p *P, source *ManualOperationSource) bool {
	if !validManualOperationOwner(source, p) {
		return false
	}
	for index := uint32(0); index < ManualOperationConfiguredCapacity(source); index++ {
		slot, ok := manualOperationSlotAt(source, index)
		if !ok {
			return false
		}
		if preemptLoad(&slot.generation) != ^uint32(0) && manualOperationReusableSlot(source, slot, index) {
			return true
		}
	}
	return false
}

// AttachManualOperationPage publishes one pristine target-owned page while
// the source is bound. Producer admission pins an exact immutable slot address.
func AttachManualOperationPage(
	source *ManualOperationSource,
	p *P,
	page *ManualOperationPage,
	directoryBlock *OperationPageDirectoryBlock,
) bool {
	if !validManualOperationOwner(source, p) || page == nil {
		return false
	}
	oldCapacity := ManualOperationConfiguredCapacity(source)
	if oldCapacity == 0 || oldCapacity > ManualOperationMaximumCapacity-ManualOperationPageCapacity {
		return false
	}
	for index := range source.extraPages {
		if &source.extraPages[index] == page {
			return false
		}
	}
	for offset := range page.slots {
		if !manualOperationReusableSlot(source, &page.slots[offset], oldCapacity+uint32(offset)) {
			return false
		}
	}
	return source.dynamicPages.publish(unsafe.Pointer(page), directoryBlock)
}

func validManualOperationOwner(source *ManualOperationSource, p *P) bool {
	return source != nil && validRoutedProducerSource(&source.routedProducerSource, p)
}

func validManualOperationLiveSlot(source *ManualOperationSource, p *P, index uint32) bool {
	if !validManualOperationOwner(source, p) || index >= ManualOperationConfiguredCapacity(source) {
		return false
	}
	slot, ok := manualOperationSlotAt(source, index)
	if !ok {
		return false
	}
	state := producerSourceLifecycle(preemptLoad(&slot.state))
	if state != producerSourceActive && state != producerSourceClosing && state != producerSourceQuiesced {
		return false
	}
	generation := preemptLoad(&slot.generation)
	id, ok := MakeOperationIDAtRoute(OperationSourceManual, source.route, index+1, generation)
	return ok && slot.record.Matches(id)
}

// ReserveAndAttachManualOperation reserves one physical slot generation and
// attaches its stable OperationRecord to a preparing logical wait-set. No
// producer is admitted until all owner pointers are initialized and Active is
// release-published.
func (source *ManualOperationSource) reserveAndAttach(p *P, state *ParkState, ticket ParkTicket, wait *WaitSetRecord, caseID uint32) (OperationID, bool) {
	if !validManualOperationOwner(source, p) {
		return OperationID{}, false
	}
	for index := uint32(0); index < ManualOperationConfiguredCapacity(source); index++ {
		slot, slotOK := manualOperationSlotAt(source, index)
		if !slotOK {
			return OperationID{}, false
		}
		if !manualOperationReusableSlot(source, slot, index) || preemptLoad(&slot.generation) == ^uint32(0) {
			continue
		}
		generation, begun := beginProducerSourceSlot(&slot.producerSourceSlot)
		if !begun {
			return OperationID{}, false
		}
		id, ok := MakeOperationIDAtRoute(OperationSourceManual, source.route, index+1, generation)
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
		if index+1 > source.scanLimit {
			source.scanLimit = index + 1
		}
		return id, true
	}
	return OperationID{}, false
}

func (source *ManualOperationSource) ReserveAndAttach(p *P, state *ParkState, ticket ParkTicket, caseID uint32) (OperationID, bool) {
	return source.reserveAndAttach(p, state, ticket, nil, caseID)
}

// ReserveAndAttachWait is the scheduler-integrated form. wait is caller-owned
// stable storage in the direct-parking coroutine frame; callbacks never see it.
func (source *ManualOperationSource) ReserveAndAttachWait(p *P, state *ParkState, ticket ParkTicket, wait *WaitSetRecord, caseID uint32) (OperationID, bool) {
	return source.reserveAndAttach(p, state, ticket, wait, caseID)
}

// Post publishes one pointer-free sticky mailbox fact. The OperationID is the
// complete producer ABI; generation is validated only while an admission lease
// pins the stable slot against recycle.
func (source *ManualOperationSource) Post(id OperationID) ManualOperationPostResult {
	slot, ok := manualOperationSlotFor(source, id)
	if !ok {
		return ManualOperationPostInvalid
	}
	switch acquireProducerSourceGeneration(&slot.producerSourceSlot, id.Generation) {
	case producerSourceAcquireClosed:
		return ManualOperationPostClosed
	case producerSourceAcquireStale:
		return ManualOperationPostStale
	case producerSourceAcquired:
	default:
		return ManualOperationPostInvalid
	}
	if preemptLoad(&slot.state) != uint32(producerSourceActive) {
		producerAdmissionRelease(&slot.inflight)
		return ManualOperationPostClosed
	}
	for {
		mailbox := manualOperationMailbox(preemptLoad(&slot.mailbox))
		switch mailbox {
		case manualOperationMailboxEmpty:
			if !preemptCompareAndSwap(&slot.mailbox, uint32(mailbox), uint32(manualOperationMailboxPosting)) {
				continue
			}
			// A payload-bearing source writes its scalar payload here, before the
			// release store of Posted. ManualOperationSource has no payload.
			preemptStore(&slot.mailbox, uint32(manualOperationMailboxPosted))
			preemptStore(&source.pending, 1)
			producerAdmissionRelease(&slot.inflight)
			return ManualOperationPosted
		case manualOperationMailboxPosting, manualOperationMailboxPosted, manualOperationMailboxDraining, manualOperationMailboxDelivered:
			producerAdmissionRelease(&slot.inflight)
			return ManualOperationPostDuplicate
		default:
			producerAdmissionRelease(&slot.inflight)
			return ManualOperationPostInvalid
		}
	}
}

func (source *ManualOperationSource) Pending() bool {
	return source != nil && routedProducerPending(&source.routedProducerSource)
}

// RequestCancel publishes a logical operation cancellation for one active
// scheduler-integrated manual wait. Source-independent task cancellation uses
// the same WaitSetRecord gate directly.
func (source *ManualOperationSource) RequestCancel(p *P, wait *WaitSetRecord) bool {
	return validManualOperationOwner(source, p) && RequestWaitSetCancel(p, wait, ParkCancelOperation)
}

func (source *ManualOperationSource) appendAffected(index uint32) bool {
	oneBased := index + 1
	if source.affectedHead == 0 {
		if source.affectedTail != 0 {
			return false
		}
		source.affectedHead, source.affectedTail = oneBased, oneBased
		return true
	}
	if source.affectedTail == 0 || source.affectedTail > ManualOperationConfiguredCapacity(source) {
		return false
	}
	tail, ok := manualOperationSlotAt(source, source.affectedTail-1)
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

// PublishPass turns producer mailboxes into owner-only sticky OperationRecord
// facts. Lost counts a completion that arrived after another case or cancel had
// already chosen the logical outcome; it is normal and is not enqueued for
// resolution again.
func (source *ManualOperationSource) beginPublishPass(p *P) bool {
	return source != nil && beginRoutedProducerPass(&source.routedProducerSource, p)
}

// publishSlot visits one exact producer mailbox. Producer publication after
// an earlier cursor position stays sticky with pending set for the next epoch;
// it cannot hold the current catalog pass open.
func (source *ManualOperationSource) publishSlot(p *P, index uint32) (published, lost uint32, ok bool) {
	if !validManualOperationOwner(source, p) || index >= source.scanLimit {
		return 0, 0, false
	}
	slot, slotOK := manualOperationSlotAt(source, index)
	if !slotOK {
		return 0, 0, false
	}
	mailbox := manualOperationMailbox(preemptLoad(&slot.mailbox))
	if mailbox == manualOperationMailboxPosting || mailbox == manualOperationMailboxEmpty || mailbox == manualOperationMailboxDelivered {
		return 0, 0, true
	}
	if mailbox != manualOperationMailboxPosted ||
		!preemptCompareAndSwap(&slot.mailbox, uint32(manualOperationMailboxPosted), uint32(manualOperationMailboxDraining)) ||
		!validManualOperationLiveSlot(source, p, index) {
		return 0, 0, false
	}
	id := slot.record.id
	switch result := PublishOperationCompletion(&slot.record, id); result {
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
		lost = 1
	default:
		return 0, 0, false
	}
	preemptStore(&slot.mailbox, uint32(manualOperationMailboxDelivered))
	return published, lost, true
}

func (source *ManualOperationSource) PublishPass(p *P) (published, lost uint32, ok bool) {
	if !source.beginPublishPass(p) {
		return 0, 0, false
	}
	limit, valid := ManualOperationScanLimit(source)
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

func addManualOperationResolution(total *CompletionResolution, resolution CompletionResolution) {
	total.WaitSets += resolution.WaitSets
	total.Completed += resolution.Completed
	total.Canceled += resolution.Canceled
	total.Defaulted += resolution.Defaulted
	total.Winners += resolution.Winners
	total.Losers += resolution.Losers
}

// ResolveAffectedPublishedEpoch consumes this source's intrusive affected
// chain after one complete bounded SourceSet publication pass. The caller must
// run every source's resolve pass before any source's ApplyAndDetach pass.
func (source *ManualOperationSource) ResolveAffectedPublishedEpoch(p *P) (total CompletionResolution, duplicates uint32, ok bool) {
	if !validManualOperationOwner(source, p) {
		return CompletionResolution{}, 0, false
	}
	for source.affectedHead != 0 {
		if source.affectedHead > ManualOperationConfiguredCapacity(source) {
			return total, duplicates, false
		}
		index := source.affectedHead - 1
		slot, slotOK := manualOperationSlotAt(source, index)
		if !slotOK {
			return total, duplicates, false
		}
		if !validManualOperationLiveSlot(source, p, index) {
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
			addManualOperationResolution(&total, resolution)
		case affectedOperationAlreadyResolved:
			duplicates++
		}
	}
	return total, duplicates, source.affectedTail == 0
}

// standaloneAffected reports source-local work created by ReserveAndAttach
// without a WaitSetRecord. ExecutorSourceSet must reject this shape before
// logical resolution: its production apply phase is intentionally driven only
// by the resolved scheduler batch, while standalone callers retain the explicit
// ResolveAffectedPublishedEpoch plus ApplyAndDetach sequence.
func (source *ManualOperationSource) standaloneAffected(p *P) (affected, ok bool) {
	if !validManualOperationOwner(source, p) || (source.affectedHead == 0) != (source.affectedTail == 0) {
		return false, false
	}
	return source.affectedHead != 0, true
}

func (source *ManualOperationSource) beginCloseSlot(p *P, id OperationID) ManualOperationCloseResult {
	slot, ok := manualOperationSlotFor(source, id)
	if !ok || !validManualOperationOwner(source, p) || preemptLoad(&slot.generation) != id.Generation || !slot.record.Matches(id) {
		return ManualOperationCloseInvalid
	}
	switch beginProducerSourceClose(&slot.producerSourceSlot) {
	case producerSourceCloseStarted:
		return ManualOperationCloseStarted
	case producerSourceAlreadyClosing:
		return ManualOperationAlreadyClosing
	case producerSourceAlreadyQuiesced:
		return ManualOperationAlreadyQuiesced
	default:
		return ManualOperationCloseInvalid
	}
}

// BeginClose seals producer admission for one exact generation. The caller must
// physically cancel/unregister its backend and strong-join every callback before
// ConfirmQuiesced; a callback admitted before the seal may still publish a late
// mailbox, which PublishPass classifies as Lost after logical detach.
func (source *ManualOperationSource) BeginClose(p *P, id OperationID) ManualOperationCloseResult {
	return source.beginCloseSlot(p, id)
}

// ApplyOne applies one terminal logical disposition reached through an exact
// source-owned record identity. It is the production SourceSet path: unrelated
// live slots are neither inspected nor changed. Closing producer admission is
// independent of physical quiescence, so the ParkLink may detach immediately
// after the source has acknowledged winner/loser/canceled disposition.
func (source *ManualOperationSource) ApplyOne(p *P, id OperationID, record *OperationRecord) OperationApplyResult {
	slot, ok := manualOperationSlotFor(source, id)
	if !ok || !validManualOperationOwner(source, p) || preemptLoad(&slot.generation) != id.Generation ||
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
	closeResult := source.beginCloseSlot(p, id)
	if closeResult != ManualOperationCloseStarted && closeResult != ManualOperationAlreadyClosing &&
		closeResult != ManualOperationAlreadyQuiesced {
		return OperationApplyInvalid
	}
	if disposition != OperationDispositionWinner && slot.record.resultState == operationResultOwned &&
		!DiscardUnselectedOperationResult(&slot.record, id) {
		return OperationApplyInvalid
	}
	if !slot.record.resolutionApplied && !AcknowledgeOperationResolution(&slot.record, id, disposition) {
		return OperationApplyInvalid
	}
	park, ticket, wait := slot.record.link.park, slot.record.link.ticket, slot.record.link.wait
	detached := wait != nil && DetachParkWaitOperation(park, ticket, &slot.record, id) ||
		wait == nil && DetachParkOperation(park, ticket, &slot.record, id)
	if !detached {
		return OperationApplyInvalid
	}
	return OperationApplyDetached
}

// ApplyAndDetach is the standalone/legacy convenience path. It intentionally
// retains its all-capacity scan for callers which do not carry a WaitSetRecord;
// ExecutorSourceSet never calls it. Physical quiescence is not a prerequisite
// for logical detach or ParkReady.
func (source *ManualOperationSource) ApplyAndDetach(p *P) (applied, detached uint32, ok bool) {
	if !validManualOperationOwner(source, p) || source.affectedHead != 0 || source.affectedTail != 0 {
		return 0, 0, false
	}
	limit, valid := ManualOperationScanLimit(source)
	if !valid {
		return 0, 0, false
	}
	for index := uint32(0); index < limit; index++ {
		slot, slotOK := manualOperationSlotAt(source, index)
		if !slotOK {
			return applied, detached, false
		}
		state := producerSourceLifecycle(preemptLoad(&slot.state))
		if state == producerSourceFree {
			if !manualOperationReusableSlot(source, slot, index) {
				return applied, detached, false
			}
			continue
		}
		if !validManualOperationLiveSlot(source, p, index) {
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

// ConfirmQuiesced accepts the caller's strong backend join assertion. The
// closed inflight word additionally proves that every Post which entered the
// source shim has returned. It is independent of logical detach.
func (source *ManualOperationSource) ConfirmQuiesced(p *P, id OperationID) bool {
	slot, ok := manualOperationSlotFor(source, id)
	mailbox := manualOperationMailbox(0)
	if ok {
		mailbox = manualOperationMailbox(preemptLoad(&slot.mailbox))
	}
	if !ok || !validManualOperationOwner(source, p) || preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.state) != uint32(producerSourceClosing) || !producerSourceSlotQuiesced(&slot.producerSourceSlot) ||
		(mailbox != manualOperationMailboxEmpty && mailbox != manualOperationMailboxDelivered) ||
		!ConfirmOperationQuiesced(&slot.record, id) {
		return false
	}
	return markProducerSourceQuiesced(&slot.producerSourceSlot)
}

func (source *ManualOperationSource) TakeResult(p *P, lease OperationResultLease) bool {
	id, ok := lease.ID()
	if !ok || !validManualOperationOwner(source, p) {
		return false
	}
	slot, ok := manualOperationSlotFor(source, id)
	return ok && preemptLoad(&slot.generation) == id.Generation && TakeOperationResult(&slot.record, lease)
}

// DiscardResult releases an exact winner lease when cleanup suppresses its
// continuation instead of copying the source payload.
func (source *ManualOperationSource) DiscardResult(p *P, lease OperationResultLease) bool {
	id, ok := lease.ID()
	if !ok || !validManualOperationOwner(source, p) {
		return false
	}
	slot, ok := manualOperationSlotFor(source, id)
	return ok && preemptLoad(&slot.generation) == id.Generation && DiscardOperationResult(&slot.record, lease)
}

// Recycle releases a detached exact generation only after producer quiescence,
// mailbox drain, logical-resolution application, and winner-result release.
// The physical slot keeps its last generation and OperationRecord in reusable
// form so the next reservation must advance rather than alias it.
func (source *ManualOperationSource) Recycle(p *P, id OperationID) bool {
	slot, ok := manualOperationSlotFor(source, id)
	if !ok || !validManualOperationOwner(source, p) || source.affectedHead != 0 || source.affectedTail != 0 ||
		preemptLoad(&slot.generation) != id.Generation || preemptLoad(&slot.state) != uint32(producerSourceQuiesced) ||
		!producerSourceSlotQuiesced(&slot.producerSourceSlot) {
		return false
	}
	mailbox := manualOperationMailbox(preemptLoad(&slot.mailbox))
	if (mailbox != manualOperationMailboxEmpty && mailbox != manualOperationMailboxDelivered) ||
		!OperationCanRecycle(&slot.record, id) || !RecycleOperation(&slot.record, id) {
		return false
	}
	slot.nextAffected = 0
	preemptStore(&slot.mailbox, uint32(manualOperationMailboxEmpty))
	if !recycleProducerSourceSlot(&slot.producerSourceSlot) {
		return false
	}
	if id.LocalSlot() == source.scanLimit {
		for source.scanLimit != 0 {
			last, ok := manualOperationSlotAt(source, source.scanLimit-1)
			if !ok {
				return false
			}
			if producerSourceLifecycle(preemptLoad(&last.state)) != producerSourceFree {
				break
			}
			source.scanLimit--
		}
	}
	return true
}

func manualOperationSourceEmpty(source *ManualOperationSource, owner *P) bool {
	if source == nil || !routedProducerHeaderEmpty(&source.routedProducerSource, owner) ||
		source.scanLimit != 0 || source.affectedHead != 0 || source.affectedTail != 0 {
		return false
	}
	for index := uint32(0); index < ManualOperationConfiguredCapacity(source); index++ {
		slot, ok := manualOperationSlotAt(source, index)
		if !ok || !manualOperationReusableSlot(source, slot, index) {
			return false
		}
	}
	return true
}

func BindManualOperationSourceAtRoute(source *ManualOperationSource, p *P, route RouteID) bool {
	if !manualOperationSourceEmpty(source, nil) {
		return false
	}
	return bindRoutedProducerSource(&source.routedProducerSource, p, route)
}

// BindManualOperationSource is the legacy single-P binding. Its IDs are
// explicitly scoped to route 1 and must not be inserted into another route's
// ingress table.
func BindManualOperationSource(source *ManualOperationSource, p *P) bool {
	return BindManualOperationSourceAtRoute(source, p, RouteID(1))
}

func UnbindManualOperationSource(source *ManualOperationSource, p *P) bool {
	if p == nil || !manualOperationSourceEmpty(source, p) {
		return false
	}
	return unbindRoutedProducerSource(&source.routedProducerSource, p)
}

func (source *ManualOperationSource) CanRelease() bool {
	return manualOperationSourceEmpty(source, nil)
}

func (source *ManualOperationSource) Route() (RouteID, bool) {
	if source == nil {
		return 0, false
	}
	return routedProducerRoute(&source.routedProducerSource)
}
