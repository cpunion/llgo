//go:build llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal) || wasm || tinygo.wasm || baremetal || llgo_coro_host)

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

// llgoCoroSemaphoreParkV2 is opaque compiler-spilled ParkState/source storage.
// The leading word gives target pointer alignment; the remaining bytes keep
// one fixed 256-byte source-level contract on wasm32 and native64 without
// exposing scheduler pointers to the standard library.
type llgoCoroSemaphoreParkV2 struct {
	_       uintptr
	storage [256 - unsafe.Sizeof(uintptr(0))]byte
}

//llgo:coro contract foreign.v1 scope=declaration progress=executor-safe affinity=caller-thread reentry=none memory=borrow-until-return
//go:linkname llgoCoroSemaphorePrepareOrAbortV2 C.__llgo_coro_sema_prepare_or_abort_v2
func llgoCoroSemaphorePrepareOrAbortV2(state, addr unsafe.Pointer)

//llgo:coro noblock
//go:linkname llgoCoroSemaphoreReleaseOrAbortV2 C.__llgo_coro_sema_release_or_abort_v2
func llgoCoroSemaphoreReleaseOrAbortV2(addr unsafe.Pointer)

//go:linkname llgoCoroSemaphoreSuspendV2 llgo.coroPark
func llgoCoroSemaphoreSuspendV2(state *llgoCoroSemaphoreParkV2, reserved uint32)

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

		var state llgoCoroSemaphoreParkV2
		llgoCoroSemaphorePrepareOrAbortV2(unsafe.Pointer(&state), unsafe.Pointer(addr))
		llgoCoroSemaphoreSuspendV2(&state, 0)
	}
}

func semaRelease(addr *uint32) {
	latomic.AddUint32(addr, 1)
	llgoCoroSemaphoreReleaseOrAbortV2(unsafe.Pointer(addr))
}
