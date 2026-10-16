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

import (
	"runtime"
	"testing"
	"unsafe"
)

func newRecoverAwaitFixture(t *testing.T, typeWord, dataWord unsafe.Pointer) *awaitCompletionFixture {
	t.Helper()
	return newAwaitCompletionFixtureConfigured(t, nil, typeWord, dataWord)
}

func completeRecoverChild(t *testing.T, fixture *awaitCompletionFixture) {
	t.Helper()
	fixture.child.header.SuspendReason = uint16(SuspendFrameComplete)
	fixture.child.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(fixture.g, fixture.child.handle, fixture.child.header) {
		t.Fatal("publish recovery child return")
	}
	fixture.destroyChildAndResumeParent(t)
}

func TestRecoverDirectDeferredChildTakesPayloadOnce(t *testing.T) {
	typeWord := unsafe.Pointer(new(byte))
	dataWord := unsafe.Pointer(new(byte))
	fixture := newRecoverAwaitFixture(t, typeWord, dataWord)
	ownerDescriptor := (*FrameDescriptorV1)(fixture.parent.descriptor)
	ownerDescriptor.Function, ownerDescriptor.File = "main.owner", "/src/main.go"
	fixture.parent.header.Line = 17
	var trace [1]PanicTraceFrameSnapshot
	wantTrace := PanicTraceFrameSnapshot{Function: "main.owner", File: "/src/main.go", Line: 17}
	if !DeferredPanicTraceActive(fixture.g) {
		t.Fatal("defer before recover did not expose its exact panic scope")
	}
	if n, ok := DeferredPanicTraceFrames(fixture.g, trace[:]); !ok || n != 1 || trace[0] != wantTrace {
		t.Fatalf("live panic trace before recover = (%d, %t, %+v), want exact owner", n, ok, trace[0])
	}

	snapshot, recovered, valid := TakeRecover(fixture.g, fixture.child.handle)
	want := RecoverSnapshot{TypeWord: typeWord, DataWord: dataWord}
	if !valid || !recovered || snapshot != want {
		t.Fatalf("take direct recover = (%+v, %t, %t), want (%+v, true, true)", snapshot, recovered, valid, want)
	}
	if !DeferredPanicTraceActive(fixture.g) {
		t.Fatal("direct recover did not expose its logical traceback scope")
	}
	if n, ok := DeferredPanicTraceFrames(fixture.g, trace[:]); !ok || n != 1 || trace[0] != wantTrace {
		t.Fatalf("live recover trace = (%d, %t, %+v), want exact owner", n, ok, trace[0])
	}
	if duplicate, recovered, valid := TakeRecover(fixture.g, fixture.child.handle); !valid || recovered || duplicate != (RecoverSnapshot{}) {
		t.Fatalf("duplicate recover = (%+v, %t, %t)", duplicate, recovered, valid)
	}
	if !DeferredPanicTraceActive(fixture.g) {
		t.Fatal("duplicate recover cleared the winning traceback scope")
	}

	completeRecoverChild(t, fixture)
	if DeferredPanicTraceActive(fixture.g) {
		t.Fatal("completed recovering child retained its traceback scope")
	}
	completion, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle)
	if !ok || completion != (CompletionSnapshot{Status: CompletionReturnRecovered}) {
		t.Fatalf("consume recovered child return = (%+v, %t)", completion, ok)
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	fixture.keepAlive()
}

