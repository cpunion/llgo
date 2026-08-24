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

// InlineAwaitDisposition is the target-neutral scheduler half of one eager
// child resume. Generated code owns llvm.coro.resume, llvm.coro.done, and
// llvm.coro.destroy so LLVM can see the exact static handle lifetime; this core
// owns only frame, completion, and scheduler state.
type InlineAwaitDisposition uint8

const (
	InlineAwaitInvalid InlineAwaitDisposition = iota
	// InlineAwaitDeclined is a valid ordinary pendingAwait left untouched at
	// the bounded native-resume depth. Generated code must take its normal
	// llvm.coro.suspend edge.
	InlineAwaitDeclined
	InlineAwaitStarted
	// InlineAwaitSuspend means the child or one of its descendants published a
	// real await/yield/park transition. Generated parents unwind their native
	// resume calls by suspending; the outer scheduler consumes the deepest
	// transition after the native stack is gone.
	InlineAwaitSuspend
	// InlineAwaitDestroy means the immediate child reached final suspend and
	// is now the exact destroy target. Generated code must destroy it and then
	// commit the physical/logical destroy receipt before continuing.
	InlineAwaitDestroy
)

// maxInlineAwaitDepth bounds temporary native resume nesting. Real suspended
// state remains entirely in LLVM frames; reaching this bound simply takes the
// pre-existing scheduler path. Sixteen is deliberately conservative for
// embedded host stacks and still collapses ordinary shallow Go call chains.
const maxInlineAwaitDepth uint8 = 16

