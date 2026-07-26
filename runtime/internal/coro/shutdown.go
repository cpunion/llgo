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

// CommandMainReturnPoint validates the scheduler episode in which the
// compiler's normal-main continuation may publish the main-return marker. It
// never mutates queues or frames while llvm.coro.resume is active.
func CommandMainReturnPoint(p *P, main *G) bool {
	current, ok := runningSpawnContext(main)
	return ok && current == p && main.spawnChild == nil
}

func validCancelFrame(frame *Frame, g *G) bool {
	return frame != nil && frame.owner == g && frame.handle != nil && frame.header != nil &&
		frame.storage != nil && frame.rawBase != nil && frame.descriptor != nil &&
		frame.header.G == unsafe.Pointer(g) && frame.header.Descriptor == frame.descriptor &&
		frame.header.AllocationBase == frame.rawBase
}

// validCancelableReadyG proves that a ready G contains exactly one structured
// suspended frame chain and no orphan allocation. In addition to the legacy
// initial/yield boundary, command shutdown accepts the three stable physical
// continuations that the bounded runner may leave at the ready tail:
//
//   - CheckResume owns either a newly-created initial frame or an await parent
//     whose completed child has already been destroyed;
//   - CheckDestroy owns the final-suspended active frame before its first
//     physical destroy; and
//   - PanicDestroy owns a suspended-await ancestor after a deeper panic frame
//     has already been destroyed.
//
// No action has started at these boundaries. NextCommandCancel consumes the
// continuation without calling done/resume and reuses an existing destroy
// target exactly once. Parked/opaque states are rejected before command
// shutdown changes P.schedule.
func validCancelableReadyG(g *G) bool {
	if !ValidG(g) || g.state != GRunnable || !g.queued || g.waiting || g.runP != nil || g.root == nil ||
		!releasableParkState(&g.park) || g.park.taskCancelKind != TaskCancelNone ||
		g.pending.kind != pendingNone || g.pending.from != nil || g.pending.target != nil ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		g.taskControlLeases != 0 ||
		g.taskState != taskStorageOwned || g.taskStorage != unsafe.Pointer(g) || g.taskSize != TaskStorageSize() {
		return false
	}
	if !gPreemptEnabledAtDepthZero(g) {
		return false
	}

	// Validate the allocation list independently, including cycle freedom.
	for slow, fast := g.frames, g.frames; fast != nil && fast.next != nil; {
		slow = slow.next
		fast = fast.next.next
		if slow == fast {
			return false
		}
	}
	frameCount := 0
	for frame := g.frames; frame != nil; frame = frame.next {
		if !validCancelFrame(frame, g) {
			return false
		}
		frameCount++
	}
	if frameCount == 0 {
		return false
	}

	leaf := g.active
	continuation := g.runAction
	switch continuation {
	case ActionInvalid:
		if leaf == nil || g.destroyTarget != nil || g.destroyRoot || g.panicUnwind ||
			!emptyPanicRecord(&g.panicRecord) {
			return false
		}
	case ActionCheckResume:
		if leaf == nil || g.destroyTarget != nil || g.destroyRoot || g.panicUnwind ||
			!emptyPanicRecord(&g.panicRecord) {
			return false
		}
	case ActionCheckDestroy, ActionPanicDestroy:
		leaf = g.destroyTarget
		if leaf == nil || g.active != leaf.parent || g.destroyRoot != (leaf == g.root) ||
			leaf.state != FrameDestroyPending || leaf.header == nil ||
			leaf.header.Lifecycle != uint16(FrameDestroyPending) {
			return false
		}
		if continuation == ActionPanicDestroy {
			if !g.panicUnwind || !publishedPanicRecord(&g.panicRecord) ||
				leaf.header.SuspendReason != uint16(SuspendCall) {
				return false
			}
		} else if g.panicUnwind {
			// The first destroy of a panicking G still uses CheckDestroy. Later
			// suspended-await ancestors use PanicDestroy.
			if !publishedPanicRecord(&g.panicRecord) ||
				leaf.header.SuspendReason != uint16(SuspendPanic) {
				return false
			}
		} else if !emptyPanicRecord(&g.panicRecord) ||
			leaf.header.SuspendReason != uint16(SuspendFrameComplete) {
			return false
		}
	default:
		return false
	}

	chainCount := 0
	for frame := leaf; frame != nil; frame = frame.parent {
		if !validCancelFrame(frame, g) {
			return false
		}
		chainCount++
		if chainCount > frameCount {
			return false
		}
		if frame == leaf {
			switch continuation {
			case ActionInvalid:
				if frame.state == FrameInitialSuspended {
					if frame.header.SuspendReason != uint16(SuspendNone) ||
						frame.header.Lifecycle != uint16(FrameInitialSuspended) {
						return false
					}
				} else if frame.state != FrameSuspended ||
					frame.header.SuspendReason != uint16(SuspendYield) ||
					frame.header.Lifecycle != uint16(FrameSuspended) {
					return false
				}
			case ActionCheckResume:
				if frame.state == FrameInitialSuspended {
					if frame.header.SuspendReason != uint16(SuspendNone) ||
						frame.header.Lifecycle != uint16(FrameInitialSuspended) {
						return false
					}
				} else if frame.state != FrameSuspended ||
					frame.header.SuspendReason != uint16(SuspendCall) ||
					frame.header.Lifecycle != uint16(FrameSuspended) {
					return false
				}
			case ActionCheckDestroy, ActionPanicDestroy:
				// The exact destroy-target shape was checked before traversal.
			default:
				return false
			}
		} else if frame.state != FrameSuspended || frame.header.SuspendReason != uint16(SuspendCall) ||
			frame.header.Lifecycle != uint16(FrameSuspended) {
			return false
		}
		if frame.parent == nil {
			if frame != g.root || frame.header.Parent != nil {
				return false
			}
		} else if frame.header.Parent != frame.parent.handle {
			return false
		}
	}
	if chainCount != frameCount {
		return false
	}
	// Count equality plus unique parent traversal is not sufficient if the
	// allocation list repeats a chain member through corruption. Prove exact
	// membership without allocating a map.
	for listed := g.frames; listed != nil; listed = listed.next {
		matches := 0
		for frame := leaf; frame != nil; frame = frame.parent {
			if listed == frame {
				matches++
			}
		}
		if matches != 1 {
			return false
		}
	}
	return true
}

