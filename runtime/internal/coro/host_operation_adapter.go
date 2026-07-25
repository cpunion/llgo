/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package coro

import "unsafe"

// HostOperationMaxArgsV1 is the common copied-word limit shared with the
// native worker transport. Keeping the transport shape identical lets both
// backends use WorkerOperationSource for logical completion, select, task
// cancellation, and strong frame retirement.
const HostOperationMaxArgsV1 = 9

// HostOperationCapacityV1 matches the inline WorkerOperationSource page used
// by host-pull targets. A future target may page both catalogs together; the
// first ABI deliberately keeps a fixed allocation-free one-to-one mapping.
const HostOperationCapacityV1 = WorkerOperationSourceCapacity

type HostOperationActionKindV1 uint32

const (
	HostOperationActionNoneV1 HostOperationActionKindV1 = iota
	HostOperationActionSubmitV1
	HostOperationActionCancelV1
)

// HostOperationActionV1 is a padding-free 96-byte POD borrowed-memory
// request. Every argument is encoded as low/high uint32 words, so the same ABI
// carries wasm32 offsets and native/embedded 64-bit words. Pointer arguments
// are guest addresses borrowed only until exact-generation completion; the
// compiler keeps their typed owners in the suspended coroutine frame.
type HostOperationActionV1 struct {
	Kind             uint32
	SourceSlot       uint32
	SourceGeneration uint32
	Opcode           uint32
	ArgCount         uint32
	Reserved         uint32
	Args             [HostOperationMaxArgsV1 * 2]uint32
}

var (
	_ [96 - unsafe.Sizeof(HostOperationActionV1{})]byte
	_ [unsafe.Sizeof(HostOperationActionV1{}) - 96]byte
	_ [4 - unsafe.Alignof(HostOperationActionV1{})]byte
	_ [unsafe.Alignof(HostOperationActionV1{}) - 4]byte
	_ [24 - unsafe.Offsetof(HostOperationActionV1{}.Args)]byte
	_ [unsafe.Offsetof(HostOperationActionV1{}.Args) - 24]byte
)

type HostOperationCancelResultV1 uint8

const (
	HostOperationCancelInvalidV1 HostOperationCancelResultV1 = iota
	HostOperationCancelRequestedV1
	HostOperationCancelAlreadyRequestedV1
	HostOperationCancelCompletionPendingV1
)

const (
	hostOperationFree uint32 = iota
	hostOperationPublishing
	hostOperationPending
	hostOperationSubmitCancelPending
	hostOperationDelivered
	hostOperationCancelPending
	hostOperationCancelDelivered
	hostOperationCompleting
	hostOperationCompleted
	hostOperationRetiring
)

type hostOperationSlotV1 struct {
	state      uint32
	sourceSlot uint32
	generation uint32
	opcode     uint32
	argc       uint32
	args       [HostOperationMaxArgsV1]uintptr
}

// HostOperationCompletionLeaseV1 is process-local producer state. Unlike the
// host wire action it never crosses an ABI boundary; it lets a target roll
// back a failed source publication without reopening a different generation.
type HostOperationCompletionLeaseV1 struct {
	sourceSlot uint32
	generation uint32
	previous   uint32
}

func (lease HostOperationCompletionLeaseV1) valid() bool {
	return lease.sourceSlot != 0 && lease.generation != 0 &&
		(lease.previous == hostOperationDelivered ||
			lease.previous == hostOperationCancelPending ||
			lease.previous == hostOperationCancelDelivered)
}

// HostOperationAdapter is only the physical host transport. It intentionally
// owns no ParkState, G, scheduler queue, result cell, or callback. The logical
// operation remains in WorkerOperationSource and is addressed by the same
// two-word OperationID from submit through completion and retirement.
type HostOperationAdapter struct {
	active uint32
	slots  [HostOperationCapacityV1]hostOperationSlotV1
}

func hostOperationAdjustActiveV1(adapter *HostOperationAdapter, delta int32) bool {
	if adapter == nil || delta != 1 && delta != -1 {
		return false
	}
	for {
		current := preemptLoad(&adapter.active)
		if delta > 0 {
			if current == ^uint32(0) {
				return false
			}
			if preemptCompareAndSwap(&adapter.active, current, current+1) {
				return true
			}
		} else {
			if current == 0 {
				return false
			}
			if preemptCompareAndSwap(&adapter.active, current, current-1) {
				return true
			}
		}
	}
}

func hostOperationSlotForV1(adapter *HostOperationAdapter, id OperationID) (*hostOperationSlotV1, bool) {
	if adapter == nil || !id.Valid() || id.Source() != OperationSourceWorker ||
		id.LocalSlot() == 0 || id.LocalSlot() > HostOperationCapacityV1 {
		return nil, false
	}
	return &adapter.slots[id.LocalSlot()-1], true
}

