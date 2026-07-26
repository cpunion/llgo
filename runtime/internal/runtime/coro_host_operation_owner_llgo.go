//go:build llgo && llgo_coro && !coro_runtime_adapter_test && (wasm || tinygo.wasm || baremetal || llgo_coro_host) && !(llgo_coro_native_pipe && (darwin || linux) && !baremetal)

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

const (
	coroHostOperationResumeSuccessV1 uint32 = iota + 1
	coroHostOperationResumeTaskAbortV1
	coroHostOperationResumeShutdownV1
)

func coroHostOperationAbortV1(message string) {
	coroRuntimeAbort(message)
	for {
	}
}

// __llgo_coro_host_operation_park_v1 is the owner half of one host-provided
// asynchronous operation. MarkSubmitted closes the logical no-return handoff
// before Submit release-publishes copied request words to the embedding.
//
//export __llgo_coro_host_operation_park_v1
func __llgo_coro_host_operation_park_v1(
	g, handle, header, storage unsafe.Pointer,
	opcode, argc uint32,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
) {
	state := (*CoroHostOperationParkV1)(storage)
	task := (*coro.G)(g)
	driver, _, _, current := coro.CurrentExecutorWorkerDriver(task)
	if g == nil || handle == nil || header == nil || state == nil ||
		*state != (CoroHostOperationParkV1{}) || opcode == 0 ||
		argc > coro.HostOperationMaxArgsV1 || !current {
		coroHostOperationAbortV1("invalid coroutine host operation park ABI")
		return
	}
	state.magic = coroHostOperationParkMagicV1
	ticket, operation, prepared := coro.PrepareCurrentExecutorWorkerPark(
		driver,
		task,
		handle,
		(*coro.HeaderV1)(header),
		&state.wait,
		1,
		1,
	)
	if !prepared {
		*state = CoroHostOperationParkV1{}
		coroHostOperationAbortV1("cannot prepare coroutine host operation park")
		return
	}
	state.ticket = ticket
	state.operation = operation
	args := [coro.HostOperationMaxArgsV1]uintptr{a0, a1, a2, a3, a4, a5, a6, a7, a8}
	if !coro.BindWaitSetResumeCleanup(
		&state.wait,
		&state.packet,
		&state.cleanup,
		coro.ResumeCleanupBinding{
			Kind:         coro.ResumeCleanupHostOperation,
			Context:      unsafe.Pointer(state),
			Entries:      unsafe.Pointer(&state.operation),
			Count:        1,
			RuntimeCount: 1,
			Stride:       unsafe.Sizeof(coro.OperationID{}),
		},
	) || !coro.CommitCurrentExecutorWorkerSubmission(driver, task, operation) ||
		!coroHostOperationAdapterV1State.Submit(operation, opcode, args[:argc]) {
		coroHostOperationAbortV1("cannot publish coroutine host operation")
	}
}

func validCoroHostOperationResultWordsV1(storage unsafe.Pointer, r1, r2, errno *uintptr) bool {
	return storage != nil && r1 != nil && r2 != nil && errno != nil &&
		r1 != r2 && r1 != errno && r2 != errno
}

//export __llgo_coro_host_operation_resume_v1
func __llgo_coro_host_operation_resume_v1(
	g, storage unsafe.Pointer,
	r1, r2, errno *uintptr,
) uint32 {
	state := (*CoroHostOperationParkV1)(storage)
	if g == nil || !validMaterializedCoroHostOperationParkV1(state) ||
		!validCoroHostOperationResultWordsV1(storage, r1, r2, errno) {
		coroHostOperationAbortV1("invalid coroutine host operation resume ABI")
		return 0
	}
	*r1, *r2, *errno = 0, 0, 0
	task := (*coro.G)(g)
	var payload coro.ScalarResultPayloadV1
	outcome, caseID, cancel, result, small, taken := coro.TakeResumePacket(
		task,
		state.ticket,
		&state.packet,
		&payload,
	)
	if !taken {
		coroHostOperationAbortV1("invalid coroutine host operation resume packet")
		return 0
	}
	canceled := outcome == coro.ParkOutcomeCanceled
	if !canceled {
		if outcome != coro.ParkOutcomeCompleted || caseID != 1 ||
			cancel != coro.TaskCancelNone || result != coro.ResumeResultScalar ||
			small != coro.ResumeSmallInvalid {
			coroHostOperationAbortV1("invalid completed coroutine host operation")
			return 0
		}
	} else if caseID != 0 || result != coro.ResumeResultNone ||
		small != coro.ResumeSmallInvalid ||
		cancel != coro.TaskCancelAbort && cancel != coro.TaskCancelShutdown {
		coroHostOperationAbortV1("invalid canceled coroutine host operation")
		return 0
	}
	*state = CoroHostOperationParkV1{}
	if canceled {
		if cancel == coro.TaskCancelShutdown {
			return coroHostOperationResumeShutdownV1
		}
		return coroHostOperationResumeTaskAbortV1
	}
	if payload.Kind() != coro.ScalarResultKindWords || payload.Count() != 3 {
		coroHostOperationAbortV1("invalid coroutine host operation result")
		return 0
	}
	outputs := [3]*uintptr{r1, r2, errno}
	for index, output := range outputs {
		value, scalarOK := payload.Scalar(uint8(index))
		if !scalarOK || uint64(uintptr(value)) != value {
			coroHostOperationAbortV1("coroutine host operation result does not fit uintptr")
			return 0
		}
		*output = uintptr(value)
	}
	return coroHostOperationResumeSuccessV1
}

