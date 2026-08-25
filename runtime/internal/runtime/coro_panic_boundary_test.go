//go:build coro_panic_boundary_adapter_test

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
	"testing"
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
)

type coroPanicBoundaryTestFrame struct {
	handle  unsafe.Pointer
	header  *coro.HeaderV1
	storage unsafe.Pointer
	memory  []uintptr
}

func newCoroPanicBoundaryTestFrame(t *testing.T) (*coro.G, *coroPanicBoundaryTestFrame) {
	t.Helper()
	task := new(coro.G)
	if !coro.InitG(task) {
		t.Fatal("initialize panic-boundary task")
	}
	const (
		size  = uintptr(37)
		align = uintptr(16)
	)
	total, ok := coro.FrameAllocationSize(size, align)
	if !ok {
		t.Fatal("compute panic-boundary frame allocation")
	}
	wordSize := unsafe.Sizeof(uintptr(0))
	memory := make([]uintptr, (total+wordSize-1)/wordSize)
	raw := unsafe.Pointer(&memory[0])
	descriptor := unsafe.Pointer(&coro.FrameDescriptorV1{Version: 1, ResultAlign: 1})
	storage, ok := coro.RegisterFrame(task, raw, total, size, align, descriptor)
	if !ok {
		t.Fatal("register panic-boundary frame")
	}
	handle := unsafe.Pointer(new(byte))
	header := &coro.HeaderV1{
		G:          unsafe.Pointer(task),
		Descriptor: descriptor,
		Lifecycle:  uint16(coro.FrameInitialSuspended),
	}
	if !coro.PublishFrame(task, handle, header, storage) || !coro.AdoptRoot(task, handle) {
		t.Fatal("publish panic-boundary root")
	}
	p := new(coro.P)
	if !coro.Enqueue(p, task) {
		t.Fatal("enqueue panic-boundary task")
	}
	if next, ok := coro.NextRunnable(p); !ok || next != task {
		t.Fatalf("dequeue panic-boundary task = (%p, %t)", next, ok)
	}
	action, ok := coro.BeginRunG(p, task)
	if !ok || action.Kind != coro.ActionCheckResume || action.Handle != handle {
		t.Fatalf("begin panic-boundary task = (%+v, %t)", action, ok)
	}
	action, ok = coro.Checked(p, task, action, false)
	if !ok || action.Kind != coro.ActionResume || action.Handle != handle {
		t.Fatalf("activate panic-boundary task = (%+v, %t)", action, ok)
	}
	if outcome, caseID, taskKind, source, generation, ok := coro.TakeRunDecisionWordsCompiler(task, 0, 0); !ok || outcome != 0 || caseID != 0 || taskKind != 0 || source != 0 || generation != 0 {
		t.Fatalf("take panic-boundary run gate = (%d, %d, %d, %d, %d, %t)",
			outcome, caseID, taskKind, source, generation, ok)
	}
	return task, &coroPanicBoundaryTestFrame{
		handle: handle, header: header, storage: storage, memory: memory,
	}
}

func resetCoroPanicBoundaryTestG() {
	coroPanicBoundaryTestG = g{}
	coroPanicBoundaryTestSignalAdmitted = true
	coroPanicBoundaryTestSignalDepth = 0
}

func TestCoroPanicBoundaryNormalAndNestedPop(t *testing.T) {
	resetCoroPanicBoundaryTestG()
	task, frame := newCoroPanicBoundaryTestFrame(t)
	base := new(Defer)
	getg().defer_ = base
	outer, inner := new(Defer), new(Defer)
	envOuter, envInner := new(byte), new(byte)
	if !coroPanicBoundaryPush(task, frame.handle, outer, unsafe.Pointer(envOuter)) ||
		!coroPanicBoundaryPush(task, frame.handle, inner, unsafe.Pointer(envInner)) {
		t.Fatal("push nested panic boundaries")
	}
	if inner.Link != outer || outer.Link != base || getg().defer_ != inner {
		t.Fatal("nested panic-boundary chain is malformed")
	}
	if coroPanicBoundaryTestSignalDepth != 2 {
		t.Fatalf("nested signal-boundary depth = %d, want 2", coroPanicBoundaryTestSignalDepth)
	}
	if got := coroPanicBoundaryPopStatus(task, frame.handle, outer); got != coroPanicBoundaryPopInvalidRecord {
		t.Fatalf("out-of-order pop status = %d, want invalid record", got)
	}
	if got := coroPanicBoundaryPopStatus(task, frame.handle, inner); got != coroPanicBoundaryPopOK {
		t.Fatalf("inner pop status = %d, want ok", got)
	}
	if *inner != (Defer{}) || getg().defer_ != outer {
		t.Fatal("inner pop did not clear and reveal outer boundary")
	}
	if coroPanicBoundaryTestSignalDepth != 1 {
		t.Fatalf("signal-boundary depth after inner pop = %d, want 1", coroPanicBoundaryTestSignalDepth)
	}
	if got := coroPanicBoundaryPopStatus(task, frame.handle, outer); got != coroPanicBoundaryPopOK {
		t.Fatalf("outer pop status = %d, want ok", got)
	}
	if *outer != (Defer{}) || getg().defer_ != base {
		t.Fatal("outer pop did not restore base defer chain")
	}
	if coroPanicBoundaryTestSignalDepth != 0 {
		t.Fatalf("signal-boundary depth after outer pop = %d, want 0", coroPanicBoundaryTestSignalDepth)
	}
	if got := coroPanicBoundaryPopStatus(task, frame.handle, outer); got != coroPanicBoundaryPopInvalidRecord {
		t.Fatalf("duplicate pop status = %d, want invalid record", got)
	}
}

