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

type inlineAwaitFixture struct {
	p      *P
	g      *G
	parent *testFrame
	child  *testFrame
	action Action
}

func newInlineAwaitFixture(t *testing.T) *inlineAwaitFixture {
	t.Helper()
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize inline-await G")
	}
	parent := newTestFrame(t, g, unsafe.Pointer(new(byte)), nil)
	child := newTestFrame(t, g, unsafe.Pointer(new(byte)), parent.handle)
	if !AdoptRoot(g, parent.handle) {
		t.Fatal("adopt inline-await root")
	}
	p := new(P)
	if !Enqueue(p, g) {
		t.Fatal("enqueue inline-await G")
	}
	if next, ok := NextRunnable(p); !ok || next != g {
		t.Fatalf("dequeue inline-await G = (%p, %t)", next, ok)
	}
	action, ok := BeginRunG(p, g)
	if !ok || action.Kind != ActionCheckResume || action.Handle != parent.handle {
		t.Fatalf("begin inline-await root = (%+v, %t)", action, ok)
	}
	action, ok = checkedTestAction(p, g, action, false)
	if !ok || action.Kind != ActionResume || action.Handle != parent.handle {
		t.Fatalf("activate inline-await root = (%+v, %t)", action, ok)
	}
	parent.header.SuspendReason = uint16(SuspendCall)
	parent.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareAwaitCompletion(g, parent.handle, child.handle) {
		t.Fatal("prepare inline-await child")
	}
	if got := BeginInlineAwait(g, parent.handle, child.handle); got != InlineAwaitStarted {
		t.Fatalf("begin inline-await = %d, want started", got)
	}
	if outcome, caseID, lease, task, taken := TakeRunDecision(g, ParkTicket{}); !taken || outcome != ParkOutcomePending || caseID != 0 ||
		lease != (OperationResultLease{}) || task != TaskCancelNone {
		t.Fatalf("take inline child initial gate = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, task, taken)
	}
	child.header.SuspendReason = uint16(SuspendNone)
	child.header.Lifecycle = uint16(FrameActive)
	return &inlineAwaitFixture{p: p, g: g, parent: parent, child: child, action: action}
}

func (fixture *inlineAwaitFixture) finishFastChild(t *testing.T) {
	t.Helper()
	if got := FinishInlineAwait(
		fixture.g, fixture.parent.handle, fixture.child.handle, true,
	); got != InlineAwaitDestroy {
		t.Fatalf("finish inline-await = %d, want destroy", got)
	}
	if fixture.p.inlineAwaitDepth != 0 || fixture.g.active != FrameFromStorage(fixture.parent.storage) {
		t.Fatalf("inline destroy did not restore parent ownership: depth=%d active=%p",
			fixture.p.inlineAwaitDepth, fixture.g.active)
	}
	releaseTestFrame(t, fixture.g, fixture.child)
	if !CommitInlineAwaitDestroy(fixture.g, fixture.parent.handle, fixture.child.handle) {
		t.Fatal("commit inline child destroy")
	}
}

func TestInlineAwaitCompletesAndConsumesWithoutSchedulerRoundTrip(t *testing.T) {
	fixture := newInlineAwaitFixture(t)
	fixture.child.header.SuspendReason = uint16(SuspendFrameComplete)
	fixture.child.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareCompleteStatus(
		fixture.g, fixture.child.handle, fixture.child.header, CompletionReturn,
	) {
		t.Fatal("publish inline child return")
	}
	fixture.finishFastChild(t)
	snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle)
	if !ok || snapshot != (CompletionSnapshot{Status: CompletionReturn}) {
		t.Fatalf("consume inline child return = (%+v, %t)", snapshot, ok)
	}
	if fixture.parent.header.SuspendReason != uint16(SuspendNone) ||
		fixture.parent.header.Lifecycle != uint16(FrameActive) {
		t.Fatalf("inline parent header = (%d, %d), want active",
			fixture.parent.header.SuspendReason, fixture.parent.header.Lifecycle)
	}
	runtime.KeepAlive(fixture.parent.memory)
}

