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

func beginActiveOSThreadTask(t *testing.T, p *P, task *yieldingTestG) Action {
	t.Helper()
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue OS-thread task")
	}
	next, ok := NextRunnable(p)
	if !ok || next != task.g {
		t.Fatalf("dequeue OS-thread task = (%p, %t)", next, ok)
	}
	action, ok := BeginRunG(p, task.g)
	if !ok {
		t.Fatal("begin OS-thread task")
	}
	return activatePreemptTestFrame(t, p, task, action)
}

func yieldActiveOSThreadTask(t *testing.T, p *P, task *yieldingTestG, action Action) {
	t.Helper()
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare OS-thread task yield")
	}
	next, ok := Resumed(p, task.g, action)
	if !ok || next != (Action{Kind: ActionYield}) {
		t.Fatalf("commit OS-thread task yield = (%+v, %t)", next, ok)
	}
}

func TestOSThreadLockNestingAndUnmatchedUnlock(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "os-thread-nesting")
	_ = beginActiveOSThreadTask(t, p, task)
	if CurrentOSThreadLocked(task.g) {
		t.Fatal("fresh task reported an OS-thread lock")
	}
	if !ExitOSThreadLock(task.g) || p.osThreadLockOwner != nil {
		t.Fatal("unmatched UnlockOSThread was not a no-op")
	}
	if !EnterOSThreadLock(task.g) || !EnterOSThreadLock(task.g) ||
		task.g.osThreadLockDepth != 2 || p.osThreadLockOwner != task.g ||
		!CurrentOSThreadLocked(task.g) {
		t.Fatalf("nested lock state = depth:%d owner:%p current:%t",
			task.g.osThreadLockDepth, p.osThreadLockOwner, CurrentOSThreadLocked(task.g))
	}
	if !ExitOSThreadLock(task.g) || task.g.osThreadLockDepth != 1 ||
		p.osThreadLockOwner != task.g || !CurrentOSThreadLocked(task.g) {
		t.Fatal("inner UnlockOSThread released the physical owner")
	}
	if !ExitOSThreadLock(task.g) || task.g.osThreadLockDepth != 0 ||
		p.osThreadLockOwner != nil || CurrentOSThreadLocked(task.g) {
		t.Fatal("outer UnlockOSThread retained the physical owner")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestOSThreadLockSelectsOwnerAcrossYieldWithoutReorderingPeers(t *testing.T) {
	p := new(P)
	owner := newYieldingTestG(t, "os-thread-owner")
	first := newYieldingTestG(t, "os-thread-first-peer")
	second := newYieldingTestG(t, "os-thread-second-peer")
	if !Enqueue(p, owner.g) || !Enqueue(p, first.g) || !Enqueue(p, second.g) {
		t.Fatal("enqueue OS-thread ownership fixture")
	}
	next, ok := NextRunnable(p)
	if !ok || next != owner.g {
		t.Fatalf("initial owner dequeue = (%p, %t)", next, ok)
	}
	action, ok := BeginRunG(p, owner.g)
	if !ok {
		t.Fatal("begin OS-thread owner")
	}
	action = activatePreemptTestFrame(t, p, owner, action)
	if !EnterOSThreadLock(owner.g) {
		t.Fatal("lock OS thread")
	}
	yieldActiveOSThreadTask(t, p, owner, action)

	if p.readyHead != first.g || first.g.nextReady != second.g ||
		second.g.nextReady != owner.g || p.readyTail != owner.g {
		t.Fatalf("locked yield changed FIFO: head=%p first.next=%p second.next=%p tail=%p",
			p.readyHead, first.g.nextReady, second.g.nextReady, p.readyTail)
	}
	next, ok = NextRunnable(p)
	if !ok || next != owner.g {
		t.Fatalf("locked P selected peer instead of owner: (%p, %t)", next, ok)
	}
	if p.readyHead != first.g || first.g.nextReady != second.g || p.readyTail != second.g {
		t.Fatal("owner extraction reordered unrelated peers")
	}
	action, ok = BeginRunG(p, owner.g)
	if !ok {
		t.Fatal("resume OS-thread owner")
	}
	action = activatePreemptTestFrame(t, p, owner, action)
	if !ExitOSThreadLock(owner.g) {
		t.Fatal("unlock OS thread")
	}
	yieldActiveOSThreadTask(t, p, owner, action)
	next, ok = NextRunnable(p)
	if !ok || next != first.g {
		t.Fatalf("unlocked P did not restore FIFO: (%p, %t)", next, ok)
	}
	runtime.KeepAlive(owner.frame.memory)
	runtime.KeepAlive(first.frame.memory)
	runtime.KeepAlive(second.frame.memory)
}

func TestOSThreadLockedGCannotCrossPNeutralTransfer(t *testing.T) {
	source, target := new(P), new(P)
	task := newYieldingTestG(t, "os-thread-transfer")
	action := beginActiveOSThreadTask(t, source, task)
	if !EnterOSThreadLock(task.g) {
		t.Fatal("lock transfer task")
	}
	yieldActiveOSThreadTask(t, source, task, action)
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind transfer mailbox")
	}
	if id, ok := PublishPNeutralRunnable(&mailbox, source, task.g); ok ||
		id != (RunnableTransferID{}) || source.readyHead != task.g ||
		source.readyTail != task.g || !task.g.queued || mailbox.count != 0 {
		t.Fatalf("locked task crossed P-neutral transfer: id=%+v ok=%t head=%p count=%d",
			id, ok, source.readyHead, mailbox.count)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestOSThreadLockIsReleasedBeforeTerminalGPublication(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "os-thread-terminal")
	peer := newYieldingTestG(t, "os-thread-terminal-peer")
	action := beginActiveOSThreadTask(t, p, task)
	if !Enqueue(p, peer.g) || !EnterOSThreadLock(task.g) {
		t.Fatal("prepare terminal OS-thread owner")
	}
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare terminal OS-thread completion")
	}
	action, ok := Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume terminal OS-thread completion = (%+v, %t)", action, ok)
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check terminal OS-thread destroy = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	action, ok = Destroyed(p, task.g, action)
	if !ok || action.Kind != ActionComplete || action.Flags != ActionRetirePhysicalOwner ||
		!ActionRetiresPhysicalOwner(action) || task.g.osThreadLockDepth != 0 ||
		p.osThreadLockOwner != nil || !ReclaimableG(task.g) {
		t.Fatalf("terminal lock release = action:%+v ok:%t depth:%d owner:%p reclaimable:%t",
			action, ok, task.g.osThreadLockDepth, p.osThreadLockOwner, ReclaimableG(task.g))
	}
	next, nextOK := NextRunnable(p)
	if !nextOK || next != peer.g {
		t.Fatalf("terminal lock did not release peer = (%p, %t)", next, nextOK)
	}
	runtime.KeepAlive(task.frame.memory)
	runtime.KeepAlive(peer.frame.memory)
}

