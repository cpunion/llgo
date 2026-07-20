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
	"github.com/goplus/llgo/runtime/internal/clite/pthread"
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/coroworker"
)

const (
	coroNativeWorkerThreadCountV1 = 4
	// The physical pthread count stays small; this static ring holds logical
	// pending work while those threads block in file and syscall operations.
	// Production configures the Worker source to the same 16-page capacity.
	coroNativeWorkerPageCountV1 = 16
	coroNativeWorkerQueueSizeV1 = coroworker.QueueCapacity
)

type coroNativeWorkerJobV1 struct {
	id       coro.OperationID
	function uintptr
	argc     uint32
	args     [coroworker.MaxArgs]uintptr
}

func (job coroNativeWorkerJobV1) valid() bool {
	return job.id.Valid() && job.id.Source() == coro.OperationSourceWorker &&
		job.function != 0 && job.argc <= coroworker.MaxArgs
}

func coroNativeWorkerJobFromTransportV1(raw coroworker.Job) (coroNativeWorkerJobV1, bool) {
	job := coroNativeWorkerJobV1{
		id: coro.OperationID{
			SourceSlot: raw.SourceSlot,
			Generation: raw.Generation,
		},
		function: raw.Function,
		argc:     raw.Argc,
		args:     raw.Args,
	}
	return job, job.valid()
}

// coroNativeWorkerPoolV1 is the native target adapter, not another executor.
// The single scheduler P is its only producer. Four fixed-stack workers drain
// a bounded C11 sequence ring and publish results into the one Worker source;
// neither ring cells nor workers inspect a G, ParkState, WaitSetRecord, or LLVM
// coroutine handle. No managed producer edge acquires a pthread mutex.
type coroNativeWorkerPoolV1 struct {
	threads [coroNativeWorkerThreadCountV1]pthread.Thread
	handle  coro.ExecutorHandle

	created  uint32
	started  bool
	stopping bool
}

var coroNativeWorkerPoolV1State coroNativeWorkerPoolV1

func coroNativeWorkerPoolCanReleaseV1() bool {
	return coroNativeWorkerPoolV1State == (coroNativeWorkerPoolV1{}) &&
		coroworker.QueueCanRelease()
}

func coroNativeWorkerPoolJoinCreatedV1(state *coroNativeWorkerPoolV1) bool {
	if state == nil || state.created > coroNativeWorkerThreadCountV1 {
		return false
	}
	ok := true
	for index := uint32(0); index < state.created; index++ {
		thread := state.threads[index]
		if thread == nil || pthread.Join(thread, nil) != 0 {
			ok = false
		}
		state.threads[index] = nil
	}
	state.created = 0
	return ok
}

func coroNativeWorkerPoolResetAfterJoinV1(state *coroNativeWorkerPoolV1) bool {
	if state == nil || state.created != 0 || !state.stopping ||
		!coroworker.QueueDestroyAfterJoin() {
		return false
	}
	*state = coroNativeWorkerPoolV1{}
	return true
}

// coroNativeWorkerPoolStartV1 uses the scheduler-owned coroworker.Create leaf.
// Threads remain joinable and are strongly joined at target close; none is
// created per G or per operation.
func coroNativeWorkerPoolStartV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeWorkerPoolV1State
	if !coroNativeWorkerPoolCanReleaseV1() || !coroProgramExecutorBoundV1State ||
		handle != coroProgramExecutorHandleV1State || handle.Slot == 0 || handle.Generation == 0 {
		return false
	}
	if !coroworker.QueueInit() {
		*state = coroNativeWorkerPoolV1{}
		return false
	}
	state.handle = handle
	state.started = true
	for index := uint32(0); index < coroNativeWorkerThreadCountV1; index++ {
		if coroworker.Create(&state.threads[index]) != 0 {
			// pthread_create leaves its result slot undefined on failure.
			state.threads[index] = nil
			if !coroworker.QueueStop(state.created) {
				coroRuntimeAbort("native coroutine worker start stop failed")
				return false
			}
			state.stopping = true
			joined := coroNativeWorkerPoolJoinCreatedV1(state)
			if !joined {
				coroRuntimeAbort("native coroutine worker start join failed")
				return false
			}
			if !coroNativeWorkerPoolResetAfterJoinV1(state) {
				coroRuntimeAbort("native coroutine worker start destroy after join failed")
				return false
			}
			return false
		}
		state.created++
	}
	return true
}

// coroNativeWorkerPoolReserveV1 is the nonblocking queue-capacity preflight.
// The single owner P reserves one exact sequence cell using lock-free atomics.
// Consumers only release cells, so its capacity cannot disappear before the
// matching SubmitReserved; a full ring is admission backpressure, never mutex
// contention on the managed scheduler thread.
func coroNativeWorkerPoolReserveV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeWorkerPoolV1State
	return state.started && !state.stopping && state.handle == handle &&
		coroworker.QueueReserve()
}

func coroNativeWorkerPoolCancelReservationV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeWorkerPoolV1State
	return state.started && !state.stopping && state.handle == handle &&
		coroworker.QueueCancelReservation()
}

