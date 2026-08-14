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

// CurrentExecutorChannelDriver resolves the exact channel source owner during
// the compiler's narrow SuspendPark/FrameSuspended transition window. Channel
// queue nodes may later rendezvous across executor routes, but preparation is
// owned by the current G's exact P/source pair. Typed resume cleanup now runs
// on the scheduler stack before runnable publication.
func CurrentExecutorChannelDriver(g *G) (*ExecutorDriver, ExecutorHandle, RouteID, bool) {
	driver, handle, route, ok := currentExecutorParkDriver(g)
	if !ok || driver.sources.channel == nil ||
		!validChannelOperationOwner(driver.sources.channel, driver.p) ||
		driver.sources.channel.route != route {
		return nil, ExecutorHandle{}, 0, false
	}
	return driver, handle, route, true
}

// CurrentExecutorChannelParkContext resolves the complete transient channel
// preparation capability in one authenticated owner lookup. Keeping the
// driver, P, ParkState, and source in one return prevents typed adapters from
// proving the same SuspendPark/FrameSuspended relation a second time through
// CurrentExecutorChannelParkOwner immediately afterwards.
func CurrentExecutorChannelParkContext(
	g *G,
) (*ExecutorDriver, ExecutorHandle, RouteID, *P, *ParkState, *ChannelOperationSource, bool) {
	driver, handle, route, ok := CurrentExecutorChannelDriver(g)
	if !ok {
		return nil, ExecutorHandle{}, 0, nil, nil, nil, false
	}
	return driver, handle, route, driver.p, &g.park, driver.sources.channel, true
}

// CurrentExecutorChannelDirectReservation resolves the current direct-channel
// owner and selects its reusable slot under the same authenticated boundary.
// current distinguishes an invalid G/P/source relation from ordinary catalog
// exhaustion; the typed adapter may grow stable source storage only for the
// latter, then retry through the general owner-audited preflight.
func CurrentExecutorChannelDirectReservation(
	g *G,
) (
	route RouteID,
	p *P,
	source *ChannelOperationSource,
	reservation ChannelDirectReservation,
	current bool,
	reserved bool,
) {
	driver, _, route, ok := CurrentExecutorChannelDriver(g)
	if !ok {
		return 0, nil, nil, ChannelDirectReservation{}, false, false
	}
	p, source = driver.p, driver.sources.channel
	reservation, reserved = source.preflightDirectReservationOwned(p)
	return route, p, source, reservation, true, reserved
}

type CurrentChannelParkPreparationState uint8

const (
	CurrentChannelParkPreparationInvalid CurrentChannelParkPreparationState = iota
	CurrentChannelParkPreparationNeedsCapacity
	CurrentChannelParkPreparationPrepared
)

// CurrentChannelParkPreparation is the closed result of one compiler-owned
// direct-channel park transaction. NeedsCapacity carries only the authenticated
// owner/source pair so the runtime can attach stable source storage and retry
// without publishing a waiter. Prepared additionally carries the exact ticket
// and operation which were bound into the frame-local resume packet.
type CurrentChannelParkPreparation struct {
	Owner     *P
	Source    *ChannelOperationSource
	Ticket    ParkTicket
	Operation OperationID
	Route     RouteID
	State     CurrentChannelParkPreparationState
}

