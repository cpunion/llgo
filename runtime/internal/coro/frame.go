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

// FrameDescriptorV1 is the runtime prefix emitted for every physical
// coroutine frame. SpawnCommit currently admits only zero-result goroutine
// roots, so it validates this descriptor instead of trusting a nil result
// slot alone.
type FrameDescriptorV1 struct {
	Version     uint32
	Flags       uint32
	HashLo      uint64
	HashHi      uint64
	ResultSize  uintptr
	ResultAlign uintptr
}

// SuspendReason describes why a coroutine returned control to its scheduler.
type SuspendReason uint16

const (
	SuspendNone SuspendReason = iota
	SuspendCall
	SuspendFrameComplete
	// SuspendYield returns the active stackless frame to the ready queue. It is
	// shared by explicit yields and compiler-inserted preemption polls: neither
	// path changes the frame chain or transfers ownership of the LLVM handle.
	SuspendYield
	// SuspendPark returns the active frame to the scheduler until an exact
	// versioned WaitTicket is completed. The platform event source owns only
	// the ticket; it never resumes an LLVM handle or mutates scheduler queues.
	SuspendPark
	// SuspendPanic is the terminal-only ExplicitStatus prototype. The active
	// frame has published its two-word panic value into its owning G and reached
	// final suspend. It is never used for cleanup, recover, Goexit, or an
	// implicit hardware fault in this first fail-closed slice.
	SuspendPanic
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
	pendingYield
	pendingPark
	pendingPanic
)

type pendingTransition struct {
	kind   pendingKind
	from   *Frame
	target *Frame
	wait   *WaitToken
	ticket WaitTicket
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
	if !ValidG(g) || g.pending.kind != pendingNone || g.spawnChild != nil {
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
	if !ValidG(g) || handle == nil || header == nil || g.pending.kind != pendingNone || g.spawnChild != nil {
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

// PrepareYield records that the active frame reached a cooperative or
// compiler-inserted preemption suspend point. The frame and its opaque LLVM
// handle remain owned by g; Resumed commits the transition only after the
// direct llvm.coro.resume wrapper has returned to the scheduler.
func PrepareYield(g *G, handle unsafe.Pointer, header *HeaderV1) bool {
	if !ValidG(g) || handle == nil || header == nil || g.pending.kind != pendingNone || g.spawnChild != nil {
		return false
	}
	frame := findFrame(g, handle)
	if frame == nil || frame != g.active || frame.header != header || frame.state != FrameActive ||
		header.SuspendReason != uint16(SuspendYield) ||
		header.Lifecycle != uint16(FrameSuspended) {
		return false
	}
	g.pending = pendingTransition{kind: pendingYield, from: frame}
	return true
}

// PreparePark records an exact external/platform completion wait. The token
// must be armed before the operation is submitted, so completion may safely
// race before, during, or after this call without losing a wakeup. As with all
// coroutine hooks, the transition is committed only after llvm.coro.resume
// returns to Resumed on the scheduler stack.
func PreparePark(g *G, handle unsafe.Pointer, header *HeaderV1, token *WaitToken, ticket WaitTicket) bool {
	if !ValidG(g) || handle == nil || header == nil || g.pending.kind != pendingNone || g.spawnChild != nil ||
		g.waitToken != nil || g.waitTicket != 0 || g.waiting || g.nextWait != nil {
		return false
	}
	frame := findFrame(g, handle)
	if frame == nil || frame != g.active || frame.header != header || frame.state != FrameActive ||
		header.SuspendReason != uint16(SuspendPark) ||
		header.Lifecycle != uint16(FrameSuspended) {
		return false
	}
	// Claim only after every scheduler-owned field has been validated. Once the
	// CAS succeeds, assigning the pending transition cannot fail, so a rejected
	// PreparePark never strands a claimed token.
	if !claimWait(token, ticket) {
		return false
	}
	g.pending = pendingTransition{kind: pendingPark, from: frame, wait: token, ticket: ticket}
	return true
}

func unlinkFrame(g *G, target *Frame) bool {
	if g == nil || target == nil {
		return false
	}
	if g.frames == target {
		g.frames = target.next
		target.next = nil
		return true
	}
	for previous := g.frames; previous != nil; previous = previous.next {
		if previous.next == target {
			previous.next = target.next
			target.next = nil
			return true
		}
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
