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
	descriptor := unsafe.Pointer(&coro.FrameDescriptorV1{Version: 1, ResultAlign: 1})
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
	panicOnResume            bool
	panicTypeWord            unsafe.Pointer
	panicDataWord            unsafe.Pointer
	spawnOnMainReturn        bool
	spawnBeforeMainReturn    bool
	child                    *coro.G
	childFrame               *coroProgramTestFrameV1
	childCompleteReady       bool
	childDoneCalls           int
	childResumeCalls         int
	cancelDestroyCalls       int
	taskReleaseCalls         int
	parkOnFirstResume        bool
	parkResumeCount          int
	waitToken                coro.WaitToken
	waitTicket               coro.WaitTicket
	waitRegistration         coro.WaitRegistrationHandle
	waitRetired              bool
	waitRetireCalls          int
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
	if !ok {
		return false
	}
	if !owned {
		return true
	}
	raw, size, ok := coro.ReleaseTaskStorage(g)
	if !ok || raw != unsafe.Pointer(g) || size != coro.TaskStorageSize() ||
		activeCoroProgramDriver == nil || activeCoroProgramDriver.child != g {
		return false
	}
	activeCoroProgramDriver.taskReleaseCalls++
	return activeCoroProgramDriver.taskReleaseCalls == 1
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
	if driver != nil && driver.childFrame != nil && handle == driver.childFrame.handle {
		driver.childDoneCalls++
		return driver.childCompleteReady
	}
	driver.requireHandle(handle)
	driver.doneCalls++
	return driver.completeReady
}

