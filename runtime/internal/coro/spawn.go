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

package coro

import "unsafe"

type taskStorageState uint8

const (
	// Static bootstrap Gs have no separately owned storage. The zero value is
	// deliberately static so InitG can continue to initialize global G objects.
	taskStorageStatic taskStorageState = iota
	taskStorageOwned
	taskStorageReleased
)

// TaskStorageSize is the exact scheduler-owned prefix required for one
// independently scheduled G. The G begins at its runtime allocation base so
// the C ABI can pass the returned address directly to a coroutine root
// factory; a runtime adapter may retain target-specific task-local storage in
// the same scanned/root allocation after this prefix.
func TaskStorageSize() uintptr {
	return unsafe.Sizeof(G{})
}

// BindTaskLocal attaches one opaque runtime context to a freshly initialized
// G. The scheduler retains it only as a scanned pointer and never inspects its
// representation.
func BindTaskLocal(g *G, local unsafe.Pointer) bool {
	if !ValidG(g) || g.state != GNew || local == nil || g.taskLocal != nil ||
		g.root != nil || g.active != nil || g.frames != nil ||
		g.queued || g.nextReady != nil || g.waiting || g.runP != nil ||
		!validLiveTaskStorage(g) {
		return false
	}
	g.taskLocal = local
	return true
}

// BindTaskLocalCompiler attaches the runtime sidecar inside an active compiler
// spawn transaction. BeginSpawnCompiler has already proved the zero-filled
// owned allocation and the child is not scheduler-visible until CommitSpawn;
// the reciprocal parent/P links are therefore the exact publication guard.
func BindTaskLocalCompiler(g *G, local unsafe.Pointer) bool {
	if !ValidG(g) || local == nil || g.taskLocal != nil || g.state != GNew ||
		g.spawnParent == nil || g.spawnP == nil || g.spawnParent.spawnChild != g ||
		g.taskState != taskStorageOwned || g.taskStorage != unsafe.Pointer(g) ||
		g.taskSize != TaskStorageSize() || g.root != nil || g.active != nil || g.frames != nil {
		return false
	}
	g.taskLocal = local
	return true
}

// TaskLocal returns the runtime-adapter context attached to g.
func TaskLocal(g *G) unsafe.Pointer {
	if !ValidG(g) {
		return nil
	}
	return g.taskLocal
}

// ReleaseTaskLocal transfers the opaque context of one terminal G back to its
// runtime adapter. Task storage remains scheduler-owned until the subsequent
// ReleaseTaskStorage call.
func ReleaseTaskLocal(g *G) (unsafe.Pointer, bool) {
	if !ReclaimableG(g) || g.taskLocal == nil {
		return nil, false
	}
	local := g.taskLocal
	g.taskLocal = nil
	return local, true
}

func validLiveTaskStorage(g *G) bool {
	if g == nil {
		return false
	}
	switch g.taskState {
	case taskStorageStatic:
		return g.taskStorage == nil && g.taskSize == 0
	case taskStorageOwned:
		return g.taskStorage == unsafe.Pointer(g) && g.taskSize == TaskStorageSize()
	default:
		return false
	}
}

func validTerminalTaskStorage(g *G) bool {
	if g == nil {
		return false
	}
	switch g.taskState {
	case taskStorageStatic:
		return g.taskStorage == nil && g.taskSize == 0
	case taskStorageOwned:
		return g.taskStorage == unsafe.Pointer(g) && g.taskSize == TaskStorageSize()
	case taskStorageReleased:
		return g.taskStorage == nil && g.taskSize == 0
	default:
		return false
	}
}

// runningSpawnContext validates the exact scheduler-stack episode in which a
// closed-static go statement may create a child. No scheduler-owned field may
// be touched from a factory running outside this parent/P resume pair.
func runningSpawnContext(parent *G) (*P, bool) {
	if !ValidG(parent) || parent.state != GRunning || parent.root == nil || parent.active == nil ||
		parent.active.owner != parent || parent.active.handle == nil || parent.active.header == nil ||
		parent.active.state != FrameActive || parent.active.header.G != unsafe.Pointer(parent) ||
		parent.active.header.SuspendReason != uint16(SuspendNone) ||
		parent.active.header.Lifecycle != uint16(FrameActive) ||
		parent.pending.kind != pendingNone || parent.pending.from != nil || parent.pending.target != nil ||
		parent.destroyTarget != nil || parent.destroyRoot || parent.queued || parent.nextReady != nil ||
		parent.waiting ||
		!releasableParkState(&parent.park) ||
		parent.spawnParent != nil || parent.spawnP != nil ||
		parent.transferState != runnableTransferGIdle || !validLiveTaskStorage(parent) {
		return nil, false
	}
	p := parent.runP
	// The current resume owns every mutable P queue link. Validate only the
	// queue endpoints and the exact affinity owner here: walking all ready and
	// parked tasks for each go statement makes a burst of N spawns O(N^2).
	// Lifecycle, shutdown, transfer, and diagnostic boundaries retain the full
	// audits; ordinary enqueue/park operations already preserve these headers.
	if !resumeGateTaken(parent) || !validReadyQueueHeader(p) ||
		!validParkWaitQueueHeader(p) || !validAffectedWaitQueueHeader(p) ||
		!validOSThreadOwnerHeader(p) {
		return nil, false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return nil, false
	}
	return p, true
}

