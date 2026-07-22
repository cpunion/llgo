//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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
	"github.com/goplus/llgo/runtime/internal/coroclock"
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
	now, clockOK := coroclock.MonotonicNano()
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
	expected uint32,
	deadline int64,
) {
	state := (*CoroTimerParkV2)(storage)
	if g == nil || handle == nil || header == nil || state == nil || controller == nil ||
		control == nil || expected == 0 || deadline < 0 || *state != (CoroTimerParkV2{}) {
		coroTimerAbortV2("invalid controlled coroutine Timer V2 park ABI")
		return
	}
	task := (*coro.G)(g)
	driver, wantExecutor, wantRoute, ok := coro.CurrentExecutorTimerDriver(task)
	if !ok {
		coroTimerAbortV2("cannot resolve controlled coroutine Timer V2 owner")
		return
	}
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

// __llgo_coro_timer_cancel_controlled_v2 publishes ordinary operation
// cancellation for one exact logical Timer generation. Zero means either the
// legal pre-publication side of Stop/Reset or a stale identity; the manager's
// post-attach atomic recheck closes the former race.
//
//export __llgo_coro_timer_cancel_controlled_v2
func __llgo_coro_timer_cancel_controlled_v2(controller unsafe.Pointer, controlWord uint32) uint32 {
	if !coroProgramExecutorBoundV1State || controller == nil || controlWord == 0 ||
		coroProgramExecutorHandleV1State == (coro.ExecutorHandle{}) {
		return 0
	}
	if coro.CancelExecutorControlledTimerV2(
		&coroProgramExecutorDriverV1State,
		uintptr(controller),
		controlWord,
	) {
		return 1
	}
	return 0
}

func validCoroTimerOutputWordsV1(token unsafe.Pointer, ticket, timerSlot, timerGeneration *uint32) bool {
	return token != nil && ticket != nil && timerSlot != nil && timerGeneration != nil &&
		unsafe.Pointer(ticket) != token && unsafe.Pointer(timerSlot) != token && unsafe.Pointer(timerGeneration) != token &&
		ticket != timerSlot && ticket != timerGeneration && timerSlot != timerGeneration
}

func coroProgramPrepareTimerAfterV1(token *coro.WaitToken, delay int64) (coro.WaitTicket, coro.TimerRegistrationHandle, coro.TimerRegistrationPrepareResult) {
	if !coroProgramExecutorBoundV1State || token == nil ||
		coroProgramExecutorHandleV1State == (coro.ExecutorHandle{}) {
		return 0, coro.TimerRegistrationHandle{}, coro.TimerRegistrationPrepareInvalid
	}
	now, ok := coroclock.MonotonicNano()
	if !ok {
		return 0, coro.TimerRegistrationHandle{}, coro.TimerRegistrationPrepareInvalid
	}
	deadline, ok := corotimer.DeadlineAfter(now, delay)
	if !ok {
		return 0, coro.TimerRegistrationHandle{}, coro.TimerRegistrationPrepareInvalid
	}
	return coro.PrepareExecutorTimerRegistration(
		&coroProgramExecutorDriverV1State,
		token,
		deadline,
	)
}

func coroProgramRetireCompletedTimerV1(token *coro.WaitToken, ticket coro.WaitTicket, timer coro.TimerRegistrationHandle) bool {
	return coroProgramExecutorBoundV1State && token != nil &&
		coro.RetireCompletedExecutorTimer(&coroProgramExecutorDriverV1State, token, ticket, timer)
}

// __llgo_coro_timer_prepare_after_v1 atomically arms one current-frame token
// and one scheduler-owned, absolute monotonic one-shot timer. It returns only
// POD identity words to its synchronous-style caller; no callback, platform
// thread, Go pointer, or LLVM coroutine handle is retained outside the runtime.
// A non-positive delay is immediately due. Positive overflow saturates at the
// maximum representable monotonic deadline.
func __llgo_coro_timer_prepare_after_v1(token unsafe.Pointer, delay int64, ticket, timerSlot, timerGeneration *uint32) bool {
	if !validCoroTimerOutputWordsV1(token, ticket, timerSlot, timerGeneration) {
		return false
	}
	*ticket = 0
	*timerSlot = 0
	*timerGeneration = 0
	preparedTicket, timer, result := coroProgramPrepareTimerAfterV1((*coro.WaitToken)(token), delay)
	if result == coro.TimerRegistrationPreparePoisoned {
		coroRuntimeAbort("coroutine timer prepare rollback failed")
		return false
	}
	if result != coro.TimerRegistrationPrepared {
		return false
	}
	*ticket = uint32(preparedTicket)
	*timerSlot = timer.Slot
	*timerGeneration = timer.Generation
	return true
}

// __llgo_coro_timer_retire_completed_v1 releases the exact delivered timer
// only after coroPark resumed and consumed its completed WaitToken generation.
func __llgo_coro_timer_retire_completed_v1(token unsafe.Pointer, ticket, timerSlot, timerGeneration uint32) bool {
	return token != nil && coroProgramRetireCompletedTimerV1(
		(*coro.WaitToken)(token),
		coro.WaitTicket(ticket),
		coro.TimerRegistrationHandle{Slot: timerSlot, Generation: timerGeneration},
	)
}

// __llgo_coro_timer_prepare_after_or_abort_v1 is the compiler-certified
// current-frame adapter. Returning normally means that the exact token is
// armed and retained by the timer owner and that every output identity word is
// valid. Rejection is terminal, so synchronous-style source can continue
// directly into the matching coroPark without a branch that could expose a
// registered frame to ordinary cancellation.
//
//export __llgo_coro_timer_prepare_after_or_abort_v1
func __llgo_coro_timer_prepare_after_or_abort_v1(token unsafe.Pointer, delay int64, ticket, timerSlot, timerGeneration *uint32) {
	if !__llgo_coro_timer_prepare_after_v1(token, delay, ticket, timerSlot, timerGeneration) {
		coroRuntimeAbort("coroutine timer prepare failed")
		// Keep the owner fail-closed even if a broken platform exit shim
		// unexpectedly returns. A retained-frame caller may never observe a
		// normal return from a rejected prepare transaction.
		for {
		}
	}
}

// __llgo_coro_timer_retire_completed_or_abort_v1 is the compiler-certified
// current-frame retirement adapter. Returning normally proves that the timer
// table no longer retains token; a mismatched or incomplete transaction is a
// terminal runtime ABI failure and may never let the coroutine frame finish.
//
//export __llgo_coro_timer_retire_completed_or_abort_v1
func __llgo_coro_timer_retire_completed_or_abort_v1(token unsafe.Pointer, ticket, timerSlot, timerGeneration uint32) {
	if !__llgo_coro_timer_retire_completed_v1(token, ticket, timerSlot, timerGeneration) {
		coroRuntimeAbort("coroutine timer retirement failed")
		// A failed retire may still leave token owned by the timer table. Never
		// return to code that could complete and destroy its coroutine frame.
		for {
		}
	}
}
