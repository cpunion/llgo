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

// PollOperationPageCapacity is the allocation-free granularity of the
// target-neutral readiness catalog. Every source includes one inline page; a
// target may attach additional stable pages before binding the source.
const PollOperationPageCapacity = 64

// PollOperationSourceCapacity is the default capacity of an unconfigured
// source. Keep this compatibility name for small and embedded profiles; it is
// not the capacity after ConfigurePollOperationPages succeeds.
const PollOperationSourceCapacity = PollOperationPageCapacity

// PollInterest is one independently serialized internal/poll direction.
// internal/poll already prevents two simultaneous readers or two simultaneous
// writers on one descriptor, while read and write may wait concurrently.
type PollInterest uint8

const (
	PollInterestInvalid PollInterest = iota
	PollInterestRead
	PollInterestWrite
)

func validPollInterest(interest PollInterest) bool {
	return interest == PollInterestRead || interest == PollInterestWrite
}

// PollOperationResult is the source result returned to a resumed synchronous
// runtime_pollWait continuation. Ready includes error/hangup readiness: the
// continuation retries the nonblocking syscall and obtains the exact OS error.
// Closing and Timeout preserve internal/poll's stable integer result mapping.
type PollOperationResult uint8

const (
	PollOperationResultInvalid PollOperationResult = iota
	PollOperationReady
	PollOperationClosing
	PollOperationTimeout
)

// PollOperationHandle is the pointer-free identity retained by a native
// reactor and by the synchronous prepare/park/retire transaction. Slot is
// one-based, and Generation never wraps or aliases a stale kernel event.
type PollOperationHandle struct {
	Slot       uint32
	Generation uint32
}

// Valid reports whether handle can identify one physical poll generation.
// Keep adapters independent from the handle's current two-word layout and
// avoid aggregate zero comparisons in stackless coroutine bodies.
func (handle PollOperationHandle) Valid() bool {
	return handle.Slot != 0 && handle.Generation != 0
}

// PollOperationSnapshot is the complete pointer-free input needed by a
// target reactor. A snapshot is valid only for the returned handle generation;
// the owner imports readiness with that handle, never with an fd lookup.
type PollOperationSnapshot struct {
	Handle   PollOperationHandle
	ID       OperationID
	Deadline int64
	FD       int32
	Interest PollInterest
}

// PollOperationPostResult classifies owner-side import of a readiness event.
// The first native slice has no reactor callback thread: target wait returns on
// the executor owner and imports an exact handle before WakeExecutorAt scans
// this source. Keeping a generation-classified result now preserves the same
// ABI if a future multi-P target moves import behind a stable ingress shim.
type PollOperationPostResult uint8

const (
	PollOperationPostInvalid PollOperationPostResult = iota
	PollOperationPosted
	PollOperationPostDuplicate
	PollOperationPostClosed
	PollOperationPostStale
)

type PollOperationUpdateResult uint8

const (
	PollOperationUpdateInvalid PollOperationUpdateResult = iota
	PollOperationUpdated
	PollOperationUpdateClosed
	PollOperationUpdateStale
)

type pollOperationState uint8

const (
	pollOperationFree pollOperationState = iota
	pollOperationInitializing
	pollOperationActive
	pollOperationDelivered
	pollOperationCanceled
)

type pollOperationMailbox uint32

const (
	pollOperationMailboxEmpty pollOperationMailbox = iota
	pollOperationMailboxPosting
	pollOperationMailboxPosted
	pollOperationMailboxDraining
	pollOperationMailboxDelivered
)

type pollOperationSlot struct {
	state      pollOperationState
	interest   PollInterest
	generation uint32
	p          *P
	fd         int32
	deadline   int64

	// v2Producer is a producer-admission header for the exact generation. A
	// reactor producer touches only this header, mailbox and
	// scalar result; every Go pointer remains owner-P-only.
	v2Producer producerSourceSlot
	v2Mailbox  uint32
	v2Result   PollOperationResult

	// record is owner-only stable storage for the select/cancel path.
	record OperationRecord
}

// PollOperationPage is stable storage supplied by a target profile. The core
// owns its private fields and never exposes a Go pointer to the reactor; target
// code observes only pointer-free PollOperationSnapshot values.
type PollOperationPage struct {
	slots [PollOperationPageCapacity]pollOperationSlot
}

// PollOperationSource is the target-neutral scheduler half of a shared native
// fd readiness reactor. It owns no epoll/kqueue descriptor and performs no
// syscall. The executor owner serializes every pointer-bearing field and
// reactor snapshot. A producer concurrently touches only its atomic
// admission/mailbox prefix and scalar result; the current retained native poll
// still imports Ready/Closing on the owner thread. No G, Go pointer, or LLVM
// coroutine handle crosses the target boundary.
//
// deadline is an absolute monotonic nanosecond value; zero means no deadline.
// Active slots participate in the ExecutorSourceSet deadline minimum, so fd
// readiness and timers share one retained target wait.
type PollOperationSource struct {
	pending    uint32
	scanLimit  uint32
	slots      [PollOperationPageCapacity]pollOperationSlot
	extraPages []PollOperationPage
	owner      *P
	route      RouteID
}