// PrepareInlineAwaitCompiler atomically arms one compiler-owned child
// completion and selects the bounded eager-resume path. The old two-hook form
// authenticated the same immutable caller/child edge twice even though no
// callback, suspension, or scheduler entry exists between prepare and begin.
// This helper performs one shared proof and commits either pendingInlineStart
// or the canonical depth-bound pendingAwait transaction.
func PrepareInlineAwaitCompiler(
	g *G, parentHandle, childHandle, recoverType, recoverData unsafe.Pointer,
) InlineAwaitDisposition {
	// The ordinary compiler lane is adjacent to the child ramp's successful
	// PublishFrameV3Compiler return. The active parent, newest initial child,
	// and already-consumed resume decision are therefore one private phase
	// receipt: no scheduler, callback, or producer can enter between them. Keep
	// the capability, exact frame edge, and depth proof here, but do not replay
	// the unrelated park/spawn/source audits already established at the outer
	// resume boundary. Recovery retains the complete lane below because its
	// payload is a separately supplied language transaction.
	if recoverType == nil && recoverData == nil && ValidG(g) &&
		parentHandle != nil && childHandle != nil && g.runP != nil &&
		g.pending.kind == pendingNone && g.destroyTarget == nil && !g.destroyRoot {
		p := g.runP
		parent, child := g.active, g.frames
		preempt := preemptLoad(&g.preempt)
		if p.current == g && p.inResume && g.state == GRunning &&
			p.runDecisionTaken && p.runDecision == (RunDecision{}) &&
			p.action.Kind == ActionResume && p.action.Flags == 0 &&
			(preempt == preemptIdle || preempt == preemptRequested) &&
			parent != nil && child != nil && parent != child &&
			parent.handle == parentHandle && child.handle == childHandle &&
			parent.owner == g && child.owner == g &&
			parent.header != nil && child.header != nil &&
			parent.state == FrameActive && child.state == FrameInitialSuspended &&
			parent.header.SuspendReason == uint16(SuspendCall) &&
			parent.header.Lifecycle == uint16(FrameSuspended) &&
			child.header.Parent == parentHandle &&
			child.header.Lifecycle == uint16(FrameInitialSuspended) &&
			child.parent == nil && emptyCompletionRecord(&parent.completion) {
			depth := p.inlineAwaitDepth
			depthOK := depth <= maxInlineAwaitDepth
			if depthOK {
				if depth == 0 {
					depthOK = p.action.Handle == parentHandle
				} else {
					outer := parent.parent
					depthOK = outer != nil && outer.completion.child == parentHandle &&
						outer.state == FrameSuspended
				}
			}
			if depthOK {
				parent.completion.child = childHandle
				parent.completion.status = completionArmed
				child.parent = parent
				if depth >= maxInlineAwaitDepth {
					g.pending = pendingTransition{kind: pendingAwait, from: parent, target: child}
					return InlineAwaitDeclined
				}
				parent.state = FrameSuspended
				child.state = FrameActive
				g.active = child
				g.pending = pendingTransition{kind: pendingInlineStart, from: parent, target: child}
				p.inlineAwaitDepth++
				return InlineAwaitStarted
			}
		}
	}
	if ValidG(g) && parentHandle != nil && childHandle != nil &&
		recoverType != nil && resumeGateTaken(g) &&
		g.runP != nil && g.pending.kind == pendingNone &&
		g.destroyTarget == nil && !g.destroyRoot &&
		g.spawnChild == nil && g.spawnParent == nil && g.spawnP == nil &&
		!g.waiting && compilerReleasableParkState(&g.park) {
		p := g.runP
		parent, child := g.active, g.frames
		if parent != nil && child != nil && parent != child &&
			parent.handle == parentHandle && child.handle == childHandle &&
			parent.owner == g && child.owner == g &&
			parent.header != nil && child.header != nil &&
			parent.state == FrameActive && child.state == FrameInitialSuspended &&
			parent.header.SuspendReason == uint16(SuspendCall) &&
			parent.header.Lifecycle == uint16(FrameSuspended) &&
			child.header.Parent == parentHandle &&
			child.header.Lifecycle == uint16(FrameInitialSuspended) &&
			child.parent == nil && emptyCompletionRecord(&parent.completion) &&
			compilerInlineAwaitParentDepth(g, parent, p.inlineAwaitDepth) {
			parent.completion.child = child.handle
			if recoverType == nil {
				parent.completion.status = completionArmed
			} else {
				parent.completion.status = completionRecoverArmed
				parent.completion.typeWord = recoverType
				parent.completion.dataWord = recoverData
			}
			child.parent = parent
			if p.inlineAwaitDepth >= maxInlineAwaitDepth {
				g.pending = pendingTransition{kind: pendingAwait, from: parent, target: child}
				return InlineAwaitDeclined
			}
			parent.state = FrameSuspended
			child.state = FrameActive
			g.active = child
			g.pending = pendingTransition{kind: pendingInlineStart, from: parent, target: child}
			p.inlineAwaitDepth++
			return InlineAwaitStarted
		}
	}

	var prepared bool
	if recoverType == nil {
		if recoverData != nil {
			return InlineAwaitInvalid
		}
		prepared = PrepareAwaitCompletionCompiler(g, parentHandle, childHandle)
	} else {
		prepared = PrepareAwaitCompletionRecover(
			g, parentHandle, childHandle, recoverType, recoverData,
		)
	}
	if !prepared {
		return InlineAwaitInvalid
	}
	return BeginInlineAwaitCompiler(g, parentHandle, childHandle)
}

func validInlineAwaitEdge(parent, child *Frame) bool {
	if parent == nil || child == nil || parent == child || parent.handle == nil ||
		child.handle == nil || parent.header == nil || child.header == nil ||
		child.parent != parent || child.header.Parent != parent.handle ||
		parent.state != FrameSuspended ||
		parent.header.SuspendReason != uint16(SuspendCall) ||
		parent.header.Lifecycle != uint16(FrameSuspended) {
		return false
	}
	record := &parent.completion
	if record.child != child.handle {
		return false
	}
	switch record.status {
	case completionArmed:
		return record.typeWord == nil && record.dataWord == nil
	case completionRecoverArmed, completionRecoverTaken:
		return record.typeWord != nil
	default:
		return false
	}
}

// inlineAwaitChildBelow proves that leaf is executing beneath root through
// exact parent-owned completion records and returns the unique child directly
// below root. root may be the scheduler's physical ActionResume frame or the
// immediate parent of one runtime inline call.
func inlineAwaitChildBelow(leaf, root *Frame) *Frame {
	if leaf == nil || root == nil || leaf == root {
		return nil
	}
	for child := leaf; child != nil && child.parent != nil; child = child.parent {
		parent := child.parent
		if !validInlineAwaitEdge(parent, child) {
			return nil
		}
		if parent == root {
			return child
		}
	}
	return nil
}