func TestDeferredPanicTraceFramesJoinRetainedPanicAndLiveOwner(t *testing.T) {
	typeWord := unsafe.Pointer(new(byte))
	dataWord := unsafe.Pointer(new(byte))
	fixture := newRecoverAwaitFixture(t, typeWord, dataWord)
	owner := fixture.parentFrame()
	ownerDescriptor := (*FrameDescriptorV1)(fixture.parent.descriptor)
	ownerDescriptor.Function = "main.owner"
	ownerDescriptor.File = "/src/main.go"
	fixture.parent.header.Line = 17

	traceMemory, traceDescriptor := retainDetachedTestPanicTrace(
		t, fixture.g, owner, typeWord, dataWord,
	)
	traceDescriptor.Function = "main.panicking"
	traceDescriptor.File = "/src/main.go"
	fixture.g.panicTraceHead.panicLine = 29
	var before [2]PanicTraceFrameSnapshot
	if n, ok := DeferredPanicTraceFrames(fixture.g, before[:]); !ok || n != 2 ||
		before[0].Function != "main.panicking" || before[0].Line != 29 ||
		before[1].Function != "main.owner" || before[1].Line != 17 {
		t.Fatalf("retained panic before recover = (%d, %t, %+v)", n, ok, before)
	}
	if snapshot, recovered, valid := TakeRecover(fixture.g, fixture.child.handle); !valid || !recovered || snapshot != (RecoverSnapshot{TypeWord: typeWord, DataWord: dataWord}) {
		t.Fatalf("take trace recovery = (%+v, %t, %t)", snapshot, recovered, valid)
	}

	wants := []PanicTraceFrameSnapshot{
		{Function: "main.panicking", File: "/src/main.go", Line: 29},
		{Function: "main.owner", File: "/src/main.go", Line: 17},
	}
	got := make([]PanicTraceFrameSnapshot, len(wants))
	if n, ok := DeferredPanicTraceFrames(fixture.g, got); !ok || n != len(wants) {
		t.Fatalf("recover trace count = (%d, %t), want (%d, true)", n, ok, len(wants))
	}
	for index, want := range wants {
		if got[index] != want {
			t.Fatalf("recover trace frame %d = %+v, want %+v", index, got[index], want)
		}
	}
	if n, ok := DeferredPanicTraceFrames(fixture.g, got[:len(got)-1]); ok || n != 0 {
		t.Fatalf("short recover trace destination accepted = (%d, %t)", n, ok)
	}

	savedCarrier := fixture.g.panicTraceTail.parent
	fixture.g.panicTraceTail.parent = nil
	if n, ok := DeferredPanicTraceFrames(fixture.g, got); ok || n != 0 {
		t.Fatalf("detached recover trace carrier accepted = (%d, %t)", n, ok)
	}
	fixture.g.panicTraceTail.parent = savedCarrier

	runtime.KeepAlive(traceMemory)
	runtime.KeepAlive(traceDescriptor)
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	fixture.keepAlive()
}

func TestDeferredPanicTraceRejectsOrdinaryAndUnboundScope(t *testing.T) {
	fixture := newAwaitCompletionFixture(t)
	var trace [2]PanicTraceFrameSnapshot
	if DeferredPanicTraceActive(fixture.g) {
		t.Fatal("ordinary deferred/call child acquired a panic scope")
	}
	if n, ok := DeferredPanicTraceFrames(fixture.g, trace[:]); ok || n != 0 {
		t.Fatalf("ordinary trace = (%d, %t)", n, ok)
	}
	record := &fixture.parentFrame().completion
	original := *record
	// A positive cache bit is never recovery authority: exercise the original
	// payload/child validation even when the cheap negative proof is absent.
	fixture.g.active.properties |= frameDeferredPanicScope
	if DeferredPanicTraceActive(fixture.g) {
		t.Fatal("inherited scope bit authorized an ordinary completion")
	}
	record.status = completionRecoverArmed
	if DeferredPanicTraceActive(fixture.g) {
		t.Fatal("panic scope with no payload was accepted")
	}
	record.typeWord = unsafe.Pointer(new(byte))
	record.child = unsafe.Pointer(new(byte))
	if DeferredPanicTraceActive(fixture.g) {
		t.Fatal("panic scope with another child's identity was accepted")
	}
	*record = original
	fixture.keepAlive()
}

