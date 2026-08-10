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

// ExplicitStatus is a terminal completion published by compiler-generated
// code. Explicit panic now has two ownership paths: a child publishes into its
// suspended parent's CompletionRecord, while a root publishes the task-local
// PanicRecord. Goexit uses the payload-free CompletionStatus channel and
// implicit faults still require their own producers.
type ExplicitStatus uint32

const (
	ExplicitStatusNone ExplicitStatus = iota
	ExplicitStatusPanic
	ExplicitStatusReturn
	ExplicitStatusGoexit
	ExplicitStatusImplicitFault
)

const (
	explicitStatusPublishing uint32 = 0x80000000 + iota
	explicitStatusRejected
)

// PanicRecord is embedded in G, so its two interface words remain rooted after
// the active LLVM frame and all suspended-await ancestors have been destroyed.
// status is the one-time publication word. The runtime must not inspect either
// payload word until it observes ExplicitStatusPanic with acquire semantics.
type PanicRecord struct {
	status   uint32
	typeWord unsafe.Pointer
	dataWord unsafe.Pointer
}

// PanicRecordSnapshot is the stable adapter-facing copy of a published
// task-local record. Neither payload word is read from a frame/header/handle.
type PanicRecordSnapshot struct {
	Status   ExplicitStatus
	TypeWord unsafe.Pointer
	DataWord unsafe.Pointer
}

func emptyPanicRecord(record *PanicRecord) bool {
	return record != nil && preemptLoad(&record.status) == uint32(ExplicitStatusNone) &&
		record.typeWord == nil && record.dataWord == nil
}

func publishedPanicRecord(record *PanicRecord) bool {
	return record != nil && preemptLoad(&record.status) == uint32(ExplicitStatusPanic)
}

// LoadPanicRecord takes an acquire snapshot after one successful publication.
// The record is deliberately not consumed: terminal reporting must retain a GC
// root until a later, separately designed fatal/recover ownership protocol.
func LoadPanicRecord(g *G) (PanicRecordSnapshot, bool) {
	if !ValidG(g) || !publishedPanicRecord(&g.panicRecord) {
		return PanicRecordSnapshot{}, false
	}
	return PanicRecordSnapshot{
		Status:   ExplicitStatusPanic,
		TypeWord: g.panicRecord.typeWord,
		DataWord: g.panicRecord.dataWord,
	}, true
}

func validPanicAncestor(g *G, frame *Frame) bool {
	return frame != nil && frame.owner == g && frame.handle != nil && frame.header != nil &&
		frame.state == FrameSuspended && frame.header.G == unsafe.Pointer(g) && frame.header.Flags == 0 &&
		frame.header.SuspendReason == uint16(SuspendCall) &&
		frame.header.Lifecycle == uint16(FrameSuspended)
}

// validPanicAncestry proves before publication that every continuation which
// terminal panic unwinding would bypass is a plain suspended await. Version
// zero has no cleanup/recover transport, so any non-zero flags reject the
// entire operation before the active frame or an ancestor can be destroyed.
func validPanicAncestry(g *G, active *Frame) bool {
	if g == nil || active == nil || active.header == nil {
		return false
	}
	child := active
	for ancestor := active.parent; ancestor != nil; ancestor = ancestor.parent {
		if !validPanicAncestor(g, ancestor) || child.header.Parent != ancestor.handle {
			return false
		}
		child = ancestor
	}
	return child == g.root && child.header.Parent == nil
}