func hostOperationSlotIDV1(slot *hostOperationSlotV1) OperationID {
	if slot == nil {
		return OperationID{}
	}
	return OperationID{
		SourceSlot: preemptLoad(&slot.sourceSlot),
		Generation: preemptLoad(&slot.generation),
	}
}

func hostOperationSlotMatchesV1(slot *hostOperationSlotV1, id OperationID) bool {
	return slot != nil && id.Valid() && hostOperationSlotIDV1(slot) == id
}

func (adapter *HostOperationAdapter) Submit(id OperationID, opcode uint32, args []uintptr) bool {
	slot, ok := hostOperationSlotForV1(adapter, id)
	if !ok || opcode == 0 || len(args) > HostOperationMaxArgsV1 ||
		!preemptCompareAndSwap(&slot.state, hostOperationFree, hostOperationPublishing) {
		return false
	}
	if slot.sourceSlot != 0 || slot.generation != 0 || slot.opcode != 0 || slot.argc != 0 ||
		slot.args != ([HostOperationMaxArgsV1]uintptr{}) {
		preemptStore(&slot.state, hostOperationFree)
		return false
	}
	slot.sourceSlot = id.SourceSlot
	slot.generation = id.Generation
	slot.opcode = opcode
	slot.argc = uint32(len(args))
	copy(slot.args[:], args)
	if !hostOperationAdjustActiveV1(adapter, 1) {
		slot.sourceSlot = 0
		slot.generation = 0
		slot.opcode = 0
		slot.argc = 0
		slot.args = [HostOperationMaxArgsV1]uintptr{}
		preemptStore(&slot.state, hostOperationFree)
		return false
	}
	preemptStore(&slot.state, hostOperationPending)
	return true
}

func fillHostOperationActionV1(slot *hostOperationSlotV1, kind HostOperationActionKindV1, out *HostOperationActionV1) bool {
	if slot == nil || out == nil ||
		(kind != HostOperationActionSubmitV1 && kind != HostOperationActionCancelV1) {
		return false
	}
	id := hostOperationSlotIDV1(slot)
	argc := preemptLoad(&slot.argc)
	opcode := preemptLoad(&slot.opcode)
	if !id.Valid() || id.Source() != OperationSourceWorker || opcode == 0 ||
		argc > HostOperationMaxArgsV1 {
		return false
	}
	*out = HostOperationActionV1{
		Kind:             uint32(kind),
		SourceSlot:       id.SourceSlot,
		SourceGeneration: id.Generation,
		Opcode:           opcode,
		ArgCount:         argc,
	}
	for index := uint32(0); index < argc; index++ {
		word := uint64(slot.args[index])
		out.Args[index*2] = uint32(word)
		out.Args[index*2+1] = uint32(word >> 32)
	}
	return true
}

func takeHostOperationActionV1(
	adapter *HostOperationAdapter,
	from, to uint32,
	kind HostOperationActionKindV1,
	out *HostOperationActionV1,
) bool {
	for index := range adapter.slots {
		slot := &adapter.slots[index]
		if preemptLoad(&slot.state) != from ||
			!preemptCompareAndSwap(&slot.state, from, to) {
			continue
		}
		if fillHostOperationActionV1(slot, kind, out) {
			return true
		}
		// Published fields are immutable until retirement. A malformed slot is
		// an internal invariant rather than an empty catalog observation.
		preemptStore(&slot.state, from)
		return false
	}
	return false
}

// NextAction transfers one exact physical obligation. The embedding serializes
// calls to NextAction. A cancellation requested before Submit was observed
// retains both obligations: Submit is always transferred first, and a later
// call transfers Cancel for the same exact generation.
func (adapter *HostOperationAdapter) NextAction(out *HostOperationActionV1) bool {
	if adapter == nil || out == nil {
		return false
	}
	*out = HostOperationActionV1{}
	if preemptLoad(&adapter.active) == 0 {
		return false
	}
	if takeHostOperationActionV1(
		adapter,
		hostOperationSubmitCancelPending,
		hostOperationCancelPending,
		HostOperationActionSubmitV1,
		out,
	) {
		return true
	}
	if takeHostOperationActionV1(
		adapter,
		hostOperationPending,
		hostOperationDelivered,
		HostOperationActionSubmitV1,
		out,
	) {
		return true
	}
	return takeHostOperationActionV1(
		adapter,
		hostOperationCancelPending,
		hostOperationCancelDelivered,
		HostOperationActionCancelV1,
		out,
	)
}