// coroNativeWorkerPoolSubmitReservedV1 is called only after the core owner has
// made the exact source generation submitted. The earlier reservation makes a
// full queue impossible; any rejection after that point is a fatal invariant,
// not ordinary backpressure.
func coroNativeWorkerPoolSubmitReservedV1(
	handle coro.ExecutorHandle,
	id coro.OperationID,
	function uintptr,
	argc uint32,
	args *[coroworker.MaxArgs]uintptr,
) bool {
	if args == nil {
		return false
	}
	job := coroworker.Job{
		SourceSlot: id.SourceSlot,
		Generation: id.Generation,
		Function:   function,
		Argc:       argc,
		Args:       *args,
	}
	if _, valid := coroNativeWorkerJobFromTransportV1(job); !valid {
		return false
	}
	state := &coroNativeWorkerPoolV1State
	return state.started && !state.stopping && state.handle == handle &&
		coroworker.QueueSubmitReserved(&job)
}

// __llgo_coro_native_worker_complete_v1 is the only C-worker-to-Go edge. The
// fixed native routine has already completed the blocking foreign call and
// passes only the operation generation and scalar result words. It remains
// valid while Stop drains committed jobs: handle/source state is retired only
// after every worker has joined.
//
//export __llgo_coro_native_worker_complete_v1
func __llgo_coro_native_worker_complete_v1(
	sourceSlot, generation uint32,
	r1, r2, errno uintptr,
) uint32 {
	state := &coroNativeWorkerPoolV1State
	id := coro.OperationID{SourceSlot: sourceSlot, Generation: generation}
	if !state.started || state.handle.Slot == 0 || state.handle.Generation == 0 ||
		!id.Valid() || id.Source() != coro.OperationSourceWorker {
		return 0
	}
	payload, ok := coro.MakeScalarResultPayloadV1(
		coro.ScalarResultKindWords,
		0,
		3,
		uint64(r1),
		uint64(r2),
		uint64(errno),
	)
	if !ok || coroProgramWorkerSourceV1State.Post(id, payload) != coro.WorkerOperationPosted {
		return 0
	}
	// Post is the durable fact. The common ingress/registry/doorbell tail is
	// requested afterwards, and pool Stop joins across this entire window.
	if !coroTargetRequestExecutorV1(state.handle) {
		return 0
	}
	return 1
}

// coroNativeWorkerPoolStopV1 seals submission, wakes all idle workers, drains
// any already committed jobs, and joins every GC-registered/native pthread.
// It returns only when no worker can still touch the source or target ingress.
func coroNativeWorkerPoolStopV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeWorkerPoolV1State
	if !state.started || state.handle != handle || state.created != coroNativeWorkerThreadCountV1 {
		return false
	}
	if state.stopping {
		return false
	}
	if !coroworker.QueueStop(state.created) {
		coroRuntimeAbort("native coroutine worker stop wake failed")
		return false
	}
	state.stopping = true
	joined := coroNativeWorkerPoolJoinCreatedV1(state)
	if !joined {
		coroRuntimeAbort("native coroutine worker stop join failed")
		return false
	}
	if !coroNativeWorkerPoolResetAfterJoinV1(state) {
		coroRuntimeAbort("native coroutine worker stop destroy after join failed")
		return false
	}
	return true
}

// coroProgramReserveNativeWorkerSubmissionV1 is the runtime-owner preflight
// used before PrepareCurrentExecutorWorkerPark changes the current frame's
// ParkState. The pool transport is process-shared, but today's production
// program adapter binds only one executor. Rejecting any other resolved handle
// is intentional fail-closed behavior until the target installs a route
// registry for completion delivery; it must not fall back to the global source.
func coroProgramReserveNativeWorkerSubmissionV1(handle coro.ExecutorHandle) bool {
	return coroProgramExecutorBoundV1State && handle == coroProgramExecutorHandleV1State &&
		coroNativeWorkerPoolReserveV1(handle)
}

func coroProgramCancelNativeWorkerSubmissionV1(handle coro.ExecutorHandle) bool {
	return coroProgramExecutorBoundV1State && handle == coroProgramExecutorHandleV1State &&
		coroNativeWorkerPoolCancelReservationV1(handle)
}

// coroProgramCommitNativeWorkerSubmissionV1 closes the no-return handoff from
// the core Worker park owner into the pre-reserved native queue. A failure to
// enqueue after MarkSubmitted would leave a retained frame with no future
// physical fact and therefore aborts instead of returning to the caller.
func coroProgramCommitNativeWorkerSubmissionV1(
	driver *coro.ExecutorDriver,
	g *coro.G,
	handle coro.ExecutorHandle,
	id coro.OperationID,
	function uintptr,
	argc uint32,
	args *[coroworker.MaxArgs]uintptr,
) bool {
	if !coroProgramExecutorBoundV1State || handle != coroProgramExecutorHandleV1State ||
		driver == nil || g == nil || args == nil || function == 0 ||
		argc > coroworker.MaxArgs || !id.Valid() || id.Source() != coro.OperationSourceWorker ||
		!coro.CommitCurrentExecutorWorkerSubmission(driver, g, id) {
		return false
	}
	if !coroNativeWorkerPoolSubmitReservedV1(
		handle,
		id,
		function,
		argc,
		args,
	) {
		coroRuntimeAbort("native coroutine worker committed submission failed")
		for {
		}
	}
	return true
}
