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
	"sync"
	"testing"
	"unsafe"
)

func TestHeaderV1TargetNeutralLayout(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	header := HeaderV1{}
	wants := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"G", unsafe.Offsetof(header.G), 0},
		{"Parent", unsafe.Offsetof(header.Parent), pointerSize},
		{"Descriptor", unsafe.Offsetof(header.Descriptor), 2 * pointerSize},
		{"AllocationBase", unsafe.Offsetof(header.AllocationBase), 3 * pointerSize},
		{"ResultSlot", unsafe.Offsetof(header.ResultSlot), 4 * pointerSize},
		{"SuspendReason", unsafe.Offsetof(header.SuspendReason), 5 * pointerSize},
		{"Lifecycle", unsafe.Offsetof(header.Lifecycle), 5*pointerSize + 2},
		{"StateID", unsafe.Offsetof(header.StateID), 5*pointerSize + 4},
		{"Flags", unsafe.Offsetof(header.Flags), 5*pointerSize + 8},
	}
	for _, field := range wants {
		if field.got != field.want {
			t.Fatalf("HeaderV1.%s offset = %d, want %d", field.name, field.got, field.want)
		}
	}
	rawSize := 5*pointerSize + 12
	wantSize := (rawSize + pointerSize - 1) &^ (pointerSize - 1)
	if got := unsafe.Sizeof(header); got != wantSize {
		t.Fatalf("HeaderV1 size = %d, want %d", got, wantSize)
	}
}

func TestFrameAllocationLayout(t *testing.T) {
	for _, align := range []uintptr{1, 2, 4, 8, 16, 64} {
		total, ok := FrameAllocationSize(37, align)
		if !ok {
			t.Fatalf("FrameAllocationSize(37, %d) rejected", align)
		}
		memory := make([]byte, total)
		raw := unsafe.Pointer(&memory[0])
		storage, ok := AlignedStorage(raw, align)
		if !ok {
			t.Fatalf("AlignedStorage align %d rejected", align)
		}
		if uintptr(storage)%align != 0 {
			t.Fatalf("storage %#x is not aligned to %d", uintptr(storage), align)
		}
		minimum := uintptr(raw) + unsafe.Sizeof(Frame{}) + unsafe.Sizeof(uintptr(0))
		if uintptr(storage) < minimum || uintptr(storage)+37 > uintptr(raw)+total {
			t.Fatalf("storage range [%#x,%#x) outside allocation [%#x,%#x)", uintptr(storage), uintptr(storage)+37, uintptr(raw), uintptr(raw)+total)
		}
		runtime.KeepAlive(memory)
	}
	for _, align := range []uintptr{0, 3, 6} {
		if _, ok := FrameAllocationSize(1, align); ok {
			t.Fatalf("invalid alignment %d accepted", align)
		}
	}
	if _, ok := FrameAllocationSize(^uintptr(0), 8); ok {
		t.Fatal("overflowing frame allocation accepted")
	}
	offset := unsafe.Sizeof(Frame{}) + unsafe.Sizeof(uintptr(0))
	if _, ok := alignedStorageOffset(^uintptr(0)-offset-1, 8); ok {
		t.Fatal("overflowing aligned storage address accepted")
	}
}

type testFrame struct {
	handle     unsafe.Pointer
	header     *HeaderV1
	storage    unsafe.Pointer
	descriptor unsafe.Pointer
	size       uintptr
	align      uintptr
	memory     []byte
}

func newTestFrame(t *testing.T, g *G, handle, parent unsafe.Pointer) *testFrame {
	t.Helper()
	const (
		size  = uintptr(37)
		align = uintptr(16)
	)
	total, ok := FrameAllocationSize(size, align)
	if !ok {
		t.Fatal("compute test frame allocation")
	}
	memory := make([]byte, total)
	descriptor := new(byte)
	storage, ok := RegisterFrame(g, unsafe.Pointer(&memory[0]), total, size, align, unsafe.Pointer(descriptor))
	if !ok {
		t.Fatal("register test frame")
	}
	header := &HeaderV1{
		G:             unsafe.Pointer(g),
		Parent:        parent,
		Descriptor:    unsafe.Pointer(descriptor),
		SuspendReason: uint16(SuspendNone),
		Lifecycle:     uint16(FrameInitialSuspended),
	}
	if !PublishFrame(g, handle, header, storage) {
		t.Fatal("publish test frame")
	}
	return &testFrame{
		handle:     handle,
		header:     header,
		storage:    storage,
		descriptor: unsafe.Pointer(descriptor),
		size:       size,
		align:      align,
		memory:     memory,
	}
}

