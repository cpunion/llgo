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

// llgoCoroNotifyWaitTokenV1 is retained only by the current stackless frame
// and the scheduler's common WaitRegistrationTable.
type llgoCoroNotifyWaitTokenV1 struct {
	word uint32
}

//llgo:coro noblock
//go:linkname llgoCoroNotifyPrepareOrAbortV1 C.__llgo_coro_notify_prepare_or_abort_v1
func llgoCoroNotifyPrepareOrAbortV1(token, notifyAddr unsafe.Pointer, target uint32, ticket, slot, generation *uint32)

//llgo:coro noblock
//go:linkname llgoCoroNotifyRetireCompletedOrAbortV1 C.__llgo_coro_notify_retire_completed_or_abort_v1
func llgoCoroNotifyRetireCompletedOrAbortV1(token unsafe.Pointer, ticket, slot, generation uint32)

//llgo:coro noblock
//go:linkname llgoCoroNotifyOneOrAbortV1 C.__llgo_coro_notify_one_or_abort_v1
func llgoCoroNotifyOneOrAbortV1(notifyAddr unsafe.Pointer, waitSnapshot uint32)

//llgo:coro noblock
//go:linkname llgoCoroNotifyAllOrAbortV1 C.__llgo_coro_notify_all_or_abort_v1
func llgoCoroNotifyAllOrAbortV1(notifyAddr unsafe.Pointer, waitSnapshot uint32)

//go:linkname llgoCoroNotifyParkV1 llgo.coroPark
func llgoCoroNotifyParkV1(token *llgoCoroNotifyWaitTokenV1, ticket uint32)

//go:linkname sync_runtime_notifyListWait sync.runtime_notifyListWait
func sync_runtime_notifyListWait(l *notifyList, target uint32) {
	if l == nil {
		return
	}
	if notifyListTicketLess(target, latomic.LoadUint32(&l.notify)) {
		return
	}

	var token llgoCoroNotifyWaitTokenV1
	var ticket, slot, generation uint32
	llgoCoroNotifyPrepareOrAbortV1(
		unsafe.Pointer(&token),
		unsafe.Pointer(&l.notify),
		target,
		&ticket,
		&slot,
		&generation,
	)
	llgoCoroNotifyParkV1(&token, ticket)
	llgoCoroNotifyRetireCompletedOrAbortV1(unsafe.Pointer(&token), ticket, slot, generation)
}

//go:linkname sync_runtime_notifyListNotifyOne sync.runtime_notifyListNotifyOne
func sync_runtime_notifyListNotifyOne(l *notifyList) {
	llgoCoroNotifyOneOrAbortV1(
		unsafe.Pointer(&l.notify),
		latomic.LoadUint32(&l.wait),
	)
}

//go:linkname sync_runtime_notifyListNotifyAll sync.runtime_notifyListNotifyAll
func sync_runtime_notifyListNotifyAll(l *notifyList) {
	llgoCoroNotifyAllOrAbortV1(
		unsafe.Pointer(&l.notify),
		latomic.LoadUint32(&l.wait),
	)
}
