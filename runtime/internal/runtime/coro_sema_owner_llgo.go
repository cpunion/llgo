//go:build (llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal) || wasm || tinygo.wasm || baremetal || llgo_coro_host)) || coro_sema_owner_test

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

//export __llgo_coro_sema_prepare_or_abort_v2
func __llgo_coro_sema_prepare_or_abort_v2(storage, addr unsafe.Pointer) {
	if addr == nil || !coroPrepareKeyedStateV2(
		(*CoroKeyedParkV2)(storage), coroKeyedParkSemaphoreV2, uintptr(addr), 0,
	) {
		coroKeyedAbortV2("coroutine semaphore prepare failed")
	}
}

// __llgo_coro_sema_release_or_abort_v2 selects the oldest published waiter.
// NoWaiter is ordinary: the semaphore count is the durable repair fact for a
// waiter which has not yet published its route-aware OperationID.
//
//export __llgo_coro_sema_release_or_abort_v2
func __llgo_coro_sema_release_or_abort_v2(addr unsafe.Pointer) {
	if addr == nil {
		coroKeyedAbortV2("coroutine semaphore release has nil key")
		return
	}
	_, ok := coroKeyedPostOneV2(coroKeyedParkSemaphoreV2, uintptr(addr), 0, false)
	if !ok {
		coroKeyedAbortV2("coroutine semaphore release failed")
	}
}