// CanBeginSpawn is a read-only preflight used by the runtime adapter before it
// allocates child task storage. BeginSpawn repeats every check before
// publishing ownership; the preflight is only an allocation fast-fail.
func CanBeginSpawn(parent *G) bool {
	_, ok := runningSpawnContext(parent)
	return ok && parent.spawnChild == nil
}

// BeginSpawn publishes one parent-owned creation transaction around an empty,
// separately allocated G. The parent link is the GC root between this call and
// CommitSpawn while the compiler directly creates the child's initial-
// suspended LLVM coroutine frame.
func BeginSpawn(parent, child *G, storage unsafe.Pointer, size uintptr) bool {
	p, ok := runningSpawnContext(parent)
	if !ok || parent.spawnChild != nil || child == nil || child == parent ||
		storage != unsafe.Pointer(child) || size != TaskStorageSize() ||
		uintptr(storage)%unsafe.Alignof(G{}) != 0 {
		return false
	}
	if !InitG(child) {
		return false
	}
	child.taskStorage = storage
	child.taskSize = size
	child.taskState = taskStorageOwned
	child.spawnParent = parent
	child.spawnP = p
	parent.spawnChild = child
	return true
}

// BeginSpawnCompiler consumes the private generated go-statement boundary.
// The child range comes directly from the runtime's zero-filled task allocator
// and the parent is inside the exact compiler resume episode. Those two
// capabilities make InitG's arbitrary-memory audit and runningSpawnContext's
// queue/park compatibility audit redundant on this adjacent path. The public
// BeginSpawn contract remains the full validator for every other caller.
func BeginSpawnCompiler(parent, child *G, storage unsafe.Pointer, size uintptr) bool {
	if parent == nil || child == nil || child == parent || storage != unsafe.Pointer(child) ||
		size != TaskStorageSize() || uintptr(storage)%unsafe.Alignof(G{}) != 0 ||
		parent.spawnChild != nil || !resumeGateTaken(parent) {
		return false
	}
	p := parent.runP
	active := parent.active
	if p == nil || active == nil || active.owner != parent ||
		active.header == nil || active.state != FrameActive ||
		active.header.G != unsafe.Pointer(parent) ||
		active.header.SuspendReason != uint16(SuspendNone) ||
		active.header.Lifecycle != uint16(FrameActive) ||
		child.magic != 0 || !gPreemptStateAtDepthZero(child, preemptDisabled) ||
		child.state != GNew || child.taskState != taskStorageStatic ||
		child.taskStorage != nil || child.taskLocal != nil {
		return false
	}
	child.magic = gMagic
	child.taskStorage = storage
	child.taskSize = size
	child.taskState = taskStorageOwned
	child.spawnParent = parent
	child.spawnP = p
	// Publish the asynchronous preemption gate only after the owned allocation
	// and reciprocal transaction fields are complete.
	if !compareAndSwapGPreemptStateAtDepthZero(child, preemptDisabled, preemptIdle) {
		return false
	}
	parent.spawnChild = child
	return true
}

// activeSpawnTransaction consumes the reciprocal parent/child/P links
// published by BeginSpawn as an unforgeable scheduler-owned certificate. The
// compiler's root factory cannot suspend or run another scheduler reduction,
// so the links remain exclusive until CommitSpawn or RollbackSpawn clears
// them. Revalidate the live resume owner because an asynchronous preemption
// request may arrive, but do not repeat every parent and queue audit already
// completed at BeginSpawn.
func activeSpawnTransaction(parent, child *G) (*P, bool) {
	if parent == nil || child == nil || parent == child ||
		parent.spawnChild != child || child.spawnParent != parent ||
		child.spawnP == nil || parent.runP != child.spawnP ||
		!resumeGateTaken(parent) {
		return nil, false
	}
	return child.spawnP, true
}