// PrepareCurrentChannelParkCleanup performs the generated one-channel park as
// one owner transaction. The compiler supplies the exact G/frame header and
// zero-filled frame storage; this function authenticates that boundary once,
// reserves and attaches one source slot, publishes the parked frame, and binds
// its cleanup packet without immediately re-walking the graph it just built.
//
// reservation is consumed only when hasReservation is true. A first call with
// no reusable slot returns NeedsCapacity without mutating scheduler or frame
// state; the runtime may grow stable storage, obtain a reservation, and retry
// in the same no-suspend interval.
func PrepareCurrentChannelParkCleanup(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	claim *SelectClaim,
	packet *ResumePacket,
	plan *ResumeCleanupPlan,
	context unsafe.Pointer,
	entry *OperationID,
	reservation ChannelDirectReservation,
	hasReservation bool,
	caseID uint32,
	seed uint32,
) CurrentChannelParkPreparation {
	driver, _, route, current := currentExecutorParkDriver(g)
	if !current || driver.sources.channel == nil ||
		!validChannelOperationOwner(driver.sources.channel, driver.p) ||
		driver.sources.channel.route != route {
		return CurrentChannelParkPreparation{}
	}
	p, source, frame := driver.p, driver.sources.channel, g.active
	base := CurrentChannelParkPreparation{Owner: p, Source: source, Route: route}
	if frame == nil || frame.handle != handle || frame.header != header ||
		wait == nil || claim == nil || packet == nil || plan == nil || context == nil || entry == nil ||
		*wait != (WaitSetRecord{}) || *claim != (SelectClaim{}) ||
		*packet != (ResumePacket{}) || *plan != (ResumeCleanupPlan{}) ||
		*entry != (OperationID{}) || caseID == 0 {
		return CurrentChannelParkPreparation{}
	}
	if hasReservation {
		if !validDirectReservationHeader(source, p, reservation) {
			return CurrentChannelParkPreparation{}
		}
	} else {
		var reserved bool
		reservation, reserved = source.preflightDirectReservationOwned(p)
		if !reserved {
			base.State = CurrentChannelParkPreparationNeedsCapacity
			return base
		}
	}

	// A stop which won before this no-suspend interval retains the generic
	// cancellation-aware transaction. It is cold and may repeat its defensive
	// proof; the ordinary compiler path below owns the compact certificate.
	if g.park.taskCancelKind != TaskCancelNone || g.park.taskCancelPhase != taskCancelIdle {
		ticket, id, ok := prepareSingleChannelParkCleanup(
			g, handle, header, source, wait, claim, packet, plan, context, entry,
			&reservation, caseID, seed,
		)
		if !ok || id.Route() != route {
			return CurrentChannelParkPreparation{}
		}
		base.Ticket, base.Operation = ticket, id
		base.State = CurrentChannelParkPreparationPrepared
		return base
	}
	if g.pending.kind != pendingNone || g.spawnChild != nil || g.waiting ||
		!validReusableSingleParkState(&g.park) ||
		g.park.attached != 0 || g.park.head != nil {
		return CurrentChannelParkPreparation{}
	}

	ticket, ok := nextParkTicket(g.park.ticket)
	if !ok {
		return CurrentChannelParkPreparation{}
	}

	// The private reservation was selected under this exact owner P and cannot
	// be changed by another owner during the no-suspend compiler hook. Prove
	// every scalar which could make OperationID construction or scan publication
	// fail before sealing the physical generation. After begin succeeds, the
	// remaining writes form one closed, no-fail suffix followed by the two
	// release publications which make the generation and hchan endpoint visible.
	slot, index, capacity := reservation.slot, reservation.index, reservation.capacity
	if slot == nil || capacity != ChannelOperationConfiguredCapacity(source) ||
		index >= capacity || index >= operationLocalMask || source.scanLimit > capacity {
		return CurrentChannelParkPreparation{}
	}
	previousGeneration := preemptLoad(&slot.generation)
	if previousGeneration == ^uint32(0) {
		return CurrentChannelParkPreparation{}
	}
	id, idOK := MakeOperationIDAtRoute(
		OperationSourceChannel, source.route, index+1, previousGeneration+1,
	)
	if !idOK {
		return CurrentChannelParkPreparation{}
	}
	generation, begun := beginProducerSourceSlot(&slot.producerSourceSlot)
	if !begun {
		return CurrentChannelParkPreparation{}
	}
	if generation != id.Generation {
		_ = resetProducerSourceSlot(&slot.producerSourceSlot, generation)
		return CurrentChannelParkPreparation{}
	}

	parkSeed := seed ^ ticket.generation*0x9e3779b9 ^ ticket.epoch*0x85ebca6b
	record := &slot.record
	*record = OperationRecord{id: id, phase: operationActive}
	setOperationCandidate(record, OperationCommitReadyThenTryCommit, OperationCommitIdle, false)
	record.link = ParkLink{
		park:      &g.park,
		wait:      wait,
		operation: record,
		ticket:    ticket,
		caseID:    caseID,
		rank:      parkCaseRank(parkSeed, caseID),
	}
	g.park = ParkState{
		ticket:   ticket,
		phase:    parkParked,
		expected: 1,
		attached: 1,
		head:     &record.link,
	}
	*wait = WaitSetRecord{
		g:             g,
		ticket:        ticket,
		state:         waitSetRecordCommitted,
		directChannel: true,
	}
	*entry = id
	installWaitSetResumeCleanup(wait, packet, plan, ResumeCleanupBinding{
		Kind:         ResumeCleanupChannelDirect,
		Context:      context,
		Entries:      unsafe.Pointer(entry),
		Claim:        claim,
		Count:        1,
		RuntimeCount: 1,
		Stride:       unsafe.Sizeof(OperationID{}),
	})
	frame.parkWait = wait
	g.pending = pendingTransition{kind: pendingParkSet, from: frame}
	slot.claim = claim
	preemptStore(&slot.external, uint32(channelExternalReserved))
	if next := index + 1; next > source.scanLimit {
		source.scanLimit = next
	}
	source.reserveCursor = index + 1
	if source.reserveCursor == capacity {
		source.reserveCursor = 0
	}
	if !activateProducerSourceSlot(&slot.producerSourceSlot, generation) ||
		!preemptCompareAndSwap(
			&slot.external,
			uint32(channelExternalReserved),
			uint32(channelExternalExposed),
		) {
		return CurrentChannelParkPreparation{}
	}
	base.Ticket, base.Operation = ticket, id
	base.State = CurrentChannelParkPreparationPrepared
	return base
}

