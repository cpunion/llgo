//go:build llgo && (darwin || linux) && !baremetal && (amd64 || arm64)

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

// Darwin/Linux amd64 and arm64 are the first targets whose native i64 atomic
// load/store/cmpxchg lowering is certified bounded inside an executor-safe
// span. Adding a target here is a semantic certification, not merely an ABI or
// alignment declaration.
const nonblockingLeaseAtomic64Bounded = true

func preemptLoad64(ptr *uint64) uint64 {
	return atomic.Load(ptr)
}

func preemptStore64(ptr *uint64, value uint64) {
	atomic.Store(ptr, value)
}

func preemptCompareAndSwap64(ptr *uint64, old, new uint64) bool {
	_, swapped := atomic.CompareAndExchange(ptr, old, new)
	return swapped
}
