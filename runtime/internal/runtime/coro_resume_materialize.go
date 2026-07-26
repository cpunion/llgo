/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

const coroHostOperationParkMagicV1 uint32 = 0x43484f31         // "CHO1"
const coroHostOperationDeadlineParkMagicV1 uint32 = 0x43484431 // "CHD1"

const (
	coroHostOperationDeadlineWorkerCaseV1 uint32 = 1
	coroHostOperationDeadlineTimerCaseV1  uint32 = 2
)

// CoroHostOperationParkV1 is compiler-owned current-frame storage. Source and
// transport state are materialized on the old owner before runnable
// publication; resume consumes only packet and scalar frame state.
type CoroHostOperationParkV1 struct {
	magic     uint32
	wait      coro.WaitSetRecord
	packet    coro.ResumePacket
	cleanup   coro.ResumeCleanupPlan
	ticket    coro.ParkTicket
	operation coro.OperationID
}

// CoroHostOperationDeadlineParkV1 is compiler-owned frame storage for the
// two-source HostOp/timer ParkSet. The embedding receives only the worker
// OperationID and copied request words; timer and Go pointers stay scheduler
// owned until old-owner materialization has retired both source generations.
type CoroHostOperationDeadlineParkV1 struct {
	magic        uint32
	wait         coro.WaitSetRecord
	packet       coro.ResumePacket
	cleanup      coro.ResumeCleanupPlan
	ticket       coro.ParkTicket
	operations   [2]coro.OperationID
	timer        coro.TimerRegistrationHandle
	executor     coro.ExecutorHandle
	timeoutErrno uintptr
	controlKey   uintptr
	controlLane  uint32
	resumeCase   uint32
}

type coroKeyedParkKindV2 uint32

const (
	coroKeyedParkInvalidV2 coroKeyedParkKindV2 = iota
	coroKeyedParkSemaphoreV2
	coroKeyedParkNotifyV2
)

const (
	coroKeyedParkPreparedMagicV2     uint32 = 0x4b505250 // KPRP
	coroKeyedParkActiveMagicV2       uint32 = 0x4b504152 // KPAR
	coroKeyedParkMaterializedMagicV2 uint32 = 0x4b504d41 // KPMA
)

// Two native domains each configure 1024 manual-operation slots. The registry
// stores only scalar equality keys and POD identities; it never retains G, P,
// ParkState, an LLVM handle, or any other Go pointer.
const coroKeyedRegistryCapacityV2 = 2 * 1024

type coroKeyedRegistryHandleV2 struct {
	Slot       uint32
	Generation uint32
}

type coroKeyedRegistrySlotStateV2 uint32

const (
	coroKeyedRegistryFreeV2 coroKeyedRegistrySlotStateV2 = iota
	coroKeyedRegistryActiveV2
	coroKeyedRegistryPostingV2
	coroKeyedRegistryDeliveredV2
)

type coroKeyedRegistrySlotV2 struct {
	generation uint32
	state      coroKeyedRegistrySlotStateV2
	kind       coroKeyedParkKindV2
	logical    uint32
	key        uintptr
	sequence   uint64
	operation  coro.OperationID
}

type coroKeyedRegistryV2 struct {
	mutex    channelMutex
	sequence uint64
	slots    [coroKeyedRegistryCapacityV2]coroKeyedRegistrySlotV2
}

var coroProgramKeyedRegistryV2State coroKeyedRegistryV2

// CoroKeyedParkV2 is compiler-spilled state for semaphore and notify waits.
// The old owner retires its private key registry and the route-local Manual
// generation before publishing the packet as P-neutral.
type CoroKeyedParkV2 struct {
	wait      coro.WaitSetRecord
	packet    coro.ResumePacket
	cleanup   coro.ResumeCleanupPlan
	ticket    coro.ParkTicket
	operation coro.OperationID
	registry  coroKeyedRegistryHandleV2
	key       uintptr
	logical   uint32
	kind      coroKeyedParkKindV2
	magic     uint32
}

// Standard-library semaphore and notify wrappers reserve one 256-byte
// pointer-aligned opaque object on every target. The runtime layout is 256
// bytes on native64 and 196 bytes on wasm32; this gate prevents silent growth.
var (
	_ [256 - unsafe.Sizeof(CoroKeyedParkV2{})]byte
	_ [unsafe.Alignof(uintptr(0)) - unsafe.Alignof(CoroKeyedParkV2{})]byte
	_ [unsafe.Alignof(CoroKeyedParkV2{}) - unsafe.Alignof(uintptr(0))]byte
)

func validPreparedCoroKeyedParkV2(state *CoroKeyedParkV2) bool {
	return state != nil && state.magic == coroKeyedParkPreparedMagicV2 &&
		(state.kind == coroKeyedParkSemaphoreV2 || state.kind == coroKeyedParkNotifyV2) &&
		state.key != 0 && state.wait == (coro.WaitSetRecord{}) &&
		state.packet == (coro.ResumePacket{}) && state.cleanup == (coro.ResumeCleanupPlan{}) &&
		state.ticket == (coro.ParkTicket{}) && state.operation == (coro.OperationID{}) &&
		state.registry == (coroKeyedRegistryHandleV2{})
}

