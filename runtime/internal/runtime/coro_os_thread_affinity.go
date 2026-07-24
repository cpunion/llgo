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

//export __llgo_coro_os_thread_lock_v1
func __llgo_coro_os_thread_lock_v1(g unsafe.Pointer) {
	if !coro.EnterOSThreadLock((*coro.G)(g)) {
		coroRuntimeAbort("invalid coroutine OS-thread lock")
	}
}

//export __llgo_coro_os_thread_unlock_v1
func __llgo_coro_os_thread_unlock_v1(g unsafe.Pointer) {
	if !coro.ExitOSThreadLock((*coro.G)(g)) {
		coroRuntimeAbort("invalid coroutine OS-thread unlock")
	}
}

//export __llgo_coro_os_thread_locked_v1
func __llgo_coro_os_thread_locked_v1(g unsafe.Pointer) bool {
	return coro.CurrentOSThreadLocked((*coro.G)(g))
}
