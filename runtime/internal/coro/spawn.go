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

// TaskStorageSize is the exact scanned/root allocation required for one
// independently scheduled G. The G begins at the allocation base so the C ABI
// can pass the returned address directly to a coroutine root factory.
func TaskStorageSize() uintptr {
	return unsafe.Sizeof(G{})
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
		parent.pending.wait != nil || parent.pending.ticket != 0 ||
		parent.destroyTarget != nil || parent.destroyRoot || parent.queued || parent.nextReady != nil ||
		parent.waitToken != nil || parent.waitTicket != 0 || parent.nextWait != nil || parent.waiting ||
		parent.spawnParent != nil || parent.spawnP != nil || !validLiveTaskStorage(parent) {
		return nil, false
	}
	p := parent.runP
	if p == nil || p.current != parent || !p.inResume ||
		!expectedAction(p, parent, p.action, ActionResume) ||
		!validReadyQueue(p) || !validWaitQueue(p) {
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

func validZeroResultSpawnRoot(child *G, handle unsafe.Pointer) (*Frame, bool) {
	root := findFrame(child, handle)
	if root == nil || child.frames != root || root.next != nil || root.owner != child ||
		root.parent != nil || root.handle != handle || root.header == nil ||
		root.header.G != unsafe.Pointer(child) || root.header.Parent != nil ||
		root.header.Descriptor != root.descriptor || root.header.ResultSlot != nil ||
		root.header.SuspendReason != uint16(SuspendNone) ||
		root.header.Lifecycle != uint16(FrameInitialSuspended) ||
		root.state != FrameInitialSuspended || root.descriptor == nil ||
		!checkedProgramObjectV1(root.descriptor, unsafe.Sizeof(FrameDescriptorV1{}), unsafe.Alignof(FrameDescriptorV1{})) {
		return nil, false
	}
	descriptor := (*FrameDescriptorV1)(root.descriptor)
	if descriptor.Version != 1 || descriptor.Flags != 0 ||
		descriptor.ResultSize != 0 || descriptor.ResultAlign != 1 {
		return nil, false
	}
	return root, true
}

// CommitSpawn atomically adopts the independently created root and appends its
// G to the current P's ready queue. Every potentially failing check happens
// before RequestPreempt and the scheduler-owned stores, so failure never
// exposes a half-adopted or half-enqueued child. The request forces the parent
// through its next compiler safepoint; yielding then places the parent behind
// the newly ready child.
func CommitSpawn(parent, child *G, handle unsafe.Pointer) bool {
	p, ok := runningSpawnContext(parent)
	if !ok || handle == nil || parent.spawnChild != child || child == nil ||
		!ValidG(child) || child.state != GNew || child.root != nil || child.active != nil ||
		child.pending.kind != pendingNone || child.pending.from != nil || child.pending.target != nil ||
		child.pending.wait != nil || child.pending.ticket != 0 ||
		child.destroyTarget != nil || child.destroyRoot || child.nextReady != nil || child.queued ||
		child.waitToken != nil || child.waitTicket != 0 || child.nextWait != nil || child.waiting || child.runP != nil ||
		child.spawnChild != nil || child.spawnParent != parent || child.spawnP != p ||
		child.taskState != taskStorageOwned || child.taskStorage != unsafe.Pointer(child) ||
		child.taskSize != TaskStorageSize() || preemptLoad(preemptAddress(child)) != preemptIdle {
		return false
	}
	root, ok := validZeroResultSpawnRoot(child, handle)
	if !ok || (p.readyHead == nil) != (p.readyTail == nil) ||
		(p.readyTail != nil && p.readyTail.nextReady != nil) {
		return false
	}
	// This cannot fail after the complete parent/child/P validation above. It is
	// intentionally issued before queue publication so no post-publication
	// operation can force CommitSpawn to report failure.
	if !RequestPreempt(parent) {
		return false
	}

	child.root = root
	child.active = root
	child.state = GRunnable
	child.spawnParent = nil
	child.spawnP = nil
	child.queued = true
	if p.readyTail == nil {
		p.readyHead = child
	} else {
		p.readyTail.nextReady = child
	}
	p.readyTail = child
	// Clear the temporary root only after P's queue reaches the child.
	parent.spawnChild = nil
	return true
}

// RollbackSpawn releases a begin transaction only before any coroutine frame
// has been allocated. Once a factory has published a handle, rejection is
// fail-stop: only the scheduler may destroy that handle, so the exported ABI
// aborts instead of trying to free it on the parent executor stack.
func RollbackSpawn(parent, child *G) (unsafe.Pointer, uintptr, bool) {
	p, ok := runningSpawnContext(parent)
	if !ok || parent.spawnChild != child || child == nil || !ValidG(child) ||
		child.spawnParent != parent || child.spawnP != p || child.spawnChild != nil ||
		child.state != GNew || child.root != nil || child.active != nil || child.frames != nil ||
		child.pending.kind != pendingNone || child.destroyTarget != nil || child.destroyRoot ||
		child.nextReady != nil || child.queued || child.waitToken != nil || child.waitTicket != 0 ||
		child.nextWait != nil || child.waiting || child.runP != nil ||
		child.taskState != taskStorageOwned || child.taskStorage != unsafe.Pointer(child) ||
		child.taskSize != TaskStorageSize() || preemptLoad(preemptAddress(child)) != preemptIdle {
		return nil, 0, false
	}
	raw, size := child.taskStorage, child.taskSize
	parent.spawnChild = nil
	child.spawnParent = nil
	child.spawnP = nil
	child.taskStorage = nil
	child.taskSize = 0
	child.taskState = taskStorageReleased
	preemptStore(preemptAddress(child), preemptDisabled)
	child.state = GDead
	return raw, size, true
}

// ReclaimableG is the per-G terminal predicate. Unlike TerminalG it does not
// require the whole P to be empty/disabled, so a completed child can retire
// while its parent and peers remain runnable or parked. It deliberately rejects
// taskStorageReleased: exactly one caller may observe a task as reclaimable and
// transfer its allocation.
func ReclaimableG(g *G) bool {
	return ValidG(g) && preemptLoad(preemptAddress(g)) == preemptDisabled && g.state == GDead &&
		g.root == nil && g.active == nil && g.frames == nil &&
		g.pending.kind == pendingNone && g.pending.from == nil && g.pending.target == nil &&
		g.pending.wait == nil && g.pending.ticket == 0 &&
		g.destroyTarget == nil && !g.destroyRoot && g.nextReady == nil && !g.queued &&
		g.waitToken == nil && g.waitTicket == 0 && g.nextWait == nil && !g.waiting && g.runP == nil &&
		g.spawnChild == nil && g.spawnParent == nil && g.spawnP == nil && validLiveTaskStorage(g) &&
		emptyPanicRecord(&g.panicRecord) && !g.panicUnwind
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
// only POD registration handles; the stable table retains P/WaitToken state
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

// DeadG is a narrow program-driver query. It does not imply that a command
// main may safely return: TerminalG must still prove that no ready or parked G
// survives.
func DeadG(g *G) bool {
	return ValidG(g) && g.state == GDead
}