func validInlineAwaitAncestry(leaf, root *Frame) bool {
	return inlineAwaitChildBelow(leaf, root) != nil
}

// validInlineAwaitParentDepth checks the O(1) inductive edge owned by one
// outstanding native inline call. Depth zero binds directly to ActionResume;
// deeper calls validate their immediate outer completion edge. Full ancestry
// is reserved for real suspension/event and foreign-handoff boundaries, not
// charged to every synchronous managed call.
func validInlineAwaitParentDepth(g *G, frame *Frame, depth uint8) bool {
	if !ValidG(g) || g.runP == nil || frame == nil || depth > maxInlineAwaitDepth {
		return false
	}
	p := g.runP
	if !expectedAction(p, g, p.action, ActionResume) {
		return false
	}
	if depth == 0 {
		return frame.handle == p.action.Handle
	}
	return frame.parent != nil && validInlineAwaitEdge(frame.parent, frame)
}

// compilerInlineAwaitParentDepth consumes the inductive edge owned by the
// immediately enclosing compiler hook. The complete ancestry walk remains at
// real suspension, event, and foreign-handoff boundaries.
func compilerInlineAwaitParentDepth(g *G, frame *Frame, depth uint8) bool {
	if g == nil || g.runP == nil || frame == nil || depth > maxInlineAwaitDepth {
		return false
	}
	p := g.runP
	if depth == 0 {
		return p.action.Kind == ActionResume && p.action.Flags == 0 &&
			p.action.Handle == frame.handle
	}
	parent := frame.parent
	return parent != nil && parent.completion.child == frame.handle &&
		parent.state == FrameSuspended
}

// BeginInlineAwait consumes one already validated pendingAwait and activates
// its initial-suspended child inside the current physical resume episode. A
// depth refusal does not mutate the ordinary scheduler transaction.
func BeginInlineAwait(g *G, parentHandle, childHandle unsafe.Pointer) InlineAwaitDisposition {
	if !ValidG(g) || parentHandle == nil || childHandle == nil || !resumeGateTaken(g) ||
		g.runP == nil || g.destroyTarget != nil || g.destroyRoot ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		g.waiting || !releasableParkState(&g.park) {
		return InlineAwaitInvalid
	}
	p := g.runP
	pending := g.pending
	parent, child := pending.from, pending.target
	if pending.kind != pendingAwait || parent == nil || child == nil ||
		parent.handle != parentHandle || child.handle != childHandle ||
		g.active != parent || parent.owner != g || child.owner != g ||
		parent.state != FrameActive || child.state != FrameInitialSuspended ||
		child.parent != parent || child.header == nil ||
		child.header.Lifecycle != uint16(FrameInitialSuspended) ||
		!validInlineAwaitEdgeForBegin(parent, child) ||
		!validInlineAwaitParentDepth(g, parent, p.inlineAwaitDepth) {
		return InlineAwaitInvalid
	}
	if p.inlineAwaitDepth >= maxInlineAwaitDepth {
		return InlineAwaitDeclined
	}

	parent.state = FrameSuspended
	child.state = FrameActive
	g.active = child
	g.pending = pendingTransition{kind: pendingInlineStart, from: parent, target: child}
	p.inlineAwaitDepth++
	return InlineAwaitStarted
}

// BeginInlineAwaitCompiler consumes the exact pendingAwait written by the
// adjacent compiler prepare hook. It keeps the bounded-depth refusal and all
// externally observable mutations identical while avoiding a second full
// graph audit on the ordinary synchronous call path.
func BeginInlineAwaitCompiler(g *G, parentHandle, childHandle unsafe.Pointer) InlineAwaitDisposition {
	if ValidG(g) && parentHandle != nil && childHandle != nil && resumeGateTaken(g) &&
		g.runP != nil && g.destroyTarget == nil && !g.destroyRoot &&
		g.spawnChild == nil && g.spawnParent == nil && g.spawnP == nil &&
		!g.waiting && compilerReleasableParkState(&g.park) {
		p := g.runP
		pending := g.pending
		parent, child := pending.from, pending.target
		if pending.kind == pendingAwait && parent != nil && child != nil &&
			parent.handle == parentHandle && child.handle == childHandle &&
			g.active == parent && parent.owner == g && child.owner == g &&
			parent.state == FrameActive && child.state == FrameInitialSuspended &&
			child.parent == parent && child.header != nil &&
			child.header.Lifecycle == uint16(FrameInitialSuspended) &&
			awaitCompletionArmedForChild(child) &&
			compilerInlineAwaitParentDepth(g, parent, p.inlineAwaitDepth) {
			if p.inlineAwaitDepth >= maxInlineAwaitDepth {
				return InlineAwaitDeclined
			}
			parent.state = FrameSuspended
			child.state = FrameActive
			g.active = child
			g.pending = pendingTransition{kind: pendingInlineStart, from: parent, target: child}
			p.inlineAwaitDepth++
			return InlineAwaitStarted
		}
	}
	return BeginInlineAwait(g, parentHandle, childHandle)
}

