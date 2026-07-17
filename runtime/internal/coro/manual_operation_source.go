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

// ManualOperationSourceCapacity is deliberately small: this source is the
// allocation-free third-source/reference implementation, not the target I/O
// catalog. A target may copy the same slot protocol into a larger generated
// source without changing OperationRecord or ParkState.
const ManualOperationSourceCapacity = 4

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

type manualOperationLifecycle uint32

const (
	manualOperationFree manualOperationLifecycle = iota
	manualOperationInitializing
	manualOperationActive
	manualOperationClosing
	manualOperationQuiesced
)

type manualOperationMailbox uint32

const (
	manualOperationMailboxEmpty manualOperationMailbox = iota
	manualOperationMailboxPosting
	manualOperationMailboxPosted
	manualOperationMailboxDraining
	manualOperationMailboxDelivered
)

const (
	manualOperationProducerClosed = uint32(1 << 31)
	manualOperationProducerMask   = manualOperationProducerClosed - 1
)

type manualOperationSlot struct {
	// Producer-visible prefix. A target ingress shim resolves the stable source
	// internally, then touches only these aligned atomic uint32 words using the
	// POD OperationID supplied to the backend.
	state      uint32
	generation uint32
	inflight   uint32
	mailbox    uint32

	// Owner-P-only suffix. A producer never reads an OperationRecord, ParkState,
	// Go pointer, affected link, or coroutine handle.
	record       OperationRecord
	nextAffected uint32
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
	pending uint32
	slots   [ManualOperationSourceCapacity]manualOperationSlot

	owner        *P
	route        RouteID
	affectedHead uint32
	affectedTail uint32
}

func manualOperationSlotFor(source *ManualOperationSource, id OperationID) (*manualOperationSlot, bool) {
	if source == nil || !source.route.Valid() || !id.Valid() || id.Source() != OperationSourceManual ||
		id.Route() != source.route || id.LocalSlot() == 0 || id.LocalSlot() > ManualOperationSourceCapacity {
		return nil, false
	}
	return &source.slots[id.LocalSlot()-1], true
}

func manualOperationAcquireProducer(slot *manualOperationSlot) bool {
	if slot == nil {
		return false
	}
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&manualOperationProducerClosed != 0 || inflight&manualOperationProducerMask == manualOperationProducerMask {
			return false
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight+1) {
			return true
		}
	}
}

func manualOperationReleaseProducer(slot *manualOperationSlot) {
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&manualOperationProducerMask == 0 {
			return
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight-1) {
			return
		}
	}
}

func manualOperationSealProducers(slot *manualOperationSlot) bool {
	if slot == nil {
		return false
	}
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&manualOperationProducerClosed != 0 {
			return true
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight|manualOperationProducerClosed) {
			return true
		}
	}
}

func manualOperationProducersQuiesced(slot *manualOperationSlot) bool {
	return slot != nil && preemptLoad(&slot.inflight) == manualOperationProducerClosed
}

func manualOperationReusableSlot(source *ManualOperationSource, slot *manualOperationSlot, index uint32) bool {
	if slot == nil || preemptLoad(&slot.state) != uint32(manualOperationFree) ||
		preemptLoad(&slot.mailbox) != uint32(manualOperationMailboxEmpty) || slot.nextAffected != 0 {
		return false
	}
	generation := preemptLoad(&slot.generation)
	if generation == 0 {
		return preemptLoad(&slot.inflight) == 0 && slot.record == (OperationRecord{})
	}
	if source == nil || !source.route.Valid() {
		return false
	}
	id, ok := MakeOperationIDAtRoute(OperationSourceManual, source.route, index+1, generation)
	return ok && preemptLoad(&slot.inflight) == manualOperationProducerClosed &&
		slot.record == (OperationRecord{id: id, phase: operationReusable})
}

func validManualOperationOwner(source *ManualOperationSource, p *P) bool {
	return source != nil && p != nil && source.owner == p && source.route.Valid()
}

