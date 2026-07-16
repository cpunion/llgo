/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package coro

import (
	"runtime"
	"testing"
	"unsafe"
)

type commandShutdownChild struct {
	g          *G
	handle     unsafe.Pointer
	frame      *testFrame
	descriptor *FrameDescriptorV1
}

type commandShutdownFixture struct {
	p          *P
	main       *yieldingTestG
	mainAction Action
	children   []*commandShutdownChild
}

func newCommandShutdownFixture(t *testing.T) *commandShutdownFixture {
	t.Helper()
	p := new(P)
	main := newYieldingTestG(t, "command-main")
	if !Enqueue(p, main.g) {
		t.Fatal("enqueue command main")
	}
	if got, ok := NextRunnable(p); !ok || got != main.g {
		t.Fatal("dequeue command main")
	}
	return &commandShutdownFixture{p: p, main: main, mainAction: beginSpawnTestResume(t, p, main)}
}

func (fixture *commandShutdownFixture) spawn(t *testing.T) *commandShutdownChild {
	t.Helper()
	child := &commandShutdownChild{g: new(G), handle: unsafe.Pointer(new(byte))}
	if !BeginSpawn(fixture.main.g, child.g, unsafe.Pointer(child.g), TaskStorageSize()) {
		t.Fatal("begin command child")
	}
	child.frame, child.descriptor = newSpawnTestFrame(t, child.g, child.handle, 0, 1)
	if !CommitSpawn(fixture.main.g, child.g, child.handle) {
		t.Fatal("commit command child")
	}
	fixture.children = append(fixture.children, child)
	return child
}

func (fixture *commandShutdownFixture) completeMain(t *testing.T) {
	t.Helper()
	completeSpawnTestG(t, fixture.p, fixture.main.g, fixture.main.frame, fixture.mainAction)
	if !ReclaimableG(fixture.main.g) {
		t.Fatal("completed command main is not reclaimable")
	}
}

func cancelOneCommandChild(t *testing.T, p *P, want *commandShutdownChild) {
	t.Helper()
	g, action, ok := NextCommandCancel(p)
	if !ok || g != want.g || action.Kind != ActionCancelDestroy || action.Handle != want.handle {
		t.Fatalf("next command cancel = (g=%p action=%+v ok=%t), want %p/%p", g, action, ok, want.g, want.handle)
	}
	if _, ok := CancelDestroyed(p, g, action); ok || p.current != g || p.action != action || g.destroyTarget == nil {
		t.Fatal("cancel commit ran before the destroy/free callback")
	}
	releaseTestFrame(t, g, want.frame)
	action, ok = CancelDestroyed(p, g, action)
	if !ok || action.Kind != ActionCancelComplete || action.Handle != nil || !ReclaimableG(g) {
		t.Fatalf("complete command cancel = (%+v, %t), reclaimable=%t", action, ok, ReclaimableG(g))
	}
	if _, ok := CancelDestroyed(p, g, Action{Kind: ActionCancelDestroy, Handle: want.handle}); ok {
		t.Fatal("completed command cancellation committed twice")
	}
	if _, _, ok := ReleaseTaskStorage(g); !ok {
		t.Fatal("release canceled command task")
	}
	if _, _, ok := ReleaseTaskStorage(g); ok {
		t.Fatal("release canceled command task twice")
	}
}

func keepCommandShutdownFixtureAlive(fixture *commandShutdownFixture) {
	runtime.KeepAlive(fixture.main.frame.memory)
	for _, child := range fixture.children {
		runtime.KeepAlive(child.frame.memory)
		runtime.KeepAlive(child.descriptor)
		runtime.KeepAlive(child.g)
	}
}

func TestCommandShutdownCancelsInitialSuspendedChild(t *testing.T) {
	fixture := newCommandShutdownFixture(t)
	child := fixture.spawn(t)
	fixture.completeMain(t)
	if !BeginCommandShutdown(fixture.p, fixture.main.g) || preemptLoad(&fixture.p.schedule) != scheduleStopping {
		t.Fatal("begin command shutdown")
	}
	if RequestSchedule(fixture.p) {
		t.Fatal("late schedule request entered stopping P")
	}
	cancelOneCommandChild(t, fixture.p, child)
	if g, action, ok := NextCommandCancel(fixture.p); !ok || g != nil || action.Kind != ActionInvalid {
		t.Fatalf("empty command cancel queue = (%p, %+v, %t)", g, action, ok)
	}
	if !FinishCommandShutdown(fixture.p, fixture.main.g) ||
		!TerminalG(fixture.p, fixture.main.g) || !TerminalG(fixture.p, child.g) {
		t.Fatal("finish initial-suspended command shutdown")
	}
	if FinishCommandShutdown(fixture.p, fixture.main.g) {
		t.Fatal("command shutdown finished twice")
	}
	keepCommandShutdownFixtureAlive(fixture)
}

