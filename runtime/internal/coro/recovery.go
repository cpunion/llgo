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

// RecoverSnapshot is the stable adapter-facing copy returned by TakeRecover.
type RecoverSnapshot struct {
	TypeWord unsafe.Pointer
	DataWord unsafe.Pointer
}

func validRecoverActiveFrame(g *G, child *Frame) bool {
	if !ValidG(g) || child == nil || child != g.active || child.owner != g || child.handle == nil ||
		child.header == nil || child.state != FrameActive || child.header.G != unsafe.Pointer(g) ||
		child.header.SuspendReason != uint16(SuspendNone) ||
		child.header.Lifecycle != uint16(FrameActive) {
		return false
	}
	if child.parent == nil {
		return child == g.root && child.header.Parent == nil
	}
	parent := child.parent
	return parent.owner == g && parent.handle != nil && parent.header != nil &&
		parent.state == FrameSuspended && parent.header.G == unsafe.Pointer(g) &&
		parent.header.SuspendReason == uint16(SuspendCall) &&
		parent.header.Lifecycle == uint16(FrameSuspended) &&
		child.header.Parent == parent.handle && awaitCompletionArmedForChild(child)
}

func validRecoverTaskActivation(g *G) bool {
	return ValidG(g) && resumeGateTaken(g) && g.state == GRunning &&
		g.runP != nil && g.pending == (pendingTransition{}) && g.destroyTarget == nil &&
		!g.destroyRoot && g.spawnChild == nil && releasableParkState(&g.park) &&
		validRecoverActiveFrame(g, g.active)
}

// RecoverAliasScopeActive validates the compiler-owned boundary at which a
// dynamic deferred descriptor may temporarily publish its transparent-wrapper
// identity. The scope itself lives in the logical runtime G; this predicate
// deliberately adds no pointer or flag to every scheduler G or physical frame.
func RecoverAliasScopeActive(g *G) bool {
	return validRecoverTaskActivation(g)
}

func validRecoverAliasAncestor(g *G, child *Frame) bool {
	if !ValidG(g) || child == nil || child.owner != g || child.handle == nil ||
		child.header == nil || child.state != FrameSuspended ||
		child.header.G != unsafe.Pointer(g) ||
		child.header.SuspendReason != uint16(SuspendCall) ||
		child.header.Lifecycle != uint16(FrameSuspended) || child.parent == nil ||
		child.header.Parent != child.parent.handle {
		return false
	}
	parent := child.parent
	return parent.owner == g && parent.handle != nil && parent.header != nil &&
		parent.state == FrameSuspended && parent.header.G == unsafe.Pointer(g) &&
		parent.header.SuspendReason == uint16(SuspendCall) &&
		parent.header.Lifecycle == uint16(FrameSuspended) &&
		awaitCompletionArmedForChild(child)
}

// TakeRecover implements the predeclared recover operation for an active
// physical coroutine. The direct-call capability is encoded in the immediate
// parent's in-flight CompletionRecord, so no second frame record, TLS lookup,
// or native stack walk is needed. valid is true for every well-formed ordinary
// call that must return nil, including roots, managed helpers, and a second
// recover in the same deferred child.
func TakeRecover(g *G, childHandle unsafe.Pointer) (snapshot RecoverSnapshot, recovered, valid bool) {
	return takeRecover(g, childHandle, false)
}

// TakeRecoverAlias is the transparent-wrapper counterpart of TakeRecover. It
// may cross ordinary managed awaits only after the runtime has proved that the
// current physical activation owns the exact token installed by
// StartRecoverFrameAlias. The first recover-armed ancestor remains the sole
// payload owner, so successful recovery naturally publishes
// CompletionReturnRecovered when the original deferred descriptor returns.
func TakeRecoverAlias(g *G, childHandle unsafe.Pointer) (snapshot RecoverSnapshot, recovered, valid bool) {
	return takeRecover(g, childHandle, true)
}

