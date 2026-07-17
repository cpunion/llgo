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

package time

import "unsafe"

// llgoCoroSleepWaitTokenV1 is the time-package side of the private WaitToken
// ABI. The current physical coroutine frame owns this one naturally aligned
// word across its exact park/resume continuation.
type llgoCoroSleepWaitTokenV1 struct {
	word uint32
}

//llgo:coro noblock
//go:linkname llgoCoroSleepPrepareTimerAfterOrAbortV1 C.__llgo_coro_timer_prepare_after_or_abort_v1
func llgoCoroSleepPrepareTimerAfterOrAbortV1(token unsafe.Pointer, delay int64, ticket, timerSlot, timerGeneration *uint32)

//llgo:coro noblock
//go:linkname llgoCoroSleepRetireCompletedTimerOrAbortV1 C.__llgo_coro_timer_retire_completed_or_abort_v1
func llgoCoroSleepRetireCompletedTimerOrAbortV1(token unsafe.Pointer, ticket, timerSlot, timerGeneration uint32)

//go:linkname llgoCoroSleepParkV1 llgo.coroPark
func llgoCoroSleepParkV1(token *llgoCoroSleepWaitTokenV1, ticket uint32)

// Sleep preserves the standard synchronous Go API while this exact physical
// body is automatically async-tainted by the direct park intrinsic. Direct
// static callers need no duplicate implementation; function-value/interface
// consumers remain subject to the compilation-wide representation and demand
// plan. The exact fail-stop owner calls and park remain one
// compiler-certified, no-preempt SSA span so the runtime can retain the
// current coroutine-frame token safely.
func Sleep(d Duration) {
	if d <= 0 {
		return
	}

	var token llgoCoroSleepWaitTokenV1
	var ticket, timerSlot, timerGeneration uint32
	llgoCoroSleepPrepareTimerAfterOrAbortV1(
		unsafe.Pointer(&token),
		int64(d),
		&ticket,
		&timerSlot,
		&timerGeneration,
	)

	llgoCoroSleepParkV1(&token, ticket)
	llgoCoroSleepRetireCompletedTimerOrAbortV1(
		unsafe.Pointer(&token),
		ticket,
		timerSlot,
		timerGeneration,
	)
}
