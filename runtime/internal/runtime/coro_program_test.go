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

type coroProgramTestManifestV2 struct {
	factoryMarker byte
	plainTargets  [5]byte
	steps         [5]coro.ProgramStepV2
	bootstrap     coro.ProgramBootstrapV2
	manifest      coro.ProgramManifestV1
}

func newCoroProgramTestManifestV2() *coroProgramTestManifestV2 {
	fixture := new(coroProgramTestManifestV2)
	fixture.factoryMarker = 0x42
	fixture.plainTargets = [5]byte{0x31, 0x32, 0x33, 0x34, 0x35}
	roles := [...]uint32{
		coro.ProgramStepFlagInternalRuntimeInitV2,
		coro.ProgramStepFlagCompilerABIInitV2,
		coro.ProgramStepFlagPublicRuntimeInitV2,
		coro.ProgramStepFlagMainPackageInitV2,
		coro.ProgramStepFlagMainV2,
	}
	for index, role := range roles {
		fixture.steps[index] = coro.ProgramStepV2{
			Kind:   uint32(coro.ProgramStepDirectPlainV2),
			Flags:  role,
			Target: unsafe.Pointer(&fixture.plainTargets[index]),
		}
	}
	fixture.bootstrap = coro.ProgramBootstrapV2{
		Version:   coro.ProgramBootstrapVersionV2,
		HashLo:    0x2122232425262728,
		HashHi:    0x3132333435363738,
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
	t                        *testing.T
	frame                    *coroProgramTestFrameV1
	doneCalls                int
	resumeCalls              int
	destroyCalls             int
	completeReady            bool
	released                 bool
	requestScheduleOnDestroy bool
}

var activeCoroProgramDriver *coroProgramTestDriverV1

// The named-source host test deliberately does not link BDWGC or libc. Set the
// allocator's private readiness byte to the bootstrapReady value so this test
// can exercise the program adapter independently; coroalloc's own tests and
// compiler IR tests cover the real bootstrap boundary.
//
//go:linkname testCoroAllocatorBootstrapState github.com/goplus/llgo/runtime/internal/coroalloc.state
var testCoroAllocatorBootstrapState uint8

// coro_program.go aborts through the full LLGo runtime. The named-source host
// test intentionally excludes that unrelated runtime implementation (which
// defines symbols reserved by the host Go runtime), so failures use this local
// non-returning stand-in. Valid test paths never call it.
func coroRuntimeAbort(message string) {
	panic(message)
}

// The named-source adapter test exercises only the static bootstrap G and does
// not link the target allocator backend. Keep the ActionComplete ownership
// check real while avoiding a reference to the production physical free hook.
// Spawn/task-storage tests live in runtime/internal/coro.
func coroReleaseCompletedTask(g *coroG) bool {
	owned, ok := coro.TaskStorageOwned(g)
	return ok && !owned
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
	if driver.requestScheduleOnDestroy && !coro.RequestSchedule(&coroProgramPV1State) {
		driver.t.Fatal("request terminal schedule retry")
	}
}

func resetCoroProgramTestStateV1(t *testing.T) {
	t.Helper()
	testCoroAllocatorBootstrapState = 2
	coroProgramLifecycleV1State = coroProgramUnusedV1
	coroProgramManifestV1State = nil
	coroProgramFactoryV1State = nil
	coroProgramGV1State = coroG{}
	coroProgramPV1State = coroP{}
	activeCoroProgramDriver = nil
	t.Cleanup(func() {
		testCoroAllocatorBootstrapState = 0
		coroProgramLifecycleV1State = coroProgramUnusedV1
		coroProgramManifestV1State = nil
		coroProgramFactoryV1State = nil
		coroProgramGV1State = coroG{}
		coroProgramPV1State = coroP{}
		activeCoroProgramDriver = nil
	})
}

func TestCoroProgramV1BeginRunAndDestroy(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)

	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok || gPointer != unsafe.Pointer(&coroProgramGV1State) || !coro.ValidG(&coroProgramGV1State) {
		t.Fatalf("begin coroutine program = (%p, %t), want initialized static G %p", gPointer, ok, &coroProgramGV1State)
	}
	if coroProgramLifecycleV1State != coroProgramBegunV1 || coroProgramManifestV1State != &manifest.manifest || coroProgramFactoryV1State != factory {
		t.Fatalf("begun coroutine program state = {lifecycle:%d manifest:%p factory:%p}", coroProgramLifecycleV1State, coroProgramManifestV1State, coroProgramFactoryV1State)
	}

	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{t: t, frame: frame}
	activeCoroProgramDriver = driver
	if !coroProgramRunV1(gPointer, frame.handle) {
		t.Fatal("run valid coroutine program")
	}
	if coroProgramLifecycleV1State != coroProgramCompleteV1 || !coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
		t.Fatalf("completed coroutine program retained scheduler state: lifecycle=%d", coroProgramLifecycleV1State)
	}
	if driver.doneCalls != 2 || driver.resumeCalls != 1 || driver.destroyCalls != 1 || !driver.released {
		t.Fatalf("coroutine wrapper calls = done:%d resume:%d destroy:%d released:%t", driver.doneCalls, driver.resumeCalls, driver.destroyCalls, driver.released)
	}

	if _, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory); ok || coroProgramLifecycleV1State != coroProgramFailedV1 {
		t.Fatalf("completed coroutine program was reusable: ok=%t lifecycle=%d", ok, coroProgramLifecycleV1State)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramV2BeginRunAndDestroy(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV2()
	factory := unsafe.Pointer(&manifest.factoryMarker)

	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok || gPointer != unsafe.Pointer(&coroProgramGV1State) || !coro.ValidG(&coroProgramGV1State) {
		t.Fatalf("begin coroutine program v2 = (%p, %t), want initialized static G %p", gPointer, ok, &coroProgramGV1State)
	}

	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{t: t, frame: frame}
	activeCoroProgramDriver = driver
	if !coroProgramRunV1(gPointer, frame.handle) {
		t.Fatal("run valid coroutine program v2")
	}
	if coroProgramLifecycleV1State != coroProgramCompleteV1 || !coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
		t.Fatalf("completed coroutine program v2 retained scheduler state: lifecycle=%d", coroProgramLifecycleV1State)
	}
	if driver.doneCalls != 2 || driver.resumeCalls != 1 || driver.destroyCalls != 1 || !driver.released {
		t.Fatalf("coroutine v2 wrapper calls = done:%d resume:%d destroy:%d released:%t", driver.doneCalls, driver.resumeCalls, driver.destroyCalls, driver.released)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramTerminalScheduleRetryDoesNotRedestroy(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)

	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin terminal-retry coroutine program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{
		t:                        t,
		frame:                    frame,
		requestScheduleOnDestroy: true,
	}
	activeCoroProgramDriver = driver
	if !coroProgramRunV1(gPointer, frame.handle) {
		t.Fatal("terminal schedule request was treated as corruption")
	}
	if driver.destroyCalls != 1 || !driver.released ||
		coroProgramLifecycleV1State != coroProgramCompleteV1 ||
		!coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
		t.Fatalf("terminal retry = destroys:%d released:%t lifecycle:%d", driver.destroyCalls, driver.released, coroProgramLifecycleV1State)
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
	); ok || g != nil || coroProgramLifecycleV1State != coroProgramFailedV1 || coro.ValidG(&coroProgramGV1State) {
		t.Fatalf("factory mismatch = (%p, %t), lifecycle=%d validG=%t", g, ok, coroProgramLifecycleV1State, coro.ValidG(&coroProgramGV1State))
	}
	runtime.KeepAlive(manifest)
}

func TestCoroProgramV1BeginFailsClosedOnNilManifest(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	if g, ok := coroProgramBeginV1(nil, unsafe.Pointer(new(byte))); ok || g != nil ||
		coroProgramLifecycleV1State != coroProgramFailedV1 || coro.ValidG(&coroProgramGV1State) {
		t.Fatalf("nil manifest = (%p, %t), lifecycle=%d validG=%t", g, ok, coroProgramLifecycleV1State, coro.ValidG(&coroProgramGV1State))
	}
}

func TestCoroProgramV1RunFailsClosedOnInvalidHandle(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	g, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin coroutine program before invalid run")
	}
	if coroProgramRunV1(g, nil) || coroProgramLifecycleV1State != coroProgramFailedV1 {
		t.Fatalf("nil-handle run did not fail closed: lifecycle=%d", coroProgramLifecycleV1State)
	}
	runtime.KeepAlive(manifest)
}
