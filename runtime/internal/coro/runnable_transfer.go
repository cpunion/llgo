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

type runnableTransferGState uint8

const (
	runnableTransferGIdle runnableTransferGState = iota
	runnableTransferGPublished
	runnableTransferGImported
)

// RunnableTransferMailboxCapacity is the fixed target-neutral capacity of one
// runnable migration mailbox. A target profile may shard mailboxes, but it may
// not silently turn this bound into an allocating or unbounded queue.
const RunnableTransferMailboxCapacity uint32 = 8

const runnableTransferMailboxMagic uint32 = 0x52544d31 // "RTM1"

const (
	runnableTransferGateIdle uint32 = iota
	runnableTransferGateHeld
)

type runnableTransferSlotState uint8

const (
	runnableTransferSlotEmpty runnableTransferSlotState = iota
	runnableTransferSlotPublished
)

// RunnableTransferID is an owner-side exact import capability. It is POD and
// generation checked, but it is not a foreign-thread ABI: this first core slice
// exposes producer concurrency only through the mailbox's non-blocking Try
// gate. A callback/ISR ingress still needs its separately designed POD route.
type RunnableTransferID struct {
	Slot       uint32
	Generation uint32
}

// RunnableTransferDrainStatus distinguishes ordinary owner/action deferral and
// producer contention from a broken owner/mailbox invariant. The mailbox gate
// is deliberately Try-only: a producer may be descheduled inside its bounded
// publication transaction, so the scheduler owner must never spin or block
// waiting for that producer.
type RunnableTransferDrainStatus uint8

const (
	RunnableTransferDrainInvalid RunnableTransferDrainStatus = iota
	RunnableTransferDrainOwnerUnstable
	RunnableTransferDrainContended
	RunnableTransferDrainCorrupt
	RunnableTransferDrainComplete
)

// Valid reports whether id can name one mailbox slot generation.
func (id RunnableTransferID) Valid() bool {
	return id.Slot > 0 && id.Slot <= RunnableTransferMailboxCapacity && id.Generation != 0
}

// runnableTransferSlot is the durable owner/transfer wrapper for one P-neutral
// continuation. While Published, g is the only scheduler ownership root: the
// source ready queue has already released it and the destination queue has not
// acquired it. source is diagnostic ownership provenance and is never used to
// resume the continuation.
type runnableTransferSlot struct {
	generation uint32
	state      runnableTransferSlotState
	_          [3]uint8
	source     *P
	g          *G
}

// RunnableTransferMailbox is a destination-sharded fixed durable FIFO with one
// owner P. A single 32-bit atomic Try gate serializes its short metadata
// transaction: distinct source domains may call PublishPNeutralRunnable
// concurrently, while a contended caller returns immediately without spinning
// or mutating its source G/P. The destination uses the same gate for import.
// This is deliberately neither a blocking mutex nor a lock-free sequence ring;
// a target may later replace the ingress with the latter without changing the
// transfer ID, eligibility, or exact owner-side import protocol.
//
// The state machine is durable across host entries: every published G remains
// rooted in exactly one slot until the destination queue accepts it.
//
// This is a phase-1 external ownership wrapper, not a permanent G.ownerP or a
// current-route locator. TaskControl and every other owner-affine lease are
// excluded before publication. After import, ownership is once again proved by
// membership in the destination P's ready queue; arbitrary-G routing remains a
// later layer.
type RunnableTransferMailbox struct {
	magic uint32
	gate  uint32
	owner *P
	head  uint32
	tail  uint32
	count uint32
	slots [RunnableTransferMailboxCapacity]runnableTransferSlot
}

// BindRunnableTransferMailbox binds a zero mailbox to its sole import owner.
// The binding is permanent in this first slice; teardown requires a separately
// designed strong join before owner storage can be reclaimed.
func BindRunnableTransferMailbox(mailbox *RunnableTransferMailbox, owner *P) bool {
	if mailbox == nil || owner == nil || *mailbox != (RunnableTransferMailbox{}) ||
		!stableRunnableTransferP(owner) {
		return false
	}
	mailbox.magic = runnableTransferMailboxMagic
	mailbox.owner = owner
	return true
}

func tryRunnableTransferGate(mailbox *RunnableTransferMailbox) bool {
	return mailbox != nil && mailbox.magic == runnableTransferMailboxMagic && mailbox.owner != nil &&
		preemptCompareAndSwap(&mailbox.gate, runnableTransferGateIdle, runnableTransferGateHeld)
}

func releaseRunnableTransferGate(mailbox *RunnableTransferMailbox) {
	preemptStore(&mailbox.gate, runnableTransferGateIdle)
}

