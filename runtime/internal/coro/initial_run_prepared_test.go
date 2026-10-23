// Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
// Use of this source code is governed by an Apache 2.0 license that can be
// found in the LICENSE file.

package coro

import (
	"runtime"
	"testing"
	"unsafe"
)

type initialRunFixture struct {
	p      *P
	parent *yieldingTestG
	child  *G
	root   *testFrame
}

func newInitialRunFixture(t *testing.T) initialRunFixture {
	t.Helper()
	p := new(P)
	parent := newYieldingTestG(t, "initial-run-parent")
	action := beginSpawnTestResume(t, p, parent)
	child := new(G)
	if !BeginSpawnCompiler(parent.g, child, unsafe.Pointer(child), TaskStorageSize()) {
		t.Fatal("begin compiler spawn")
	}
	root, _ := newSpawnTestFrame(t, child, unsafe.Pointer(new(byte)), 0, 1)
	if !CommitSpawnCompiler(parent.g, child, root.handle) {
		t.Fatal("commit compiler spawn")
	}
	yieldSpawnTestG(t, p, parent.g, parent.frame, action)
	return initialRunFixture{p, parent, child, root}
}
func TestInitialRunReceiptDispatchAndSize(t *testing.T) {
	f := newInitialRunFixture(t)
	defer runtime.KeepAlive(f.root.memory)
	if unsafe.Sizeof(uintptr(0)) == 8 && TaskStorageSize() != 264 {
		t.Fatalf("G storage grew to %d", TaskStorageSize())
	}
	if !f.child.initialRunPrepared || f.child.runAction != ActionInvalid {
		t.Fatal("initial child lost P-neutral publication")
	}
	driver := &ExecutorDriver{p: f.p}
	step, ok := dispatchExecutorRunReadyAction(driver, f.child, true)
	if !ok || !step.Dispatched || step.G != f.child || step.Action.Kind != ActionCheckResume || step.Action.Handle != f.root.handle || f.child.initialRunPrepared || f.child.runP != f.p || f.p.current != f.child || driver.run.issued != ActionCheckResume || f.p.readyHead != f.parent.g {
		t.Fatalf("initial dispatch=(%+v,%t)", step, ok)
	}
}
func TestInitialRunReceiptDoesNotWeakenPublicBegin(t *testing.T) {
	f := newInitialRunFixture(t)
	defer runtime.KeepAlive(f.root.memory)
	if dequeueReadyHeadUnchecked(f.p) != f.child {
		t.Fatal("dequeue child")
	}
	f.child.park.expected = 1
	if _, ok := BeginRunG(f.p, f.child); ok || f.p.current != nil || f.child.state != GRunnable || !f.child.initialRunPrepared {
		t.Fatal("public begin accepted malformed park cell through private receipt")
	}
}
func TestInitialRunReceiptRejectsMalformedCancelAndRestoresQueue(t *testing.T) {
	f := newInitialRunFixture(t)
	defer runtime.KeepAlive(f.root.memory)
	f.child.park.taskCancelPhase = taskCancelRequested
	if _, ok := dispatchExecutorRunReadyAction(&ExecutorDriver{p: f.p}, f.child, true); ok || f.p.current != nil || f.p.readyHead != f.child || f.child.nextReady != f.parent.g || f.p.readyCount != 2 || !f.child.queued || !f.child.initialRunPrepared {
		t.Fatal("invalid cancellation bypassed full validator or lost ready ownership")
	}
}
func TestInitialRunReceiptRetainsRequestedCancel(t *testing.T) {
	f := newInitialRunFixture(t)
	defer runtime.KeepAlive(f.root.memory)
	if !RequestTaskCancellation(f.p, f.child, TaskCancelAbort) {
		t.Fatal("request child cancellation")
	}
	step, ok := dispatchExecutorRunReadyAction(&ExecutorDriver{p: f.p}, f.child, true)
	if !ok || step.Action.Kind != ActionCheckResume || f.child.initialRunPrepared || f.child.park.taskCancelKind != TaskCancelAbort || f.child.park.taskCancelPhase != taskCancelRequested {
		t.Fatal("initial dispatch lost sticky cancellation")
	}
}
func TestInitialRunReceiptRetainsScheduleRejection(t *testing.T) {
	f := newInitialRunFixture(t)
	defer runtime.KeepAlive(f.root.memory)
	preemptStore(&f.p.schedule, ^uint32(0))
	if _, ok := dispatchExecutorRunReadyAction(&ExecutorDriver{p: f.p}, f.child, true); ok || f.p.current != nil || f.p.readyHead != f.child || !f.child.queued || !f.child.initialRunPrepared {
		t.Fatal("bad schedule consumed initial receipt or ready ownership")
	}
}
func TestInitialRunReceiptSurvivesPNeutralTransfer(t *testing.T) {
	f := newInitialRunFixture(t)
	defer runtime.KeepAlive(f.root.memory)
	target := new(P)
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind mailbox")
	}
	id, ok := PublishPNeutralRunnable(&mailbox, f.p, f.child)
	if !ok || !ImportPNeutralRunnable(&mailbox, target, id) || f.child.runAction != ActionInvalid || !f.child.initialRunPrepared {
		t.Fatal("initial receipt changed P-neutral transfer contract")
	}
	step, ok := dispatchExecutorRunReadyAction(&ExecutorDriver{p: target}, f.child, true)
	if !ok || step.Action.Kind != ActionCheckResume || step.Action.Handle != f.root.handle || f.child.initialRunPrepared || f.child.runP != target {
		t.Fatal("imported initial child did not enter target executor")
	}
}
func TestInitialRunReceiptRejectsDirtyInitialization(t *testing.T) {
	g := &G{initialRunPrepared: true}
	if InitG(g) || g.magic != 0 {
		t.Fatal("initial receipt accepted as zero G")
	}
}
