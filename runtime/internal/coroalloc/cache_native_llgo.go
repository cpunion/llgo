//go:build llgo && (darwin || linux) && !baremetal

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

package coroalloc

import "unsafe"

// cacheAllocationSize maps the repeatedly allocated coroutine/task range onto
// compact classes. Compiler frames are commonly 250 bytes to a few KiB; using
// powers of two there can waste almost half of every live goroutine. The hot
// 256..1024 byte range therefore uses 32-byte classes; 1152..4096 uses 128-byte
// classes. Both mappings remain one range check plus an add/mask, and the C
// cache derives the same bin index with arithmetic rather than a search.
// Larger, uncommon ranges retain powers of two and outliers retain the
// backend's exact-size path.
func cacheAllocationSize(size uintptr) uintptr {
	return nativeCacheAllocationSize(size)
}

// The cache leaf owns no Go pointer, callback, scheduler object, or coroutine
// handle. It only transfers a zeroed raw allocation under a short C11 atomic
// critical section, so these are the two exact native boundary facts which
// cannot be inferred from a C signature.

//llgo:coro noblock
//go:linkname cacheTake C.__llgo_coro_alloc_cache_take_v1
func cacheTake(size uintptr) unsafe.Pointer

//llgo:coro noblock
//go:linkname cachePut C.__llgo_coro_alloc_cache_put_v1
func cachePut(ptr unsafe.Pointer, size uintptr) bool
