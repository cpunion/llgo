//go:build coro_panic_payload_adapter_test

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

package runtime

import (
	goruntime "runtime"
	"testing"
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

// This named source island supplies only the runtime interface prefix and
// PanicNilError contract consumed by coro_panic_payload.go.
type _type struct{}
type eface struct {
	_type *_type
	data  unsafe.Pointer
}

type PanicNilError struct {
	_ [0]*PanicNilError
}

func (*PanicNilError) Error() string { return "panic called with nil argument" }
func (*PanicNilError) RuntimeError() {}

func coroRuntimeAbort(message string) { panic(message) }

type coroPanicPayloadTestFrameV1 struct {
	handle     unsafe.Pointer
	header     *coro.HeaderV1
	storage    unsafe.Pointer
	descriptor unsafe.Pointer
	raw        unsafe.Pointer
	total      uintptr
	size       uintptr
	align      uintptr
	memory     []uintptr
}

func newCoroPanicPayloadTestFrameV1(
	t *testing.T,
	g *coro.G,
	parent unsafe.Pointer,
) *coroPanicPayloadTestFrameV1 {
	t.Helper()
	const (
		size  = uintptr(37)
		align = uintptr(16)
	)
	total, ok := coro.FrameAllocationSize(size, align)
	if !ok {
		t.Fatal("compute panic payload frame allocation")
	}
	wordSize := unsafe.Sizeof(uintptr(0))
	memory := make([]uintptr, (total+wordSize-1)/wordSize)
	raw := unsafe.Pointer(&memory[0])
	descriptor := unsafe.Pointer(&coro.FrameDescriptorV1{Version: 1, ResultAlign: 1})
	storage, ok := coro.RegisterFrame(g, raw, total, size, align, descriptor)
	if !ok {
		t.Fatal("register panic payload frame")
	}
	handle := unsafe.Pointer(new(byte))
	header := &coro.HeaderV1{
		G:             unsafe.Pointer(g),
		Parent:        parent,
		Descriptor:    descriptor,
		SuspendReason: uint16(coro.SuspendNone),
		Lifecycle:     uint16(coro.FrameInitialSuspended),
	}
	if !coro.PublishFrame(g, handle, header, storage) {
		t.Fatal("publish panic payload frame")
	}
	return &coroPanicPayloadTestFrameV1{
		handle:     handle,
		header:     header,
		storage:    storage,
		descriptor: descriptor,
		raw:        raw,
		total:      total,
		size:       size,
		align:      align,
		memory:     memory,
	}
}

func releaseCoroPanicPayloadTestFrameV1(
	t *testing.T,
	g *coro.G,
	frame *coroPanicPayloadTestFrameV1,
) {
	t.Helper()
	raw, total, ok := coro.ReleaseFrame(g, frame.storage, frame.size, frame.align, frame.descriptor)
	if !ok || raw != frame.raw || total != frame.total {
		t.Fatalf("release panic payload frame = (%p, %d, %t), want (%p, %d, true)",
			raw, total, ok, frame.raw, frame.total)
	}
}

func activateCoroPanicPayloadActionV1(
	t *testing.T,
	p *coro.P,
	g *coro.G,
	action coro.Action,
	handle unsafe.Pointer,
) coro.Action {
	t.Helper()
	action, ok := coro.Checked(p, g, action, false)
	if !ok || action.Kind != coro.ActionResume || action.Handle != handle {
		t.Fatalf("activate panic payload frame = (%+v, %t)", action, ok)
	}
	if outcome, caseID, task, sourceSlot, generation, ok := coro.TakeRunDecisionWords(g, 0, 0); !ok || outcome != 0 || caseID != 0 || task != 0 || sourceSlot != 0 || generation != 0 {
		t.Fatalf("take panic payload resume gate = (%d, %d, %d, %d, %d, %t)",
			outcome, caseID, task, sourceSlot, generation, ok)
	}
	return action
}

func beginCoroPanicPayloadRootV1(
	t *testing.T,
	p *coro.P,
	g *coro.G,
	frame *coroPanicPayloadTestFrameV1,
) coro.Action {
	t.Helper()
	if !coro.AdoptRoot(g, frame.handle) || !coro.Enqueue(p, g) {
		t.Fatal("adopt and enqueue panic payload root")
	}
	if next, ok := coro.NextRunnable(p); !ok || next != g {
		t.Fatalf("dequeue panic payload root = (%p, %t)", next, ok)
	}
	action, ok := coro.BeginRunG(p, g)
	if !ok || action.Kind != coro.ActionCheckResume || action.Handle != frame.handle {
		t.Fatalf("begin panic payload root = (%+v, %t)", action, ok)
	}
	return activateCoroPanicPayloadActionV1(t, p, g, action, frame.handle)
}

func coroPanicNilPayloadWordsV1(t *testing.T) (unsafe.Pointer, unsafe.Pointer) {
	t.Helper()
	payload := *(*eface)(unsafe.Pointer(&coroPanicNilPayloadV1))
	if payload._type == nil || payload.data == nil {
		t.Fatalf("panic(nil) package payload = (%p, %p)", payload._type, payload.data)
	}
	return unsafe.Pointer(payload._type), payload.data
}

func TestCoroNormalizePanicNilPayloadV1IsAllocationFreeStableAndTyped(t *testing.T) {
	wantType, wantData := coroPanicNilPayloadWordsV1(t)
	if wantData != unsafe.Pointer(&coroPanicNilErrorV1) {
		t.Fatalf("panic(nil) data word = %p, want package-owned %p", wantData, &coroPanicNilErrorV1)
	}
	marker := unsafe.Pointer(new(byte))
	if allocations := testing.AllocsPerRun(1000, func() {
		typeWord, dataWord := coroNormalizePanicPayloadV1(nil, marker)
		if typeWord != wantType || dataWord != wantData {
			panic("unstable panic(nil) interface words")
		}
	}); allocations != 0 {
		t.Fatalf("panic(nil) normalization allocations = %v, want 0", allocations)
	}

	typeWord, dataWord := coroNormalizePanicPayloadV1(nil, nil)
	value := *(*any)(unsafe.Pointer(&eface{_type: (*_type)(typeWord), data: dataWord}))
	panicNil, ok := value.(*PanicNilError)
	if !ok || panicNil != &coroPanicNilErrorV1 || panicNil.Error() != "panic called with nil argument" {
		t.Fatalf("normalized panic(nil) value = (%#v, %t)", value, ok)
	}

	if gotType, gotData := coroNormalizePanicPayloadV1(marker, nil); gotType != marker || gotData != nil {
		t.Fatalf("typed nil payload changed = (%p, %p), want (%p, nil)", gotType, gotData, marker)
	}
	goruntime.GC()
	if gotType, gotData := coroNormalizePanicPayloadV1(nil, nil); gotType != wantType || gotData != wantData {
		t.Fatalf("panic(nil) payload changed after GC = (%p, %p), want (%p, %p)",
			gotType, gotData, wantType, wantData)
	}
	goruntime.KeepAlive(coroPanicNilPayloadV1)
}

func TestCoroPanicNilHookPublishesRootPanicRecord(t *testing.T) {
	g := new(coro.G)
	if !coro.InitG(g) {
		t.Fatal("initialize panic(nil) root G")
	}
	frame := newCoroPanicPayloadTestFrameV1(t, g, nil)
	p := new(coro.P)
	action := beginCoroPanicPayloadRootV1(t, p, g, frame)
	frame.header.SuspendReason = uint16(coro.SuspendPanic)
	frame.header.Lifecycle = uint16(coro.FrameFinalSuspended)
	__llgo_coro_panic_prepare_v1(unsafe.Pointer(g), frame.handle, unsafe.Pointer(frame.header), nil, nil)

	wantType, wantData := coroPanicNilPayloadWordsV1(t)
	record, published := coro.LoadPanicRecord(g)
	if !published || record.Status != coro.ExplicitStatusPanic ||
		record.TypeWord != wantType || record.DataWord != wantData {
		t.Fatalf("root panic(nil) record = (%+v, %t), want (%p, %p)",
			record, published, wantType, wantData)
	}

	action, ok := coro.Resumed(p, g, action)
	if !ok || action.Kind != coro.ActionCheckDestroy || action.Handle != frame.handle {
		t.Fatalf("dispatch panic(nil) root destroy = (%+v, %t)", action, ok)
	}
	action, ok = coro.Checked(p, g, action, true)
	if !ok || action.Kind != coro.ActionDestroy || action.Handle != frame.handle {
		t.Fatalf("activate panic(nil) root destroy = (%+v, %t)", action, ok)
	}
	releaseCoroPanicPayloadTestFrameV1(t, g, frame)
	action, ok = coro.Destroyed(p, g, action)
	if !ok || action.Kind != coro.ActionPanicComplete {
		t.Fatalf("commit panic(nil) root destroy = (%+v, %t)", action, ok)
	}
	goruntime.GC()
	record, published = coro.LoadPanicRecord(g)
	if !published || record.TypeWord != wantType || record.DataWord != wantData {
		t.Fatalf("root panic(nil) record after frame destruction = (%+v, %t)", record, published)
	}
	goruntime.KeepAlive(frame.memory)
	goruntime.KeepAlive(coroPanicNilPayloadV1)
}

func TestCoroPanicNilHookPublishesAwaitedChildCompletion(t *testing.T) {
	g := new(coro.G)
	if !coro.InitG(g) {
		t.Fatal("initialize panic(nil) child G")
	}
	parent := newCoroPanicPayloadTestFrameV1(t, g, nil)
	child := newCoroPanicPayloadTestFrameV1(t, g, parent.handle)
	p := new(coro.P)
	parentAction := beginCoroPanicPayloadRootV1(t, p, g, parent)
	parent.header.SuspendReason = uint16(coro.SuspendCall)
	parent.header.Lifecycle = uint16(coro.FrameSuspended)
	if !coro.PrepareAwaitCompletion(g, parent.handle, child.handle) {
		t.Fatal("prepare panic(nil) child await")
	}
	action, ok := coro.Resumed(p, g, parentAction)
	if !ok || action.Kind != coro.ActionCheckResume || action.Handle != child.handle {
		t.Fatalf("dispatch panic(nil) child = (%+v, %t)", action, ok)
	}
	action = activateCoroPanicPayloadActionV1(t, p, g, action, child.handle)
	child.header.SuspendReason = uint16(coro.SuspendPanic)
	child.header.Lifecycle = uint16(coro.FrameFinalSuspended)
	__llgo_coro_panic_prepare_v1(unsafe.Pointer(g), child.handle, unsafe.Pointer(child.header), nil, nil)
	if record, published := coro.LoadPanicRecord(g); published || record != (coro.PanicRecordSnapshot{}) {
		t.Fatalf("awaited panic(nil) escaped to root = (%+v, %t)", record, published)
	}

	action, ok = coro.Resumed(p, g, action)
	if !ok || action.Kind != coro.ActionCheckDestroy || action.Handle != child.handle {
		t.Fatalf("dispatch panic(nil) child destroy = (%+v, %t)", action, ok)
	}
	action, ok = coro.Checked(p, g, action, true)
	if !ok || action.Kind != coro.ActionDestroy || action.Handle != child.handle {
		t.Fatalf("activate panic(nil) child destroy = (%+v, %t)", action, ok)
	}
	releaseCoroPanicPayloadTestFrameV1(t, g, child)
	action, ok = coro.Destroyed(p, g, action)
	if !ok || action.Kind != coro.ActionCheckResume || action.Handle != parent.handle {
		t.Fatalf("commit panic(nil) child destroy = (%+v, %t)", action, ok)
	}
	action = activateCoroPanicPayloadActionV1(t, p, g, action, parent.handle)
	parent.header.SuspendReason = uint16(coro.SuspendNone)
	parent.header.Lifecycle = uint16(coro.FrameActive)
	snapshot, consumed := coro.ConsumeAwaitCompletion(g, parent.handle)
	wantType, wantData := coroPanicNilPayloadWordsV1(t)
	if !consumed || snapshot.Status != coro.CompletionPanic ||
		snapshot.TypeWord != wantType || snapshot.DataWord != wantData {
		t.Fatalf("consume child panic(nil) = (%+v, %t), want (%p, %p)",
			snapshot, consumed, wantType, wantData)
	}
	if record, published := coro.LoadPanicRecord(g); published || record != (coro.PanicRecordSnapshot{}) {
		t.Fatalf("consumed child panic(nil) poisoned root = (%+v, %t)", record, published)
	}
	goruntime.KeepAlive(parent.memory)
	goruntime.KeepAlive(child.memory)
	goruntime.KeepAlive(coroPanicNilPayloadV1)
}
