//go:build llgo && llgo_coro && !coro_runtime_adapter_test && (wasm || tinygo.wasm || baremetal || llgo_coro_host) && !(llgo_coro_native_pipe && (darwin || linux) && !baremetal)

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

const coroHostOperationDeadlineParkMagicV1 uint32 = 0x43484431 // "CHD1"

const (
	coroHostOperationDeadlineWorkerCaseV1 uint32 = 1
	coroHostOperationDeadlineTimerCaseV1  uint32 = 2
)

// CoroHostOperationDeadlineParkV1 is compiler-owned frame storage for the
// two-source HostOp/timer ParkSet. The embedding receives only the worker
// OperationID and copied request words; the timer and all Go pointers remain
// owned by the scheduler.
type CoroHostOperationDeadlineParkV1 struct {
	magic          uint32
	wait           coro.WaitSetRecord
	ticket         coro.ParkTicket
	worker         coro.OperationID
	timer          coro.TimerRegistrationHandle
	timerOperation coro.OperationID
	executor       coro.ExecutorHandle
	timeoutErrno   uintptr
	controlKey     uintptr
	controlLane    uint32
}

func validCoroHostOperationDeadlineParkV1(state *CoroHostOperationDeadlineParkV1) bool {
	if state == nil || state.magic != coroHostOperationDeadlineParkMagicV1 ||
		!state.ticket.Valid() || !state.worker.Valid() ||
		state.worker.Source() != coro.OperationSourceWorker ||
		state.executor.Slot == 0 || state.executor.Generation == 0 ||
		state.timeoutErrno == 0 {
		return false
	}
	timerAbsent := state.timer == (coro.TimerRegistrationHandle{}) &&
		state.timerOperation == (coro.OperationID{})
	timerValid := state.timer.Slot != 0 && state.timer.Generation != 0 &&
		state.timerOperation.Valid() &&
		state.timerOperation.Source() == coro.OperationSourceTimer &&
		state.timerOperation.LocalSlot() == state.timer.Slot &&
		state.timerOperation.Generation == state.timer.Generation
	cell, controlOK := coroHostOperationControlCellV1(state.controlKey, state.controlLane)
	return (timerAbsent || timerValid) && controlOK && cell.operation == state.worker
}

func coroHostOperationDeadlineV1(lo, hi uintptr) (int64, bool) {
	word := uint64(uint32(lo)) | uint64(uint32(hi))<<32
	if word > uint64(^uint64(0)>>1) {
		return 0, false
	}
	return int64(word), true
}

