//go:build llgo && llgo_coro && !baremetal && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux)) || wasm || tinygo.wasm || llgo_coro_host)

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

//go:linkname llgoCoroTimerSleep llgo.coroTimerSleep
func llgoCoroTimerSleep(delay int64)

// These bridges deliberately expose only unsafe.Pointer across the time/runtime
// package boundary. The bodyful wrappers below retain the exact standard-library
// signatures while the bridge declarations match their runtime definitions
// without relying on package-local Timer layouts in linkname identity.
//
//go:linkname llgoCoroTimerNewV1 runtime.llgoCoroTimerNewV1
func llgoCoroTimerNewV1(when, period int64, f func(any, uintptr, int64), arg any, cp unsafe.Pointer) unsafe.Pointer

//go:linkname llgoCoroTimerStopV1 runtime.llgoCoroTimerStopV1
func llgoCoroTimerStopV1(timer unsafe.Pointer) bool

//go:linkname llgoCoroTimerResetV1 runtime.llgoCoroTimerResetV1
func llgoCoroTimerResetV1(timer unsafe.Pointer, when, period int64) bool

func newTimer(when, period int64, f func(any, uintptr, int64), arg any, cp unsafe.Pointer) *Timer {
	return (*Timer)(llgoCoroTimerNewV1(when, period, f, arg, cp))
}

func stopTimer(timer *Timer) bool {
	return llgoCoroTimerStopV1(unsafe.Pointer(timer))
}

func resetTimer(timer *Timer, when, period int64) bool {
	return llgoCoroTimerResetV1(unsafe.Pointer(timer), when, period)
}

// Sleep preserves the standard synchronous Go API while this exact physical
// body is automatically async-tainted by the dedicated timer intrinsic.
// Direct static callers need no duplicate implementation; function-value and
// interface consumers remain subject to the compilation-wide representation
// and demand plan. The compiler owns all fixed timer park storage and lowers
// the call to one stackless park/resume transaction in the current frame.
func Sleep(d Duration) {
	if d <= 0 {
		return
	}
	llgoCoroTimerSleep(int64(d))
}

// AfterFunc keeps the standard synchronous API while the coroutine runtime
// owns callback launch through the callback value retained in arg. Its timer
// manager never invokes the legacy f slot for a func-based timer, so publishing
// the standard-library launcher there would create a dead, over-approximated
// dynamic spawn target. Keep that slot nil and retain only the user callback.
func AfterFunc(d Duration, f func()) *Timer {
	return newTimer(when(d), 0, nil, f, nil)
}
