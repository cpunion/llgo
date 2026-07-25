//go:build llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal) || wasm || tinygo.wasm || baremetal || llgo_coro_host)

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

	catomic "github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/corotimer"
)

const coroTimerParkMagicV2 uint32 = 0x43544d32 // "CTM2"

const (
	coroTimerResumeSuccessV2 uint32 = iota + 1
	coroTimerResumeOperationCanceledV2
	coroTimerResumeTaskAbortV2
	coroTimerResumeShutdownV2
)

// CoroTimerParkV2 is opaque compiler-owned storage in the current LLVM
// coroutine frame. It retains one exact logical ParkSet ticket and only
// pointer-free source identities; the timer source itself owns the transient
// WaitSetRecord link. No target wait, callback, or foreign thread receives
// this address.
type CoroTimerParkV2 struct {
	magic     uint32
	wait      coro.WaitSetRecord
	ticket    coro.ParkTicket
	timer     coro.TimerRegistrationHandle
	operation coro.OperationID
	executor  coro.ExecutorHandle
}

func validCoroTimerParkV2(state *CoroTimerParkV2) bool {
	return state != nil && state.magic == coroTimerParkMagicV2 && state.ticket.Valid() &&
		state.timer.Slot != 0 && state.timer.Generation != 0 &&
		state.operation.Valid() && state.operation.Source() == coro.OperationSourceTimer &&
		state.operation.LocalSlot() == state.timer.Slot && state.operation.Generation == state.timer.Generation &&
		state.executor.Slot != 0 && state.executor.Generation != 0
}

func coroTimerAbortV2(message string) {
	coroRuntimeAbort(message)
	for {
	}
}

// __llgo_coro_timer_park_v2 is the source-aware production Sleep owner hook.
// The compiler calls it after publishing SuspendPark/FrameSuspended and then
// immediately executes llvm.coro.suspend. Deadline calculation and every
// fallible source admission step complete before PrepareParkSet publishes the
// scheduler handoff.
//
//export __llgo_coro_timer_park_v2
func __llgo_coro_timer_park_v2(g, handle, header, storage unsafe.Pointer, delay int64) {
	state := (*CoroTimerParkV2)(storage)
	if g == nil || handle == nil || header == nil || state == nil || *state != (CoroTimerParkV2{}) {
		coroTimerAbortV2("invalid coroutine Timer V2 park ABI")
		return
	}
	task := (*coro.G)(g)
	driver, wantExecutor, wantRoute, ok := coro.CurrentExecutorTimerDriver(task)
	now, clockOK := CoroMonotonicNano()
	deadline, deadlineOK := corotimer.DeadlineAfter(now, delay)
	if !ok || !clockOK || !deadlineOK {
		coroTimerAbortV2("cannot resolve coroutine Timer V2 owner or deadline")
		return
	}
	ticket, timer, operation, executor, prepared := coro.PrepareCurrentExecutorTimerPark(
		driver,
		task,
		handle,
		(*coro.HeaderV1)(header),
		&state.wait,
		1,
		1,
		deadline,
	)
	if !prepared || executor != wantExecutor || operation.Route() != wantRoute {
		coroTimerAbortV2("cannot prepare coroutine Timer V2 park")
		return
	}
	state.magic = coroTimerParkMagicV2
	state.ticket = ticket
	state.timer = timer
	state.operation = operation
	state.executor = executor
}