// validInlineAwaitEdgeForBegin is the pre-dispatch form: prepareAwait has
// already published/armed the headers and record, while parent.state remains
// FrameActive until either inline selection or dispatchPending commits it.
func validInlineAwaitEdgeForBegin(parent, child *Frame) bool {
	if parent == nil || child == nil || parent.header == nil || child.header == nil ||
		child.parent != parent || child.header.Parent != parent.handle ||
		parent.header.SuspendReason != uint16(SuspendCall) ||
		parent.header.Lifecycle != uint16(FrameSuspended) {
		return false
	}
	return awaitCompletionArmedForChild(child)
}

// takeInlineAwaitInitialDecision is the child's synthetic initial-resume gate.
// It deliberately leaves the outer scheduler decision marked taken: all
// nested resumes share one logical G quantum and may still yield at ordinary
// compiler polls.
func takeInlineAwaitInitialDecision(g *G, expected ParkTicket) bool {
	if expected != (ParkTicket{}) || !ValidG(g) || g.runP == nil {
		return false
	}
	p := g.runP
	pending := g.pending
	parent, child := pending.from, pending.target
	if pending.kind != pendingInlineStart || parent == nil || child == nil ||
		p.inlineAwaitDepth == 0 || p.current != g || !p.inResume ||
		g.state != GRunning || !p.runDecisionTaken ||
		p.runDecision != (RunDecision{}) ||
		!expectedAction(p, g, p.action, ActionResume) ||
		g.active != child || child.state != FrameActive ||
		child.header == nil || child.header.Lifecycle != uint16(FrameInitialSuspended) ||
		!validInlineAwaitEdge(parent, child) ||
		!validInlineAwaitParentDepth(g, parent, p.inlineAwaitDepth-1) {
		return false
	}
	g.pending = pendingTransition{}
	return true
}

// takeInlineAwaitInitialDecisionCompiler consumes the pendingInlineStart
// certificate produced immediately before the compiler enters the child's
// initial llvm.coro.resume. BeginInlineAwaitCompiler already authenticated the
// complete edge and bounded ancestry; no external producer can mutate this
// owner-private transaction before the child prologue takes it. Any uncertain
// shape falls back to takeInlineAwaitInitialDecision through TakeRunDecision.
func takeInlineAwaitInitialDecisionCompiler(g *G) bool {
	if !ValidG(g) || g.runP == nil {
		return false
	}
	p := g.runP
	pending := g.pending
	parent, child := pending.from, pending.target
	if pending.kind != pendingInlineStart || parent == nil || child == nil ||
		p.inlineAwaitDepth == 0 || p.current != g || !p.inResume ||
		g.active != child || child.parent != parent {
		return false
	}
	// pendingInlineStart is written only after PrepareInlineAwaitCompiler has
	// authenticated the exact parent/child headers, completion record, outer
	// resume action, and depth. No callback or scheduler boundary exists before
	// this prologue consumes it. Re-reading those fields here used to turn the
	// private two-pointer receipt back into a second graph audit without adding
	// a new trust boundary.
	g.pending = pendingTransition{}
	return true
}