// CurrentExecutorChannelParkOwner returns transient owner-only objects needed
// by the typed hchan adapter to build a direct or multi-case park. None of the
// returned pointers may cross suspension except the ChannelOperationSource
// stored in the compiler-spilled endpoint itself. That source is stable for
// the executor lifetime, while OperationID remains the producer/wake identity.
func CurrentExecutorChannelParkOwner(
	driver *ExecutorDriver,
	g *G,
) (*P, *ParkState, *ChannelOperationSource, bool) {
	if driver == nil || !ValidG(g) {
		return nil, nil, nil, false
	}
	current, _, _, ok := CurrentExecutorChannelDriver(g)
	if !ok || current != driver || driver.p != g.runP {
		return nil, nil, nil, false
	}
	return driver.p, &g.park, driver.sources.channel, true
}

// ActiveChannelParkOwner is retained for target-neutral lifecycle tests and
// compatibility adapters which have not opted into ResumeCleanupPlan. New
// typed channel code must materialize before promotion instead of consulting
// its old source from a compiler resume gate.
func ActiveChannelParkOwner(g *G, source *ChannelOperationSource) (*P, *ParkState, bool) {
	if !ValidG(g) || !resumeGateTaken(g) || g.runP == nil || !validChannelOperationOwner(source, g.runP) {
		return nil, nil, false
	}
	return g.runP, &g.park, true
}

// PrepareSingleChannelPark is the bounded owner-P transaction used by the
// compiler-generated slow path for one blocking send or receive. wait and
// claim are stable caller storage in the LLVM coroutine frame. The function
// completes every fallible logical/source preparation step before publishing
// Exposed; after success the typed hchan layer may publish exactly one queue
// node and the compiler may immediately execute llvm.coro.suspend.
//
// A failure is pre-effect but not generally retryable: once source admission
// has begun, an invariant failure deliberately leaves the exact generation
// fail-closed for the runtime adapter to abort. Ordinary capacity exhaustion
// is rejected before BeginParkSet mutates G.
func PrepareSingleChannelPark(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	source *ChannelOperationSource,
	wait *WaitSetRecord,
	claim *SelectClaim,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	// The ordinary compiler-generated direct park is a one-candidate
	// transaction, not a degenerate select. Build its exact relation in one
	// owner-only interval so generic Seal/sort/Prepare/Commit layers do not each
	// re-audit the same single ParkLink. A pending task stop retains the generic
	// path below because CommitParkSet must translate it into logical cancel.
	if g != nil && g.park.taskCancelKind == TaskCancelNone &&
		g.park.taskCancelPhase == taskCancelIdle {
		return prepareSingleChannelParkOrdinary(
			g, handle, header, source, wait, claim, nil, caseID, seed,
		)
	}
	return prepareSingleChannelParkGeneric(
		g, handle, header, source, wait, claim, caseID, seed,
	)
}