func (driver *coroProgramTestDriverV1) resume(handle unsafe.Pointer) {
	if driver != nil && driver.childFrame != nil && handle == driver.childFrame.handle {
		driver.childResumeCalls++
		if driver.childResumeCalls != 1 {
			driver.t.Fatalf("child coroutine resume calls = %d, want 1", driver.childResumeCalls)
		}
		frame := driver.childFrame
		outcome, caseID, taskKind, sourceSlot, generation, decisionOK := coro.TakeRunDecisionWords(frame.g, 0, 0)
		if !decisionOK || outcome != 0 || caseID != 0 || taskKind != 0 || sourceSlot != 0 || generation != 0 {
			driver.t.Fatalf("take child coroutine run decision = (%d, %d, %d, %d, %d, %t)",
				outcome, caseID, taskKind, sourceSlot, generation, decisionOK)
		}
		frame.header.SuspendReason = uint16(coro.SuspendFrameComplete)
		frame.header.Lifecycle = uint16(coro.FrameFinalSuspended)
		if !coro.PrepareComplete(frame.g, handle, frame.header) {
			driver.t.Fatal("prepare simulated child final suspend")
		}
		driver.childCompleteReady = true
		return
	}
	driver.requireHandle(handle)
	driver.resumeCalls++
	parkCount := driver.parkResumeCount
	if driver.parkOnFirstResume {
		parkCount = 1
	}
	maxResumeCalls := parkCount + 1
	if driver.spawnBeforeMainReturn {
		maxResumeCalls = 2
	}
	if driver.resumeCalls > maxResumeCalls {
		driver.t.Fatalf("coroutine resume calls = %d, max %d", driver.resumeCalls, maxResumeCalls)
	}
	frame := driver.frame
	// The named-source test driver stands in for compiler-generated coroutine
	// code. Model its mandatory resume prologue before invoking any runtime
	// transition hook; every resume shape in this fixture is a normal
	// zero-ticket continuation.
	outcome, caseID, taskKind, sourceSlot, generation, decisionOK := coro.TakeRunDecisionWords(frame.g, 0, 0)
	if !decisionOK || outcome != 0 || caseID != 0 || taskKind != 0 || sourceSlot != 0 || generation != 0 {
		driver.t.Fatalf("take simulated coroutine run decision = (%d, %d, %d, %d, %d, %t)",
			outcome, caseID, taskKind, sourceSlot, generation, decisionOK)
	}
	frame.header.SuspendReason = uint16(coro.SuspendNone)
	frame.header.Lifecycle = uint16(coro.FrameActive)
	if parkCount != 0 && driver.resumeCalls > 1 {
		if outcome, ok := coro.WaitOutcomeOf(&driver.waitToken, driver.waitTicket); !ok || outcome != coro.WaitOutcomeCompleted {
			driver.t.Fatalf("resumed executor wait outcome = (%d, %t), want completed", outcome, ok)
		}
		if result := coroProgramWaitTableV1State.BeginClose(driver.waitRegistration); result != coro.WaitRegistrationCloseStarted {
			driver.t.Fatalf("close delivered executor wait = %d", result)
		}
		if result, ok := coroProgramWaitTableV1State.ConfirmQuiesced(driver.waitRegistration); !ok || result != coro.WaitCancelCompletionWon {
			driver.t.Fatalf("confirm delivered executor wait = (%d, %t)", result, ok)
		}
		if !coroProgramWaitTableV1State.Retire(driver.waitRegistration) {
			driver.t.Fatal("retire delivered executor wait")
		}
		driver.waitRetireCalls++
		driver.waitRetired = true
	}
	if parkCount != 0 && driver.resumeCalls <= parkCount {
		var ok bool
		driver.waitTicket, ok = coro.ArmWait(&driver.waitToken)
		if !ok {
			driver.t.Fatal("arm named-adapter executor wait")
		}
		driver.waitRegistration, ok = coroProgramWaitTableV1State.Register(
			&coroProgramPV1State,
			&driver.waitToken,
			driver.waitTicket,
		)
		if !ok {
			driver.t.Fatal("register named-adapter executor wait")
		}
		frame.header.SuspendReason = uint16(coro.SuspendPark)
		frame.header.Lifecycle = uint16(coro.FrameSuspended)
		if !coro.PreparePark(frame.g, handle, frame.header, &driver.waitToken, driver.waitTicket) {
			driver.t.Fatal("prepare named-adapter executor park")
		}
		driver.waitRetired = false
		return
	}
	if driver.panicOnResume {
		frame.header.SuspendReason = uint16(coro.SuspendPanic)
		frame.header.Lifecycle = uint16(coro.FrameFinalSuspended)
		__llgo_coro_panic_prepare_v1(
			unsafe.Pointer(frame.g),
			handle,
			unsafe.Pointer(frame.header),
			driver.panicTypeWord,
			driver.panicDataWord,
		)
		driver.completeReady = true
		return
	}
	if driver.spawnOnMainReturn || driver.spawnBeforeMainReturn && driver.resumeCalls == 1 {
		driver.child = new(coro.G)
		if !coro.BeginSpawn(frame.g, driver.child, unsafe.Pointer(driver.child), coro.TaskStorageSize()) {
			driver.t.Fatal("begin named-adapter command child")
		}
		driver.childFrame = newCoroProgramTestFrameV1(driver.t, driver.child)
		if !coro.CommitSpawn(frame.g, driver.child, driver.childFrame.handle) {
			driver.t.Fatal("commit named-adapter command child")
		}
	}
	if driver.spawnBeforeMainReturn && driver.resumeCalls == 1 {
		frame.header.SuspendReason = uint16(coro.SuspendYield)
		frame.header.Lifecycle = uint16(coro.FrameSuspended)
		if !coro.PrepareYield(frame.g, handle, frame.header) {
			driver.t.Fatal("prepare named-adapter main yield before return")
		}
		return
	}
	if driver.spawnOnMainReturn || driver.spawnBeforeMainReturn {
		if driver.child == nil || driver.childFrame == nil ||
			driver.spawnBeforeMainReturn && driver.childResumeCalls != 1 {
			driver.t.Fatal("main return did not observe the completed child physical resume")
		}
		if !coroProgramMainReturnV1(unsafe.Pointer(frame.g)) {
			driver.t.Fatal("publish named-adapter normal main return")
		}
		if coroProgramLifecycleV1State != coroProgramMainReturnRequestedV1 ||
			!coro.CommandMainReturnPoint(&coroProgramPV1State, frame.g) ||
			driver.cancelDestroyCalls != 0 || driver.taskReleaseCalls != 0 {
			driver.t.Fatal("main-return hook mutated scheduler ownership inside resume")
		}
	}
	frame.header.SuspendReason = uint16(coro.SuspendFrameComplete)
	frame.header.Lifecycle = uint16(coro.FrameFinalSuspended)
	if !coro.PrepareComplete(frame.g, handle, frame.header) {
		driver.t.Fatal("prepare simulated final coroutine suspend")
	}
	driver.completeReady = true
}

