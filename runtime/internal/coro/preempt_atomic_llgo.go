//go:build llgo

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

package coro

import "github.com/goplus/llgo/runtime/internal/clite/sync/atomic"

// Keep these operations as target-lowered sequentially consistent uint32
// atomics. A target without native 32-bit atomics (for example RV32IMC without
// the A extension) must provide its platform atomic/IRQ-critical-section
// adapter, typically the __atomic_load_4/__atomic_store_4/
// __atomic_compare_exchange_4 compiler-runtime surface selected by LLVM.
// Deliberately do not fall back to ordinary Go loads/stores: registration Post
// may run in an ISR or another worker, where that fallback would silently lose
// admission, result publication, or wakeups. Until such an adapter is linked,
// failure at link time is the safe capability boundary.

func preemptLoad(ptr *uint32) uint32 {
	return atomic.Load(ptr)
}

func preemptStore(ptr *uint32, value uint32) {
	atomic.Store(ptr, value)
}

func preemptCompareAndSwap(ptr *uint32, old, new uint32) bool {
	_, swapped := atomic.CompareAndExchange(ptr, old, new)
	return swapped
}

// The uint64 lease operations are deliberately target-selected alongside the
// bounded atomic64 capability. Unsupported targets receive non-atomic
// fail-closed stubs, so merely compiling this package cannot introduce a
// hidden __atomic_*_8 lock fallback.

func preemptLoadWord(ptr *uintptr) uintptr {
	return atomic.Load(ptr)
}

func preemptStoreWord(ptr *uintptr, value uintptr) {
	atomic.Store(ptr, value)
}
