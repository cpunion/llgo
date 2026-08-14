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
	"github.com/goplus/llgo/runtime/internal/coro"
)

// Active polling is a bounded opportunity, not a new waiting primitive. It
// lets a worker running on another physical CPU finish a cache-hot syscall
// before this executor publishes IdleArmed, avoiding the pipe/poll round trip.
// A worker which can really block misses this window and immediately falls
// back to the retained doorbell protocol. Keep this as an iteration budget so
// the policy does not require a clock read or target timer capability.
const coroNativeWorkerCompletionSpinBudgetV1 uint32 = 32768

func coroNativeTryFastWorkerCompletionV1(driver *coro.ExecutorDriver) (ready, ok bool) {
	probe, awaiting, ready, ok := coro.PrepareExecutorWorkerCompletionProbe(driver)
	if !ok || ready || !awaiting {
		return ready, ok
	}
	if !probe.Valid() {
		return false, false
	}
	for attempt := uint32(0); attempt < coroNativeWorkerCompletionSpinBudgetV1; attempt++ {
		if probe.Ready() {
			return true, true
		}
	}
	return false, true
}