func stableRunnableTransferP(p *P) bool {
	if p == nil || p.current != nil || p.inResume || p.action != (Action{}) ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken || p.servicePreemptBudget != 0 ||
		!validReadyQueueHeader(p) {
		return false
	}
	switch preemptLoad(&p.schedule) {
	case scheduleIdle, scheduleRequested:
		return true
	default:
		return false
	}
}

// validRunnableTransferHeaderLocked is the complete O(1) shared-ring audit used
// in the Try critical section. Exact operations additionally validate only the
// head/tail slot they will mutate. Other published slots were frozen when they
// entered the ring and cannot change before becoming the exact head.
func validRunnableTransferHeaderLocked(mailbox *RunnableTransferMailbox) bool {
	if mailbox == nil || mailbox.magic != runnableTransferMailboxMagic || mailbox.owner == nil ||
		preemptLoad(&mailbox.gate) != runnableTransferGateHeld ||
		mailbox.head >= RunnableTransferMailboxCapacity || mailbox.tail >= RunnableTransferMailboxCapacity ||
		mailbox.count > RunnableTransferMailboxCapacity ||
		mailbox.tail != (mailbox.head+mailbox.count)%RunnableTransferMailboxCapacity {
		return false
	}
	return true
}

// pNeutralRunnable freezes the deliberately narrow first migration class. It
// accepts an initial root, a normal yielded continuation, or a park whose old
// owner already committed a frame-local ResumePacket. No source/result lease,
// control endpoint, panic, spawn, or pin ownership may follow the G.
func pNeutralRunnable(g *G, queued bool) bool {
	return pNeutralRunnableHeader(g, queued) && pNeutralFrameChain(g)
}

func pNeutralRunnableParkState(state *ParkState) bool {
	if !validParkState(state) {
		return false
	}
	switch state.phase {
	case parkIdle:
		return *state == (ParkState{})
	case parkMaterialized:
		return true
	default:
		return false
	}
}

// initialPNeutralRunnable recognizes the one safe single-runnable sharing
// exception without a target-owned spawn hint. A never-run frame cannot have
// bounced from another P; after its first physical action it becomes yielded,
// parked, or terminal and must satisfy the ordinary surplus rule.
func initialPNeutralRunnable(g *G, queued bool) bool {
	return pNeutralRunnable(g, queued) && g.active == g.root &&
		g.active.state == FrameInitialSuspended &&
		g.active.header.SuspendReason == uint16(SuspendNone) &&
		g.active.header.Lifecycle == uint16(FrameInitialSuspended)
}

// pNeutralRunnableHeader is the O(1) revalidation performed after the mailbox
// Try gate succeeds. The source owner completed the full queue/frame audit
// immediately before the CAS; no other scheduler may mutate this G, while an
// asynchronous RequestPreempt is ordered by the later idle-to-disabled CAS.
func pNeutralRunnableHeader(g *G, queued bool) bool {
	wantTransfer, wantPreempt := runnableTransferGIdle, preemptIdle
	if !queued {
		wantTransfer, wantPreempt = runnableTransferGPublished, preemptDisabled
	}
	if !ValidG(g) || g.state != GRunnable || g.queued != queued ||
		(!queued && g.nextReady != nil) || g.transferState != wantTransfer ||
		g.runnableAffinity != runnableAnyOwner ||
		!gPreemptStateAtDepthZero(g, wantPreempt) ||
		g.taskControlLeases != 0 || g.runAction != ActionInvalid || g.osThreadLockDepth != 0 ||
		g.pending != (pendingTransition{}) || g.destroyTarget != nil || g.destroyRoot ||
		g.waiting || g.runP != nil ||
		!pNeutralRunnableParkState(&g.park) || g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		!validLiveTaskStorage(g) || !emptyPanicRecord(&g.panicRecord) || g.panicUnwind {
		return false
	}
	active := g.active
	if g.root == nil || active == nil || g.frames != active || g.root.owner != g ||
		g.root.parent != nil || g.root.header == nil || g.root.header.Parent != nil ||
		active.owner != g || active.handle == nil || active.header == nil ||
		active.header.G != unsafe.Pointer(g) || active.header.Flags != 0 ||
		active.parkWait != nil || !emptyCompletionRecord(&active.completion) ||
		active.state != FrameState(active.header.Lifecycle) {
		return false
	}
	initial := active == g.root && active.state == FrameInitialSuspended &&
		active.header.SuspendReason == uint16(SuspendNone) &&
		active.header.Lifecycle == uint16(FrameInitialSuspended)
	yielded := active.state == FrameSuspended &&
		active.header.SuspendReason == uint16(SuspendYield) &&
		active.header.Lifecycle == uint16(FrameSuspended)
	materialized := active.state == FrameSuspended &&
		active.header.SuspendReason == uint16(SuspendPark) &&
		active.header.Lifecycle == uint16(FrameSuspended) &&
		g.park.phase == parkMaterialized
	return initial || yielded || materialized
}

