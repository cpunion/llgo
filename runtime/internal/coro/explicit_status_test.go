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

type explicitPanicFixture struct {
	p        *P
	g        *G
	frames   []*testFrame
	byHandle map[unsafe.Pointer]*testFrame
	action   Action
}

func newExplicitPanicFixture(t *testing.T, depth int) *explicitPanicFixture {
	t.Helper()
	if depth < 1 {
		t.Fatal("explicit panic fixture requires at least one frame")
	}
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize explicit panic G")
	}
	frames := make([]*testFrame, depth)
	byHandle := make(map[unsafe.Pointer]*testFrame, depth)
	var parent unsafe.Pointer
	for index := range frames {
		handle := unsafe.Pointer(new(byte))
		frames[index] = newTestFrame(t, g, handle, parent)
		frames[index].header.StateID = uint32(index + 1)
		byHandle[handle] = frames[index]
		parent = handle
	}
	if !AdoptRoot(g, frames[0].handle) {
		t.Fatal("adopt explicit panic root")
	}
	p := new(P)
	if !Enqueue(p, g) {
		t.Fatal("enqueue explicit panic G")
	}
	if got, ok := NextRunnable(p); !ok || got != g {
		t.Fatalf("dequeue explicit panic G = (%p, %t)", got, ok)
	}
	action, ok := BeginRunG(p, g)
	if !ok || action.Kind != ActionCheckResume {
		t.Fatalf("begin explicit panic G = (%+v, %t)", action, ok)
	}
	action, ok = Checked(p, g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatalf("activate explicit panic root = (%+v, %t)", action, ok)
	}
	frames[0].header.SuspendReason = uint16(SuspendNone)
	frames[0].header.Lifecycle = uint16(FrameActive)

	for index := 0; index+1 < len(frames); index++ {
		current, child := frames[index], frames[index+1]
		current.header.SuspendReason = uint16(SuspendCall)
		current.header.Lifecycle = uint16(FrameSuspended)
		if !PrepareAwait(g, current.handle, child.handle) {
			t.Fatalf("prepare explicit panic await %d", index)
		}
		action, ok = Resumed(p, g, action)
		if !ok || action.Kind != ActionCheckResume || action.Handle != child.handle {
			t.Fatalf("dispatch explicit panic child %d = (%+v, %t)", index, action, ok)
		}
		action, ok = Checked(p, g, action, false)
		if !ok || action.Kind != ActionResume || action.Handle != child.handle {
			t.Fatalf("activate explicit panic child %d = (%+v, %t)", index, action, ok)
		}
		child.header.SuspendReason = uint16(SuspendNone)
		child.header.Lifecycle = uint16(FrameActive)
	}
	return &explicitPanicFixture{p: p, g: g, frames: frames, byHandle: byHandle, action: action}
}

func (fixture *explicitPanicFixture) publish(t *testing.T, typeWord, dataWord unsafe.Pointer) {
	t.Helper()
	leaf := fixture.frames[len(fixture.frames)-1]
	leaf.header.SuspendReason = uint16(SuspendPanic)
	leaf.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PreparePanic(fixture.g, leaf.handle, leaf.header, typeWord, dataWord) {
		t.Fatal("publish explicit panic")
	}
}

func (fixture *explicitPanicFixture) beginPanicDestroy(t *testing.T) Action {
	t.Helper()
	action, ok := Resumed(fixture.p, fixture.g, fixture.action)
	if !ok || action.Kind != ActionCheckDestroy || action.Handle != fixture.frames[len(fixture.frames)-1].handle {
		t.Fatalf("panic active-frame completion = (%+v, %t)", action, ok)
	}
	action, ok = Checked(fixture.p, fixture.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("panic active-frame done check = (%+v, %t)", action, ok)
	}
	return action
}

func (fixture *explicitPanicFixture) release(t *testing.T, action Action) {
	t.Helper()
	frame := fixture.byHandle[action.Handle]
	if frame == nil {
		t.Fatalf("release unknown panic handle %p", action.Handle)
	}
	releaseTestFrame(t, fixture.g, frame)
}

func (fixture *explicitPanicFixture) commitDestroyed(action Action) (Action, bool) {
	switch action.Kind {
	case ActionDestroy:
		return Destroyed(fixture.p, fixture.g, action)
	case ActionPanicDestroy:
		return PanicDestroyed(fixture.p, fixture.g, action)
	default:
		return Action{}, false
	}
}