func TestRecoverUntakenScopePublishesOrdinaryReturn(t *testing.T) {
	typeWord := unsafe.Pointer(new(byte))
	dataWord := unsafe.Pointer(new(byte))
	fixture := newRecoverAwaitFixture(t, typeWord, dataWord)
	completeRecoverChild(t, fixture)

	snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle)
	if !ok || snapshot != (CompletionSnapshot{Status: CompletionReturn}) {
		t.Fatalf("consume unrecovered child return = (%+v, %t)", snapshot, ok)
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	fixture.keepAlive()
}

func TestRecoverOutsideArmedScopeReturnsNil(t *testing.T) {
	fixture := newAwaitCompletionFixture(t)
	if snapshot, recovered, valid := TakeRecover(fixture.g, fixture.child.handle); !valid || recovered || snapshot != (RecoverSnapshot{}) {
		t.Fatalf("ordinary child recover = (%+v, %t, %t)", snapshot, recovered, valid)
	}
	completeRecoverChild(t, fixture)
	if snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); !ok || snapshot.Status != CompletionReturn {
		t.Fatalf("consume ordinary child return = (%+v, %t)", snapshot, ok)
	}
	fixture.keepAlive()
}

func TestRecoverAliasRequiresReactivatedHeaderDuringCancellationCleanup(t *testing.T) {
	for _, kind := range []TaskCancelKind{TaskCancelAbort, TaskCancelShutdown} {
		fixture := newAwaitCompletionFixture(t)
		fixture.g.park.taskCancelKind = kind
		fixture.g.park.taskCancelPhase = taskCancelCleanup
		if !RecoverAliasScopeActive(fixture.g) {
			t.Fatalf("valid cleanup activation rejected for cancellation %d", kind)
		}
		for _, reason := range []SuspendReason{SuspendPark, SuspendYield} {
			fixture.child.header.SuspendReason = uint16(reason)
			fixture.child.header.Lifecycle = uint16(FrameSuspended)
			if RecoverAliasScopeActive(fixture.g) {
				t.Fatalf("cancellation %d accepted stale suspended header %d", kind, reason)
			}
			fixture.child.header.SuspendReason = uint16(SuspendNone)
			if RecoverAliasScopeActive(fixture.g) {
				t.Fatalf("cancellation %d accepted partially activated header", kind)
			}
			fixture.child.header.Lifecycle = uint16(FrameActive)
			if !RecoverAliasScopeActive(fixture.g) {
				t.Fatalf("cancellation %d rejected reactivated header", kind)
			}
		}
		fixture.g.park.taskCancelKind = TaskCancelNone
		fixture.g.park.taskCancelPhase = taskCancelIdle
		completeRecoverChild(t, fixture)
		if _, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); !ok {
			t.Fatal("consume cleanup activation fixture")
		}
		fixture.keepAlive()
	}
}

