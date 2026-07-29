//go:build !nogc && !baremetal && !wasm && !tinygo.wasm

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

	"github.com/goplus/llgo/runtime/internal/clite/bdwgc"
)

const backendKind = "bdwgc"

func backendBootstrap() bool {
	bdwgc.Init()
	return true
}

func backendAllocFrame(size uintptr) unsafe.Pointer {
	return bdwgc.MallocUncollectable(size)
}

func backendFreeFrame(ptr unsafe.Pointer, size uintptr) bool {
	_ = size
	bdwgc.Free(ptr)
	return true
}
