//go:build !nogc && (baremetal || ((wasm || tinygo.wasm) && llgo_wasm_gc))

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

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/runtime/tinygogc"
)

const backendKind = "tinygogc"

func backendBootstrap() bool {
	tinygogc.Init()
	return true
}

func backendAllocFrame(size uintptr) unsafe.Pointer {
	return tinygogc.Alloc(size)
}

func backendFreeFrame(ptr unsafe.Pointer) {
	// Frames and task records are unlinked and cleared exactly once by the
	// scheduler. The stop-the-world collector reclaims their physical blocks
	// after the live P/G graph no longer reaches them.
	_ = ptr
}
