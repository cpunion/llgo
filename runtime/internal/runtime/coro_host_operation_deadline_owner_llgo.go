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
	state.operations[0] = worker
	state.operations[1] = timerOperation
	state.timer = timer
	state.executor = executor
	state.timeoutErrno = timeoutErrno
	state.controlKey = controlKey
	state.controlLane = lane
	if !coroHostOperationControlBindV1(controlKey, lane, worker) {
		coroHostOperationAbortV1("cannot bind deadline coroutine host operation control")
		return
	}
	args := [coro.HostOperationMaxArgsV1]uintptr{a0, a1, a2, a3, a4, a5, a6, a7, a8}
	if !coro.BindWaitSetResumeCleanup(
		&state.wait,
		&state.packet,
		&state.cleanup,
		coro.ResumeCleanupBinding{
			Kind:         coro.ResumeCleanupHostOperationDeadline,
			Context:      unsafe.Pointer(state),
			Entries:      unsafe.Pointer(&state.operations[0]),
			Count:        uint32(len(state.operations)),
			RuntimeCount: 1,
			Stride:       unsafe.Sizeof(coro.OperationID{}),
		},
	) || !coro.CommitCurrentExecutorWorkerSubmission(driver, task, worker) ||
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

//export __llgo_coro_host_operation_deadline_resume_v1
func __llgo_coro_host_operation_deadline_resume_v1(
	g, storage unsafe.Pointer,
	r1, r2, errno *uintptr,
) uint32 {
	state := (*CoroHostOperationDeadlineParkV1)(storage)
	if g == nil || !validMaterializedCoroHostOperationDeadlineParkV1(state) ||
		!validCoroHostOperationResultWordsV1(storage, r1, r2, errno) {
		coroHostOperationAbortV1("invalid deadline coroutine host operation resume ABI")
		return 0
	}
	*r1, *r2, *errno = 0, 0, 0
	task := (*coro.G)(g)
	var payload coro.ScalarResultPayloadV1
	var payloadOut *coro.ScalarResultPayloadV1
	if state.resumeCase == coroHostOperationDeadlineWorkerCaseV1 {
		payloadOut = &payload
	}
	outcome, caseID, cancel, result, small, taken := coro.TakeResumePacket(
		task,
		state.ticket,
		&state.packet,
		payloadOut,
	)
	if !taken {
		coroHostOperationAbortV1("invalid deadline coroutine host operation resume packet")
		return 0
	}

	status := uint32(0)
	switch {
	case outcome == coro.ParkOutcomeCompleted &&
		caseID == coroHostOperationDeadlineWorkerCaseV1 &&
		state.resumeCase == caseID && cancel == coro.TaskCancelNone &&
		result == coro.ResumeResultScalar && small == coro.ResumeSmallInvalid:
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
		state.resumeCase == caseID && cancel == coro.TaskCancelNone &&
		result == coro.ResumeResultNone && small == coro.ResumeSmallInvalid:
		*r1 = ^uintptr(0)
		*errno = state.timeoutErrno
		status = coroHostOperationResumeSuccessV1
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 &&
		result == coro.ResumeResultNone && small == coro.ResumeSmallInvalid &&
		cancel == coro.TaskCancelNone:
		// The synchronous internal/poll wrapper rechecks closing/deadline state
		// before resubmitting. Reuse its timeout errno as an internal retry fact;
		// it is never exposed while the latest deadline is still in the future.
		*r1 = ^uintptr(0)
		*errno = state.timeoutErrno
		status = coroHostOperationResumeSuccessV1
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 &&
		result == coro.ResumeResultNone && small == coro.ResumeSmallInvalid &&
		(cancel == coro.TaskCancelAbort || cancel == coro.TaskCancelShutdown):
		if cancel == coro.TaskCancelShutdown {
			status = coroHostOperationResumeShutdownV1
		} else {
			status = coroHostOperationResumeTaskAbortV1
		}
	default:
		coroHostOperationAbortV1("unsupported deadline host operation decision")
		return 0
	}
	*state = CoroHostOperationDeadlineParkV1{}
	return status
}
