//go:build (llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal) || wasm || tinygo.wasm || baremetal || llgo_coro_host)) || coro_notify_owner_test

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

	catomic "github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
)

//export __llgo_coro_notify_prepare_or_abort_v2
func __llgo_coro_notify_prepare_or_abort_v2(storage, notifyAddr unsafe.Pointer, target uint32) {
	if notifyAddr == nil || !coroPrepareKeyedStateV2(
		(*CoroKeyedParkV2)(storage), coroKeyedParkNotifyV2, uintptr(notifyAddr), target,
	) {
		coroKeyedAbortV2("coroutine notify prepare failed")
	}
}

//export __llgo_coro_notify_one_or_abort_v2
func __llgo_coro_notify_one_or_abort_v2(notifyAddr unsafe.Pointer, waitSnapshot uint32) {
	if notifyAddr == nil {
		coroKeyedAbortV2("coroutine notify-one has nil key")
		return
	}
	notify := (*uint32)(notifyAddr)
	current := catomic.Load(notify)
	if current == waitSnapshot {
		return
	}
	catomic.Store(notify, current+1)
	_, ok := coroKeyedPostOneV2(coroKeyedParkNotifyV2, uintptr(notifyAddr), current, true)
	if !ok {
		coroKeyedAbortV2("coroutine notify-one publication failed")
	}
}

//export __llgo_coro_notify_all_or_abort_v2
func __llgo_coro_notify_all_or_abort_v2(notifyAddr unsafe.Pointer, waitSnapshot uint32) {
	if notifyAddr == nil {
		coroKeyedAbortV2("coroutine notify-all has nil key")
		return
	}
	notify := (*uint32)(notifyAddr)
	current := catomic.Load(notify)
	if current == waitSnapshot {
		return
	}
	catomic.Store(notify, waitSnapshot)
	for ticket := current; ticket != waitSnapshot; ticket++ {
		if _, ok := coroKeyedPostOneV2(
			coroKeyedParkNotifyV2, uintptr(notifyAddr), ticket, true,
		); !ok {
			coroKeyedAbortV2("coroutine notify-all publication failed")
			return
		}
	}
}