func TestCommandShutdownDestroysNestedInitialFrameBeforeRoot(t *testing.T) {
	fixture := newCommandShutdownFixture(t)
	child := fixture.spawn(t)
	nestedHandle := unsafe.Pointer(new(byte))
	nested := newTestFrame(t, child.g, nestedHandle, child.handle)
	rootFrame := FrameFromStorage(child.frame.storage)
	nestedFrame := FrameFromStorage(nested.storage)
	rootFrame.state = FrameSuspended
	rootFrame.header.SuspendReason = uint16(SuspendCall)
	rootFrame.header.Lifecycle = uint16(FrameSuspended)
	nestedFrame.parent = rootFrame
	child.g.active = nestedFrame

	fixture.completeMain(t)
	if !BeginCommandShutdown(fixture.p, fixture.main.g) {
		t.Fatal("begin nested-initial shutdown")
	}
	g, action, ok := NextCommandCancel(fixture.p)
	if !ok || g != child.g || action.Kind != ActionCancelDestroy || action.Handle != nestedHandle {
		t.Fatalf("nested initial first action = (g=%p action=%+v ok=%t)", g, action, ok)
	}
	releaseTestFrame(t, child.g, nested)
	action, ok = CancelDestroyed(fixture.p, child.g, action)
	if !ok || action.Kind != ActionCancelDestroy || action.Handle != child.handle {
		t.Fatalf("nested initial root action = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, child.g, child.frame)
	action, ok = CancelDestroyed(fixture.p, child.g, action)
	if !ok || action.Kind != ActionCancelComplete {
		t.Fatalf("nested initial completion = (%+v, %t)", action, ok)
	}
	if _, _, ok := ReleaseTaskStorage(child.g); !ok || !FinishCommandShutdown(fixture.p, fixture.main.g) {
		t.Fatal("release/finish nested-initial shutdown")
	}
	runtime.KeepAlive(nested.memory)
	keepCommandShutdownFixtureAlive(fixture)
}

func TestCommandShutdownCancelsYieldedChild(t *testing.T) {
	fixture := newCommandShutdownFixture(t)
	child := fixture.spawn(t)
	yieldSpawnTestG(t, fixture.p, fixture.main.g, fixture.main.frame, fixture.mainAction)
	if got, ok := NextRunnable(fixture.p); !ok || got != child.g {
		t.Fatal("dequeue child before yield")
	}
	childAction := beginSpawnTestChildResume(t, fixture.p, child.g, child.frame)
	if !PollPreempt(child.g) {
		t.Fatal("ready main did not preempt child")
	}
	child.frame.header.SuspendReason = uint16(SuspendYield)
	child.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(child.g, child.handle, child.frame.header) {
		t.Fatal("prepare child yield")
	}
	if action, ok := Resumed(fixture.p, child.g, childAction); !ok || action.Kind != ActionYield {
		t.Fatal("commit child yield")
	}
	if got, ok := NextRunnable(fixture.p); !ok || got != fixture.main.g {
		t.Fatal("dequeue main after child yield")
	}
	fixture.mainAction = beginSpawnTestResume(t, fixture.p, fixture.main)
	fixture.completeMain(t)
	if !BeginCommandShutdown(fixture.p, fixture.main.g) {
		t.Fatal("begin yielded-child shutdown")
	}
	cancelOneCommandChild(t, fixture.p, child)
	if !FinishCommandShutdown(fixture.p, fixture.main.g) {
		t.Fatal("finish yielded-child shutdown")
	}
	keepCommandShutdownFixtureAlive(fixture)
}

func TestCommandShutdownDestroysStructuredChainDeepestToRoot(t *testing.T) {
	fixture := newCommandShutdownFixture(t)
	child := fixture.spawn(t)
	yieldSpawnTestG(t, fixture.p, fixture.main.g, fixture.main.frame, fixture.mainAction)
	if got, ok := NextRunnable(fixture.p); !ok || got != child.g {
		t.Fatal("dequeue structured child")
	}
	action := beginSpawnTestChildResume(t, fixture.p, child.g, child.frame)

	midHandle := unsafe.Pointer(new(byte))
	mid := newTestFrame(t, child.g, midHandle, child.handle)
	child.frame.header.SuspendReason = uint16(SuspendCall)
	child.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareAwait(child.g, child.handle, midHandle) {
		t.Fatal("prepare root-to-mid await")
	}
	action, ok := Resumed(fixture.p, child.g, action)
	if !ok || action.Kind != ActionCheckResume || action.Handle != midHandle {
		t.Fatal("dispatch mid frame")
	}
	action, ok = Checked(fixture.p, child.g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatal("activate mid frame")
	}
	mid.header.SuspendReason = uint16(SuspendNone)
	mid.header.Lifecycle = uint16(FrameActive)

	leafHandle := unsafe.Pointer(new(byte))
	leaf := newTestFrame(t, child.g, leafHandle, midHandle)
	mid.header.SuspendReason = uint16(SuspendCall)
	mid.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareAwait(child.g, midHandle, leafHandle) {
		t.Fatal("prepare mid-to-leaf await")
	}
	action, ok = Resumed(fixture.p, child.g, action)
	if !ok || action.Kind != ActionCheckResume || action.Handle != leafHandle {
		t.Fatal("dispatch leaf frame")
	}
	action, ok = Checked(fixture.p, child.g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatal("activate leaf frame")
	}
	leaf.header.SuspendReason = uint16(SuspendNone)
	leaf.header.Lifecycle = uint16(FrameActive)
	if !PollPreempt(child.g) {
		t.Fatal("structured leaf missed parent competitor preemption")
	}
	leaf.header.SuspendReason = uint16(SuspendYield)
	leaf.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(child.g, leafHandle, leaf.header) {
		t.Fatal("prepare structured leaf yield")
	}
	if action, ok = Resumed(fixture.p, child.g, action); !ok || action.Kind != ActionYield {
		t.Fatal("commit structured leaf yield")
	}

	if got, ok := NextRunnable(fixture.p); !ok || got != fixture.main.g {
		t.Fatal("dequeue main beside structured child")
	}
	fixture.mainAction = beginSpawnTestResume(t, fixture.p, fixture.main)
	fixture.completeMain(t)
	if !BeginCommandShutdown(fixture.p, fixture.main.g) {
		t.Fatal("begin structured shutdown")
	}
	g, action, ok := NextCommandCancel(fixture.p)
	if !ok || g != child.g {
		t.Fatal("select structured child for cancellation")
	}
	wants := []struct {
		handle unsafe.Pointer
		frame  *testFrame
	}{{leafHandle, leaf}, {midHandle, mid}, {child.handle, child.frame}}
	for index, want := range wants {
		if action.Kind != ActionCancelDestroy || action.Handle != want.handle {
			t.Fatalf("destroy[%d] = %+v, want handle %p", index, action, want.handle)
		}
		releaseTestFrame(t, child.g, want.frame)
		action, ok = CancelDestroyed(fixture.p, child.g, action)
		if !ok {
			t.Fatalf("commit destroy[%d]", index)
		}
	}
	if action.Kind != ActionCancelComplete || !ReclaimableG(child.g) {
		t.Fatal("structured child did not reach cancel-complete")
	}
	if _, _, ok := ReleaseTaskStorage(child.g); !ok || !FinishCommandShutdown(fixture.p, fixture.main.g) {
		t.Fatal("release/finish structured shutdown")
	}
	runtime.KeepAlive(mid.memory)
	runtime.KeepAlive(leaf.memory)
	keepCommandShutdownFixtureAlive(fixture)
}

func TestCommandShutdownCancelsMultipleChildrenFIFO(t *testing.T) {
	fixture := newCommandShutdownFixture(t)
	a := fixture.spawn(t)
	b := fixture.spawn(t)
	c := fixture.spawn(t)
	fixture.completeMain(t)
	if !BeginCommandShutdown(fixture.p, fixture.main.g) {
		t.Fatal("begin multi-child shutdown")
	}
	for _, child := range []*commandShutdownChild{a, b, c} {
		cancelOneCommandChild(t, fixture.p, child)
	}
	if !FinishCommandShutdown(fixture.p, fixture.main.g) {
		t.Fatal("finish multi-child shutdown")
	}
	keepCommandShutdownFixtureAlive(fixture)
}

func TestCommandShutdownRejectsWaitWithoutPartialMutation(t *testing.T) {
	fixture := newCommandShutdownFixture(t)
	child := fixture.spawn(t)
	yieldSpawnTestG(t, fixture.p, fixture.main.g, fixture.main.frame, fixture.mainAction)
	if got, ok := NextRunnable(fixture.p); !ok || got != child.g {
		t.Fatal("dequeue child before park")
	}
	action := beginSpawnTestChildResume(t, fixture.p, child.g, child.frame)
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm shutdown-rejection wait")
	}
	child.frame.header.SuspendReason = uint16(SuspendPark)
	child.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(child.g, child.handle, child.frame.header, token, ticket) {
		t.Fatal("prepare shutdown-rejection park")
	}
	if action, ok = Resumed(fixture.p, child.g, action); !ok || action.Kind != ActionPark {
		t.Fatal("commit shutdown-rejection park")
	}
	if got, ok := NextRunnable(fixture.p); !ok || got != fixture.main.g {
		t.Fatal("dequeue main beside parked child")
	}
	fixture.mainAction = beginSpawnTestResume(t, fixture.p, fixture.main)
	fixture.completeMain(t)
	beforeSchedule := preemptLoad(&fixture.p.schedule)
	beforeWord := preemptLoad(&token.word)
	beforeHead := fixture.p.waitHead
	if BeginCommandShutdown(fixture.p, fixture.main.g) {
		t.Fatal("shutdown accepted parked child")
	}
	if preemptLoad(&fixture.p.schedule) != beforeSchedule || preemptLoad(&token.word) != beforeWord ||
		fixture.p.waitHead != beforeHead || fixture.p.waitTail != child.g || child.g.state != GWaiting ||
		child.g.destroyTarget != nil || child.g.frames == nil {
		t.Fatal("rejected wait shutdown partially mutated scheduler state")
	}
	keepCommandShutdownFixtureAlive(fixture)
}

func TestCommandShutdownAcceptsIdleOrRequestedGateAndRejectsBusyP(t *testing.T) {
	for _, requested := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "requested"}[requested], func(t *testing.T) {
			fixture := newCommandShutdownFixture(t)
			fixture.spawn(t)
			fixture.completeMain(t)
			if requested && !RequestSchedule(fixture.p) {
				t.Fatal("request schedule before shutdown")
			}
			if !BeginCommandShutdown(fixture.p, fixture.main.g) || preemptLoad(&fixture.p.schedule) != scheduleStopping {
				t.Fatal("begin shutdown from supported gate")
			}
			if RequestSchedule(fixture.p) {
				t.Fatal("stopping gate accepted schedule request")
			}
			keepCommandShutdownFixtureAlive(fixture)
		})
	}

	tests := []struct {
		name   string
		mutate func(*P)
	}{
		{"current", func(p *P) { p.current = new(G) }},
		{"in-resume", func(p *P) { p.inResume = true }},
		{"action", func(p *P) { p.action = Action{Kind: ActionResume, Handle: unsafe.Pointer(new(byte))} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCommandShutdownFixture(t)
			child := fixture.spawn(t)
			fixture.completeMain(t)
			test.mutate(fixture.p)
			beforeHead, beforeTail := fixture.p.readyHead, fixture.p.readyTail
			if BeginCommandShutdown(fixture.p, fixture.main.g) || preemptLoad(&fixture.p.schedule) == scheduleStopping ||
				fixture.p.readyHead != beforeHead || fixture.p.readyTail != beforeTail || child.g.destroyTarget != nil {
				t.Fatal("busy-P shutdown did not fail before mutation")
			}
			keepCommandShutdownFixtureAlive(fixture)
		})
	}
}

func TestCommandShutdownLinearizesWithScheduleRequester(t *testing.T) {
	for iteration := 0; iteration < 250; iteration++ {
		fixture := newCommandShutdownFixture(t)
		fixture.spawn(t)
		fixture.completeMain(t)
		start := make(chan struct{})
		result := make(chan bool, 1)
		go func() {
			<-start
			result <- RequestSchedule(fixture.p)
		}()
		close(start)
		if !BeginCommandShutdown(fixture.p, fixture.main.g) {
			t.Fatalf("iteration %d: begin racing shutdown", iteration)
		}
		_ = <-result // true linearized before stopping; false linearized after it.
		if preemptLoad(&fixture.p.schedule) != scheduleStopping || RequestSchedule(fixture.p) {
			t.Fatalf("iteration %d: stopping gate reopened", iteration)
		}
		keepCommandShutdownFixtureAlive(fixture)
	}
}
