//go:build llgo && llgo_coro && llgo_coro_native_pipe && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

package runtime

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/coroworker"
)

const coroWorkerParkMagicV1 uint32 = 0x43574b31 // "CWK1"

const (
	coroWorkerResumeSuccessV1 uint32 = iota + 1
	coroWorkerResumeTaskAbortV1
	coroWorkerResumeShutdownV1
)

// CoroWorkerParkV1 is opaque compiler-owned storage in the current LLVM
// coroutine frame. The worker queue never receives this address: it retains
// only the pointer-free OperationID and a by-value scalar job. Consequently
// cancellation may delay frame retirement until a blocking syscall returns,
// but no native worker can dereference a destroyed Go/coroutine frame.
type CoroWorkerParkV1 struct {
	magic     uint32
	wait      coro.WaitSetRecord
	packet    coro.ResumePacket
	ticket    coro.ParkTicket
	operation coro.OperationID
}

func validCoroWorkerParkV1(state *CoroWorkerParkV1) bool {
	return state != nil && state.magic == coroWorkerParkMagicV1 &&
		state.ticket.Valid() && state.operation.Valid() &&
		state.operation.Source() == coro.OperationSourceWorker
}

func validCoroWorkerResultWordsV1(state unsafe.Pointer, r1, r2, errno *uintptr) bool {
	return state != nil && r1 != nil && r2 != nil && errno != nil &&
		r1 != r2 && r1 != errno && r2 != errno
}

// coroWorkerAbortV1 routes terminal output through coroRuntimeAbort's private
// synchronous terminal boundary. Every call site has a distinct stable
// message, so a second phase byte would add no diagnostic information.
func coroWorkerAbortV1(message string) {
	coroRuntimeAbort(message)
	for {
	}
}

// __llgo_coro_worker_park_v1 is the owner-P half of ForeignWait. Queue
// capacity is reserved before the irreversible ParkState transaction. Once
// CommitWorkerSubmission succeeds, every path is fail-stop unless the fixed
// pool eventually publishes the exact generation's scalar completion.
//
//export __llgo_coro_worker_park_v1
func __llgo_coro_worker_park_v1(
	g, handle, header, storage unsafe.Pointer,
	function uintptr,
	argc uint32,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
) {
	state := (*CoroWorkerParkV1)(storage)
	task := (*coro.G)(g)
	driver, executor, route, current := coro.CurrentExecutorWorkerDriver(task)
	if g == nil || handle == nil || header == nil || state == nil ||
		*state != (CoroWorkerParkV1{}) || function == 0 || argc > coroworker.MaxArgs ||
		!current || !ensureCoroWorkerOperationCapacityV1(driver, task, coroRuntimeWorkerCapacityV1) {
		coroWorkerAbortV1("invalid coroutine worker park ABI")
		return
	}
	reservation, reserved := coroReserveNativeWorkerSubmissionV1(executor, route)
	if !reserved {
		coroWorkerAbortV1("coroutine worker queue capacity unavailable")
		return
	}

	state.magic = coroWorkerParkMagicV1
	ticket, operation, ok := coro.PrepareCurrentExecutorWorkerPark(
		driver,
		task,
		handle,
		(*coro.HeaderV1)(header),
		&state.wait,
		1,
		1,
	)
	if !ok {
		canceled := coroCancelNativeWorkerSubmissionV1(executor, route, reservation)
		*state = CoroWorkerParkV1{}
		if !canceled {
			coroWorkerAbortV1("coroutine worker park reservation rollback failed")
			return
		}
		coroWorkerAbortV1("cannot prepare coroutine worker park")
		return
	}
	state.ticket = ticket
	state.operation = operation
	if !coro.BindSingleWaitSetResumePacket(&state.wait, &state.packet, operation) {
		coroWorkerAbortV1("cannot bind coroutine worker resume packet")
		return
	}
	args := [coroworker.MaxArgs]uintptr{a0, a1, a2, a3, a4, a5, a6, a7, a8}
	if !coroCommitNativeWorkerSubmissionV1(
		driver, task, executor, route, reservation, operation, function, argc, &args,
	) {
		coroWorkerAbortV1("cannot commit coroutine worker submission")
	}
}

// __llgo_coro_worker_resume_v1 consumes the frame-local result packet. The
// original owner already copied or discarded the scalar payload and strongly
// retired the exact worker generation before publishing this G as runnable.
// Resume therefore remains valid after a P-neutral transfer.
//
//export __llgo_coro_worker_resume_v1
func __llgo_coro_worker_resume_v1(
	g, storage unsafe.Pointer,
	r1, r2, errno *uintptr,
) uint32 {
	state := (*CoroWorkerParkV1)(storage)
	if g == nil || !validCoroWorkerParkV1(state) ||
		!validCoroWorkerResultWordsV1(storage, r1, r2, errno) {
		coroWorkerAbortV1("invalid coroutine worker resume ABI")
		return 0
	}
	*r1, *r2, *errno = 0, 0, 0

	task := (*coro.G)(g)
	var payload coro.ScalarResultPayloadV1
	outcome, caseID, cancel, result, small, ok := coro.TakeResumePacket(
		task,
		state.ticket,
		&state.packet,
		&payload,
	)
	if !ok {
		coroWorkerAbortV1("invalid coroutine worker run decision")
		return 0
	}
	discard := outcome == coro.ParkOutcomeCanceled
	if outcome == coro.ParkOutcomeCompleted {
		if caseID != 1 || cancel != coro.TaskCancelNone ||
			result != coro.ResumeResultScalar || small != coro.ResumeSmallInvalid {
			coroWorkerAbortV1("invalid completed coroutine worker decision")
			return 0
		}
	} else if !discard || caseID != 0 ||
		cancel != coro.TaskCancelAbort && cancel != coro.TaskCancelShutdown ||
		result != coro.ResumeResultNone || small != coro.ResumeSmallInvalid {
		coroWorkerAbortV1("invalid canceled coroutine worker decision")
		return 0
	}
	*state = CoroWorkerParkV1{}
	if discard {
		if cancel == coro.TaskCancelShutdown {
			return coroWorkerResumeShutdownV1
		}
		return coroWorkerResumeTaskAbortV1
	}
	if payload.Kind() != coro.ScalarResultKindWords || payload.Count() != 3 {
		coroWorkerAbortV1("invalid coroutine worker result payload")
		return 0
	}
	values := [3]*uintptr{r1, r2, errno}
	for index, output := range values {
		value, scalarOK := payload.Scalar(uint8(index))
		if !scalarOK || uint64(uintptr(value)) != value {
			coroWorkerAbortV1("coroutine worker result does not fit uintptr")
			return 0
		}
		*output = uintptr(value)
	}
	return coroWorkerResumeSuccessV1
}