//export __llgo_coro_host_operation_deadline_park_v1
func __llgo_coro_host_operation_deadline_park_v1(
	g, handle, header, storage unsafe.Pointer,
	opcode, argc uint32,
	deadlineLo, deadlineHi, timeoutErrno uintptr,
	controlKey, controlLane, controlEpoch uintptr,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
) {
	state := (*CoroHostOperationDeadlineParkV1)(storage)
	task := (*coro.G)(g)
	deadline, deadlineOK := coroHostOperationDeadlineV1(deadlineLo, deadlineHi)
	lane := uint32(controlLane)
	expectedEpoch := uint32(controlEpoch)
	_, controlOK := coroHostOperationControlCellV1(controlKey, lane)
	driver, wantExecutor, wantRoute, current := coro.CurrentExecutorWorkerDriver(task)
	if g == nil || handle == nil || header == nil || state == nil ||
		*state != (CoroHostOperationDeadlineParkV1{}) || opcode == 0 ||
		argc > coro.HostOperationMaxArgsV1 || timeoutErrno == 0 ||
		uintptr(lane) != controlLane || uintptr(expectedEpoch) != controlEpoch ||
		!controlOK || !deadlineOK || !current {
		coroHostOperationAbortV1("invalid deadline coroutine host operation park ABI")
		return
	}
	ticket, worker, timer, timerOperation, executor, prepared :=
		coro.PrepareCurrentExecutorWorkerTimerPark(
			driver,
			task,
			handle,
			(*coro.HeaderV1)(header),
			&state.wait,
			coroHostOperationDeadlineWorkerCaseV1,
			coroHostOperationDeadlineTimerCaseV1,
			1,
			deadline,
		)
	hasTimer := deadline != 0
	if !prepared || executor != wantExecutor || worker.Route() != wantRoute ||
		hasTimer && timerOperation.Route() != wantRoute ||
		!hasTimer && (timer != (coro.TimerRegistrationHandle{}) ||
			timerOperation != (coro.OperationID{})) {
		coroHostOperationAbortV1("cannot prepare deadline coroutine host operation")
		return
	}
	state.magic = coroHostOperationDeadlineParkMagicV1
	state.ticket = ticket
	state.worker = worker
	state.timer = timer
	state.timerOperation = timerOperation
	state.executor = executor
	state.timeoutErrno = timeoutErrno
	state.controlKey = controlKey
	state.controlLane = lane
	if !coroHostOperationControlBindV1(controlKey, lane, worker) {
		coroHostOperationAbortV1("cannot bind deadline coroutine host operation control")
		return
	}
	args := [coro.HostOperationMaxArgsV1]uintptr{a0, a1, a2, a3, a4, a5, a6, a7, a8}
	if !coro.CommitCurrentExecutorWorkerSubmission(driver, task, worker) ||
		!coroHostOperationAdapterV1State.Submit(worker, opcode, args[:argc]) {
		coroHostOperationAbortV1("cannot publish deadline coroutine host operation")
		return
	}
	currentEpoch, epochOK := CoroHostOperationControlEpochV1(controlKey, lane)
	if !epochOK {
		coroHostOperationAbortV1("cannot validate deadline coroutine host operation epoch")
		return
	}
	if currentEpoch != expectedEpoch &&
		!coro.RequestCurrentExecutorWorkerParkCancel(driver, task, worker, ticket) {
		coroHostOperationAbortV1("cannot cancel reconfigured deadline coroutine host operation")
	}
}

func finishCoroHostOperationDeadlineWorkerV1(
	driver *coro.ExecutorDriver,
	task *coro.G,
	state *CoroHostOperationDeadlineParkV1,
	lease coro.OperationResultLease,
	discard bool,
	payload *coro.ScalarResultPayloadV1,
) bool {
	return coro.FinishCurrentExecutorWorkerPark(
		driver,
		task,
		state.worker,
		lease,
		discard,
		payload,
	) == coro.WorkerParkFinishComplete
}

func finishCoroHostOperationDeadlineTimerV1(
	driver *coro.ExecutorDriver,
	task *coro.G,
	state *CoroHostOperationDeadlineParkV1,
	lease coro.OperationResultLease,
	discard bool,
) bool {
	if state.timerOperation == (coro.OperationID{}) {
		return state.timer == (coro.TimerRegistrationHandle{}) && !lease.Valid()
	}
	return coro.FinishCurrentExecutorTimerPark(
		driver,
		task,
		state.executor,
		state.timer,
		state.timerOperation,
		lease,
		discard,
	)
}