func (fixture *explicitPanicFixture) acknowledgeTerminalSchedule(action Action) bool {
	switch action.Kind {
	case ActionDestroy:
		return AcknowledgeTerminalSchedule(fixture.p, fixture.g, action)
	case ActionPanicDestroy:
		return AcknowledgePanicTerminalSchedule(fixture.p, fixture.g, action)
	default:
		return false
	}
}

func (fixture *explicitPanicFixture) finish(t *testing.T, action Action) ([]unsafe.Pointer, Action) {
	t.Helper()
	destroyed := make([]unsafe.Pointer, 0, len(fixture.frames))
	for {
		switch action.Kind {
		case ActionDestroy:
			destroyed = append(destroyed, action.Handle)
			fixture.release(t, action)
			var ok bool
			action, ok = fixture.commitDestroyed(action)
			if !ok {
				t.Fatalf("commit panic active destroy = (%+v, %t)", action, ok)
			}
		case ActionPanicDestroy:
			destroyed = append(destroyed, action.Handle)
			fixture.release(t, action)
			var ok bool
			action, ok = fixture.commitDestroyed(action)
			if !ok {
				t.Fatalf("commit panic ancestor destroy = (%+v, %t)", action, ok)
			}
		case ActionPanicComplete:
			return destroyed, action
		default:
			t.Fatalf("panic path resumed or emitted unexpected action %+v", action)
		}
	}
}

func TestExplicitPanicPublishOnceRace(t *testing.T) {
	fixture := newExplicitPanicFixture(t, 1)
	leaf := fixture.frames[0]
	leaf.header.SuspendReason = uint16(SuspendPanic)
	leaf.header.Lifecycle = uint16(FrameFinalSuspended)

	const contenders = 32
	typeWords := new([contenders]byte)
	dataWords := new([contenders]byte)
	winners := make(chan int, contenders)
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(contenders)
	for index := 0; index < contenders; index++ {
		go func(index int) {
			defer group.Done()
			<-start
			if PreparePanic(fixture.g, leaf.handle, leaf.header,
				unsafe.Pointer(&typeWords[index]), unsafe.Pointer(&dataWords[index])) {
				winners <- index
			}
		}(index)
	}
	close(start)
	group.Wait()
	close(winners)
	winner := -1
	for index := range winners {
		if winner != -1 {
			t.Fatalf("multiple explicit panic publishers won: %d and %d", winner, index)
		}
		winner = index
	}
	if winner < 0 {
		t.Fatal("no explicit panic publisher won")
	}
	record, ok := LoadPanicRecord(fixture.g)
	if !ok || record.Status != ExplicitStatusPanic ||
		record.TypeWord != unsafe.Pointer(&typeWords[winner]) || record.DataWord != unsafe.Pointer(&dataWords[winner]) {
		t.Fatalf("published panic record = (%+v, %t), winner=%d", record, ok, winner)
	}
	destroyed, action := fixture.finish(t, fixture.beginPanicDestroy(t))
	if len(destroyed) != 1 || destroyed[0] != leaf.handle || action.Kind != ActionPanicComplete {
		t.Fatalf("single-frame panic destroy = %v / %+v", destroyed, action)
	}
	if TerminalG(fixture.p, fixture.g) || ReclaimableG(fixture.g) {
		t.Fatal("published panic was misclassified as ordinary completion")
	}
	runtime.KeepAlive(typeWords)
	runtime.KeepAlive(dataWords)
	runtime.KeepAlive(leaf.memory)
}

