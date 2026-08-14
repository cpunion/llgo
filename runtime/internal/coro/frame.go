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
	Line           uint32
	Flags          uint32
}

// FrameDescriptorV1 is the runtime prefix emitted for every physical
// coroutine frame. SpawnCommit validates the descriptor result layout; a Go
// spawn requires a nil ResultSlot and discards any values returned by the
// target function.
type FrameDescriptorV1 struct {
	Version     uint32
	Flags       uint32
	HashLo      uint64
	HashHi      uint64
	ResultSize  uintptr
	ResultAlign uintptr
	Function    string
	File        string
}

const (
	FrameDescriptorTraceHiddenV1      uint32 = 1 << 0
	FrameDescriptorNoRuntimeContextV1 uint32 = 1 << 1
	frameDescriptorAllowedFlagsV1            = FrameDescriptorTraceHiddenV1 | FrameDescriptorNoRuntimeContextV1
)

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

// frameRuntimeContextMode is a publication-time cache of the immutable
// compiler descriptor capability. Generated frames are always published via
// PublishFrameV2/V3, so their hot resume path never has to decode Go strings
// and descriptor flags again. Legacy/test PublishFrame callers retain the
// unknown value and are validated from the live descriptor when queried.
type frameRuntimeContextMode uint8

const (
	frameRuntimeContextUnknown frameRuntimeContextMode = iota
	frameRuntimeContextRequired
	frameRuntimeContextNotRequired
)

func descriptorRuntimeContextMode(descriptor unsafe.Pointer) (frameRuntimeContextMode, bool) {
	if descriptor == nil {
		return frameRuntimeContextUnknown, false
	}
	value := (*FrameDescriptorV1)(descriptor)
	if value.Version != 1 || value.Flags&^frameDescriptorAllowedFlagsV1 != 0 ||
		len(value.Function) == 0 {
		return frameRuntimeContextUnknown, false
	}
	if value.Flags&FrameDescriptorNoRuntimeContextV1 != 0 {
		return frameRuntimeContextNotRequired, true
	}
	return frameRuntimeContextRequired, true
}

const gMagic uint32 = 0x434f524f // "CORO"

type pendingKind uint8

const (
	pendingNone pendingKind = iota
	pendingAwait
	// pendingInlineStart exists only between BeginInlineAwait selecting an
	// initial-suspended child and that child's compiler resume prologue taking
	// its synthetic zero decision. It is never observable at a scheduler
	// boundary and therefore is not handled by dispatchPending.
	pendingInlineStart
	pendingComplete
	pendingYield
	pendingParkSet
	pendingPanic
)

type pendingTransition struct {
	kind pendingKind
	// directChannel is a one-resume capability produced only by the compact
	// compiler/runtime channel transaction. It certifies that from, its wait
	// record, and the adjacent ParkState were constructed together before the
	// hchan waiter became visible. The bit is owner-only and is consumed on the
	// immediately following llvm.coro.resume return; generic parks leave it
	// clear and retain their complete graph audit.
	directChannel bool
	from          *Frame
	target        *Frame
}

// Frame is scheduler-owned metadata. It lives at the beginning of the same
// allocation as the aligned LLVM frame storage. A back-pointer immediately
// before storage makes the free hook independent of maps, TLS, pthreads,
// libuv, and any particular garbage collector.
type Frame struct {
	owner  *G
	handle unsafe.Pointer
	header *HeaderV1
	// completion is owned by this frame while it is suspended awaiting one
	// direct child.  The child publishes a terminal outcome before it becomes
	// destroy-pending; the parent consumes it only after the child allocation
	// has been released and the parent is active again.  Keeping the record in
	// scheduler metadata makes panic payload lifetime independent of the child
	// LLVM frame and gives every managed call shape one reconciliation point.
	completion CompletionRecord
	// parkWait points to caller-owned storage only from PrepareParkSet until
	// the matching V2 park is promoted. The record itself is spilled into the
	// direct-parking LLVM coroutine frame; ordinary frames pay only this
	// metadata pointer during the first migration stage.
	parkWait       *WaitSetRecord
	storage        unsafe.Pointer
	rawBase        unsafe.Pointer
	descriptor     unsafe.Pointer
	size           uintptr
	align          uintptr
	allocationSize uintptr
	panicLine      uint32
	state          FrameState
	runtimeContext frameRuntimeContextMode
	// retainPanicTrace is set only after a managed child publishes
	// CompletionPanic. It occupies the padding before parent on pointer-aligned
	// targets and transfers the destroyed allocation to the task trace chain.
	retainPanicTrace bool
	// borrowedStorage identifies scheduler metadata injected into an LLVM
	// coroutine frame by the compiler. Its LLVM storage is owned by the exact
	// static parent frame, so llvm.coro.free deliberately skips the ordinary
	// ReleaseFrame callback after CoroAnnotationElide. The runtime still links,
	// validates, and destroys this metadata through the same Frame protocol.
	borrowedStorage bool
	parent          *Frame
	next            *Frame
}

