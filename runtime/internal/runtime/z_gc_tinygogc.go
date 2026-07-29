//go:build !nogc && (baremetal || ((wasm || tinygo.wasm) && llgo_wasm_gc))

/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
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

	"github.com/goplus/llgo/runtime/internal/runtime/tinygogc"
)

// AllocU allocates uninitialized memory.
func AllocU(size uintptr) unsafe.Pointer {
	ret := tinygogc.Alloc(size)
	recordMemProfileAlloc(size)
	return ret
}

// AllocZ allocates zero-initialized memory.
func AllocZ(size uintptr) unsafe.Pointer {
	ret := tinygogc.Alloc(size)
	recordMemProfileAlloc(size)
	return ret
}

func AllocRoot(size uintptr) unsafe.Pointer {
	return tinygogc.AllocOpaqueRooted(size)
}

func FreeRoot(ptr unsafe.Pointer) {
	if ptr != nil && !tinygogc.FreeOpaqueRooted(ptr) {
		coroRuntimeAbort("invalid tinygogc root release")
	}
}

// AddCleanupPtr is unavailable until tinygogc owns a finalizer/cleanup queue.
func AddCleanupPtr(ptr unsafe.Pointer, cleanup func()) (cancel func()) {
	_, _ = ptr, cleanup
	return func() {}
}
