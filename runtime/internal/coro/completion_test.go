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

// awaitCompletionFixture stops with the child inside its first physical
// activation. Both normal return and explicit panic can therefore exercise the
// same child-publish, destroy/free, parent-resume transaction.
type awaitCompletionFixture struct {
	p      *P
	g      *G
	parent *testFrame
	child  *testFrame
	action Action
}

func newAwaitCompletionFixture(t *testing.T) *awaitCompletionFixture {
	return newAwaitCompletionFixtureBeforeAwait(t, nil)
}

func newAwaitCompletionFixtureBeforeAwait(
	t *testing.T,
	beforeAwait func(g *G, parent, child *testFrame),
) *awaitCompletionFixture {
	return newAwaitCompletionFixtureConfigured(t, beforeAwait, nil, nil)
}

func newAwaitCompletionFixtureConfigured(
	t *testing.T,
	beforeAwait func(g *G, parent, child *testFrame),
	recoverType, recoverData unsafe.Pointer,
) *awaitCompletionFixture {
	t.Helper()
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize completion G")
	}
	parentHandle := unsafe.Pointer(new(byte))
	childHandle := unsafe.Pointer(new(byte))
	parent := newTestFrame(t, g, parentHandle, nil)
	child := newTestFrame(t, g, childHandle, parentHandle)
	if !AdoptRoot(g, parentHandle) {
		t.Fatal("adopt completion root")
	}
	p := new(P)
	if !Enqueue(p, g) {
		t.Fatal("enqueue completion G")
	}
	if next, ok := NextRunnable(p); !ok || next != g {
		t.Fatalf("dequeue completion G = (%p, %t)", next, ok)
	}
	action, ok := BeginRunG(p, g)
	if !ok || action.Kind != ActionCheckResume || action.Handle != parentHandle {
		t.Fatalf("begin completion root = (%+v, %t)", action, ok)
	}
	action, ok = checkedTestAction(p, g, action, false)
	if !ok || action.Kind != ActionResume || action.Handle != parentHandle {
		t.Fatalf("activate completion root = (%+v, %t)", action, ok)
	}
	parent.header.SuspendReason = uint16(SuspendNone)
	parent.header.Lifecycle = uint16(FrameActive)
	if beforeAwait != nil {
		beforeAwait(g, parent, child)
	}
	parent.header.SuspendReason = uint16(SuspendCall)
	parent.header.Lifecycle = uint16(FrameSuspended)
	var prepared bool
	if recoverType != nil {
		prepared = PrepareAwaitCompletionRecover(g, parentHandle, childHandle, recoverType, recoverData)
	} else {
		prepared = PrepareAwaitCompletion(g, parentHandle, childHandle)
	}
	if !prepared {
		t.Fatal("prepare completion child await")
	}
	action, ok = Resumed(p, g, action)
	if !ok || action.Kind != ActionCheckResume || action.Handle != childHandle {
		t.Fatalf("dispatch completion child = (%+v, %t)", action, ok)
	}
	action, ok = checkedTestAction(p, g, action, false)
	if !ok || action.Kind != ActionResume || action.Handle != childHandle {
		t.Fatalf("activate completion child = (%+v, %t)", action, ok)
	}
	child.header.SuspendReason = uint16(SuspendNone)
	child.header.Lifecycle = uint16(FrameActive)
	return &awaitCompletionFixture{p: p, g: g, parent: parent, child: child, action: action}
}