func pNeutralFrameChain(g *G) bool {
	// A stable runnable task has no unattached allocation: the LIFO frame list
	// is the active-to-root parent chain. Audit the chain before following it.
	for slow, fast := g.active, g.active; fast != nil && fast.parent != nil; {
		slow = slow.parent
		fast = fast.parent.parent
		if slow == fast {
			return false
		}
	}

	for frame := g.active; frame != nil; frame = frame.parent {
		if frame.owner != g || frame.handle == nil || frame.header == nil || frame.next != frame.parent ||
			frame.header.G != unsafe.Pointer(g) || frame.header.Flags != 0 ||
			frame.parkWait != nil || !emptyCompletionRecord(&frame.completion) ||
			frame.state != FrameState(frame.header.Lifecycle) {
			return false
		}
		if frame.parent == nil {
			if frame != g.root || frame.header.Parent != nil {
				return false
			}
		} else if frame.parent.owner != g || frame.parent.handle == nil ||
			frame.header.Parent != frame.parent.handle {
			return false
		}
		if frame != g.active && (frame.state != FrameSuspended || frame.header == nil ||
			frame.header.SuspendReason != uint16(SuspendCall) ||
			frame.header.Lifecycle != uint16(FrameSuspended)) {
			return false
		}
	}

	return true
}

// preflightPNeutralRunnablePublish performs the potentially linear source
// queue/frame audit before entering the destination's short Try section.
func preflightPNeutralRunnablePublish(mailbox *RunnableTransferMailbox, source *P, g *G) bool {
	return mailbox != nil && mailbox.magic == runnableTransferMailboxMagic && mailbox.owner != nil &&
		source != nil && source != mailbox.owner && stableRunnableTransferP(source) &&
		validReadyQueue(source) && source.readyHead == g && pNeutralRunnable(g, true)
}

func publishPNeutralRunnableLocked(mailbox *RunnableTransferMailbox, source *P, g *G) (RunnableTransferID, bool) {
	if !validRunnableTransferHeaderLocked(mailbox) || source == nil || source == mailbox.owner ||
		!stableRunnableTransferP(source) || source.readyHead != g ||
		!pNeutralRunnableHeader(g, true) || mailbox.count == RunnableTransferMailboxCapacity {
		return RunnableTransferID{}, false
	}
	slotIndex := mailbox.tail
	slot := &mailbox.slots[slotIndex]
	if slot.state != runnableTransferSlotEmpty || slot.source != nil || slot.g != nil ||
		slot.generation == ^uint32(0) {
		return RunnableTransferID{}, false
	}
	generation := slot.generation + 1

	// All ordinary validation is complete. Disable the pointer-only preemption
	// entry and mark the transfer while source still owns g in its ready queue:
	// queued blocks a stray Enqueue before the mark, and the mark blocks it after
	// dequeue. A racing preemption request fails publication before either owner
	// field changes. Under the source-P owner contract dequeue must now return this
	// exact head, so no fallible operation remains.
	if !compareAndSwapGPreemptStateAtDepthZero(g, preemptIdle, preemptDisabled) {
		return RunnableTransferID{}, false
	}
	g.transferState = runnableTransferGPublished
	removed := dequeue(source)
	if removed != g {
		// This is unreachable without violating source-owner serialization. Undo
		// our marker/gate and restore any head we did remove; no reported publish
		// failure is allowed to leave a hidden transfer state behind.
		if removed != nil {
			prependReadyUnchecked(source, removed)
		}
		g.transferState = runnableTransferGIdle
		if !compareAndSwapGPreemptStateAtDepthZero(g, preemptDisabled, preemptIdle) {
			return RunnableTransferID{}, false
		}
		return RunnableTransferID{}, false
	}

	slot.generation = generation
	slot.source = source
	slot.g = g
	slot.state = runnableTransferSlotPublished
	mailbox.tail = (mailbox.tail + 1) % RunnableTransferMailboxCapacity
	mailbox.count++
	return RunnableTransferID{Slot: slotIndex + 1, Generation: generation}, true
}

// PublishPNeutralRunnable transfers the exact source ready head into one
// durable slot. It performs the source-owner audit first, then makes one
// non-blocking CAS attempt to enter the destination transaction. Contention,
// full capacity, wrong ownership, and ineligibility leave source unchanged.
func PublishPNeutralRunnable(mailbox *RunnableTransferMailbox, source *P, g *G) (RunnableTransferID, bool) {
	if !preflightPNeutralRunnablePublish(mailbox, source, g) || !tryRunnableTransferGate(mailbox) {
		return RunnableTransferID{}, false
	}
	id, ok := publishPNeutralRunnableLocked(mailbox, source, g)
	releaseRunnableTransferGate(mailbox)
	return id, ok
}