// PollOperationConfiguredCapacity returns the exact linear slot capacity. It
// is independent of the target reactor's physical wait-buffer size.
func PollOperationConfiguredCapacity(source *PollOperationSource) uint32 {
	if source == nil {
		return 0
	}
	return uint32(1+len(source.extraPages)) * PollOperationPageCapacity
}

// PollOperationScanLimit returns the scheduler-owned active-prefix bound for
// reactor snapshots and executor service. The boolean rejects corrupt metadata
// instead of silently treating an out-of-range bound as an empty source.
func PollOperationScanLimit(source *PollOperationSource) (uint32, bool) {
	if source == nil {
		return 0, false
	}
	capacity := PollOperationConfiguredCapacity(source)
	return source.scanLimit, validSourceScanLimit(source.scanLimit, capacity)
}

func pollOperationSlotAt(source *PollOperationSource, index uint32) (*pollOperationSlot, bool) {
	capacity := PollOperationConfiguredCapacity(source)
	if index >= capacity {
		return nil, false
	}
	if index < PollOperationPageCapacity {
		return &source.slots[index], true
	}
	page := index/PollOperationPageCapacity - 1
	offset := index % PollOperationPageCapacity
	return &source.extraPages[page].slots[offset], true
}

// ConfigurePollOperationPages attaches stable allocation-free catalog pages
// before bind. Repeating the same configuration is idempotent, and an empty
// unbound source may monotonically expose more pages from the same backing
// pool. The linear slot number and generation still fit the existing two-word
// handle and 15-bit OperationID local field, so no producer or compiler ABI
// changes.
func ConfigurePollOperationPages(source *PollOperationSource, pages []PollOperationPage) bool {
	if source == nil || source.owner != nil || preemptLoad(&source.pending) != 0 ||
		len(pages) > int(operationLocalMask/PollOperationPageCapacity)-1 {
		return false
	}
	existing := len(source.extraPages)
	if existing != 0 && (len(pages) < existing || len(pages) == 0 || &source.extraPages[0] != &pages[0]) {
		return false
	}
	if len(pages) == existing {
		return true
	}
	for index := uint32(0); index < PollOperationConfiguredCapacity(source); index++ {
		slot, ok := pollOperationSlotAt(source, index)
		if !ok || !reusablePollOperationSlot(source, slot, index) {
			return false
		}
	}
	for page := existing; page < len(pages); page++ {
		for offset := range pages[page].slots {
			index := uint32(page+1)*PollOperationPageCapacity + uint32(offset)
			if !reusablePollOperationSlot(source, &pages[page].slots[offset], index) {
				return false
			}
		}
	}
	source.extraPages = pages
	return true
}

func pollOperationSlotFor(source *PollOperationSource, handle PollOperationHandle) (*pollOperationSlot, bool) {
	if source == nil || handle.Slot == 0 || handle.Generation == 0 {
		return nil, false
	}
	return pollOperationSlotAt(source, handle.Slot-1)
}

func pollOperationIDFor(source *PollOperationSource, index, generation uint32) (OperationID, bool) {
	if source == nil {
		return OperationID{}, false
	}
	return MakeOperationIDAtRoute(OperationSourcePoll, source.route, index+1, generation)
}

func validPollOperationRecordResidue(source *PollOperationSource, slot *pollOperationSlot, index uint32) bool {
	if slot == nil {
		return false
	}
	if slot.record == (OperationRecord{}) {
		return true
	}
	id := slot.record.id
	return source != nil && source.route.Valid() && slot.generation != 0 && id.Valid() &&
		id.Source() == OperationSourcePoll && id.Route() == source.route && id.LocalSlot() == index+1 &&
		id.Generation <= slot.generation && slot.record == (OperationRecord{id: id, phase: operationReusable})
}

func reusablePollOperationSlot(source *PollOperationSource, slot *pollOperationSlot, index uint32) bool {
	return slot != nil && slot.state == pollOperationFree &&
		slot.interest == PollInterestInvalid && slot.p == nil && slot.fd == 0 && slot.deadline == 0 &&
		producerSourceSlotReusable(&slot.v2Producer) &&
		preemptLoad(&slot.v2Mailbox) == uint32(pollOperationMailboxEmpty) &&
		slot.v2Result == PollOperationResultInvalid &&
		validPollOperationRecordResidue(source, slot, index)
}

