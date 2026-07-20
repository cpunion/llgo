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

// ChannelOperationPageCapacity is the allocation-free granularity of the
// target-neutral channel/select catalog. Every source includes one inline
// page; a target may attach additional stable pages before binding it.
const ChannelOperationPageCapacity = 64

// ChannelOperationSourceCapacity is the default capacity of an unconfigured
// source. Keep this compatibility name for small, embedded, and bare-metal
// profiles; it is not the capacity after ConfigureChannelOperationPages.
const ChannelOperationSourceCapacity = ChannelOperationPageCapacity

type channelOperationMailbox uint32

const (
	channelMailboxEmpty channelOperationMailbox = iota
	channelMailboxReady
	channelMailboxForced
	channelMailboxDrainingReady
	channelMailboxDrainingForced
	// A forced peer commit may overtake an owner drain of an ordinary Ready
	// hint. The owner converts this sticky handoff to Forced instead of clearing
	// the mailbox, so publication cannot be lost behind the source cursor.
	channelMailboxForcedBehindReadyDrain
)

type channelPhysicalState uint32

const (
	channelPhysicalIdle channelPhysicalState = iota
	channelPhysicalReady
	channelPhysicalRetryBudget
	channelPhysicalCommitted
)

type channelExternalState uint32

const (
	channelExternalDisabled channelExternalState = iota
	// Reserved means the exact claim/generation mapping is installed, but a
	// typed hchan queue node must not expose the endpoint yet.
	channelExternalReserved
	// Exposed is the release-published certificate that PrepareParkSet has
	// completed and the owner will perform no fallible work before suspending.
	// A peer may acquire the admission/claim and commit either before or after
	// the physical llvm.coro.suspend without reading fields mutated by Resumed.
	channelExternalExposed
)

type channelOperationSlot struct {
	// Producer-concurrent POD prefix. Producers retain only OperationID; source
	// lookup supplies this stable slot and never exports claim or record pointers.
	producerSourceSlot
	mailbox       uint32
	physical      uint32
	external      uint32
	externalLease uint32

	record OperationRecord
	claim  *SelectClaim
}

// ChannelOperationPage is stable target-provided storage. Producer ingress
// still resolves it exclusively through the existing two-word OperationID;
// neither this page pointer nor a frame pointer crosses an ingress ABI.
type ChannelOperationPage struct {
	slots [ChannelOperationPageCapacity]channelOperationSlot
}

// ChannelOperationSource is the paged durable channel/select catalog. The
// atomic slot prefix is producer-concurrent and stays at a stable address through
// strong admission join. record and claim are owner-only suffix fields: claim
// is a temporary pointer into direct-parking frame storage and is cleared by
// Apply immediately after detach. Ordinary source producer shims retain only
// OperationID and never load either suffix field. The pair transaction may
// compare slot.claim with its hchan-node claim only after both endpoint
// admissions are held, and keeps them held across every later claim access.
// Precise stack-map rooting for that node is part of typed hchan/compiler C1,
// not this scalar protocol.
type ChannelOperationSource struct {
	routedProducerSource
	slots      [ChannelOperationPageCapacity]channelOperationSlot
	extraPages []ChannelOperationPage
	scanLimit  uint32
}

// ChannelOperationConfiguredCapacity returns the exact linear slot and scan
// capacity. Linear one-based slots remain encodable in OperationID's frozen
// 15-bit source-local field.
func ChannelOperationConfiguredCapacity(source *ChannelOperationSource) uint32 {
	if source == nil {
		return 0
	}
	return uint32(1+len(source.extraPages)) * ChannelOperationPageCapacity
}

func channelOperationScanLimit(source *ChannelOperationSource) (uint32, bool) {
	if source == nil {
		return 0, false
	}
	capacity := ChannelOperationConfiguredCapacity(source)
	return source.scanLimit, validSourceScanLimit(source.scanLimit, capacity)
}

func channelOperationSlotAt(source *ChannelOperationSource, index uint32) (*channelOperationSlot, bool) {
	if index >= ChannelOperationConfiguredCapacity(source) {
		return nil, false
	}
	if index < ChannelOperationPageCapacity {
		return &source.slots[index], true
	}
	page := index/ChannelOperationPageCapacity - 1
	offset := index % ChannelOperationPageCapacity
	return &source.extraPages[page].slots[offset], true
}

// ConfigureChannelOperationPages attaches stable allocation-free catalog
// pages while the source is empty and unbound. Repeating an identical
// configuration is harmless, and an empty source may monotonically expose
// more of the same backing pool. The slice header is frozen while bound, so
// concurrent producer lookup always observes stable slot addresses.
func ConfigureChannelOperationPages(source *ChannelOperationSource, pages []ChannelOperationPage) bool {
	if source == nil || !routedProducerHeaderEmpty(&source.routedProducerSource, nil) ||
		len(pages) > int(operationLocalMask/ChannelOperationPageCapacity)-1 {
		return false
	}
	existing := len(source.extraPages)
	if existing != 0 && (len(pages) < existing || len(pages) == 0 || &source.extraPages[0] != &pages[0]) {
		return false
	}
	if len(pages) == existing {
		return true
	}
	for index := uint32(0); index < ChannelOperationConfiguredCapacity(source); index++ {
		slot, ok := channelOperationSlotAt(source, index)
		if !ok || !channelOperationReusableSlot(source, slot, index) {
			return false
		}
	}
	for page := existing; page < len(pages); page++ {
		for offset := range pages[page].slots {
			index := uint32(page+1)*ChannelOperationPageCapacity + uint32(offset)
			if !channelOperationReusableSlot(source, &pages[page].slots[offset], index) {
				return false
			}
		}
	}
	source.extraPages = pages
	return true
}

func channelOperationSlotFor(source *ChannelOperationSource, id OperationID) (*channelOperationSlot, bool) {
	if source == nil || !source.route.Valid() || !id.Valid() || id.Source() != OperationSourceChannel ||
		id.Route() != source.route || id.LocalSlot() == 0 || id.LocalSlot() > ChannelOperationConfiguredCapacity(source) {
		return nil, false
	}
	return channelOperationSlotAt(source, id.LocalSlot()-1)
}

func validChannelOperationOwner(source *ChannelOperationSource, p *P) bool {
	_, scanOK := channelOperationScanLimit(source)
	return scanOK && p != nil && p.channelSource == source &&
		validRoutedProducerSource(&source.routedProducerSource, p)
}

func channelOperationReusableSlot(source *ChannelOperationSource, slot *channelOperationSlot, index uint32) bool {
	if source == nil || slot == nil || index >= operationLocalMask ||
		!producerSourceSlotReusable(&slot.producerSourceSlot) ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxEmpty) ||
		preemptLoad(&slot.physical) != uint32(channelPhysicalIdle) || preemptLoad(&slot.external) != 0 ||
		preemptLoad(&slot.externalLease)&1 != 0 || slot.claim != nil {
		return false
	}
	generation := preemptLoad(&slot.generation)
	if generation == 0 {
		return slot.record == (OperationRecord{})
	}
	if !source.route.Valid() {
		return false
	}
	id, ok := MakeOperationIDAtRoute(OperationSourceChannel, source.route, index+1, generation)
	return ok && slot.record == (OperationRecord{id: id, phase: operationReusable})
}

// channelOperationExternalReservable separates an empty lifecycle slot from
// one which can still issue a fresh linear external token. The last even token
// remains lifecycle-reusable so a retired slot can be recycled, unbound, and
// released; claim-backed reservations skip it permanently instead of creating
// an operation whose every external admission must fail contended. A
// claim-less local operation does not consume this token domain.
func channelOperationExternalReservable(slot *channelOperationSlot) bool {
	if slot == nil {
		return false
	}
	lease := preemptLoad(&slot.externalLease)
	return lease&1 == 0 && lease < ^uint32(0)-1
}

func BindChannelOperationSourceAtRoute(source *ChannelOperationSource, p *P, route RouteID) bool {
	if source == nil || p == nil || !route.Valid() || source.owner != nil || preemptLoad(&source.pending) != 0 ||
		p.channelSource != nil || preemptLoad(&p.executorMode) != executorModeUnbound || p.executor != nil ||
		source.route != 0 && source.route != route {
		return false
	}
	previousRoute := source.route
	source.route = route
	for index := uint32(0); index < ChannelOperationConfiguredCapacity(source); index++ {
		slot, ok := channelOperationSlotAt(source, index)
		if !ok || !channelOperationReusableSlot(source, slot, index) {
			source.route = previousRoute
			return false
		}
	}
	if !bindRoutedProducerSource(&source.routedProducerSource, p, route) {
		source.route = previousRoute
		return false
	}
	source.scanLimit = 0
	p.channelSource = source
	return true
}

func BindChannelOperationSource(source *ChannelOperationSource, p *P) bool {
	return BindChannelOperationSourceAtRoute(source, p, RouteID(1))
}

