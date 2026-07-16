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

// Package coro implements the target-neutral core of llgo's stackless
// coroutine scheduler. It deliberately depends only on unsafe: allocation and
// LLVM coroutine handle operations belong to the runtime adapter.
package coro

import "unsafe"

// HeaderV1 is the runtime view of cl.coroHeaderType. Keep this layout
// pointer-size neutral: compiler-generated code shares it with native, wasm32,
// embedded, and bare-metal runtimes.
type HeaderV1 struct {
	G              unsafe.Pointer
	Parent         unsafe.Pointer
	Descriptor     unsafe.Pointer
	AllocationBase unsafe.Pointer
	ResultSlot     unsafe.Pointer
	SuspendReason  uint16
	Lifecycle      uint16
	StateID        uint32
	Flags          uint32
}

// SuspendReason describes why a coroutine returned control to its scheduler.
type SuspendReason uint16

const (
	SuspendNone SuspendReason = iota
	SuspendCall
	SuspendFrameComplete
)

// FrameState values deliberately match the lifecycle field emitted by cl.
type FrameState uint16

const (
	FrameAllocated FrameState = iota
	FrameInitialSuspended
	FrameActive
	FrameSuspended
	FrameFinalSuspended
	FrameDestroyPending
	FrameDestroyed
)

const gMagic uint32 = 0x434f524f // "CORO"

type pendingKind uint8

const (
	pendingNone pendingKind = iota
	pendingAwait
	pendingComplete
)

type pendingTransition struct {
	kind   pendingKind
	from   *Frame
	target *Frame
}

// Frame is scheduler-owned metadata. It lives at the beginning of the same
// allocation as the aligned LLVM frame storage. A back-pointer immediately
// before storage makes the free hook independent of maps, TLS, pthreads,
// libuv, and any particular garbage collector.
type Frame struct {
	owner          *G
	handle         unsafe.Pointer
	header         *HeaderV1
	storage        unsafe.Pointer
	rawBase        unsafe.Pointer
	descriptor     unsafe.Pointer
	size           uintptr
	align          uintptr
	allocationSize uintptr
	state          FrameState
	parent         *Frame
	next           *Frame
}

// ValidG reports whether g has been initialized as a coroutine task.
func ValidG(g *G) bool {
	return g != nil && g.magic == gMagic
}

// FrameAllocationSize returns the single-allocation size needed for Frame,
// the storage back-pointer, alignment padding, and the LLVM coroutine frame.
func FrameAllocationSize(size, align uintptr) (uintptr, bool) {
	if align == 0 || align&(align-1) != 0 {
		return 0, false
	}
	overhead := unsafe.Sizeof(Frame{}) + unsafe.Sizeof(uintptr(0))
	max := ^uintptr(0)
	if overhead > max-(align-1) {
		return 0, false
	}
	overhead += align - 1
	if size > max-overhead {
		return 0, false
	}
	return overhead + size, true
}

// AlignedStorage locates LLVM frame storage within a combined allocation.
func AlignedStorage(raw unsafe.Pointer, align uintptr) (unsafe.Pointer, bool) {
	if raw == nil || align == 0 || align&(align-1) != 0 {
		return nil, false
	}
	offset, ok := alignedStorageOffset(uintptr(raw), align)
	if !ok {
		return nil, false
	}
	return unsafe.Add(raw, offset), true
}

func alignedStorageOffset(base, align uintptr) (uintptr, bool) {
	offset := unsafe.Sizeof(Frame{}) + unsafe.Sizeof(uintptr(0))
	if offset > ^uintptr(0)-base {
		return 0, false
	}
	start := base + offset
	padding := -start & (align - 1)
	if padding > ^uintptr(0)-start {
		return 0, false
	}
	return offset + padding, true
}

// Zero clears size bytes beginning at ptr without introducing a libc/runtime
// dependency into the scheduler core.
func Zero(ptr unsafe.Pointer, size uintptr) {
	for offset := uintptr(0); offset < size; offset++ {
		*(*byte)(unsafe.Add(ptr, offset)) = 0
	}
}

// RegisterFrame initializes a combined allocation and links it into g. raw
// must be the base returned by the target runtime allocator, and total must be
// exactly FrameAllocationSize(size, align).
func RegisterFrame(g *G, raw unsafe.Pointer, total, size, align uintptr, descriptor unsafe.Pointer) (unsafe.Pointer, bool) {
	want, ok := FrameAllocationSize(size, align)
	if !ValidG(g) || raw == nil || descriptor == nil || !ok || total != want ||
		uintptr(raw)%unsafe.Alignof(Frame{}) != 0 {
		return nil, false
	}
	storage, ok := AlignedStorage(raw, align)
	if !ok {
		return nil, false
	}
	Zero(raw, total)
	frame := (*Frame)(raw)
	frame.owner = g
	frame.storage = storage
	frame.rawBase = raw
	frame.descriptor = descriptor
	frame.size = size
	frame.align = align
	frame.allocationSize = total
	frame.state = FrameAllocated
	frame.next = g.frames
	g.frames = frame
	back := (**Frame)(unsafe.Add(storage, -int(unsafe.Sizeof(uintptr(0)))))
	*back = frame
	return storage, true
}