func validManualOperationLiveSlot(source *ManualOperationSource, p *P, index uint32) bool {
	if !validManualOperationOwner(source, p) || index >= uint32(len(source.slots)) {
		return false
	}
	slot := &source.slots[index]
	state := manualOperationLifecycle(preemptLoad(&slot.state))
	if state != manualOperationActive && state != manualOperationClosing && state != manualOperationQuiesced {
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
	for index := range source.slots {
		slot := &source.slots[index]
		generation := preemptLoad(&slot.generation)
		if generation == ^uint32(0) || !manualOperationReusableSlot(source, slot, uint32(index)) ||
			!preemptCompareAndSwap(&slot.state, uint32(manualOperationFree), uint32(manualOperationInitializing)) {
			continue
		}
		if !manualOperationSealProducers(slot) || !manualOperationProducersQuiesced(slot) {
			return OperationID{}, false
		}

		var id OperationID
		var ok bool
		if generation == 0 {
			id, ok = MakeOperationIDAtRoute(OperationSourceManual, source.route, uint32(index)+1, 1)
			ok = ok && InitOperation(&slot.record, id)
		} else {
			id, ok = RearmOperation(&slot.record)
			ok = ok && id.Generation == generation+1 && id.Source() == OperationSourceManual &&
				id.Route() == source.route && id.LocalSlot() == uint32(index)+1
		}
		if !ok {
			return OperationID{}, false
		}
		preemptStore(&slot.generation, id.Generation)
		attached := false
		if wait == nil {
			attached = AttachParkOperation(state, ticket, &slot.record, caseID)
		} else {
			attached = AttachParkWaitOperation(state, ticket, wait, &slot.record, caseID)
		}
		if !attached {
			if !AbortReservedOperation(&slot.record, id) {
				return OperationID{}, false
			}
			preemptStore(&slot.state, uint32(manualOperationFree))
			return OperationID{}, false
		}
		if !preemptCompareAndSwap(&slot.inflight, manualOperationProducerClosed, 0) {
			return OperationID{}, false
		}
		preemptStore(&slot.state, uint32(manualOperationActive))
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
	if !manualOperationAcquireProducer(slot) {
		return ManualOperationPostClosed
	}
	if preemptLoad(&slot.generation) != id.Generation {
		manualOperationReleaseProducer(slot)
		return ManualOperationPostStale
	}
	if preemptLoad(&slot.state) != uint32(manualOperationActive) {
		manualOperationReleaseProducer(slot)
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
			manualOperationReleaseProducer(slot)
			return ManualOperationPosted
		case manualOperationMailboxPosting, manualOperationMailboxPosted, manualOperationMailboxDraining, manualOperationMailboxDelivered:
			manualOperationReleaseProducer(slot)
			return ManualOperationPostDuplicate
		default:
			manualOperationReleaseProducer(slot)
			return ManualOperationPostInvalid
		}
	}
}

func (source *ManualOperationSource) Pending() bool {
	return source != nil && preemptLoad(&source.pending) != 0
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
	if source.affectedTail == 0 || source.affectedTail > uint32(len(source.slots)) {
		return false
	}
	tail := &source.slots[source.affectedTail-1]
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
func (source *ManualOperationSource) PublishPass(p *P) (published, lost uint32, ok bool) {
	if !validManualOperationOwner(source, p) {
		return 0, 0, false
	}
	preemptStore(&source.pending, 0)
	for index := range source.slots {
		slot := &source.slots[index]
		mailbox := manualOperationMailbox(preemptLoad(&slot.mailbox))
		if mailbox == manualOperationMailboxPosting || mailbox == manualOperationMailboxEmpty || mailbox == manualOperationMailboxDelivered {
			continue
		}
		if mailbox != manualOperationMailboxPosted ||
			!preemptCompareAndSwap(&slot.mailbox, uint32(manualOperationMailboxPosted), uint32(manualOperationMailboxDraining)) ||
			!validManualOperationLiveSlot(source, p, uint32(index)) {
			return published, lost, false
		}
		id := slot.record.id
		switch result := PublishOperationCompletion(&slot.record, id); result {
		case OperationCompletionPublished:
			if slot.record.link.wait != nil {
				if !MarkWaitSetAffected(p, slot.record.link.wait) {
					return published, lost, false
				}
			} else if !source.appendAffected(uint32(index)) {
				return published, lost, false
			}
			published++
		case OperationCompletionLost:
			lost++
		default:
			return published, lost, false
		}
		preemptStore(&slot.mailbox, uint32(manualOperationMailboxDelivered))
	}
	return published, lost, true
}

func addManualOperationResolution(total *CompletionResolution, resolution CompletionResolution) {
	total.WaitSets += resolution.WaitSets
	total.Completed += resolution.Completed
	total.Canceled += resolution.Canceled
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
		if source.affectedHead > uint32(len(source.slots)) {
			return total, duplicates, false
		}
		index := source.affectedHead - 1
		slot := &source.slots[index]
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
	for {
		switch state := manualOperationLifecycle(preemptLoad(&slot.state)); state {
		case manualOperationActive:
			if !preemptCompareAndSwap(&slot.state, uint32(state), uint32(manualOperationClosing)) {
				continue
			}
			if !manualOperationSealProducers(slot) {
				return ManualOperationCloseInvalid
			}
			return ManualOperationCloseStarted
		case manualOperationClosing:
			return ManualOperationAlreadyClosing
		case manualOperationQuiesced:
			return ManualOperationAlreadyQuiesced
		default:
			return ManualOperationCloseInvalid
		}
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
	state := manualOperationLifecycle(preemptLoad(&slot.state))
	if state != manualOperationActive && state != manualOperationClosing && state != manualOperationQuiesced {
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
	for index := range source.slots {
		slot := &source.slots[index]
		state := manualOperationLifecycle(preemptLoad(&slot.state))
		if state == manualOperationFree {
			if !manualOperationReusableSlot(source, slot, uint32(index)) {
				return applied, detached, false
			}
			continue
		}
		if !validManualOperationLiveSlot(source, p, uint32(index)) {
			return applied, detached, false
		}
		id := slot.record.id
		if slot.record.phase == operationDetached {
			if state != manualOperationClosing && state != manualOperationQuiesced {
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
		preemptLoad(&slot.state) != uint32(manualOperationClosing) || !manualOperationProducersQuiesced(slot) ||
		(mailbox != manualOperationMailboxEmpty && mailbox != manualOperationMailboxDelivered) ||
		!ConfirmOperationQuiesced(&slot.record, id) {
		return false
	}
	preemptStore(&slot.state, uint32(manualOperationQuiesced))
	return true
}

func (source *ManualOperationSource) TakeResult(p *P, lease OperationResultLease) bool {
	id, ok := lease.ID()
	if !ok || !validManualOperationOwner(source, p) {
		return false
	}
	slot, ok := manualOperationSlotFor(source, id)
	return ok && preemptLoad(&slot.generation) == id.Generation && TakeOperationResult(&slot.record, lease)
}

// Recycle releases a detached exact generation only after producer quiescence,
// mailbox drain, logical-resolution application, and winner-result release.
// The physical slot keeps its last generation and OperationRecord in reusable
// form so the next reservation must advance rather than alias it.
func (source *ManualOperationSource) Recycle(p *P, id OperationID) bool {
	slot, ok := manualOperationSlotFor(source, id)
	if !ok || !validManualOperationOwner(source, p) || source.affectedHead != 0 || source.affectedTail != 0 ||
		preemptLoad(&slot.generation) != id.Generation || preemptLoad(&slot.state) != uint32(manualOperationQuiesced) ||
		!manualOperationProducersQuiesced(slot) {
		return false
	}
	mailbox := manualOperationMailbox(preemptLoad(&slot.mailbox))
	if (mailbox != manualOperationMailboxEmpty && mailbox != manualOperationMailboxDelivered) ||
		!OperationCanRecycle(&slot.record, id) || !RecycleOperation(&slot.record, id) {
		return false
	}
	slot.nextAffected = 0
	preemptStore(&slot.mailbox, uint32(manualOperationMailboxEmpty))
	preemptStore(&slot.state, uint32(manualOperationFree))
	return true
}

func manualOperationSourceEmpty(source *ManualOperationSource, owner *P) bool {
	if source == nil || source.owner != owner || preemptLoad(&source.pending) != 0 || source.affectedHead != 0 || source.affectedTail != 0 {
		return false
	}
	for index := range source.slots {
		if !manualOperationReusableSlot(source, &source.slots[index], uint32(index)) {
			return false
		}
	}
	return true
}

func BindManualOperationSourceAtRoute(source *ManualOperationSource, p *P, route RouteID) bool {
	if p == nil || !route.Valid() || !manualOperationSourceEmpty(source, nil) ||
		source.route != 0 && source.route != route {
		return false
	}
	source.route = route
	source.owner = p
	return true
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
	source.owner = nil
	return true
}

func (source *ManualOperationSource) CanRelease() bool {
	return manualOperationSourceEmpty(source, nil)
}

func (source *ManualOperationSource) Route() (RouteID, bool) {
	if source == nil || !source.route.Valid() {
		return 0, false
	}
	return source.route, true
}