// BorrowedFrameStorageV2 is the compiler/runtime capacity contract for one
// Frame stored inside an elidable coroutine. It is intentionally opaque to
// generated code: the compiler only reserves and forwards this pointer, while
// the runtime initializes and remains the sole owner of Frame's private
// layout. Twenty pointer
// words cover both wasm32 and native layouts with expansion room; the compile-
// time assertion fails if runtime metadata ever outgrows the ABI capacity.
type BorrowedFrameStorageV2 [20]uintptr

var _ [int(unsafe.Sizeof(BorrowedFrameStorageV2{})) - int(unsafe.Sizeof(Frame{}))]byte

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

// RegisterFrame initializes a zero-filled combined allocation and links it
// into g. raw must be the base returned by the target runtime allocator, whose
// contract guarantees the complete range is cleared, and total must be exactly
// FrameAllocationSize(size, align).
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

// PublishFrameV2 binds either an ordinary dynamically allocated LLVM frame or
// compiler-injected metadata for an allocation-elided static child. metadata
// is ignored on the dynamic V1-compatible path; keeping that path unchanged
// makes the first elision gate reversible and isolates its lifetime rules.
func PublishFrameV2(
	g *G, handle unsafe.Pointer, header *HeaderV1, storage, metadata unsafe.Pointer,
) bool {
	if header == nil {
		return false
	}
	mode, modeOK := descriptorRuntimeContextMode(header.Descriptor)
	if !modeOK {
		return false
	}
	if storage != nil {
		if metadata == nil || !PublishFrame(g, handle, header, storage) {
			return false
		}
		frame := FrameFromStorage(storage)
		if frame == nil {
			return false
		}
		frame.runtimeContext = mode
		return true
	}
	if !ValidG(g) || handle == nil || header == nil || metadata == nil ||
		metadata == unsafe.Pointer(g) || metadata == unsafe.Pointer(header) ||
		uintptr(metadata)%unsafe.Alignof(Frame{}) != 0 ||
		header.G != unsafe.Pointer(g) || header.Descriptor == nil ||
		header.Lifecycle != uint16(FrameInitialSuspended) ||
		header.SuspendReason != uint16(SuspendNone) || findFrame(g, handle) != nil {
		return false
	}
	Zero(metadata, unsafe.Sizeof(Frame{}))
	frame := (*Frame)(metadata)
	frame.owner = g
	frame.handle = handle
	frame.header = header
	frame.descriptor = header.Descriptor
	frame.state = FrameInitialSuspended
	frame.runtimeContext = mode
	frame.borrowedStorage = true
	frame.next = g.frames
	g.frames = frame
	header.AllocationBase = metadata
	return true
}

// PublishFrameV3 initializes the complete compiler/runtime header and then
// publishes either dynamic storage or compiler-borrowed metadata. Keeping the
// initialization in this shared helper makes coroutine ramps small: generated
// code supplies only immutable descriptor/result operands and never duplicates
// the scheduler header's ten-field initialization sequence.
func PublishFrameV3(
	g *G, handle unsafe.Pointer, header *HeaderV1, storage, metadata,
	descriptor, resultSlot unsafe.Pointer,
) bool {
	if !ValidG(g) || handle == nil || header == nil || metadata == nil || descriptor == nil {
		return false
	}
	*header = HeaderV1{
		G:             unsafe.Pointer(g),
		Descriptor:    descriptor,
		ResultSlot:    resultSlot,
		SuspendReason: uint16(SuspendNone),
		Lifecycle:     uint16(FrameInitialSuspended),
	}
	return PublishFrameV2(g, handle, header, storage, metadata)
}