func validDiscardResultSpawnRoot(child *G, handle unsafe.Pointer) (*Frame, bool) {
	root := findFrame(child, handle)
	if root == nil || child.frames != root || root.next != nil || root.owner != child ||
		root.parent != nil || root.handle != handle || root.header == nil ||
		root.header.G != unsafe.Pointer(child) || root.header.Parent != nil ||
		root.header.Descriptor == nil || root.header.ResultSlot != nil ||
		root.header.SuspendReason != uint16(SuspendNone) ||
		root.header.Lifecycle != uint16(FrameInitialSuspended) ||
		root.state != FrameInitialSuspended ||
		!checkedProgramObjectV1(root.header.Descriptor, unsafe.Sizeof(FrameDescriptorV1{}), unsafe.Alignof(FrameDescriptorV1{})) {
		return nil, false
	}
	descriptor := (*FrameDescriptorV1)(root.header.Descriptor)
	if descriptor.Version != 1 || descriptor.Flags&^frameDescriptorAllowedFlagsV1 != 0 ||
		!validProgramPayloadLayoutV1(descriptor.ResultSize, descriptor.ResultAlign) {
		return nil, false
	}
	return root, true
}

// CommitSpawn atomically adopts the independently created root and appends its
// G to the current P's ready queue. Every potentially failing check happens
// before RequestPreempt and the scheduler-owned stores, so failure never
// exposes a half-adopted or half-enqueued child. The first local child does not
// force an immediate parent yield: a parent which is about to park can hand the
// P directly to that child without exporting either continuation or ringing a
// peer doorbell. An already non-empty local queue requests preemption so bursts
// still reach the scheduler promptly and can be shared with idle Ps.
func CommitSpawn(parent, child *G, handle unsafe.Pointer) bool {
	p, ok := activeSpawnTransaction(parent, child)
	if !ok || handle == nil ||
		!ValidG(child) || child.state != GNew || child.root != nil || child.active != nil ||
		child.pending.kind != pendingNone || child.pending.from != nil || child.pending.target != nil ||
		child.destroyTarget != nil || child.destroyRoot || child.nextReady != nil || child.queued ||
		child.waiting || child.runP != nil ||
		child.spawnChild != nil ||
		child.transferState != runnableTransferGIdle ||
		child.taskState != taskStorageOwned || child.taskStorage != unsafe.Pointer(child) ||
		child.taskSize != TaskStorageSize() || !gPreemptStateAtDepthZero(child, preemptIdle) {
		return false
	}
	root, ok := validDiscardResultSpawnRoot(child, handle)
	if !ok || !validReadyQueueHeader(p) || p.readyCount == ^uint32(0) {
		return false
	}
	// This cannot fail after the complete parent/child/P validation above. It is
	// intentionally issued before queue publication so no post-publication
	// operation can force CommitSpawn to report failure. A sole new child is
	// serviced when the parent next parks, yields, or exhausts its ordinary
	// compiler-poll quantum; it needs no eager scheduling transaction.
	if p.readyCount != 0 && !RequestPreempt(parent) {
		return false
	}

	child.root = root
	child.active = root
	child.state = GRunnable
	child.spawnParent = nil
	child.spawnP = nil
	appendReadyUnchecked(p, child)
	// Clear the temporary root only after P's queue reaches the child.
	parent.spawnChild = nil
	return true
}