// channelOperationCommitDomainCompatible binds every Channel case in one
// preparing logical wait to the same frame-local SelectClaim, and prevents one
// non-nil claim from being reused by another wait/ticket. The bounded configured
// catalog is scanned before a new slot generation becomes producer-visible. A
// nil/non-nil mix and two distinct claims are both rejected. A claim-less local
// operation is valid only when it is the ParkSet's sole candidate; a larger set
// needs one shared claim even if only one case is Channel. Otherwise two peers
// could acquire independent claims, or production discovery could encounter a
// multi-candidate Channel set it cannot arbitrate, after the slots were already
// producer-visible.
func channelOperationCommitDomainCompatible(
	source *ChannelOperationSource,
	state *ParkState,
	ticket ParkTicket,
	wait *WaitSetRecord,
	claim *SelectClaim,
) bool {
	if source == nil || state == nil || wait == nil || !validParkTicket(ticket) ||
		claim == nil && state.expected != 1 {
		return false
	}
	limit, valid := channelOperationScanLimit(source)
	if !valid {
		return false
	}
	for index := uint32(0); index < limit; index++ {
		slot, ok := channelOperationSlotAt(source, index)
		if !ok {
			return false
		}
		record := &slot.record
		samePark := record.link.park == state
		sameClaim := claim != nil && slot.claim == claim
		if !samePark && !sameClaim {
			continue
		}
		lifecycle := producerSourceLifecycle(preemptLoad(&slot.state))
		if lifecycle != producerSourceActive && lifecycle != producerSourceClosing ||
			record.phase != operationActive || record.link.operation != record ||
			record.link.ticket != ticket || record.link.wait != wait || slot.claim != claim {
			return false
		}
	}
	return true
}

func (source *ChannelOperationSource) ReserveAndAttachWait(
	p *P,
	state *ParkState,
	ticket ParkTicket,
	wait *WaitSetRecord,
	caseID uint32,
	claim *SelectClaim,
) (OperationID, bool) {
	if !validChannelOperationOwner(source, p) || claim != nil && selectClaimLoad(claim) != selectClaimOpen ||
		!channelOperationCommitDomainCompatible(source, state, ticket, wait, claim) {
		return OperationID{}, false
	}
	for index := uint32(0); index < ChannelOperationConfiguredCapacity(source); index++ {
		slot, slotOK := channelOperationSlotAt(source, index)
		if !slotOK || !channelOperationReusableSlot(source, slot, index) || preemptLoad(&slot.generation) == ^uint32(0) ||
			claim != nil && !channelOperationExternalReservable(slot) {
			continue
		}
		generation, begun := beginProducerSourceSlot(&slot.producerSourceSlot)
		if !begun {
			return OperationID{}, false
		}
		if !raiseSourceScanLimit(&source.scanLimit, index, ChannelOperationConfiguredCapacity(source)) {
			return OperationID{}, false
		}
		id, ok := MakeOperationIDAtRoute(OperationSourceChannel, source.route, index+1, generation)
		if !ok || !PrepareOperationAtGeneration(&slot.record, id) {
			_ = resetProducerSourceSlot(&slot.producerSourceSlot, generation)
			return OperationID{}, false
		}
		if !DeclareOperationCommitMode(&slot.record, OperationCommitReadyThenTryCommit) ||
			!AttachParkWaitOperation(state, ticket, wait, &slot.record, caseID) {
			if !AbortReservedOperation(&slot.record, id) ||
				!resetProducerSourceSlot(&slot.producerSourceSlot, generation) {
				return OperationID{}, false
			}
			return OperationID{}, false
		}
		slot.claim = claim
		if claim != nil {
			preemptStore(&slot.external, uint32(channelExternalReserved))
		}
		if !activateProducerSourceSlot(&slot.producerSourceSlot, generation) {
			return OperationID{}, false
		}
		return id, true
	}
	return OperationID{}, false
}

// ExposeExternalCommit is the final owner-side publication before a typed
// hchan node becomes reachable. PrepareParkSet has already frozen the exact
// ParkState/WaitSet/frame relation and installed pendingParkSet; after this
// release publication the compiler/runtime path may only publish the node and
// execute llvm.coro.suspend. Rejection leaves Reserved unchanged, so no peer
// can mistake a partial preparation for a committable endpoint.
func (source *ChannelOperationSource) ExposeExternalCommit(
	p *P,
	g *G,
	id OperationID,
	ticket ParkTicket,
	wait *WaitSetRecord,
	claim *SelectClaim,
) bool {
	slot, ok := channelOperationSlotFor(source, id)
	if !ok || !validChannelOperationOwner(source, p) || g == nil || claim == nil ||
		preemptLoad(&slot.generation) != id.Generation || preemptLoad(&slot.state) != uint32(producerSourceActive) ||
		preemptLoad(&slot.external) != uint32(channelExternalReserved) || slot.claim != claim ||
		selectClaimLoad(claim) != selectClaimOpen || g.runP != p || p.current != g || !p.inResume ||
		g.state != GRunning || g.pending.kind != pendingParkSet || g.pending.from == nil ||
		g.pending.from != g.active || g.pending.from.parkWait != wait || wait == nil ||
		wait.state != waitSetRecordCommitted || wait.g != g || wait.ticket != ticket ||
		&g.park != slot.record.link.park || g.park.phase != parkParked || g.park.ticket != ticket ||
		slot.record.phase != operationActive || slot.record.id != id || slot.record.link.operation != &slot.record ||
		slot.record.link.wait != wait || slot.record.link.ticket != ticket ||
		operationCandidateMode(&slot.record) != OperationCommitReadyThenTryCommit {
		return false
	}
	return preemptCompareAndSwap(
		&slot.external,
		uint32(channelExternalReserved),
		uint32(channelExternalExposed),
	)
}

// AbortSelectPreparation atomically excludes external claimers, aborts one
// owner preparation transaction, and terminalizes its Channel commit domain.
// This is not a general cancellation or resolver entry: wait must still be
// uncommitted compiler frame storage, and every attached Channel case must
// belong to this exact source, wait, ticket, and claim. ApplyOne deliberately
// continues to require a terminal claim; this entry supplies that fact without
// weakening detection of a resolver which forgot to claim a parked select.
//
// C1 hchan lowering must keep every queue node and OperationID unreachable by
// a matcher until CommitParkSet/PrepareParkSet publishes Parked. The compiler
// calls this method only inside the same NoSuspend/NoPanic, preemption-disabled
// preparation transaction. Publishing a node earlier would let an external
// matcher inspect ParkState while Seal/Commit/Abort writes owner-only fields
// and is a contract violation even though its pre-effect validation would
// eventually reject a non-Parked endpoint.
func (source *ChannelOperationSource) AbortSelectPreparation(
	p *P,
	state *ParkState,
	ticket ParkTicket,
	wait *WaitSetRecord,
	claim *SelectClaim,
) bool {
	if !validChannelOperationOwner(source, p) || state == nil || wait == nil || claim == nil ||
		!validParkTicket(ticket) || state.ticket != ticket ||
		(state.phase != parkPreparing && state.phase != parkSealed) || state.resolving || !validParkState(state) ||
		wait.g == nil || &wait.g.park != state || wait.ticket != ticket ||
		wait.state != waitSetRecordPreparing || wait.work != waitSetWorkIdle ||
		wait.activePrev != nil || wait.activeNext != nil || wait.workNext != nil {
		return false
	}
	if !channelOperationCommitDomainCompatible(source, state, ticket, wait, claim) {
		return false
	}
	channelLinks := uint32(0)
	for link := state.head; link != nil; link = link.next {
		record := link.operation
		if record == nil || record.id.Source() != OperationSourceChannel {
			continue
		}
		slot, ok := channelOperationSlotFor(source, record.id)
		if !ok || &slot.record != record || slot.claim != claim || link.wait != wait ||
			preemptLoad(&slot.generation) != record.id.Generation ||
			producerSourceLifecycle(preemptLoad(&slot.state)) != producerSourceActive ||
			record.phase != operationActive || record.disposition != OperationDispositionPending ||
			record.resolutionApplied {
			return false
		}
		channelLinks++
	}
	if channelLinks == 0 {
		return false
	}
	if selectClaimOwnerAcquire(claim) != selectClaimOpen {
		return false
	}
	if !AbortParkSet(state, ticket) {
		_ = selectClaimOwnerReleasePending(claim)
		return false
	}
	return selectClaimOwnerReleaseTerminal(claim)
}

type ChannelOperationPostResult uint8

const (
	ChannelOperationPostInvalid ChannelOperationPostResult = iota
	ChannelOperationPosted
	ChannelOperationPostDuplicate
	ChannelOperationPostStale
	ChannelOperationPostRetry
	ChannelOperationPostClosed
)

type ChannelOperationCloseResult uint8

