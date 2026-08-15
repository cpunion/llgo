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

func newSpawnTestFrame(t *testing.T, g *G, handle unsafe.Pointer, resultSize, resultAlign uintptr) (*testFrame, *FrameDescriptorV1) {
	t.Helper()
	const (
		size  = uintptr(37)
		align = uintptr(16)
	)
	total, ok := FrameAllocationSize(size, align)
	if !ok {
		t.Fatal("compute spawn test frame allocation")
	}
	memory := make([]byte, total)
	descriptor := &FrameDescriptorV1{
		Version:     1,
		HashLo:      0x0102030405060708,
		HashHi:      0x1112131415161718,
		ResultSize:  resultSize,
		ResultAlign: resultAlign,
	}
	descriptorPointer := unsafe.Pointer(descriptor)
	storage, ok := RegisterFrame(g, unsafe.Pointer(&memory[0]), total, size, align, descriptorPointer)
	if !ok {
		t.Fatal("register spawn test frame")
	}
	header := &HeaderV1{
		G:             unsafe.Pointer(g),
		Descriptor:    descriptorPointer,
		SuspendReason: uint16(SuspendNone),
		Lifecycle:     uint16(FrameInitialSuspended),
	}
	if !PublishFrame(g, handle, header, storage) {
		t.Fatal("publish spawn test frame")
	}
	return &testFrame{
		handle:     handle,
		header:     header,
		storage:    storage,
		descriptor: descriptorPointer,
		size:       size,
		align:      align,
		memory:     memory,
	}, descriptor
}

func beginSpawnTestResume(t *testing.T, p *P, task *yieldingTestG) Action {
	t.Helper()
	action, ok := BeginRunG(p, task.g)
	if !ok || action.Kind != ActionCheckResume {
		t.Fatalf("begin spawn test G %s = (%+v, %t)", task.name, action, ok)
	}
	action, ok = checkedTestAction(p, task.g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatalf("activate spawn test G %s = (%+v, %t)", task.name, action, ok)
	}
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	return action
}

func beginSpawnTestChildResume(t *testing.T, p *P, g *G, frame *testFrame) Action {
	t.Helper()
	action, ok := BeginRunG(p, g)
	if !ok || action.Kind != ActionCheckResume {
		t.Fatalf("begin spawned child = (%+v, %t)", action, ok)
	}
	action, ok = checkedTestAction(p, g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatalf("activate spawned child = (%+v, %t)", action, ok)
	}
	frame.header.SuspendReason = uint16(SuspendNone)
	frame.header.Lifecycle = uint16(FrameActive)
	return action
}

func TestCompilerSpawnUsesAdjacentTransactionCertificates(t *testing.T) {
	p := new(P)
	parent := newYieldingTestG(t, "compiler-spawn-parent")
	_ = beginSpawnTestResume(t, p, parent)
	child := new(G)
	if !BeginSpawnCompiler(parent.g, child, unsafe.Pointer(child), TaskStorageSize()) {
		t.Fatal("begin compiler spawn")
	}
	if parent.g.spawnChild != child || child.spawnParent != parent.g || child.spawnP != p ||
		child.taskStorage != unsafe.Pointer(child) || child.taskSize != TaskStorageSize() ||
		child.taskState != taskStorageOwned || !gPreemptStateAtDepthZero(child, preemptIdle) {
		t.Fatalf("compiler spawn begin state: parent-child=%p child-parent=%p child-p=%p storage=%p size=%d state=%d",
			parent.g.spawnChild, child.spawnParent, child.spawnP, child.taskStorage, child.taskSize, child.taskState)
	}
	local := unsafe.Pointer(new(byte))
	if !BindTaskLocalCompiler(child, local) || TaskLocal(child) != local ||
		BindTaskLocalCompiler(child, unsafe.Pointer(new(byte))) {
		t.Fatal("compiler spawn task-local binding did not publish exactly once")
	}
	handle := unsafe.Pointer(new(byte))
	root, _ := newSpawnTestFrame(t, child, handle, 0, 1)
	if !CommitSpawnCompiler(parent.g, child, handle) {
		t.Fatal("commit compiler spawn")
	}
	metadata := FrameFromStorage(root.storage)
	if child.root != metadata || child.active != metadata || child.frames != metadata ||
		child.state != GRunnable || !child.queued || p.readyHead != child || p.readyTail != child ||
		p.readyCount != 1 || parent.g.spawnChild != nil || child.spawnParent != nil || child.spawnP != nil {
		t.Fatalf("compiler spawn commit state: child=%+v p=(%p,%p,%d) parent-child=%p",
			child, p.readyHead, p.readyTail, p.readyCount, parent.g.spawnChild)
	}
	runtime.KeepAlive(parent.frame.memory)
	runtime.KeepAlive(root.memory)
}

