//go:build (llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test) || coro_notify_owner_test

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
	"github.com/goplus/llgo/runtime/internal/coro"
)

func validCoroNotifyOutputWordsV1(token, notifyAddr unsafe.Pointer, ticket, slot, generation *uint32) bool {
	return token != nil && notifyAddr != nil && token != notifyAddr &&
		ticket != nil && slot != nil && generation != nil &&
		unsafe.Pointer(ticket) != token && unsafe.Pointer(slot) != token && unsafe.Pointer(generation) != token &&
		unsafe.Pointer(ticket) != notifyAddr && unsafe.Pointer(slot) != notifyAddr && unsafe.Pointer(generation) != notifyAddr &&
		ticket != slot && ticket != generation && slot != generation
}

func coroProgramPrepareNotifyV1(
	token *coro.WaitToken,
	key uintptr,
	target uint32,
) (coro.WaitTicket, coro.KeyedWaitHandle, coro.KeyedWaitPrepareResult) {
	if !coroProgramExecutorBoundV1State || token == nil || key == 0 ||
		coroProgramExecutorHandleV1State == (coro.ExecutorHandle{}) {
		return 0, coro.KeyedWaitHandle{}, coro.KeyedWaitPrepareInvalid
	}
	return coro.PrepareExecutorNotifyWait(
		&coroProgramExecutorDriverV1State,
		&coroProgramKeyedWaitCatalogV1State,
		token,
		key,
		target,
	)
}

func coroProgramRetireCompletedNotifyV1(
	token *coro.WaitToken,
	ticket coro.WaitTicket,
	handle coro.KeyedWaitHandle,
) bool {
	return coroProgramExecutorBoundV1State && token != nil &&
		coro.RetireCompletedExecutorNotifyWait(
			&coroProgramExecutorDriverV1State,
			&coroProgramKeyedWaitCatalogV1State,
			token,
			ticket,
			handle,
		)
}

func __llgo_coro_notify_prepare_v1(
	token, notifyAddr unsafe.Pointer,
	target uint32,
	ticket, slot, generation *uint32,
) bool {
	if !validCoroNotifyOutputWordsV1(token, notifyAddr, ticket, slot, generation) {
		return false
	}
	*ticket, *slot, *generation = 0, 0, 0
	preparedTicket, handle, result := coroProgramPrepareNotifyV1(
		(*coro.WaitToken)(token), uintptr(notifyAddr), target,
	)
	if result == coro.KeyedWaitPreparePoisoned {
		coroRuntimeAbort("coroutine notify prepare rollback failed")
		return false
	}
	if result != coro.KeyedWaitPrepared {
		return false
	}
	*ticket = uint32(preparedTicket)
	*slot = handle.Slot
	*generation = handle.Generation

	// NotifyOne/All may advance notify after notifyListAdd but before this
	// registration became visible. Publication comes first; the bounded ticket
	// recheck then durable-posts this exact handle before coroPark can run.
	if coro.KeyedWaitTicketLess(target, catomic.Load((*uint32)(notifyAddr))) {
		if coro.PostPreparedExecutorNotifyWait(
			&coroProgramExecutorDriverV1State,
			&coroProgramKeyedWaitCatalogV1State,
			handle,
			uintptr(notifyAddr),
			target,
		) != coro.KeyedWaitPosted || !coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State) {
			coroRuntimeAbort("coroutine notify prepare wake repair failed")
			for {
			}
		}
	}
	return true
}

func __llgo_coro_notify_retire_completed_v1(token unsafe.Pointer, ticket, slot, generation uint32) bool {
	return token != nil && coroProgramRetireCompletedNotifyV1(
		(*coro.WaitToken)(token),
		coro.WaitTicket(ticket),
		coro.KeyedWaitHandle{Slot: slot, Generation: generation},
	)
}

//export __llgo_coro_notify_prepare_or_abort_v1
func __llgo_coro_notify_prepare_or_abort_v1(
	token, notifyAddr unsafe.Pointer,
	target uint32,
	ticket, slot, generation *uint32,
) {
	if !__llgo_coro_notify_prepare_v1(token, notifyAddr, target, ticket, slot, generation) {
		coroRuntimeAbort("coroutine notify prepare failed")
		for {
		}
	}
}

//export __llgo_coro_notify_retire_completed_or_abort_v1
func __llgo_coro_notify_retire_completed_or_abort_v1(token unsafe.Pointer, ticket, slot, generation uint32) {
	if !__llgo_coro_notify_retire_completed_v1(token, ticket, slot, generation) {
		coroRuntimeAbort("coroutine notify retirement failed")
		for {
		}
	}
}

//export __llgo_coro_notify_one_or_abort_v1
func __llgo_coro_notify_one_or_abort_v1(notifyAddr unsafe.Pointer, waitSnapshot uint32) {
	if notifyAddr == nil {
		coroRuntimeAbort("coroutine notify-one invalid owner")
		for {
		}
	}
	current := catomic.Load((*uint32)(notifyAddr))
	if current == waitSnapshot {
		return
	}
	if !coroProgramExecutorBoundV1State {
		coroRuntimeAbort("coroutine notify-one has pending tickets before executor bind")
		for {
		}
	}
	catomic.Store((*uint32)(notifyAddr), current+1)
	result := coro.PostExecutorNotifyWaitOne(
		&coroProgramExecutorDriverV1State,
		&coroProgramKeyedWaitCatalogV1State,
		uintptr(notifyAddr),
		current,
	)
	switch result {
	case coro.KeyedWaitNoWaiter:
		return
	case coro.KeyedWaitPosted:
		if coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State) {
			return
		}
	}
	coroRuntimeAbort("coroutine notify-one publication failed")
	for {
	}
}

//export __llgo_coro_notify_all_or_abort_v1
func __llgo_coro_notify_all_or_abort_v1(notifyAddr unsafe.Pointer, waitSnapshot uint32) {
	if notifyAddr == nil {
		coroRuntimeAbort("coroutine notify-all invalid owner")
		for {
		}
	}
	current := catomic.Load((*uint32)(notifyAddr))
	if current == waitSnapshot {
		return
	}
	if !coroProgramExecutorBoundV1State {
		coroRuntimeAbort("coroutine notify-all has pending tickets before executor bind")
		for {
		}
	}
	catomic.Store((*uint32)(notifyAddr), waitSnapshot)
	posted, ok := coro.PostExecutorNotifyWaitAll(
		&coroProgramExecutorDriverV1State,
		&coroProgramKeyedWaitCatalogV1State,
		uintptr(notifyAddr),
		current,
		waitSnapshot,
	)
	if !ok {
		coroRuntimeAbort("coroutine notify-all publication failed")
		for {
		}
	}
	if posted != 0 && !coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State) {
		coroRuntimeAbort("coroutine notify-all executor request failed")
		for {
		}
	}
}
