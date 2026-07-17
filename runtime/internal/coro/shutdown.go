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
// suspended frame chain and no orphan allocation. The active leaf may be a root
// that has never resumed, or a frame suspended only for scheduler yield. Every
// ancestor must be suspended awaiting its direct child. Parked/opaque states
// are rejected before command shutdown changes P.schedule.
func validCancelableReadyG(g *G) bool {
	if !ValidG(g) || g.state != GRunnable || !g.queued || g.waiting || g.waitToken != nil ||
		g.waitTicket != 0 || g.nextWait != nil || g.runP != nil || g.root == nil || g.active == nil ||
		!releasableParkState(&g.park) || g.park.taskCancelKind != TaskCancelNone ||
		g.pending.kind != pendingNone || g.pending.from != nil || g.pending.target != nil ||
		g.pending.wait != nil || g.pending.ticket != 0 || g.destroyTarget != nil || g.destroyRoot ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		g.taskState != taskStorageOwned || g.taskStorage != unsafe.Pointer(g) || g.taskSize != TaskStorageSize() {
		return false
	}
	gate := preemptLoad(preemptAddress(g))
	if gate != preemptIdle && gate != preemptRequested {
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

	chainCount := 0
	for frame := g.active; frame != nil; frame = frame.parent {
		if !validCancelFrame(frame, g) {
			return false
		}
		chainCount++
		if chainCount > frameCount {
			return false
		}
		if frame == g.active {
			switch frame.state {
			case FrameInitialSuspended:
				if frame.header.SuspendReason != uint16(SuspendNone) ||
					frame.header.Lifecycle != uint16(FrameInitialSuspended) {
					return false
				}
			case FrameSuspended:
				if frame.header.SuspendReason != uint16(SuspendYield) ||
					frame.header.Lifecycle != uint16(FrameSuspended) {
					return false
				}
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
		for frame := g.active; frame != nil; frame = frame.parent {
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
		preemptLoad(&p.executorMode) != executorModeUnbound || p.executor != nil ||
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
	g := p.readyHead
	if g == nil {
		return nil, Action{}, true
	}
	if !validCancelableReadyG(g) {
		return nil, Action{}, false
	}
	if dequeue(p) != g {
		return nil, Action{}, false
	}
	p.current = g
	g.runP = p
	g.state = GCanceling
	action, ok := prepareCancelFrame(p, g, g.active)
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
		preemptLoad(&p.schedule) != scheduleStopping || g.state != GCanceling || g.destroyTarget != nil {
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
	if !wasRoot || g.frames != nil {
		return Action{}, false
	}
	g.destroyRoot = false
	g.root = nil
	preemptStore(preemptAddress(g), preemptDisabled)
	g.state = GDead
	g.runP = nil
	p.current = nil
	p.servicePreemptBudget = 0
	p.action = Action{}
	return Action{Kind: ActionCancelComplete}, true
}

// FinishCommandShutdown disables the sealed P only after every ready child has
// been destroyed/reclaimed. No wait producer can survive a successful v1
// shutdown because BeginCommandShutdown rejected a non-empty wait set.
func FinishCommandShutdown(p *P, main *G) bool {
	if p == nil || !ReclaimableG(main) || main.taskState != taskStorageStatic ||
		preemptLoad(&p.executorMode) != executorModeUnbound || p.executor != nil ||
		p.current != nil || p.inResume || p.action.Kind != ActionInvalid || p.action.Handle != nil ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken || p.servicePreemptBudget != 0 ||
		!validReadyQueue(p) || !validSchedulerWaitQueues(p) || p.readyHead != nil || p.readyTail != nil ||
		!emptySchedulerWaitQueues(p) {
		return false
	}
	return preemptCompareAndSwap(&p.schedule, scheduleStopping, scheduleDisabled)
}