const (
	ChannelOperationCloseInvalid ChannelOperationCloseResult = iota
	ChannelOperationCloseStarted
	ChannelOperationAlreadyClosing
	ChannelOperationAlreadyQuiesced
)

func (source *ChannelOperationSource) PostReady(id OperationID) ChannelOperationPostResult {
	slot, ok := channelOperationSlotFor(source, id)
	if !ok {
		return ChannelOperationPostInvalid
	}
	switch acquireProducerSourceGeneration(&slot.producerSourceSlot, id.Generation) {
	case producerSourceAcquireClosed:
		return ChannelOperationPostClosed
	case producerSourceAcquireStale:
		return ChannelOperationPostStale
	case producerSourceAcquired:
	default:
		return ChannelOperationPostInvalid
	}
	result := source.postReadyAdmitted(slot, id)
	if !producerAdmissionReleaseChecked(&slot.inflight) {
		return ChannelOperationPostInvalid
	}
	return result
}

func (source *ChannelOperationSource) postReadyAdmitted(slot *channelOperationSlot, id OperationID) ChannelOperationPostResult {
	if preemptLoad(&slot.generation) != id.Generation {
		return ChannelOperationPostStale
	}
	if preemptLoad(&slot.state) != uint32(producerSourceActive) {
		return ChannelOperationPostClosed
	}
	for {
		switch channelPhysicalState(preemptLoad(&slot.physical)) {
		case channelPhysicalIdle:
			if !preemptCompareAndSwap(&slot.physical, uint32(channelPhysicalIdle), uint32(channelPhysicalReady)) {
				continue
			}
		case channelPhysicalReady:
		case channelPhysicalRetryBudget:
			return ChannelOperationPostRetry
		case channelPhysicalCommitted:
			return ChannelOperationPostDuplicate
		default:
			return ChannelOperationPostInvalid
		}
		break
	}
	for {
		switch mailbox := channelOperationMailbox(preemptLoad(&slot.mailbox)); mailbox {
		case channelMailboxEmpty:
			if !preemptCompareAndSwap(&slot.mailbox, uint32(mailbox), uint32(channelMailboxReady)) {
				continue
			}
			preemptStore(&source.pending, 1)
			return ChannelOperationPosted
		case channelMailboxReady, channelMailboxForced, channelMailboxDrainingReady,
			channelMailboxDrainingForced, channelMailboxForcedBehindReadyDrain:
			return ChannelOperationPostDuplicate
		default:
			return ChannelOperationPostInvalid
		}
	}
}

// channelExternalCommitAdmission is an internal fragment of
// channelExternalCommitPair, not a producer
// handle. C0 tests exercise its scalar transitions directly, but real hchan
// wiring must enter only through the pair transaction and must not copy or
// expose endpointA/B. Before C1 makes that wiring reachable, the endpoint
// helpers should additionally receive an exact parent/address certificate so
// a copied child value cannot race the owning pair for its linear token.
type channelExternalCommitAdmission struct {
	source *ChannelOperationSource
	slot   *channelOperationSlot
	id     OperationID
	token  uint32
	held   bool
	posted bool
	broken bool
}

type channelExternalCommitAcquireResult uint8

const (
	channelExternalCommitAcquireInvalid channelExternalCommitAcquireResult = iota
	channelExternalCommitAcquired
	channelExternalCommitAcquireStale
	channelExternalCommitAcquireClosed
	channelExternalCommitAcquireUnsupported
	channelExternalCommitAcquireContended
)

// acquireChannelExternalLease gives one hchan transaction a source-specific
// linear release token. The shared producer admission count only proves that
// some producers remain; it cannot distinguish a copied release from another
// legitimate producer's lease. Odd values are held tokens and even values are
// reusable sequence states. Exhaustion fails closed instead of permitting ABA.
func acquireChannelExternalLease(slot *channelOperationSlot) (uint32, bool) {
	if slot == nil {
		return 0, false
	}
	for {
		state := preemptLoad(&slot.externalLease)
		if state&1 != 0 || state >= ^uint32(0)-1 {
			return 0, false
		}
		token := state + 1
		if preemptCompareAndSwap(&slot.externalLease, state, token) {
			return token, true
		}
	}
}

// acquireExternalCommit is the lifetime-first half of the hchan shim. While in
// its synchronization domain, hchan first acquires both endpoint admissions in
// a stable order and only then touches either frame-local SelectClaim. Thus a
// failed admission never needs to write a frame which Apply may already have
// detached. Both admissions stay held through claim acquisition/rollback,
// physical effect, both mailbox publications, and both Claimed stores. This
// shim reads only atomic prefix fields. C0 admits external matching only for
// claim-backed selects; a claim-less true single operation uses local
// TryCommit until C1 adds its hchan committing fence. Failure releases
// admission before returning.
func (source *ChannelOperationSource) acquireExternalCommit(id OperationID) (channelExternalCommitAdmission, channelExternalCommitAcquireResult) {
	slot, ok := channelOperationSlotFor(source, id)
	if !ok {
		return channelExternalCommitAdmission{}, channelExternalCommitAcquireInvalid
	}
	switch acquireProducerSourceGeneration(&slot.producerSourceSlot, id.Generation) {
	case producerSourceAcquireClosed:
		return channelExternalCommitAdmission{}, channelExternalCommitAcquireClosed
	case producerSourceAcquireStale:
		return channelExternalCommitAdmission{}, channelExternalCommitAcquireStale
	case producerSourceAcquired:
	default:
		return channelExternalCommitAdmission{}, channelExternalCommitAcquireInvalid
	}
	if preemptLoad(&slot.state) != uint32(producerSourceActive) {
		if !producerAdmissionReleaseChecked(&slot.inflight) {
			return channelExternalCommitAdmission{}, channelExternalCommitAcquireInvalid
		}
		return channelExternalCommitAdmission{}, channelExternalCommitAcquireClosed
	}
	if preemptLoad(&slot.external) != uint32(channelExternalExposed) {
		if !producerAdmissionReleaseChecked(&slot.inflight) {
			return channelExternalCommitAdmission{}, channelExternalCommitAcquireInvalid
		}
		return channelExternalCommitAdmission{}, channelExternalCommitAcquireUnsupported
	}
	token, acquired := acquireChannelExternalLease(slot)
	if !acquired {
		if !producerAdmissionReleaseChecked(&slot.inflight) {
			return channelExternalCommitAdmission{}, channelExternalCommitAcquireInvalid
		}
		return channelExternalCommitAdmission{}, channelExternalCommitAcquireContended
	}
	return channelExternalCommitAdmission{source: source, slot: slot, id: id, token: token, held: true}, channelExternalCommitAcquired
}

func (admission *channelExternalCommitAdmission) releaseLinearLease() bool {
	if admission == nil || !admission.held || admission.broken || admission.slot == nil ||
		admission.token == 0 || admission.token&1 == 0 ||
		!preemptCompareAndSwap(&admission.slot.externalLease, admission.token, admission.token+1) {
		if admission != nil {
			admission.broken = true
		}
		return false
	}
	if !producerAdmissionReleaseChecked(&admission.slot.inflight) {
		admission.broken = true
		return false
	}
	return true
}

// releaseWithoutCommit is the no-effect rollback used when the peer endpoint
// admission or either select claim cannot be acquired. Every claim write and
// rollback must happen while both admissions remain held. It never writes a
// mailbox and is invalid after physical publication.
func (admission *channelExternalCommitAdmission) releaseWithoutCommit() bool {
	if admission == nil || !admission.held || admission.posted || admission.broken || admission.source == nil ||
		admission.slot == nil || !admission.id.Valid() {
		return false
	}
	if !admission.releaseLinearLease() {
		return false
	}
	*admission = channelExternalCommitAdmission{}
	return true
}

// publishExternallyCommitted is called only after the hchan synchronization
// domain performed the typed physical transfer while both endpoint admissions
// remained held. Closing is accepted because the owner may seal after
// acquisition. This method records physical ownership and a sticky mailbox but
// deliberately does not release admission: both frame claims must first be
// stored Claimed. Executor requests happen only after releaseCommitted on both
// endpoints.
func (admission *channelExternalCommitAdmission) publishExternallyCommitted() ChannelOperationPostResult {
	if admission == nil || !admission.held || admission.posted || admission.broken || admission.source == nil ||
		admission.slot == nil || !admission.id.Valid() || admission.token == 0 || admission.token&1 == 0 ||
		preemptLoad(&admission.slot.externalLease) != admission.token {
		return ChannelOperationPostInvalid
	}
	source, slot, id := admission.source, admission.slot, admission.id
	result := source.publishExternallyCommittedHeld(slot, id)
	if result == ChannelOperationPosted {
		admission.posted = true
	} else {
		// The caller crossed the effect boundary before entering this method.
		// Duplicate is therefore not idempotent success: it may represent a
		// duplicate physical transfer. Retain the lifetime lease fail-closed.
		admission.broken = true
	}
	return result
}