func validActiveCoroKeyedParkV2(state *CoroKeyedParkV2) bool {
	return state != nil && state.magic == coroKeyedParkActiveMagicV2 &&
		(state.kind == coroKeyedParkSemaphoreV2 || state.kind == coroKeyedParkNotifyV2) &&
		state.key != 0 && state.ticket.Valid() && state.operation.Valid() &&
		state.operation.Source() == coro.OperationSourceManual &&
		state.registry.Slot != 0 && state.registry.Generation != 0 &&
		state.packet != (coro.ResumePacket{}) && state.cleanup != (coro.ResumeCleanupPlan{})
}

func validMaterializedCoroKeyedParkV2(state *CoroKeyedParkV2) bool {
	return state != nil && state.magic == coroKeyedParkMaterializedMagicV2 &&
		state.wait == (coro.WaitSetRecord{}) && state.ticket.Valid() &&
		state.operation == (coro.OperationID{}) &&
		state.registry == (coroKeyedRegistryHandleV2{}) && state.key == 0 &&
		state.logical == 0 && state.kind == coroKeyedParkInvalidV2 &&
		state.packet != (coro.ResumePacket{}) && state.cleanup == (coro.ResumeCleanupPlan{})
}

func coroKeyedRegistryReusableSlotV2(slot *coroKeyedRegistrySlotV2) bool {
	return slot != nil && slot.state == coroKeyedRegistryFreeV2 &&
		slot.kind == coroKeyedParkInvalidV2 && slot.logical == 0 && slot.key == 0 &&
		slot.sequence == 0 && slot.operation == (coro.OperationID{})
}

func coroKeyedRegistrySlotV2For(
	registry *coroKeyedRegistryV2,
	handle coroKeyedRegistryHandleV2,
) (*coroKeyedRegistrySlotV2, bool) {
	if registry == nil || handle.Slot == 0 || handle.Generation == 0 ||
		handle.Slot > uint32(len(registry.slots)) {
		return nil, false
	}
	return &registry.slots[handle.Slot-1], true
}

// retire detaches the exact private registry identity in one bounded critical
// section. Posting may be cleared because its producer owns only the POD
// handle and OperationID: finishPost observes the retired generation and skips
// publication. Once Delivered is visible, any concurrent source Post is
// protected independently by ManualOperationSource admission/quiescence.
func (registry *coroKeyedRegistryV2) retire(
	handle coroKeyedRegistryHandleV2,
	operation coro.OperationID,
) bool {
	registry.mutex.Lock()
	slot, ok := coroKeyedRegistrySlotV2For(registry, handle)
	if !ok || slot.generation != handle.Generation || slot.operation != operation ||
		(slot.state != coroKeyedRegistryActiveV2 &&
			slot.state != coroKeyedRegistryPostingV2 &&
			slot.state != coroKeyedRegistryDeliveredV2) {
		registry.mutex.Unlock()
		return false
	}
	generation := slot.generation
	*slot = coroKeyedRegistrySlotV2{generation: generation}
	registry.mutex.Unlock()
	return true
}

var coroHostOperationAdapterV1State coro.HostOperationAdapter

const (
	CoroHostOperationControlReadV1 uint32 = 1 << iota
	CoroHostOperationControlWriteV1
)

const coroHostOperationControlCapacityV1 = 64

type coroHostOperationControlLaneV1 struct {
	operation coro.OperationID
	epoch     uint32
}

type coroHostOperationControlSlotV1 struct {
	read  coroHostOperationControlLaneV1
	write coroHostOperationControlLaneV1
}

// The table is owner-executor state. It retains only exact scalar operation
// generations, never a G, frame, WaitSetRecord, or buffer pointer.
var coroHostOperationControlSlotsV1 [coroHostOperationControlCapacityV1]coroHostOperationControlSlotV1

func coroHostOperationControlCellV1(
	key uintptr,
	lane uint32,
) (*coroHostOperationControlLaneV1, bool) {
	if key == 0 || key > uintptr(len(coroHostOperationControlSlotsV1)) {
		return nil, false
	}
	slot := &coroHostOperationControlSlotsV1[key-1]
	switch lane {
	case CoroHostOperationControlReadV1:
		return &slot.read, true
	case CoroHostOperationControlWriteV1:
		return &slot.write, true
	default:
		return nil, false
	}
}

func coroHostOperationControlBindV1(key uintptr, lane uint32, id coro.OperationID) bool {
	cell, ok := coroHostOperationControlCellV1(key, lane)
	if !ok || !id.Valid() || id.Source() != coro.OperationSourceWorker ||
		cell.operation != (coro.OperationID{}) {
		return false
	}
	cell.operation = id
	return true
}

func coroHostOperationControlUnbindV1(key uintptr, lane uint32, id coro.OperationID) bool {
	cell, ok := coroHostOperationControlCellV1(key, lane)
	if !ok || cell.operation != id {
		return false
	}
	cell.operation = coro.OperationID{}
	return true
}