// BeginCommandShutdown atomically seals a command P against new scheduling
// requests after main has returned normally. Version one supports only ready
// YieldOnly/AwaitStructured children. Any wait/current/action state is rejected
// before the schedule gate changes. Stable wait registration now provides a
// safe close/quiesce primitive, but P does not yet own a registry enumeration
// or platform-specific unregister callback for command-wide cancellation.
func BeginCommandShutdown(p *P, main *G) bool {
	if p == nil || !ReclaimableG(main) || main.taskState != taskStorageStatic ||
		preemptLoad(&p.executorMode) != executorModeUnbound || p.executor != nil || p.channelSource != nil ||
		p.current != nil || p.inResume || p.action.Kind != ActionInvalid || p.action.Handle != nil ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken || p.servicePreemptBudget != 0 ||
		!validReadyQueue(p) || !validSchedulerWaitQueues(p) || !emptySchedulerWaitQueues(p) {
		return false
	}
	for g := p.readyHead; g != nil; g = g.nextReady {
		if !validCancelableReadyG(g) {
			return false
		}
	}
	for {
		schedule := preemptLoad(&p.schedule)
		if schedule != scheduleIdle && schedule != scheduleRequested {
			return false
		}
		if preemptCompareAndSwap(&p.schedule, schedule, scheduleStopping) {
			return true
		}
	}
}

