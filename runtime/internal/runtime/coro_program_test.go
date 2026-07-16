//go:build coro_runtime_adapter_test

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
	"runtime"
	"testing"
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

// The production scheduler calls three compiler-owned C ABI wrappers through
// direct linknames. Test-only definitions of those exact symbols let this
// package exercise the real runtime adapter without adding an injectable
// function pointer or dynamic-dispatch seam to production code. This is a
// runtime-side integration test; compiler tests separately verify the LLVM
// wrappers and their pre-/post-CoroSplit object emission.
//
//go:linkname testCoroHandleDone C.__llgo_coro_done_v1
func testCoroHandleDone(handle unsafe.Pointer) bool {
	return activeCoroProgramDriver.done(handle)
}

//go:linkname testCoroHandleResume C.__llgo_coro_resume_v1
func testCoroHandleResume(handle unsafe.Pointer) {
	activeCoroProgramDriver.resume(handle)
}

//go:linkname testCoroHandleDestroy C.__llgo_coro_destroy_v1
func testCoroHandleDestroy(handle unsafe.Pointer) {
	activeCoroProgramDriver.destroy(handle)
}

type coroProgramTestManifestV1 struct {
	factoryMarker byte
	plainTargets  [2]byte
	steps         [2]coro.ProgramStepV1
	bootstrap     coro.ProgramBootstrapV1
	manifest      coro.ProgramManifestV1
}

func newCoroProgramTestManifestV1() *coroProgramTestManifestV1 {
	fixture := new(coroProgramTestManifestV1)
	fixture.factoryMarker = 0x41
	fixture.plainTargets = [2]byte{0x31, 0x32}
	fixture.steps = [2]coro.ProgramStepV1{
		{
			Kind:   uint32(coro.ProgramStepDirectPlainV1),
			Flags:  coro.ProgramStepFlagInitV1,
			Target: unsafe.Pointer(&fixture.plainTargets[0]),
		},
		{
			Kind:   uint32(coro.ProgramStepDirectPlainV1),
			Flags:  coro.ProgramStepFlagMainV1,
			Target: unsafe.Pointer(&fixture.plainTargets[1]),
		},
	}
	fixture.bootstrap = coro.ProgramBootstrapV1{
		Version:   coro.ProgramBootstrapVersionV1,
		HashLo:    0x0102030405060708,
		HashHi:    0x1112131415161718,
		StepCount: uintptr(len(fixture.steps)),
		Steps:     unsafe.Pointer(&fixture.steps[0]),
		Factory:   unsafe.Pointer(&fixture.factoryMarker),
	}
	fixture.manifest = coro.ProgramManifestV1{
		Version:   coro.ProgramManifestVersionV1,
		HashLo:    fixture.bootstrap.HashLo,
		HashHi:    fixture.bootstrap.HashHi,
		Bootstrap: unsafe.Pointer(&fixture.bootstrap),
	}
	return fixture
}