// CommitSpawnCompiler consumes the reciprocal begin receipt immediately after
// the generated root ramp published its initial-suspended frame. The new root
// is necessarily the frame-list head and PublishFrameV3Compiler has already
// validated its descriptor capability, so an O(n) handle lookup and another
// program-object range proof are unnecessary here.
func CommitSpawnCompiler(parent, child *G, handle unsafe.Pointer) bool {
	if parent == nil || child == nil || child == parent || handle == nil ||
		parent.spawnChild != child || child.spawnParent != parent ||
		child.spawnP == nil || parent.runP != child.spawnP {
		return false
	}
	p := child.spawnP
	// BeginSpawnCompiler authenticated the complete resume gate before
	// publishing these reciprocal links. The generated root ramp reaches only
	// initial suspend and cannot run a scheduler reduction, so the compact live
	// episode check below consumes that receipt without replaying the full gate.
	if p.current != parent || !p.inResume || parent.state != GRunning ||
		!p.runDecisionTaken || p.runDecision != (RunDecision{}) ||
		p.action.Kind != ActionResume || p.action.Flags != 0 || p.action.Handle == nil {
		return false
	}
	root := child.frames
	if child.magic != gMagic || child.state != GNew || child.root != nil || child.active != nil ||
		child.spawnChild != nil || child.nextReady != nil || child.queued || child.waiting ||
		child.runP != nil || child.transferState != runnableTransferGIdle ||
		child.taskState != taskStorageOwned || child.taskStorage != unsafe.Pointer(child) ||
		child.taskSize != TaskStorageSize() || !gPreemptStateAtDepthZero(child, preemptIdle) ||
		root == nil || root.next != nil || root.owner != child || root.parent != nil ||
		root.handle != handle || root.header == nil || root.header.G != unsafe.Pointer(child) ||
		root.header.Parent != nil || root.header.Descriptor == nil ||
		root.header.ResultSlot != nil || root.header.SuspendReason != uint16(SuspendNone) ||
		root.header.Lifecycle != uint16(FrameInitialSuspended) ||
		root.state != FrameInitialSuspended ||
		!validReadyQueueHeader(p) || p.readyCount == ^uint32(0) {
		return false
	}
	if p.readyCount != 0 && !RequestPreempt(parent) {
		return false
	}

	child.root = root
	child.active = root
	child.state = GRunnable
	child.spawnParent = nil
	child.spawnP = nil
	appendReadyUnchecked(p, child)
	parent.spawnChild = nil
	return true
}

// RollbackSpawn releases a begin transaction only before any coroutine frame
// has been allocated. Once a factory has published a handle, rejection is
// fail-stop: only the scheduler may destroy that handle, so the exported ABI
// aborts instead of trying to free it on the parent executor stack.
func RollbackSpawn(parent, child *G) (unsafe.Pointer, uintptr, bool) {
	_, ok := activeSpawnTransaction(parent, child)
	if !ok || !ValidG(child) || child.spawnChild != nil ||
		child.state != GNew || child.root != nil || child.active != nil || child.frames != nil ||
		child.pending.kind != pendingNone || child.destroyTarget != nil || child.destroyRoot ||
		child.nextReady != nil || child.queued || child.waiting || child.runP != nil ||
		!releasableParkState(&child.park) || child.park.taskCancelKind != TaskCancelNone ||
		child.transferState != runnableTransferGIdle ||
		child.taskState != taskStorageOwned || child.taskStorage != unsafe.Pointer(child) ||
		child.taskSize != TaskStorageSize() || !gPreemptStateAtDepthZero(child, preemptIdle) {
		return nil, 0, false
	}
	if !disableGPreempt(child) {
		return nil, 0, false
	}
	raw, size := child.taskStorage, child.taskSize
	parent.spawnChild = nil
	child.spawnParent = nil
	child.spawnP = nil
	child.taskStorage = nil
	child.taskSize = 0
	child.taskState = taskStorageReleased
	child.state = GDead
	return raw, size, true
}

// ReclaimableG is the per-G terminal predicate. Unlike TerminalG it does not
// require the whole P to be empty/disabled, so a completed child can retire
// while its parent and peers remain runnable or parked. It deliberately rejects
// taskStorageReleased: exactly one caller may observe a task as reclaimable and
// transfer its allocation.
func ReclaimableG(g *G) bool {
	return ValidG(g) && gPreemptStateAtDepthZero(g, preemptDisabled) && g.state == GDead &&
		g.taskControlLeases == 0 && g.runAction == ActionInvalid && g.transferState == runnableTransferGIdle &&
		g.osThreadLockDepth == 0 &&
		g.root == nil && g.active == nil && g.frames == nil &&
		g.pending.kind == pendingNone && !g.pending.directChannel && g.pending.from == nil && g.pending.target == nil &&
		g.destroyTarget == nil && !g.destroyRoot && g.nextReady == nil && !g.queued &&
		!g.waiting && g.runP == nil &&
		releasableParkState(&g.park) && g.park.taskCancelKind == TaskCancelNone &&
		g.spawnChild == nil && g.spawnParent == nil && g.spawnP == nil && validLiveTaskStorage(g) &&
		emptyPanicRecord(&g.panicRecord) && !g.panicUnwind &&
		g.panicTraceHead == nil && g.panicTraceTail == nil && g.panicTraceCount == 0
}

