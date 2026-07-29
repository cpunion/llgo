//go:build baremetal || (!nogc && (wasm || tinygo.wasm) && llgo_wasm_gc)

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

import "unsafe"

const rootedAllocationMagic uintptr = 0x726f6f74

// rootedAllocation is stored after the caller-visible payload. Keeping the
// allocator's real object base unchanged preserves every frame/task alignment
// and identity guarantee while an interior header remains sufficient for the
// conservative collector to find and mark the whole allocation.
type rootedAllocation struct {
	previous *rootedAllocation
	next     *rootedAllocation
	magic    uintptr
	size     uintptr
}

var rootedAllocations *rootedAllocation

// AllocRooted allocates one conservatively scanned object and keeps it alive
// independently of native-stack or global-variable discovery until
// FreeRooted removes the logical owner. Coroutine frames and scheduler task
// records use this boundary because a suspended stackless frame is itself the
// storage that contains its only live Go roots.
func AllocRooted(size uintptr) unsafe.Pointer {
	headerSize := unsafe.Sizeof(rootedAllocation{})
	offset, ok := rootedAllocationOffset(size)
	if !ok || offset > ^uintptr(0)-headerSize {
		return nil
	}
	raw := Alloc(offset + headerSize)
	if raw == nil {
		return nil
	}
	root := (*rootedAllocation)(unsafe.Add(raw, offset))

	lock(&gcMutex)
	linkRootedAllocation(root, size)
	unlock(&gcMutex)
	return raw
}

// FreeRooted removes one exact logical root. Physical reclamation remains the
// collector's responsibility on a later sweep, matching tinygogc's existing
// non-immediate free contract.
func FreeRooted(ptr unsafe.Pointer, size uintptr) bool {
	offset, ok := rootedAllocationOffset(size)
	if ptr == nil || !ok {
		return false
	}
	root := (*rootedAllocation)(unsafe.Add(ptr, offset))

	lock(&gcMutex)
	if !validRootedAllocation(root) || root.size != size {
		unlock(&gcMutex)
		return false
	}
	unlinkRootedAllocation(root)
	unlock(&gcMutex)
	return true
}

// AllocOpaqueRooted allocates an explicitly rooted object whose logical
// pointer does not need to equal the collector allocation base. Its prefix
// makes the matching free independent of a caller-retained size. This is the
// implementation boundary for runtime.AllocRoot; compiler-owned coroutine
// frames use AllocRooted instead because LLVM requires exact allocator-base
// identity.
func AllocOpaqueRooted(size uintptr) unsafe.Pointer {
	headerSize := unsafe.Sizeof(rootedAllocation{})
	if size == 0 || size > ^uintptr(0)-headerSize {
		return nil
	}
	raw := Alloc(headerSize + size)
	if raw == nil {
		return nil
	}
	root := (*rootedAllocation)(raw)

	lock(&gcMutex)
	linkRootedAllocation(root, size)
	unlock(&gcMutex)
	return unsafe.Add(raw, headerSize)
}

// FreeOpaqueRooted releases an object returned by AllocOpaqueRooted from the
// explicit root set. The physical allocation is reclaimed by a later sweep.
func FreeOpaqueRooted(ptr unsafe.Pointer) bool {
	if ptr == nil {
		return false
	}
	headerSize := unsafe.Sizeof(rootedAllocation{})
	root := (*rootedAllocation)(unsafe.Add(ptr, -int(headerSize)))

	lock(&gcMutex)
	if !validRootedAllocation(root) {
		unlock(&gcMutex)
		return false
	}
	unlinkRootedAllocation(root)
	unlock(&gcMutex)
	return true
}

func linkRootedAllocation(root *rootedAllocation, size uintptr) {
	root.previous = nil
	root.next = rootedAllocations
	if rootedAllocations != nil {
		rootedAllocations.previous = root
	}
	root.magic = rootedAllocationMagic
	root.size = size
	rootedAllocations = root
}

func unlinkRootedAllocation(root *rootedAllocation) {
	previous, next := root.previous, root.next
	if previous == nil {
		rootedAllocations = next
	} else {
		previous.next = next
	}
	if next != nil {
		next.previous = previous
	}
	root.previous = nil
	root.next = nil
	root.magic = 0
	root.size = 0
}

func rootedAllocationOffset(size uintptr) (uintptr, bool) {
	alignment := unsafe.Alignof(rootedAllocation{})
	if size == 0 || size > ^uintptr(0)-(alignment-1) {
		return 0, false
	}
	return (size + alignment - 1) &^ (alignment - 1), true
}

func validRootedAllocation(root *rootedAllocation) bool {
	if root == nil || root.magic != rootedAllocationMagic || root.size == 0 {
		return false
	}
	if root.previous == nil {
		if rootedAllocations != root {
			return false
		}
	} else if root.previous.next != root {
		return false
	}
	return root.next == nil || root.next.previous == root
}

// markRootedAllocations is called with gcMutex held. It marks the complete
// header+payload allocation, so the ordinary conservative object scan reaches
// every pointer spilled into an LLVM coroutine frame.
func markRootedAllocations() {
	for root := rootedAllocations; root != nil; root = root.next {
		markRoot(uintptr(unsafe.Pointer(&rootedAllocations)), uintptr(unsafe.Pointer(root)))
	}
}