func takeRecover(g *G, childHandle unsafe.Pointer, transparent bool) (snapshot RecoverSnapshot, recovered, valid bool) {
	if childHandle == nil || !validRecoverTaskActivation(g) {
		return RecoverSnapshot{}, false, false
	}
	child := findFrame(g, childHandle)
	if child != g.active {
		return RecoverSnapshot{}, false, false
	}
	if child.parent == nil {
		if transparent {
			return RecoverSnapshot{}, false, false
		}
		return RecoverSnapshot{}, false, true
	}
	for {
		record := &child.parent.completion
		if record.child != child.handle {
			return RecoverSnapshot{}, false, false
		}
		switch record.status {
		case completionArmed:
			if record.typeWord != nil || record.dataWord != nil {
				return RecoverSnapshot{}, false, false
			}
			if !transparent {
				return RecoverSnapshot{}, false, true
			}
			child = child.parent
			if child.parent == nil || !validRecoverAliasAncestor(g, child) {
				return RecoverSnapshot{}, false, false
			}
		case completionRecoverArmed:
			if record.typeWord == nil {
				return RecoverSnapshot{}, false, false
			}
			record.status = completionRecoverTaken
			return RecoverSnapshot{TypeWord: record.typeWord, DataWord: record.dataWord}, true, true
		case completionRecoverTaken:
			if record.typeWord == nil {
				return RecoverSnapshot{}, false, false
			}
			return RecoverSnapshot{}, false, true
		default:
			return RecoverSnapshot{}, false, false
		}
	}
}

func recoverTraceScope(g *G) (owner *Frame, typeWord, dataWord unsafe.Pointer, ok bool) {
	// This is a read-only observation used from runtime.Callers, which may be
	// inside a compiler/runtime critical section. The active-frame and exact
	// ancestry validation below are sufficient; requiring the suspension gate
	// here would incorrectly hide the scope precisely while stack inspection
	// has preemption masked.
	if !ValidG(g) || g.state != GRunning {
		return nil, nil, nil, false
	}
	child := g.active
	if child == nil || child.owner != g || child.handle == nil || child.header == nil ||
		child.state != FrameActive || child.header.G != unsafe.Pointer(g) ||
		child.header.SuspendReason != uint16(SuspendNone) ||
		child.header.Lifecycle != uint16(FrameActive) {
		return nil, nil, nil, false
	}
	// Calls made by the recovering deferred function may themselves be managed
	// children (debug.Stack -> runtime.Callers is one real example). Walk the
	// exact await ancestry until the recovery-owning CompletionRecord appears.
	for ; child.parent != nil; child = child.parent {
		record := &child.parent.completion
		if emptyCompletionRecord(record) {
			return nil, nil, nil, false
		}
		if record.child != child.handle {
			return nil, nil, nil, false
		}
		switch record.status {
		case completionArmed, completionRecoverArmed:
			continue
		case completionRecoverTaken:
			if record.typeWord == nil {
				return nil, nil, nil, false
			}
			return child.parent, record.typeWord, record.dataWord, true
		default:
			return nil, nil, nil, false
		}
	}
	return nil, nil, nil, false
}

// RecoverTraceActive reports whether the currently executing physical frame
// is the deferred child which consumed its parent's panic or an exact managed
// descendant of that child. The CompletionRecord already owns this scope from
// recover through the child's terminal return, so stackless traceback
// visibility needs no native frame marker, TLS side table, or additional per-G
// state.
func RecoverTraceActive(g *G) bool {
	_, _, _, ok := recoverTraceScope(g)
	return ok
}

// RecoverTraceFrames joins the retained panic path to its still-live recovery
// owner and suspended ancestors. The resulting deepest-to-root sequence is the
// exact snapshot runtime.Caller needs after panic propagation has destroyed
// the original LLVM coroutine frames. No second trace is stored in G: callers
// materialize their target-specific PC view only after recover succeeds.
func RecoverTraceFrames(g *G, snapshots []PanicTraceFrameSnapshot) (int, bool) {
	owner, typeWord, dataWord, scopeOK := recoverTraceScope(g)
	if !scopeOK || len(snapshots) == 0 {
		return 0, false
	}
	// A panic caught before any physical child is destroyed has no retained
	// prefix to splice; its complete caller chain is still live. This is a
	// successful empty snapshot, distinct from a malformed non-empty trace.
	if emptyPanicTrace(g) {
		return 0, true
	}
	if !activePanicTrace(g) || panicTraceCarrier(g) != owner {
		return 0, false
	}
	traceType, traceData, identityOK := panicTraceIdentity(g.panicTraceHead)
	if !identityOK || traceType != typeWord || traceData != dataWord {
		return 0, false
	}

	n := 0
	frame := g.panicTraceHead
	for count := uint32(0); count < g.panicTraceCount; count++ {
		if frame == nil || n == len(snapshots) {
			return 0, false
		}
		snapshot, next, ok := retainedPanicTraceFrame(g, frame)
		if !ok {
			return 0, false
		}
		snapshots[n] = snapshot
		n++
		frame = next
	}
	if frame != nil || n == len(snapshots) {
		return 0, false
	}
	physicalCount, physicalOK := physicalTraceFrames(g, owner, false, snapshots[n:])
	if !physicalOK {
		return 0, false
	}
	return n + physicalCount, true
}