func importPNeutralRunnableLocked(mailbox *RunnableTransferMailbox, owner *P, id RunnableTransferID) bool {
	if !validRunnableTransferHeaderLocked(mailbox) || owner == nil || owner != mailbox.owner ||
		!stableRunnableTransferP(owner) || !id.Valid() || mailbox.count == 0 ||
		id.Slot-1 != mailbox.head {
		return false
	}
	slot := &mailbox.slots[mailbox.head]
	if slot.state != runnableTransferSlotPublished || slot.generation != id.Generation ||
		slot.source == nil || slot.source == owner || !pNeutralRunnableHeader(slot.g, false) {
		return false
	}

	// Every fallible check is complete. Publish destination queue ownership,
	// clear the transfer marker, and enable the pointer-only preemption gate
	// before releasing the mailbox root. Ordinary Enqueue deliberately rejects
	// Published, so only this owner-side handoff may cross that state.
	g := slot.g
	appendReadyUnchecked(owner, g)
	g.transferState = runnableTransferGImported
	if !compareAndSwapGPreemptStateAtDepthZero(g, preemptDisabled, preemptIdle) {
		return false
	}
	slot.state = runnableTransferSlotEmpty
	slot.source = nil
	slot.g = nil
	mailbox.head = (mailbox.head + 1) % RunnableTransferMailboxCapacity
	mailbox.count--
	return true
}

// ImportPNeutralRunnable imports one exact FIFO head generation. Reusing id
// after success is rejected, so a duplicate host/domain delivery cannot enqueue
// the same G twice.
func ImportPNeutralRunnable(mailbox *RunnableTransferMailbox, owner *P, id RunnableTransferID) bool {
	if mailbox == nil || mailbox.magic != runnableTransferMailboxMagic || mailbox.owner != owner || owner == nil ||
		!id.Valid() || !stableRunnableTransferP(owner) || !validReadyQueue(owner) ||
		!tryRunnableTransferGate(mailbox) {
		return false
	}
	ok := importPNeutralRunnableLocked(mailbox, owner, id)
	releaseRunnableTransferGate(mailbox)
	return ok
}

// TryDrainPNeutralRunnables imports at most budget FIFO entries on the sole
// owner P. budget is itself capped by the static mailbox capacity so every
// successful transaction has a target-independent upper bound. Contended is
// an ordinary retry result and never implies that mailbox metadata was read
// without its gate; Complete is the only status for which more is meaningful.
func TryDrainPNeutralRunnables(
	mailbox *RunnableTransferMailbox,
	owner *P,
	budget uint32,
) (moved uint32, more bool, status RunnableTransferDrainStatus) {
	if mailbox == nil || mailbox.magic != runnableTransferMailboxMagic || mailbox.owner != owner || owner == nil ||
		budget == 0 || budget > RunnableTransferMailboxCapacity {
		return 0, false, RunnableTransferDrainInvalid
	}
	if !stableRunnableTransferP(owner) || !validReadyQueue(owner) {
		return 0, false, RunnableTransferDrainOwnerUnstable
	}
	if !tryRunnableTransferGate(mailbox) {
		return 0, false, RunnableTransferDrainContended
	}
	if !validRunnableTransferHeaderLocked(mailbox) || owner != mailbox.owner ||
		!stableRunnableTransferP(owner) {
		releaseRunnableTransferGate(mailbox)
		return 0, false, RunnableTransferDrainCorrupt
	}
	for moved < budget && mailbox.count != 0 {
		slot := &mailbox.slots[mailbox.head]
		id := RunnableTransferID{Slot: mailbox.head + 1, Generation: slot.generation}
		if !importPNeutralRunnableLocked(mailbox, owner, id) {
			more = mailbox.count != 0
			releaseRunnableTransferGate(mailbox)
			return moved, more, RunnableTransferDrainCorrupt
		}
		moved++
	}
	more = mailbox.count != 0
	releaseRunnableTransferGate(mailbox)
	return moved, more, RunnableTransferDrainComplete
}

// DrainPNeutralRunnables is the compatibility bool wrapper. New scheduler
// owners should use TryDrainPNeutralRunnables so ordinary producer contention
// cannot be mistaken for an invariant failure.
func DrainPNeutralRunnables(mailbox *RunnableTransferMailbox, owner *P, budget uint32) (moved uint32, more bool, ok bool) {
	moved, more, status := TryDrainPNeutralRunnables(mailbox, owner, budget)
	return moved, more, status == RunnableTransferDrainComplete
}