// releaseCommitted ends the frame-lifetime lease after both endpoint claims
// are Claimed. No code may touch a claim through the hchan node after this
// call; source/executor request publication is the only remaining producer
// tail.
func (admission *channelExternalCommitAdmission) releaseCommitted() bool {
	if admission == nil || !admission.held || !admission.posted || admission.broken || admission.source == nil ||
		admission.slot == nil || !admission.id.Valid() {
		return false
	}
	if !admission.releaseLinearLease() {
		return false
	}
	*admission = channelExternalCommitAdmission{}
	return true
}

type channelExternalCommitPairPhase uint8

const (
	channelExternalCommitPairInvalid channelExternalCommitPairPhase = iota
	channelExternalCommitPairPrepared
	channelExternalCommitPairEffect
	channelExternalCommitPairBroken
)

type channelExternalCommitPairBeginResult uint8

const (
	channelExternalCommitPairBeginInvalid channelExternalCommitPairBeginResult = iota
	channelExternalCommitPairBeginPrepared
	channelExternalCommitPairBeginFirstAdmissionFailed
	channelExternalCommitPairBeginSecondAdmissionFailed
	channelExternalCommitPairBeginClaimMismatch
	channelExternalCommitPairBeginClaimContended
	channelExternalCommitPairBeginInvariantFailure
)

// channelExternalCommitPair is a caller-owned fixed-layout transaction, not a
// Future or separately allocated operation-state. self binds the transaction
// to the exact storage address supplied to begin; an accidental value copy is
// rejected before it can dereference a claim/slot or release an admission.
// Host-Go escape caused by self is only a C0 test artifact. C1 may wire this
// to a real hchan only after the compiler supplies caller-owned storage and a
// noescape certificate proving that storage is in a non-moving coroutine frame
// without heap allocation, plus a NoSuspend/NoPanic certificate for the entire
// hchan critical section. endpointA/B preserve the
// caller's original send/recv mapping; firstIsA records only the stable
// source-slot acquisition order, so reversing channel direction cannot reverse
// lifetime lock order. Claims remain in logical endpoint order and apply their
// own stable address order.
type channelExternalCommitPair struct {
	self      *channelExternalCommitPair
	endpointA channelExternalCommitAdmission
	endpointB channelExternalCommitAdmission
	claimA    *SelectClaim
	claimB    *SelectClaim
	phase     channelExternalCommitPairPhase
	firstIsA  bool
	_         [6]byte
}

func releaseChannelExternalCommitPairWithoutEffect(pair *channelExternalCommitPair) bool {
	if pair == nil || pair.self != pair || pair.phase != channelExternalCommitPairPrepared {
		return false
	}
	first, second := &pair.endpointA, &pair.endpointB
	if !pair.firstIsA {
		first, second = second, first
	}
	secondOK := second.releaseWithoutCommit()
	if !secondOK || !first.releaseWithoutCommit() {
		pair.phase = channelExternalCommitPairBroken
		return false
	}
	*pair = channelExternalCommitPair{}
	return true
}

// validChannelExternalEndpointHeld is called only after both select claims
// were acquired. Admission pins the frame/link lifetime, Exposed certifies the
// final owner preparation boundary, and claim ownership excludes resolver
// mutation. The check is O(1) and deliberately avoids G/WaitSet/ParkState
// fields which Resumed may update concurrently with an early peer match.
func validChannelExternalEndpointHeld(admission *channelExternalCommitAdmission, claim *SelectClaim) bool {
	if admission == nil || !admission.held || admission.posted || admission.broken ||
		admission.source == nil || admission.slot == nil || !admission.id.Valid() || claim == nil ||
		selectClaimLoad(claim) != selectClaimAcquiring {
		return false
	}
	source, slot, id := admission.source, admission.slot, admission.id
	resolvedSlot, ok := channelOperationSlotFor(source, id)
	if !ok || resolvedSlot != slot || slot.claim != claim || preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.external) != uint32(channelExternalExposed) || admission.token == 0 || admission.token&1 == 0 ||
		preemptLoad(&slot.externalLease) != admission.token || preemptLoad(&slot.inflight)&producerAdmissionCountMask == 0 {
		return false
	}
	switch producerSourceLifecycle(preemptLoad(&slot.state)) {
	case producerSourceActive, producerSourceClosing:
	default:
		return false
	}
	record, link := &slot.record, &slot.record.link
	if record.id != id || record.phase != operationActive || link.operation != record ||
		link.wait == nil || link.wait.g == nil || link.park != &link.wait.g.park || link.ticket != link.wait.ticket ||
		operationCandidateMode(record) != OperationCommitReadyThenTryCommit {
		return false
	}
	// Exposed was release-published only after the owner validated the complete
	// pendingParkSet relation. Resumed may concurrently change G state,
	// WaitSetRecord state, and the active-wait queue, so none of those fields is
	// read here. ParkState/link/record are unchanged by Resumed; the acquired
	// claim excludes their later resolver/detach mutation.
	state := link.park
	if state == nil || state.phase != parkParked || state.resolving || state.ticket != link.ticket ||
		!validParkTicket(state.ticket) || state.outcome != ParkOutcomePending ||
		state.winnerRecord != nil || state.winnerID != (OperationID{}) ||
		state.attached != state.expected || state.attached == 0 || state.head == nil ||
		record.disposition != OperationDispositionPending || record.resolutionApplied ||
		!operationCandidatePendingResultStorageValid(record) || !operationCandidatePendingForResolution(record) {
		return false
	}
	if link.previous == nil {
		if state.head != link {
			return false
		}
	} else if link.previous.next != link || link.previous.rank >= link.rank {
		return false
	}
	return link.next == nil || link.next.previous == link && link.rank < link.next.rank
}

// beginChannelExternalCommitPair is the all-or-none pre-effect gate used by a
// future typed hchan matcher. It acquires both exact admissions before the
// first frame access, checks only lifetime-stable claim/generation identity,
// then acquires both claims before reading owner-only records. Ordinary
// admission/claim contention returns a zero transaction after complete
// rollback. An invariant failure deliberately returns a non-zero Broken
// transaction retaining any lifetime lease; callers must fail-stop rather
// than risk release followed by frame use.
//
// A C1 matcher may receive an endpoint only after its owner has atomically
// published the Parked preparation boundary. ReserveAndAttachWait deliberately
// makes the source slot producer-visible earlier so ordinary readiness cannot
// be lost, but that scalar admission is not permission to expose an hchan queue
// node or call this pair gate while Seal/Commit/Abort still mutates ParkState.
func beginChannelExternalCommitPair(
	pair *channelExternalCommitPair,
	sourceA *ChannelOperationSource,
	idA OperationID,
	claimA *SelectClaim,
	sourceB *ChannelOperationSource,
	idB OperationID,
	claimB *SelectClaim,
) channelExternalCommitPairBeginResult {
	if pair == nil || *pair != (channelExternalCommitPair{}) {
		return channelExternalCommitPairBeginInvalid
	}
	slotA, okA := channelOperationSlotFor(sourceA, idA)
	slotB, okB := channelOperationSlotFor(sourceB, idB)
	if !okA || !okB || slotA == slotB || claimA == nil || claimB == nil || claimA == claimB {
		return channelExternalCommitPairBeginInvalid
	}
	firstSource, firstID := sourceA, idA
	secondSource, secondID := sourceB, idB
	firstIsA := true
	if uintptr(unsafe.Pointer(slotA)) > uintptr(unsafe.Pointer(slotB)) {
		firstSource, secondSource = secondSource, firstSource
		firstID, secondID = secondID, firstID
		firstIsA = false
	}
	first, firstResult := firstSource.acquireExternalCommit(firstID)
	if firstResult != channelExternalCommitAcquired {
		return channelExternalCommitPairBeginFirstAdmissionFailed
	}
	second, secondResult := secondSource.acquireExternalCommit(secondID)
	if secondResult != channelExternalCommitAcquired {
		if !first.releaseWithoutCommit() {
			*pair = channelExternalCommitPair{
				self: pair, phase: channelExternalCommitPairBroken, firstIsA: firstIsA,
			}
			if firstIsA {
				pair.endpointA = first
			} else {
				pair.endpointB = first
			}
			return channelExternalCommitPairBeginInvariantFailure
		}
		return channelExternalCommitPairBeginSecondAdmissionFailed
	}
	*pair = channelExternalCommitPair{
		self: pair, claimA: claimA, claimB: claimB,
		phase: channelExternalCommitPairPrepared, firstIsA: firstIsA,
	}
	if firstIsA {
		pair.endpointA, pair.endpointB = first, second
	} else {
		pair.endpointA, pair.endpointB = second, first
	}
	// Admission is the lifetime lease for the stable claim pointer: attach
	// publishes claim before opening ingress, and Apply cannot clear it until
	// the sealed count reaches zero. Admission does not serialize owner-only
	// OperationRecord mutation, so do not inspect record until both claims have
	// excluded their owner resolvers.
	if pair.endpointA.slot.claim != claimA || pair.endpointB.slot.claim != claimB ||
		preemptLoad(&pair.endpointA.slot.generation) != idA.Generation ||
		preemptLoad(&pair.endpointB.slot.generation) != idB.Generation {
		if !releaseChannelExternalCommitPairWithoutEffect(pair) {
			return channelExternalCommitPairBeginInvariantFailure
		}
		return channelExternalCommitPairBeginClaimMismatch
	}
	acquired, claimsOK := tryAcquireExternalSelectClaims(claimA, claimB)
	if !claimsOK {
		pair.phase = channelExternalCommitPairBroken
		return channelExternalCommitPairBeginInvariantFailure
	}
	if !acquired {
		if !releaseChannelExternalCommitPairWithoutEffect(pair) {
			return channelExternalCommitPairBeginInvariantFailure
		}
		return channelExternalCommitPairBeginClaimContended
	}
	// Both owner resolvers are now excluded. The admissions still pin each
	// record/link, so exact identity can be checked without racing detach or
	// owner resolution. A failure after claim acquisition must roll claims back
	// before releasing either admission.
	if !validChannelExternalEndpointHeld(&pair.endpointA, claimA) ||
		!validChannelExternalEndpointHeld(&pair.endpointB, claimB) {
		if !pair.abort() {
			return channelExternalCommitPairBeginInvariantFailure
		}
		return channelExternalCommitPairBeginInvariantFailure
	}
	return channelExternalCommitPairBeginPrepared
}