func TestPhysicalOwnerRetireFlagUsesActionPadding(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	if unsafe.Offsetof(Action{}.Flags) != unsafe.Sizeof(Action{}.Kind) ||
		unsafe.Offsetof(Action{}.Handle) != pointerSize ||
		unsafe.Sizeof(Action{}) != 2*pointerSize {
		t.Fatalf("owner-retire action layout = kind:%d flags:%d handle:%d size:%d pointer:%d",
			unsafe.Offsetof(Action{}.Kind), unsafe.Offsetof(Action{}.Flags),
			unsafe.Offsetof(Action{}.Handle), unsafe.Sizeof(Action{}), pointerSize)
	}
	if !ActionRetiresPhysicalOwner(Action{
		Kind:  ActionComplete,
		Flags: ActionRetirePhysicalOwner,
	}) {
		t.Fatal("valid terminal owner-retire flag was rejected")
	}
	if ActionRetiresPhysicalOwner(Action{
		Kind:  ActionYield,
		Flags: ActionRetirePhysicalOwner,
	}) || ActionRetiresPhysicalOwner(Action{
		Kind:  ActionComplete,
		Flags: ActionRetirePhysicalOwner << 1,
	}) {
		t.Fatal("malformed owner-retire flag was accepted")
	}
}

func TestOSThreadLockDepthUsesExistingGPadding(t *testing.T) {
	lockOffset := unsafe.Offsetof(G{}.osThreadLockDepth)
	panicOffset := unsafe.Offsetof(G{}.panicUnwind)
	if unsafe.Sizeof(G{}.osThreadLockDepth) != 1 ||
		lockOffset != panicOffset+unsafe.Sizeof(G{}.panicUnwind) ||
		unsafe.Sizeof(G{}) != wantSchedulerGSize {
		t.Fatalf("OS-thread lock layout changed: panic=%d lock=%d/%d G=%d want=%d",
			panicOffset, lockOffset, unsafe.Sizeof(G{}.osThreadLockDepth),
			unsafe.Sizeof(G{}), wantSchedulerGSize)
	}
}
