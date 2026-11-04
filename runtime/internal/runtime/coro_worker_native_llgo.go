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
	"github.com/xgo-dev/llgo/runtime/internal/coro"
	"github.com/xgo-dev/llgo/runtime/internal/coroworker"
	threadpkg "github.com/xgo-dev/llgo/runtime/internal/thread"
)

const (
	coroNativeWorkerThreadCountV1 = 4
	// The physical pthread count stays small; this static ring holds logical
	// pending work while those threads block in file and syscall operations.
	// Keep this program/fleet-neutral: the non-timer native profile deliberately
	// does not link the fleet capacity policy.
	coroNativeWorkerQueueSizeV1 = coroworker.QueueCapacity
	coroNativeWorkerCapacityV1  = coroworker.QueueCapacity
)

type coroNativeWorkerDeliveryV1 uint8

const (
	coroNativeWorkerDeliveryUnusedV1 coroNativeWorkerDeliveryV1 = iota
	coroNativeWorkerDeliveryProgramV1
	coroNativeWorkerDeliveryFleetV1
)

// coroNativeWorkerPoolV1 is the native target adapter, not another executor.
// Exact scheduler owners reserve independent lock-free queue cells and submit
// or cancel them without suspension. Up to four fixed-stack workers
// drain the bounded C11 sequence ring, and each job's existing OperationID
// route selects its Worker source on completion. Neither ring cells nor
// workers inspect a G, ParkState, WaitSetRecord, or LLVM coroutine handle. No
// managed producer edge acquires a pthread mutex.
type coroNativeWorkerPoolV1 struct {
	threads  [coroNativeWorkerThreadCountV1]threadpkg.Thread
	delivery coroNativeWorkerDeliveryV1
	handle   coro.ExecutorHandle
	route    coro.RouteID

	growth   coro.WorkerGrowth
	joined   uint32
	started  bool
	stopping bool
}

var coroNativeWorkerPoolV1State coroNativeWorkerPoolV1

func coroNativeWorkerPoolCanReleaseV1() bool {
	return coroNativeWorkerPoolV1State == (coroNativeWorkerPoolV1{}) &&
		coroworker.QueueCanRelease()
}

func coroNativeWorkerPoolJoinCreatedV1(state *coroNativeWorkerPoolV1, created uint32) bool {
	if state == nil || created > coroNativeWorkerThreadCountV1 || state.joined > created {
		return false
	}
	for index := state.joined; index < created; index++ {
		thread := state.threads[index]
		if thread == nil || threadpkg.Join(thread, nil) != 0 ||
			!coroTargetReleasePhysicalThreadV1() {
			return false
		}
		state.threads[index] = nil
		state.joined = index + 1
	}
	return true
}

func coroNativeWorkerPoolResetAfterJoinV1(state *coroNativeWorkerPoolV1) bool {
	if state == nil || !state.stopping ||
		!coroworker.QueueDestroyAfterJoin() || !state.growth.ResetAfterJoin(state.joined) {
		return false
	}
	*state = coroNativeWorkerPoolV1{}
	return true
}

// The creator lease spans only raw scheduler-owner calls. Thread identity is
// stored before PublishNext releases its count to another physical owner.
func coroNativeWorkerPoolCreateNextV1(state *coroNativeWorkerPoolV1, index uint32) bool {
	if state == nil || index >= coroNativeWorkerThreadCountV1 ||
		!coroTargetReservePhysicalThreadV1() {
		return false
	}
	if coroworker.Create(&state.threads[index]) != 0 {
		// pthread_create leaves its result slot undefined on failure.
		state.threads[index] = nil
		if !coroTargetReleasePhysicalThreadV1() {
			coroRuntimeAbort("native coroutine worker create reservation rollback failed")
		}
		return false
	}
	if !state.growth.PublishNext(index + 1) {
		coroRuntimeAbort("native coroutine worker identity publication failed")
		return false
	}
	return true
}