func validLivePollOperation(source *PollOperationSource, slot *pollOperationSlot, owner *P, index uint32) bool {
	if source == nil || slot == nil || slot.generation == 0 ||
		slot.p == nil || owner != nil && slot.p != owner ||
		slot.fd < 0 || !validPollInterest(slot.interest) || slot.deadline < 0 {
		return false
	}
	id, ok := pollOperationIDFor(source, index, slot.generation)
	if !ok || !slot.record.Matches(id) || preemptLoad(&slot.v2Producer.generation) != slot.generation {
		return false
	}
	lifecycle := producerSourceLifecycle(preemptLoad(&slot.v2Producer.state))
	if lifecycle != producerSourceActive && lifecycle != producerSourceClosing && lifecycle != producerSourceQuiesced {
		return false
	}
	mailbox := pollOperationMailbox(preemptLoad(&slot.v2Mailbox))
	if mailbox > pollOperationMailboxDelivered {
		return false
	}
	switch mailbox {
	case pollOperationMailboxEmpty:
		// Empty may concurrently become Posting after this acquire load. The
		// producer owns v2Result from that CAS through release-published Posted,
		// so live validation must not inspect the non-atomic payload here.
	case pollOperationMailboxPosting:
		// The admitted producer owns v2Result until it release-publishes Posted.
		// Do not inspect the non-atomic payload in this transient state.
	case pollOperationMailboxPosted, pollOperationMailboxDraining, pollOperationMailboxDelivered:
		if slot.v2Result != PollOperationReady && slot.v2Result != PollOperationClosing &&
			slot.v2Result != PollOperationTimeout {
			return false
		}
	}
	switch slot.state {
	case pollOperationActive:
		return true
	case pollOperationDelivered:
		return mailbox == pollOperationMailboxDelivered
	case pollOperationCanceled:
		return lifecycle == producerSourceClosing || lifecycle == producerSourceQuiesced
	default:
		return false
	}
}

func pollOperationSourceOwner(source *PollOperationSource, p *P) bool {
	return source != nil && p != nil && source.owner == p && source.route.Valid()
}

// CanReservePollOperationV2 is the allocation-free owner preflight used by a
// compiler/runtime park transaction before it mutates ParkState.
func CanReservePollOperationV2(p *P, source *PollOperationSource) bool {
	if !pollOperationSourceOwner(source, p) {
		return false
	}
	for index := uint32(0); index < PollOperationConfiguredCapacity(source); index++ {
		slot, ok := pollOperationSlotAt(source, index)
		if !ok {
			return false
		}
		if slot.generation != ^uint32(0) && reusablePollOperationSlot(source, slot, index) {
			return true
		}
	}
	return false
}

func beginPollOperationV2Producer(slot *pollOperationSlot, generation uint32) bool {
	if slot == nil || generation == 0 || !producerSourceSlotReusable(&slot.v2Producer) {
		return false
	}
	prepared, ok := beginProducerSourceSlot(&slot.v2Producer)
	if !ok || prepared > generation {
		return false
	}
	// V1 uses may have advanced the authoritative physical generation since the
	// previous V2 reservation. Admission is still sealed in Initializing, so it
	// is safe to skip those consumed generations before release publication.
	preemptStore(&slot.v2Producer.generation, generation)
	return true
}

// ReserveAndAttachPollOperationV2 reserves one exact fd/interest generation
// and attaches its ReadyThenTryCommit record to a scheduler-integrated wait.
// The returned handle and OperationID identify the same shared generation;
// only the ID may be retained by a reactor producer.
func (source *PollOperationSource) ReserveAndAttachPollOperationV2(
	p *P,
	state *ParkState,
	ticket ParkTicket,
	wait *WaitSetRecord,
	caseID uint32,
	fd int32,
	interest PollInterest,
	deadline int64,
) (PollOperationHandle, OperationID, bool) {
	if !pollOperationSourceOwner(source, p) || state == nil || wait == nil || fd < 0 ||
		!validPollInterest(interest) || deadline < 0 {
		return PollOperationHandle{}, OperationID{}, false
	}
	for index := uint32(0); index < PollOperationConfiguredCapacity(source); index++ {
		slot, slotOK := pollOperationSlotAt(source, index)
		if !slotOK || slot.generation == ^uint32(0) || !reusablePollOperationSlot(source, slot, index) {
			continue
		}
		desired, idOK := pollOperationIDFor(source, index, slot.generation+1)
		if !idOK {
			continue
		}
		slot.state = pollOperationInitializing
		if !raiseSourceScanLimit(&source.scanLimit, index, PollOperationConfiguredCapacity(source)) {
			return PollOperationHandle{}, OperationID{}, false
		}
		if !PrepareOperationAtGeneration(&slot.record, desired) ||
			!DeclareOperationCommitMode(&slot.record, OperationCommitReadyThenTryCommit) {
			slot.state = pollOperationFree
			continue
		}
		slot.generation = desired.Generation
		if !beginPollOperationV2Producer(slot, desired.Generation) {
			if !AbortReservedOperation(&slot.record, desired) {
				return PollOperationHandle{}, OperationID{}, false
			}
			slot.state = pollOperationFree
			return PollOperationHandle{}, OperationID{}, false
		}
		if !AttachParkWaitOperation(state, ticket, wait, &slot.record, caseID) {
			if !AbortReservedOperation(&slot.record, desired) ||
				!resetProducerSourceSlot(&slot.v2Producer, desired.Generation) {
				return PollOperationHandle{}, OperationID{}, false
			}
			slot.state = pollOperationFree
			return PollOperationHandle{}, OperationID{}, false
		}
		slot.interest = interest
		slot.p = p
		slot.fd = fd
		slot.deadline = deadline
		slot.state = pollOperationActive
		if !activateProducerSourceSlot(&slot.v2Producer, desired.Generation) {
			return PollOperationHandle{}, OperationID{}, false
		}
		return PollOperationHandle{Slot: index + 1, Generation: desired.Generation}, desired, true
	}
	return PollOperationHandle{}, OperationID{}, false
}

