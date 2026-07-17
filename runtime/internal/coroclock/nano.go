//go:build (darwin || linux) && !baremetal && (!llgo || (llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && !coro_runtime_adapter_test))

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

// Package coroclock provides the allocation-free monotonic clock used by the
// native stackless-coroutine executor. It deliberately exposes neither wall
// time nor a callback/alarm facility.
package coroclock

const (
	monotonicNanosPerSecond int64 = 1_000_000_000
	monotonicMaxInt64             = int64(^uint64(0) >> 1)
)

func composeMonotonicNano(seconds, nanoseconds int64) (int64, bool) {
	if seconds < 0 || nanoseconds < 0 || nanoseconds >= monotonicNanosPerSecond {
		return 0, false
	}
	if seconds > (monotonicMaxInt64-nanoseconds)/monotonicNanosPerSecond {
		return 0, false
	}
	return seconds*monotonicNanosPerSecond + nanoseconds, true
}
