//go:build coro_poll_owner_test

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

// This test is run as a named production-source island with
// coro_poll_owner_llgo.go. These definitions replace unrelated full-runtime
// services which intentionally cannot be linked into the host Go runtime.
var (
	coroProgramPV1State                coro.P
	coroProgramExecutorRegistryV1State coro.ExecutorRegistry
	coroProgramWaitTableV1State        coro.WaitRegistrationTable
	coroProgramPollSourceV1State       coro.PollOperationSource
	coroProgramExecutorDriverV1State   coro.ExecutorDriver
	coroProgramExecutorHandleV1State   coro.ExecutorHandle
	coroProgramExecutorBoundV1State    bool
)

func coroTargetRequestExecutorV1(handle coro.ExecutorHandle) bool {
	if !coroProgramExecutorBoundV1State || handle != coroProgramExecutorHandleV1State {
		return false
	}
	result := coroProgramExecutorRegistryV1State.Request(handle)
	return result == coro.ExecutorRequestPublished || result == coro.ExecutorRequestCoalesced ||
		result == coro.ExecutorRequestIdleWake
}

func coroRuntimeAbort(message string) { panic(message) }

type coroPollOwnerTestFrame struct {
	g          *coro.G
	handle     unsafe.Pointer
	header     *coro.HeaderV1
	storage    unsafe.Pointer
	descriptor unsafe.Pointer
	size       uintptr
	align      uintptr
	memory     []uintptr
}

func newCoroPollOwnerTestFrame(t *testing.T) *coroPollOwnerTestFrame {
	t.Helper()
	g := new(coro.G)
	if !coro.InitG(g) {
		t.Fatal("initialize poll owner G")
	}
	const size, align = uintptr(32), uintptr(8)
	total, ok := coro.FrameAllocationSize(size, align)
	if !ok {
		t.Fatal("compute poll owner frame size")
	}
	words := (total + unsafe.Sizeof(uintptr(0)) - 1) / unsafe.Sizeof(uintptr(0))
	memory := make([]uintptr, words)
	descriptor := unsafe.Pointer(&coro.FrameDescriptorV1{Version: 1, ResultAlign: 1})
	storage, ok := coro.RegisterFrame(g, unsafe.Pointer(&memory[0]), total, size, align, descriptor)
	if !ok {
		t.Fatal("register poll owner frame")
	}
	handle := unsafe.Pointer(new(byte))
	header := &coro.HeaderV1{
		G:             unsafe.Pointer(g),
		Descriptor:    descriptor,
		SuspendReason: uint16(coro.SuspendNone),
		Lifecycle:     uint16(coro.FrameInitialSuspended),
	}
	if !coro.PublishFrame(g, handle, header, storage) || !coro.AdoptRoot(g, handle) {
		t.Fatal("publish poll owner frame")
	}
	return &coroPollOwnerTestFrame{
		g: g, handle: handle, header: header, storage: storage,
		descriptor: descriptor, size: size, align: align, memory: memory,
	}
}