// PostPollOperationV2 publishes one pointer-free readiness/closing event from
// a reactor producer. It never resolves a wait or touches a Go pointer. The
// caller must separately request executor service; the current native retained
// poll imports on the owner thread and therefore needs no cross-route ingress.
func (source *PollOperationSource) PostPollOperationV2(id OperationID, result PollOperationResult) PollOperationPostResult {
	if source == nil || !source.route.Valid() || id.Source() != OperationSourcePoll || id.Route() != source.route ||
		(result != PollOperationReady && result != PollOperationClosing) {
		return PollOperationPostInvalid
	}
	if id.LocalSlot() == 0 || id.LocalSlot() > PollOperationConfiguredCapacity(source) {
		return PollOperationPostInvalid
	}
	slot, ok := pollOperationSlotAt(source, id.LocalSlot()-1)
	if !ok {
		return PollOperationPostInvalid
	}
	switch acquireProducerSourceGeneration(&slot.v2Producer, id.Generation) {
	case producerSourceAcquireClosed:
		return PollOperationPostClosed
	case producerSourceAcquireStale:
		return PollOperationPostStale
	case producerSourceAcquired:
	default:
		return PollOperationPostInvalid
	}
	// An admitted producer is allowed to finish while owner cancellation seals
	// admission. This guarantees either a durable mailbox or a duplicate fact;
	// Apply never waits for an invisible in-flight producer.
	for {
		switch mailbox := pollOperationMailbox(preemptLoad(&slot.v2Mailbox)); mailbox {
		case pollOperationMailboxEmpty:
			if !preemptCompareAndSwap(&slot.v2Mailbox, uint32(mailbox), uint32(pollOperationMailboxPosting)) {
				continue
			}
			slot.v2Result = result
			preemptStore(&slot.v2Mailbox, uint32(pollOperationMailboxPosted))
			preemptStore(&source.pending, 1)
			producerAdmissionRelease(&slot.v2Producer.inflight)
			return PollOperationPosted
		case pollOperationMailboxPosting, pollOperationMailboxPosted,
			pollOperationMailboxDraining, pollOperationMailboxDelivered:
			// Closing Apply may be waiting for this admission to leave. Preserve a
			// source pass even when this event is a duplicate.
			preemptStore(&source.pending, 1)
			producerAdmissionRelease(&slot.v2Producer.inflight)
			return PollOperationPostDuplicate
		default:
			producerAdmissionRelease(&slot.v2Producer.inflight)
			return PollOperationPostInvalid
		}
	}
}

func (source *PollOperationSource) Pending() bool {
	return source != nil && preemptLoad(&source.pending) != 0
}

// SnapshotAt exposes one catalog slot to a target-owned reactor rebuild. ok
// distinguishes a valid inactive slot from a corrupt source; active is true
// only while the exact generation may be armed in the backend. No target may
// retain or inspect source.slots directly.
func (source *PollOperationSource) SnapshotAt(
	p *P,
	index uint32,
) (snapshot PollOperationSnapshot, active, ok bool) {
	if !pollOperationSourceOwner(source, p) || index >= PollOperationConfiguredCapacity(source) {
		return PollOperationSnapshot{}, false, false
	}
	slot, slotOK := pollOperationSlotAt(source, index)
	if !slotOK {
		return PollOperationSnapshot{}, false, false
	}
	switch slot.state {
	case pollOperationFree:
		return PollOperationSnapshot{}, false, reusablePollOperationSlot(source, slot, index)
	case pollOperationActive:
		if !validLivePollOperation(source, slot, p, index) {
			return PollOperationSnapshot{}, false, false
		}
		id, idOK := pollOperationIDFor(source, index, slot.generation)
		if !idOK {
			return PollOperationSnapshot{}, false, false
		}
		if pollOperationMailbox(preemptLoad(&slot.v2Mailbox)) != pollOperationMailboxEmpty {
			return PollOperationSnapshot{}, false, true
		}
		return PollOperationSnapshot{
			Handle:   PollOperationHandle{Slot: index + 1, Generation: slot.generation},
			ID:       id,
			Deadline: slot.deadline,
			FD:       slot.fd,
			Interest: slot.interest,
		}, true, true
	case pollOperationDelivered, pollOperationCanceled:
		return PollOperationSnapshot{}, false, validLivePollOperation(source, slot, p, index)
	default:
		return PollOperationSnapshot{}, false, false
	}
}