// PrepareAwait records a parent-to-child handoff. It never resumes either
// coroutine; only the runtime driver may perform handle operations requested
// by the scheduler action protocol.
func prepareAwait(
	g *G, parentHandle, childHandle unsafe.Pointer, completion bool,
	recoverType, recoverData unsafe.Pointer,
) bool {
	if !ValidG(g) || !resumeGateTaken(g) || g.pending.kind != pendingNone || g.spawnChild != nil ||
		!releasableParkState(&g.park) {
		return false
	}
	parent := findFrame(g, parentHandle)
	child := findFrame(g, childHandle)
	if parent == nil || child == nil || parent == child || g.active != parent ||
		parent.header == nil || child.header == nil || parent.state != FrameActive ||
		child.state != FrameInitialSuspended || parent.header.SuspendReason != uint16(SuspendCall) ||
		parent.header.Lifecycle != uint16(FrameSuspended) || child.header.Parent != parentHandle ||
		child.parent != nil || completion && !armAwaitCompletion(parent, child, recoverType, recoverData) ||
		!completion && (recoverType != nil || recoverData != nil) {
		return false
	}
	child.parent = parent
	g.pending = pendingTransition{kind: pendingAwait, from: parent, target: child}
	return true
}

// compilerReleasableParkState consumes the owner-only certificate established
// when a park reaches a source-free phase. Compiler hooks run inside one
// already authenticated resume episode, so replaying the complete union audit
// at every ordinary Go call does not establish a new ownership boundary.
// Compatibility and lifecycle APIs continue to use releasableParkState.
func compilerReleasableParkState(state *ParkState) bool {
	if state == nil || state.resolving || state.directChannel || state.attached != 0 || state.head != nil ||
		!validTaskCancelState(state.taskCancelKind, state.taskCancelPhase) {
		return false
	}
	switch state.phase {
	case parkIdle, parkConsumed, parkDelivered:
		return true
	default:
		return false
	}
}

// PrepareAwaitCompletionCompiler is the private compiler-hook lane for an
// ordinary managed call. The child ramp has just published the newest frame
// and the current frame is the exact active parent, so both frame identities
// are available in O(1). If that certificate is absent, retain the complete
// compatibility validator and fail-closed behavior.
func PrepareAwaitCompletionCompiler(g *G, parentHandle, childHandle unsafe.Pointer) bool {
	if ValidG(g) && parentHandle != nil && childHandle != nil && resumeGateTaken(g) &&
		g.pending.kind == pendingNone && g.spawnChild == nil &&
		compilerReleasableParkState(&g.park) {
		parent, child := g.active, g.frames
		if parent != nil && child != nil && parent != child &&
			parent.handle == parentHandle && child.handle == childHandle &&
			parent.owner == g && child.owner == g &&
			parent.header != nil && child.header != nil &&
			parent.state == FrameActive && child.state == FrameInitialSuspended &&
			parent.header.SuspendReason == uint16(SuspendCall) &&
			parent.header.Lifecycle == uint16(FrameSuspended) &&
			child.header.Parent == parentHandle && child.parent == nil &&
			emptyCompletionRecord(&parent.completion) {
			parent.completion.child = child.handle
			parent.completion.status = completionArmed
			child.parent = parent
			g.pending = pendingTransition{kind: pendingAwait, from: parent, target: child}
			return true
		}
	}
	return PrepareAwaitCompletion(g, parentHandle, childHandle)
}

// PrepareAwait preserves the original V1 scheduler transaction. It is kept for
// adapters and tests that intentionally have no child-outcome transport.
func PrepareAwait(g *G, parentHandle, childHandle unsafe.Pointer) bool {
	return prepareAwait(g, parentHandle, childHandle, false, nil, nil)
}

// PrepareAwaitCompletion is the V2 managed-call transaction. In addition to
// linking the child it arms the parent-owned CompletionRecord that must be
// published before the child is destroyed and consumed after parent resume.
func PrepareAwaitCompletion(g *G, parentHandle, childHandle unsafe.Pointer) bool {
	return prepareAwait(g, parentHandle, childHandle, true, nil, nil)
}

// PrepareAwaitCompletionRecover is the explicit-status defer transaction. A
// normalized non-nil panic type arms recover for this exact direct child while
// reusing the same CompletionRecord that will later publish Return, Recovered,
// or a replacement Panic outcome.
func PrepareAwaitCompletionRecover(
	g *G, parentHandle, childHandle, typeWord, dataWord unsafe.Pointer,
) bool {
	if typeWord == nil {
		return false
	}
	return prepareAwait(g, parentHandle, childHandle, true, typeWord, dataWord)
}