// RequestCommandShutdownDrain publishes task-shutdown cancellation while the
// executor and its typed event sources are still bound. A selected channel,
// timer, or I/O result may retain frame-owned cleanup storage until the
// compiler resume gate consumes it; closing the executor before that gate
// would either leak the source slot or leave a backend queue pointing into a
// destroyed coroutine frame.
//
// The caller remains the scheduler owner. Ready CheckResume continuations are
// canceled here; already-prepared destroy continuations contain no user code
// and are allowed to advance until their next resume gate. Parked V2 waits are
// canceled through their ordinary affected-wait transaction, so the unified
// source poll performs the same detach/apply/promotion sequence as an external
// completion. No target callback or interface value is introduced.
//
// needed is true when at least one non-main task must cross this pre-close
// drain. A false needed result proves that command shutdown can proceed to the
// executor close transaction without running another task.
func RequestCommandShutdownDrain(p *P, main *G) (needed, ok bool) {
	if p == nil || main == nil || !ReclaimableG(main) || main.taskState != taskStorageStatic ||
		preemptLoad(&p.executorMode) != executorModeBound || p.executor == nil ||
		p.current != nil || p.inResume || p.action != (Action{}) ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken || p.servicePreemptBudget != 0 ||
		!validReadyQueue(p) || !validSchedulerWaitQueues(p) {
		return false, false
	}
	// Preserve the existing allocation-free direct-destroy path when every
	// remaining child is already at a source-independent shutdown boundary.
	// Enter the pre-close runner only when some wait or delivered result still
	// owns source-specific frame cleanup.
	for g := p.readyHead; g != nil; g = g.nextReady {
		if g == main {
			return false, false
		}
		needed = needed || !validCancelableReadyG(g)
	}
	needed = needed || p.parkWaitHead != nil
	if !needed {
		return false, true
	}
	request := func(g *G, resumeGate bool) bool {
		if g == nil || g == main {
			return false
		}
		if !resumeGate {
			return true
		}
		return RequestTaskCancellation(p, g, TaskCancelShutdown)
	}
	for g := p.readyHead; g != nil; g = g.nextReady {
		switch g.runAction {
		case ActionInvalid, ActionCheckResume:
			if !request(g, true) {
				return false, false
			}
		case ActionCheckDestroy, ActionPanicDestroy:
			// The physical destroy itself cannot execute user code. Do not put a
			// Requested token in front of it: BeginRunG deliberately blocks that
			// combination until a compiler resume gate can own cleanup.
			if !request(g, false) {
				return false, false
			}
		default:
			return false, false
		}
	}
	for record := p.parkWaitHead; record != nil; record = record.activeNext {
		if !request(record.g, true) {
			return false, false
		}
	}
	return needed, true
}

func prepareCancelFrame(p *P, g *G, frame *Frame) (Action, bool) {
	if p == nil || g == nil || frame == nil || p.current != g || g.state != GCanceling ||
		g.destroyTarget != nil || !validCancelFrame(frame, g) ||
		(frame.state != FrameInitialSuspended && frame.state != FrameSuspended) {
		return Action{}, false
	}
	handle := frame.handle
	g.active = frame.parent
	g.destroyRoot = frame == g.root
	frame.state = FrameDestroyPending
	frame.header.Lifecycle = uint16(FrameDestroyPending)
	g.destroyTarget = frame
	return setAction(p, ActionCancelDestroy, handle)
}

