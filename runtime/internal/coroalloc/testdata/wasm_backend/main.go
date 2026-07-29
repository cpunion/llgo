//go:build wasm || tinygo.wasm

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

package main

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coroalloc"
)

func main() {
	if !coroalloc.Bootstrap() || !coroalloc.Ready() {
		panic("bootstrap wasm coroutine allocator")
	}

	const size = uintptr(64)
	ptr := coroalloc.AllocFrame(size)
	if ptr == nil {
		panic("allocate wasm coroutine frame")
	}
	for offset := uintptr(0); offset < size; offset++ {
		*(*byte)(unsafe.Add(ptr, offset)) = byte(offset + 1)
	}
	for offset := uintptr(0); offset < size; offset++ {
		if *(*byte)(unsafe.Add(ptr, offset)) != byte(offset+1) {
			panic("corrupt wasm coroutine frame")
		}
	}
	if !coroalloc.FreeFrame(ptr, size) {
		panic("free wasm coroutine frame")
	}
}