// UpdatePollOperationV2Deadline changes one still-reactor-active exact V2
// generation. A readiness/closing publication already in flight wins and
// closes this update; its synchronous continuation will re-register if the
// descriptor remains waitable under a newer logical deadline.
func (source *PollOperationSource) UpdatePollOperationV2Deadline(
	p *P,
	id OperationID,
	deadline int64,
) PollOperationUpdateResult {
	if !pollOperationSourceOwner(source, p) || deadline < 0 || !id.Valid() ||
		id.Source() != OperationSourcePoll || id.Route() != source.route ||
		id.LocalSlot() == 0 || id.LocalSlot() > PollOperationConfiguredCapacity(source) {
		return PollOperationUpdateInvalid
	}
	slot, ok := pollOperationSlotAt(source, id.LocalSlot()-1)
	if !ok {
		return PollOperationUpdateInvalid
	}
	if slot.generation != id.Generation {
		return PollOperationUpdateStale
	}
	if slot.state == pollOperationFree {
		return PollOperationUpdateClosed
	}
	if slot.state != pollOperationActive ||
		!validLivePollOperation(source, slot, p, id.LocalSlot()-1) ||
		preemptLoad(&slot.v2Producer.state) != uint32(producerSourceActive) ||
		pollOperationMailbox(preemptLoad(&slot.v2Mailbox)) != pollOperationMailboxEmpty {
		return PollOperationUpdateClosed
	}
	slot.deadline = deadline
	preemptStore(&source.pending, 1)
	return PollOperationUpdated
}

func (source *PollOperationSource) beginDrainPass(owner *P) bool {
	if source == nil || source.owner != owner {
		return false
	}
	preemptStore(&source.pending, 0)
	return true
}

func (source *PollOperationSource) publishPollOperationV2(
	owner *P,
	slot *pollOperationSlot,
	index uint32,
) (published bool, ok bool) {
	if !validLivePollOperation(source, slot, owner, index) ||
		pollOperationMailbox(preemptLoad(&slot.v2Mailbox)) != pollOperationMailboxDraining {
		return false, false
	}
	id, idOK := pollOperationIDFor(source, index, slot.generation)
	if !idOK {
		return false, false
	}
	switch result := PublishReadyThenTryCommitCandidate(&slot.record, id); result {
	case OperationCompletionPublished, OperationCompletionLost:
		if slot.record.link.wait == nil || !MarkWaitSetAffected(owner, slot.record.link.wait) {
			return false, false
		}
		preemptStore(&slot.v2Mailbox, uint32(pollOperationMailboxDelivered))
		if slot.state == pollOperationActive {
			slot.state = pollOperationDelivered
		}
		return true, true
	case OperationCompletionDeferred:
		if !preemptCompareAndSwap(
			&slot.v2Mailbox,
			uint32(pollOperationMailboxDraining),
			uint32(pollOperationMailboxPosted),
		) {
			return false, false
		}
		preemptStore(&source.pending, 1)
		return false, true
	default:
		return false, false
	}
}

func (source *PollOperationSource) retryQuiescedPollOperationV2(
	owner *P,
	slot *pollOperationSlot,
	index uint32,
) (bool, bool) {
	if !validLivePollOperation(source, slot, owner, index) {
		return false, false
	}
	lifecycle := producerSourceLifecycle(preemptLoad(&slot.v2Producer.state))
	if lifecycle != producerSourceClosing || !producerSourceSlotQuiesced(&slot.v2Producer) ||
		slot.record.phase != operationActive || slot.record.disposition == OperationDispositionPending {
		return false, true
	}
	if slot.record.link.wait == nil || !MarkWaitSetAffected(owner, slot.record.link.wait) {
		return false, false
	}
	return true, true
}

