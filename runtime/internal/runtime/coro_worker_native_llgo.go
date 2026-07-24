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
	coroNativeWorkerPageCountV1 = coroNativeSourcePageCountV1
	coroNativeWorkerQueueSizeV1 = coroworker.QueueCapacity
	coroNativeWorkerCapacityV1  = coroNativeWorkerPageCountV1 * coro.WorkerOperationPageCapacity
)

var coroProgramWorkerExtraPagesV1State [coroNativeWorkerPageCountV1 - 1]coro.WorkerOperationPage

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

type coroNativeWorkerDeliveryV1 uint8

const (
	coroNativeWorkerDeliveryUnusedV1 coroNativeWorkerDeliveryV1 = iota
	coroNativeWorkerDeliveryProgramV1
	coroNativeWorkerDeliveryFleetV1
)

// coroNativeWorkerPoolV1 is the native target adapter, not another executor.
// Exact scheduler owners reserve independent lock-free queue cells and submit
// or cancel them without suspension. Four fixed-stack workers
// drain the bounded C11 sequence ring, and each job's existing OperationID
// route selects its Worker source on completion. Neither ring cells nor
// workers inspect a G, ParkState, WaitSetRecord, or LLVM coroutine handle. No
// managed producer edge acquires a pthread mutex.
type coroNativeWorkerPoolV1 struct {
	threads  [coroNativeWorkerThreadCountV1]pthread.Thread
	delivery coroNativeWorkerDeliveryV1
	handle   coro.ExecutorHandle
	route    coro.RouteID

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

// coroNativeWorkerPoolStartDeliveryV1 uses the scheduler-owned
// coroworker.Create leaf. Threads remain joinable and are strongly joined at
// target close; none is created per G, operation, executor, or route.
func coroNativeWorkerPoolStartDeliveryV1(
	delivery coroNativeWorkerDeliveryV1,
	handle coro.ExecutorHandle,
	route coro.RouteID,
) bool {
	state := &coroNativeWorkerPoolV1State
	if !coroNativeWorkerPoolCanReleaseV1() ||
		!coroNativeWorkerDeliveryReadyV1(delivery, handle, route) {
		return false
	}
	if !coroworker.QueueInit() {
		*state = coroNativeWorkerPoolV1{}
		return false
	}
	state.delivery = delivery
	state.handle = handle
	state.route = route
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

func coroNativeWorkerPoolStartV1(handle coro.ExecutorHandle) bool {
	route, ok := coroProgramWorkerSourceV1State.Route()
	// Worker is a compiler-selected capability. Programs whose source catalog
	// does not bind it must not pay for four idle pthreads, but still require a
	// pristine transport so a stale pool cannot be silently ignored.
	if !ok {
		return coroProgramWorkerSourceV1State.CanRelease() &&
			coroNativeWorkerPoolCanReleaseV1()
	}
	return coroNativeWorkerPoolStartDeliveryV1(coroNativeWorkerDeliveryProgramV1, handle, route)
}

// coroNativeWorkerPoolStartFleetV1 starts the same physical pool for all
// already-bound fleet routes. It deliberately retains no executor handle: the
// exact destination is encoded by every submitted OperationID.
func coroNativeWorkerPoolStartFleetV1() bool {
	return coroNativeWorkerPoolStartDeliveryV1(
		coroNativeWorkerDeliveryFleetV1,
		coro.ExecutorHandle{},
		0,
	)
}

func coroNativeWorkerSubmissionOwnerV1(handle coro.ExecutorHandle, route coro.RouteID) bool {
	state := &coroNativeWorkerPoolV1State
	if !state.started || state.stopping || !route.Valid() {
		return false
	}
	return coroNativeWorkerSubmissionOwnerProfileV1(state, handle, route)
}

// coroNativeWorkerPoolReserveV1 is the nonblocking queue-capacity preflight.
// Multiple exact P owners can own different sequence cells concurrently. Each
// token must be submitted or canceled before leaving the compiler-enforced
// no-suspend hook; consumers cannot revoke an owned cell.
func coroNativeWorkerPoolReserveV1(
	handle coro.ExecutorHandle,
	route coro.RouteID,
) (coroworker.QueueReservation, bool) {
	if !coroNativeWorkerSubmissionOwnerV1(handle, route) {
		return 0, false
	}
	var reservation coroworker.QueueReservation
	reserved := coroworker.QueueReserve(&reservation)
	return reservation, reserved
}

func coroNativeWorkerPoolCancelReservationV1(
	handle coro.ExecutorHandle,
	route coro.RouteID,
	reservation coroworker.QueueReservation,
) bool {
	return coroNativeWorkerSubmissionOwnerV1(handle, route) &&
		coroworker.QueueCancelReservation(reservation)
}

// coroNativeWorkerPoolSubmitReservedV1 is called only after the core owner has
// made the exact source generation submitted. The earlier reservation makes a
// full queue impossible; any rejection after that point is a fatal invariant,
// not ordinary backpressure.
func coroNativeWorkerPoolSubmitReservedV1(
	handle coro.ExecutorHandle,
	route coro.RouteID,
	reservation coroworker.QueueReservation,
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
	return id.Route() == route && coroNativeWorkerSubmissionOwnerV1(handle, route) &&
		coroworker.QueueSubmitReserved(reservation, &job)
}

// coroNativeWorkerPoolStopDeliveryV1 seals submission, wakes all idle workers,
// drains any already committed jobs, and joins every GC-registered/native
// pthread. It returns only when no worker can still touch any source or target
// ingress selected by this pool mode.
func coroNativeWorkerPoolStopDeliveryV1(
	delivery coroNativeWorkerDeliveryV1,
	handle coro.ExecutorHandle,
) bool {
	state := &coroNativeWorkerPoolV1State
	if !state.started || state.delivery != delivery || state.handle != handle ||
		state.created != coroNativeWorkerThreadCountV1 {
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

func coroNativeWorkerPoolStopV1(handle coro.ExecutorHandle) bool {
	if coroNativeWorkerPoolV1State == (coroNativeWorkerPoolV1{}) {
		return coroProgramWorkerSourceV1State.CanRelease() &&
			coroNativeWorkerPoolCanReleaseV1()
	}
	return coroNativeWorkerPoolStopDeliveryV1(coroNativeWorkerDeliveryProgramV1, handle)
}

// coroNativeWorkerPoolStopFleetV1 must run while every fleet route and target
// ingress is still active. The join covers all queued route completions; only
// after it returns may the coordinator begin route close.
func coroNativeWorkerPoolStopFleetV1() bool {
	return coroNativeWorkerPoolStopDeliveryV1(
		coroNativeWorkerDeliveryFleetV1,
		coro.ExecutorHandle{},
	)
}

// coroReserveNativeWorkerSubmissionV1 is the runtime-owner preflight used
// before PrepareCurrentExecutorWorkerPark changes the current frame's
// ParkState. Program and fleet owners use the same bounded physical queue; the
// exact current executor plus route authorizes one lock-free reservation.
func coroReserveNativeWorkerSubmissionV1(
	handle coro.ExecutorHandle,
	route coro.RouteID,
) (coroworker.QueueReservation, bool) {
	return coroNativeWorkerPoolReserveV1(handle, route)
}

func coroCancelNativeWorkerSubmissionV1(
	handle coro.ExecutorHandle,
	route coro.RouteID,
	reservation coroworker.QueueReservation,
) bool {
	return coroNativeWorkerPoolCancelReservationV1(handle, route, reservation)
}

// coroCommitNativeWorkerSubmissionV1 closes the no-return handoff from
// the core Worker park owner into the pre-reserved native queue. A failure to
// enqueue after MarkSubmitted would leave a retained frame with no future
// physical fact and therefore aborts instead of returning to the caller.
func coroCommitNativeWorkerSubmissionV1(
	driver *coro.ExecutorDriver,
	g *coro.G,
	handle coro.ExecutorHandle,
	route coro.RouteID,
	reservation coroworker.QueueReservation,
	id coro.OperationID,
	function uintptr,
	argc uint32,
	args *[coroworker.MaxArgs]uintptr,
) bool {
	if driver == nil || g == nil || args == nil || function == 0 ||
		argc > coroworker.MaxArgs || !id.Valid() || id.Source() != coro.OperationSourceWorker || id.Route() != route ||
		!coroNativeWorkerSubmissionOwnerV1(handle, route) ||
		!coro.CommitCurrentExecutorWorkerSubmission(driver, g, id) {
		return false
	}
	if !coroNativeWorkerPoolSubmitReservedV1(
		handle,
		route,
		reservation,
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
