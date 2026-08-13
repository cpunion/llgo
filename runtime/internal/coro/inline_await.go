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

// InlineAwaitDisposition is the target-neutral half of one eager child
// resume. The runtime adapter remains the sole owner of llvm.coro.resume,
// llvm.coro.done, and llvm.coro.destroy; the core owns only frame state.
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
	// is now the exact destroy target. The adapter must destroy it and then
	// call CommitInlineAwaitDestroy before returning success to generated code.
	InlineAwaitDestroy
)

// maxInlineAwaitDepth bounds temporary native resume nesting. Real suspended
// state remains entirely in LLVM frames; reaching this bound simply takes the
// pre-existing scheduler path. Sixteen is deliberately conservative for
// embedded host stacks and still collapses ordinary shallow Go call chains.
const maxInlineAwaitDepth uint8 = 16

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

// FinishInlineAwait commits the return from the adapter's nested
// llvm.coro.resume. done is the adapter's exact llvm.coro.done result.
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

// CommitInlineAwaitDestroy restores the parent activation after the adapter's
// synchronous llvm.coro.destroy has called ReleaseFrame. It never consumes the
// completion; generated code uses the same await-consume ABI as the scheduler
// path.
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