func (source *PollOperationSource) drainSlotFor(
	owner *P,
	now int64,
	index uint32,
) (completed int, deadline int64, hasDeadline, ok bool) {
	if source == nil || source.owner != owner || now < 0 || index >= PollOperationConfiguredCapacity(source) {
		return 0, 0, false, false
	}
	slot, slotOK := pollOperationSlotAt(source, index)
	if !slotOK {
		return 0, 0, false, false
	}
	switch slot.state {
	case pollOperationFree:
		if !reusablePollOperationSlot(source, slot, index) {
			return 0, 0, false, false
		}
	case pollOperationActive:
		if !validLivePollOperation(source, slot, owner, index) {
			return 0, 0, false, false
		}
		switch mailbox := pollOperationMailbox(preemptLoad(&slot.v2Mailbox)); mailbox {
		case pollOperationMailboxPosting:
			preemptStore(&source.pending, 1)
			if slot.deadline > 0 {
				return 0, slot.deadline, true, true
			}
			return 0, 0, false, true
		case pollOperationMailboxPosted:
			if !preemptCompareAndSwap(&slot.v2Mailbox, uint32(mailbox), uint32(pollOperationMailboxDraining)) {
				preemptStore(&source.pending, 1)
				return 0, 0, false, true
			}
			// Readiness and the absolute deadline are facts from independent
			// producers. Once the owner observes both in the same source pass,
			// the deadline is authoritative; otherwise a delayed route can let
			// data that arrived after expiry win merely because another P ran
			// first. Closing remains authoritative over timeout and is handled
			// unchanged by the synchronous continuation.
			if slot.v2Result == PollOperationReady && slot.deadline > 0 && slot.deadline <= now {
				slot.v2Result = PollOperationTimeout
			}
			published, publishOK := source.publishPollOperationV2(owner, slot, index)
			if !publishOK {
				return 0, 0, false, false
			}
			if published {
				return 1, 0, false, true
			}
		case pollOperationMailboxEmpty:
			if slot.deadline > 0 && slot.deadline <= now {
				if !preemptCompareAndSwap(&slot.v2Mailbox, uint32(mailbox), uint32(pollOperationMailboxDraining)) {
					preemptStore(&source.pending, 1)
					return 0, 0, false, true
				}
				slot.v2Result = PollOperationTimeout
				published, publishOK := source.publishPollOperationV2(owner, slot, index)
				if !publishOK || !published {
					return 0, 0, false, false
				}
				return 1, 0, false, true
			}
			if slot.deadline > 0 {
				return 0, slot.deadline, true, true
			}
			return 0, 0, false, true
		case pollOperationMailboxDraining, pollOperationMailboxDelivered:
			return 0, 0, false, false
		default:
			return 0, 0, false, false
		}
		return 0, 0, false, true
	case pollOperationDelivered, pollOperationCanceled:
		if !validLivePollOperation(source, slot, owner, index) {
			return 0, 0, false, false
		}
		mailbox := pollOperationMailbox(preemptLoad(&slot.v2Mailbox))
		if mailbox == pollOperationMailboxPosted {
			if !preemptCompareAndSwap(&slot.v2Mailbox, uint32(mailbox), uint32(pollOperationMailboxDraining)) {
				preemptStore(&source.pending, 1)
				return 0, 0, false, true
			}
			published, publishOK := source.publishPollOperationV2(owner, slot, index)
			if !publishOK {
				return 0, 0, false, false
			}
			if published {
				return 1, 0, false, true
			}
		}
		if mailbox == pollOperationMailboxPosting {
			preemptStore(&source.pending, 1)
			return 0, 0, false, true
		}
		if retried, retryOK := source.retryQuiescedPollOperationV2(owner, slot, index); !retryOK {
			return 0, 0, false, false
		} else if retried {
			return 1, 0, false, true
		}
	default:
		return 0, 0, false, false
	}
	return 0, 0, false, true
}

func (source *PollOperationSource) drainFor(owner *P, now int64) (completed int, deadline int64, hasDeadline, ok bool) {
	if !source.beginDrainPass(owner) || now < 0 {
		return 0, 0, false, false
	}
	limit, valid := PollOperationScanLimit(source)
	if !valid {
		return 0, 0, false, false
	}
	for index := uint32(0); index < limit; index++ {
		one, next, hasNext, slotOK := source.drainSlotFor(owner, now, index)
		completed += one
		if !slotOK {
			return completed, 0, false, false
		}
		if hasNext && (!hasDeadline || next < deadline) {
			deadline, hasDeadline = next, true
		}
	}
	return completed, deadline, hasDeadline, true
}

func (source *PollOperationSource) nextDeadlineFor(owner *P) (deadline int64, hasDeadline, ok bool) {
	if source == nil || source.owner != owner {
		return 0, false, false
	}
	limit, valid := PollOperationScanLimit(source)
	if !valid {
		return 0, false, false
	}
	for index := uint32(0); index < limit; index++ {
		slot, slotOK := pollOperationSlotAt(source, index)
		if !slotOK {
			return 0, false, false
		}
		switch slot.state {
		case pollOperationFree:
			if !reusablePollOperationSlot(source, slot, index) {
				return 0, false, false
			}
		case pollOperationActive:
			if !validLivePollOperation(source, slot, owner, index) {
				return 0, false, false
			}
			if slot.deadline > 0 && (!hasDeadline || slot.deadline < deadline) {
				deadline, hasDeadline = slot.deadline, true
			}
		case pollOperationDelivered, pollOperationCanceled:
			if !validLivePollOperation(source, slot, owner, index) {
				return 0, false, false
			}
		default:
			return 0, false, false
		}
	}
	return deadline, hasDeadline, true
}

// RequestPollOperationV2Cancel publishes logical operation cancellation. Task
// abort and executor shutdown use their common stronger WaitSet cancellation
// paths and converge on the same source Apply operation.
func (source *PollOperationSource) RequestPollOperationV2Cancel(p *P, wait *WaitSetRecord) bool {
	return pollOperationSourceOwner(source, p) && RequestWaitSetCancel(p, wait, ParkCancelOperation)
}

