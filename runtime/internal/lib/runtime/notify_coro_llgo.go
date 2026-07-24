//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

	latomic "github.com/goplus/llgo/runtime/internal/lib/sync/atomic"
)

type llgoCoroNotifyParkV2 struct {
	words [16]uintptr
}

//llgo:coro contract foreign.v1 scope=declaration progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
//go:linkname llgoCoroNotifyPrepareOrAbortV2 C.__llgo_coro_notify_prepare_or_abort_v2
func llgoCoroNotifyPrepareOrAbortV2(state, notifyAddr unsafe.Pointer, target uint32)

//llgo:coro noblock
//go:linkname llgoCoroNotifyOneOrAbortV2 C.__llgo_coro_notify_one_or_abort_v2
func llgoCoroNotifyOneOrAbortV2(notifyAddr unsafe.Pointer, waitSnapshot uint32)

//llgo:coro noblock
//go:linkname llgoCoroNotifyAllOrAbortV2 C.__llgo_coro_notify_all_or_abort_v2
func llgoCoroNotifyAllOrAbortV2(notifyAddr unsafe.Pointer, waitSnapshot uint32)

//go:linkname llgoCoroNotifySuspendV2 llgo.coroPark
func llgoCoroNotifySuspendV2(state *llgoCoroNotifyParkV2, reserved uint32)

//llgo:managedlink
//go:linkname sync_runtime_notifyListWait sync.runtime_notifyListWait
func sync_runtime_notifyListWait(l *notifyList, target uint32) {
	if l == nil {
		return
	}
	if notifyListTicketLess(target, latomic.LoadUint32(&l.notify)) {
		return
	}

	var state llgoCoroNotifyParkV2
	llgoCoroNotifyPrepareOrAbortV2(unsafe.Pointer(&state), unsafe.Pointer(&l.notify), target)
	llgoCoroNotifySuspendV2(&state, 0)
}

//llgo:managedlink
//go:linkname sync_runtime_notifyListNotifyOne sync.runtime_notifyListNotifyOne
func sync_runtime_notifyListNotifyOne(l *notifyList) {
	llgoCoroNotifyOneOrAbortV2(
		unsafe.Pointer(&l.notify),
		latomic.LoadUint32(&l.wait),
	)
}

//llgo:managedlink
//go:linkname sync_runtime_notifyListNotifyAll sync.runtime_notifyListNotifyAll
func sync_runtime_notifyListNotifyAll(l *notifyList) {
	llgoCoroNotifyAllOrAbortV2(
		unsafe.Pointer(&l.notify),
		latomic.LoadUint32(&l.wait),
	)
}