func (fixture *awaitCompletionFixture) destroyChildAndResumeParent(t *testing.T) {
	t.Helper()
	action, ok := Resumed(fixture.p, fixture.g, fixture.action)
	if !ok || action.Kind != ActionCheckDestroy || action.Handle != fixture.child.handle {
		t.Fatalf("dispatch completion child destroy = (%+v, %t)", action, ok)
	}
	if snapshot, consumed := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); consumed || snapshot != (CompletionSnapshot{}) {
		t.Fatalf("completion consumed before child destroy = (%+v, %t)", snapshot, consumed)
	}
	action, ok = checkedTestAction(fixture.p, fixture.g, action, true)
	if !ok || action.Kind != ActionDestroy || action.Handle != fixture.child.handle {
		t.Fatalf("activate completion child destroy = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, fixture.g, fixture.child)
	action, ok = Destroyed(fixture.p, fixture.g, action)
	if !ok || action.Kind != ActionCheckResume || action.Handle != fixture.parent.handle {
		t.Fatalf("commit completion child destroy = (%+v, %t)", action, ok)
	}
	if snapshot, consumed := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); consumed || snapshot != (CompletionSnapshot{}) {
		t.Fatalf("completion consumed before parent activation = (%+v, %t)", snapshot, consumed)
	}
	action, ok = checkedTestAction(fixture.p, fixture.g, action, false)
	if !ok || action.Kind != ActionResume || action.Handle != fixture.parent.handle {
		t.Fatalf("reactivate completion parent = (%+v, %t)", action, ok)
	}
	fixture.parent.header.SuspendReason = uint16(SuspendNone)
	fixture.parent.header.Lifecycle = uint16(FrameActive)
	fixture.action = action
}

func (fixture *awaitCompletionFixture) keepAlive() {
	runtime.KeepAlive(fixture.parent.memory)
	runtime.KeepAlive(fixture.child.memory)
}

func TestAwaitCompletionReturnConsumedAfterChildDestroy(t *testing.T) {
	fixture := newAwaitCompletionFixture(t)
	fixture.child.header.SuspendReason = uint16(SuspendFrameComplete)
	fixture.child.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(fixture.g, fixture.child.handle, fixture.child.header) {
		t.Fatal("publish child return completion")
	}
	wantRecord := CompletionRecord{status: CompletionReturn, child: fixture.child.handle}
	if fixture.parentFrame().completion != wantRecord {
		t.Fatalf("published return record = %+v, want %+v", fixture.parentFrame().completion, wantRecord)
	}
	if PrepareComplete(fixture.g, fixture.child.handle, fixture.child.header) {
		t.Fatal("duplicate child return publication accepted")
	}
	if fixture.parentFrame().completion != wantRecord {
		t.Fatal("duplicate child return publication mutated the winning record")
	}
	if snapshot, consumed := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); consumed || snapshot != (CompletionSnapshot{}) {
		t.Fatalf("return completion consumed while child was live = (%+v, %t)", snapshot, consumed)
	}

	fixture.destroyChildAndResumeParent(t)
	snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle)
	if !ok || snapshot != (CompletionSnapshot{Status: CompletionReturn}) {
		t.Fatalf("consume child return = (%+v, %t)", snapshot, ok)
	}
	if !emptyCompletionRecord(&fixture.parentFrame().completion) {
		t.Fatalf("return consume retained parent record: %+v", fixture.parentFrame().completion)
	}
	if duplicate, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); ok || duplicate != (CompletionSnapshot{}) {
		t.Fatalf("duplicate return consume = (%+v, %t)", duplicate, ok)
	}
	fixture.keepAlive()
}