// FinishInlineAwait commits the return from generated code's nested
// llvm.coro.resume. done is its exact llvm.coro.done result.
func FinishInlineAwait(
	g *G, parentHandle, childHandle unsafe.Pointer, done bool,
) InlineAwaitDisposition {
	if !ValidG(g) || parentHandle == nil || childHandle == nil || g.runP == nil {
		return InlineAwaitInvalid
	}
	p := g.runP
	parent := findFrame(g, parentHandle)
	child := findFrame(g, childHandle)
	if parent == nil || child == nil || child.parent != parent ||
		p.inlineAwaitDepth == 0 || p.current != g || !p.inResume ||
		g.state != GRunning || !p.runDecisionTaken ||
		p.runDecision != (RunDecision{}) ||
		!expectedAction(p, g, p.action, ActionResume) ||
		g.destroyTarget != nil || g.destroyRoot ||
		!validInlineAwaitParentDepth(g, parent, p.inlineAwaitDepth-1) {
		return InlineAwaitInvalid
	}

	if g.active == child && g.pending.kind == pendingComplete && g.pending.from == child {
		if !done || child.parent != parent ||
			!terminalInlineCompletion(&parent.completion, child.handle) {
			return InlineAwaitInvalid
		}
		destroy, yielded, directPark, ok := dispatchPending(g, child)
		if !ok || yielded || directPark || destroy != child || g.active != parent ||
			g.destroyTarget != child || child.state != FrameDestroyPending {
			return InlineAwaitInvalid
		}
		p.inlineAwaitDepth--
		return InlineAwaitDestroy
	}

	// A slow transition belongs to the deepest active descendant. Every
	// immediate runtime inline call observes the same leaf while its generated
	// parent suspends and unwinds one native resume level.
	leaf := g.active
	if done || leaf == nil || g.pending.kind == pendingNone ||
		g.pending.kind == pendingInlineStart || g.pending.from != leaf ||
		inlineAwaitChildBelow(leaf, parent) != child {
		return InlineAwaitInvalid
	}
	p.inlineAwaitDepth--
	return InlineAwaitSuspend
}

// FinishInlineAwaitReturnCompiler commits the dominant normal-return
// transaction directly from its private pendingComplete certificate. The
// caller has just observed llvm.coro.done=true; keeping that physical fact out
// of this scalar receipt consumer makes the ordinary helper independently
// small. Slow suspension, panic, cancellation, and every uncertain shape use
// FinishInlineAwait's complete graph validation and common dispatch path.
func FinishInlineAwaitReturnCompiler(
	g *G, parentHandle, childHandle unsafe.Pointer,
) bool {
	if ValidG(g) && parentHandle != nil && childHandle != nil && g.runP != nil {
		p := g.runP
		pending := g.pending
		child := pending.from
		if child != nil && child == g.active {
			parent := child.parent
			record := (*CompletionRecord)(nil)
			if parent != nil {
				record = &parent.completion
			}
			if parent != nil && record != nil &&
				parent.handle == parentHandle && child.handle == childHandle &&
				pending.kind == pendingComplete && pending.from == child && pending.target == nil &&
				p.inlineAwaitDepth != 0 &&
				g.destroyTarget == nil && !g.destroyRoot &&
				record.child == childHandle && record.status == CompletionReturn &&
				record.typeWord == nil && record.dataWord == nil {
				g.active = parent
				child.state = FrameDestroyPending
				child.header.Lifecycle = uint16(FrameDestroyPending)
				g.destroyTarget = child
				g.pending = pendingTransition{
					kind: pendingInlineDestroy, from: parent, target: child,
				}
				p.inlineAwaitDepth--
				return true
			}
		}
	}
	return false
}

func terminalInlineCompletion(record *CompletionRecord, childHandle unsafe.Pointer) bool {
	if record == nil || childHandle == nil || record.child != childHandle {
		return false
	}
	switch record.status {
	case CompletionReturn, CompletionReturnRecovered,
		CompletionAbort, CompletionShutdown, CompletionGoexit:
		return record.typeWord == nil && record.dataWord == nil
	case CompletionPanic:
		return record.typeWord != nil
	default:
		return false
	}
}