func releaseTestFrame(t *testing.T, g *G, frame *testFrame) {
	t.Helper()
	raw, total, ok := ReleaseFrame(g, frame.storage, frame.size, frame.align, frame.descriptor)
	if !ok {
		t.Fatal("release test frame")
	}
	if raw != unsafe.Pointer(&frame.memory[0]) || total != uintptr(len(frame.memory)) {
		t.Fatalf("release range = (%p, %d), want (%p, %d)", raw, total, &frame.memory[0], len(frame.memory))
	}
}

func TestFramePublishAndHandoffState(t *testing.T) {
	g := &G{}
	if !InitG(g) {
		t.Fatal("InitG failed")
	}
	parentHandle, childHandle := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	parent := newTestFrame(t, g, parentHandle, nil)
	child := newTestFrame(t, g, childHandle, parentHandle)
	if parent.header.AllocationBase != unsafe.Pointer(&parent.memory[0]) ||
		child.header.AllocationBase != unsafe.Pointer(&child.memory[0]) {
		t.Fatal("frame publication did not expose the raw allocation base")
	}
	if !AdoptRoot(g, parentHandle) {
		t.Fatal("adopt root")
	}
	g.state = GRunning
	g.active.state = FrameActive
	parent.header.SuspendReason = uint16(SuspendCall)
	parent.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareAwait(g, parentHandle, childHandle) {
		t.Fatal("valid child handoff rejected")
	}
	if PrepareAwait(g, parentHandle, childHandle) {
		t.Fatal("duplicate child handoff accepted")
	}
	destroy, yielded, ok := dispatchPending(g, g.active)
	if !ok || destroy != nil || yielded || g.active.handle != childHandle || g.root.state != FrameSuspended {
		t.Fatalf("await dispatch = (destroy=%p, yielded=%t, ok=%t, active=%p, parent=%d)", destroy, yielded, ok, g.active.handle, g.root.state)
	}

	g.active.state = FrameActive
	child.header.SuspendReason = uint16(SuspendFrameComplete)
	child.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(g, childHandle, child.header) {
		t.Fatal("valid child completion rejected")
	}
	destroy, yielded, ok = dispatchPending(g, g.active)
	if !ok || destroy == nil || yielded || destroy.handle != childHandle || g.active != g.root || g.destroyTarget != destroy ||
		destroy.state != FrameDestroyPending || child.header.Lifecycle != uint16(FrameDestroyPending) {
		t.Fatalf("completion dispatch = (destroy=%p, yielded=%t, ok=%t, active=%p, target=%p)", destroy, yielded, ok, g.active, g.destroyTarget)
	}
	releaseTestFrame(t, g, child)
	runtime.KeepAlive(parent.memory)
}

func TestReleaseFrameDoesNotPartiallyCommitFailedUnlink(t *testing.T) {
	g := &G{}
	if !InitG(g) {
		t.Fatal("InitG failed")
	}
	handle := unsafe.Pointer(new(byte))
	test := newTestFrame(t, g, handle, nil)
	frame := FrameFromStorage(test.storage)
	frame.state = FrameDestroyPending
	test.header.Lifecycle = uint16(FrameDestroyPending)
	g.destroyTarget = frame
	g.frames = nil // Simulate corrupted scheduler ownership metadata.

	if _, _, ok := ReleaseFrame(g, test.storage, test.size, test.align, test.descriptor); ok {
		t.Fatal("release unexpectedly accepted a frame missing from the owner list")
	}
	if frame.state != FrameDestroyPending || test.header.Lifecycle != uint16(FrameDestroyPending) || g.destroyTarget != frame {
		t.Fatalf("failed release partially committed: state=%d lifecycle=%d target=%p", frame.state, test.header.Lifecycle, g.destroyTarget)
	}
	runtime.KeepAlive(test.memory)
}

func TestSinglePSchedulerChildDestroyedBeforeParentResume(t *testing.T) {
	runSchedulerScenario(t)
}