// TryCommitPollOperationV2 binds one exact readiness hint selected by the
// common seeded resolver. The retained poll event is already physically
// one-shot; committing only transfers its scalar result to the winner lease.
func (source *PollOperationSource) TryCommitPollOperationV2(request ParkCommitRequest) (ParkCommitAttempt, bool) {
	id, ok := request.ID()
	if !ok || source == nil || id.Source() != OperationSourcePoll || id.Route() != source.route ||
		id.LocalSlot() == 0 || id.LocalSlot() > PollOperationConfiguredCapacity(source) ||
		!currentParkCommitRequest(request) {
		return ParkCommitAttempt{}, false
	}
	slot, slotOK := pollOperationSlotAt(source, id.LocalSlot()-1)
	if !slotOK || slot.generation != id.Generation || &slot.record != request.record ||
		!validLivePollOperation(source, slot, source.owner, id.LocalSlot()-1) ||
		slot.state != pollOperationDelivered ||
		pollOperationMailbox(preemptLoad(&slot.v2Mailbox)) != pollOperationMailboxDelivered {
		return ParkCommitAttempt{}, false
	}
	return BindParkCommitResult(request)
}

func closeUnselectedPollOperationV2Mailbox(slot *pollOperationSlot) bool {
	if slot == nil || !producerSourceSlotQuiesced(&slot.v2Producer) {
		return false
	}
	for {
		switch mailbox := pollOperationMailbox(preemptLoad(&slot.v2Mailbox)); mailbox {
		case pollOperationMailboxEmpty, pollOperationMailboxDelivered:
			return true
		case pollOperationMailboxPosted:
			if preemptCompareAndSwap(&slot.v2Mailbox, uint32(mailbox), uint32(pollOperationMailboxDelivered)) {
				return true
			}
		case pollOperationMailboxPosting:
			return false
		default:
			return false
		}
	}
}

// ApplyPollOperationV2One seals the exact reactor ingress, joins every
// admitted producer, applies the frozen winner/loser/canceled disposition and
// detaches exactly one ParkLink. The current retained native reactor rebuilds
// its fd set only on this owner thread, so admission quiescence is also the
// physical unregister boundary. A future callback/multi-P reactor must insert
// its backend unregister/join before this method can confirm quiescence.
func (source *PollOperationSource) ApplyPollOperationV2One(
	p *P,
	id OperationID,
	record *OperationRecord,
) OperationApplyResult {
	if !pollOperationSourceOwner(source, p) || id.Source() != OperationSourcePoll || id.Route() != source.route ||
		id.LocalSlot() == 0 || id.LocalSlot() > PollOperationConfiguredCapacity(source) {
		return OperationApplyInvalid
	}
	slot, ok := pollOperationSlotAt(source, id.LocalSlot()-1)
	if !ok || slot.generation != id.Generation || &slot.record != record ||
		!validLivePollOperation(source, slot, p, id.LocalSlot()-1) || slot.record.phase != operationActive {
		return OperationApplyInvalid
	}
	disposition, terminal := OperationDispositionOf(&slot.record, id)
	if !terminal || slot.record.link.park == nil || slot.record.link.wait == nil ||
		slot.record.link.operation != &slot.record || slot.record.link.ticket == (ParkTicket{}) {
		return OperationApplyInvalid
	}
	if disposition == OperationDispositionWinner {
		if slot.state != pollOperationDelivered ||
			pollOperationMailbox(preemptLoad(&slot.v2Mailbox)) != pollOperationMailboxDelivered ||
			!operationCandidateIsPublished(&slot.record) {
			return OperationApplyInvalid
		}
	} else if disposition != OperationDispositionLost && disposition != OperationDispositionCanceled {
		return OperationApplyInvalid
	}
	switch beginProducerSourceClose(&slot.v2Producer) {
	case producerSourceCloseStarted, producerSourceAlreadyClosing, producerSourceAlreadyQuiesced:
	default:
		return OperationApplyInvalid
	}
	if disposition != OperationDispositionWinner {
		slot.state = pollOperationCanceled
	}
	if !producerSourceSlotQuiesced(&slot.v2Producer) {
		preemptStore(&source.pending, 1)
		return OperationApplyAwaitExternalFact
	}
	if disposition != OperationDispositionWinner && !closeUnselectedPollOperationV2Mailbox(slot) {
		preemptStore(&source.pending, 1)
		return OperationApplyAwaitExternalFact
	}
	if preemptLoad(&slot.v2Producer.state) == uint32(producerSourceClosing) &&
		!markProducerSourceQuiesced(&slot.v2Producer) {
		return OperationApplyInvalid
	}
	if !slot.record.quiesced && !ConfirmOperationQuiesced(&slot.record, id) {
		return OperationApplyInvalid
	}
	if disposition != OperationDispositionWinner && slot.record.resultState == operationResultOwned &&
		!DiscardUnselectedOperationResult(&slot.record, id) {
		return OperationApplyInvalid
	}
	if !slot.record.resolutionApplied && !AcknowledgeOperationResolution(&slot.record, id, disposition) {
		return OperationApplyInvalid
	}
	park, ticket := slot.record.link.park, slot.record.link.ticket
	if !DetachParkWaitOperation(park, ticket, &slot.record, id) {
		return OperationApplyInvalid
	}
	return OperationApplyDetached
}

