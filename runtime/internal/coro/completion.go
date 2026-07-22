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

// CompletionStatus is the terminal result of one managed child call.  Values
// are part of the compiler/runtime ABI: cl.awaitCoroChild switches on these
// exact constants after the scheduler has destroyed the child and resumed its
// parent. Abort and Shutdown carry the cleanup base selected by the child; a
// panic remains a separate overlay owned by the parent cleanup drainer.
type CompletionStatus uint32

const (
	CompletionNone CompletionStatus = iota
	CompletionReturn
	CompletionPanic
	CompletionAbort
	CompletionShutdown
	// CompletionReturnRecovered is a normal child return after that exact
	// deferred child consumed its parent's panic. It tells the cleanup drainer
	// to clear its panic overlay; the base return/cancel control remains intact.
	CompletionReturnRecovered
)

const (
	completionArmed        CompletionStatus = 0x80000000
	completionRecoverArmed CompletionStatus = 0x80000001
	completionRecoverTaken CompletionStatus = 0x80000002
)

// CompletionRecord is embedded in the parent Frame, never in the child LLVM
// frame.  child is an opaque identity only; it is never dereferenced.  The
// scheduler core is single-owner for these fields, so publication needs no
// cross-thread atomics.  Platform callbacks still communicate exclusively via
// Wait/Operation records and cannot access this object.
type CompletionRecord struct {
	status   CompletionStatus
	child    unsafe.Pointer
	typeWord unsafe.Pointer
	dataWord unsafe.Pointer
}

// CompletionSnapshot is the compiler-adapter-facing copy consumed by the
// resumed parent. Return, Abort, and Shutdown have nil payload words; Panic
// carries one concrete Go interface pair.
type CompletionSnapshot struct {
	Status   CompletionStatus
	TypeWord unsafe.Pointer
	DataWord unsafe.Pointer
}

func emptyCompletionRecord(record *CompletionRecord) bool {
	return record != nil && record.status == CompletionNone && record.child == nil &&
		record.typeWord == nil && record.dataWord == nil
}

func armAwaitCompletion(parent, child *Frame, recoverType, recoverData unsafe.Pointer) bool {
	if parent == nil || child == nil || child.handle == nil || !emptyCompletionRecord(&parent.completion) ||
		recoverType == nil && recoverData != nil {
		return false
	}
	parent.completion.child = child.handle
	if recoverType == nil {
		parent.completion.status = completionArmed
		return true
	}
	parent.completion.typeWord = recoverType
	parent.completion.dataWord = recoverData
	parent.completion.status = completionRecoverArmed
	return true
}