// beginEffect is the explicit no-return boundary immediately before the typed
// hchan transfer. Abort is valid only before this transition; no callback or
// closure is needed to enforce that distinction.
func (pair *channelExternalCommitPair) beginEffect() bool {
	if pair == nil || pair.self != pair || pair.phase != channelExternalCommitPairPrepared {
		return false
	}
	if !beginExternalSelectClaimsEffect(pair.claimA, pair.claimB) {
		// A copied Prepared transaction cannot acquire shared effect permission
		// after its peer has moved the claims to Committing. Any partial second
		// CAS anomaly is also fail-closed: admissions remain held and rollback is
		// forbidden because one claim may already carry effect permission.
		pair.phase = channelExternalCommitPairBroken
		return false
	}
	pair.phase = channelExternalCommitPairEffect
	return true
}

func (pair *channelExternalCommitPair) abort() bool {
	if pair == nil || pair.self != pair || pair.phase != channelExternalCommitPairPrepared {
		return false
	}
	if !rollbackExternalSelectClaims(pair.claimA, pair.claimB) {
		pair.phase = channelExternalCommitPairBroken
		return false
	}
	return releaseChannelExternalCommitPairWithoutEffect(pair)
}

// commit publishes the two exact source facts after the caller's typed effect,
// then both Claimed stores, and only then releases endpoint lifetime leases.
// A post-effect invariant failure retains the transaction/admissions and must
// fail-stop; rollback is never legal in Effect or Broken.
func (pair *channelExternalCommitPair) commit() bool {
	if pair == nil || pair.self != pair || pair.phase != channelExternalCommitPairEffect {
		return false
	}
	firstResult := pair.endpointA.publishExternallyCommitted()
	if firstResult != ChannelOperationPosted {
		pair.phase = channelExternalCommitPairBroken
		return false
	}
	secondResult := pair.endpointB.publishExternallyCommitted()
	if secondResult != ChannelOperationPosted {
		pair.phase = channelExternalCommitPairBroken
		return false
	}
	if !publishExternalSelectClaims(pair.claimA, pair.claimB) {
		pair.phase = channelExternalCommitPairBroken
		return false
	}
	first, second := &pair.endpointA, &pair.endpointB
	if !pair.firstIsA {
		first, second = second, first
	}
	if !second.releaseCommitted() || !first.releaseCommitted() {
		pair.phase = channelExternalCommitPairBroken
		return false
	}
	*pair = channelExternalCommitPair{}
	return true
}

// ChannelExternalCommitPair is the typed hchan-facing wrapper around the C0
// pair proof. The inner self certificate still points at its exact field, so a
// copied wrapper fails every transition before touching either endpoint. The
// wrapper exposes no admissions, source slots, or owner-only records.
type ChannelExternalCommitPair struct {
	transaction channelExternalCommitPair
}

type ChannelExternalCommitPairBeginResult uint8

const (
	ChannelExternalCommitPairBeginInvalid ChannelExternalCommitPairBeginResult = iota
	ChannelExternalCommitPairBeginPrepared
	ChannelExternalCommitPairBeginFirstAdmissionFailed
	ChannelExternalCommitPairBeginSecondAdmissionFailed
	ChannelExternalCommitPairBeginClaimMismatch
	ChannelExternalCommitPairBeginClaimContended
	ChannelExternalCommitPairBeginInvariantFailure
)

// BeginChannelExternalCommitPair acquires both endpoint lifetimes and both
// claims. Prepared is still reversible and contains no physical channel
// effect. A non-zero wrapper returned with InvariantFailure is Broken and must
// be treated as terminal by the hchan caller.
func BeginChannelExternalCommitPair(
	out *ChannelExternalCommitPair,
	sourceA *ChannelOperationSource,
	idA OperationID,
	claimA *SelectClaim,
	sourceB *ChannelOperationSource,
	idB OperationID,
	claimB *SelectClaim,
) ChannelExternalCommitPairBeginResult {
	if out == nil || *out != (ChannelExternalCommitPair{}) {
		return ChannelExternalCommitPairBeginInvalid
	}
	switch beginChannelExternalCommitPair(
		&out.transaction,
		sourceA, idA, claimA,
		sourceB, idB, claimB,
	) {
	case channelExternalCommitPairBeginPrepared:
		return ChannelExternalCommitPairBeginPrepared
	case channelExternalCommitPairBeginFirstAdmissionFailed:
		return ChannelExternalCommitPairBeginFirstAdmissionFailed
	case channelExternalCommitPairBeginSecondAdmissionFailed:
		return ChannelExternalCommitPairBeginSecondAdmissionFailed
	case channelExternalCommitPairBeginClaimMismatch:
		return ChannelExternalCommitPairBeginClaimMismatch
	case channelExternalCommitPairBeginClaimContended:
		return ChannelExternalCommitPairBeginClaimContended
	case channelExternalCommitPairBeginInvariantFailure:
		return ChannelExternalCommitPairBeginInvariantFailure
	default:
		return ChannelExternalCommitPairBeginInvalid
	}
}

func (pair *ChannelExternalCommitPair) BeginEffect() bool {
	return pair != nil && pair.transaction.beginEffect()
}

func (pair *ChannelExternalCommitPair) Abort() bool {
	return pair != nil && pair.transaction.abort()
}

func (pair *ChannelExternalCommitPair) Commit() bool {
	return pair != nil && pair.transaction.commit()
}

// ChannelExternalCommit is the single-endpoint counterpart of the pair
// transaction above. A typed hchan buffer/close path has one physical effect
// but still has to exclude the owner resolver, pin the exact frame endpoint,
// publish Forced after the effect, and release the lifetime admission only
// after SelectClaim becomes terminal. It is caller-owned fixed storage, not a
// Task/Future object, and it never survives the hchan critical section.
//
// self gives the value linear identity. Every exported transition checks it
// before dereferencing endpoint or claim, so copying a Prepared/Effect value
// cannot release or commit the original transaction. A compiler wiring this
// primitive into hchan must prove that the local does not become a managed
// heap allocation and that Begin -> BeginEffect -> typed effect -> Commit is a
// NoSuspend/NoPanic span.
type ChannelExternalCommit struct {
	self     *ChannelExternalCommit
	endpoint channelExternalCommitAdmission
	claim    *SelectClaim
	phase    channelExternalCommitPairPhase
	_        [7]byte
}

// ChannelExternalCommitBeginResult distinguishes ordinary stale/contention
// from a fail-closed invariant break without exposing source internals to the
// typed hchan layer.
type ChannelExternalCommitBeginResult uint8

const (
	ChannelExternalCommitBeginInvalid ChannelExternalCommitBeginResult = iota
	ChannelExternalCommitBeginPrepared
	ChannelExternalCommitBeginAdmissionFailed
	ChannelExternalCommitBeginClaimMismatch
	ChannelExternalCommitBeginClaimContended
	ChannelExternalCommitBeginInvariantFailure
)