func TestCoroPanicBoundaryStagesExactHandleAndTakesOnce(t *testing.T) {
	resetCoroPanicBoundaryTestG()
	task, frame := newCoroPanicBoundaryTestFrame(t)
	base, boundary := new(Defer), new(Defer)
	getg().defer_ = base
	if !coroPanicBoundaryPush(task, frame.handle, boundary, unsafe.Pointer(new(byte))) {
		t.Fatal("push panic boundary")
	}
	want := any(&struct{ value uint32 }{value: 73})
	node := &panicNode{arg: want, defer_: boundary}
	getg().panic_ = unsafe.Pointer(node)
	wrong := unsafe.Pointer(new(byte))
	if coroPanicBoundaryStage(task, wrong, boundary) {
		t.Fatal("mismatched handle staged a panic")
	}
	if getg().panic_ != unsafe.Pointer(node) || getg().defer_ != boundary || node.defer_ != boundary {
		t.Fatal("rejected stage mutated panic ownership")
	}
	if got := coroPanicBoundaryPopStatus(task, frame.handle, boundary); got != coroPanicBoundaryPopActivePanic {
		t.Fatalf("pop with active panic status = %d, want active panic", got)
	}
	coroPanicBoundaryTestSignalAdmitted = false
	if coroPanicBoundaryStage(task, frame.handle, boundary) {
		t.Fatal("fault rejected at signal time was staged")
	}
	if getg().panic_ != unsafe.Pointer(node) || getg().defer_ != boundary || node.defer_ != boundary {
		t.Fatal("policy-rejected stage mutated panic ownership")
	}
	coroPanicBoundaryTestSignalAdmitted = true
	if !coroPanicBoundaryStage(task, frame.handle, boundary) {
		t.Fatal("stage exact panic boundary")
	}
	if getg().defer_ != base || node.defer_ != (*Defer)(frame.handle) || *boundary != (Defer{}) {
		t.Fatal("stage did not transfer panic ownership to exact handle")
	}
	if coroPanicBoundaryTestSignalDepth != 0 {
		t.Fatalf("signal-boundary depth after stage = %d, want 0", coroPanicBoundaryTestSignalDepth)
	}
	if token := __llgo_coro_panic_boundary_take_v1(unsafe.Pointer(task), wrong); token != nil {
		t.Fatal("mismatched handle took staged panic")
	}
	token := __llgo_coro_panic_boundary_take_v1(unsafe.Pointer(task), frame.handle)
	if token != unsafe.Pointer(node) || getg().panic_ != nil {
		t.Fatal("exact handle did not detach staged panic")
	}
	if duplicate := __llgo_coro_panic_boundary_take_v1(unsafe.Pointer(task), frame.handle); duplicate != nil {
		t.Fatal("duplicate take returned a token")
	}
	if detached := coroDetachedBoundaryPanic(token); detached != node {
		t.Fatal("detached token failed its ownership check")
	}
	wantType := efaceOf(&want)._type
	if gotType := (*_type)(__llgo_coro_panic_boundary_type_v1(token)); gotType != wantType {
		t.Fatalf("detached type = %p, want %p", gotType, wantType)
	}
	node.prev = wrong
	if coroDetachedBoundaryPanic(token) != nil {
		t.Fatal("mutated detached token retained validity")
	}
}