func (driver *coroProgramTestDriverV1) destroy(handle unsafe.Pointer) {
	if driver.childFrame != nil && handle == driver.childFrame.handle {
		if !coroProgramTestTargetV1State.joined {
			driver.t.Fatal("ready child cancellation ran before target strong join")
		}
		driver.cancelDestroyCalls++
		if driver.cancelDestroyCalls != 1 {
			driver.t.Fatalf("child coroutine destroy calls = %d, want 1", driver.cancelDestroyCalls)
		}
		frame := driver.childFrame
		raw, total, ok := coro.ReleaseFrame(frame.g, frame.storage, frame.size, frame.align, frame.descriptor)
		if !ok || raw != frame.raw || total != frame.total {
			driver.t.Fatalf("release canceled child frame = (%p, %d, %t)", raw, total, ok)
		}
		return
	}
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
	if driver.requestScheduleOnDestroy {
		if result := coroProgramExecutorRegistryV1State.Request(coroProgramExecutorHandleV1State); result != coro.ExecutorRequestPublished {
			driver.t.Fatalf("request terminal executor retry = %d", result)
		}
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
	coroProgramContinuationV1State = coroProgramContinuationNoneV1
	coroProgramContinuationEpochV1 = 0
	coroProgramDriveAdmissionV1State = coro.DriveAdmission{}
	coroProgramExecutorRegistryV1State = coro.ExecutorRegistry{}
	coroProgramWaitTableV1State = coro.WaitRegistrationTable{}
	coroProgramExecutorDriverV1State = coro.ExecutorDriver{}
	coroProgramExecutorHandleV1State = coro.ExecutorHandle{}
	coroProgramExecutorBoundV1State = false
	coroProgramTestTargetV1State = coroProgramTestTargetStateV1{}
	activeCoroProgramDriver = nil
	t.Cleanup(func() {
		testCoroAllocatorBootstrapState = 0
		coroProgramLifecycleV1State = coroProgramUnusedV1
		coroProgramManifestV1State = nil
		coroProgramFactoryV1State = nil
		coroProgramGV1State = coroG{}
		coroProgramPV1State = coroP{}
		coroProgramContinuationV1State = coroProgramContinuationNoneV1
		coroProgramContinuationEpochV1 = 0
		coroProgramDriveAdmissionV1State = coro.DriveAdmission{}
		coroProgramExecutorRegistryV1State = coro.ExecutorRegistry{}
		coroProgramWaitTableV1State = coro.WaitRegistrationTable{}
		coroProgramExecutorDriverV1State = coro.ExecutorDriver{}
		coroProgramExecutorHandleV1State = coro.ExecutorHandle{}
		coroProgramExecutorBoundV1State = false
		coroProgramTestTargetV1State = coroProgramTestTargetStateV1{}
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
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveCompleteV1 {
		t.Fatalf("run valid coroutine program = %d", status)
	}
	if coroProgramLifecycleV1State != coroProgramCompleteV1 || !coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
		t.Fatalf("completed coroutine program retained scheduler state: lifecycle=%d", coroProgramLifecycleV1State)
	}
	if driver.doneCalls != 2 || driver.resumeCalls != 1 || driver.destroyCalls != 1 || !driver.released {
		t.Fatalf("coroutine wrapper calls = done:%d resume:%d destroy:%d released:%t", driver.doneCalls, driver.resumeCalls, driver.destroyCalls, driver.released)
	}
	if !coroProgramTestTargetV1State.joined || coroProgramTestTargetV1State.closeCalls != 1 ||
		coroProgramExecutorBoundV1State || coroProgramExecutorDriverV1State != (coro.ExecutorDriver{}) ||
		!coroProgramExecutorRegistryV1State.CanRelease() || !coroProgramWaitTableV1State.CanRelease() {
		t.Fatal("completed coroutine program retained executor target state")
	}

	if _, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory); ok || coroProgramLifecycleV1State != coroProgramFailedV1 {
		t.Fatalf("completed coroutine program was reusable: ok=%t lifecycle=%d", ok, coroProgramLifecycleV1State)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramRunSliceBudgetOneKeepsPhysicalActionsAtomic(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok || gPointer != unsafe.Pointer(&coroProgramGV1State) {
		t.Fatal("begin budget-one coroutine program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{t: t, frame: frame}
	activeCoroProgramDriver = driver
	if !coroProgramDriveAdmissionV1State.Acquire() {
		t.Fatal("acquire budget-one scheduler owner")
	}
	if !coroAdoptRoot(&coroProgramGV1State, frame.handle) ||
		!coroEnqueue(&coroProgramPV1State, &coroProgramGV1State) ||
		!coroTargetExecutorStartV1(coroProgramExecutorHandleV1State) {
		t.Fatal("start budget-one coroutine program")
	}
	coroProgramLifecycleV1State = coroProgramRunningV1

	var sources, dispatches, resumes, destroys uint32
	for entry := 0; entry < 10000; entry++ {
		result := coroRunSlice(
			&coroProgramPV1State,
			&coroProgramGV1State,
			&coroProgramExecutorDriverV1State,
			1,
		)
		if result.used != 1 || result.sources+result.dispatches+result.resumes+result.destroys != 1 {
			t.Fatalf("budget-one entry %d accounting = %+v", entry, result)
		}
		sources += result.sources
		dispatches += result.dispatches
		resumes += result.resumes
		destroys += result.destroys
		if result.stop == coroRunDestroyCommitV1 {
			if result.action.Kind != coro.ActionCommitDestroy || result.action.Handle != nil {
				t.Fatalf("budget-one destroy receipt = %+v", result)
			}
			break
		}
		if result.stop != coroRunSliceBudgetV1 {
			t.Fatalf("budget-one entry %d stop = %+v", entry, result)
		}
	}
	if dispatches != 2 || resumes != 1 || destroys != 1 ||
		driver.doneCalls != 2 || driver.resumeCalls != 1 || driver.destroyCalls != 1 || !driver.released {
		t.Fatalf("budget-one totals = source:%d dispatch:%d resume:%d destroy:%d wrappers={done:%d resume:%d destroy:%d released:%t}",
			sources, dispatches, resumes, destroys, driver.doneCalls, driver.resumeCalls, driver.destroyCalls, driver.released)
	}
	if status := coroProgramFinishDriveAdmissionV1(coroProgramDriveStepV1()); status != coroProgramDriveCompleteV1 ||
		!coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
		t.Fatalf("finish budget-one coroutine program = %d", status)
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
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveCompleteV1 {
		t.Fatalf("run valid coroutine program v2 = %d", status)
	}
	if coroProgramLifecycleV1State != coroProgramCompleteV1 || !coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
		t.Fatalf("completed coroutine program v2 retained scheduler state: lifecycle=%d", coroProgramLifecycleV1State)
	}
	if driver.doneCalls != 2 || driver.resumeCalls != 1 || driver.destroyCalls != 1 || !driver.released {
		t.Fatalf("coroutine v2 wrapper calls = done:%d resume:%d destroy:%d released:%t", driver.doneCalls, driver.resumeCalls, driver.destroyCalls, driver.released)
	}
	if !coroProgramTestTargetV1State.joined || coroProgramTestTargetV1State.closeCalls != 1 ||
		coroProgramExecutorBoundV1State || !coroProgramExecutorRegistryV1State.CanRelease() ||
		!coroProgramWaitTableV1State.CanRelease() {
		t.Fatal("completed coroutine program v2 retained executor target state")
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramAsyncTerminalJoinContinuesFromStaticState(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	coroProgramTestTargetV1State.mode = coroProgramTestTargetAsyncV1
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin asynchronous terminal program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{t: t, frame: frame}
	activeCoroProgramDriver = driver
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveSuspendedV1 {
		t.Fatalf("initial asynchronous terminal drive = %d", status)
	}
	epoch := coroProgramContinuationEpochV1
	if epoch == 0 || coroProgramContinuationV1State != coroProgramContinuationTerminalJoinV1 ||
		coroProgramLifecycleV1State != coroProgramRunningV1 || !coroProgramExecutorBoundV1State ||
		driver.destroyCalls != 1 || !driver.released || coroProgramTestTargetV1State.closeCalls != 1 ||
		coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
		t.Fatalf("asynchronous terminal suspension = epoch:%d continuation:%d lifecycle:%d bound:%t destroy:%d close:%d",
			epoch, coroProgramContinuationV1State, coroProgramLifecycleV1State,
			coroProgramExecutorBoundV1State, driver.destroyCalls, coroProgramTestTargetV1State.closeCalls)
	}
	if status := coroProgramContinueV1(epoch); status != coroProgramDriveSuspendedV1 ||
		coroProgramTestTargetV1State.pollCalls != 1 {
		t.Fatalf("premature asynchronous terminal continuation = %d, polls=%d", status, coroProgramTestTargetV1State.pollCalls)
	}
	coroProgramTestTargetV1State.joined = true
	if status := coroProgramContinueV1(epoch); status != coroProgramDriveCompleteV1 {
		t.Fatalf("joined asynchronous terminal continuation = %d", status)
	}
	if coroProgramLifecycleV1State != coroProgramCompleteV1 || coroProgramExecutorBoundV1State ||
		coroProgramContinuationV1State != coroProgramContinuationNoneV1 ||
		!coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) ||
		coroProgramExecutorDriverV1State != (coro.ExecutorDriver{}) ||
		!coroProgramExecutorRegistryV1State.CanRelease() || !coroProgramWaitTableV1State.CanRelease() {
		t.Fatal("asynchronous terminal continuation retained static ownership")
	}
	if status := coroProgramContinueV1(epoch); status != coroProgramDriveIgnoredV1 ||
		coroProgramLifecycleV1State != coroProgramCompleteV1 || !coroProgramDriveAdmissionV1State.CanRelease() {
		t.Fatalf("duplicate terminal continuation = %d, lifecycle=%d", status, coroProgramLifecycleV1State)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramCompletionBeforeTargetBeginReturnsIsDeferred(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	coroProgramTestTargetV1State.mode = coroProgramTestTargetAsyncV1
	coroProgramTestTargetV1State.completeCloseBeforeBeginReturn = true
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin early-completion terminal program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{t: t, frame: frame}
	activeCoroProgramDriver = driver
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveCompleteV1 {
		t.Fatalf("early target completion drive = %d", status)
	}
	if coroProgramTestTargetV1State.reentrantCloseStatus != coroProgramDriveSuspendedV1 ||
		coroProgramTestTargetV1State.pollCalls != 1 || coroProgramLifecycleV1State != coroProgramCompleteV1 ||
		!coroProgramDriveAdmissionV1State.CanRelease() || coroProgramExecutorBoundV1State {
		t.Fatalf("early target completion = reentrant:%d polls:%d lifecycle:%d admission:%t bound:%t",
			coroProgramTestTargetV1State.reentrantCloseStatus, coroProgramTestTargetV1State.pollCalls,
			coroProgramLifecycleV1State, coroProgramDriveAdmissionV1State.CanRelease(), coroProgramExecutorBoundV1State)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramConcurrentContinuationHasOneSchedulerOwner(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	coroProgramTestTargetV1State.mode = coroProgramTestTargetAsyncV1
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin concurrent-continuation terminal program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{t: t, frame: frame}
	activeCoroProgramDriver = driver
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveSuspendedV1 {
		t.Fatalf("initial concurrent-continuation drive = %d", status)
	}
	epoch := coroProgramContinuationEpochV1
	coroProgramTestTargetV1State.joined = true
	coroProgramTestTargetV1State.closePollEntered = make(chan struct{})
	coroProgramTestTargetV1State.closePollRelease = make(chan struct{})
	first := make(chan coroProgramDriveStatusV1, 1)
	go func() {
		first <- coroProgramContinueV1(epoch)
	}()
	<-coroProgramTestTargetV1State.closePollEntered
	if status := coroProgramContinueV1(epoch); status != coroProgramDriveSuspendedV1 {
		t.Fatalf("concurrent duplicate continuation = %d, want deferred", status)
	}
	close(coroProgramTestTargetV1State.closePollRelease)
	if status := <-first; status != coroProgramDriveCompleteV1 {
		t.Fatalf("owning concurrent continuation = %d", status)
	}
	if coroProgramTestTargetV1State.pollCalls != 1 || coroProgramLifecycleV1State != coroProgramCompleteV1 ||
		!coroProgramDriveAdmissionV1State.CanRelease() || coroProgramExecutorBoundV1State {
		t.Fatalf("concurrent continuation = polls:%d lifecycle:%d admission:%t bound:%t",
			coroProgramTestTargetV1State.pollCalls, coroProgramLifecycleV1State,
			coroProgramDriveAdmissionV1State.CanRelease(), coroProgramExecutorBoundV1State)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramStaleContinuationDoesNotPoisonActiveEpoch(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	coroProgramTestTargetV1State.mode = coroProgramTestTargetAsyncV1
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin stale-continuation terminal program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{t: t, frame: frame}
	activeCoroProgramDriver = driver
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveSuspendedV1 {
		t.Fatalf("initial stale-continuation drive = %d", status)
	}
	epoch := coroProgramContinuationEpochV1
	if status := coroProgramContinueV1(epoch + 1); status != coroProgramDriveIgnoredV1 ||
		coroProgramLifecycleV1State != coroProgramRunningV1 ||
		coroProgramContinuationV1State != coroProgramContinuationTerminalJoinV1 ||
		!coroProgramExecutorBoundV1State {
		t.Fatalf("stale continuation = %d lifecycle:%d continuation:%d bound:%t",
			status, coroProgramLifecycleV1State, coroProgramContinuationV1State, coroProgramExecutorBoundV1State)
	}
	coroProgramTestTargetV1State.joined = true
	if status := coroProgramContinueV1(epoch); status != coroProgramDriveCompleteV1 {
		t.Fatalf("valid continuation after stale callback = %d", status)
	}
	if status := coroProgramContinueV1(epoch); status != coroProgramDriveIgnoredV1 ||
		coroProgramLifecycleV1State != coroProgramCompleteV1 || !coroProgramDriveAdmissionV1State.CanRelease() {
		t.Fatalf("late duplicate continuation = %d lifecycle:%d admission:%t",
			status, coroProgramLifecycleV1State, coroProgramDriveAdmissionV1State.CanRelease())
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramExecutorWakeContinuesParkedRoot(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin executor-wake program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{t: t, frame: frame, parkOnFirstResume: true}
	activeCoroProgramDriver = driver
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveSuspendedV1 {
		t.Fatalf("initial executor-wake drive = %d", status)
	}
	epoch := coroProgramContinuationEpochV1
	if epoch == 0 || coroProgramContinuationV1State != coroProgramContinuationExecutorWakeV1 ||
		driver.resumeCalls != 1 || driver.waitRegistration == (coro.WaitRegistrationHandle{}) ||
		coroProgramTestTargetV1State.waitCalls != 1 || coroProgramTestTargetV1State.waitEpoch != epoch ||
		!coroProgramExecutorBoundV1State {
		t.Fatalf("parked executor wait = epoch:%d continuation:%d resumes:%d wait:%+v targetWaits:%d targetEpoch:%d bound:%t",
			epoch, coroProgramContinuationV1State, driver.resumeCalls, driver.waitRegistration,
			coroProgramTestTargetV1State.waitCalls, coroProgramTestTargetV1State.waitEpoch,
			coroProgramExecutorBoundV1State)
	}
	posted := coro.PostWaitAndRequest(
		&coroProgramWaitTableV1State,
		driver.waitRegistration,
		&coroProgramExecutorRegistryV1State,
		coroProgramExecutorHandleV1State,
	)
	if posted.Wait != coro.WaitRegistrationPosted || posted.Executor != coro.ExecutorRequestIdleWake {
		t.Fatalf("post retained executor wake = (%d, %d), want (posted, idle-wake)", posted.Wait, posted.Executor)
	}
	coroProgramTestTargetV1State.wakeReady = true
	if status := coroProgramContinueV1(epoch); status != coroProgramDriveCompleteV1 {
		t.Fatalf("executor wake continuation = %d", status)
	}
	if coroProgramLifecycleV1State != coroProgramCompleteV1 || driver.resumeCalls != 2 ||
		driver.doneCalls != 3 || driver.destroyCalls != 1 || !driver.waitRetired ||
		coroProgramTestTargetV1State.wakePollCalls != 1 || coroProgramTestTargetV1State.waitEpoch != 0 ||
		coroProgramTestTargetV1State.closeCalls != 1 || !coroProgramTestTargetV1State.joined ||
		coroProgramExecutorBoundV1State || !coroProgramDriveAdmissionV1State.CanRelease() ||
		!coroProgramExecutorRegistryV1State.CanRelease() || !coroProgramWaitTableV1State.CanRelease() {
		t.Fatalf("completed executor wake = lifecycle:%d resumes:%d done:%d destroy:%d retired:%t wakePolls:%d waitEpoch:%d close:%d joined:%t bound:%t admission:%t",
			coroProgramLifecycleV1State, driver.resumeCalls, driver.doneCalls, driver.destroyCalls,
			driver.waitRetired, coroProgramTestTargetV1State.wakePollCalls,
			coroProgramTestTargetV1State.waitEpoch, coroProgramTestTargetV1State.closeCalls,
			coroProgramTestTargetV1State.joined, coroProgramExecutorBoundV1State,
			coroProgramDriveAdmissionV1State.CanRelease())
	}
	if status := coroProgramContinueV1(epoch); status != coroProgramDriveIgnoredV1 ||
		coroProgramLifecycleV1State != coroProgramCompleteV1 {
		t.Fatalf("late executor wake = %d lifecycle:%d", status, coroProgramLifecycleV1State)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(&driver.waitToken)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramSynchronousWaitUsesIterativeDrivePump(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin synchronous-wait program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	const waits = 2048
	driver := &coroProgramTestDriverV1{t: t, frame: frame, parkResumeCount: waits}
	activeCoroProgramDriver = driver
	coroProgramTestTargetV1State.completeWaitBeforeBeginReturn = true
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveCompleteV1 {
		t.Fatalf("synchronous-wait drive = %d", status)
	}
	if driver.resumeCalls != waits+1 || driver.waitRetireCalls != waits || !driver.waitRetired ||
		coroProgramTestTargetV1State.waitCalls != waits || coroProgramTestTargetV1State.waitBeginDepth != 0 ||
		coroProgramTestTargetV1State.maxWaitBeginDepth != 1 ||
		coroProgramLifecycleV1State != coroProgramCompleteV1 || coroProgramExecutorBoundV1State ||
		!coroProgramDriveAdmissionV1State.CanRelease() || !coroProgramExecutorRegistryV1State.CanRelease() ||
		!coroProgramWaitTableV1State.CanRelease() {
		t.Fatalf("iterative synchronous waits = resumes:%d retired:%d finalRetired:%t waits:%d depth:%d maxDepth:%d lifecycle:%d bound:%t admission:%t",
			driver.resumeCalls, driver.waitRetireCalls, driver.waitRetired,
			coroProgramTestTargetV1State.waitCalls, coroProgramTestTargetV1State.waitBeginDepth,
			coroProgramTestTargetV1State.maxWaitBeginDepth, coroProgramLifecycleV1State,
			coroProgramExecutorBoundV1State, coroProgramDriveAdmissionV1State.CanRelease())
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(&driver.waitToken)
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
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveCompleteV1 {
		t.Fatalf("terminal executor request was treated as corruption: %d", status)
	}
	if driver.destroyCalls != 1 || !driver.released ||
		coroProgramLifecycleV1State != coroProgramCompleteV1 ||
		!coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
		t.Fatalf("terminal retry = destroys:%d released:%t lifecycle:%d", driver.destroyCalls, driver.released, coroProgramLifecycleV1State)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func requireCoroProgramRuntimeAbort(t *testing.T, want string, call func()) {
	t.Helper()
	defer func() {
		recovered := recover()
		if recovered != want {
			t.Fatalf("coroutine runtime abort = %#v, want %q", recovered, want)
		}
	}()
	call()
	t.Fatal("coroutine runtime ABI violation returned after abort")
}

func TestCoroProgramExplicitPanicHookAndTerminalDispatcher(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)

	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin explicit-panic coroutine program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	typeWord, dataWord := new(byte), new(byte)
	driver := &coroProgramTestDriverV1{
		t:             t,
		frame:         frame,
		panicOnResume: true,
		panicTypeWord: unsafe.Pointer(typeWord),
		panicDataWord: unsafe.Pointer(dataWord),
	}
	activeCoroProgramDriver = driver
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDrivePanicV1 {
		t.Fatalf("ActionPanicComplete status = %d, want panic", status)
	}
	record, published := coro.LoadPanicRecord(&coroProgramGV1State)
	if !published || record.Status != coro.ExplicitStatusPanic ||
		record.TypeWord != unsafe.Pointer(typeWord) || record.DataWord != unsafe.Pointer(dataWord) {
		t.Fatalf("terminal adapter panic record = (%+v, %t)", record, published)
	}
	if coroProgramLifecycleV1State != coroProgramFailedV1 ||
		driver.doneCalls != 2 || driver.resumeCalls != 1 || driver.destroyCalls != 1 || !driver.released ||
		coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) || coro.ReclaimableG(&coroProgramGV1State) ||
		!coroProgramTestTargetV1State.joined || coroProgramTestTargetV1State.closeCalls != 1 ||
		coroProgramExecutorBoundV1State || !coroProgramExecutorRegistryV1State.CanRelease() ||
		!coroProgramWaitTableV1State.CanRelease() {
		t.Fatalf("explicit panic adapter = lifecycle:%d done:%d resume:%d destroy:%d released:%t",
			coroProgramLifecycleV1State, driver.doneCalls, driver.resumeCalls, driver.destroyCalls, driver.released)
	}

	// Publication is once-only at the exported boundary as well: a duplicate
	// compiler hook is a non-returning ABI violation, never a normal result.
	requireCoroProgramRuntimeAbort(t, "invalid coroutine panic handoff", func() {
		__llgo_coro_panic_prepare_v1(
			gPointer,
			frame.handle,
			unsafe.Pointer(frame.header),
			unsafe.Pointer(typeWord),
			unsafe.Pointer(dataWord),
		)
	})
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramAsyncTerminalPanicContinuesAfterJoin(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	coroProgramTestTargetV1State.mode = coroProgramTestTargetAsyncV1
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin asynchronous panic program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	typeWord, dataWord := new(byte), new(byte)
	driver := &coroProgramTestDriverV1{
		t:             t,
		frame:         frame,
		panicOnResume: true,
		panicTypeWord: unsafe.Pointer(typeWord),
		panicDataWord: unsafe.Pointer(dataWord),
	}
	activeCoroProgramDriver = driver
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveSuspendedV1 {
		t.Fatalf("initial asynchronous panic drive = %d", status)
	}
	epoch := coroProgramContinuationEpochV1
	if epoch == 0 || coroProgramContinuationV1State != coroProgramContinuationTerminalJoinV1 ||
		driver.destroyCalls != 1 || !driver.released || !coroProgramExecutorBoundV1State {
		t.Fatalf("asynchronous panic suspension = epoch:%d continuation:%d destroy:%d bound:%t",
			epoch, coroProgramContinuationV1State, driver.destroyCalls, coroProgramExecutorBoundV1State)
	}
	coroProgramTestTargetV1State.joined = true
	if status := coroProgramContinueV1(epoch); status != coroProgramDrivePanicV1 {
		t.Fatalf("joined asynchronous panic continuation = %d", status)
	}
	record, published := coro.LoadPanicRecord(&coroProgramGV1State)
	if !published || record.TypeWord != unsafe.Pointer(typeWord) || record.DataWord != unsafe.Pointer(dataWord) ||
		coroProgramLifecycleV1State != coroProgramFailedV1 || coroProgramExecutorBoundV1State ||
		coroProgramContinuationV1State != coroProgramContinuationNoneV1 ||
		coroProgramExecutorDriverV1State != (coro.ExecutorDriver{}) ||
		!coroProgramExecutorRegistryV1State.CanRelease() || !coroProgramWaitTableV1State.CanRelease() {
		t.Fatalf("asynchronous panic completion = record:(%+v,%t) lifecycle:%d bound:%t",
			record, published, coroProgramLifecycleV1State, coroProgramExecutorBoundV1State)
	}
	runtime.KeepAlive(typeWord)
	runtime.KeepAlive(dataWord)
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramExplicitPanicHookRejectsInvalidPhysicalG(t *testing.T) {
	requireCoroProgramRuntimeAbort(t, "invalid coroutine panic handoff", func() {
		__llgo_coro_panic_prepare_v1(nil, nil, nil, nil, nil)
	})
}

func TestCoroProgramNormalMainReturnCancelsReadyChild(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin command-shutdown program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{t: t, frame: frame, spawnOnMainReturn: true}
	activeCoroProgramDriver = driver
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveCompleteV1 {
		t.Fatalf("run command-shutdown program = %d", status)
	}
	if coroProgramLifecycleV1State != coroProgramCompleteV1 || driver.doneCalls != 2 ||
		driver.resumeCalls != 1 || driver.destroyCalls != 1 || driver.cancelDestroyCalls != 1 ||
		driver.taskReleaseCalls != 1 || driver.child == nil || driver.childFrame == nil ||
		!coroProgramTestTargetV1State.joined || coroProgramTestTargetV1State.closeCalls != 1 ||
		coroProgramExecutorBoundV1State || !coroProgramExecutorRegistryV1State.CanRelease() ||
		!coroProgramWaitTableV1State.CanRelease() ||
		!coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) ||
		!coro.TerminalG(&coroProgramPV1State, driver.child) {
		t.Fatalf("command shutdown = lifecycle:%d done:%d resume:%d mainDestroy:%d childDestroy:%d taskRelease:%d",
			coroProgramLifecycleV1State, driver.doneCalls, driver.resumeCalls, driver.destroyCalls,
			driver.cancelDestroyCalls, driver.taskReleaseCalls)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(driver.childFrame.memory)
	runtime.KeepAlive(driver.child)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramMainReturnCancelsBoundedChildDestroyContinuation(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin bounded-child command program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{t: t, frame: frame, spawnBeforeMainReturn: true}
	activeCoroProgramDriver = driver
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveCompleteV1 {
		t.Fatalf("run bounded-child command program = %d", status)
	}
	if coroProgramLifecycleV1State != coroProgramCompleteV1 ||
		driver.doneCalls != 3 || driver.resumeCalls != 2 || driver.destroyCalls != 1 ||
		driver.childDoneCalls != 1 || driver.childResumeCalls != 1 || !driver.childCompleteReady ||
		driver.cancelDestroyCalls != 1 || driver.taskReleaseCalls != 1 ||
		driver.child == nil || driver.childFrame == nil ||
		!coroProgramTestTargetV1State.joined || coroProgramTestTargetV1State.closeCalls != 1 ||
		coroProgramExecutorBoundV1State || !coroProgramExecutorRegistryV1State.CanRelease() ||
		!coroProgramWaitTableV1State.CanRelease() ||
		!coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) ||
		!coro.TerminalG(&coroProgramPV1State, driver.child) {
		t.Fatalf("bounded child shutdown = lifecycle:%d main={done:%d resume:%d destroy:%d} child={done:%d resume:%d cancelDestroy:%d release:%d}",
			coroProgramLifecycleV1State, driver.doneCalls, driver.resumeCalls, driver.destroyCalls,
			driver.childDoneCalls, driver.childResumeCalls, driver.cancelDestroyCalls, driver.taskReleaseCalls)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(driver.childFrame.memory)
	runtime.KeepAlive(driver.child)
	runtime.KeepAlive(manifest)
}

func TestCoroProgramAsyncCommandJoinPrecedesReadyChildCancellation(t *testing.T) {
	resetCoroProgramTestStateV1(t)
	coroProgramTestTargetV1State.mode = coroProgramTestTargetAsyncV1
	manifest := newCoroProgramTestManifestV1()
	factory := unsafe.Pointer(&manifest.factoryMarker)
	gPointer, ok := coroProgramBeginV1(unsafe.Pointer(&manifest.manifest), factory)
	if !ok {
		t.Fatal("begin asynchronous command-shutdown program")
	}
	frame := newCoroProgramTestFrameV1(t, &coroProgramGV1State)
	driver := &coroProgramTestDriverV1{t: t, frame: frame, spawnOnMainReturn: true}
	activeCoroProgramDriver = driver
	if status := coroProgramRunV1(gPointer, frame.handle); status != coroProgramDriveSuspendedV1 {
		t.Fatalf("initial asynchronous command drive = %d", status)
	}
	epoch := coroProgramContinuationEpochV1
	if epoch == 0 || coroProgramContinuationV1State != coroProgramContinuationCommandJoinV1 ||
		coroProgramLifecycleV1State != coroProgramMainReturnRequestedV1 ||
		driver.destroyCalls != 1 || driver.cancelDestroyCalls != 0 || driver.taskReleaseCalls != 0 ||
		driver.child == nil || driver.childFrame == nil || !coroProgramExecutorBoundV1State ||
		coroProgramTestTargetV1State.closeCalls != 1 {
		t.Fatalf("command join crossed cancellation boundary: epoch=%d continuation=%d lifecycle=%d main=%d child=%d release=%d",
			epoch, coroProgramContinuationV1State, coroProgramLifecycleV1State,
			driver.destroyCalls, driver.cancelDestroyCalls, driver.taskReleaseCalls)
	}
	coroProgramTestTargetV1State.joined = true
	if status := coroProgramContinueV1(epoch); status != coroProgramDriveCompleteV1 {
		t.Fatalf("joined asynchronous command continuation = %d", status)
	}
	if coroProgramLifecycleV1State != coroProgramCompleteV1 || driver.cancelDestroyCalls != 1 ||
		driver.taskReleaseCalls != 1 || coroProgramExecutorBoundV1State ||
		coroProgramContinuationV1State != coroProgramContinuationNoneV1 ||
		!coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) ||
		!coro.TerminalG(&coroProgramPV1State, driver.child) {
		t.Fatalf("asynchronous command completion = lifecycle:%d childDestroy:%d release:%d bound:%t",
			coroProgramLifecycleV1State, driver.cancelDestroyCalls, driver.taskReleaseCalls,
			coroProgramExecutorBoundV1State)
	}
	runtime.KeepAlive(frame.memory)
	runtime.KeepAlive(driver.childFrame.memory)
	runtime.KeepAlive(driver.child)
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
	if status := coroProgramRunV1(g, nil); status != coroProgramDriveInvalidV1 || coroProgramLifecycleV1State != coroProgramFailedV1 {
		t.Fatalf("nil-handle run = %d, lifecycle=%d", status, coroProgramLifecycleV1State)
	}
	runtime.KeepAlive(manifest)
}