func TestAwaitCompletionCancellationPropagatesAcrossAncestors(t *testing.T) {
	tests := []struct {
		name   string
		kind   TaskCancelKind
		status CompletionStatus
	}{
		{name: "abort", kind: TaskCancelAbort, status: CompletionAbort},
		{name: "shutdown", kind: TaskCancelShutdown, status: CompletionShutdown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := new(G)
			if !InitG(g) {
				t.Fatal("initialize cancellation propagation G")
			}
			root := newTestFrame(t, g, unsafe.Pointer(new(byte)), nil)
			middle := newTestFrame(t, g, unsafe.Pointer(new(byte)), root.handle)
			leaf := newTestFrame(t, g, unsafe.Pointer(new(byte)), middle.handle)
			if !AdoptRoot(g, root.handle) {
				t.Fatal("adopt cancellation propagation root")
			}
			p := new(P)
			if !Enqueue(p, g) {
				t.Fatal("enqueue cancellation propagation G")
			}
			if next, ok := NextRunnable(p); !ok || next != g {
				t.Fatalf("dequeue cancellation propagation G = (%p, %t)", next, ok)
			}
			action, ok := BeginRunG(p, g)
			if !ok || action.Kind != ActionCheckResume || action.Handle != root.handle {
				t.Fatalf("begin cancellation propagation root = (%+v, %t)", action, ok)
			}
			activateNormal := func(frame *testFrame) {
				t.Helper()
				var active bool
				action, active = checkedTestAction(p, g, action, false)
				if !active || action.Kind != ActionResume || action.Handle != frame.handle {
					t.Fatalf("activate cancellation propagation frame = (%+v, %t), want %p", action, active, frame.handle)
				}
				frame.header.SuspendReason = uint16(SuspendNone)
				frame.header.Lifecycle = uint16(FrameActive)
			}
			await := func(parent, child *testFrame) {
				t.Helper()
				parent.header.SuspendReason = uint16(SuspendCall)
				parent.header.Lifecycle = uint16(FrameSuspended)
				if !PrepareAwaitCompletion(g, parent.handle, child.handle) {
					t.Fatal("prepare cancellation propagation await")
				}
				var resumed bool
				action, resumed = Resumed(p, g, action)
				if !resumed || action.Kind != ActionCheckResume || action.Handle != child.handle {
					t.Fatalf("dispatch cancellation propagation child = (%+v, %t)", action, resumed)
				}
				activateNormal(child)
			}
			activateNormal(root)
			await(root, middle)
			await(middle, leaf)

			if !RequestTaskCancellation(p, g, test.kind) {
				t.Fatal("request task cancellation at active leaf")
			}
			leaf.header.SuspendReason = uint16(SuspendFrameComplete)
			leaf.header.Lifecycle = uint16(FrameFinalSuspended)
			if PrepareCompleteStatus(g, leaf.handle, leaf.header, test.status) {
				t.Fatal("published cancellation before its resume gate claim")
			}
			leaf.header.SuspendReason = uint16(SuspendYield)
			leaf.header.Lifecycle = uint16(FrameSuspended)
			if !PrepareYield(g, leaf.handle, leaf.header) {
				t.Fatal("prepare leaf cancellation safepoint")
			}
			action, ok = Resumed(p, g, action)
			if !ok || action.Kind != ActionYield {
				t.Fatalf("yield leaf for cancellation = (%+v, %t)", action, ok)
			}
			if next, ready := NextRunnable(p); !ready || next != g {
				t.Fatalf("dequeue canceled leaf = (%p, %t)", next, ready)
			}
			action, ok = BeginRunG(p, g)
			if !ok || action.Kind != ActionCheckResume || action.Handle != leaf.handle {
				t.Fatalf("begin canceled leaf = (%+v, %t)", action, ok)
			}
			action, ok = Checked(p, g, action, false)
			if !ok || action.Kind != ActionResume || action.Handle != leaf.handle {
				t.Fatalf("select canceled leaf resume = (%+v, %t)", action, ok)
			}
			outcome, caseID, lease, kind, taken := TakeRunDecision(g, ParkTicket{})
			if !taken || outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) || kind != test.kind {
				t.Fatalf("take leaf cancellation = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, kind, taken)
			}
			leaf.header.SuspendReason = uint16(SuspendNone)
			leaf.header.Lifecycle = uint16(FrameActive)
			wrong := CompletionAbort
			if test.status == CompletionAbort {
				wrong = CompletionShutdown
			}
			leaf.header.SuspendReason = uint16(SuspendFrameComplete)
			leaf.header.Lifecycle = uint16(FrameFinalSuspended)
			if PrepareCompleteStatus(g, leaf.handle, leaf.header, wrong) {
				t.Fatal("published cancellation status that mismatches the claimed token")
			}

			completeAndResumeParent := func(frame, parent *testFrame) {
				t.Helper()
				if !PrepareCompleteStatus(g, frame.handle, frame.header, test.status) {
					t.Fatal("publish frame cancellation completion")
				}
				var resumed bool
				action, resumed = Resumed(p, g, action)
				if !resumed || action.Kind != ActionCheckDestroy || action.Handle != frame.handle {
					t.Fatalf("dispatch canceled frame destroy = (%+v, %t)", action, resumed)
				}
				action, resumed = checkedTestAction(p, g, action, true)
				if !resumed || action.Kind != ActionDestroy || action.Handle != frame.handle {
					t.Fatalf("activate canceled frame destroy = (%+v, %t)", action, resumed)
				}
				releaseTestFrame(t, g, frame)
				action, resumed = Destroyed(p, g, action)
				if !resumed || action.Kind != ActionCheckResume || action.Handle != parent.handle {
					t.Fatalf("resume cancellation ancestor = (%+v, %t)", action, resumed)
				}
				activateNormal(parent)
				snapshot, consumed := ConsumeAwaitCompletion(g, parent.handle)
				if !consumed || snapshot != (CompletionSnapshot{Status: test.status}) {
					t.Fatalf("consume propagated cancellation = (%+v, %t)", snapshot, consumed)
				}
				parent.header.SuspendReason = uint16(SuspendFrameComplete)
				parent.header.Lifecycle = uint16(FrameFinalSuspended)
			}
			completeAndResumeParent(leaf, middle)
			completeAndResumeParent(middle, root)
			if !PrepareCompleteStatus(g, root.handle, root.header, test.status) {
				t.Fatal("publish root cancellation completion")
			}
			action, ok = Resumed(p, g, action)
			if !ok || action.Kind != ActionCheckDestroy || action.Handle != root.handle {
				t.Fatalf("dispatch canceled root destroy = (%+v, %t)", action, ok)
			}
			action, ok = checkedTestAction(p, g, action, true)
			if !ok || action.Kind != ActionDestroy || action.Handle != root.handle {
				t.Fatalf("activate canceled root destroy = (%+v, %t)", action, ok)
			}
			releaseTestFrame(t, g, root)
			action, ok = Destroyed(p, g, action)
			if !ok || action.Kind != ActionComplete || action.Handle != nil {
				t.Fatalf("commit canceled root destroy = (%+v, %t)", action, ok)
			}
			if !AcknowledgeTaskCancellation(g, test.kind) || !TerminalG(p, g) {
				t.Fatal("canceled task did not reach reclaimable terminal state")
			}
			runtime.KeepAlive(root.memory)
			runtime.KeepAlive(middle.memory)
			runtime.KeepAlive(leaf.memory)
		})
	}
}