// PrepareSingleChannelParkCleanup is the compiler/runtime direct-channel ABI.
// It combines the one-candidate logical/source preparation with installation
// of its frame-local typed cleanup descriptor. entry, packet, and plan are
// stable fields in the same LLVM coroutine frame as wait and claim; no pointer
// escapes to a producer. The public general BindWaitSetResumeCleanup remains
// available for select and independently assembled source sets.
func PrepareSingleChannelParkCleanup(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	source *ChannelOperationSource,
	wait *WaitSetRecord,
	claim *SelectClaim,
	packet *ResumePacket,
	plan *ResumeCleanupPlan,
	context unsafe.Pointer,
	entry *OperationID,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	return prepareSingleChannelParkCleanup(
		g, handle, header, source, wait, claim, packet, plan, context, entry,
		nil, caseID, seed,
	)
}

// PrepareSingleChannelParkCleanupReserved consumes the exact owner-only slot
// capability returned by PreflightDirectReservation. No suspension or owner
// transition may occur between the two calls. The ordinary wrapper above is
// retained for adapters which do not need to combine catalog growth with
// direct park preparation.
func PrepareSingleChannelParkCleanupReserved(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	source *ChannelOperationSource,
	wait *WaitSetRecord,
	claim *SelectClaim,
	packet *ResumePacket,
	plan *ResumeCleanupPlan,
	context unsafe.Pointer,
	entry *OperationID,
	reservation ChannelDirectReservation,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	return prepareSingleChannelParkCleanup(
		g, handle, header, source, wait, claim, packet, plan, context, entry,
		&reservation, caseID, seed,
	)
}

func prepareSingleChannelParkCleanup(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	source *ChannelOperationSource,
	wait *WaitSetRecord,
	claim *SelectClaim,
	packet *ResumePacket,
	plan *ResumeCleanupPlan,
	context unsafe.Pointer,
	entry *OperationID,
	reservation *ChannelDirectReservation,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	if packet == nil || plan == nil || context == nil || entry == nil ||
		*packet != (ResumePacket{}) || *plan != (ResumeCleanupPlan{}) ||
		*entry != (OperationID{}) {
		return ParkTicket{}, OperationID{}, false
	}
	var ticket ParkTicket
	var id OperationID
	var ok bool
	if reservation != nil && g != nil && g.park.taskCancelKind == TaskCancelNone &&
		g.park.taskCancelPhase == taskCancelIdle {
		ticket, id, ok = prepareSingleChannelParkOrdinary(
			g, handle, header, source, wait, claim, reservation, caseID, seed,
		)
	} else {
		ticket, id, ok = PrepareSingleChannelPark(
			g, handle, header, source, wait, claim, caseID, seed,
		)
	}
	if !ok {
		return ParkTicket{}, OperationID{}, false
	}
	*entry = id
	binding := ResumeCleanupBinding{
		Kind:         ResumeCleanupChannelDirect,
		Context:      context,
		Entries:      unsafe.Pointer(entry),
		Claim:        claim,
		Count:        1,
		RuntimeCount: 1,
		Stride:       unsafe.Sizeof(OperationID{}),
	}
	// PrepareSingleChannelPark has just frozen this exact one-link relation and
	// no typed hchan node is reachable yet. Recheck the scalar frame boundary,
	// then install through the common no-fail write half without rescanning the
	// source catalog or revalidating the same plan after construction.
	if wait == nil || wait.state != waitSetRecordCommitted || wait.work != waitSetWorkIdle ||
		wait.resume != nil || wait.resumeKind != resumeBindingNone || wait.g != g ||
		wait.ticket != ticket || g == nil || g.active == nil || g.active.parkWait != wait ||
		g.pending.kind != pendingParkSet || g.pending.from != g.active ||
		g.park.phase != parkParked || g.park.ticket != ticket || g.park.expected != 1 ||
		g.park.attached != 1 || g.park.head == nil || g.park.head.next != nil ||
		g.park.head.wait != wait || g.park.head.operation == nil ||
		g.park.head.operation.id != id || g.park.head.caseID != caseID ||
		selectClaimLoad(claim) != selectClaimOpen {
		return ParkTicket{}, OperationID{}, false
	}
	installWaitSetResumeCleanup(wait, packet, plan, binding)
	wait.directChannel = true
	return ticket, id, true
}

