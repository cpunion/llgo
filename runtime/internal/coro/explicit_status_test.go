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
	action, ok = checkedTestAction(p, g, action, false)
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
		action, ok = checkedTestAction(p, g, action, false)
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

func TestExplicitPanicRetainsDescriptorTraceDeepestToRoot(t *testing.T) {
	fixture := newExplicitPanicFixture(t, 3)
	descriptors := []*FrameDescriptorV1{
		{
			Version:  1,
			Flags:    FrameDescriptorTraceHiddenV1,
			Function: "runtime.__llgo_coro_program_bootstrap",
		},
		{
			Version:  1,
			Function: "main.outer",
			File:     "/src/main.go",
		},
		{
			Version:  1,
			Function: "main.inner",
			File:     "/src/main.go",
		},
	}
	lines := []uint32{0, 20, 11}
	for index, frame := range fixture.frames {
		descriptor := unsafe.Pointer(descriptors[index])
		frame.descriptor = descriptor
		frame.header.Descriptor = descriptor
		frame.header.Line = lines[index]
	}

	typeWord, dataWord := new(byte), new(byte)
	fixture.publish(t, unsafe.Pointer(typeWord), unsafe.Pointer(dataWord))
	action := fixture.beginPanicDestroy(t)
	for action.Kind == ActionDestroy || action.Kind == ActionPanicDestroy {
		frame := fixture.byHandle[action.Handle]
		if frame == nil {
			t.Fatalf("retain unknown panic handle %p", action.Handle)
		}
		raw, total, ok := ReleaseFrame(
			fixture.g,
			frame.storage,
			frame.size,
			frame.align,
			frame.descriptor,
		)
		if !ok {
			t.Fatalf("release trace frame %p", action.Handle)
		}
		if !RetainPanicTraceFrame(fixture.g, raw, total) {
			t.Fatalf("retain trace frame %p", action.Handle)
		}
		action, ok = fixture.commitDestroyed(action)
		if !ok {
			t.Fatalf("commit retained panic frame = (%+v, %t)", action, ok)
		}
	}
	if action.Kind != ActionPanicComplete {
		t.Fatalf("retained panic terminal action = %+v", action)
	}

	wants := []PanicTraceFrameSnapshot{
		{Function: "main.inner", File: "/src/main.go", Line: 11},
		{Function: "main.outer", File: "/src/main.go", Line: 20},
		{Function: "runtime.__llgo_coro_program_bootstrap", Hidden: true},
	}
	cursor := FirstPanicTraceFrame(fixture.g)
	for index, want := range wants {
		if cursor == nil {
			t.Fatalf("trace ended before frame %d", index)
		}
		got, next, ok := LoadPanicTraceFrame(fixture.g, cursor)
		if !ok || got != want {
			t.Fatalf("trace frame %d = (%+v, %t), want %+v", index, got, ok, want)
		}
		cursor = next
	}
	if cursor != nil {
		t.Fatal("trace contains an unexpected frame after the hidden root")
	}
	runtime.KeepAlive(descriptors)
	for _, frame := range fixture.frames {
		runtime.KeepAlive(frame.memory)
	}
}

func TestActiveTraceFrameUsesCurrentCompilerDescriptor(t *testing.T) {
	fixture := newExplicitPanicFixture(t, 1)
	frame := fixture.frames[0]
	descriptor := &FrameDescriptorV1{
		Version:  1,
		Function: "main.viaGo",
		File:     "/src/main.go",
	}
	frame.descriptor = unsafe.Pointer(descriptor)
	frame.header.Descriptor = unsafe.Pointer(descriptor)
	frame.header.Line = 17

	want := PanicTraceFrameSnapshot{
		Function: "main.viaGo",
		File:     "/src/main.go",
		Line:     17,
	}
	if got, ok := ActiveTraceFrame(fixture.g); !ok || got != want {
		t.Fatalf("active trace frame = (%+v, %t), want %+v", got, ok, want)
	}
	frame.header.SuspendReason = uint16(SuspendPark)
	frame.header.Lifecycle = uint16(FrameSuspended)
	if got, ok := ActiveTraceFrame(fixture.g); !ok || got != want {
		t.Fatalf("park-resume trace frame = (%+v, %t), want %+v", got, ok, want)
	}
	frame.header.SuspendReason = uint16(SuspendNone)
	if got, ok := ActiveTraceFrame(fixture.g); ok || got != (PanicTraceFrameSnapshot{}) {
		t.Fatalf("incoherent active trace header accepted: %+v", got)
	}
	frame.header.Lifecycle = uint16(FrameActive)

	descriptor.Flags = FrameDescriptorTraceHiddenV1
	want.Hidden = true
	if got, ok := ActiveTraceFrame(fixture.g); !ok || got != want {
		t.Fatalf("hidden active trace frame = (%+v, %t), want %+v", got, ok, want)
	}
	descriptor.Flags |= 1 << 31
	if got, ok := ActiveTraceFrame(fixture.g); ok || got != (PanicTraceFrameSnapshot{}) {
		t.Fatalf("invalid active trace descriptor accepted: %+v", got)
	}
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(frame.memory)
}