// RequestCancel mirrors the logical source's durable cancelRequested bit into
// the host transport. Completion is still the physical acknowledgement: a
// host which cannot revoke an operation may finish it normally, while a
// cancellable backend responds to the Cancel action with its terminal result.
func (adapter *HostOperationAdapter) RequestCancel(id OperationID) HostOperationCancelResultV1 {
	slot, ok := hostOperationSlotForV1(adapter, id)
	if !ok || !hostOperationSlotMatchesV1(slot, id) {
		return HostOperationCancelInvalidV1
	}
	for {
		switch state := preemptLoad(&slot.state); state {
		case hostOperationPending:
			if !preemptCompareAndSwap(&slot.state, state, hostOperationSubmitCancelPending) {
				continue
			}
			return HostOperationCancelRequestedV1
		case hostOperationDelivered:
			if !preemptCompareAndSwap(&slot.state, state, hostOperationCancelPending) {
				continue
			}
			return HostOperationCancelRequestedV1
		case hostOperationSubmitCancelPending, hostOperationCancelPending, hostOperationCancelDelivered:
			return HostOperationCancelAlreadyRequestedV1
		case hostOperationCompleting, hostOperationCompleted:
			return HostOperationCancelCompletionPendingV1
		default:
			return HostOperationCancelInvalidV1
		}
	}
}

// BeginComplete acquires the exact request generation before a target
// publishes its scalar result into WorkerOperationSource.
func (adapter *HostOperationAdapter) BeginComplete(id OperationID) (HostOperationCompletionLeaseV1, bool) {
	slot, ok := hostOperationSlotForV1(adapter, id)
	if !ok || !hostOperationSlotMatchesV1(slot, id) {
		return HostOperationCompletionLeaseV1{}, false
	}
	for {
		state := preemptLoad(&slot.state)
		switch state {
		case hostOperationDelivered, hostOperationCancelPending, hostOperationCancelDelivered:
			if !preemptCompareAndSwap(&slot.state, state, hostOperationCompleting) {
				continue
			}
			return HostOperationCompletionLeaseV1{
				sourceSlot: id.SourceSlot,
				generation: id.Generation,
				previous:   state,
			}, true
		default:
			return HostOperationCompletionLeaseV1{}, false
		}
	}
}

func (adapter *HostOperationAdapter) CommitComplete(lease HostOperationCompletionLeaseV1) bool {
	id := OperationID{SourceSlot: lease.sourceSlot, Generation: lease.generation}
	slot, ok := hostOperationSlotForV1(adapter, id)
	return lease.valid() && ok && hostOperationSlotMatchesV1(slot, id) &&
		preemptCompareAndSwap(&slot.state, hostOperationCompleting, hostOperationCompleted)
}

func (adapter *HostOperationAdapter) AbortComplete(lease HostOperationCompletionLeaseV1) bool {
	id := OperationID{SourceSlot: lease.sourceSlot, Generation: lease.generation}
	slot, ok := hostOperationSlotForV1(adapter, id)
	return lease.valid() && ok && hostOperationSlotMatchesV1(slot, id) &&
		preemptCompareAndSwap(&slot.state, hostOperationCompleting, lease.previous)
}

func (adapter *HostOperationAdapter) Retire(id OperationID) bool {
	slot, ok := hostOperationSlotForV1(adapter, id)
	if !ok || !hostOperationSlotMatchesV1(slot, id) ||
		!preemptCompareAndSwap(&slot.state, hostOperationCompleted, hostOperationRetiring) {
		return false
	}
	slot.sourceSlot = 0
	slot.generation = 0
	slot.opcode = 0
	slot.argc = 0
	slot.args = [HostOperationMaxArgsV1]uintptr{}
	preemptStore(&slot.state, hostOperationFree)
	return hostOperationAdjustActiveV1(adapter, -1)
}

// SnapshotActiveID is the bounded owner-side bridge used to mirror logical
// source cancellation into the physical adapter. It exposes no source or
// frame pointer and is not a host ABI.
func (adapter *HostOperationAdapter) SnapshotActiveID(index uint32) (OperationID, bool) {
	if adapter == nil || index >= HostOperationCapacityV1 {
		return OperationID{}, false
	}
	slot := &adapter.slots[index]
	state := preemptLoad(&slot.state)
	if state == hostOperationFree || state == hostOperationPublishing ||
		state == hostOperationRetiring {
		return OperationID{}, false
	}
	id := hostOperationSlotIDV1(slot)
	return id, id.Valid() && id.Source() == OperationSourceWorker &&
		id.LocalSlot() == index+1
}

func (adapter *HostOperationAdapter) Active() bool {
	return adapter != nil && preemptLoad(&adapter.active) != 0
}

func (adapter *HostOperationAdapter) CanRelease() bool {
	if adapter == nil || preemptLoad(&adapter.active) != 0 {
		return false
	}
	for index := range adapter.slots {
		if adapter.slots[index] != (hostOperationSlotV1{}) {
			return false
		}
	}
	return true
}