// CommitInlineAwaitDestroy restores the parent activation after generated
// code's synchronous llvm.coro.destroy has called ReleaseFrame. It never
// consumes the completion; compatibility callers use the same await-consume
// transaction as the scheduler path.
func CommitInlineAwaitDestroy(g *G, parentHandle, childHandle unsafe.Pointer) bool {
	if !ValidG(g) || parentHandle == nil || childHandle == nil || g.runP == nil {
		return false
	}
	p := g.runP
	parent := g.active
	if parent == nil || parent.handle != parentHandle || parent.owner != g ||
		parent.header == nil || parent.state != FrameSuspended ||
		parent.header.SuspendReason != uint16(SuspendCall) ||
		parent.header.Lifecycle != uint16(FrameSuspended) ||
		p.current != g || !p.inResume || g.state != GRunning ||
		!p.runDecisionTaken || p.runDecision != (RunDecision{}) ||
		!expectedAction(p, g, p.action, ActionResume) ||
		g.pending != (pendingTransition{}) || g.destroyTarget != nil ||
		g.destroyRoot || findFrame(g, childHandle) != nil ||
		!terminalInlineCompletion(&parent.completion, childHandle) ||
		!validInlineAwaitParentDepth(g, parent, p.inlineAwaitDepth) {
		return false
	}
	parent.state = FrameActive
	parent.header.SuspendReason = uint16(SuspendNone)
	parent.header.Lifecycle = uint16(FrameActive)
	return true
}

// CommitInlineAwaitDestroyCompiler consumes the exact physical-destroy
// receipt immediately after CommitFrameDestroyV2. The parent completion record
// is the durable child identity, so a second scan of the remaining frame list
// is unnecessary on this private suffix.
func CommitInlineAwaitDestroyCompiler(g *G, parentHandle, childHandle unsafe.Pointer) bool {
	if ValidG(g) && parentHandle != nil && childHandle != nil && g.runP != nil {
		p := g.runP
		parent := g.active
		if parent != nil && parent.handle == parentHandle && parent.owner == g &&
			parent.header != nil && parent.state == FrameSuspended &&
			parent.header.SuspendReason == uint16(SuspendCall) &&
			parent.header.Lifecycle == uint16(FrameSuspended) &&
			p.current == g && p.inResume && g.state == GRunning &&
			p.runDecisionTaken && p.runDecision == (RunDecision{}) &&
			p.action.Kind == ActionResume && p.action.Flags == 0 &&
			g.pending == (pendingTransition{}) && g.destroyTarget == nil &&
			!g.destroyRoot && parent.completion.child == childHandle &&
			parent.completion.status == CompletionReturn &&
			parent.completion.typeWord == nil && parent.completion.dataWord == nil &&
			compilerInlineAwaitParentDepth(g, parent, p.inlineAwaitDepth) {
			parent.state = FrameActive
			parent.header.SuspendReason = uint16(SuspendNone)
			parent.header.Lifecycle = uint16(FrameActive)
			return true
		}
	}
	return CommitInlineAwaitDestroy(g, parentHandle, childHandle)
}

// CommitInlineAwaitReturnPhysicalDestroyCompiler consumes the exact ordinary
// Return receipt after llvm.coro.destroy and restores its static parent. It is
// deliberately scalar: the compiler ABI already knows this outcome has no
// interface payload, so the common path need not construct and decompose a
// CompletionSnapshot before continuing.
// The common Return path consumes pendingInlineDestroy, which was published by
// FinishInlineAwaitReturnCompiler after its complete owner/action proof. A dynamic
// ReleaseFrameCompiler changes only that receipt's target from the exact child
// to nil after validating and unlinking it; a borrowed child keeps the target
// until this commit unlinks its embedded metadata. No callback, suspension, or
// scheduler entry can occur inside either adjacent interval. Every fallible
// observation is therefore completed before the no-fail reactivate suffix.
func CommitInlineAwaitReturnPhysicalDestroyCompiler(
	g *G, parentHandle, childHandle unsafe.Pointer,
) bool {
	if ValidG(g) && parentHandle != nil && childHandle != nil && !g.destroyRoot {
		pending := g.pending
		parent := pending.from
		if parent != nil && parent.handle == parentHandle && parent.owner == g &&
			pending.kind == pendingInlineDestroy && parent == g.active &&
			parent.header != nil && parent.state == FrameSuspended &&
			parent.header.SuspendReason == uint16(SuspendCall) &&
			parent.header.Lifecycle == uint16(FrameSuspended) &&
			parent.completion.child == childHandle &&
			parent.completion.status == CompletionReturn &&
			parent.completion.typeWord == nil && parent.completion.dataWord == nil {
			child := pending.target
			switch {
			case child == nil && g.destroyTarget == nil && g.frames == parent:
				// The dynamic coro.free callback already validated and removed
				// the newest child. Nothing can enter the scheduler or publish a
				// second frame between that callback and this compiler hook.
			case child != nil && g.destroyTarget == child && child != parent &&
				child.handle == childHandle &&
				child.owner == g && child.parent == parent && child.header != nil &&
				child.state == FrameDestroyPending &&
				child.header.Lifecycle == uint16(FrameDestroyPending) &&
				child.borrowedStorage && child.allocationSize == 0 && child.parkWait == nil &&
				child.header.AllocationBase == unsafe.Pointer(child) &&
				!child.retainPanicTrace && g.frames == child && child.next == parent:
				g.frames = parent
				child.next = nil
				child.state = FrameDestroyed
				child.header.Lifecycle = uint16(FrameDestroyed)
				g.destroyTarget = nil
				// This compiler-private receipt requires an ordinary Return with
				// no retained panic trace or park payload. After the unlink every
				// remaining pointer names g, parent, or the dying child storage
				// itself; none keeps an otherwise dead object alive. The next
				// PublishFrameV3Compiler clears and initializes the complete
				// borrowed Frame before relinking it, so clearing the same metadata
				// area here would be duplicate lifetime work.
			default:
				return false
			}
			if g.destroyTarget == nil && g.frames == parent {
				g.pending = pendingTransition{}
				parent.state = FrameActive
				parent.header.SuspendReason = uint16(SuspendNone)
				parent.header.Lifecycle = uint16(FrameActive)
				parent.completion = CompletionRecord{}
				return true
			}
		}
	}
	return false
}