// PrepareCompleteStatus records a final-suspended frame and its exact cleanup
// base. Destruction remains owned by the scheduler and occurs only after the
// resume operation returns. Goexit is a payload-free language outcome;
// Abort/Shutdown are admitted only after the task cancellation token has
// entered cleanup, so a malformed compiler cannot manufacture a terminal stop
// outcome.
func PrepareCompleteStatus(g *G, handle unsafe.Pointer, header *HeaderV1, status CompletionStatus) bool {
	if !ValidG(g) || !resumeGateTaken(g) || handle == nil || header == nil || g.pending.kind != pendingNone || g.spawnChild != nil ||
		!releasableParkState(&g.park) || g.park.taskCancelPhase == taskCancelRequested {
		return false
	}
	switch status {
	case CompletionReturn:
	case CompletionGoexit:
	case CompletionAbort:
		if g.park.taskCancelPhase != taskCancelCleanup || g.park.taskCancelKind != TaskCancelAbort {
			return false
		}
	case CompletionShutdown:
		if g.park.taskCancelPhase != taskCancelCleanup || g.park.taskCancelKind != TaskCancelShutdown {
			return false
		}
	default:
		return false
	}
	frame := findFrame(g, handle)
	if frame == nil || frame != g.active || frame.header != header || frame.state != FrameActive ||
		header.SuspendReason != uint16(SuspendFrameComplete) ||
		header.Lifecycle != uint16(FrameFinalSuspended) ||
		(frame.parent != nil && awaitCompletionArmedForChild(frame) &&
			!validAwaitCompletionPublisher(g, frame, status)) {
		return false
	}
	if frame.parent != nil && awaitCompletionArmedForChild(frame) {
		if !publishAwaitCompletion(frame.parent, status, nil, nil) {
			return false
		}
	}
	g.pending = pendingTransition{kind: pendingComplete, from: frame}
	return true
}

// PrepareCompleteStatusCompiler handles the dominant ordinary-return suffix
// from compiler-generated code using the active-frame and parent-completion
// certificates already established by PrepareAwaitCompletionCompiler. Panic,
// Goexit, cancellation, roots, and any uncertain shape retain the complete
// status validator below.
func PrepareCompleteStatusCompiler(g *G, handle unsafe.Pointer, header *HeaderV1, status CompletionStatus) bool {
	if status == CompletionReturn && ValidG(g) && resumeGateTaken(g) &&
		handle != nil && header != nil && g.pending.kind == pendingNone &&
		g.spawnChild == nil && compilerReleasableParkState(&g.park) &&
		g.park.taskCancelPhase != taskCancelRequested {
		frame := g.active
		if frame != nil && frame.parent != nil && frame.handle == handle && frame.header == header &&
			frame.owner == g && frame.state == FrameActive &&
			header.SuspendReason == uint16(SuspendFrameComplete) &&
			header.Lifecycle == uint16(FrameFinalSuspended) {
			if awaitCompletionArmedForChild(frame) &&
				publishAwaitCompletion(frame.parent, CompletionReturn, nil, nil) {
				g.pending = pendingTransition{kind: pendingComplete, from: frame}
				return true
			}
		}
	}
	return PrepareCompleteStatus(g, handle, header, status)
}

// PrepareComplete preserves the normal-return V1 adapter contract.
func PrepareComplete(g *G, handle unsafe.Pointer, header *HeaderV1) bool {
	return PrepareCompleteStatus(g, handle, header, CompletionReturn)
}