func (source *PollOperationSource) releasePollOperationV2Result(
	p *P,
	handle PollOperationHandle,
	lease OperationResultLease,
	discard bool,
) (PollOperationResult, bool) {
	slot, ok := pollOperationSlotFor(source, handle)
	id, idOK := pollOperationIDFor(source, handle.Slot-1, handle.Generation)
	if !ok || !idOK || !pollOperationSourceOwner(source, p) || slot.generation != handle.Generation ||
		slot.state != pollOperationDelivered || slot.record.id != id ||
		!validLivePollOperation(source, slot, p, handle.Slot-1) ||
		slot.record.disposition != OperationDispositionWinner ||
		pollOperationMailbox(preemptLoad(&slot.v2Mailbox)) != pollOperationMailboxDelivered {
		return PollOperationResultInvalid, false
	}
	result := slot.v2Result
	if result == PollOperationResultInvalid {
		return PollOperationResultInvalid, false
	}
	if discard {
		return result, DiscardOperationResult(&slot.record, lease)
	}
	return result, TakeOperationResult(&slot.record, lease)
}

func (source *PollOperationSource) TakePollOperationV2Result(
	p *P,
	handle PollOperationHandle,
	lease OperationResultLease,
) (PollOperationResult, bool) {
	return source.releasePollOperationV2Result(p, handle, lease, false)
}

func (source *PollOperationSource) DiscardPollOperationV2Result(
	p *P,
	handle PollOperationHandle,
	lease OperationResultLease,
) (PollOperationResult, bool) {
	return source.releasePollOperationV2Result(p, handle, lease, true)
}

// RecyclePollOperationV2 clears one detached exact generation only after
// producer quiescence and winner lease release. The record and both physical
// generation counters retain canonical residue for fail-closed ABA rejection.
func (source *PollOperationSource) RecyclePollOperationV2(p *P, handle PollOperationHandle) bool {
	slot, ok := pollOperationSlotFor(source, handle)
	id, idOK := pollOperationIDFor(source, handle.Slot-1, handle.Generation)
	if !ok || !idOK || !pollOperationSourceOwner(source, p) || slot.generation != handle.Generation ||
		(slot.state != pollOperationDelivered && slot.state != pollOperationCanceled) ||
		!validLivePollOperation(source, slot, p, handle.Slot-1) ||
		preemptLoad(&slot.v2Producer.state) != uint32(producerSourceQuiesced) ||
		!producerSourceSlotQuiesced(&slot.v2Producer) ||
		!OperationCanRecycle(&slot.record, id) || !RecycleOperation(&slot.record, id) {
		return false
	}
	slot.interest = PollInterestInvalid
	slot.p = nil
	slot.fd = 0
	slot.deadline = 0
	slot.v2Result = PollOperationResultInvalid
	preemptStore(&slot.v2Mailbox, uint32(pollOperationMailboxEmpty))
	if !recycleProducerSourceSlot(&slot.v2Producer) {
		return false
	}
	slot.state = pollOperationFree
	return true
}

func pollOperationSourceEmpty(source *PollOperationSource, owner *P) bool {
	if source == nil || source.owner != owner || preemptLoad(&source.pending) != 0 {
		return false
	}
	for index := uint32(0); index < PollOperationConfiguredCapacity(source); index++ {
		slot, slotOK := pollOperationSlotAt(source, index)
		if !slotOK || !reusablePollOperationSlot(source, slot, index) {
			return false
		}
	}
	return true
}

func BindPollOperationSourceAtRoute(source *PollOperationSource, p *P, route RouteID) bool {
	if source == nil || p == nil || !route.Valid() || !pollOperationSourceEmpty(source, nil) ||
		source.route != 0 && source.route != route {
		return false
	}
	source.route = route
	source.scanLimit = 0
	source.owner = p
	return true
}

func BindPollOperationSource(source *PollOperationSource, p *P) bool {
	return BindPollOperationSourceAtRoute(source, p, RouteID(1))
}

func UnbindPollOperationSource(source *PollOperationSource, p *P) bool {
	if p == nil || !pollOperationSourceEmpty(source, p) {
		return false
	}
	source.owner = nil
	source.scanLimit = 0
	return true
}

func (source *PollOperationSource) CanRelease() bool {
	return pollOperationSourceEmpty(source, nil)
}

func (source *PollOperationSource) Route() (RouteID, bool) {
	if source == nil || !source.route.Valid() {
		return 0, false
	}
	return source.route, true
}