func TestTerminalGRejectsResidualSchedulerState(t *testing.T) {
	if TerminalG(nil, nil) || TerminalG(new(P), nil) || TerminalG(nil, new(G)) {
		t.Fatal("nil scheduler state reported terminal")
	}
	terminal := func() *G {
		return &G{magic: gMagic, state: GDead}
	}
	terminalP := func() *P {
		p := new(P)
		preemptStore(&p.schedule, scheduleDisabled)
		return p
	}
	if !TerminalG(terminalP(), terminal()) {
		t.Fatal("strict zero-residue dead G did not report terminal")
	}

	dummyFrame := new(Frame)
	dummyG := new(G)
	tests := []struct {
		name   string
		mutate func(*G)
	}{
		{"magic", func(g *G) { g.magic = 0 }},
		{"preempt", func(g *G) { preemptStore(preemptAddress(g), preemptRequested) }},
		{"state", func(g *G) { g.state = GRunnable }},
		{"root", func(g *G) { g.root = dummyFrame }},
		{"active", func(g *G) { g.active = dummyFrame }},
		{"frames", func(g *G) { g.frames = dummyFrame }},
		{"pending kind", func(g *G) { g.pending.kind = pendingAwait }},
		{"pending from", func(g *G) { g.pending.from = dummyFrame }},
		{"pending target", func(g *G) { g.pending.target = dummyFrame }},
		{"pending wait", func(g *G) { g.pending.wait = new(WaitToken) }},
		{"pending ticket", func(g *G) { g.pending.ticket = 1 }},
		{"destroy target", func(g *G) { g.destroyTarget = dummyFrame }},
		{"destroy root", func(g *G) { g.destroyRoot = true }},
		{"ready link", func(g *G) { g.nextReady = dummyG }},
		{"queued", func(g *G) { g.queued = true }},
		{"wait token", func(g *G) { g.waitToken = new(WaitToken) }},
		{"wait ticket", func(g *G) { g.waitTicket = 1 }},
		{"wait link", func(g *G) { g.nextWait = dummyG }},
		{"waiting", func(g *G) { g.waiting = true }},
		{"running P", func(g *G) { g.runP = new(P) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := terminal()
			test.mutate(g)
			if TerminalG(terminalP(), g) {
				t.Fatal("G with residual scheduler state reported terminal")
			}
		})
	}

	dummyActionHandle := unsafe.Pointer(new(byte))
	pTests := []struct {
		name   string
		mutate func(*P)
	}{
		{"current", func(p *P) { p.current = dummyG }},
		{"ready head", func(p *P) { p.readyHead = dummyG }},
		{"ready tail", func(p *P) { p.readyTail = dummyG }},
		{"wait head", func(p *P) { p.waitHead = dummyG }},
		{"wait tail", func(p *P) { p.waitTail = dummyG }},
		{"schedule idle", func(p *P) { preemptStore(&p.schedule, scheduleIdle) }},
		{"schedule requested", func(p *P) { preemptStore(&p.schedule, scheduleRequested) }},
		{"schedule stopping", func(p *P) { preemptStore(&p.schedule, scheduleStopping) }},
		{"executor mode", func(p *P) { preemptStore(&p.executorMode, executorModeBound) }},
		{"executor pointer", func(p *P) { p.executor = new(ExecutorDriver) }},
		{"in resume", func(p *P) { p.inResume = true }},
		{"action kind", func(p *P) { p.action.Kind = ActionResume }},
		{"action handle", func(p *P) { p.action.Handle = dummyActionHandle }},
		{"timer preempt budget", func(p *P) { p.timerPreemptBudget = 1 }},
	}
	for _, test := range pTests {
		t.Run("P "+test.name, func(t *testing.T) {
			p := terminalP()
			test.mutate(p)
			if TerminalG(p, terminal()) {
				t.Fatal("P with residual scheduler state reported terminal")
			}
		})
	}
}

