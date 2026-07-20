//go:build !nogc && !baremetal

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

import "unsafe"

// coroRuntimeGCMalloc is the runtime-private collectable allocation boundary.
// The sync certificate means only that the physical call returns on the same
// native thread without parking through the LLGo coroutine scheduler or
// retaining the current coroutine frame: its sole input is a scalar size.
// BDWGC may still take internal locks, perform a collection, and pause that
// thread; sync is not a lock-free or bounded-latency claim.
//
// Keep ordinary bdwgc.Malloc uncertified. Runtime allocation is the one
// audited capability allowed to use this distinct physical wrapper.
//
//llgo:coro sync
//go:linkname coroRuntimeGCMalloc C.__llgo_coro_runtime_gc_malloc_v1
func coroRuntimeGCMalloc(size uintptr) unsafe.Pointer