// PrepareYield records that the active frame reached a cooperative or
// compiler-inserted preemption suspend point. The frame and its opaque LLVM
// handle remain owned by g; Resumed commits the transition only after the
// direct llvm.coro.resume wrapper has returned to the scheduler.
func PrepareYield(g *G, handle unsafe.Pointer, header *HeaderV1) bool {
	if !ValidG(g) || !resumeGateTaken(g) || handle == nil || header == nil || g.pending.kind != pendingNone || g.spawnChild != nil ||
		!releasableParkState(&g.park) {
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

// PrepareParkSet records a V2 multi-source park. Every candidate operation is
// already attached and producer-visible; CommitParkSet is the exact owner-P
// claim that makes the logical ticket eligible for SourceSet resolution.
// Completion may have been published early in an OperationRecord, but no
// callback receives G, ParkState, or an LLVM handle.
func PrepareParkSet(g *G, handle unsafe.Pointer, header *HeaderV1, ticket ParkTicket, record *WaitSetRecord) bool {
	if !ValidG(g) || !resumeGateTaken(g) || handle == nil || header == nil || g.pending.kind != pendingNone || g.spawnChild != nil ||
		g.waiting ||
		!validParkState(&g.park) || g.park.phase != parkSealed || ticket != g.park.ticket ||
		!validPreparingWaitSetRecord(record, &g.park, ticket) {
		return false
	}
	frame := findFrame(g, handle)
	if frame == nil || frame != g.active || frame.header != header || frame.state != FrameActive ||
		frame.parkWait != nil ||
		header.SuspendReason != uint16(SuspendPark) ||
		header.Lifecycle != uint16(FrameSuspended) {
		return false
	}
	for link := g.park.head; link != nil; link = link.next {
		if link.wait != record {
			return false
		}
	}
	if !CommitParkSet(&g.park, ticket) {
		return false
	}
	record.state = waitSetRecordCommitted
	frame.parkWait = record
	g.pending = pendingTransition{kind: pendingParkSet, from: frame}
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
	if !ValidG(g) || !gPreemptEnabledAtDepthZero(g) || storage == nil {
		return nil, 0, false
	}
	frame := FrameFromStorage(storage)
	if frame == nil || frame.owner != g || frame.storage != storage || frame.size != size ||
		frame.align != align || frame.descriptor != descriptor || frame.state != FrameDestroyPending ||
		g.destroyTarget != frame || frame.header == nil || frame.parkWait != nil ||
		frame.header.Lifecycle != uint16(FrameDestroyPending) {
		return nil, 0, false
	}
	raw, total := frame.rawBase, frame.allocationSize
	if !unlinkFrame(g, frame) {
		return nil, 0, false
	}
	frame.state = FrameDestroyed
	frame.panicLine = frame.header.Line
	frame.header.Lifecycle = uint16(FrameDestroyed)
	g.destroyTarget = nil
	return raw, total, true
}

// CommitFrameDestroyV2 completes the physical destroy of an allocation-elided
// static frame. A dynamic frame has already been unlinked by ReleaseFrame and
// is accepted without a second mutation. The adapter invokes this immediately
// after every llvm.coro.destroy, before any logical Destroyed transition.
func CommitFrameDestroyV2(g *G, handle unsafe.Pointer) bool {
	if !ValidG(g) || handle == nil || !gPreemptEnabledAtDepthZero(g) {
		return false
	}
	frame := g.destroyTarget
	if frame == nil {
		// The dynamic free callback already consumed the exact target. A stale
		// schedulable frame with the same handle would make that receipt invalid.
		return findFrame(g, handle) == nil
	}
	if frame.handle != handle || !frame.borrowedStorage || frame.storage != nil ||
		frame.rawBase != nil || frame.allocationSize != 0 || frame.owner != g ||
		frame.header == nil || frame.parkWait != nil || frame.state != FrameDestroyPending ||
		frame.header.AllocationBase != unsafe.Pointer(frame) ||
		frame.header.Lifecycle != uint16(FrameDestroyPending) || !unlinkFrame(g, frame) {
		return false
	}
	frame.state = FrameDestroyed
	frame.panicLine = frame.header.Line
	frame.header.Lifecycle = uint16(FrameDestroyed)
	g.destroyTarget = nil
	if retainDestroyedBorrowedPanicTraceFrame(g, frame) {
		return true
	}
	Zero(unsafe.Pointer(frame), unsafe.Sizeof(Frame{}))
	return true
}

// CommitFrameDestroyCompiler consumes the receipt produced synchronously by
// the immediately preceding compiler-owned llvm.coro.destroy. A dynamic frame
// can clear destroyTarget only through ReleaseFrame after validating the exact
// storage, descriptor, size, lifecycle, and target. Allocation-elided frames
// retain destroyTarget and therefore use the complete unlink transaction.
func CommitFrameDestroyCompiler(g *G, handle unsafe.Pointer) bool {
	if ValidG(g) && handle != nil && gPreemptEnabledAtDepthZero(g) &&
		g.destroyTarget == nil {
		return true
	}
	return CommitFrameDestroyV2(g, handle)
}

// PanicTraceFrameSnapshot is the allocation-free diagnostic prefix retained
// from one destroyed physical frame. Function and File point into immutable
// descriptor storage emitted by the compiler.
type PanicTraceFrameSnapshot struct {
	Function string
	File     string
	Line     uint32
	Hidden   bool
}

// ActiveTraceFrame snapshots the compiler descriptor for g's currently
// executing physical frame. It accepts both the ordinary active header and
// the narrow park-resume-hook window, where scheduler ownership has resumed
// the LLVM frame but the compiler intentionally calls its result hook before
// restoring SuspendNone/FrameActive. It is current-owner-only: callers may use
// the immutable strings after return, but must not retain or expose the private
// Frame or Header pointers across a suspension boundary.
//
// This is the exact metadata boundary needed when a foreign operation resumes
// with a fault. The worker owns no G pointer and cannot walk a stackless Go
// continuation; after resume, the owner can prepend this frame to the bounded
// native fault identities without reverse-looking-up a function address.
func ActiveTraceFrame(g *G) (PanicTraceFrameSnapshot, bool) {
	if !ValidG(g) || !resumeGateTaken(g) || g.state != GRunning ||
		g.active == nil || g.pending != (pendingTransition{}) || g.destroyTarget != nil ||
		g.destroyRoot || g.spawnChild != nil {
		return PanicTraceFrameSnapshot{}, false
	}
	frame := g.active
	header := frame.header
	activeHeader := header != nil &&
		header.SuspendReason == uint16(SuspendNone) &&
		header.Lifecycle == uint16(FrameActive)
	parkResumeHeader := header != nil &&
		header.SuspendReason == uint16(SuspendPark) &&
		header.Lifecycle == uint16(FrameSuspended)
	if frame.owner != g || frame.handle == nil || header == nil ||
		frame.descriptor == nil || frame.state != FrameActive ||
		header.G != unsafe.Pointer(g) || header.Descriptor != frame.descriptor ||
		(!activeHeader && !parkResumeHeader) {
		return PanicTraceFrameSnapshot{}, false
	}
	descriptor := (*FrameDescriptorV1)(frame.descriptor)
	if descriptor.Version != 1 ||
		descriptor.Flags & ^frameDescriptorAllowedFlagsV1 != 0 ||
		len(descriptor.Function) == 0 {
		return PanicTraceFrameSnapshot{}, false
	}
	return PanicTraceFrameSnapshot{
		Function: descriptor.Function,
		File:     descriptor.File,
		Line:     header.Line,
		Hidden:   descriptor.Flags&FrameDescriptorTraceHiddenV1 != 0,
	}, true
}

func emptyPanicTrace(g *G) bool {
	return g != nil && g.panicTraceHead == nil && g.panicTraceTail == nil &&
		g.panicTraceCount == 0
}

func activePanicTrace(g *G) bool {
	return g != nil && g.panicTraceHead != nil && g.panicTraceTail != nil &&
		g.panicTraceCount != 0
}

// panicTraceCarrier is the still-live frame which owns continued propagation
// of the retained trace. A retained frame preserves its logical parent, so the
// tail carries this fact without adding another pointer to every G.
func panicTraceCarrier(g *G) *Frame {
	if !activePanicTrace(g) {
		return nil
	}
	return g.panicTraceTail.parent
}

// A staged discard reuses the active head while tail=nil/count=0. The runtime
// adapter drains it synchronously before the compiler hook returns, so no
// second G pointer or target allocator dependency enters the scheduler core.
func stagedPanicTraceDiscard(g *G) bool {
	return g != nil && g.panicTraceHead != nil && g.panicTraceTail == nil &&
		g.panicTraceCount == 0
}

func panicTraceIdentity(frame *Frame) (typeWord, dataWord unsafe.Pointer, ok bool) {
	if frame == nil {
		return nil, nil, false
	}
	record := &frame.completion
	return record.typeWord, record.dataWord,
		record.status == CompletionPanic && record.child == nil && record.typeWord != nil
}

func canStagePanicTraceDiscard(g *G) bool {
	return emptyPanicTrace(g) || activePanicTrace(g)
}

func stagePanicTraceDiscard(g *G) bool {
	if !canStagePanicTraceDiscard(g) {
		return false
	}
	if emptyPanicTrace(g) {
		return true
	}
	g.panicTraceTail = nil
	g.panicTraceCount = 0
	return true
}

// ReplacePanicTrace applies the compiler's exact language-semantic signal that
// cleanup is about to replace the currently propagated panic. Payload identity
// is deliberately absent: panic(x) may replace an older panic(x) with identical
// interface words, which cannot be inferred after publication. The detached
// trace is released synchronously by the runtime adapter before it returns to
// generated code.
func ReplacePanicTrace(g *G, handle unsafe.Pointer) bool {
	if !ValidG(g) || !resumeGateTaken(g) || handle == nil ||
		g.pending != (pendingTransition{}) || g.destroyTarget != nil || g.destroyRoot ||
		g.queued || g.nextReady != nil || g.waiting ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		!releasableParkState(&g.park) || g.panicUnwind ||
		!emptyPanicRecord(&g.panicRecord) {
		return false
	}
	frame := findFrame(g, handle)
	if frame == nil || frame != g.active || frame.owner != g ||
		frame.header == nil || frame.state != FrameActive ||
		frame.header.G != unsafe.Pointer(g) ||
		frame.header.SuspendReason != uint16(SuspendNone) ||
		frame.header.Lifecycle != uint16(FrameActive) {
		return false
	}
	if activePanicTrace(g) && panicTraceCarrier(g) != frame {
		return false
	}
	return stagePanicTraceDiscard(g)
}

func retainPanicTraceFrameMetadata(
	g *G, frame *Frame, raw unsafe.Pointer, total uintptr, typeWord, dataWord unsafe.Pointer,
) bool {
	if !ValidG(g) || frame == nil || typeWord == nil ||
		g.destroyTarget != nil || g.panicTraceCount == ^uint32(0) ||
		!emptyPanicTrace(g) && !activePanicTrace(g) {
		return false
	}
	if frame.borrowedStorage {
		if raw != nil || total != 0 || frame.rawBase != nil || frame.allocationSize != 0 ||
			frame.storage != nil {
			return false
		}
	} else if raw == nil || total == 0 || frame.rawBase != raw ||
		frame.allocationSize != total || raw != unsafe.Pointer(frame) {
		return false
	}
	if frame.owner != g ||
		frame.state != FrameDestroyed || frame.next != nil || frame.descriptor == nil ||
		!emptyCompletionRecord(&frame.completion) {
		return false
	}
	descriptor := (*FrameDescriptorV1)(frame.descriptor)
	if descriptor.Version != 1 ||
		descriptor.Flags & ^frameDescriptorAllowedFlagsV1 != 0 ||
		len(descriptor.Function) == 0 {
		return false
	}
	if activePanicTrace(g) {
		existingType, existingData, ok := panicTraceIdentity(g.panicTraceHead)
		if !ok || existingType != typeWord || existingData != dataWord {
			return false
		}
		// Destroy order must extend the exact logical ancestry of the current
		// panic. A replacement trace is detached before its first new frame is
		// retained, so silently joining unrelated frame chains is never valid.
		if panicTraceCarrier(g) != frame {
			return false
		}
	}
	frame.header = nil
	frame.retainPanicTrace = false
	if emptyPanicTrace(g) {
		frame.completion = CompletionRecord{
			status:   CompletionPanic,
			typeWord: typeWord,
			dataWord: dataWord,
		}
		g.panicTraceHead = frame
	} else {
		g.panicTraceTail.next = frame
	}
	g.panicTraceTail = frame
	g.panicTraceCount++
	return true
}

func retainPanicTraceFrame(
	g *G,
	raw unsafe.Pointer,
	total uintptr,
	typeWord, dataWord unsafe.Pointer,
) bool {
	if raw == nil {
		return false
	}
	return retainPanicTraceFrameMetadata(g, (*Frame)(raw), raw, total, typeWord, dataWord)
}

func retainDestroyedBorrowedPanicTraceFrame(g *G, frame *Frame) bool {
	if frame == nil || !frame.borrowedStorage {
		return false
	}
	if frame.retainPanicTrace && frame.parent != nil &&
		frame.parent.completion.status == CompletionPanic &&
		frame.parent.completion.child == frame.handle &&
		frame.parent.completion.typeWord != nil {
		record := frame.parent.completion
		return retainPanicTraceFrameMetadata(
			g, frame, nil, 0, record.typeWord, record.dataWord,
		)
	}
	if g.panicUnwind && publishedPanicRecord(&g.panicRecord) {
		return retainPanicTraceFrameMetadata(
			g, frame, nil, 0, g.panicRecord.typeWord, g.panicRecord.dataWord,
		)
	}
	return false
}

// RetainPendingPanicTraceFrame transfers one managed child frame whose panic
// remains owned by its resumed parent. The parent CompletionRecord supplies
// the stable panic identity; later propagation appends ancestors, while an
// exact recover/replacement stages the chain for adapter-owned release.
func RetainPendingPanicTraceFrame(g *G, raw unsafe.Pointer, total uintptr) bool {
	if !ValidG(g) || raw == nil {
		return false
	}
	frame := (*Frame)(raw)
	if !frame.retainPanicTrace || frame.parent == nil ||
		frame.parent.completion.status != CompletionPanic ||
		frame.parent.completion.child != frame.handle ||
		frame.parent.completion.typeWord == nil {
		return false
	}
	record := frame.parent.completion
	return retainPanicTraceFrame(g, raw, total, record.typeWord, record.dataWord)
}

// RetainPanicTraceFrame transfers one terminal command-root frame allocation
// to the same trace chain. The LLVM handle is already destroyed and the frame
// is no longer schedulable; a caller which receives true must not clear or free
// raw. Runtime adapters must call this terminal form only when they own a
// no-return panic reporter.
func RetainPanicTraceFrame(g *G, raw unsafe.Pointer, total uintptr) bool {
	if !ValidG(g) || !g.panicUnwind || !publishedPanicRecord(&g.panicRecord) {
		return false
	}
	record := &g.panicRecord
	return retainPanicTraceFrame(g, raw, total, record.typeWord, record.dataWord)
}

// PanicTraceDiscardPending reports the transient adapter-owned drain state.
// It is true only between a successful recovery/replacement transaction and
// the synchronous runtime hook that releases the detached allocations.
func PanicTraceDiscardPending(g *G) bool {
	return ValidG(g) && stagedPanicTraceDiscard(g)
}

// TakeDiscardedPanicTraceFrame pops one allocation from a trace staged by an
// exact recovery or panic replacement. A nil raw with ok=true means the drain
// is complete. The adapter owns zeroing/freeing each returned range.
func TakeDiscardedPanicTraceFrame(g *G) (raw unsafe.Pointer, total uintptr, ok bool) {
	if !ValidG(g) {
		return nil, 0, false
	}
	for {
		if emptyPanicTrace(g) {
			return nil, 0, true
		}
		if !stagedPanicTraceDiscard(g) {
			return nil, 0, false
		}
		frame := g.panicTraceHead
		if frame.owner != g || frame.state != FrameDestroyed ||
			frame.header != nil || frame.descriptor == nil {
			return nil, 0, false
		}
		if frame.borrowedStorage {
			if frame.rawBase != nil || frame.allocationSize != 0 || frame.storage != nil {
				return nil, 0, false
			}
		} else if frame.rawBase != unsafe.Pointer(frame) || frame.allocationSize == 0 {
			return nil, 0, false
		}
		g.panicTraceHead = frame.next
		frame.next = nil
		frame.parent = nil
		frame.completion = CompletionRecord{}
		if frame.borrowedStorage {
			Zero(unsafe.Pointer(frame), unsafe.Sizeof(Frame{}))
			continue
		}
		return frame.rawBase, frame.allocationSize, true
	}
}

// FirstPanicTraceFrame returns the opaque cursor for the deepest retained
// physical frame. Frames are appended in llvm.coro.destroy order, so following
// Next walks from the panic site toward the command root.
func FirstPanicTraceFrame(g *G) unsafe.Pointer {
	if !ValidG(g) || !publishedPanicRecord(&g.panicRecord) ||
		!activePanicTrace(g) {
		return nil
	}
	return unsafe.Pointer(g.panicTraceHead)
}

// LoadPanicTraceFrame validates and snapshots one cursor retained by
// RetainPanicTraceFrame. next is nil only for the exact tail.
func LoadPanicTraceFrame(g *G, cursor unsafe.Pointer) (snapshot PanicTraceFrameSnapshot, next unsafe.Pointer, ok bool) {
	if !ValidG(g) || cursor == nil || !publishedPanicRecord(&g.panicRecord) {
		return PanicTraceFrameSnapshot{}, nil, false
	}
	frame := (*Frame)(cursor)
	if frame.owner != g || frame.state != FrameDestroyed || frame.header != nil || frame.descriptor == nil ||
		frame.borrowedStorage && (frame.rawBase != nil || frame.allocationSize != 0 || frame.storage != nil) ||
		!frame.borrowedStorage && (frame.rawBase != cursor || frame.allocationSize == 0) {
		return PanicTraceFrameSnapshot{}, nil, false
	}
	descriptor := (*FrameDescriptorV1)(frame.descriptor)
	if descriptor.Version != 1 ||
		descriptor.Flags & ^frameDescriptorAllowedFlagsV1 != 0 ||
		len(descriptor.Function) == 0 {
		return PanicTraceFrameSnapshot{}, nil, false
	}
	if frame == g.panicTraceTail {
		if frame.next != nil {
			return PanicTraceFrameSnapshot{}, nil, false
		}
	} else if frame.next == nil || frame.parent != frame.next {
		return PanicTraceFrameSnapshot{}, nil, false
	}
	return PanicTraceFrameSnapshot{
		Function: descriptor.Function,
		File:     descriptor.File,
		Line:     frame.panicLine,
		Hidden:   descriptor.Flags&FrameDescriptorTraceHiddenV1 != 0,
	}, unsafe.Pointer(frame.next), true
}