func explicitFramePermutations() [][3]int {
	return [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
}

func TestExplicitPanicDestroysDeepestToRootAcrossFrameListShuffle(t *testing.T) {
	for _, permutation := range explicitFramePermutations() {
		permutation := permutation
		t.Run(string(rune('0'+permutation[0]))+string(rune('0'+permutation[1]))+string(rune('0'+permutation[2])), func(t *testing.T) {
			fixture := newExplicitPanicFixture(t, 3)
			metadata := make([]*Frame, len(fixture.frames))
			for index, frame := range fixture.frames {
				metadata[index] = FrameFromStorage(frame.storage)
			}
			for index, source := range permutation {
				metadata[source].next = nil
				if index+1 < len(permutation) {
					metadata[source].next = metadata[permutation[index+1]]
				}
			}
			fixture.g.frames = metadata[permutation[0]]

			typeWord, dataWord := new(byte), new(byte)
			fixture.publish(t, unsafe.Pointer(typeWord), unsafe.Pointer(dataWord))
			destroyed, action := fixture.finish(t, fixture.beginPanicDestroy(t))
			want := []unsafe.Pointer{fixture.frames[2].handle, fixture.frames[1].handle, fixture.frames[0].handle}
			if len(destroyed) != len(want) {
				t.Fatalf("destroy order = %v, want %v", destroyed, want)
			}
			for index := range want {
				if destroyed[index] != want[index] {
					t.Fatalf("destroy order = %v, want deepest-to-root %v", destroyed, want)
				}
			}
			if action.Kind != ActionPanicComplete || action.Handle != nil || fixture.g.state != GDead ||
				fixture.g.root != nil || fixture.g.active != nil || fixture.g.frames != nil || fixture.g.panicUnwind {
				t.Fatalf("panic terminal state = action:%+v state:%d root:%p active:%p frames:%p unwind:%t",
					action, fixture.g.state, fixture.g.root, fixture.g.active, fixture.g.frames, fixture.g.panicUnwind)
			}
			if record, ok := LoadPanicRecord(fixture.g); !ok || record.TypeWord != unsafe.Pointer(typeWord) || record.DataWord != unsafe.Pointer(dataWord) {
				t.Fatalf("post-destroy task-local record = (%+v, %t)", record, ok)
			}
			for _, frame := range fixture.frames {
				runtime.KeepAlive(frame.memory)
			}
		})
	}
}

func TestExplicitStatusUnsupportedShapesFailClosed(t *testing.T) {
	tests := []struct {
		name     string
		status   ExplicitStatus
		typeWord bool
		flags    uint32
	}{
		{name: "normal return", status: ExplicitStatusReturn, typeWord: true},
		{name: "goexit", status: ExplicitStatusGoexit, typeWord: true},
		{name: "implicit fault", status: ExplicitStatusImplicitFault, typeWord: true},
		{name: "explicit nil", status: ExplicitStatusPanic},
		{name: "cleanup", status: ExplicitStatusPanic, typeWord: true, flags: 1 << 0},
		{name: "recover", status: ExplicitStatusPanic, typeWord: true, flags: 1 << 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExplicitPanicFixture(t, 1)
			leaf := fixture.frames[0]
			leaf.header.SuspendReason = uint16(SuspendPanic)
			leaf.header.Lifecycle = uint16(FrameFinalSuspended)
			leaf.header.Flags = test.flags
			var typeWord unsafe.Pointer
			if test.typeWord {
				typeWord = unsafe.Pointer(new(byte))
			}
			if PrepareExplicitStatus(fixture.g, leaf.handle, leaf.header, test.status, typeWord, unsafe.Pointer(new(byte))) {
				t.Fatal("unsupported explicit terminal shape accepted")
			}
			if fixture.g.pending.kind != pendingNone || fixture.g.panicUnwind {
				t.Fatal("rejected explicit terminal shape mutated scheduler transition")
			}
			if record, ok := LoadPanicRecord(fixture.g); ok || record != (PanicRecordSnapshot{}) {
				t.Fatalf("rejected explicit terminal shape published record (%+v, %t)", record, ok)
			}
			leaf.header.Flags = 0
			if PreparePanic(fixture.g, leaf.handle, leaf.header, unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))) {
				t.Fatal("poisoned one-shot record accepted a later supported panic")
			}
			runtime.KeepAlive(leaf.memory)
		})
	}
}

func TestExplicitPanicRejectsUnsupportedAncestorBeforeDestroy(t *testing.T) {
	fixture := newExplicitPanicFixture(t, 2)
	root, leaf := fixture.frames[0], fixture.frames[1]
	rootMetadata, leafMetadata := FrameFromStorage(root.storage), FrameFromStorage(leaf.storage)
	root.header.Flags = 1 // cleanup/recover metadata is not representable in v0.
	leaf.header.SuspendReason = uint16(SuspendPanic)
	leaf.header.Lifecycle = uint16(FrameFinalSuspended)
	if PreparePanic(fixture.g, leaf.handle, leaf.header, unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))) {
		t.Fatal("panic with unsupported suspended ancestor was published")
	}
	if fixture.g.pending.kind != pendingNone || fixture.g.panicUnwind || fixture.g.destroyTarget != nil ||
		leafMetadata.state != FrameActive || rootMetadata.state != FrameSuspended ||
		leaf.header.Lifecycle != uint16(FrameFinalSuspended) || root.header.Lifecycle != uint16(FrameSuspended) {
		t.Fatal("rejected ancestor cleanup mutated frame destruction state")
	}
	if record, ok := LoadPanicRecord(fixture.g); ok || record != (PanicRecordSnapshot{}) {
		t.Fatalf("rejected ancestor cleanup published record (%+v, %t)", record, ok)
	}
	runtime.KeepAlive(root.memory)
	runtime.KeepAlive(leaf.memory)
}