func TestTerminalReplacementStagesDifferentPayloadTraceForRelease(t *testing.T) {
	fixture := newExplicitPanicFixture(t, 1)
	oldType, oldData := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	traceMemory, descriptor := retainDetachedTestPanicTrace(
		t, fixture.g, FrameFromStorage(fixture.frames[0].storage), oldType, oldData,
	)
	traceRaw := unsafe.Pointer(&traceMemory[0])

	newType, newData := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	leaf := fixture.frames[0]
	if !ReplacePanicTrace(fixture.g, leaf.handle) ||
		!PanicTraceDiscardPending(fixture.g) {
		t.Fatalf("terminal replacement hook discard = %t",
			PanicTraceDiscardPending(fixture.g))
	}
	raw, total, ok := TakeDiscardedPanicTraceFrame(fixture.g)
	if !ok || raw != traceRaw || total != uintptr(len(traceMemory)) {
		t.Fatalf("terminal replacement discarded trace = (%p, %d, %t), want (%p, %d)",
			raw, total, ok, traceRaw, len(traceMemory))
	}
	if raw, total, ok = TakeDiscardedPanicTraceFrame(fixture.g); !ok ||
		raw != nil || total != 0 || !emptyPanicTrace(fixture.g) {
		t.Fatalf("terminal replacement trace drain = (%p, %d, %t)", raw, total, ok)
	}
	leaf.header.SuspendReason = uint16(SuspendPanic)
	leaf.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PreparePanic(fixture.g, leaf.handle, leaf.header, newType, newData) {
		t.Fatal("publish terminal replacement panic after exact hook")
	}
	record, published := LoadPanicRecord(fixture.g)
	if !published || record.TypeWord != newType || record.DataWord != newData {
		t.Fatalf("terminal replacement record = (%+v, %t)", record, published)
	}
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(traceMemory)
	runtime.KeepAlive(oldType)
	runtime.KeepAlive(oldData)
	runtime.KeepAlive(newType)
	runtime.KeepAlive(newData)
	runtime.KeepAlive(leaf.memory)
}

func TestTerminalReplacementRequiresExactCompilerHook(t *testing.T) {
	fixture := newExplicitPanicFixture(t, 1)
	leaf := fixture.frames[0]
	oldType, oldData := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	traceMemory, descriptor := retainDetachedTestPanicTrace(
		t, fixture.g, FrameFromStorage(leaf.storage), oldType, oldData,
	)

	newType, newData := unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))
	leaf.header.SuspendReason = uint16(SuspendPanic)
	leaf.header.Lifecycle = uint16(FrameFinalSuspended)
	if PreparePanic(fixture.g, leaf.handle, leaf.header, newType, newData) {
		t.Fatal("terminal replacement without compiler semantic hook was accepted")
	}
	if !activePanicTrace(fixture.g) || PanicTraceDiscardPending(fixture.g) {
		t.Fatalf("rejected replacement mutated trace ownership: active=%t discard=%t",
			activePanicTrace(fixture.g), PanicTraceDiscardPending(fixture.g))
	}
	if _, published := LoadPanicRecord(fixture.g); published {
		t.Fatal("rejected replacement published a terminal panic record")
	}
	runtime.KeepAlive(descriptor)
	runtime.KeepAlive(traceMemory)
	runtime.KeepAlive(oldType)
	runtime.KeepAlive(oldData)
	runtime.KeepAlive(newType)
	runtime.KeepAlive(newData)
	runtime.KeepAlive(leaf.memory)
}

func TestExplicitStatusUnsupportedShapesFailClosed(t *testing.T) {
	tests := []struct {
		name     string
		status   ExplicitStatus
		typeWord bool
	}{
		{name: "normal return", status: ExplicitStatusReturn, typeWord: true},
		{name: "goexit", status: ExplicitStatusGoexit, typeWord: true},
		{name: "implicit fault", status: ExplicitStatusImplicitFault, typeWord: true},
		{name: "explicit nil", status: ExplicitStatusPanic},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExplicitPanicFixture(t, 1)
			leaf := fixture.frames[0]
			leaf.header.SuspendReason = uint16(SuspendPanic)
			leaf.header.Lifecycle = uint16(FrameFinalSuspended)
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
			if PreparePanic(fixture.g, leaf.handle, leaf.header, unsafe.Pointer(new(byte)), unsafe.Pointer(new(byte))) {
				t.Fatal("poisoned one-shot record accepted a later supported panic")
			}
			runtime.KeepAlive(leaf.memory)
		})
	}
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