// validCommittedDirectChannelPark is the post-llvm.coro.suspend gate for the
// fused direct preparation transaction. The full graph was audited before the
// endpoint became Exposed; between that publication and Resumed, a peer may
// change source atomics and the SelectClaim but cannot mutate this owner-only
// ParkState/OperationRecord suffix. Validate the exact scalar/pointer boundary
// once instead of immediately repeating validParkState and the cleanup plan.
func validCommittedDirectChannelPark(g *G, frame *Frame, wait *WaitSetRecord) bool {
	if g == nil || frame == nil || wait == nil || !wait.directChannel ||
		frame.owner != g || frame.parkWait != wait || wait.g != g ||
		wait.state != waitSetRecordCommitted || wait.work != waitSetWorkIdle ||
		wait.activePrev != nil || wait.activeNext != nil || wait.workNext != nil {
		return false
	}
	if wait.resumeKind == resumeBindingDirectChannel {
		return validCommittedCompactDirectChannelPark(g, frame, wait)
	}
	plan := (*ResumeCleanupPlan)(wait.resume)
	if !validDirectChannelBoundResumeState(wait, plan) {
		return false
	}
	id := (*OperationID)(plan.entries)
	state := &g.park
	if state.ticket != wait.ticket || state.phase != parkParked || state.resolving ||
		state.expected != 1 || state.attached != 1 || state.seed != 0 || state.hasDefault ||
		state.taskCancelKind != TaskCancelNone || state.taskCancelPhase != taskCancelIdle ||
		state.cancelKind != ParkCancelNone || state.outcome != ParkOutcomePending ||
		state.winnerCase != 0 || state.winnerID != (OperationID{}) ||
		state.winnerRecord != nil || state.head == nil || state.head.previous != nil ||
		state.head.next != nil || state.head.wait != wait || state.head.park != state ||
		state.head.ticket != wait.ticket || state.head.operation == nil {
		return false
	}
	record := state.head.operation
	return record.id == *id && record.phase == operationActive &&
		record.disposition == OperationDispositionPending && !record.resolutionApplied &&
		!record.cancelRequested && !record.quiesced &&
		operationCandidateMode(record) == OperationCommitReadyThenTryCommit &&
		operationCandidateState(record) == OperationCommitIdle &&
		!operationCandidateIsPublished(record) && record.resultState == operationResultEmpty &&
		record.resultTicket == (ParkTicket{}) && record.link.park == state &&
		record.link.wait == wait && record.link.operation == record &&
		record.link.ticket == wait.ticket && record.link.previous == nil && record.link.next == nil
}

// validReusableSingleParkState is the O(1) preflight for an ordinary
// compiler-owned one-event park. Idle and Delivered have no attached operation
// graph; checking them through validParkState would enter its general N-way
// link walker and large phase switch on every operation. Consumed is a
// compatibility handoff shape with outcome-dependent state and deliberately
// retains the complete validator.
func validReusableSingleParkState(state *ParkState) bool {
	if state == nil || state.resolving || state.taskCancelKind != TaskCancelNone ||
		state.taskCancelPhase != taskCancelIdle {
		return false
	}
	switch state.phase {
	case parkIdle:
		return *state == (ParkState{})
	case parkDelivered:
		return validParkTicket(state.ticket) && state.expected == 0 && state.attached == 0 &&
			state.seed == 0 && !state.hasDefault && !state.directChannel && state.cancelKind == ParkCancelNone &&
			state.outcome == ParkOutcomePending && state.winnerCase == 0 &&
			state.winnerID == (OperationID{}) && state.winnerRecord == nil && state.head == nil
	case parkConsumed:
		return validParkState(state)
	default:
		return false
	}
}

