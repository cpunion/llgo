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
	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/pthread"
	psync "github.com/goplus/llgo/runtime/internal/clite/pthread/sync"
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/coroworker"
)

const (
	coroNativeWorkerThreadCountV1 = 4
	coroNativeWorkerQueueSizeV1   = coro.WorkerOperationSourceCapacity
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

// coroNativeWorkerPoolV1 is the native target adapter, not another executor.
// The single scheduler P is its only producer. Four fixed-stack workers drain
// a bounded pointer-free ring and publish results into the one Worker source;
// they never inspect a G, ParkState, WaitSetRecord, or LLVM coroutine handle.
type coroNativeWorkerPoolV1 struct {
	mutex psync.Mutex
	work  psync.Cond

	threads [coroNativeWorkerThreadCountV1]pthread.Thread
	queue   [coroNativeWorkerQueueSizeV1]coroNativeWorkerJobV1
	handle  coro.ExecutorHandle

	head        uint32
	tail        uint32
	count       uint32
	running     uint32
	created     uint32
	started     bool
	stopping    bool
	reservation bool
}

var coroNativeWorkerPoolV1State coroNativeWorkerPoolV1

func coroNativeWorkerPoolCanReleaseV1() bool {
	return coroNativeWorkerPoolV1State == (coroNativeWorkerPoolV1{})
}

func coroNativeWorkerAdvanceQueueIndexV1(index uint32) uint32 {
	index++
	if index == coroNativeWorkerQueueSizeV1 {
		return 0
	}
	return index
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

func coroNativeWorkerPoolResetV1(state *coroNativeWorkerPoolV1) {
	state.work.Destroy()
	state.mutex.Destroy()
	*state = coroNativeWorkerPoolV1{}
}

// coroNativeWorkerPoolStartV1 uses pthread.Create, whose selected runtime
// implementation is GC_pthread_create for collecting builds and pthread_create
// for nogc builds. Threads remain joinable and are strongly joined at target
// close; none is created per G or per operation.
func coroNativeWorkerPoolStartV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeWorkerPoolV1State
	if !coroNativeWorkerPoolCanReleaseV1() || !coroProgramExecutorBoundV1State ||
		handle != coroProgramExecutorHandleV1State || handle.Slot == 0 || handle.Generation == 0 {
		return false
	}
	if state.mutex.Init(nil) != 0 {
		*state = coroNativeWorkerPoolV1{}
		return false
	}
	if state.work.Init(nil) != 0 {
		state.mutex.Destroy()
		*state = coroNativeWorkerPoolV1{}
		return false
	}
	state.handle = handle
	state.started = true
	for index := uint32(0); index < coroNativeWorkerThreadCountV1; index++ {
		if pthread.Create(&state.threads[index], nil, coroNativeWorkerMainV1, nil) != 0 {
			// pthread_create leaves its result slot undefined on failure.
			state.threads[index] = nil
			state.mutex.Lock()
			state.stopping = true
			broadcast := state.work.Broadcast() == 0
			state.mutex.Unlock()
			if !broadcast {
				coroRuntimeAbort("native coroutine worker start broadcast failed")
				return false
			}
			joined := coroNativeWorkerPoolJoinCreatedV1(state)
			if !joined {
				coroRuntimeAbort("native coroutine worker start join failed")
				return false
			}
			coroNativeWorkerPoolResetV1(state)
			return false
		}
		state.created++
	}
	return true
}

// coroNativeWorkerPoolReserveV1 is the nonblocking queue-capacity preflight.
// The single owner may retain at most one reservation while it prepares the
// matching Worker ParkState. Consumers only remove jobs, so this capacity
// cannot disappear before SubmitReserved commits it.
func coroNativeWorkerPoolReserveV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeWorkerPoolV1State
	if !state.started || state.handle != handle || state.mutex.TryLock() != 0 {
		return false
	}
	ok := !state.stopping && !state.reservation && state.count < coroNativeWorkerQueueSizeV1
	if ok {
		state.reservation = true
	}
	state.mutex.Unlock()
	return ok
}

func coroNativeWorkerPoolCancelReservationV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeWorkerPoolV1State
	if !state.started || state.handle != handle {
		return false
	}
	state.mutex.Lock()
	ok := !state.stopping && state.reservation
	if ok {
		state.reservation = false
	}
	state.mutex.Unlock()
	return ok
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
	job := coroNativeWorkerJobV1{id: id, function: function, argc: argc, args: *args}
	if !job.valid() {
		return false
	}
	state := &coroNativeWorkerPoolV1State
	if !state.started || state.handle != handle {
		return false
	}
	state.mutex.Lock()
	if state.stopping || !state.reservation || state.count >= coroNativeWorkerQueueSizeV1 ||
		state.queue[state.tail] != (coroNativeWorkerJobV1{}) {
		state.mutex.Unlock()
		return false
	}
	state.reservation = false
	state.queue[state.tail] = job
	state.tail = coroNativeWorkerAdvanceQueueIndexV1(state.tail)
	state.count++
	signaled := state.work.Signal() == 0
	state.mutex.Unlock()
	return signaled
}