func activateCoroPollOwnerTestFrame(t *testing.T, p *coro.P, frame *coroPollOwnerTestFrame, bound bool) coro.Action {
	t.Helper()
	var next *coro.G
	var ok bool
	if bound {
		next, ok = coro.NextRunnableAt(p, 0)
	} else {
		next, ok = coro.NextRunnable(p)
	}
	if !ok || next != frame.g {
		t.Fatalf("dequeue poll owner G = (%p, %t)", next, ok)
	}
	action, ok := coro.BeginRunG(p, frame.g)
	if !ok || action.Kind != coro.ActionCheckResume {
		t.Fatalf("begin poll owner G = (%+v, %t)", action, ok)
	}
	action, ok = coro.Checked(p, frame.g, action, false)
	if !ok || action.Kind != coro.ActionResume {
		t.Fatalf("activate poll owner G = (%+v, %t)", action, ok)
	}
	if outcome, caseID, lease, task, taken := coro.TakeRunDecision(frame.g, coro.ParkTicket{}); !taken || outcome != coro.ParkOutcomePending || caseID != 0 || lease.Valid() || task != coro.TaskCancelNone {
		t.Fatalf("take poll owner resume gate = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, task, taken)
	}
	frame.header.SuspendReason = uint16(coro.SuspendNone)
	frame.header.Lifecycle = uint16(coro.FrameActive)
	return action
}

func finishCoroPollOwnerTestRun(t *testing.T, frame *coroPollOwnerTestFrame, action coro.Action) {
	t.Helper()
	frame.header.SuspendReason = uint16(coro.SuspendYield)
	frame.header.Lifecycle = uint16(coro.FrameSuspended)
	if !coro.PrepareYield(frame.g, frame.handle, frame.header) {
		t.Fatal("prepare poll owner yield")
	}
	if yielded, ok := coro.Resumed(&coroProgramPV1State, frame.g, action); !ok || yielded.Kind != coro.ActionYield {
		t.Fatalf("yield poll owner G = (%+v, %t)", yielded, ok)
	}
	if !coro.BeginExecutorClose(&coroProgramExecutorDriverV1State) ||
		!coro.ConfirmExecutorClose(&coroProgramExecutorDriverV1State) {
		t.Fatal("close poll owner executor")
	}
	if coroProgramExecutorDriverV1State != (coro.ExecutorDriver{}) ||
		!coroProgramExecutorRegistryV1State.CanRelease() || !coroProgramWaitTableV1State.CanRelease() ||
		!coroProgramPollSourceV1State.CanRelease() {
		t.Fatal("closed poll owner executor retained core resources")
	}
	coroProgramExecutorBoundV1State = false
	coroProgramExecutorHandleV1State = coro.ExecutorHandle{}

	action = activateCoroPollOwnerTestFrame(t, &coroProgramPV1State, frame, false)
	frame.header.SuspendReason = uint16(coro.SuspendFrameComplete)
	frame.header.Lifecycle = uint16(coro.FrameFinalSuspended)
	if !coro.PrepareComplete(frame.g, frame.handle, frame.header) {
		t.Fatal("prepare poll owner completion")
	}
	action, ok := coro.Resumed(&coroProgramPV1State, frame.g, action)
	if !ok || action.Kind != coro.ActionCheckDestroy {
		t.Fatalf("resume poll owner completion = (%+v, %t)", action, ok)
	}
	action, ok = coro.Checked(&coroProgramPV1State, frame.g, action, true)
	if !ok || action.Kind != coro.ActionDestroy {
		t.Fatalf("check poll owner destroy = (%+v, %t)", action, ok)
	}
	if _, _, ok := coro.ReleaseFrame(frame.g, frame.storage, frame.size, frame.align, frame.descriptor); !ok {
		t.Fatal("release poll owner frame")
	}
	if action, ok = coro.Destroyed(&coroProgramPV1State, frame.g, action); !ok || action.Kind != coro.ActionComplete {
		t.Fatalf("destroy poll owner frame = (%+v, %t)", action, ok)
	}
	if !coro.TerminalG(&coroProgramPV1State, frame.g) || !coroProgramPollSourceV1State.CanRelease() {
		t.Fatal("poll owner cleanup retained state")
	}
	runtime.KeepAlive(frame.memory)
	coroProgramPV1State = coro.P{}
	coroProgramExecutorRegistryV1State = coro.ExecutorRegistry{}
	coroProgramWaitTableV1State = coro.WaitRegistrationTable{}
	coroProgramPollSourceV1State = coro.PollOperationSource{}
}

func TestCoroPollOwnerV2ParkEventResumeAndRecycle(t *testing.T) {
	if coroProgramExecutorBoundV1State || coroProgramExecutorHandleV1State != (coro.ExecutorHandle{}) ||
		coroProgramExecutorDriverV1State != (coro.ExecutorDriver{}) ||
		!coroProgramExecutorRegistryV1State.CanRelease() || !coroProgramWaitTableV1State.CanRelease() ||
		!coroProgramPollSourceV1State.CanRelease() {
		t.Fatal("Poll V2 owner globals are not initially releasable")
	}
	executor, ok := coroProgramExecutorRegistryV1State.Register()
	if !ok || !coro.BindExecutorSourceCatalog(
		&coroProgramExecutorDriverV1State,
		&coroProgramPV1State,
		&coroProgramExecutorRegistryV1State,
		executor,
		coro.ExecutorSourceCatalog{Waits: &coroProgramWaitTableV1State, Poll: &coroProgramPollSourceV1State},
	) {
		t.Fatal("bind Poll V2 owner executor")
	}
	coroProgramExecutorHandleV1State = executor
	coroProgramExecutorBoundV1State = true

	frame := newCoroPollOwnerTestFrame(t)
	if !coro.Enqueue(&coroProgramPV1State, frame.g) {
		t.Fatal("enqueue Poll V2 owner frame")
	}
	action := activateCoroPollOwnerTestFrame(t, &coroProgramPV1State, frame, true)
	frame.header.SuspendReason = uint16(coro.SuspendPark)
	frame.header.Lifecycle = uint16(coro.FrameSuspended)
	var state CoroPollParkV2
	__llgo_coro_poll_park_v2(
		unsafe.Pointer(frame.g),
		frame.handle,
		unsafe.Pointer(frame.header),
		unsafe.Pointer(&state),
		17,
		uint32(coro.PollInterestRead),
		0,
	)
	if !validCoroPollParkV2(&state) || state.executor != executor {
		t.Fatalf("invalid prepared Poll V2 ABI state: %+v", state)
	}
	operation := state.operation
	if parked, ok := coro.Resumed(&coroProgramPV1State, frame.g, action); !ok || parked.Kind != coro.ActionPark {
		t.Fatalf("commit Poll V2 owner park = (%+v, %t)", parked, ok)
	}
	if result := coro.PollOperationPostResult(__llgo_coro_poll_post_event_v2(
		operation.SourceSlot,
		operation.Generation+1,
		uint32(coro.PollOperationReady),
	)); result == coro.PollOperationPosted {
		t.Fatal("Poll V2 owner-return import accepted stale generation")
	}
	if result := coro.PollOperationPostResult(__llgo_coro_poll_post_event_v2(
		operation.SourceSlot,
		operation.Generation,
		uint32(coro.PollOperationReady),
	)); result != coro.PollOperationPosted {
		t.Fatalf("post Poll V2 ABI readiness = %d", result)
	}
	if waits, timers, promoted, ok := coro.PollExecutorAt(&coroProgramExecutorDriverV1State, 0); !ok ||
		waits != 0 || timers != 0 || promoted != 1 {
		t.Fatalf("scan Poll V2 ABI readiness = (%d, %d, %d, %t)", waits, timers, promoted, ok)
	}
	if next, ok := coro.NextRunnableAt(&coroProgramPV1State, 0); !ok || next != frame.g {
		t.Fatalf("dequeue Poll V2 resumed G = (%p, %t)", next, ok)
	}
	checked, ok := coro.BeginRunG(&coroProgramPV1State, frame.g)
	if !ok || checked.Kind != coro.ActionCheckResume {
		t.Fatalf("begin Poll V2 resumed G = (%+v, %t)", checked, ok)
	}
	action, ok = coro.Checked(&coroProgramPV1State, frame.g, checked, false)
	if !ok || action.Kind != coro.ActionResume {
		t.Fatalf("activate Poll V2 resumed G = (%+v, %t)", action, ok)
	}
	frame.header.SuspendReason = uint16(coro.SuspendPark)
	frame.header.Lifecycle = uint16(coro.FrameSuspended)
	if status := __llgo_coro_poll_resume_v2(unsafe.Pointer(frame.g), unsafe.Pointer(&state)); status != coroPollResumeReadyV2 || state != (CoroPollParkV2{}) {
		t.Fatalf("resume Poll V2 ABI = (%d, %+v)", status, state)
	}
	frame.header.SuspendReason = uint16(coro.SuspendNone)
	frame.header.Lifecycle = uint16(coro.FrameActive)
	finishCoroPollOwnerTestRun(t, frame, action)
}