func releaseChannelExternalCommitWithoutClaim(transaction *ChannelExternalCommit) bool {
	if transaction == nil || transaction.self != transaction ||
		transaction.phase != channelExternalCommitPairPrepared || transaction.claim == nil {
		return false
	}
	if !transaction.endpoint.releaseWithoutCommit() {
		transaction.phase = channelExternalCommitPairBroken
		return false
	}
	*transaction = ChannelExternalCommit{}
	return true
}

// BeginChannelExternalCommit acquires the exact endpoint admission before it
// reads the frame-local claim, then acquires that claim before inspecting the
// owner-only record/link. Prepared means the caller owns reversible pre-effect
// permission. Admission failure, claim mismatch, and contention leave out
// exact-zero. InvariantFailure is recoverably zero only when rollback was
// proven; a non-zero Broken out must be treated as terminal by the caller.
func BeginChannelExternalCommit(
	out *ChannelExternalCommit,
	source *ChannelOperationSource,
	id OperationID,
	claim *SelectClaim,
) ChannelExternalCommitBeginResult {
	if out == nil || *out != (ChannelExternalCommit{}) || source == nil || !id.Valid() || claim == nil {
		return ChannelExternalCommitBeginInvalid
	}
	endpoint, acquired := source.acquireExternalCommit(id)
	if acquired != channelExternalCommitAcquired {
		return ChannelExternalCommitBeginAdmissionFailed
	}
	*out = ChannelExternalCommit{
		self: out, endpoint: endpoint, claim: claim, phase: channelExternalCommitPairPrepared,
	}
	// Admission pins the stable claim pointer and generation. Record/link reads
	// remain forbidden until the claim has excluded the owner resolver.
	if out.endpoint.slot.claim != claim || preemptLoad(&out.endpoint.slot.generation) != id.Generation {
		if !releaseChannelExternalCommitWithoutClaim(out) {
			return ChannelExternalCommitBeginInvariantFailure
		}
		return ChannelExternalCommitBeginClaimMismatch
	}
	switch state := selectClaimOwnerAcquire(claim); state {
	case selectClaimOpen:
	case selectClaimAcquiring, selectClaimCommitting, selectClaimClaimed, selectClaimContended:
		if !releaseChannelExternalCommitWithoutClaim(out) {
			return ChannelExternalCommitBeginInvariantFailure
		}
		return ChannelExternalCommitBeginClaimContended
	default:
		out.phase = channelExternalCommitPairBroken
		return ChannelExternalCommitBeginInvariantFailure
	}
	if !validChannelExternalEndpointHeld(&out.endpoint, claim) {
		if !out.Abort() {
			return ChannelExternalCommitBeginInvariantFailure
		}
		return ChannelExternalCommitBeginInvariantFailure
	}
	return ChannelExternalCommitBeginPrepared
}

// BeginEffect is the single-endpoint no-return boundary. Once it succeeds,
// Abort is forbidden even when a later invariant fails.
func (transaction *ChannelExternalCommit) BeginEffect() bool {
	if transaction == nil || transaction.self != transaction ||
		transaction.phase != channelExternalCommitPairPrepared || transaction.claim == nil {
		return false
	}
	if !beginExternalSelectClaimEffect(transaction.claim) {
		transaction.phase = channelExternalCommitPairBroken
		return false
	}
	transaction.phase = channelExternalCommitPairEffect
	return true
}

// Abort releases a Prepared transaction in claim-before-admission order.
func (transaction *ChannelExternalCommit) Abort() bool {
	if transaction == nil || transaction.self != transaction ||
		transaction.phase != channelExternalCommitPairPrepared || transaction.claim == nil {
		return false
	}
	if !selectClaimOwnerReleasePending(transaction.claim) {
		transaction.phase = channelExternalCommitPairBroken
		return false
	}
	return releaseChannelExternalCommitWithoutClaim(transaction)
}

// Commit publishes the irreversible source fact, then terminal Claim, then
// releases the frame-lifetime admission. The hchan caller requests the exact
// executor only after this method returns true.
func (transaction *ChannelExternalCommit) Commit() bool {
	if transaction == nil || transaction.self != transaction ||
		transaction.phase != channelExternalCommitPairEffect || transaction.claim == nil {
		return false
	}
	if transaction.endpoint.publishExternallyCommitted() != ChannelOperationPosted ||
		!publishExternalSelectClaim(transaction.claim) ||
		!transaction.endpoint.releaseCommitted() {
		transaction.phase = channelExternalCommitPairBroken
		return false
	}
	*transaction = ChannelExternalCommit{}
	return true
}

func (source *ChannelOperationSource) publishExternallyCommittedHeld(slot *channelOperationSlot, id OperationID) ChannelOperationPostResult {
	if preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.external) != uint32(channelExternalExposed) {
		return ChannelOperationPostStale
	}
	state := producerSourceLifecycle(preemptLoad(&slot.state))
	if state != producerSourceActive && state != producerSourceClosing {
		return ChannelOperationPostClosed
	}
	for {
		physical := channelPhysicalState(preemptLoad(&slot.physical))
		switch physical {
		case channelPhysicalIdle, channelPhysicalReady, channelPhysicalRetryBudget:
			if !preemptCompareAndSwap(&slot.physical, uint32(physical), uint32(channelPhysicalCommitted)) {
				continue
			}
		case channelPhysicalCommitted:
			return ChannelOperationPostDuplicate
		default:
			return ChannelOperationPostInvalid
		}
		break
	}
	for {
		switch mailbox := channelOperationMailbox(preemptLoad(&slot.mailbox)); mailbox {
		case channelMailboxEmpty, channelMailboxReady:
			if !preemptCompareAndSwap(&slot.mailbox, uint32(mailbox), uint32(channelMailboxForced)) {
				continue
			}
			preemptStore(&source.pending, 1)
			return ChannelOperationPosted
		case channelMailboxDrainingReady:
			if !preemptCompareAndSwap(&slot.mailbox, uint32(mailbox), uint32(channelMailboxForcedBehindReadyDrain)) {
				continue
			}
			preemptStore(&source.pending, 1)
			return ChannelOperationPosted
		case channelMailboxForced, channelMailboxDrainingForced, channelMailboxForcedBehindReadyDrain:
			return ChannelOperationPostDuplicate
		default:
			return ChannelOperationPostInvalid
		}
	}
}

func (source *ChannelOperationSource) beginPublishPass(p *P) bool {
	return source != nil && beginRoutedProducerPass(&source.routedProducerSource, p)
}

func (source *ChannelOperationSource) Pending() bool {
	return source != nil && routedProducerPending(&source.routedProducerSource)
}

func finishChannelMailboxDrain(source *ChannelOperationSource, slot *channelOperationSlot, mailbox channelOperationMailbox) bool {
	switch mailbox {
	case channelMailboxReady:
		if preemptCompareAndSwap(&slot.mailbox, uint32(channelMailboxDrainingReady), uint32(channelMailboxEmpty)) {
			return true
		}
		if preemptCompareAndSwap(&slot.mailbox, uint32(channelMailboxForcedBehindReadyDrain), uint32(channelMailboxForced)) {
			preemptStore(&source.pending, 1)
			return true
		}
	case channelMailboxForced:
		return preemptCompareAndSwap(&slot.mailbox, uint32(channelMailboxDrainingForced), uint32(channelMailboxEmpty))
	}
	return false
}

func restoreChannelMailboxDrain(source *ChannelOperationSource, slot *channelOperationSlot, mailbox channelOperationMailbox) bool {
	restored := false
	switch mailbox {
	case channelMailboxReady:
		restored = preemptCompareAndSwap(&slot.mailbox, uint32(channelMailboxDrainingReady), uint32(channelMailboxReady)) ||
			preemptCompareAndSwap(&slot.mailbox, uint32(channelMailboxForcedBehindReadyDrain), uint32(channelMailboxForced))
	case channelMailboxForced:
		restored = preemptCompareAndSwap(&slot.mailbox, uint32(channelMailboxDrainingForced), uint32(channelMailboxForced))
	}
	if restored {
		preemptStore(&source.pending, 1)
	}
	return restored
}

func beginChannelMailboxDrain(slot *channelOperationSlot, mailbox channelOperationMailbox) (channelOperationMailbox, bool) {
	if slot == nil {
		return channelMailboxEmpty, false
	}
	retried := false
	for {
		var draining channelOperationMailbox
		switch mailbox {
		case channelMailboxEmpty:
			return channelMailboxEmpty, !retried
		case channelMailboxReady:
			draining = channelMailboxDrainingReady
		case channelMailboxForced:
			draining = channelMailboxDrainingForced
		default:
			// Draining states are owner-only. ForcedBehindReadyDrain is valid
			// only after this owner has already acquired DrainingReady.
			return channelMailboxEmpty, false
		}
		if preemptCompareAndSwap(&slot.mailbox, uint32(mailbox), uint32(draining)) {
			return mailbox, true
		}
		// A producer may monotonically overtake Ready with Forced between
		// the load and CAS. Re-read and drain that stronger fact in this slot
		// visit instead of turning a normal race into executor corruption.
		mailbox = channelOperationMailbox(preemptLoad(&slot.mailbox))
		retried = true
	}
}

