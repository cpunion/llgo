//go:build llgo && llgo_coro && llgo_coro_native_pipe && !llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

import "unsafe"

// __llgo_coro_os_thread_foreign_call_v1 remains an exact runtime root on a
// native pipe-only target because every worker call has a dynamic
// LockOSThread branch. Such a target has neither the certified 64-bit atomic
// lease nor the timer-backed native fleet needed to lend managed execution to
// a replacement M. Calling C synchronously here would block its sole executor,
// while sending the call to an arbitrary worker would violate LockOSThread.
// Keep the ABI present for ordinary worker programs, but fail closed if that
// unsupported dynamic combination is actually reached.
//
//export __llgo_coro_os_thread_foreign_call_v1
func __llgo_coro_os_thread_foreign_call_v1(
	g unsafe.Pointer,
	function, traceTarget uintptr,
	argc uint32,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
	r1, r2, errno *uintptr,
) uint32 {
	_, _, _, _ = g, function, traceTarget, argc
	_, _, _, _, _, _, _, _, _ = a0, a1, a2, a3, a4, a5, a6, a7, a8
	_, _, _ = r1, r2, errno
	coroRuntimeAbort("locked-thread foreign call requires timer-capable native fleet")
	return coroWorkerResumeShutdownV1
}
