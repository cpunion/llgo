//go:build llgo && llgo_coro && (!llgo_coro_native_pipe || !(darwin || linux) || baremetal || coro_runtime_adapter_test)

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

var coroSinglePhysicalThreadLimitV1 = int(coro.PhysicalThreadDefaultLimit)

// CoroSetMaxThreads preserves the standard query/set shape on host-pull,
// WebAssembly, embedded, and bare-metal profiles. These profiles own one
// physical executor and do not create native worker Ms.
func CoroSetMaxThreads(n int) int {
	previous := coroSinglePhysicalThreadLimitV1
	if n > int(coro.PhysicalThreadMaximumLimit) {
		n = int(coro.PhysicalThreadMaximumLimit)
	}
	if n < 1 {
		coroRuntimeAbort("program exceeds coroutine physical thread limit")
		return previous
	}
	coroSinglePhysicalThreadLimitV1 = n
	return previous
}
