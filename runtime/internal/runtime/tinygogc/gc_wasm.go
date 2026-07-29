//go:build !nogc && (wasm || tinygo.wasm) && llgo_wasm_gc

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

package tinygogc

import (
	"unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
)

const (
	LLGoFiles    = "_wrap/gc_wasm.c"
	LLGoPackage  = "link: -Wl,--wrap=malloc -Wl,--wrap=free -Wl,--wrap=realloc -Wl,--wrap=calloc"
	wasmPageSize = uintptr(64 << 10)
)

func gcMemoryLayout() (heapStart, heapEnd, globalsStart, globalsEnd, stackTop uintptr) {
	heapStart = alignUp(gcWasmHeapBase(), bytesPerBlock)
	heapEnd = alignDown(gcWasmMemorySize(), bytesPerBlock)
	globalsStart = gcWasmGlobalsStart()
	globalsEnd = gcWasmGlobalsEnd()
	stackTop = gcWasmStackTop()
	return
}

func gcGrowMemory(oldHeapEnd uintptr) uintptr {
	current := gcWasmMemorySize()
	if current < oldHeapEnd {
		return oldHeapEnd
	}
	growth := oldHeapEnd - heapStart
	if growth < 1<<20 {
		growth = 1 << 20
	}
	if current > ^uintptr(0)-growth {
		return oldHeapEnd
	}
	requested := current + growth
	if requested > ^uintptr(0)-(wasmPageSize-1) {
		return oldHeapEnd
	}
	required := alignUp(requested, wasmPageSize)
	if gcWasmGrowMemory(required) == 0 {
		return oldHeapEnd
	}
	return alignDown(gcWasmMemorySize(), bytesPerBlock)
}

//llgo:nounwind
func gcMarkReachable() {
	sp := uintptr(getsp())
	if sp < stackTop {
		markRoots(sp, stackTop)
	}
	if globalsStart < globalsEnd {
		markRoots(globalsStart, globalsEnd)
	}
}

func gcStackStats() (inuse, sys uintptr) {
	sp := uintptr(getsp())
	if sp < stackTop {
		inuse = stackTop - sp
		sys = inuse
	}
	return
}

func gcResumeWorld() {
	// The first WebAssembly collector profile is deliberately single-threaded.
}

func alignUp(value, alignment uintptr) uintptr {
	return (value + alignment - 1) &^ (alignment - 1)
}

func alignDown(value, alignment uintptr) uintptr {
	return value &^ (alignment - 1)
}

//export __wrap_malloc
func __wrap_malloc(size uintptr) unsafe.Pointer {
	return Alloc(size)
}

//export __wrap_free
func __wrap_free(ptr unsafe.Pointer) {
	_ = ptr
}

//export __wrap_calloc
func __wrap_calloc(nmemb, size uintptr) unsafe.Pointer {
	totalSize := nmemb * size
	if nmemb != 0 && totalSize/nmemb != size {
		return nil
	}
	return Alloc(totalSize)
}

//export __wrap_realloc
func __wrap_realloc(ptr unsafe.Pointer, size uintptr) unsafe.Pointer {
	return Realloc(ptr, size)
}

//go:linkname getsp llgo.stackSave
func getsp() unsafe.Pointer

//go:linkname gcWasmGlobalsStart C.llgo_gc_globals_start
func gcWasmGlobalsStart() uintptr

//go:linkname gcWasmGlobalsEnd C.llgo_gc_globals_end
func gcWasmGlobalsEnd() uintptr

//go:linkname gcWasmHeapBase C.llgo_gc_heap_base
func gcWasmHeapBase() uintptr

//go:linkname gcWasmStackTop C.llgo_gc_stack_top
func gcWasmStackTop() uintptr

//go:linkname gcWasmMemorySize C.llgo_gc_memory_size
func gcWasmMemorySize() uintptr

//go:linkname gcWasmGrowMemory C.llgo_gc_grow_memory
func gcWasmGrowMemory(required uintptr) c.Int