func runSchedulerScenario(t *testing.T) {
	t.Helper()
	g := &G{}
	if !InitG(g) {
		t.Fatal("initialize G")
	}
	rootHandle, childHandle := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	root := newTestFrame(t, g, rootHandle, nil)
	child := newTestFrame(t, g, childHandle, rootHandle)
	if !AdoptRoot(g, rootHandle) {
		t.Fatal("adopt root")
	}
	p := &P{}
	if !Enqueue(p, g) || Enqueue(p, g) {
		t.Fatal("ready queue must accept a runnable G exactly once")
	}

	frames := map[unsafe.Pointer]*testFrame{rootHandle: root, childHandle: child}
	done := make(map[unsafe.Pointer]bool)
	destroyCount := make(map[unsafe.Pointer]int)
	rootResumes := 0
	childReleased := false
	var events []string
	runnable, ok := NextRunnable(p)
	if !ok || runnable != g {
		t.Fatalf("next runnable = (%p, %t), want (%p, true)", runnable, ok, g)
	}
	action, ok := BeginRunG(p, runnable)
	if !ok {
		t.Fatal("begin scheduler run")
	}
	for action.Kind != ActionComplete {
		switch action.Kind {
		case ActionCheckResume, ActionCheckDestroy:
			action, ok = Checked(p, g, action, done[action.Handle])
		case ActionResume:
			handle := action.Handle
			switch handle {
			case rootHandle:
				rootResumes++
				if rootResumes == 1 {
					events = append(events, "root-await")
					if _, nestedOK := NextRunnable(p); nestedOK {
						t.Error("nested scheduler dequeue accepted")
					}
					if _, nestedOK := BeginRunG(p, g); nestedOK {
						t.Error("nested scheduler run accepted")
					}
					root.header.SuspendReason = uint16(SuspendCall)
					root.header.Lifecycle = uint16(FrameSuspended)
					if !PrepareAwait(g, rootHandle, childHandle) {
						t.Error("prepare child await")
					}
				} else {
					if !childReleased {
						t.Error("parent resumed before child destroy/free completed")
					}
					events = append(events, "root-complete")
					done[rootHandle] = true
					root.header.SuspendReason = uint16(SuspendFrameComplete)
					root.header.Lifecycle = uint16(FrameFinalSuspended)
					if !PrepareComplete(g, rootHandle, root.header) {
						t.Error("prepare root completion")
					}
				}
			case childHandle:
				events = append(events, "child-complete")
				done[childHandle] = true
				child.header.SuspendReason = uint16(SuspendFrameComplete)
				child.header.Lifecycle = uint16(FrameFinalSuspended)
				if !PrepareComplete(g, childHandle, child.header) {
					t.Error("prepare child completion")
				}
			default:
				t.Fatalf("resume unknown handle %p", handle)
			}
			action, ok = Resumed(p, g, action)
		case ActionDestroy:
			handle := action.Handle
			destroyCount[handle]++
			if destroyCount[handle] != 1 {
				t.Errorf("handle %p destroyed %d times", handle, destroyCount[handle])
			}
			events = append(events, map[bool]string{true: "root-destroy", false: "child-destroy"}[handle == rootHandle])
			releaseTestFrame(t, g, frames[handle])
			if handle == childHandle {
				childReleased = true
			}
			action, ok = Destroyed(p, g, action)
		default:
			t.Fatalf("unexpected scheduler action %d", action.Kind)
		}
		if !ok {
			t.Fatalf("scheduler action %d for %p failed", action.Kind, action.Handle)
		}
	}
	want := []string{"root-await", "child-complete", "child-destroy", "root-complete", "root-destroy"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
	if g.state != GDead || g.root != nil || g.active != nil || g.frames != nil || g.destroyTarget != nil || g.destroyRoot {
		t.Fatalf("completed G retained state: state=%d root=%p active=%p frames=%p destroy=%p destroyRoot=%t", g.state, g.root, g.active, g.frames, g.destroyTarget, g.destroyRoot)
	}
	if !TerminalG(p, g) {
		t.Fatal("completed G failed strict terminal-state validation")
	}
	if p.current != nil || p.readyHead != nil || p.readyTail != nil || p.inResume || p.action.Kind != ActionInvalid {
		t.Fatalf("completed P retained state: current=%p head=%p tail=%p resume=%t action=%d", p.current, p.readyHead, p.readyTail, p.inResume, p.action.Kind)
	}
	if destroyCount[rootHandle] != 1 || destroyCount[childHandle] != 1 {
		t.Fatalf("destroy counts = root:%d child:%d", destroyCount[rootHandle], destroyCount[childHandle])
	}
	runtime.KeepAlive(root.memory)
	runtime.KeepAlive(child.memory)
}

func TestIndependentSinglePSchedulersRace(t *testing.T) {
	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			runSchedulerScenario(t)
		}()
	}
	wg.Wait()
}