// FrameFromStorage obtains scheduler metadata through the back-pointer stored
// immediately before LLVM coroutine frame storage.
func FrameFromStorage(storage unsafe.Pointer) *Frame {
	if storage == nil {
		return nil
	}
	back := (**Frame)(unsafe.Add(storage, -int(unsafe.Sizeof(uintptr(0)))))
	return *back
}

func findFrame(g *G, handle unsafe.Pointer) *Frame {
	if g == nil || handle == nil {
		return nil
	}
	for frame := g.frames; frame != nil; frame = frame.next {
		if frame.handle == handle {
			return frame
		}
	}
	return nil
}

// PublishFrame binds an LLVM handle/header to newly allocated storage after
// the coroutine has reached its initial suspend point.
func PublishFrame(g *G, handle unsafe.Pointer, header *HeaderV1, storage unsafe.Pointer) bool {
	if !ValidG(g) || handle == nil || header == nil || storage == nil {
		return false
	}
	frame := FrameFromStorage(storage)
	if frame == nil || frame.owner != g || frame.storage != storage || frame.state != FrameAllocated ||
		frame.handle != nil || frame.header != nil || header.G != unsafe.Pointer(g) ||
		header.Descriptor != frame.descriptor || header.Lifecycle != uint16(FrameInitialSuspended) ||
		header.SuspendReason != uint16(SuspendNone) {
		return false
	}
	if existing := findFrame(g, handle); existing != nil && existing != frame {
		return false
	}
	frame.handle = handle
	frame.header = header
	frame.state = FrameInitialSuspended
	header.AllocationBase = frame.rawBase
	return true
}

// PrepareAwait records a parent-to-child handoff. It never resumes either
// coroutine; only the runtime driver may perform handle operations requested
// by the scheduler action protocol.
func PrepareAwait(g *G, parentHandle, childHandle unsafe.Pointer) bool {
	if !ValidG(g) || g.pending.kind != pendingNone {
		return false
	}
	parent := findFrame(g, parentHandle)
	child := findFrame(g, childHandle)
	if parent == nil || child == nil || parent == child || g.active != parent ||
		parent.header == nil || child.header == nil || parent.state != FrameActive ||
		child.state != FrameInitialSuspended || parent.header.SuspendReason != uint16(SuspendCall) ||
		parent.header.Lifecycle != uint16(FrameSuspended) || child.header.Parent != parentHandle ||
		child.parent != nil {
		return false
	}
	child.parent = parent
	g.pending = pendingTransition{kind: pendingAwait, from: parent, target: child}
	return true
}

// PrepareComplete records a final-suspended frame. Destruction remains owned
// by the scheduler and occurs only after the resume operation returns.
func PrepareComplete(g *G, handle unsafe.Pointer, header *HeaderV1) bool {
	if !ValidG(g) || handle == nil || header == nil || g.pending.kind != pendingNone {
		return false
	}
	frame := findFrame(g, handle)
	if frame == nil || frame != g.active || frame.header != header || frame.state != FrameActive ||
		header.SuspendReason != uint16(SuspendFrameComplete) ||
		header.Lifecycle != uint16(FrameFinalSuspended) {
		return false
	}
	g.pending = pendingTransition{kind: pendingComplete, from: frame}
	return true
}

func unlinkFrame(g *G, target *Frame) bool {
	if g == nil || target == nil {
		return false
	}
	link := &g.frames
	for *link != nil {
		if *link == target {
			*link = target.next
			target.next = nil
			return true
		}
		link = &(*link).next
	}
	return false
}

// ReleaseFrame validates the compiler deallocation callback and unlinks its
// combined allocation. The adapter must clear/free the returned range and
// must not dereference the Frame afterwards.
func ReleaseFrame(g *G, storage unsafe.Pointer, size, align uintptr, descriptor unsafe.Pointer) (unsafe.Pointer, uintptr, bool) {
	if !ValidG(g) || storage == nil {
		return nil, 0, false
	}
	frame := FrameFromStorage(storage)
	if frame == nil || frame.owner != g || frame.storage != storage || frame.size != size ||
		frame.align != align || frame.descriptor != descriptor || frame.state != FrameDestroyPending ||
		g.destroyTarget != frame || frame.header == nil ||
		frame.header.Lifecycle != uint16(FrameDestroyPending) {
		return nil, 0, false
	}
	raw, total := frame.rawBase, frame.allocationSize
	if !unlinkFrame(g, frame) {
		return nil, 0, false
	}
	frame.state = FrameDestroyed
	frame.header.Lifecycle = uint16(FrameDestroyed)
	g.destroyTarget = nil
	return raw, total, true
}