func coroNativeWorkerTakeV1(state *coroNativeWorkerPoolV1) (coroNativeWorkerJobV1, bool) {
	state.mutex.Lock()
	for state.count == 0 && !state.stopping {
		if state.work.Wait(&state.mutex) != 0 {
			state.mutex.Unlock()
			return coroNativeWorkerJobV1{}, false
		}
	}
	if state.count == 0 {
		state.mutex.Unlock()
		return coroNativeWorkerJobV1{}, state.stopping
	}
	job := state.queue[state.head]
	if !job.valid() {
		state.mutex.Unlock()
		return coroNativeWorkerJobV1{}, false
	}
	state.queue[state.head] = coroNativeWorkerJobV1{}
	state.head = coroNativeWorkerAdvanceQueueIndexV1(state.head)
	state.count--
	state.running++
	state.mutex.Unlock()
	return job, true
}

func coroNativeWorkerFinishRunningV1(state *coroNativeWorkerPoolV1) bool {
	state.mutex.Lock()
	if state.running == 0 {
		state.mutex.Unlock()
		return false
	}
	state.running--
	state.mutex.Unlock()
	return true
}

func coroNativeWorkerCompleteV1(handle coro.ExecutorHandle, job coroNativeWorkerJobV1) bool {
	var result coroworker.Result
	if !job.valid() || !coroworker.Call(job.function, job.argc, &job.args, &result) {
		return false
	}
	payload, ok := coro.MakeScalarResultPayloadV1(
		coro.ScalarResultKindWords,
		0,
		3,
		uint64(result.R1),
		uint64(result.R2),
		uint64(result.Errno),
	)
	if !ok || coroProgramWorkerSourceV1State.Post(job.id, payload) != coro.WorkerOperationPosted {
		return false
	}
	// Post is the durable fact. The common ingress/registry/doorbell tail is
	// requested afterwards, and pool Stop joins across this entire window.
	return coroTargetRequestExecutorV1(handle)
}

// coroNativeWorkerMainV1 is an ordinary fixed-stack pthread routine. Its
// foreign call may block, but it is deliberately outside every LLVM coroutine
// and must never be transformed into another scheduler continuation.
func coroNativeWorkerMainV1(c.Pointer) c.Pointer {
	state := &coroNativeWorkerPoolV1State
	for {
		job, ok := coroNativeWorkerTakeV1(state)
		if !ok {
			coroRuntimeAbort("native coroutine worker queue corruption")
			return nil
		}
		if job == (coroNativeWorkerJobV1{}) {
			return nil
		}
		if !coroNativeWorkerCompleteV1(state.handle, job) || !coroNativeWorkerFinishRunningV1(state) {
			coroRuntimeAbort("native coroutine worker completion failed")
			return nil
		}
	}
}

// coroNativeWorkerPoolStopV1 seals submission, wakes all idle workers, drains
// any already committed jobs, and joins every GC-registered/native pthread.
// It returns only when no worker can still touch the source or target ingress.
func coroNativeWorkerPoolStopV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeWorkerPoolV1State
	if !state.started || state.handle != handle || state.created != coroNativeWorkerThreadCountV1 {
		return false
	}
	state.mutex.Lock()
	if state.stopping || state.reservation {
		state.mutex.Unlock()
		return false
	}
	state.stopping = true
	broadcast := state.work.Broadcast() == 0
	state.mutex.Unlock()
	if !broadcast {
		coroRuntimeAbort("native coroutine worker stop broadcast failed")
		return false
	}
	joined := coroNativeWorkerPoolJoinCreatedV1(state)
	if !joined {
		coroRuntimeAbort("native coroutine worker stop join failed")
		return false
	}
	clean := state.count == 0 && state.running == 0 && state.head == state.tail &&
		!state.reservation
	if !clean {
		return false
	}
	coroNativeWorkerPoolResetV1(state)
	return true
}

// coroProgramReserveNativeWorkerSubmissionV1 is the runtime-owner preflight
// used before PrepareSingleWorkerPark changes the current frame's ParkState.
func coroProgramReserveNativeWorkerSubmissionV1() bool {
	return coroProgramExecutorBoundV1State &&
		coroNativeWorkerPoolReserveV1(coroProgramExecutorHandleV1State)
}

func coroProgramCancelNativeWorkerSubmissionV1() bool {
	return coroProgramExecutorBoundV1State &&
		coroNativeWorkerPoolCancelReservationV1(coroProgramExecutorHandleV1State)
}

// coroProgramCommitNativeWorkerSubmissionV1 closes the no-return handoff from
// the core Worker park owner into the pre-reserved native queue. A failure to
// enqueue after MarkSubmitted would leave a retained frame with no future
// physical fact and therefore aborts instead of returning to the caller.
func coroProgramCommitNativeWorkerSubmissionV1(
	g *coroG,
	id coro.OperationID,
	function uintptr,
	argc uint32,
	args *[coroworker.MaxArgs]uintptr,
) bool {
	if !coroProgramExecutorBoundV1State || g == nil || args == nil || function == 0 ||
		argc > coroworker.MaxArgs || !id.Valid() || id.Source() != coro.OperationSourceWorker ||
		!coro.CommitWorkerSubmission(g, &coroProgramWorkerSourceV1State, id) {
		return false
	}
	if !coroNativeWorkerPoolSubmitReservedV1(
		coroProgramExecutorHandleV1State,
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