// PrepareExplicitStatus is the independently testable core of the future
// compiler hook. Only ExplicitStatusPanic is accepted. The first caller owns
// the publication attempt; any malformed winner permanently poisons the record
// instead of allowing execution to continue with ambiguous terminal state.
//
// HeaderV1.Flags must be zero. Thus cleanup/recover/Goexit/implicit-fault
// shapes cannot be smuggled through an unversioned flag convention. An
// untyped nil panic is also rejected; the compiler must first materialize the
// Go-version-appropriate non-nil panic type word.
func PrepareExplicitStatus(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	status ExplicitStatus,
	typeWord, dataWord unsafe.Pointer,
) bool {
	if g == nil || !ValidG(g) || !resumeGateTaken(g) {
		return false
	}
	// A managed child panic is not terminal for the logical G.  Reconcile it at
	// the immediate call site so the parent can run defers/recover before any
	// outcome is allowed to reach the next ancestor or the root reporter.
	if frame := findFrame(g, handle); frame != nil && frame.parent != nil && hasAwaitCompletionTransaction(frame) {
		return prepareChildPanic(g, frame, header, status, typeWord, dataWord)
	}
	if !canStagePanicTraceDiscard(g) {
		return false
	}
	if activePanicTrace(g) {
		if _, _, ok := panicTraceIdentity(g.panicTraceHead); !ok {
			return false
		}
	}
	record := &g.panicRecord
	if !preemptCompareAndSwap(&record.status, uint32(ExplicitStatusNone), explicitStatusPublishing) {
		return false
	}
	reject := func() bool {
		preemptStore(&record.status, explicitStatusRejected)
		return false
	}
	if status != ExplicitStatusPanic || typeWord == nil || handle == nil || header == nil || header.Flags != 0 ||
		g.state != GRunning || g.active == nil || g.root == nil || g.runP == nil ||
		g.pending.kind != pendingNone || g.pending.from != nil || g.pending.target != nil ||
		g.destroyTarget != nil || g.destroyRoot || g.queued || g.nextReady != nil ||
		g.waiting || g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		!releasableParkState(&g.park) || g.park.taskCancelPhase == taskCancelRequested || g.panicUnwind {
		return reject()
	}
	frame := findFrame(g, handle)
	if frame == nil || frame != g.active || frame.owner != g || frame.header != header ||
		frame.state != FrameActive || header.G != unsafe.Pointer(g) ||
		header.SuspendReason != uint16(SuspendPanic) ||
		header.Lifecycle != uint16(FrameFinalSuspended) || !validPanicAncestry(g, frame) {
		return reject()
	}
	if activePanicTrace(g) {
		traceType, traceData, ok := panicTraceIdentity(g.panicTraceHead)
		if !ok || panicTraceCarrier(g) != frame ||
			traceType != typeWord || traceData != dataWord {
			// A cleanup-local replacement must first use ReplacePanicTrace.
			// Payload inequality is not a sound substitute for that exact
			// compiler semantic operation.
			return reject()
		}
	}

	// The winner is the only writer. Publish pending ownership before the
	// release-store of status so a post-resume scheduler and any snapshot reader
	// can never observe a partially initialized record.
	record.typeWord = typeWord
	record.dataWord = dataWord
	g.pending = pendingTransition{kind: pendingPanic, from: frame}
	preemptStore(&record.status, uint32(ExplicitStatusPanic))
	return true
}

// PreparePanic is the intended runtime hook shape. It carries the physical G
// explicitly and never consults TLS or a process-global current-G variable.
func PreparePanic(g *G, handle unsafe.Pointer, header *HeaderV1, typeWord, dataWord unsafe.Pointer) bool {
	return PrepareExplicitStatus(g, handle, header, ExplicitStatusPanic, typeWord, dataWord)
}

func preparePanicAncestor(p *P, g *G, frame *Frame) (Action, bool) {
	if p == nil || g == nil || frame == nil || p.current != g || g.state != GPanicking ||
		!g.panicUnwind || !publishedPanicRecord(&g.panicRecord) || g.destroyTarget != nil ||
		frame != g.active || !validPanicAncestor(g, frame) {
		return Action{}, false
	}
	handle := frame.handle
	g.active = frame.parent
	g.destroyRoot = frame == g.root
	frame.state = FrameDestroyPending
	frame.header.Lifecycle = uint16(FrameDestroyPending)
	g.destroyTarget = frame
	return setAction(p, ActionPanicDestroy, handle)
}