//export __llgo_coro_host_operation_deadline_resume_v1
func __llgo_coro_host_operation_deadline_resume_v1(
	g, storage unsafe.Pointer,
	r1, r2, errno *uintptr,
) uint32 {
	state := (*CoroHostOperationDeadlineParkV1)(storage)
	if g == nil || !validCoroHostOperationDeadlineParkV1(state) ||
		!validCoroHostOperationResultWordsV1(storage, r1, r2, errno) {
		coroHostOperationAbortV1("invalid deadline coroutine host operation resume ABI")
		return 0
	}
	*r1, *r2, *errno = 0, 0, 0
	task := (*coro.G)(g)
	outcome, caseID, lease, cancel, taken := coro.TakeRunDecision(task, state.ticket)
	if !taken {
		coroHostOperationAbortV1("invalid deadline coroutine host operation run decision")
		return 0
	}
	driver, executor, route, current := coro.CurrentExecutorWorkerDriver(task)
	if !current || executor != state.executor ||
		route != state.worker.Route() ||
		state.timerOperation.Valid() && route != state.timerOperation.Route() {
		coroHostOperationAbortV1("deadline coroutine host operation resumed without its owner")
		return 0
	}

	status := uint32(0)
	var payload coro.ScalarResultPayloadV1
	switch {
	case outcome == coro.ParkOutcomeCompleted &&
		caseID == coroHostOperationDeadlineWorkerCaseV1 &&
		lease.Valid() && cancel == coro.TaskCancelNone:
		if !finishCoroHostOperationDeadlineWorkerV1(
			driver, task, state, lease, false, &payload,
		) || !finishCoroHostOperationDeadlineTimerV1(
			driver, task, state, coro.OperationResultLease{}, true,
		) {
			coroHostOperationAbortV1("cannot retire completed deadline host operation")
			return 0
		}
		if payload.Kind() != coro.ScalarResultKindWords || payload.Count() != 3 {
			coroHostOperationAbortV1("invalid deadline host operation result")
			return 0
		}
		outputs := [3]*uintptr{r1, r2, errno}
		for index, output := range outputs {
			value, scalarOK := payload.Scalar(uint8(index))
			if !scalarOK || uint64(uintptr(value)) != value {
				coroHostOperationAbortV1("deadline host operation result does not fit uintptr")
				return 0
			}
			*output = uintptr(value)
		}
		status = coroHostOperationResumeSuccessV1
	case outcome == coro.ParkOutcomeCompleted &&
		caseID == coroHostOperationDeadlineTimerCaseV1 &&
		state.timerOperation.Valid() &&
		lease.Valid() && cancel == coro.TaskCancelNone:
		if !finishCoroHostOperationDeadlineWorkerV1(
			driver, task, state, coro.OperationResultLease{}, true, nil,
		) || !finishCoroHostOperationDeadlineTimerV1(
			driver, task, state, lease, false,
		) {
			coroHostOperationAbortV1("cannot retire timed-out deadline host operation")
			return 0
		}
		*r1 = ^uintptr(0)
		*errno = state.timeoutErrno
		status = coroHostOperationResumeSuccessV1
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && !lease.Valid() &&
		cancel == coro.TaskCancelNone:
		if !finishCoroHostOperationDeadlineWorkerV1(
			driver, task, state, coro.OperationResultLease{}, true, nil,
		) || !finishCoroHostOperationDeadlineTimerV1(
			driver, task, state, coro.OperationResultLease{}, true,
		) {
			coroHostOperationAbortV1("cannot retire reconfigured deadline host operation")
			return 0
		}
		// The synchronous internal/poll wrapper rechecks closing/deadline state
		// before resubmitting. Reuse its timeout errno as an internal retry fact;
		// it is never exposed while the latest deadline is still in the future.
		*r1 = ^uintptr(0)
		*errno = state.timeoutErrno
		status = coroHostOperationResumeSuccessV1
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && !lease.Valid() &&
		(cancel == coro.TaskCancelAbort || cancel == coro.TaskCancelShutdown):
		if !finishCoroHostOperationDeadlineWorkerV1(
			driver, task, state, coro.OperationResultLease{}, true, nil,
		) || !finishCoroHostOperationDeadlineTimerV1(
			driver, task, state, coro.OperationResultLease{}, true,
		) {
			coroHostOperationAbortV1("cannot retire canceled deadline host operation")
			return 0
		}
		if cancel == coro.TaskCancelShutdown {
			status = coroHostOperationResumeShutdownV1
		} else {
			status = coroHostOperationResumeTaskAbortV1
		}
	default:
		coroHostOperationAbortV1("unsupported deadline host operation decision")
		return 0
	}
	if !coroHostOperationAdapterV1State.Retire(state.worker) {
		coroHostOperationAbortV1("cannot retire deadline host operation transport")
		return 0
	}
	if !coroHostOperationControlUnbindV1(state.controlKey, state.controlLane, state.worker) {
		coroHostOperationAbortV1("cannot unbind deadline coroutine host operation control")
		return 0
	}
	*state = CoroHostOperationDeadlineParkV1{}
	return status
}
