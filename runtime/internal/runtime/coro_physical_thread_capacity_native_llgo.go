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

import "github.com/goplus/llgo/runtime/internal/coro"

var coroNativePhysicalThreadCapacityV1State coro.PhysicalThreadCapacity

func coroTargetStartPhysicalThreadCapacityV1() bool {
	return coroNativePhysicalThreadCapacityV1State.Start(
		1,
		coro.PhysicalThreadDefaultLimit,
	)
}

func coroTargetReservePhysicalThreadV1() bool {
	accepted, ok := coroNativePhysicalThreadCapacityV1State.Reserve()
	if !ok || !accepted {
		coroRuntimeAbort("program exceeds coroutine physical thread limit")
		return false
	}
	return true
}

func coroTargetReleasePhysicalThreadV1() bool {
	return coroNativePhysicalThreadCapacityV1State.Release()
}

// CoroSetMaxThreads implements runtime/debug.SetMaxThreads over the exact
// process-wide count of runtime-created native threads. Like Go, lowering the
// limit below the current count is immediately process-fatal.
func CoroSetMaxThreads(n int) int {
	next := uint32(0)
	if n > int(coro.PhysicalThreadMaximumLimit) {
		next = coro.PhysicalThreadMaximumLimit
	} else if n > 0 {
		next = uint32(n)
	}
	previous, _, within, ok :=
		coroNativePhysicalThreadCapacityV1State.SetLimit(next)
	if !ok || !within {
		coroRuntimeAbort("program exceeds coroutine physical thread limit")
		return int(previous)
	}
	return int(previous)
}
