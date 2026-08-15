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

// cacheAllocationSize maps the small, repeatedly allocated coroutine/task
// ranges onto exact power-of-two classes. The native cache keeps at most
// 128 KiB per class; larger and unusual ranges retain the backend's exact-size
// path. Keeping this policy in Go lets every backend allocation and release
// receive the same physical size even after a cache miss.
func cacheAllocationSize(size uintptr) uintptr {
	switch {
	case size <= 256:
		return 256
	case size <= 512:
		return 512
	case size <= 1024:
		return 1024
	case size <= 2048:
		return 2048
	case size <= 4096:
		return 4096
	case size <= 8192:
		return 8192
	case size <= 16384:
		return 16384
	case size <= 32768:
		return 32768
	case size <= 65536:
		return 65536
	default:
		return size
	}
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
