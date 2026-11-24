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
	"testing"
	"unsafe"
)

func TestCoroAuditModeInlineAwaitAndSpawnLifecycle(t *testing.T) {
	fixture := newInlineAwaitFixtureForCompiler(t, true)
	g, p := fixture.g, fixture.p
	parent, child := fixture.parent, fixture.child

	if !ResumePanicBoundaryActiveCompiler(g, child.handle) {
		t.Fatal("ResumePanicBoundaryActiveCompiler failed")
	}
	child.header.SuspendReason = uint16(SuspendFrameComplete)
	child.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareCompleteStatusCompiler(g, child.handle, child.header, CompletionReturn) {
		t.Fatal("PrepareCompleteStatusCompiler(child) failed")
	}
	if !ResumePanicBoundaryReturningCompiler(g, child.handle) {
		t.Fatal("ResumePanicBoundaryReturningCompiler failed")
	}
	if !FinishInlineAwaitReturnCompiler(g, parent.handle, child.handle) {
		t.Fatal("FinishInlineAwaitReturnCompiler failed")
	}
	releaseTestFrame(t, g, child)
	if !CommitInlineAwaitReturnPhysicalDestroyCompiler(g, parent.handle, child.handle) {
		t.Fatal("CommitInlineAwaitReturnPhysicalDestroyCompiler failed")
	}

	// Dynamic compiler-allocated inline child lifecycle (exercises RegisterFrameCompilerPrepared
	// + PublishFrameV3Compiler + ReleaseFrameCompiler fast path).
	dynDesc := FrameDescriptorV1{Version: 1, Function: "dynchild"}
	const dynSize, dynAlign = uintptr(256), uintptr(16)
	dynTotal, ok := CompilerFrameAllocationSize(dynSize, dynAlign)
	if !ok {
		t.Fatal("CompilerFrameAllocationSize(dynchild) failed")
	}
	dynRaw := make([]byte, dynTotal+dynAlign)
	dynBase := unsafe.Pointer((uintptr(unsafe.Pointer(&dynRaw[0])) + dynAlign - 1) &^ (dynAlign - 1))
	dynStorage, ok := RegisterFrameCompilerPrepared(g, dynBase, dynTotal, dynSize, dynAlign, unsafe.Pointer(&dynDesc))
	if !ok {
		t.Fatal("RegisterFrameCompilerPrepared(dynchild) failed")
	}
	dynHandle := dynStorage
	dynHeader := (*HeaderV1)(dynStorage)
	dynMeta := unsafe.Add(dynStorage, unsafe.Sizeof(HeaderV1{}))
	if !PublishFrameV3Compiler(g, dynHandle, dynHeader, dynStorage, dynMeta, unsafe.Pointer(&dynDesc), nil) {
		t.Fatal("PublishFrameV3Compiler(dynchild) failed")
	}
	dynHeader.Parent = parent.handle
	parent.header.SuspendReason = uint16(SuspendCall)
	parent.header.Lifecycle = uint16(FrameSuspended)
	if disp := PrepareInlineAwaitCompiler(g, parent.handle, dynHandle, nil, nil); disp != InlineAwaitStarted {
		t.Fatalf("PrepareInlineAwaitCompiler(dynchild) = %v, want InlineAwaitStarted", disp)
	}
	if _, _, _, _, _, taken := TakeRunDecisionWordsCompiler(g, 0, 0); !taken {
		t.Fatal("TakeRunDecisionWordsCompiler(dynchild) failed")
	}
	dynHeader.SuspendReason = uint16(SuspendFrameComplete)
	dynHeader.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareCompleteStatusCompiler(g, dynHandle, dynHeader, CompletionReturn) {
		t.Fatal("PrepareCompleteStatusCompiler(dynchild) failed")
	}
	if !FinishInlineAwaitReturnCompiler(g, parent.handle, dynHandle) {
		t.Fatal("FinishInlineAwaitReturnCompiler(dynchild) failed")
	}
	_, raw, total, released := ReleaseFrameCompiler(g, dynStorage, dynSize, dynAlign, unsafe.Pointer(&dynDesc))
	if !released || raw != dynBase || total != dynTotal {
		t.Fatalf("ReleaseFrameCompiler(dynchild) = (_, %p, %d, %t), want (%p, %d, true)", raw, total, released, dynBase, dynTotal)
	}
	if !CommitInlineAwaitReturnPhysicalDestroyCompiler(g, parent.handle, dynHandle) {
		t.Fatal("CommitInlineAwaitReturnPhysicalDestroyCompiler(dynchild) failed")
	}

	// Borrowed inline child lifecycle.
	var borrowedHeader HeaderV1
	var borrowedMeta BorrowedFrameStorageV2
	borrowedDesc := FrameDescriptorV1{Version: 1, Function: "borrowed"}
	borrowedHandle := unsafe.Pointer(&borrowedHeader)
	if !PublishFrameV3Compiler(g, borrowedHandle, &borrowedHeader, nil, unsafe.Pointer(&borrowedMeta), unsafe.Pointer(&borrowedDesc), nil) {
		t.Fatal("PublishFrameV3Compiler(borrowed) failed")
	}
	borrowedHeader.Parent = parent.handle
	parent.header.SuspendReason = uint16(SuspendCall)
	parent.header.Lifecycle = uint16(FrameSuspended)
	if disp := PrepareInlineAwaitCompiler(g, parent.handle, borrowedHandle, nil, nil); disp != InlineAwaitStarted {
		t.Fatalf("PrepareInlineAwaitCompiler(borrowed) = %v, want InlineAwaitStarted", disp)
	}
	if _, _, _, _, _, taken := TakeRunDecisionWordsCompiler(g, 0, 0); !taken {
		t.Fatal("TakeRunDecisionWordsCompiler(borrowed) failed")
	}
	borrowedHeader.SuspendReason = uint16(SuspendFrameComplete)
	borrowedHeader.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareCompleteStatusCompiler(g, borrowedHandle, &borrowedHeader, CompletionReturn) {
		t.Fatal("PrepareCompleteStatusCompiler(borrowed) failed")
	}
	if !FinishInlineAwaitReturnCompiler(g, parent.handle, borrowedHandle) {
		t.Fatal("FinishInlineAwaitReturnCompiler(borrowed) failed")
	}
	if !CommitInlineAwaitReturnPhysicalDestroyCompiler(g, parent.handle, borrowedHandle) {
		t.Fatal("CommitInlineAwaitReturnPhysicalDestroyCompiler(borrowed) failed")
	}

	// Spawn lifecycle.
	spawnedG := new(G)
	if !BeginSpawnCompiler(g, spawnedG, unsafe.Pointer(spawnedG), TaskStorageSize()) {
		t.Fatal("BeginSpawnCompiler failed")
	}
	spawnDesc := FrameDescriptorV1{Version: 1, Function: "spawned"}
	spawnTotal, ok := CompilerFrameAllocationSize(256, 16)
	if !ok {
		t.Fatal("CompilerFrameAllocationSize(spawn) failed")
	}
	spawnRaw := make([]byte, spawnTotal+16)
	spawnBase := unsafe.Pointer((uintptr(unsafe.Pointer(&spawnRaw[0])) + 15) &^ 15)
	spawnStorage, ok := RegisterFrameCompilerPrepared(spawnedG, spawnBase, spawnTotal, 256, 16, unsafe.Pointer(&spawnDesc))
	if !ok {
		t.Fatal("RegisterFrameCompilerPrepared(spawn) failed")
	}
	spawnHandle := spawnStorage
	spawnHeader := (*HeaderV1)(spawnStorage)
	spawnMeta := unsafe.Add(spawnStorage, unsafe.Sizeof(HeaderV1{}))
	if !PublishFrameV3Compiler(spawnedG, spawnHandle, spawnHeader, spawnStorage, spawnMeta, unsafe.Pointer(&spawnDesc), nil) {
		t.Fatal("PublishFrameV3Compiler(spawn) failed")
	}
	if !CommitSpawnCompiler(g, spawnedG, spawnHandle) {
		t.Fatal("CommitSpawnCompiler failed")
	}
	if p.readyCount != 1 {
		t.Fatalf("readyCount = %d, want 1", p.readyCount)
	}

	// Complete root and release its frame.
	parent.header.SuspendReason = uint16(SuspendFrameComplete)
	parent.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareCompleteStatusCompiler(g, parent.handle, parent.header, CompletionReturn) {
		t.Fatal("PrepareCompleteStatusCompiler(parent) failed")
	}
	nextAction, ok := Resumed(p, g, fixture.action)
	if !ok || nextAction.Kind != ActionCheckDestroy || nextAction.Handle != parent.handle {
		t.Fatalf("Resumed(parent) = (%+v, %t), want ActionCheckDestroy", nextAction, ok)
	}
	nextAction, ok = checkedTestAction(p, g, nextAction, true)
	if !ok || nextAction.Kind != ActionDestroy || nextAction.Handle != parent.handle {
		t.Fatalf("checkedTestAction(parent) = (%+v, %t), want ActionDestroy", nextAction, ok)
	}
	releaseTestFrame(t, g, parent)
	if !CommitFrameDestroyCompiler(g, parent.handle) {
		t.Fatal("CommitFrameDestroyCompiler(parent) failed")
	}
	doneAction, ok := Destroyed(p, g, nextAction)
	if !ok || doneAction.Kind != ActionComplete {
		t.Fatalf("Destroyed(parent) = (%+v, %t), want ActionComplete", doneAction, ok)
	}
}
