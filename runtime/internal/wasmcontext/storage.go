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

package wasmcontext

import "unsafe"

const (
	// LLGo uses fixed-size Fiber stacks. Scale the 128 KiB wasm32 budget with
	// the pointer width: memory64 doubles pointer slots and compiler GC roots.
	// With only 128 KiB on memory64, ASN.1 certificate encoding crosses the
	// Fiber boundary (caught by Emscripten's STACK_OVERFLOW_CHECK=2).
	defaultStackSize = uintptr(128<<10) * unsafe.Sizeof(uintptr(0)) / 4
	// A suspension within Go code spills the complete active Wasm call chain,
	// not just the host-call boundary. The checked memory64 recursion test
	// spills 44 bytes per frame even with scalar caller instrumentation: its
	// 4096-frame suspension exceeds 64 KiB while its C stack fits in 256 KiB.
	// Keep independent buffers with the same pointer-width-scaled budget.
	// Both remain fixed-size stacks, not Go-style automatically growing stacks.
	defaultAsyncifyStackSize = defaultStackSize
	stackAlignment           = uintptr(16)
)

func allocStorage(stackSize uintptr, alloc func(uintptr) unsafe.Pointer, free func(unsafe.Pointer)) (stack unsafe.Pointer, normalizedStackSize uintptr, asyncifyStack unsafe.Pointer, asyncifySize uintptr, ok bool) {
	customStackSize := stackSize != 0
	if stackSize == 0 {
		stackSize = defaultStackSize
	}
	stackSize = alignStackSize(stackSize)
	if stackSize == 0 || stackSize > ^uintptr(0)-(stackAlignment-1) {
		return
	}
	asyncifySize = defaultAsyncifyStackSize
	if customStackSize && stackSize > asyncifySize {
		asyncifySize = stackSize
	}

	// wasm32 malloc may provide only 8-byte alignment, but the C ABI requires
	// a 16-byte stack pointer. Retain this original pointer for freeStorage;
	// callers pass alignedStackBase(stack) and the full size to the host.
	stack = alloc(stackSize + stackAlignment - 1)
	if stack == nil {
		return
	}
	asyncifyStack = alloc(asyncifySize)
	if asyncifyStack == nil {
		free(stack)
		stack = nil
		return
	}
	return stack, stackSize, asyncifyStack, asyncifySize, true
}

func freeStorage(stack, asyncifyStack unsafe.Pointer, free func(unsafe.Pointer)) {
	if stack != nil {
		free(stack)
	}
	if asyncifyStack != nil {
		free(asyncifyStack)
	}
}

func alignStackSize(size uintptr) uintptr {
	return (size + stackAlignment - 1) &^ (stackAlignment - 1)
}

func alignedStackBase(ptr unsafe.Pointer) unsafe.Pointer {
	return unsafe.Add(ptr, -uintptr(ptr)&(stackAlignment-1))
}
