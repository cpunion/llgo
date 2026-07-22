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
)

// __llgo_coro_critical_enter_v1 is the compiler-to-runtime boundary for one
// structurally proved critical-region entry. The compiler supplies the current
// G explicitly; there is no TLS or plain-call fallback for this coroutine-only
// operation.
//
//export __llgo_coro_critical_enter_v1
func __llgo_coro_critical_enter_v1(g unsafe.Pointer) {
	if !coro.EnterCritical((*coro.G)(g)) {
		coroRuntimeAbort("invalid coroutine critical-region entry")
	}
}

// __llgo_coro_critical_exit_v1 closes one nested critical region. Only the
// outermost exit may return true, instructing the compiler to take its existing
// current-frame yield path. Invalid or unbalanced exits are runtime ABI faults.
//
//export __llgo_coro_critical_exit_v1
func __llgo_coro_critical_exit_v1(g unsafe.Pointer) bool {
	mustYield, ok := coro.ExitCritical((*coro.G)(g))
	if !ok {
		coroRuntimeAbort("invalid coroutine critical-region exit")
	}
	return mustYield
}