// validPreparedDirectChannelParkState is the single-link post-build audit.
// A source has just produced this exact graph under the owner P, and no source
// endpoint is externally reachable yet. This is the complete one-link
// equivalent of validParkState, shared by direct-channel and manual parks.
func validPreparedDirectChannelParkState(
	state *ParkState,
	wait *WaitSetRecord,
	id OperationID,
	caseID uint32,
) bool {
	if state == nil || wait == nil || !id.Valid() || caseID == 0 ||
		state.phase != parkParked || !validParkTicket(state.ticket) || state.resolving ||
		state.expected != 1 || state.attached != 1 || state.seed != 0 || state.hasDefault ||
		state.taskCancelKind != TaskCancelNone || state.taskCancelPhase != taskCancelIdle ||
		state.cancelKind != ParkCancelNone || state.outcome != ParkOutcomePending ||
		state.winnerCase != 0 || state.winnerID != (OperationID{}) ||
		state.winnerRecord != nil || state.head == nil || state.head.previous != nil ||
		state.head.next != nil || !validPreparingWaitSetRecord(wait, state, state.ticket) {
		return false
	}
	link := state.head
	record := link.operation
	return link.park == state && link.wait == wait && record != nil &&
		link == &record.link && link.ticket == state.ticket && link.caseID == caseID &&
		record.id == id && record.phase == operationActive &&
		record.disposition == OperationDispositionPending && !record.resolutionApplied &&
		operationCandidatePendingResultStorageValid(record) &&
		operationCandidatePendingForResolution(record)
}

func prepareSingleChannelParkOrdinary(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	source *ChannelOperationSource,
	wait *WaitSetRecord,
	claim *SelectClaim,
	prepared *ChannelDirectReservation,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	if !ValidG(g) || handle == nil || header == nil || source == nil || wait == nil || claim == nil ||
		*wait != (WaitSetRecord{}) || *claim != (SelectClaim{}) || caseID == 0 ||
		!resumeGateTaken(g) || g.runP == nil || g.pending.kind != pendingNone ||
		g.spawnChild != nil || g.waiting || g.park.taskCancelKind != TaskCancelNone ||
		g.park.taskCancelPhase != taskCancelIdle || !validReusableSingleParkState(&g.park) ||
		g.park.attached != 0 || g.park.head != nil {
		return ParkTicket{}, OperationID{}, false
	}
	p := g.runP
	frame := findFrame(g, handle)
	if frame == nil || frame != g.active || frame.header != header || frame.state != FrameActive ||
		frame.parkWait != nil || header.SuspendReason != uint16(SuspendPark) ||
		header.Lifecycle != uint16(FrameSuspended) {
		return ParkTicket{}, OperationID{}, false
	}
	var reservation ChannelDirectReservation
	if prepared == nil {
		var reservationOK bool
		reservation, reservationOK = source.PreflightDirectReservation(p)
		if !reservationOK {
			return ParkTicket{}, OperationID{}, false
		}
	} else {
		reservation = *prepared
		if !validDirectReservationHeader(source, p, reservation) {
			return ParkTicket{}, OperationID{}, false
		}
	}
	ticket, ticketOK := nextParkTicket(g.park.ticket)
	if !ticketOK {
		return ParkTicket{}, OperationID{}, false
	}
	// Write the complete Preparing value only after every allocation/capacity/
	// frame preflight above has succeeded.
	g.park = ParkState{
		ticket:   ticket,
		phase:    parkPreparing,
		expected: 1,
		seed:     seed ^ ticket.generation*0x9e3779b9 ^ ticket.epoch*0x85ebca6b,
	}
	wait.g = g
	wait.ticket = ticket
	wait.state = waitSetRecordPreparing
	id, ok := source.reserveAndAttachDirectWait(
		p, &g.park, ticket, wait, caseID, claim, reservation,
	)
	if !ok {
		return ParkTicket{}, OperationID{}, false
	}
	// One candidate is already sorted and unique. Freeze the preparation seed
	// into the ordinary Parked visit cursor, then perform the transaction's one
	// complete post-build graph audit before publishing frame/scheduler state.
	g.park.seed = 0
	g.park.phase = parkParked
	if !validPreparedDirectChannelParkState(&g.park, wait, id, caseID) {
		return ParkTicket{}, OperationID{}, false
	}
	wait.state = waitSetRecordCommitted
	frame.parkWait = wait
	g.pending = pendingTransition{kind: pendingParkSet, from: frame}
	if !source.exposeExternalCommitDirect(g, id, ticket, wait, claim, reservation) {
		return ParkTicket{}, OperationID{}, false
	}
	return ticket, id, true
}

