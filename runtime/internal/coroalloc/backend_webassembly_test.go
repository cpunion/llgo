//go:build !nogc && !baremetal && (wasm || tinygo.wasm) && !llgo_wasm_gc

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
	"testing"
	"unsafe"
)

func TestWasmBackendAllocatesAndFreesWithLibc(t *testing.T) {
	if backendKind != "malloc" {
		t.Fatalf("wasm frame allocator backend = %q, want malloc", backendKind)
	}
	if !Bootstrap() || !Ready() {
		t.Fatal("bootstrap wasm malloc frame allocator")
	}
	const size = uintptr(64)
	ptr := AllocFrame(size)
	if ptr == nil {
		t.Fatal("wasm malloc frame allocation returned nil")
	}
	for offset := uintptr(0); offset < size; offset++ {
		if got := *(*byte)(unsafe.Add(ptr, offset)); got != 0 {
			t.Fatalf("wasm calloc frame byte %d = %d, want zero", offset, got)
		}
	}
	for offset := uintptr(0); offset < size; offset++ {
		*(*byte)(unsafe.Add(ptr, offset)) = byte(offset + 1)
	}
	for offset := uintptr(0); offset < size; offset++ {
		if got, want := *(*byte)(unsafe.Add(ptr, offset)), byte(offset+1); got != want {
			t.Fatalf("wasm malloc frame byte %d = %d, want %d", offset, got, want)
		}
	}
	if !FreeFrame(ptr, size) {
		t.Fatal("wasm free frame rejected allocated range")
	}
}
