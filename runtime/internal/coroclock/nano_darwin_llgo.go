//go:build llgo && llgo_coro && llgo_coro_native_pipe && darwin && !baremetal && !coro_runtime_adapter_test

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
	_ "unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
)

// CLOCK_UPTIME_RAW is the nanosecond-resolution monotonic clock used by Go's
// Darwin runtime. It is the clock_gettime_nsec_np view of mach_absolute_time.
const darwinClockUptimeRaw = c.Int(8)

// clock_gettime_nsec_np is one bounded C leaf: it neither waits for an
// external event nor invokes a callback. The certificate is not an
// async-signal-safety claim.
//
//llgo:coro noblock
//go:linkname nativeClockGettimeNsec C.clock_gettime_nsec_np
func nativeClockGettimeNsec(clockID c.Int) uint64

// MonotonicNano returns the native monotonic clock in nanoseconds. false means
// that the value cannot be represented in the runtime's int64 deadline domain.
// It allocates no storage and retains no pointer.
func MonotonicNano() (int64, bool) {
	value := nativeClockGettimeNsec(darwinClockUptimeRaw)
	if value > uint64(monotonicMaxInt64) {
		return 0, false
	}
	return int64(value), true
}
