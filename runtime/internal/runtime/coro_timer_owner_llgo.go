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
)

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
	deadline, ok := coroTimerDeadlineAfterV1(now, delay)
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
//
//export __llgo_coro_timer_prepare_after_v1
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
//
//export __llgo_coro_timer_retire_completed_v1
func __llgo_coro_timer_retire_completed_v1(token unsafe.Pointer, ticket, timerSlot, timerGeneration uint32) bool {
	return token != nil && coroProgramRetireCompletedTimerV1(
		(*coro.WaitToken)(token),
		coro.WaitTicket(ticket),
		coro.TimerRegistrationHandle{Slot: timerSlot, Generation: timerGeneration},
	)
}