func validCoroHostOperationParkV1(state *CoroHostOperationParkV1) bool {
	return state != nil && state.magic == coroHostOperationParkMagicV1 &&
		state.ticket.Valid() && state.operation.Valid() &&
		state.operation.Source() == coro.OperationSourceWorker
}

func validMaterializedCoroHostOperationParkV1(state *CoroHostOperationParkV1) bool {
	return state != nil && state.magic == coroHostOperationParkMagicV1 &&
		state.wait == (coro.WaitSetRecord{}) && state.ticket.Valid() &&
		state.operation == (coro.OperationID{}) &&
		state.cleanup == (coro.ResumeCleanupPlan{})
}

func validCoroHostOperationDeadlineParkV1(state *CoroHostOperationDeadlineParkV1) bool {
	if state == nil || state.magic != coroHostOperationDeadlineParkMagicV1 ||
		!state.ticket.Valid() || !state.operations[0].Valid() ||
		state.operations[0].Source() != coro.OperationSourceWorker ||
		state.executor.Slot == 0 || state.executor.Generation == 0 ||
		state.timeoutErrno == 0 {
		return false
	}
	timerAbsent := state.timer == (coro.TimerRegistrationHandle{}) &&
		state.operations[1] == (coro.OperationID{})
	timerValid := state.timer.Slot != 0 && state.timer.Generation != 0 &&
		state.operations[1].Valid() &&
		state.operations[1].Source() == coro.OperationSourceTimer &&
		state.operations[1].LocalSlot() == state.timer.Slot &&
		state.operations[1].Generation == state.timer.Generation
	cell, controlOK := coroHostOperationControlCellV1(state.controlKey, state.controlLane)
	return (timerAbsent || timerValid) && controlOK && cell.operation == state.operations[0]
}

func validMaterializedCoroHostOperationDeadlineParkV1(
	state *CoroHostOperationDeadlineParkV1,
) bool {
	if state == nil || state.magic != coroHostOperationDeadlineParkMagicV1 ||
		state.wait != (coro.WaitSetRecord{}) || !state.ticket.Valid() ||
		state.operations != ([2]coro.OperationID{}) ||
		state.timer != (coro.TimerRegistrationHandle{}) ||
		state.executor != (coro.ExecutorHandle{}) ||
		state.timeoutErrno == 0 || state.cleanup != (coro.ResumeCleanupPlan{}) {
		return false
	}
	cell, ok := coroHostOperationControlCellV1(state.controlKey, state.controlLane)
	return ok && cell.operation == (coro.OperationID{}) &&
		state.resumeCase <= coroHostOperationDeadlineTimerCaseV1
}

func coroMaterializePrivateResumeCleanupStepV1(step coro.ResumeCleanupStep) bool {
	if step.Index != 0 {
		return false
	}
	switch step.Kind {
	case coro.ResumeCleanupHostOperation:
		state := (*CoroHostOperationParkV1)(step.Context)
		if !validCoroHostOperationParkV1(state) ||
			!coroHostOperationAdapterV1State.Retire(state.operation) {
			return false
		}
	case coro.ResumeCleanupHostOperationDeadline:
		state := (*CoroHostOperationDeadlineParkV1)(step.Context)
		if !validCoroHostOperationDeadlineParkV1(state) {
			return false
		}
		worker := state.operations[0]
		if !coroHostOperationAdapterV1State.Retire(worker) ||
			!coroHostOperationControlUnbindV1(state.controlKey, state.controlLane, worker) {
			return false
		}
		state.resumeCase = step.WinnerCase
		state.timer = coro.TimerRegistrationHandle{}
		state.executor = coro.ExecutorHandle{}
	case coro.ResumeCleanupKeyedPark:
		state := (*CoroKeyedParkV2)(step.Context)
		if !validActiveCoroKeyedParkV2(state) ||
			!coroProgramKeyedRegistryV2State.retire(state.registry, state.operation) {
			return false
		}
		state.registry = coroKeyedRegistryHandleV2{}
		state.key = 0
		state.logical = 0
		state.kind = coroKeyedParkInvalidV2
		state.magic = coroKeyedParkMaterializedMagicV2
	default:
		return false
	}
	return coro.CommitResumeCleanupStep(step, coro.ResumeSmallInvalid)
}

// coroMaterializeResumeCleanupStepV1 is the sole closed runtime dispatch for
// typed frame cleanup. Source confirmation/result/recycle remains in the
// source-neutral coro core; this switch exposes only target/runtime layouts.
func coroMaterializeResumeCleanupStepV1(step coro.ResumeCleanupStep) bool {
	switch step.Kind {
	case coro.ResumeCleanupChannelDirect, coro.ResumeCleanupChannelSelect:
		return coroMaterializeChannelResumeCleanupStepV1(step)
	case coro.ResumeCleanupHostOperation, coro.ResumeCleanupHostOperationDeadline,
		coro.ResumeCleanupKeyedPark:
		return coroMaterializePrivateResumeCleanupStepV1(step)
	default:
		return false
	}
}
