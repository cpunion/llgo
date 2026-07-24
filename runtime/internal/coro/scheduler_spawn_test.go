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

func yieldSpawnTestG(t *testing.T, p *P, g *G, frame *testFrame, action Action) {
	t.Helper()
	if !PollPreempt(g) {
		t.Fatal("spawn commit did not request parent preemption")
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
	if !CommitSpawn(parent.g, child, handle) {
		t.Fatal("commit goroutine root with discarded result")
	}
	if parent.g.spawnChild != nil || child.root == nil || child.active != child.root || child.state != GRunnable ||
		child.root.header.ResultSlot != nil || !child.queued ||
		p.readyHead != child || p.readyTail != child || preemptLoad(preemptAddress(parent.g)) != preemptRequested {
		t.Fatal("committed spawn state is incomplete")
	}
	if CommitSpawn(parent.g, child, handle) || p.readyHead != child || p.readyTail != child || child.nextReady != nil {
		t.Fatal("duplicate spawn commit changed the ready queue")
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
