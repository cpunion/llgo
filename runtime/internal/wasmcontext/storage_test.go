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

import (
	"testing"
	"unsafe"
)

func TestStorageLifecycle(t *testing.T) {
	var allocated []uintptr
	var freed []unsafe.Pointer
	buffers := make([][]byte, 0, 2)
	alloc := func(size uintptr) unsafe.Pointer {
		allocated = append(allocated, size)
		buf := make([]byte, size)
		buffers = append(buffers, buf)
		return unsafe.Pointer(&buf[0])
	}
	free := func(ptr unsafe.Pointer) {
		freed = append(freed, ptr)
	}

	stack, stackSize, asyncify, asyncifySize, ok := allocStorage(defaultStackSize+1, alloc, free)
	if !ok {
		t.Fatal("init failed")
	}
	wantSize := defaultStackSize + stackAlignment
	if len(allocated) != 2 || allocated[0] != wantSize+stackAlignment-1 || allocated[1] != wantSize {
		t.Fatalf("allocated sizes = %v, want [%d %d]", allocated, wantSize+stackAlignment-1, wantSize)
	}
	if stackSize != wantSize || asyncifySize != wantSize {
		t.Fatalf("returned sizes = %d/%d, want %d/%d", stackSize, asyncifySize, wantSize, wantSize)
	}

	freeStorage(stack, asyncify, free)
	if len(freed) != 2 || freed[0] != stack || freed[1] != asyncify {
		t.Fatalf("freed pointers = %v, want [%p %p]", freed, stack, asyncify)
	}
}

func TestStorageInitFailure(t *testing.T) {
	buf := make([]byte, defaultStackSize)
	stack := unsafe.Pointer(&buf[0])
	for _, failAt := range []int{1, 2} {
		allocations := 0
		alloc := func(uintptr) unsafe.Pointer {
			allocations++
			if allocations == failAt {
				return nil
			}
			return stack
		}
		var freed unsafe.Pointer

		stackResult, _, asyncifyResult, _, ok := allocStorage(0, alloc, func(ptr unsafe.Pointer) { freed = ptr })
		if ok {
			t.Fatalf("allocation %d failure succeeded", failAt)
		}
		wantFreed := unsafe.Pointer(nil)
		if failAt == 2 {
			wantFreed = stack
		}
		if freed != wantFreed {
			t.Fatalf("allocation %d freed pointer = %p, want %p", failAt, freed, wantFreed)
		}
		if stackResult != nil || asyncifyResult != nil {
			t.Fatalf("allocation %d failure returned storage", failAt)
		}
	}
}

func TestStorageDefaultSize(t *testing.T) {
	var sizes []uintptr
	buffers := make([][]byte, 0, 2)
	stack, stackSize, asyncify, asyncifySize, ok := allocStorage(0, func(size uintptr) unsafe.Pointer {
		sizes = append(sizes, size)
		buf := make([]byte, size)
		buffers = append(buffers, buf)
		return unsafe.Pointer(&buf[0])
	}, func(unsafe.Pointer) {})
	if !ok {
		t.Fatal("init failed")
	}
	if stackSize != defaultStackSize || asyncifySize != defaultAsyncifyStackSize {
		t.Fatalf("default sizes = %d/%d", stackSize, asyncifySize)
	}
	if len(sizes) != 2 || sizes[0] != defaultStackSize+stackAlignment-1 || sizes[1] != defaultAsyncifyStackSize {
		t.Fatalf("requested sizes = %v", sizes)
	}
	freeStorage(stack, asyncify, func(unsafe.Pointer) {})
}

func TestStorageAlignsAllocatorAddresses(t *testing.T) {
	for offset := uintptr(0); offset < stackAlignment; offset++ {
		var buffers [][]byte
		var allocated, freed []unsafe.Pointer
		var sizes []uintptr
		alloc := func(size uintptr) unsafe.Pointer {
			buf := make([]byte, size+stackAlignment)
			buffers = append(buffers, buf)
			base := unsafe.Pointer(&buf[0])
			ptr := unsafe.Add(base, (offset-uintptr(base))&(stackAlignment-1))
			allocated = append(allocated, ptr)
			sizes = append(sizes, size)
			return ptr
		}
		free := func(ptr unsafe.Pointer) { freed = append(freed, ptr) }
		stack, size, asyncify, _, ok := allocStorage(127, alloc, free)
		if !ok {
			t.Fatalf("offset %d: allocation failed", offset)
		}
		base := uintptr(alignedStackBase(stack))
		if base%stackAlignment != 0 || (base+size)%stackAlignment != 0 {
			t.Fatalf("offset %d: stack range [%x, %x) is misaligned", offset, base, base+size)
		}
		if base < uintptr(stack) || base+size > uintptr(stack)+sizes[0] {
			t.Fatalf("offset %d: aligned stack exceeds its allocation", offset)
		}
		freeStorage(stack, asyncify, free)
		if len(freed) != 2 || freed[0] != allocated[0] || freed[1] != allocated[1] {
			t.Fatalf("offset %d: did not free the original allocation pointers", offset)
		}
	}
}

func TestStorageRejectsSizeOverflow(t *testing.T) {
	for _, size := range []uintptr{^uintptr(0), ^uintptr(0) - stackAlignment + 2} {
		_, _, _, _, ok := allocStorage(size, func(uintptr) unsafe.Pointer {
			t.Fatal("overflow reached allocator")
			return nil
		}, func(unsafe.Pointer) { t.Fatal("overflow reached free") })
		if ok {
			t.Fatalf("size %d accepted", size)
		}
	}
}