func TestInlineAwaitPanicRetainsExistingCompletionProtocol(t *testing.T) {
	fixture := newInlineAwaitFixture(t)
	typeWord, dataWord := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	fixture.child.header.SuspendReason = uint16(SuspendPanic)
	fixture.child.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PreparePanic(fixture.g, fixture.child.handle, fixture.child.header, typeWord, dataWord) {
		t.Fatal("publish inline child panic")
	}
	fixture.finishFastChild(t)
	snapshot, ok := ConsumeAwaitCompletion(fixture.g, fixture.parent.handle)
	if !ok || snapshot != (CompletionSnapshot{
		Status: CompletionPanic, TypeWord: typeWord, DataWord: dataWord,
	}) {
		t.Fatalf("consume inline child panic = (%+v, %t)", snapshot, ok)
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	runtime.KeepAlive(fixture.parent.memory)
}

func TestInlineAwaitSlowYieldUnwindsBeforeSchedulerDispatch(t *testing.T) {
	fixture := newInlineAwaitFixture(t)
	fixture.child.header.SuspendReason = uint16(SuspendYield)
	fixture.child.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(fixture.g, fixture.child.handle, fixture.child.header) {
		t.Fatal("publish inline child yield")
	}
	if got := FinishInlineAwait(
		fixture.g, fixture.parent.handle, fixture.child.handle, false,
	); got != InlineAwaitSuspend {
		t.Fatalf("finish yielding inline child = %d, want suspend", got)
	}
	if fixture.p.inlineAwaitDepth != 0 || fixture.g.active != FrameFromStorage(fixture.child.storage) {
		t.Fatalf("yielding inline child ownership: depth=%d active=%p",
			fixture.p.inlineAwaitDepth, fixture.g.active)
	}
	action, ok := Resumed(fixture.p, fixture.g, fixture.action)
	if !ok || action.Kind != ActionYield || action.Handle != nil ||
		fixture.g.state != GRunnable || !fixture.g.queued {
		t.Fatalf("dispatch unwound inline yield = (%+v, %t), state=%d queued=%t",
			action, ok, fixture.g.state, fixture.g.queued)
	}
	runtime.KeepAlive(fixture.parent.memory)
	runtime.KeepAlive(fixture.child.memory)
}

func TestNestedInlineAwaitLeavesOnlyDeepestPendingTransition(t *testing.T) {
	fixture := newInlineAwaitFixture(t)
	grandchild := newTestFrame(t, fixture.g, unsafe.Pointer(new(byte)), fixture.child.handle)
	fixture.child.header.SuspendReason = uint16(SuspendCall)
	fixture.child.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareAwaitCompletion(fixture.g, fixture.child.handle, grandchild.handle) {
		t.Fatal("prepare nested inline child")
	}
	if got := BeginInlineAwait(fixture.g, fixture.child.handle, grandchild.handle); got != InlineAwaitStarted {
		t.Fatalf("begin nested inline child = %d", got)
	}
	takeNormalResumeGateForTest(t, fixture.g)
	grandchild.header.SuspendReason = uint16(SuspendYield)
	grandchild.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(fixture.g, grandchild.handle, grandchild.header) {
		t.Fatal("publish nested inline yield")
	}
	if got := FinishInlineAwait(
		fixture.g, fixture.child.handle, grandchild.handle, false,
	); got != InlineAwaitSuspend || fixture.p.inlineAwaitDepth != 1 {
		t.Fatalf("finish nested inline child = %d, depth=%d", got, fixture.p.inlineAwaitDepth)
	}
	if got := FinishInlineAwait(
		fixture.g, fixture.parent.handle, fixture.child.handle, false,
	); got != InlineAwaitSuspend || fixture.p.inlineAwaitDepth != 0 {
		t.Fatalf("finish outer inline child = %d, depth=%d", got, fixture.p.inlineAwaitDepth)
	}
	action, ok := Resumed(fixture.p, fixture.g, fixture.action)
	if !ok || action.Kind != ActionYield || fixture.g.active != FrameFromStorage(grandchild.storage) {
		t.Fatalf("dispatch nested inline yield = (%+v, %t), active=%p",
			action, ok, fixture.g.active)
	}
	runtime.KeepAlive(fixture.parent.memory)
	runtime.KeepAlive(fixture.child.memory)
	runtime.KeepAlive(grandchild.memory)
}

func TestInlineAwaitSlowPathBindsImmediateChild(t *testing.T) {
	fixture := newInlineAwaitFixture(t)
	sibling := newTestFrame(t, fixture.g, unsafe.Pointer(new(byte)), fixture.parent.handle)
	// Model a corrupt second direct child without changing the parent's exact
	// completion record, which continues to own fixture.child.
	FrameFromStorage(sibling.storage).parent = FrameFromStorage(fixture.parent.storage)
	fixture.child.header.SuspendReason = uint16(SuspendYield)
	fixture.child.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(fixture.g, fixture.child.handle, fixture.child.header) {
		t.Fatal("publish inline child yield")
	}
	beforeDepth := fixture.p.inlineAwaitDepth
	beforePending := fixture.g.pending
	if got := FinishInlineAwait(
		fixture.g, fixture.parent.handle, sibling.handle, false,
	); got != InlineAwaitInvalid {
		t.Fatalf("finish wrong immediate child = %d, want invalid", got)
	}
	if fixture.p.inlineAwaitDepth != beforeDepth || fixture.g.pending != beforePending ||
		fixture.g.active != FrameFromStorage(fixture.child.storage) {
		t.Fatal("rejected immediate-child mismatch mutated inline transaction")
	}
	runtime.KeepAlive(fixture.parent.memory)
	runtime.KeepAlive(fixture.child.memory)
	runtime.KeepAlive(sibling.memory)
}

func TestInlineAwaitDepthBoundDeclinesWithoutMutation(t *testing.T) {
	fixture := newInlineAwaitFixture(t)
	frames := []*testFrame{fixture.parent, fixture.child}
	active := fixture.child
	for fixture.p.inlineAwaitDepth < maxInlineAwaitDepth {
		next := newTestFrame(t, fixture.g, unsafe.Pointer(new(byte)), active.handle)
		frames = append(frames, next)
		active.header.SuspendReason = uint16(SuspendCall)
		active.header.Lifecycle = uint16(FrameSuspended)
		if !PrepareAwaitCompletion(fixture.g, active.handle, next.handle) {
			t.Fatalf("prepare inline depth %d", fixture.p.inlineAwaitDepth+1)
		}
		if got := BeginInlineAwait(fixture.g, active.handle, next.handle); got != InlineAwaitStarted {
			t.Fatalf("begin inline depth %d = %d", fixture.p.inlineAwaitDepth+1, got)
		}
		if outcome, caseID, lease, task, taken := TakeRunDecision(fixture.g, ParkTicket{}); !taken ||
			outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) ||
			task != TaskCancelNone {
			t.Fatalf("take inline depth %d initial gate", fixture.p.inlineAwaitDepth)
		}
		next.header.SuspendReason = uint16(SuspendNone)
		next.header.Lifecycle = uint16(FrameActive)
		active = next
	}

	declined := newTestFrame(t, fixture.g, unsafe.Pointer(new(byte)), active.handle)
	frames = append(frames, declined)
	active.header.SuspendReason = uint16(SuspendCall)
	active.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareAwaitCompletion(fixture.g, active.handle, declined.handle) {
		t.Fatal("prepare depth-bound child")
	}
	before := fixture.g.pending
	if got := BeginInlineAwait(fixture.g, active.handle, declined.handle); got != InlineAwaitDeclined {
		t.Fatalf("depth-bound inline await = %d, want declined", got)
	}
	if fixture.g.pending != before || fixture.g.active != FrameFromStorage(active.storage) ||
		FrameFromStorage(active.storage).state != FrameActive ||
		FrameFromStorage(declined.storage).state != FrameInitialSuspended ||
		fixture.p.inlineAwaitDepth != maxInlineAwaitDepth {
		t.Fatal("depth-bound refusal mutated prepared scheduler transaction")
	}
	for _, frame := range frames {
		runtime.KeepAlive(frame.memory)
	}
}