// NextCommandCancel removes one ready child in FIFO order and requests direct
// destruction of its deepest suspended frame. An empty ready queue returns
// (nil, ActionInvalid, true).
func NextCommandCancel(p *P) (*G, Action, bool) {
	if p == nil || preemptLoad(&p.schedule) != scheduleStopping || p.current != nil ||
		p.inResume || p.action.Kind != ActionInvalid || p.action.Handle != nil ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken || p.servicePreemptBudget != 0 ||
		!validReadyQueue(p) || !validSchedulerWaitQueues(p) || !emptySchedulerWaitQueues(p) {
		return nil, Action{}, false
	}
	g := nextOSThreadRunnable(p)
	if g == nil {
		return nil, Action{}, p.readyHead == nil
	}
	if !validCancelableReadyG(g) {
		return nil, Action{}, false
	}
	if dequeueOSThreadRunnable(p) != g {
		return nil, Action{}, false
	}
	continuation := g.runAction
	target := g.destroyTarget
	g.runAction = ActionInvalid
	p.current = g
	g.runP = p
	g.state = GCanceling
	var action Action
	var ok bool
	switch continuation {
	case ActionInvalid, ActionCheckResume:
		action, ok = prepareCancelFrame(p, g, g.active)
	case ActionCheckDestroy, ActionPanicDestroy:
		// The bounded runner has not executed this physical destroy. Preserve
		// the exact already-prepared target and issue it once as command cancel;
		// no done check or coroutine resume is needed or permitted.
		if target == nil || target != g.destroyTarget || target.handle == nil ||
			target.state != FrameDestroyPending {
			return nil, Action{}, false
		}
		action, ok = setAction(p, ActionCancelDestroy, target.handle)
	default:
		return nil, Action{}, false
	}
	if !ok {
		return nil, Action{}, false
	}
	return g, action, true
}

// CancelDestroyed commits the return from one direct llvm.coro.destroy. The
// compiler free hook must already have unlinked the destroyed frame. Ancestors
// are destroyed deepest-to-root without coro.done and without resume.
func CancelDestroyed(p *P, g *G, action Action) (Action, bool) {
	if !expectedAction(p, g, action, ActionCancelDestroy) || p.inResume ||
		preemptLoad(&p.schedule) != scheduleStopping || g.state != GCanceling || g.destroyTarget != nil ||
		!gPreemptEnabledAtDepthZero(g) {
		return Action{}, false
	}
	wasRoot := g.destroyRoot
	if g.active != nil {
		if wasRoot {
			return Action{}, false
		}
		g.destroyRoot = false
		return prepareCancelFrame(p, g, g.active)
	}
	if !wasRoot || g.frames != nil || g.runAction != ActionInvalid ||
		g.transferState != runnableTransferGIdle || g.taskControlLeases != 0 {
		return Action{}, false
	}
	if g.panicUnwind {
		// A normal command-main return terminates the process without waiting for
		// background goroutines. If it wins before a child panic is reported, the
		// child is command-canceled and its retained panic payload is discarded
		// only after every child frame has been physically destroyed.
		if !publishedPanicRecord(&g.panicRecord) {
			return Action{}, false
		}
		g.panicRecord.typeWord = nil
		g.panicRecord.dataWord = nil
		preemptStore(&g.panicRecord.status, uint32(ExplicitStatusNone))
		g.panicUnwind = false
	} else if !emptyPanicRecord(&g.panicRecord) {
		return Action{}, false
	}
	retireOwner, released := releaseOSThreadLockForExit(p, g)
	if !released || !disableGPreempt(g) {
		return Action{}, false
	}
	g.destroyRoot = false
	g.root = nil
	g.state = GDead
	g.runP = nil
	p.current = nil
	p.servicePreemptBudget = 0
	p.action = Action{}
	return Action{
		Kind:  ActionCancelComplete,
		Flags: physicalOwnerRetireFlags(retireOwner),
	}, true
}

// FinishCommandShutdown disables the sealed P only after every ready child has
// been destroyed/reclaimed. No wait producer can survive a successful v1
// shutdown because BeginCommandShutdown rejected a non-empty wait set.
func FinishCommandShutdown(p *P, main *G) bool {
	if p == nil || !ReclaimableG(main) || main.taskState != taskStorageStatic ||
		preemptLoad(&p.executorMode) != executorModeUnbound || p.executor != nil || p.channelSource != nil ||
		p.current != nil || p.inResume || p.action.Kind != ActionInvalid || p.action.Handle != nil ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken || p.servicePreemptBudget != 0 ||
		!validReadyQueue(p) || !validSchedulerWaitQueues(p) || p.readyHead != nil || p.readyTail != nil ||
		!emptySchedulerWaitQueues(p) {
		return false
	}
	return preemptCompareAndSwap(&p.schedule, scheduleStopping, scheduleDisabled)
}