func TestAwaitCompletionPanicConsumedAfterChildDestroy(t *testing.T) {
	fixture := newAwaitCompletionFixture(t)
	typeWord := unsafe.Pointer(new(byte))
	dataWord := unsafe.Pointer(new(byte))
	fixture.child.header.SuspendReason = uint16(SuspendPanic)
	fixture.child.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PreparePanic(fixture.g, fixture.child.handle, fixture.child.header, typeWord, dataWord) {
		t.Fatal("publish child explicit panic")
	}
	wantRecord := CompletionRecord{
		status: CompletionPanic, child: fixture.child.handle,
		typeWord: typeWord, dataWord: dataWord,
	}
	if fixture.parentFrame().completion != wantRecord {
		t.Fatalf("published panic record = %+v, want %+v", fixture.parentFrame().completion, wantRecord)
	}
	if fixture.g.panicUnwind || !emptyPanicRecord(&fixture.g.panicRecord) {
		t.Fatalf("child panic escaped parent reconciliation: completion=%+v unwind=%t panic=%+v",
			fixture.parentFrame().completion, fixture.g.panicUnwind, fixture.g.panicRecord)
	}
	fixture.destroyChildAndResumeParent(t)
	snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle)
	want := CompletionSnapshot{Status: CompletionPanic, TypeWord: typeWord, DataWord: dataWord}
	if !ok || snapshot != want {
		t.Fatalf("consume child panic = (%+v, %t), want %+v", snapshot, ok, want)
	}
	if fixture.g.panicUnwind || !emptyPanicRecord(&fixture.g.panicRecord) ||
		!emptyCompletionRecord(&fixture.parentFrame().completion) {
		t.Fatalf("panic consume retained terminal state: completion=%+v unwind=%t panic=%+v",
			fixture.parentFrame().completion, fixture.g.panicUnwind, fixture.g.panicRecord)
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	fixture.keepAlive()
}