func awaitCompletionArmedForChild(child *Frame) bool {
	if child == nil || child.parent == nil || child.handle == nil {
		return false
	}
	record := &child.parent.completion
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

func hasAwaitCompletionTransaction(child *Frame) bool {
	return child != nil && child.parent != nil && !emptyCompletionRecord(&child.parent.completion)
}

func validAwaitCompletionPublisher(g *G, child *Frame, status CompletionStatus) bool {
	if !ValidG(g) || child == nil || child.handle == nil || child.header == nil ||
		(status != CompletionReturn && status != CompletionPanic &&
			status != CompletionAbort && status != CompletionShutdown) {
		return false
	}
	parent := child.parent
	if parent == nil || parent.owner != g || parent.handle == nil || parent.header == nil ||
		parent.state != FrameSuspended || parent.header.G != unsafe.Pointer(g) ||
		parent.header.SuspendReason != uint16(SuspendCall) ||
		parent.header.Lifecycle != uint16(FrameSuspended) || child.header.Parent != parent.handle {
		return false
	}
	return awaitCompletionArmedForChild(child)
}

func publishAwaitCompletion(parent *Frame, status CompletionStatus, typeWord, dataWord unsafe.Pointer) bool {
	record := &parent.completion
	switch status {
	case CompletionReturn:
		if typeWord != nil || dataWord != nil {
			return false
		}
		switch record.status {
		case completionArmed:
			record.status = CompletionReturn
		case completionRecoverArmed:
			record.status = CompletionReturn
			record.typeWord = nil
			record.dataWord = nil
		case completionRecoverTaken:
			record.status = CompletionReturnRecovered
			record.typeWord = nil
			record.dataWord = nil
		default:
			return false
		}
		return true
	case CompletionPanic:
		if typeWord == nil || record.child == nil {
			return false
		}
		switch record.status {
		case completionArmed:
			if record.typeWord != nil || record.dataWord != nil {
				return false
			}
		case completionRecoverArmed, completionRecoverTaken:
			if record.typeWord == nil {
				return false
			}
		default:
			return false
		}
		record.typeWord = typeWord
		record.dataWord = dataWord
		record.status = CompletionPanic
		return true
	case CompletionAbort, CompletionShutdown:
		if typeWord != nil || dataWord != nil || record.child == nil {
			return false
		}
		switch record.status {
		case completionArmed:
			if record.typeWord != nil || record.dataWord != nil {
				return false
			}
		case completionRecoverArmed, completionRecoverTaken:
			if record.typeWord == nil {
				return false
			}
		default:
			return false
		}
		// Cancellation becomes the parent's cleanup base. Recovery is committed
		// only by CompletionReturnRecovered; an interrupted deferred child does
		// not clear the parent's panic overlay.
		record.typeWord = nil
		record.dataWord = nil
		record.status = status
		return true
	default:
		return false
	}
}

// prepareChildPanic converts a terminal child panic into the same destroy-then-
// resume transaction as a normal child return.  It deliberately does not
// publish G.panicRecord: the parent still owns Go cleanup/recover semantics and
// may either handle the outcome or republish it to its own parent later.
func prepareChildPanic(
	g *G,
	frame *Frame,
	header *HeaderV1,
	status ExplicitStatus,
	typeWord, dataWord unsafe.Pointer,
) bool {
	if !ValidG(g) || !resumeGateTaken(g) || status != ExplicitStatusPanic || typeWord == nil ||
		frame == nil || frame.parent == nil || frame != g.active || frame.header != header ||
		header == nil || header.Flags != 0 || frame.state != FrameActive ||
		header.G != unsafe.Pointer(g) || header.SuspendReason != uint16(SuspendPanic) ||
		header.Lifecycle != uint16(FrameFinalSuspended) ||
		g.state != GRunning || g.runP == nil || g.pending.kind != pendingNone ||
		g.pending.from != nil || g.pending.target != nil || g.pending.wait != nil || g.pending.ticket != 0 ||
		g.destroyTarget != nil || g.destroyRoot || g.queued || g.nextReady != nil ||
		g.waitToken != nil || g.waitTicket != 0 || g.nextWait != nil || g.waiting ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		!releasableParkState(&g.park) || g.park.taskCancelPhase == taskCancelRequested ||
		g.panicUnwind || !emptyPanicRecord(&g.panicRecord) ||
		!validAwaitCompletionPublisher(g, frame, CompletionPanic) {
		return false
	}
	if !publishAwaitCompletion(frame.parent, CompletionPanic, typeWord, dataWord) {
		return false
	}
	g.pending = pendingTransition{kind: pendingComplete, from: frame}
	return true
}

func completionMatchesTerminalFrame(frame *Frame) bool {
	if frame == nil || frame.header == nil {
		return false
	}
	if frame.parent == nil {
		return frame.header.SuspendReason == uint16(SuspendFrameComplete)
	}
	switch frame.parent.completion.status {
	case CompletionNone:
		// Legacy/V1 child transactions have no parent-owned outcome record.
		return frame.header.SuspendReason == uint16(SuspendFrameComplete)
	case CompletionReturn, CompletionReturnRecovered, CompletionAbort, CompletionShutdown:
		return frame.header.SuspendReason == uint16(SuspendFrameComplete)
	case CompletionPanic:
		return frame.header.SuspendReason == uint16(SuspendPanic)
	default:
		return false
	}
}

// ConsumeAwaitCompletion is called inside the resumed parent's physical
// activation, after the child destroy/free transaction has committed.  A
// successful consume resets the record before user code or another child call
// can run, making sequential awaits allocation-free and fail-closed.
func ConsumeAwaitCompletion(g *G, parentHandle unsafe.Pointer) (CompletionSnapshot, bool) {
	if !ValidG(g) || !resumeGateTaken(g) || parentHandle == nil ||
		g.pending.kind != pendingNone || g.destroyTarget != nil || g.spawnChild != nil ||
		!releasableParkState(&g.park) {
		return CompletionSnapshot{}, false
	}
	parent := findFrame(g, parentHandle)
	if parent == nil || parent != g.active || parent.owner != g || parent.header == nil ||
		parent.state != FrameActive || parent.header.G != unsafe.Pointer(g) ||
		parent.header.SuspendReason != uint16(SuspendNone) ||
		parent.header.Lifecycle != uint16(FrameActive) {
		return CompletionSnapshot{}, false
	}
	record := &parent.completion
	if record.child == nil || findFrame(g, record.child) != nil {
		return CompletionSnapshot{}, false
	}
	snapshot := CompletionSnapshot{Status: record.status, TypeWord: record.typeWord, DataWord: record.dataWord}
	switch record.status {
	case CompletionReturn, CompletionReturnRecovered:
		if record.typeWord != nil || record.dataWord != nil {
			return CompletionSnapshot{}, false
		}
	case CompletionPanic:
		if record.typeWord == nil {
			return CompletionSnapshot{}, false
		}
	case CompletionAbort, CompletionShutdown:
		if record.typeWord != nil || record.dataWord != nil {
			return CompletionSnapshot{}, false
		}
	default:
		return CompletionSnapshot{}, false
	}
	*record = CompletionRecord{}
	return snapshot, true
}
