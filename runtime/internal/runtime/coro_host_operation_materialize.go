/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

import "github.com/goplus/llgo/runtime/internal/coro"

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
		state.ticket.Valid() && state.operation == (coro.OperationID{}) &&
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
		!state.ticket.Valid() || state.operations != ([2]coro.OperationID{}) ||
		state.timer != (coro.TimerRegistrationHandle{}) ||
		state.executor != (coro.ExecutorHandle{}) ||
		state.timeoutErrno == 0 || state.cleanup != (coro.ResumeCleanupPlan{}) {
		return false
	}
	cell, ok := coroHostOperationControlCellV1(state.controlKey, state.controlLane)
	return ok && cell.operation == (coro.OperationID{}) &&
		state.resumeCase <= coroHostOperationDeadlineTimerCaseV1
}

func coroMaterializeHostOperationResumeCleanupStepV1(step coro.ResumeCleanupStep) bool {
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
	default:
		return false
	}
	return coro.CommitResumeCleanupStep(step, coro.ResumeSmallInvalid)
}