func TestCompilerSpawnRejectsUntakenResumeGateWithoutMutation(t *testing.T) {
	fixture := newUncheckedResumeGateFixture(t, "compiler-spawn-gate")
	child := new(G)
	if BeginSpawnCompiler(fixture.task.g, child, unsafe.Pointer(child), TaskStorageSize()) {
		t.Fatal("compiler spawn accepted an untaken resume gate")
	}
	if fixture.task.g.spawnChild != nil || child.magic != 0 || child.taskStorage != nil ||
		child.spawnParent != nil || child.spawnP != nil ||
		!gPreemptStateAtDepthZero(child, preemptDisabled) {
		t.Fatal("rejected compiler spawn mutated transaction state")
	}
	assertResumeGateStillUnchecked(t, fixture)
}

func yieldSpawnTestG(t *testing.T, p *P, g *G, frame *testFrame, action Action) {
	t.Helper()
	// A sole newly spawned child now stays local without forcing the parent to
	// yield. Tests which need to hand execution to that child request the
	// scheduling point explicitly; a burst with existing ready work still
	// coalesces the same request in CommitSpawn.
	if !RequestPreempt(g) || !PollPreempt(g) {
		t.Fatal("request spawned-parent preemption")
	}
	frame.header.SuspendReason = uint16(SuspendYield)
	frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(g, frame.handle, frame.header) {
		t.Fatal("prepare spawned-parent yield")
	}
	got, ok := Resumed(p, g, action)
	if !ok || got.Kind != ActionYield {
		t.Fatalf("commit spawned-parent yield = (%+v, %t)", got, ok)
	}
}