// CommitInlineAwaitPhysicalDestroyCompatibilityCompiler retains the complete
// physical/logical destroy and outcome transaction for panic, recovered
// return, Goexit, cancellation, retained traces, and every non-private shape.
// The runtime ABI calls it only after the ordinary Return receipt above has
// declined without mutation.
func CommitInlineAwaitPhysicalDestroyCompatibilityCompiler(
	g *G, parentHandle, childHandle unsafe.Pointer,
) (CompletionSnapshot, bool) {
	if !CommitFrameDestroyCompiler(g, childHandle) ||
		!CommitInlineAwaitDestroyCompiler(g, parentHandle, childHandle) {
		return CompletionSnapshot{}, false
	}
	return ConsumeAwaitCompletionCompiler(g, parentHandle)
}

// resumedFrameForAction accepts the ordinary one-frame return or a completely
// unwound inline chain. In the latter case dispatchPending must consume the
// deepest active frame, not the outer ActionResume handle whose native call
// has just returned.
func resumedFrameForAction(g *G, action Action) (*Frame, bool) {
	if g == nil || action.Handle == nil || g.active == nil || g.runP == nil ||
		g.runP.inlineAwaitDepth != 0 || g.pending.from != g.active ||
		g.active.state != FrameActive {
		return nil, false
	}
	if g.active.handle == action.Handle {
		return g.active, true
	}
	root := findFrame(g, action.Handle)
	if root == nil || !validInlineAwaitAncestry(g.active, root) {
		return nil, false
	}
	return g.active, true
}

// activeResumeOwnedByAction proves that the current physical frame is either
// the scheduler's direct ActionResume handle or a bounded inline descendant
// executing inside that still-live resume episode. Runtime event adapters use
// this instead of equating P.action with the leaf handle.
func activeResumeOwnedByAction(g *G) bool {
	if !ValidG(g) || g.runP == nil || g.active == nil {
		return false
	}
	p := g.runP
	if !expectedAction(p, g, p.action, ActionResume) {
		return false
	}
	return resumeActionOwnsActive(g, p.action, p.inlineAwaitDepth)
}

func resumeActionOwnsActive(g *G, action Action, inlineDepth uint8) bool {
	if !ValidG(g) || action.Kind != ActionResume || action.Handle == nil ||
		g.active == nil || inlineDepth > maxInlineAwaitDepth {
		return false
	}
	if g.active.handle == action.Handle {
		return inlineDepth == 0
	}
	if inlineDepth == 0 {
		return false
	}
	root := findFrame(g, action.Handle)
	return root != nil && validInlineAwaitAncestry(g.active, root)
}