func finishPanicG(p *P, g *G, wasRoot bool) (Action, bool) {
	if p == nil || g == nil || !wasRoot || g.active != nil || g.frames != nil ||
		!g.panicUnwind || !publishedPanicRecord(&g.panicRecord) {
		return Action{}, false
	}
	kind := ActionPanicDestroy
	if g.state == GDispatching {
		kind = ActionDestroy
	}
	return commitRootDestroyedCompatibility(p, g, kind)
}

// commitInitialPanicDestroyed is entered only after the active final-suspended
// panic frame passed coro.done, was directly destroyed, and its free hook
// unlinked it. A suspended-await ancestor is never checked or resumed.
func commitInitialPanicDestroyed(p *P, g *G, wasRoot bool) (Action, bool) {
	if g == nil || !g.panicUnwind || !publishedPanicRecord(&g.panicRecord) {
		return Action{}, false
	}
	if g.active != nil {
		if wasRoot {
			return Action{}, false
		}
		g.destroyRoot = false
		g.state = GPanicking
		return preparePanicAncestor(p, g, g.active)
	}
	return finishPanicG(p, g, wasRoot)
}

// PanicDestroyed commits one direct ancestor destroy. ReleaseFrame must have
// already removed the frame. The next action is either another deepest parent
// destroy or terminal PanicComplete; no normal continuation is resumed.
func PanicDestroyed(p *P, g *G, action Action) (Action, bool) {
	if !expectedAction(p, g, action, ActionPanicDestroy) || p.inResume || p.inlineAwaitDepth != 0 ||
		g.state != GPanicking || !g.panicUnwind || !publishedPanicRecord(&g.panicRecord) ||
		g.destroyTarget != nil {
		return Action{}, false
	}
	wasRoot := g.destroyRoot
	if g.active != nil {
		if wasRoot {
			return Action{}, false
		}
		g.destroyRoot = false
		return preparePanicAncestor(p, g, g.active)
	}
	return finishPanicG(p, g, wasRoot)
}

// PanicDestroyedBounded is the direct-ancestor counterpart of
// DestroyedBounded. Each call commits exactly one already-performed physical
// destroy. A surviving ancestor is returned as a ready-tail continuation; the
// root publishes the same handle-free terminal receipt as normal completion.
func PanicDestroyedBounded(p *P, g *G, action Action) (Action, bool) {
	if !expectedAction(p, g, action, ActionPanicDestroy) || p.inResume || p.inlineAwaitDepth != 0 ||
		g.state != GPanicking || !g.panicUnwind || !publishedPanicRecord(&g.panicRecord) ||
		g.destroyTarget != nil || g.runAction != ActionInvalid {
		return Action{}, false
	}
	wasRoot := g.destroyRoot
	if g.active != nil {
		if wasRoot {
			return Action{}, false
		}
		g.destroyRoot = false
		return preparePanicAncestor(p, g, g.active)
	}
	return finishBoundedRootDestroy(p, g, wasRoot, true)
}

// AcknowledgePanicTerminalSchedule consumes the only legal failed terminal
// commit after the last handle was already destroyed: RequestSchedule won the
// idle-to-disabled race. The adapter may then retry PanicDestroyed without
// calling llvm.coro.destroy twice.
func AcknowledgePanicTerminalSchedule(p *P, g *G, action Action) bool {
	return expectedAction(p, g, action, ActionPanicDestroy) && !p.inResume && p.inlineAwaitDepth == 0 &&
		preemptLoad(&p.executorMode) == executorModeUnbound && p.executor == nil && p.channelSource == nil &&
		g.state == GPanicking && g.panicUnwind && publishedPanicRecord(&g.panicRecord) &&
		g.destroyTarget == nil && g.destroyRoot && g.active == nil && g.frames == nil &&
		p.readyHead == nil && p.readyTail == nil && emptySchedulerWaitQueues(p) &&
		validReadyQueue(p) && validSchedulerWaitQueues(p) &&
		preemptCompareAndSwap(&p.schedule, scheduleRequested, scheduleIdle)
}