// TaskStorageOwned reports the only two legal storage states at ActionComplete.
// A released value is rejected so the runtime cannot silently free one task
// allocation twice.
func TaskStorageOwned(g *G) (owned bool, ok bool) {
	if !ReclaimableG(g) {
		return false, false
	}
	switch g.taskState {
	case taskStorageStatic:
		return false, g.taskStorage == nil && g.taskSize == 0
	case taskStorageOwned:
		return true, g.taskStorage == unsafe.Pointer(g) && g.taskSize == TaskStorageSize()
	default:
		return false, false
	}
}

// ReleaseTaskStorage transfers one terminal spawned G allocation back to the
// runtime adapter. It marks the transfer before returning; the caller must not
// dereference g after clearing/freeing raw. External completion producers own
// only POD source identities; the stable catalogs retain scheduler state
// until quiescence and must never retain this child after task retirement.
func ReleaseTaskStorage(g *G) (raw unsafe.Pointer, size uintptr, ok bool) {
	owned, valid := TaskStorageOwned(g)
	if !valid || !owned {
		return nil, 0, false
	}
	raw, size = g.taskStorage, g.taskSize
	g.taskStorage = nil
	g.taskSize = 0
	g.taskState = taskStorageReleased
	return raw, size, true
}

// ReleaseCompletedTask transfers both runtime-adapter context and physical
// task storage after one complete terminal audit. Production retirement owns
// these two values as one scheduler-thread transaction: the adapter destroys
// the returned context while the owned allocation is still live, then clears
// and frees that allocation. Clearing taskLocal/taskState here makes duplicate
// transfer fail without repeating ReclaimableG through each release stage.
//
// The narrower ReleaseTaskLocal and ReleaseTaskStorage APIs remain available
// for diagnostics and lifecycle tests which intentionally exercise stages in
// isolation.
func ReleaseCompletedTask(g *G) (local, raw unsafe.Pointer, size uintptr, owned, ok bool) {
	if !ReclaimableG(g) || g.taskLocal == nil {
		return nil, nil, 0, false, false
	}
	local = g.taskLocal
	switch g.taskState {
	case taskStorageStatic:
		if g.taskStorage != nil || g.taskSize != 0 {
			return nil, nil, 0, false, false
		}
	case taskStorageOwned:
		if g.taskStorage != unsafe.Pointer(g) || g.taskSize != TaskStorageSize() {
			return nil, nil, 0, false, false
		}
		raw, size, owned = g.taskStorage, g.taskSize, true
		g.taskStorage = nil
		g.taskSize = 0
		g.taskState = taskStorageReleased
	default:
		return nil, nil, 0, false, false
	}
	g.taskLocal = nil
	return local, raw, size, owned, true
}

// ReleaseCompletedTaskCompiler consumes the ActionComplete receipt returned
// immediately after a generated root completed and its physical frame was
// destroyed. The normal spawned task has never parked or been canceled, so a
// zero ParkState plus the terminal ownership headers certifies that no source
// can retain it. Tasks with any exceptional residue fall back to the complete
// reclaimability audit.
func ReleaseCompletedTaskCompiler(g *G) (local, raw unsafe.Pointer, size uintptr, owned, ok bool) {
	if g != nil && g.magic == gMagic && gPreemptStateAtDepthZero(g, preemptDisabled) &&
		g.state == GDead && g.taskControlLeases == 0 && g.runAction == ActionInvalid &&
		g.transferState == runnableTransferGIdle && g.osThreadLockDepth == 0 &&
		g.root == nil && g.active == nil && g.frames == nil && g.pending == (pendingTransition{}) &&
		g.destroyTarget == nil && !g.destroyRoot && g.nextReady == nil && !g.queued && !g.waiting &&
		g.runP == nil && g.park == (ParkState{}) &&
		g.spawnChild == nil && g.spawnParent == nil && g.spawnP == nil &&
		emptyPanicRecord(&g.panicRecord) && !g.panicUnwind &&
		g.panicTraceHead == nil && g.panicTraceTail == nil && g.panicTraceCount == 0 &&
		g.taskLocal != nil && g.taskState == taskStorageOwned &&
		g.taskStorage == unsafe.Pointer(g) && g.taskSize == TaskStorageSize() {
		local, raw, size = g.taskLocal, g.taskStorage, g.taskSize
		g.taskLocal = nil
		g.taskStorage = nil
		g.taskSize = 0
		g.taskState = taskStorageReleased
		return local, raw, size, true, true
	}
	return ReleaseCompletedTask(g)
}

// DeadG is a narrow program-driver query. It does not imply that a command
// main may safely return: TerminalG must still prove that no ready or parked G
// survives.
func DeadG(g *G) bool {
	return ValidG(g) && gPreemptStateAtDepthZero(g, preemptDisabled) && g.state == GDead
}