func prepareSingleChannelParkGeneric(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	source *ChannelOperationSource,
	wait *WaitSetRecord,
	claim *SelectClaim,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	if !ValidG(g) || handle == nil || header == nil || source == nil || wait == nil || claim == nil ||
		*wait != (WaitSetRecord{}) || *claim != (SelectClaim{}) || caseID == 0 ||
		!resumeGateTaken(g) || g.runP == nil || !validChannelOperationOwner(source, g.runP) ||
		!CanReserveChannelOperations(g.runP, source, 1) {
		return ParkTicket{}, OperationID{}, false
	}
	p := g.runP
	ticket, ok := BeginParkSet(&g.park, 1, seed)
	if !ok || !PrepareWaitSetRecord(wait, g, ticket) {
		return ParkTicket{}, OperationID{}, false
	}
	id, ok := source.ReserveAndAttachWait(p, &g.park, ticket, wait, caseID, claim)
	if !ok || !SealParkSet(&g.park, ticket) ||
		!PrepareParkSet(g, handle, header, ticket, wait) ||
		!source.ExposeExternalCommit(p, g, id, ticket, wait, claim) {
		return ParkTicket{}, OperationID{}, false
	}
	return ticket, id, true
}

// PrepareEmptyChannelPark is the nil-channel counterpart. It publishes a
// zero-candidate ParkSet which cannot become ready through an operation source
// but remains reachable by task abort/shutdown cancellation. No permanent
// scheduler or source object is allocated for the nil channel.
func PrepareEmptyChannelPark(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	seed uint32,
) (ParkTicket, bool) {
	if !ValidG(g) || handle == nil || header == nil || wait == nil || *wait != (WaitSetRecord{}) ||
		!resumeGateTaken(g) {
		return ParkTicket{}, false
	}
	ticket, ok := BeginParkSet(&g.park, 0, seed)
	if !ok || !PrepareWaitSetRecord(wait, g, ticket) || !SealParkSet(&g.park, ticket) ||
		!PrepareParkSet(g, handle, header, ticket, wait) {
		return ParkTicket{}, false
	}
	return ticket, true
}

// CanReserveChannelOperations is the allocation/preflight boundary for a
// compiler park transaction. No ParkState field or producer-visible
// generation changes until this check succeeds. The configured source uses a
// bounded scan over stable pages attached before bind; configuration cannot
// change inside the no-fail preparation section.
func CanReserveChannelOperations(p *P, source *ChannelOperationSource, needed uint32) bool {
	capacity := ChannelOperationConfiguredCapacity(source)
	if !validChannelOperationOwner(source, p) || needed == 0 || needed > capacity {
		return false
	}
	start := source.reserveCursor
	if start >= capacity {
		start = 0
	}
	available := uint32(0)
	for offset := uint32(0); offset < capacity; offset++ {
		index := start + offset
		if index >= capacity {
			index -= capacity
		}
		slot, ok := channelOperationSlotAt(source, index)
		if !ok {
			return false
		}
		if channelOperationReusableSlot(source, slot, index) &&
			preemptLoad(&slot.generation) != ^uint32(0) &&
			channelOperationExternalReservable(slot) {
			available++
			if available == needed {
				return true
			}
		}
	}
	return false
}

// FinishSingleChannelPark is the compatibility release boundary for adapters
// which have not opted into ResumeCleanupPlan. A valid lease
// is taken for a selected continuation or discarded when task cancellation
// suppresses an already committed result. A canceled operation with no
// physical winner carries a zero lease.
func FinishSingleChannelPark(
	g *G,
	source *ChannelOperationSource,
	id OperationID,
	claim *SelectClaim,
	lease OperationResultLease,
	discard bool,
) bool {
	if !resumeGateTaken(g) || g.runP == nil || source == nil || !id.Valid() || claim == nil ||
		!validChannelOperationOwner(source, g.runP) {
		return false
	}
	if lease.Valid() {
		leaseID, ok := lease.ID()
		if !ok || leaseID != id {
			return false
		}
	}
	p := g.runP
	if !source.ConfirmQuiesced(p, id) || !source.ResetSelectClaim(p, claim) {
		return false
	}
	if lease.Valid() {
		var released bool
		if discard {
			released = source.DiscardResult(p, lease)
		} else {
			released = source.TakeResult(p, lease)
		}
		if !released {
			return false
		}
	}
	return source.Recycle(p, id)
}