// __llgo_coro_timer_park_controlled_v2 is the standard-library Timer manager
// counterpart. time runtime already supplies an absolute monotonic deadline;
// controller/control/expected identify the exact logical generation that
// Stop or Reset may invalidate while this manager is parked.
//
//export __llgo_coro_timer_park_controlled_v2
func __llgo_coro_timer_park_controlled_v2(
	g, handle, header, storage, controller unsafe.Pointer,
	control *uint32,
	ownerRoute *uint32,
	expected uint32,
	deadline int64,
) {
	state := (*CoroTimerParkV2)(storage)
	if g == nil || handle == nil || header == nil || state == nil || controller == nil ||
		control == nil || ownerRoute == nil || catomic.Load(ownerRoute) != 0 ||
		expected == 0 || deadline < 0 || *state != (CoroTimerParkV2{}) {
		coroTimerAbortV2("invalid controlled coroutine Timer V2 park ABI")
		return
	}
	task := (*coro.G)(g)
	driver, wantExecutor, wantRoute, ok := coro.CurrentExecutorTimerDriver(task)
	if !ok {
		coroTimerAbortV2("cannot resolve controlled coroutine Timer V2 owner")
		return
	}
	// Publish the immutable route before source attachment. A concurrent
	// Stop/Reset either requests this owner or changes control before the
	// post-attach recheck below; there is no route-publication lost-wake gap.
	catomic.Store(ownerRoute, uint32(wantRoute))
	ticket, timer, operation, executor, prepared := coro.PrepareCurrentExecutorControlledTimerPark(
		driver,
		task,
		handle,
		(*coro.HeaderV1)(header),
		&state.wait,
		1,
		1,
		deadline,
		uintptr(controller),
		control,
		expected,
	)
	if !prepared || executor != wantExecutor || operation.Route() != wantRoute {
		coroTimerAbortV2("cannot prepare controlled coroutine Timer V2 park")
		return
	}
	state.magic = coroTimerParkMagicV2
	state.ticket = ticket
	state.timer = timer
	state.operation = operation
	state.executor = executor
}

// __llgo_coro_timer_resume_v2 consumes the exact run decision, releases or
// discards a winner lease, and recycles the timer generation before returning
// control to user/stdlib code. Task abort and command shutdown remain distinct
// so compiler cleanup dispatch can preserve the terminal cause.
//
//export __llgo_coro_timer_resume_v2
func __llgo_coro_timer_resume_v2(g, storage unsafe.Pointer) uint32 {
	state := (*CoroTimerParkV2)(storage)
	if g == nil || !validCoroTimerParkV2(state) {
		coroTimerAbortV2("invalid coroutine Timer V2 resume ABI")
		return 0
	}
	task := (*coro.G)(g)
	outcome, caseID, lease, cancel, taken := coro.TakeRunDecision(task, state.ticket)
	if !taken {
		coroTimerAbortV2("invalid coroutine Timer V2 run decision")
		return 0
	}
	driver, executor, route, ok := coro.CurrentExecutorTimerDriver(task)
	if !ok || executor != state.executor || route != state.operation.Route() {
		coroTimerAbortV2("coroutine Timer V2 resumed on the wrong source")
		return 0
	}
	discard := outcome == coro.ParkOutcomeCanceled
	status := uint32(0)
	switch {
	case outcome == coro.ParkOutcomeCompleted && caseID == 1 && lease.Valid() && cancel == coro.TaskCancelNone:
		status = coroTimerResumeSuccessV2
		discard = false
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelNone:
		status = coroTimerResumeOperationCanceledV2
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelAbort:
		status = coroTimerResumeTaskAbortV2
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelShutdown:
		status = coroTimerResumeShutdownV2
	default:
		coroTimerAbortV2("unsupported coroutine Timer V2 run decision")
		return 0
	}
	if !coro.FinishCurrentExecutorTimerPark(
		driver,
		task,
		state.executor,
		state.timer,
		state.operation,
		lease,
		discard,
	) {
		coroTimerAbortV2("cannot retire coroutine Timer V2 source")
		return 0
	}
	*state = CoroTimerParkV2{}
	return status
}

// __llgo_coro_timer_request_controlled_v2 wakes the exact route after
// Stop/Reset has atomically changed a timer's logical generation. The control
// word is the durable fact; the owner-side timer scan turns its mismatch into
// ordinary operation cancellation.
//
//export __llgo_coro_timer_request_controlled_v2
func __llgo_coro_timer_request_controlled_v2(route uint32) uint32 {
	if !coroTargetRequestControlledTimerV2(coro.RouteID(route)) {
		return 0
	}
	return 1
}
