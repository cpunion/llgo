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

// ActiveChannelParkOwner returns the scheduler-owner context used by the
// trusted typed-channel adapter while a compiler resume gate is active. The
// returned ParkState pointer is frame-independent G storage and must not be
// retained after the adapter returns or passed to a producer.
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
	available := uint32(0)
	for index := uint32(0); index < capacity; index++ {
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

// FinishSingleChannelPark releases one detached channel operation after the
// compiler's exact-ticket resume gate has consumed its RunDecision and the
// typed hchan layer has removed the frame node from its queue. A valid lease
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
