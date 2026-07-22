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

// llgoCoroSemaphoreWaitTokenV1 is retained only by the current stackless
// coroutine frame and the scheduler's common wait table.
type llgoCoroSemaphoreWaitTokenV1 struct {
	word uint32
}

//llgo:coro noblock
//go:linkname llgoCoroSemaphorePrepareOrAbortV1 C.__llgo_coro_sema_prepare_or_abort_v1
func llgoCoroSemaphorePrepareOrAbortV1(token, addr unsafe.Pointer, ticket, slot, generation *uint32)

//llgo:coro noblock
//go:linkname llgoCoroSemaphoreRetireCompletedOrAbortV1 C.__llgo_coro_sema_retire_completed_or_abort_v1
func llgoCoroSemaphoreRetireCompletedOrAbortV1(token unsafe.Pointer, ticket, slot, generation uint32)

//llgo:coro noblock
//go:linkname llgoCoroSemaphoreReleaseOrAbortV1 C.__llgo_coro_sema_release_or_abort_v1
func llgoCoroSemaphoreReleaseOrAbortV1(addr unsafe.Pointer)

//go:linkname llgoCoroSemaphoreParkV1 llgo.coroPark
func llgoCoroSemaphoreParkV1(token *llgoCoroSemaphoreWaitTokenV1, ticket uint32)

// semaAcquire keeps the standard synchronous Go contract. A failed fast-path
// CAS publishes the address-keyed wait and parks the stackless current frame;
// after wake it retires that exact generation and retries the CAS because a
// different runnable goroutine is allowed to consume the released token.
func semaAcquire(addr *uint32) {
	for {
		value := latomic.LoadUint32(addr)
		if value != 0 && latomic.CompareAndSwapUint32(addr, value, value-1) {
			return
		}

		var token llgoCoroSemaphoreWaitTokenV1
		var ticket, slot, generation uint32
		llgoCoroSemaphorePrepareOrAbortV1(
			unsafe.Pointer(&token),
			unsafe.Pointer(addr),
			&ticket,
			&slot,
			&generation,
		)
		llgoCoroSemaphoreParkV1(&token, ticket)
		llgoCoroSemaphoreRetireCompletedOrAbortV1(
			unsafe.Pointer(&token),
			ticket,
			slot,
			generation,
		)
	}
}

func semaRelease(addr *uint32) {
	latomic.AddUint32(addr, 1)
	llgoCoroSemaphoreReleaseOrAbortV1(unsafe.Pointer(addr))
}