func TestAwaitCompletionDuplicatePanicPublicationFailsClosed(t *testing.T) {
	fixture := newAwaitCompletionFixture(t)
	typeWord := unsafe.Pointer(new(byte))
	dataWord := unsafe.Pointer(new(byte))
	fixture.child.header.SuspendReason = uint16(SuspendPanic)
	fixture.child.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PreparePanic(fixture.g, fixture.child.handle, fixture.child.header, typeWord, dataWord) {
		t.Fatal("publish child explicit panic")
	}
	wantRecord := fixture.parentFrame().completion
	if PreparePanic(fixture.g, fixture.child.handle, fixture.child.header, typeWord, dataWord) {
		t.Fatal("duplicate child panic publication accepted")
	}
	if fixture.parentFrame().completion != wantRecord || fixture.g.panicUnwind || !emptyPanicRecord(&fixture.g.panicRecord) {
		t.Fatalf("duplicate child panic mutated scheduler state: completion=%+v want=%+v unwind=%t panic=%+v",
			fixture.parentFrame().completion, wantRecord, fixture.g.panicUnwind, fixture.g.panicRecord)
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	fixture.keepAlive()
}

func TestAwaitCompletionMalformedRecordsFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CompletionRecord, *awaitCompletionFixture)
	}{
		{"armed", func(record *CompletionRecord, _ *awaitCompletionFixture) { record.status = completionArmed }},
		{"none", func(record *CompletionRecord, _ *awaitCompletionFixture) { record.status = CompletionNone }},
		{"unknown", func(record *CompletionRecord, _ *awaitCompletionFixture) { record.status = CompletionStatus(77) }},
		{"missing-child", func(record *CompletionRecord, _ *awaitCompletionFixture) { record.child = nil }},
		{"live-child", func(record *CompletionRecord, fixture *awaitCompletionFixture) { record.child = fixture.parent.handle }},
		{"return-type-payload", func(record *CompletionRecord, _ *awaitCompletionFixture) { record.typeWord = unsafe.Pointer(new(byte)) }},
		{"return-data-payload", func(record *CompletionRecord, _ *awaitCompletionFixture) { record.dataWord = unsafe.Pointer(new(byte)) }},
		{"panic-missing-type", func(record *CompletionRecord, _ *awaitCompletionFixture) { record.status = CompletionPanic }},
		{"abort-payload", func(record *CompletionRecord, _ *awaitCompletionFixture) {
			record.status = CompletionAbort
			record.dataWord = unsafe.Pointer(new(byte))
		}},
		{"shutdown-payload", func(record *CompletionRecord, _ *awaitCompletionFixture) {
			record.status = CompletionShutdown
			record.typeWord = unsafe.Pointer(new(byte))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAwaitCompletionFixture(t)
			fixture.child.header.SuspendReason = uint16(SuspendFrameComplete)
			fixture.child.header.Lifecycle = uint16(FrameFinalSuspended)
			if !PrepareComplete(fixture.g, fixture.child.handle, fixture.child.header) {
				t.Fatal("publish child return completion")
			}
			fixture.destroyChildAndResumeParent(t)
			test.mutate(&fixture.parentFrame().completion, fixture)
			before := fixture.parentFrame().completion
			if snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); ok || snapshot != (CompletionSnapshot{}) {
				t.Fatalf("malformed consume = (%+v, %t), record=%+v", snapshot, ok, before)
			}
			if fixture.parentFrame().completion != before {
				t.Fatalf("rejected malformed consume mutated record: got %+v, want %+v",
					fixture.parentFrame().completion, before)
			}
			fixture.keepAlive()
		})
	}
}

func TestAwaitCompletionWrongParentHandleFailsClosed(t *testing.T) {
	fixture := newAwaitCompletionFixture(t)
	fixture.child.header.SuspendReason = uint16(SuspendFrameComplete)
	fixture.child.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(fixture.g, fixture.child.handle, fixture.child.header) {
		t.Fatal("publish child return completion")
	}
	fixture.destroyChildAndResumeParent(t)
	before := fixture.parentFrame().completion
	if snapshot, ok := ConsumeAwaitCompletion(fixture.g, unsafe.Pointer(new(byte))); ok || snapshot != (CompletionSnapshot{}) {
		t.Fatalf("wrong-parent consume = (%+v, %t)", snapshot, ok)
	}
	if fixture.parentFrame().completion != before {
		t.Fatal("wrong-parent consume mutated the valid record")
	}
	if _, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle); !ok {
		t.Fatal("wrong-parent attempt poisoned the valid completion")
	}
	fixture.keepAlive()
}

func (fixture *awaitCompletionFixture) parentFrame() *Frame {
	return FrameFromStorage(fixture.parent.storage)
}
