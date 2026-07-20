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
	"sync/atomic"
	"unsafe"
)

// internal/runtime/atomic deliberately declares these two pointer operations
// without bodies: the Go runtime owns the write-barrier-aware definitions. The
// llgo runtime uses a conservative, non-moving collector for the current coro
// profile, so the exact sync/atomic operations are the required implementation
// and no separate moving-GC write barrier is needed.
//
// Keeping these as ordinary bodyful Go linkname definitions is important for
// coroutine planning. The emission universe can pair each bodyless stdlib hook
// with this exact source implementation and route managed callers to the sole
// planned plain/coroutine primary instead of treating the declaration as an
// open dynamic ABI boundary.

//go:linkname atomic_storePointer internal/runtime/atomic.storePointer
func atomic_storePointer(ptr *unsafe.Pointer, new unsafe.Pointer) {
	atomic.StorePointer(ptr, new)
}

//go:linkname atomic_casPointer internal/runtime/atomic.casPointer
func atomic_casPointer(ptr *unsafe.Pointer, old, new unsafe.Pointer) bool {
	return atomic.CompareAndSwapPointer(ptr, old, new)
}