func completeSpawnTestG(t *testing.T, p *P, g *G, frame *testFrame, action Action) Action {
	t.Helper()
	frame.header.SuspendReason = uint16(SuspendFrameComplete)
	frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(g, frame.handle, frame.header) {
		t.Fatal("prepare spawn test completion")
	}
	action, ok := Resumed(p, g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("spawn test completion = (%+v, %t)", action, ok)
	}
	action, ok = Checked(p, g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("spawn test destroy check = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, g, frame)
	action, ok = Destroyed(p, g, action)
	if !ok || action.Kind != ActionComplete {
		t.Fatalf("spawn test destroy commit = (%+v, %t)", action, ok)
	}
	return action
}

func TestSpawnOwnerGateIsConstantTimeAndChecksLocalHeaders(t *testing.T) {
	p := new(P)
	parent := newYieldingTestG(t, "owner-fast-parent")
	peers := []*yieldingTestG{
		newYieldingTestG(t, "owner-fast-first"),
		newYieldingTestG(t, "owner-fast-middle"),
		newYieldingTestG(t, "owner-fast-last"),
	}
	if !Enqueue(p, parent.g) {
		t.Fatal("enqueue owner-fast parent")
	}
	for _, peer := range peers {
		if !Enqueue(p, peer.g) {
			t.Fatalf("enqueue owner-fast peer %s", peer.name)
		}
	}
	if got, ok := NextRunnable(p); !ok || got != parent.g {
		t.Fatal("dequeue owner-fast parent")
	}
	action := beginSpawnTestResume(t, p, parent)

	// Corrupt only distant payloads while retaining the owner-maintained queue
	// endpoints. The full diagnostic audits must see the corruption; the spawn
	// hot gate must not walk unrelated tasks or wait records.
	peerState := peers[1].g.state
	peers[1].g.state = GDead
	var firstWait, lastWait WaitSetRecord
	firstWait.activeNext = &lastWait
	lastWait.activePrev = &firstWait
	p.parkWaitHead, p.parkWaitTail = &firstWait, &lastWait
	if validReadyQueue(p) || validSchedulerWaitQueues(p) {
		t.Fatal("full diagnostics accepted deliberately corrupt distant state")
	}
	if !CanBeginSpawn(parent.g) {
		t.Fatal("spawn owner gate scanned unrelated ready or parked payloads")
	}
	peers[1].g.state = peerState
	p.parkWaitHead, p.parkWaitTail = nil, nil

	// Endpoint, affected-work, and affinity corruption are local O(1) facts and
	// must still fail closed without publishing a spawn transaction.
	tailNext := p.readyTail.nextReady
	p.readyTail.nextReady = p.readyTail
	if CanBeginSpawn(parent.g) {
		t.Fatal("spawn owner gate accepted corrupt ready tail")
	}
	p.readyTail.nextReady = tailNext
	p.parkWaitHead = &firstWait
	if CanBeginSpawn(parent.g) {
		t.Fatal("spawn owner gate accepted mismatched park endpoints")
	}
	p.parkWaitHead = nil
	p.affectedWaitHead = &firstWait
	if CanBeginSpawn(parent.g) {
		t.Fatal("spawn owner gate accepted mismatched affected endpoints")
	}
	p.affectedWaitHead = nil
	p.osThreadSuspend = osThreadSuspendPhase(0xff)
	if CanBeginSpawn(parent.g) {
		t.Fatal("spawn owner gate accepted invalid affinity owner header")
	}
	p.osThreadSuspend = osThreadSuspendAttached
	if !CanBeginSpawn(parent.g) || parent.g.spawnChild != nil {
		t.Fatal("restored spawn owner gate did not recover without mutation")
	}

	runtime.KeepAlive(action)
	runtime.KeepAlive(parent.frame.memory)
	for _, peer := range peers {
		runtime.KeepAlive(peer.frame.memory)
	}
}

func TestSpawnBeginRollbackIsExactlyOnce(t *testing.T) {
	p := new(P)
	parent := newYieldingTestG(t, "rollback-parent")
	if !Enqueue(p, parent.g) {
		t.Fatal("enqueue rollback parent")
	}
	if got, ok := NextRunnable(p); !ok || got != parent.g {
		t.Fatal("dequeue rollback parent")
	}
	action := beginSpawnTestResume(t, p, parent)

	child := new(G)
	if !CanBeginSpawn(parent.g) || !BeginSpawn(parent.g, child, unsafe.Pointer(child), TaskStorageSize()) {
		t.Fatal("begin rollback spawn")
	}
	if CanBeginSpawn(parent.g) {
		t.Fatal("nested spawn begin was not blocked")
	}
	other := new(G)
	if BeginSpawn(parent.g, other, unsafe.Pointer(other), TaskStorageSize()) || ValidG(other) {
		t.Fatal("rejected nested spawn initialized another child")
	}
	if CommitSpawn(parent.g, child, nil) || child.root != nil || child.state != GNew || child.queued || p.readyHead != nil {
		t.Fatal("invalid commit partially adopted or queued child")
	}
	raw, size, ok := RollbackSpawn(parent.g, child)
	if !ok || raw != unsafe.Pointer(child) || size != TaskStorageSize() || parent.g.spawnChild != nil ||
		child.spawnParent != nil || child.spawnP != nil || child.state != GDead || child.taskState != taskStorageReleased {
		t.Fatalf("rollback = (%p, %d, %t), child state=%d task=%d", raw, size, ok, child.state, child.taskState)
	}
	if _, _, ok := RollbackSpawn(parent.g, child); ok {
		t.Fatal("spawn transaction rolled back twice")
	}

	completeSpawnTestG(t, p, parent.g, parent.frame, action)
	if !TerminalG(p, parent.g) {
		t.Fatal("rollback test parent did not become terminal")
	}
	runtime.KeepAlive(parent.frame.memory)
	runtime.KeepAlive(child)
}

func TestSpawnCommitDiscardedResultAtomicAndTaskReclaim(t *testing.T) {
	p := new(P)
	parent := newYieldingTestG(t, "commit-parent")
	if !Enqueue(p, parent.g) {
		t.Fatal("enqueue commit parent")
	}
	if got, ok := NextRunnable(p); !ok || got != parent.g {
		t.Fatal("dequeue commit parent")
	}
	parentAction := beginSpawnTestResume(t, p, parent)

	child := new(G)
	if !BeginSpawn(parent.g, child, unsafe.Pointer(child), TaskStorageSize()) {
		t.Fatal("begin committed spawn")
	}
	handle := unsafe.Pointer(new(byte))
	frame, descriptor := newSpawnTestFrame(t, child, handle, 8, 0)
	if CommitSpawn(parent.g, child, handle) {
		t.Fatal("invalid result layout accepted")
	}
	if child.root != nil || child.active != nil || child.state != GNew || child.queued ||
		p.readyHead != nil || parent.g.spawnChild != child || preemptLoad(preemptAddress(parent.g)) != preemptIdle {
		t.Fatal("rejected result layout partially committed spawn")
	}
	descriptor.ResultAlign = 8
	p.current = nil
	if CommitSpawn(parent.g, child, handle) {
		t.Fatal("spawn transaction accepted a lost resume owner")
	}
	p.current = parent.g
	child.spawnP = new(P)
	if CommitSpawn(parent.g, child, handle) {
		t.Fatal("spawn transaction accepted a mismatched P certificate")
	}
	child.spawnP = p
	if !CommitSpawn(parent.g, child, handle) {
		t.Fatal("commit goroutine root with discarded result")
	}
	if parent.g.spawnChild != nil || child.root == nil || child.active != child.root || child.state != GRunnable ||
		child.root.header.ResultSlot != nil || !child.queued ||
		p.readyHead != child || p.readyTail != child || preemptLoad(preemptAddress(parent.g)) != preemptIdle {
		t.Fatal("committed spawn state is incomplete")
	}
	if CommitSpawn(parent.g, child, handle) || p.readyHead != child || p.readyTail != child || child.nextReady != nil {
		t.Fatal("duplicate spawn commit changed the ready queue")
	}

	if !RequestPreempt(parent.g) {
		t.Fatal("request explicit parent yield after sole-child locality check")
	}
	yieldSpawnTestG(t, p, parent.g, parent.frame, parentAction)
	if got, ok := NextRunnable(p); !ok || got != child {
		t.Fatalf("spawned child was not first after parent yield: (%p, %t)", got, ok)
	}
	childAction := beginSpawnTestChildResume(t, p, child, frame)
	completeSpawnTestG(t, p, child, frame, childAction)
	if !ReclaimableG(child) || TerminalG(p, child) {
		t.Fatal("per-G reclaimability was confused with P-wide terminal state")
	}
	owned, ok := TaskStorageOwned(child)
	if !ok || !owned {
		t.Fatal("completed child did not retain one owned task allocation")
	}
	raw, size, ok := ReleaseTaskStorage(child)
	if !ok || raw != unsafe.Pointer(child) || size != TaskStorageSize() {
		t.Fatalf("release child task = (%p, %d, %t)", raw, size, ok)
	}
	if _, _, ok := ReleaseTaskStorage(child); ok {
		t.Fatal("child task allocation released twice")
	}

	if got, ok := NextRunnable(p); !ok || got != parent.g {
		t.Fatalf("parent was not runnable after child completion: (%p, %t)", got, ok)
	}
	parentAction = beginSpawnTestResume(t, p, parent)
	completeSpawnTestG(t, p, parent.g, parent.frame, parentAction)
	if !TerminalG(p, parent.g) || !TerminalG(p, child) {
		t.Fatal("spawn/parent completion retained scheduler state")
	}
	runtime.KeepAlive(parent.frame.memory)
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(child)
}

func TestCompletedTaskTransfersContextAndStorageAfterOneTerminalAudit(t *testing.T) {
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize completed task transfer G")
	}
	g.taskStorage = unsafe.Pointer(g)
	g.taskSize = TaskStorageSize()
	g.taskState = taskStorageOwned
	local := unsafe.Pointer(new(byte))
	if !BindTaskLocal(g, local) {
		t.Fatal("bind completed task transfer context")
	}
	if !disableGPreempt(g) {
		t.Fatal("disable completed task transfer preemption")
	}
	g.state = GDead
	g.taskControlLeases = 1
	if _, _, _, _, ok := ReleaseCompletedTaskCompiler(g); ok {
		t.Fatal("completed task with a live control lease transferred")
	}
	g.taskControlLeases = 0

	releasedLocal, raw, size, owned, ok := ReleaseCompletedTaskCompiler(g)
	if !ok || !owned || releasedLocal != local || raw != unsafe.Pointer(g) || size != TaskStorageSize() ||
		g.taskLocal != nil || g.taskStorage != nil || g.taskSize != 0 || g.taskState != taskStorageReleased {
		t.Fatalf("completed task transfer = local:%p raw:%p size:%d owned:%t ok:%t state:%d",
			releasedLocal, raw, size, owned, ok, g.taskState)
	}
	if _, _, _, _, ok := ReleaseCompletedTaskCompiler(g); ok {
		t.Fatal("completed task transferred twice")
	}
}

func TestSpawnReadyQueuePreservesFIFOAndParentFairness(t *testing.T) {
	p := new(P)
	parent := newYieldingTestG(t, "fair-parent")
	competitor := newYieldingTestG(t, "fair-competitor")
	if !Enqueue(p, parent.g) || !Enqueue(p, competitor.g) {
		t.Fatal("enqueue fairness tasks")
	}
	if got, ok := NextRunnable(p); !ok || got != parent.g {
		t.Fatal("dequeue fairness parent")
	}
	parentAction := beginSpawnTestResume(t, p, parent)
	child := new(G)
	if !BeginSpawn(parent.g, child, unsafe.Pointer(child), TaskStorageSize()) {
		t.Fatal("begin fairness child")
	}
	handle := unsafe.Pointer(new(byte))
	frame, descriptor := newSpawnTestFrame(t, child, handle, 0, 1)
	if !CommitSpawn(parent.g, child, handle) {
		t.Fatal("commit fairness child")
	}
	yieldSpawnTestG(t, p, parent.g, parent.frame, parentAction)

	wants := []*G{competitor.g, child, parent.g}
	for index, want := range wants {
		if got := dequeue(p); got != want {
			t.Fatalf("ready[%d] = %p, want %p", index, got, want)
		}
	}
	if p.readyHead != nil || p.readyTail != nil {
		t.Fatal("fairness queue retained an unexpected task")
	}
	runtime.KeepAlive(parent.frame.memory)
	runtime.KeepAlive(competitor.frame.memory)
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(child)
}