func TestExplicitPanicRechecksAncestorBeforeDirectDestroy(t *testing.T) {
	fixture := newExplicitPanicFixture(t, 2)
	root := fixture.frames[0]
	rootMetadata := FrameFromStorage(root.storage)
	fixture.publish(t, unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte)))
	action := fixture.beginPanicDestroy(t)
	fixture.release(t, action)

	// Model corrupted or version-skewed metadata after publication. The active
	// panic frame may already be gone, but the unsupported ancestor must never
	// be directly destroyed or resumed.
	root.header.Flags = 1
	next, ok := Destroyed(fixture.p, fixture.g, action)
	if ok || next != (Action{}) || fixture.g.destroyTarget != nil ||
		rootMetadata.state != FrameSuspended || root.header.Lifecycle != uint16(FrameSuspended) {
		t.Fatalf("unsupported ancestor entered direct destroy: action=(%+v, %t), state=%d lifecycle=%d",
			next, ok, rootMetadata.state, root.header.Lifecycle)
	}
	runtime.KeepAlive(root.memory)
}

func TestExplicitPanicTerminalScheduleRaceDoesNotRedestroy(t *testing.T) {
	tests := []struct {
		name  string
		depth int
	}{
		{name: "active root", depth: 1},
		{name: "suspended ancestor root", depth: 2},
	}
	const iterations = 250
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for iteration := 0; iteration < iterations; iteration++ {
				fixture := newExplicitPanicFixture(t, test.depth)
				fixture.publish(t, unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte)))
				action := fixture.beginPanicDestroy(t)
				for action.Handle != fixture.frames[0].handle {
					fixture.release(t, action)
					var ok bool
					action, ok = fixture.commitDestroyed(action)
					if !ok {
						t.Fatalf("iteration %d: prepare panic root destroy = (%+v, %t)", iteration, action, ok)
					}
				}
				if test.depth == 1 && action.Kind != ActionDestroy ||
					test.depth > 1 && action.Kind != ActionPanicDestroy {
					t.Fatalf("iteration %d: panic root destroy kind = %+v", iteration, action)
				}
				fixture.release(t, action)

				start := make(chan struct{})
				requestResult := make(chan bool, 1)
				commitResult := make(chan struct {
					action Action
					ok     bool
				}, 1)
				go func() {
					<-start
					requestResult <- RequestSchedule(fixture.p)
				}()
				go func() {
					<-start
					next, committed := fixture.commitDestroyed(action)
					commitResult <- struct {
						action Action
						ok     bool
					}{next, committed}
				}()
				close(start)
				requested := <-requestResult
				committed := <-commitResult
				if committed.ok {
					if committed.action.Kind != ActionPanicComplete || requested ||
						preemptLoad(&fixture.p.schedule) != scheduleDisabled {
						t.Fatalf("iteration %d: terminal winner = action:%+v request:%t schedule:%d",
							iteration, committed.action, requested, preemptLoad(&fixture.p.schedule))
					}
				} else {
					if !requested || preemptLoad(&fixture.p.schedule) != scheduleRequested ||
						!fixture.g.destroyRoot || fixture.g.frames != nil || fixture.g.active != nil {
						t.Fatalf("iteration %d: request winner partially committed terminal state", iteration)
					}
					if !fixture.acknowledgeTerminalSchedule(action) {
						t.Fatalf("iteration %d: acknowledge panic terminal schedule", iteration)
					}
					committed.action, committed.ok = fixture.commitDestroyed(action)
					if !committed.ok || committed.action.Kind != ActionPanicComplete {
						t.Fatalf("iteration %d: retry panic terminal commit = (%+v, %t)", iteration, committed.action, committed.ok)
					}
				}
				if _, ok := LoadPanicRecord(fixture.g); !ok || TerminalG(fixture.p, fixture.g) || ReclaimableG(fixture.g) {
					t.Fatalf("iteration %d: terminal panic record/state invalid", iteration)
				}
				for _, frame := range fixture.frames {
					runtime.KeepAlive(frame.memory)
				}
			}
		})
	}
}