// coroProgramSyncHostOperationCancelsV1 mirrors only exact logical
// cancelRequested facts after an owner source pass. Completion remains the
// acknowledgement barrier and may legitimately win this race.
func coroProgramSyncHostOperationCancelsV1(driver *coro.ExecutorDriver) bool {
	if !coroHostOperationAdapterV1State.Active() {
		return true
	}
	for index := uint32(0); index < coro.HostOperationCapacityV1; index++ {
		id, active := coroHostOperationAdapterV1State.SnapshotActiveID(index)
		if !active {
			continue
		}
		requested, exact := coro.ExecutorWorkerPhysicalCancelRequested(driver, id)
		if !exact || !requested {
			continue
		}
		switch coroHostOperationAdapterV1State.RequestCancel(id) {
		case coro.HostOperationCancelRequestedV1,
			coro.HostOperationCancelAlreadyRequestedV1,
			coro.HostOperationCancelCompletionPendingV1:
		default:
			return false
		}
	}
	return true
}

//export __llgo_coro_host_next_operation_v1
func __llgo_coro_host_next_operation_v1(out *coro.HostOperationActionV1) uint32 {
	if out == nil || !coroHostOperationAdapterV1State.NextAction(out) {
		if out != nil {
			*out = coro.HostOperationActionV1{}
		}
		return uint32(coro.HostOperationActionNoneV1)
	}
	return out.Kind
}

func coroHostOperationResultV1(
	flags, count,
	r1Lo, r1Hi,
	r2Lo, r2Hi,
	errnoLo, errnoHi uint32,
) (coro.ScalarResultPayloadV1, bool) {
	return coro.MakeScalarResultPayloadV1(
		coro.ScalarResultKindWords,
		coro.ScalarResultFlags(flags),
		uint8(count),
		uint64(r1Lo)|uint64(r1Hi)<<32,
		uint64(r2Lo)|uint64(r2Hi)<<32,
		uint64(errnoLo)|uint64(errnoHi)<<32,
	)
}

// __llgo_coro_host_complete_operation_v1 publishes one exact terminal result
// and requests a later scheduler turn. It never recursively enters RunSlice.
//
//export __llgo_coro_host_complete_operation_v1
func __llgo_coro_host_complete_operation_v1(
	sourceSlot, generation, flags, count,
	r1Lo, r1Hi, r2Lo, r2Hi, errnoLo, errnoHi uint32,
) uint32 {
	id := coro.OperationID{SourceSlot: sourceSlot, Generation: generation}
	payload, payloadOK := coroHostOperationResultV1(
		flags, count, r1Lo, r1Hi, r2Lo, r2Hi, errnoLo, errnoHi,
	)
	lease, leased := coroHostOperationAdapterV1State.BeginComplete(id)
	if !payloadOK || count != 3 || !leased {
		return uint32(coro.WorkerOperationPostInvalid)
	}
	executor := coroProgramExecutorHandleV1State
	if !coroHostTargetV1State.EnterProducer(executor) ||
		!coroProgramExecutorBoundV1State ||
		executor == (coro.ExecutorHandle{}) {
		_ = coroHostOperationAdapterV1State.AbortComplete(lease)
		return uint32(coro.WorkerOperationPostClosed)
	}
	post := coroProgramWorkerSourceV1State.Post(id, payload)
	if post != coro.WorkerOperationPosted {
		_ = coroHostTargetV1State.LeaveProducer()
		_ = coroHostOperationAdapterV1State.AbortComplete(lease)
		return uint32(post)
	}
	request := coroProgramExecutorRegistryV1State.Request(executor)
	wakeOK := !coro.ExecutorRequestNeedsDoorbell(request) ||
		coroHostTargetV1State.RequestWake(executor)
	commitOK := coroHostOperationAdapterV1State.CommitComplete(lease)
	leaveOK := coroHostTargetV1State.LeaveProducer()
	if !wakeOK || !commitOK || !leaveOK {
		coroHostOperationAbortV1("cannot complete coroutine host operation")
		return uint32(coro.WorkerOperationPostInvalid)
	}
	return uint32(post)
}