type coroProgramTestFrameV1 struct {
	g          *coro.G
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

func newCoroProgramTestFrameV1(t *testing.T, g *coro.G) *coroProgramTestFrameV1 {
	t.Helper()
	const (
		size  = uintptr(37)
		align = uintptr(16)
	)
	total, ok := coro.FrameAllocationSize(size, align)
	if !ok {
		t.Fatal("compute coroutine program test frame allocation")
	}
	wordSize := unsafe.Sizeof(uintptr(0))
	memory := make([]uintptr, (total+wordSize-1)/wordSize)
	raw := unsafe.Pointer(&memory[0])
	descriptor := unsafe.Pointer(new(byte))
	storage, ok := coro.RegisterFrame(g, raw, total, size, align, descriptor)
	if !ok {
		t.Fatal("register coroutine program test frame")
	}
	handle := unsafe.Pointer(new(byte))
	header := &coro.HeaderV1{
		G:             unsafe.Pointer(g),
		Descriptor:    descriptor,
		SuspendReason: uint16(coro.SuspendNone),
		Lifecycle:     uint16(coro.FrameInitialSuspended),
	}
	if !coro.PublishFrame(g, handle, header, storage) {
		t.Fatal("publish coroutine program test frame")
	}
	return &coroProgramTestFrameV1{
		g:          g,
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

type coroProgramTestDriverV1 struct {
	t             *testing.T
	frame         *coroProgramTestFrameV1
	doneCalls     int
	resumeCalls   int
	destroyCalls  int
	completeReady bool
	released      bool
}

var activeCoroProgramDriver *coroProgramTestDriverV1

// coro_program.go aborts through the full LLGo runtime. The named-source host
// test intentionally excludes that unrelated runtime implementation (which
// defines symbols reserved by the host Go runtime), so failures use this local
// non-returning stand-in. Valid test paths never call it.
func coroRuntimeAbort(message string) {
	panic(message)
}

func (driver *coroProgramTestDriverV1) requireHandle(handle unsafe.Pointer) {
	if driver == nil {
		panic("coroutine test wrapper called without an active driver")
	}
	driver.t.Helper()
	if driver.frame == nil {
		driver.t.Fatal("coroutine test wrapper called without an active frame")
	}
	if handle != driver.frame.handle {
		driver.t.Fatalf("coroutine wrapper handle = %p, want %p", handle, driver.frame.handle)
	}
}

func (driver *coroProgramTestDriverV1) done(handle unsafe.Pointer) bool {
	driver.requireHandle(handle)
	driver.doneCalls++
	return driver.completeReady
}

func (driver *coroProgramTestDriverV1) resume(handle unsafe.Pointer) {
	driver.requireHandle(handle)
	driver.resumeCalls++
	if driver.resumeCalls != 1 {
		driver.t.Fatalf("coroutine resume calls = %d, want 1", driver.resumeCalls)
	}
	frame := driver.frame
	frame.header.SuspendReason = uint16(coro.SuspendFrameComplete)
	frame.header.Lifecycle = uint16(coro.FrameFinalSuspended)
	if !coro.PrepareComplete(frame.g, handle, frame.header) {
		driver.t.Fatal("prepare simulated final coroutine suspend")
	}
	driver.completeReady = true
}

func (driver *coroProgramTestDriverV1) destroy(handle unsafe.Pointer) {
	driver.requireHandle(handle)
	driver.destroyCalls++
	if driver.destroyCalls != 1 {
		driver.t.Fatalf("coroutine destroy calls = %d, want 1", driver.destroyCalls)
	}
	frame := driver.frame
	raw, total, ok := coro.ReleaseFrame(
		frame.g, frame.storage, frame.size, frame.align, frame.descriptor,
	)
	if !ok || raw != frame.raw || total != frame.total {
		driver.t.Fatalf("release simulated coroutine frame = (%p, %d, %t), want (%p, %d, true)", raw, total, ok, frame.raw, frame.total)
	}
	driver.released = true
}

func resetCoroProgramTestStateV1(t *testing.T) {
	t.Helper()
	coroProgramV1 = coroProgramStateV1{}
	activeCoroProgramDriver = nil
	t.Cleanup(func() {
		coroProgramV1 = coroProgramStateV1{}
		activeCoroProgramDriver = nil
	})
}

func TestCoroProgramV1BeginRunAndDestroy(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)

	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok || gPointer != unsafe.Pointer(&coroProgramV1.g) || !coro.ValidG(&coroProgramV1.g) {
		t.Fatalf("begin coroutine program = (%p, %t), want initialized static G %p", gPointer, ok, &coroProgramV1.g)
	}
	if coroProgramV1.lifecycle != coroProgramBegunV1 || coroProgramV1.manifest != &manifest.manifest || coroProgramV1.factory != factory {
		t.Fatalf("begun coroutine program state = {lifecycle:%d manifest:%p factory:%p}", coroProgramV1.lifecycle, coroProgramV1.manifest, coroProgramV1.factory)
	}

	frame := newCoroProgramTestFrameV1(t, &coroProgramV1.g)
	driver := &coroProgramTestDriverV1{t: t, frame: frame}
	activeCoroProgramDriver = driver
	if !coroProgramRunV1(gPointer, frame.handle) {
		t.Fatal("run valid coroutine program")
	}
	if coroProgramV1.lifecycle != coroProgramCompleteV1 || !coro.TerminalG(&coroProgramV1.p, &coroProgramV1.g) {
		t.Fatalf("completed coroutine program retained scheduler state: lifecycle=%d", coroProgramV1.lifecycle)
	}
	if driver.doneCalls != 2 || driver.resumeCalls != 1 || driver.destroyCalls != 1 || !driver.released {
		t.Fatalf("coroutine wrapper calls = done:%d resume:%d destroy:%d released:%t", driver.doneCalls, driver.resumeCalls, driver.destroyCalls, driver.released)
	}

	if _, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory); ok || coroProgramV1.lifecycle != coroProgramFailedV1 {
		t.Fatalf("completed coroutine program was reusable: ok=%t lifecycle=%d", ok, coroProgramV1.lifecycle)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramV1BeginFailsClosedOnFactoryIdentity(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	otherFactory := new(byte)
	if g, ok := coroProgramBeginV1(
		unsafe.Pointer(&manifest.manifest), unsafe.Pointer(otherFactory),
	); ok || g != nil || coroProgramV1.lifecycle != coroProgramFailedV1 || coro.ValidG(&coroProgramV1.g) {
		t.Fatalf("factory mismatch = (%p, %t), lifecycle=%d validG=%t", g, ok, coroProgramV1.lifecycle, coro.ValidG(&coroProgramV1.g))
	}
	runtime.KeepAlive(manifest)
}

func TestCoroProgramV1RunFailsClosedOnInvalidHandle(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	g, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin coroutine program before invalid run")
	}
	if coroProgramRunV1(g, nil) || coroProgramV1.lifecycle != coroProgramFailedV1 {
		t.Fatalf("nil-handle run did not fail closed: lifecycle=%d", coroProgramV1.lifecycle)
	}
	runtime.KeepAlive(manifest)
}