func releaseChannelPublishClaim(claim *SelectClaim, held bool) bool {
	return !held || selectClaimOwnerReleasePending(claim)
}

// publishDrainedSlot owns one mailbox draining state. A claim-backed Ready
// publication temporarily acquires the same SelectClaim used by resolution
// and external matching before it reads or writes the OperationRecord. A
// Forced mailbox is published only after the external effect has made Claimed
// terminal; Acquiring/Committing keeps the mailbox sticky for a later epoch.
// Thus neither an admission nor a claim load is mistaken for serialization:
// the only mutable-record paths are an exact Open->Acquiring lease or the
// terminal Claimed state.
func (source *ChannelOperationSource) publishDrainedSlot(
	p *P,
	slot *channelOperationSlot,
	mailbox channelOperationMailbox,
) (published, lost uint32, ok bool) {
	if !validChannelOperationOwner(source, p) || slot == nil ||
		(mailbox != channelMailboxReady && mailbox != channelMailboxForced) {
		return 0, 0, false
	}
	switch producerSourceLifecycle(preemptLoad(&slot.state)) {
	case producerSourceActive, producerSourceClosing:
	default:
		return 0, 0, false
	}
	draining := channelOperationMailbox(preemptLoad(&slot.mailbox))
	if mailbox == channelMailboxReady {
		if draining != channelMailboxDrainingReady && draining != channelMailboxForcedBehindReadyDrain {
			return 0, 0, false
		}
	} else if draining != channelMailboxDrainingForced {
		return 0, 0, false
	}

	claim, claimHeld := slot.claim, false
	if claim != nil {
		switch mailbox {
		case channelMailboxReady:
			switch selectClaimOwnerAcquire(claim) {
			case selectClaimOpen:
				claimHeld = true
			case selectClaimAcquiring, selectClaimCommitting, selectClaimContended:
				return 0, 0, restoreChannelMailboxDrain(source, slot, mailbox)
			case selectClaimClaimed:
				// Claimed is terminal: no external matcher or resolver can
				// mutate this record while the source pass publishes the
				// remaining loser facts before forced discovery.
			default:
				_ = restoreChannelMailboxDrain(source, slot, mailbox)
				return 0, 0, false
			}
		case channelMailboxForced:
			switch selectClaimLoad(claim) {
			case selectClaimAcquiring, selectClaimCommitting:
				return 0, 0, restoreChannelMailboxDrain(source, slot, mailbox)
			case selectClaimClaimed:
			default:
				_ = restoreChannelMailboxDrain(source, slot, mailbox)
				return 0, 0, false
			}
		}
	} else if mailbox == channelMailboxForced {
		_ = restoreChannelMailboxDrain(source, slot, mailbox)
		return 0, 0, false
	}

	id := slot.record.id
	if preemptLoad(&slot.generation) != id.Generation || !slot.record.Matches(id) {
		_ = restoreChannelMailboxDrain(source, slot, mailbox)
		_ = releaseChannelPublishClaim(claim, claimHeld)
		return 0, 0, false
	}
	var result OperationCompletionResult
	if mailbox == channelMailboxForced {
		result = PublishExternallyCommittedReadyThenCandidate(&slot.record, id)
	} else {
		result = PublishReadyThenTryCommitCandidate(&slot.record, id)
	}
	switch result {
	case OperationCompletionPublished:
		if slot.record.link.wait == nil || !MarkWaitSetAffected(p, slot.record.link.wait) {
			_ = restoreChannelMailboxDrain(source, slot, mailbox)
			_ = releaseChannelPublishClaim(claim, claimHeld)
			return 0, 0, false
		}
		published = 1
	case OperationCompletionDuplicate:
	case OperationCompletionLost:
		lost = 1
	case OperationCompletionDeferred:
		restored := restoreChannelMailboxDrain(source, slot, mailbox)
		released := releaseChannelPublishClaim(claim, claimHeld)
		return 0, 0, restored && released
	default:
		_ = restoreChannelMailboxDrain(source, slot, mailbox)
		_ = releaseChannelPublishClaim(claim, claimHeld)
		return 0, 0, false
	}
	finished := finishChannelMailboxDrain(source, slot, mailbox)
	released := releaseChannelPublishClaim(claim, claimHeld)
	return published, lost, finished && released
}

func (source *ChannelOperationSource) publishSlot(p *P, index uint32) (published, lost uint32, ok bool) {
	if !validChannelOperationOwner(source, p) || index >= ChannelOperationConfiguredCapacity(source) {
		return 0, 0, false
	}
	slot, slotOK := channelOperationSlotAt(source, index)
	if !slotOK {
		return 0, 0, false
	}
	state := producerSourceLifecycle(preemptLoad(&slot.state))
	if state != producerSourceActive && state != producerSourceClosing {
		return 0, 0, state == producerSourceFree || state == producerSourceQuiesced
	}
	mailbox, drainOK := beginChannelMailboxDrain(slot, channelOperationMailbox(preemptLoad(&slot.mailbox)))
	if !drainOK {
		return 0, 0, false
	}
	if mailbox == channelMailboxEmpty {
		return 0, 0, true
	}
	return source.publishDrainedSlot(p, slot, mailbox)
}

type selectClaimOwner struct {
	claim *SelectClaim
	held  bool
}

func (source *ChannelOperationSource) ClaimFor(record *OperationRecord) (*SelectClaim, bool) {
	if source == nil || record == nil || record.id.Source() != OperationSourceChannel {
		return nil, false
	}
	slot, ok := channelOperationSlotFor(source, record.id)
	if !ok {
		return nil, false
	}
	state := producerSourceLifecycle(preemptLoad(&slot.state))
	return slot.claim, &slot.record == record && preemptLoad(&slot.generation) == record.id.Generation &&
		(state == producerSourceActive || state == producerSourceClosing)
}

func (source *ChannelOperationSource) TryCommit(request ParkCommitRequest, owner selectClaimOwner) (ParkCommitAttempt, bool) {
	id, ok := request.ID()
	slot, slotOK := channelOperationSlotFor(source, id)
	if !ok || !slotOK || preemptLoad(&slot.generation) != id.Generation || &slot.record != request.record ||
		preemptLoad(&slot.state) != uint32(producerSourceActive) || !currentParkCommitRequest(request) || slot.claim != nil &&
		(!owner.held || owner.claim != slot.claim || selectClaimLoad(slot.claim) != selectClaimAcquiring) {
		return ParkCommitAttempt{}, false
	}
	switch channelPhysicalState(preemptLoad(&slot.physical)) {
	case channelPhysicalRetryBudget:
		return request.RetryBudget(), true
	case channelPhysicalIdle:
		return request.Failed(), true
	case channelPhysicalReady:
		if !preemptCompareAndSwap(&slot.physical, uint32(channelPhysicalReady), uint32(channelPhysicalCommitted)) {
			return request.RetryBudget(), true
		}
		attempt, bound := BindParkCommitResult(request)
		if !bound {
			if !preemptCompareAndSwap(&slot.physical, uint32(channelPhysicalCommitted), uint32(channelPhysicalReady)) {
				return ParkCommitAttempt{}, false
			}
			return ParkCommitAttempt{}, false
		}
		return attempt, bound
	case channelPhysicalCommitted:
		// A nil claim is permitted only for a true single channel operation.
		// Its peer may have committed behind this source cursor; binding the
		// already-owned result is exact and the sticky Forced mailbox is drained
		// before Apply can detach. Multi-case selects are fenced by Acquiring.
		if slot.claim != nil {
			return ParkCommitAttempt{}, false
		}
		return BindParkCommitResult(request)
	default:
		return ParkCommitAttempt{}, false
	}
}

func (source *ChannelOperationSource) beginCloseSlot(p *P, id OperationID) ChannelOperationCloseResult {
	slot, ok := channelOperationSlotFor(source, id)
	if !ok || !validChannelOperationOwner(source, p) || preemptLoad(&slot.generation) != id.Generation ||
		!slot.record.Matches(id) {
		return ChannelOperationCloseInvalid
	}
	switch beginProducerSourceClose(&slot.producerSourceSlot) {
	case producerSourceCloseStarted:
		return ChannelOperationCloseStarted
	case producerSourceAlreadyClosing:
		return ChannelOperationAlreadyClosing
	case producerSourceAlreadyQuiesced:
		return ChannelOperationAlreadyQuiesced
	default:
		return ChannelOperationCloseInvalid
	}
}

// BeginClose seals the exact scalar producer ingress. ConfirmQuiesced still
// requires the caller's backend join plus the closed-with-zero admission word.
func (source *ChannelOperationSource) BeginClose(p *P, id OperationID) ChannelOperationCloseResult {
	return source.beginCloseSlot(p, id)
}

