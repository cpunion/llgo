//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && linux && !baremetal && !coro_runtime_adapter_test

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

package coroclock

import (
	ctime "github.com/goplus/llgo/runtime/internal/clite/time"
)

// Linux assigns 1 to CLOCK_MONOTONIC. The shared clite constant carries
// Darwin's value 6, which Linux interprets as CLOCK_MONOTONIC_COARSE.
const linuxClockMonotonic = ctime.ClockidT(1)

// MonotonicNano returns the native monotonic clock in nanoseconds. false means
// that the OS call failed or returned a value outside the runtime's int64
// deadline domain. It allocates no storage and retains no pointer.
func MonotonicNano() (int64, bool) {
	var value ctime.Timespec
	if ctime.ClockGettime(linuxClockMonotonic, &value) != 0 {
		return 0, false
	}
	return composeMonotonicNano(int64(value.Sec), int64(value.Nsec))
}
