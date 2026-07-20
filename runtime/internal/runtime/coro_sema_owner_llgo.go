//go:build (llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test) || coro_sema_owner_test

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

func validCoroSemaphoreOutputWordsV1(token unsafe.Pointer, ticket, slot, generation *uint32) bool {
	return token != nil && ticket != nil && slot != nil && generation != nil &&
		unsafe.Pointer(ticket) != token && unsafe.Pointer(slot) != token && unsafe.Pointer(generation) != token &&
		ticket != slot && ticket != generation && slot != generation
}

func coroProgramPrepareSemaphoreV1(
	token *coro.WaitToken,
	key uintptr,
) (coro.WaitTicket, coro.SemaphoreWaitHandle, coro.SemaphoreWaitPrepareResult) {
	if !coroProgramExecutorBoundV1State || token == nil || key == 0 ||
		coroProgramExecutorHandleV1State == (coro.ExecutorHandle{}) {
		return 0, coro.SemaphoreWaitHandle{}, coro.SemaphoreWaitPrepareInvalid
	}
	return coro.PrepareExecutorSemaphoreWait(
		&coroProgramExecutorDriverV1State,
		&coroProgramKeyedWaitCatalogV1State,
		token,
		key,
	)
}

func coroProgramRetireCompletedSemaphoreV1(
	token *coro.WaitToken,
	ticket coro.WaitTicket,
	handle coro.SemaphoreWaitHandle,
) bool {
	return coroProgramExecutorBoundV1State && token != nil &&
		coro.RetireCompletedExecutorSemaphoreWait(
			&coroProgramExecutorDriverV1State,
			&coroProgramKeyedWaitCatalogV1State,
			token,
			ticket,
			handle,
		)
}

func __llgo_coro_sema_prepare_v1(token, addr unsafe.Pointer, ticket, slot, generation *uint32) bool {
	if addr == nil || !validCoroSemaphoreOutputWordsV1(token, ticket, slot, generation) {
		return false
	}
	*ticket, *slot, *generation = 0, 0, 0
	preparedTicket, handle, result := coroProgramPrepareSemaphoreV1(
		(*coro.WaitToken)(token),
		uintptr(addr),
	)
	if result == coro.SemaphoreWaitPreparePoisoned {
		coroRuntimeAbort("coroutine semaphore prepare rollback failed")
		return false
	}
	if result != coro.SemaphoreWaitPrepared {
		return false
	}
	*ticket = uint32(preparedTicket)
	*slot = handle.Slot
	*generation = handle.Generation
	// The acquire fast path may have observed zero and then been preempted
	// before entering this owner call. Publication above comes first; if a
	// release left a token while no waiter existed, post this exact new waiter
	// before returning into the compiler-certified prepare -> park span.
	if catomic.Load((*uint32)(addr)) != 0 {
		if coro.PostPreparedExecutorSemaphoreWait(
			&coroProgramExecutorDriverV1State,
			&coroProgramKeyedWaitCatalogV1State,
			handle,
		) != coro.SemaphoreWaitPosted ||
			!coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State) {
			coroRuntimeAbort("coroutine semaphore prepare wake repair failed")
			for {
			}
		}
	}
	return true
}

func __llgo_coro_sema_retire_completed_v1(token unsafe.Pointer, ticket, slot, generation uint32) bool {
	return token != nil && coroProgramRetireCompletedSemaphoreV1(
		(*coro.WaitToken)(token),
		coro.WaitTicket(ticket),
		coro.SemaphoreWaitHandle{Slot: slot, Generation: generation},
	)
}

// __llgo_coro_sema_prepare_or_abort_v1 is the compiler-certified current-frame
// prepare transaction. Normal return proves that both the common wait and its
// address key have been published by the exact running executor owner.

//export __llgo_coro_sema_prepare_or_abort_v1
func __llgo_coro_sema_prepare_or_abort_v1(token, addr unsafe.Pointer, ticket, slot, generation *uint32) {
	if !__llgo_coro_sema_prepare_v1(token, addr, ticket, slot, generation) {
		coroRuntimeAbort("coroutine semaphore prepare failed")
		for {
		}
	}
}

//export __llgo_coro_sema_retire_completed_or_abort_v1
func __llgo_coro_sema_retire_completed_or_abort_v1(token unsafe.Pointer, ticket, slot, generation uint32) {
	if !__llgo_coro_sema_retire_completed_v1(token, ticket, slot, generation) {
		coroRuntimeAbort("coroutine semaphore retirement failed")
		for {
		}
	}
}

// __llgo_coro_sema_release_or_abort_v1 publishes at most one matching waiter.
// The common wait table is the durable event source; the target request only
// supplies the coalesced wake after that source-of-truth publication. The raw,
// non-suspending owner boundary receives a pointer so the managed caller never
// loses pointer provenance; only this owner derives the non-dereferenced table
// key retained by the scheduler.

//export __llgo_coro_sema_release_or_abort_v1
func __llgo_coro_sema_release_or_abort_v1(addr unsafe.Pointer) {
	key := uintptr(addr)
	// Fast releases during package/runtime initialization have no parked
	// semaphore frame to wake and therefore need no bound executor. Once the
	// catalog owns any waiter, release must be serialized by its exact running
	// owner below.
	if key != 0 && coroProgramKeyedWaitCatalogV1State.CanRelease() {
		return
	}
	result := coro.PostExecutorSemaphoreWait(
		&coroProgramExecutorDriverV1State,
		&coroProgramKeyedWaitCatalogV1State,
		key,
	)
	switch result {
	case coro.SemaphoreWaitNoWaiter:
		return
	case coro.SemaphoreWaitPosted:
		if coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State) {
			return
		}
	}
	coroRuntimeAbort("coroutine semaphore release failed")
	for {
	}
}