// ApplyOne seals producer ingress and joins every admitted frame access before
// detach. A source-only Ready producer touches just the atomic prefix, but an
// external hchan transaction uses this same admission as the lifetime lease
// for its queue-node claim pointer. ConfirmQuiesced remains the later backend
// strong join and physical cleanup boundary, mirroring ManualOperationSource.
func (source *ChannelOperationSource) ApplyOne(p *P, id OperationID, record *OperationRecord) OperationApplyResult {
	slot, ok := channelOperationSlotFor(source, id)
	if !ok || !validChannelOperationOwner(source, p) || preemptLoad(&slot.generation) != id.Generation ||
		&slot.record != record || !record.Matches(id) || record.phase != operationActive {
		return OperationApplyInvalid
	}
	disposition, terminal := OperationDispositionOf(record, id)
	if !terminal || record.link.park == nil || record.link.wait == nil || record.link.operation != record ||
		record.link.ticket == (ParkTicket{}) {
		return OperationApplyInvalid
	}
	closeResult := source.beginCloseSlot(p, id)
	if closeResult != ChannelOperationCloseStarted && closeResult != ChannelOperationAlreadyClosing &&
		closeResult != ChannelOperationAlreadyQuiesced {
		return OperationApplyInvalid
	}
	// Admission covers every hchan access to the frame-local claim, not just
	// atomic source fields. A held external transaction may have published its
	// mailbox but not yet stored both claims Claimed. Never acknowledge, detach,
	// clear the claim pointer, or make the G promotable until the sealed source
	// joins that transaction.
	if !producerSourceSlotQuiesced(&slot.producerSourceSlot) {
		return OperationApplyRetryBudget
	}
	// A normal owner resolution stores Claimed before entering Apply. An
	// external Acquiring/Committing transaction must still hold admission and
	// therefore cannot reach closed-with-zero above. Seeing a live nonterminal
	// frame claim here is corruption, not retryable contention: fail closed
	// before acknowledgement, detach, or clearing the frame pointer.
	if slot.claim != nil && selectClaimLoad(slot.claim) != selectClaimClaimed {
		return OperationApplyInvalid
	}
	if disposition != OperationDispositionWinner && record.resultState == operationResultOwned {
		// Only externally forced strong cancellation owns an unselected Channel
		// result. Its candidate remains Committed while Apply discards ownership;
		// it is never rewritten to RolledBack.
		if !operationCandidateForcedCanceled(record) || !DiscardUnselectedOperationResult(record, id) {
			return OperationApplyInvalid
		}
	}
	if !record.resolutionApplied && !AcknowledgeOperationResolution(record, id, disposition) {
		return OperationApplyInvalid
	}
	park, ticket := record.link.park, record.link.ticket
	if !DetachParkWaitOperation(park, ticket, record, id) {
		return OperationApplyInvalid
	}
	// No new producer can enter and the admission join above proves no hchan
	// transaction can still touch the frame. Discovery is terminal, so release
	// the source's frame pointer before the G is promotable.
	slot.claim = nil
	return OperationApplyDetached
}

// ConfirmQuiesced accepts the hchan/backend strong join. The admission word
// additionally proves every source shim admitted before Apply's seal returned;
// a late sticky mailbox must first be classified by a later publish epoch.
func (source *ChannelOperationSource) ConfirmQuiesced(p *P, id OperationID) bool {
	slot, ok := channelOperationSlotFor(source, id)
	if !ok || !validChannelOperationOwner(source, p) || preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.state) != uint32(producerSourceClosing) || !producerSourceSlotQuiesced(&slot.producerSourceSlot) ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxEmpty) || slot.claim != nil ||
		preemptLoad(&slot.externalLease)&1 != 0 ||
		slot.record.phase != operationDetached || !slot.record.resolutionApplied {
		return false
	}
	disposition, terminal := OperationDispositionOf(&slot.record, id)
	if !terminal {
		return false
	}
	physical := channelPhysicalState(preemptLoad(&slot.physical))
	if disposition == OperationDispositionWinner {
		if physical != channelPhysicalCommitted || slot.record.resultState != operationResultOwned &&
			slot.record.resultState != operationResultLeased && slot.record.resultState != operationResultTaken &&
			slot.record.resultState != operationResultDiscarded {
			return false
		}
	} else {
		forcedCanceled := disposition == OperationDispositionCanceled &&
			operationCandidateMode(&slot.record) == OperationCommitReadyThenTryCommit &&
			operationCandidateState(&slot.record) == OperationCommitCommitted &&
			slot.record.resultState == operationResultDiscarded
		if forcedCanceled {
			if physical != channelPhysicalCommitted {
				return false
			}
			preemptStore(&slot.physical, uint32(channelPhysicalIdle))
		} else {
			switch physical {
			case channelPhysicalIdle:
			case channelPhysicalReady, channelPhysicalRetryBudget:
				preemptStore(&slot.physical, uint32(channelPhysicalIdle))
			default:
				return false
			}
		}
	}
	if !ConfirmOperationQuiesced(&slot.record, id) {
		return false
	}
	preemptStore(&slot.external, 0)
	return markProducerSourceQuiesced(&slot.producerSourceSlot)
}

// ResetSelectClaim is the resume/compiler-owner reuse boundary. A select claim
// remains Claimed through logical resolution and every Channel detach; it may
// return to Open only after this source no longer retains that frame pointer.
func (source *ChannelOperationSource) ResetSelectClaim(p *P, claim *SelectClaim) bool {
	if !validChannelOperationOwner(source, p) || p.channelSource != source || claim == nil ||
		selectClaimLoad(claim) != selectClaimClaimed {
		return false
	}
	limit, valid := channelOperationScanLimit(source)
	if !valid {
		return false
	}
	for index := uint32(0); index < limit; index++ {
		slot, ok := channelOperationSlotAt(source, index)
		if !ok || slot.claim == claim {
			return false
		}
	}
	return preemptCompareAndSwap(&claim.state, selectClaimClaimed, selectClaimOpen)
}

func (source *ChannelOperationSource) TakeResult(p *P, lease OperationResultLease) bool {
	id, ok := lease.ID()
	if !ok || !validChannelOperationOwner(source, p) {
		return false
	}
	slot, ok := channelOperationSlotFor(source, id)
	return ok && preemptLoad(&slot.generation) == id.Generation && TakeOperationResult(&slot.record, lease)
}

func (source *ChannelOperationSource) DiscardResult(p *P, lease OperationResultLease) bool {
	id, ok := lease.ID()
	if !ok || !validChannelOperationOwner(source, p) {
		return false
	}
	slot, ok := channelOperationSlotFor(source, id)
	return ok && preemptLoad(&slot.generation) == id.Generation && DiscardOperationResult(&slot.record, lease)
}

func (source *ChannelOperationSource) Recycle(p *P, id OperationID) bool {
	slot, ok := channelOperationSlotFor(source, id)
	if !ok || !validChannelOperationOwner(source, p) || preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.state) != uint32(producerSourceQuiesced) || !producerSourceSlotQuiesced(&slot.producerSourceSlot) ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxEmpty) || preemptLoad(&slot.external) != 0 ||
		preemptLoad(&slot.externalLease)&1 != 0 || slot.claim != nil ||
		!OperationCanRecycle(&slot.record, id) {
		return false
	}
	physical := channelPhysicalState(preemptLoad(&slot.physical))
	if physical != channelPhysicalIdle && physical != channelPhysicalCommitted {
		return false
	}
	if !RecycleOperation(&slot.record, id) {
		return false
	}
	preemptStore(&slot.physical, uint32(channelPhysicalIdle))
	return recycleProducerSourceSlot(&slot.producerSourceSlot)
}

func channelOperationSourceEmpty(source *ChannelOperationSource, owner *P) bool {
	if source == nil || !routedProducerHeaderEmpty(&source.routedProducerSource, owner) {
		return false
	}
	for index := uint32(0); index < ChannelOperationConfiguredCapacity(source); index++ {
		slot, ok := channelOperationSlotAt(source, index)
		if !ok || !channelOperationReusableSlot(source, slot, index) {
			return false
		}
	}
	return true
}

func UnbindChannelOperationSource(source *ChannelOperationSource, p *P) bool {
	if p == nil || p.channelSource != source || !channelOperationSourceEmpty(source, p) {
		return false
	}
	if !unbindRoutedProducerSource(&source.routedProducerSource, p) {
		return false
	}
	p.channelSource = nil
	source.scanLimit = 0
	return true
}

func (source *ChannelOperationSource) CanRelease() bool {
	return channelOperationSourceEmpty(source, nil)
}

func (source *ChannelOperationSource) Route() (RouteID, bool) {
	if source == nil {
		return 0, false
	}
	return routedProducerRoute(&source.routedProducerSource)
}