func TestRecoverTransparentAliasTakesExactAncestorScope(t *testing.T) {
	typeWord := unsafe.Pointer(new(byte))
	dataWord := unsafe.Pointer(new(byte))
	fixture := newInlineAwaitFixtureWithRecovery(t, true, typeWord, dataWord)
	wrapper := FrameFromStorage(fixture.child.storage)
	if wrapper == nil {
		t.Fatal("transparent recover wrapper metadata is absent")
	}
	leafFrame := newTestFrame(t, fixture.g, unsafe.Pointer(new(byte)), fixture.child.handle)
	wrapper.header.SuspendReason = uint16(SuspendCall)
	wrapper.header.Lifecycle = uint16(FrameSuspended)
	if disposition := PrepareInlineAwaitCompiler(
		fixture.g, fixture.child.handle, leafFrame.handle, nil, nil,
	); disposition != InlineAwaitStarted {
		t.Fatalf("begin transparent recover leaf = %d, want started", disposition)
	}
	if outcome, caseID, task, source, generation, taken := TakeRunDecisionWordsCompiler(
		fixture.g, 0, 0,
	); !taken || outcome != 0 || caseID != 0 || task != 0 || source != 0 || generation != 0 {
		t.Fatalf(
			"take transparent recover leaf initial gate = (%d, %d, %d, %d, %d, %t)",
			outcome, caseID, task, source, generation, taken,
		)
	}
	leafFrame.header.SuspendReason = uint16(SuspendNone)
	leafFrame.header.Lifecycle = uint16(FrameActive)
	if !DeferredPanicTraceActive(fixture.g) {
		t.Fatal("ordinary inline descendant lost its inherited recovery scope")
	}

	if snapshot, recovered, valid := TakeRecover(
		fixture.g, leafFrame.handle,
	); !valid || recovered || snapshot != (RecoverSnapshot{}) {
		t.Fatalf("ordinary nested recover = (%+v, %t, %t), want nil/false/true", snapshot, recovered, valid)
	}
	want := RecoverSnapshot{TypeWord: typeWord, DataWord: dataWord}
	if snapshot, recovered, valid := TakeRecoverAlias(
		fixture.g, leafFrame.handle,
	); !valid || !recovered || snapshot != want {
		t.Fatalf("transparent nested recover = (%+v, %t, %t), want (%+v, true, true)", snapshot, recovered, valid, want)
	}
	if !DeferredPanicTraceActive(fixture.g) {
		t.Fatal("recover hid the deferred child's still-live traceback scope")
	}
	root := FrameFromStorage(fixture.parent.storage)
	if root == nil || root.completion.status != completionRecoverTaken ||
		wrapper.completion.status != completionArmed {
		t.Fatalf("transparent recover records = root:%+v wrapper:%+v", root.completion, wrapper.completion)
	}
	if snapshot, recovered, valid := TakeRecoverAlias(
		fixture.g, leafFrame.handle,
	); !valid || recovered || snapshot != (RecoverSnapshot{}) {
		t.Fatalf("duplicate transparent recover = (%+v, %t, %t)", snapshot, recovered, valid)
	}

	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	runtime.KeepAlive(fixture.parent.memory)
	runtime.KeepAlive(fixture.child.memory)
	runtime.KeepAlive(leafFrame.memory)
}

func TestRecoverInRootFrameReturnsNil(t *testing.T) {
	checked := false
	fixture := newAwaitCompletionFixtureBeforeAwait(t, func(g *G, parent, _ *testFrame) {
		snapshot, recovered, valid := TakeRecover(g, parent.handle)
		if !valid || recovered || snapshot != (RecoverSnapshot{}) {
			t.Fatalf("root recover = (%+v, %t, %t)", snapshot, recovered, valid)
		}
		checked = true
	})
	if !checked {
		t.Fatal("root recover callback was not exercised")
	}
	completeRecoverChild(t, fixture)
	if snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); !ok || snapshot.Status != CompletionReturn {
		t.Fatalf("consume root-recover fixture child = (%+v, %t)", snapshot, ok)
	}
	fixture.keepAlive()
}

func TestRecoverCompletionTransactionRejectsDuplicatePrepare(t *testing.T) {
	typeWord := unsafe.Pointer(new(byte))
	dataWord := unsafe.Pointer(new(byte))
	fixture := newRecoverAwaitFixture(t, typeWord, dataWord)
	before := fixture.parentFrame().completion
	if PrepareAwaitCompletionRecover(fixture.g, fixture.parent.handle, fixture.child.handle, typeWord, dataWord) {
		t.Fatal("duplicate recoverable await accepted while child is active")
	}
	if fixture.parentFrame().completion != before {
		t.Fatal("duplicate recoverable await mutated the winning completion transaction")
	}
	if snapshot, recovered, valid := TakeRecover(fixture.g, fixture.parent.handle); valid || recovered || snapshot != (RecoverSnapshot{}) {
		t.Fatalf("non-active child identity accepted = (%+v, %t, %t)", snapshot, recovered, valid)
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	fixture.keepAlive()
}