// The caller has sealed creation after every scheduling owner stopped. No
// worker ID or physical-thread reservation can be published past this point.
func coroNativeWorkerPoolStopCreatedV1(state *coroNativeWorkerPoolV1, created uint32) bool {
	if state == nil || state.stopping || !coroworker.QueueStop(created) {
		coroRuntimeAbort("native coroutine worker stop wake failed")
		return false
	}
	state.stopping = true
	joined := coroNativeWorkerPoolJoinCreatedV1(state, created)
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
	if !state.growth.Start(coroNativeWorkerThreadCountV1) {
		coroRuntimeAbort("native coroutine worker factory start failed")
		return false
	}
	if _, _, status := state.growth.TryAcquire(); status != coro.WorkerGrowthAcquired {
		coroRuntimeAbort("native coroutine initial worker lease failed")
		return false
	}
	created := coroNativeWorkerPoolCreateNextV1(state, 0)
	if !state.growth.Release() {
		coroRuntimeAbort("native coroutine initial worker lease release failed")
		return false
	}
	if !created {
		// Startup precedes every user task, so its empty queue is safe to join.
		count, status := state.growth.TrySeal()
		if status != coro.WorkerGrowthAcquired || count != 0 ||
			!coroNativeWorkerPoolStopCreatedV1(state, count) {
			coroRuntimeAbort("native coroutine initial worker rollback failed")
		}
		return false
	}
	return true
}

// This hook runs after each physical RunSlice has returned, outside managed
// producer hooks and certified slice work. A competing raw owner retries Busy:
// returning to sleep instead could miss demand added while a creator releases.
func coroTargetEnsureWorkerCapacityV1() {
	state := &coroNativeWorkerPoolV1State
	if !state.started {
		return
	}
	created, limit, ok := state.growth.Snapshot()
	if !ok || created == 0 {
		coroRuntimeAbort("native coroutine worker factory snapshot failed")
		return
	}
	if created == limit || coroworker.QueueDemand() <= created {
		return
	}
	for {
		var status coro.WorkerGrowthStatus
		created, limit, status = state.growth.TryAcquire()
		if status == coro.WorkerGrowthBusy {
			continue
		}
		if status != coro.WorkerGrowthAcquired {
			coroRuntimeAbort("native coroutine worker growth lease failed")
			return
		}
		break
	}
	for created < limit && coroworker.QueueDemand() > created {
		if !coroNativeWorkerPoolCreateNextV1(state, created) {
			// Submitted syscalls may block indefinitely. Do not join that live
			// pool on creation failure, or report a smaller capacity as success.
			state.growth.Fail()
			coroRuntimeAbort("native coroutine worker demand growth failed")
			return
		}
		created++
	}
	if !state.growth.Release() {
		coroRuntimeAbort("native coroutine worker growth lease release failed")
	}
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
	if !coroProgramWorkerCapabilityV2() {
		return coroNativeWorkerPoolCanReleaseV1()
	}
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
	reservation := coroworker.QueueReserve()
	return reservation, reservation != 0
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
	function, traceTarget uintptr,
	argc uint32,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
) bool {
	if !id.Valid() || id.Source() != coro.OperationSourceWorker ||
		function == 0 || traceTarget == 0 || argc > coroworker.MaxArgs {
		return false
	}
	return id.Route() == route && coroNativeWorkerSubmissionOwnerV1(handle, route) &&
		coroworker.QueueSubmitReserved(
			reservation,
			id.SourceSlot,
			id.Generation,
			function,
			traceTarget,
			argc,
			a0, a1, a2, a3, a4, a5, a6, a7, a8,
		)
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
		state.stopping {
		return false
	}
	for {
		created, status := state.growth.TrySeal()
		if status == coro.WorkerGrowthBusy {
			continue
		}
		if status != coro.WorkerGrowthAcquired || created == 0 || created > coroNativeWorkerThreadCountV1 {
			return false
		}
		return coroNativeWorkerPoolStopCreatedV1(state, created)
	}
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
	if coroNativeWorkerPoolV1State == (coroNativeWorkerPoolV1{}) {
		return coroNativeWorkerPoolCanReleaseV1()
	}
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

// coroPublishNativeWorkerSubmissionV1 closes the no-return handoff from the
// already committed core Worker generation into the pre-reserved native queue.
// A failure to enqueue would leave a retained frame with no future physical
// fact and therefore aborts instead of returning to the caller.
func coroPublishNativeWorkerSubmissionV1(
	handle coro.ExecutorHandle,
	route coro.RouteID,
	reservation coroworker.QueueReservation,
	id coro.OperationID,
	function, traceTarget uintptr,
	argc uint32,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
) bool {
	if function == 0 || traceTarget == 0 ||
		argc > coroworker.MaxArgs || !id.Valid() || id.Source() != coro.OperationSourceWorker || id.Route() != route ||
		!coroNativeWorkerSubmissionOwnerV1(handle, route) {
		return false
	}
	if !coroNativeWorkerPoolSubmitReservedV1(
		handle,
		route,
		reservation,
		id,
		function,
		traceTarget,
		argc,
		a0, a1, a2, a3, a4, a5, a6, a7, a8,
	) {
		coroRuntimeAbort("native coroutine worker committed submission failed")
		for {
		}
	}
	return true
}
