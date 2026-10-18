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
	"os"
	"runtime"
	"testing"
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
	"github.com/xgo-dev/llgo/runtime/internal/corodoorbell"
)

// This test is run as a named production-source island with
// coro_poll_owner_llgo.go. These definitions replace unrelated full-runtime
// services which intentionally cannot be linked into the host Go runtime.
var (
	coroProgramPV1State                coro.P
	coroProgramExecutorRegistryV1State coro.ExecutorRegistry
	coroProgramPollSourceV1State       coro.PollOperationSource
	coroProgramExecutorDriverV1State   coro.ExecutorDriver
	coroProgramExecutorHandleV1State   coro.ExecutorHandle
	coroProgramExecutorBoundV1State    bool
)

const coroPollOwnerTestDescriptorContext uintptr = 1

var (
	coroNativePollEntriesV1 [corodoorbell.PollSetCapacity]corodoorbell.PollFD
	coroNativePollIDsV2     [corodoorbell.PollSetCapacity - 1]coro.OperationID
)

var coroPollOwnerTestDescriptorState struct {
	operation coro.OperationID
	interest  coro.PollInterest
	deadline  int64
	closing   bool
}

func coroPollDescPublishOperationV1(
	context uintptr,
	interest coro.PollInterest,
	operation coro.OperationID,
) (bool, bool) {
	state := &coroPollOwnerTestDescriptorState
	if context != coroPollOwnerTestDescriptorContext || state.operation.Valid() ||
		!operation.Valid() || operation.Source() != coro.OperationSourcePoll ||
		(interest != coro.PollInterestRead && interest != coro.PollInterestWrite) {
		return false, false
	}
	state.operation = operation
	state.interest = interest
	return state.closing, true
}

func coroPollDescClearOperationV1(
	context uintptr,
	interest coro.PollInterest,
	operation coro.OperationID,
) bool {
	state := &coroPollOwnerTestDescriptorState
	if context != coroPollOwnerTestDescriptorContext || state.interest != interest ||
		state.operation != operation {
		return false
	}
	state.operation = coro.OperationID{}
	state.interest = coro.PollInterestInvalid
	return true
}

func coroPollDescLoadOperationV1(context uintptr, interest coro.PollInterest) (coro.OperationID, bool) {
	state := &coroPollOwnerTestDescriptorState
	if context != coroPollOwnerTestDescriptorContext ||
		(interest != coro.PollInterestRead && interest != coro.PollInterestWrite) {
		return coro.OperationID{}, false
	}
	if state.operation.Valid() && state.interest != interest {
		return coro.OperationID{}, true
	}
	return state.operation, true
}

func coroPollDescDeadlineV1(context uintptr, interest coro.PollInterest) (int64, bool) {
	if context != coroPollOwnerTestDescriptorContext ||
		(interest != coro.PollInterestRead && interest != coro.PollInterestWrite) {
		return 0, false
	}
	return coroPollOwnerTestDescriptorState.deadline, true
}

func coroTargetRequestExecutorV1(handle coro.ExecutorHandle) bool {
	if !coroProgramExecutorBoundV1State || handle != coroProgramExecutorHandleV1State {
		return false
	}
	result := coroProgramExecutorRegistryV1State.Request(handle)
	return result == coro.ExecutorRequestPublished || result == coro.ExecutorRequestCoalesced ||
		result == coro.ExecutorRequestIdleWake
}

func coroTargetPostPollOperationV2(
	id coro.OperationID,
	status coro.PollOperationResult,
) coro.PollOperationPostResult {
	if !coroProgramExecutorBoundV1State || coroProgramExecutorHandleV1State == (coro.ExecutorHandle{}) {
		return coro.PollOperationPostInvalid
	}
	result := coro.PostExecutorPollEvent(&coroProgramExecutorDriverV1State, id, status)
	if result == coro.PollOperationPosted && !coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State) {
		return coro.PollOperationPostInvalid
	}
	return result
}

func coroRuntimeAbort(message string) { panic(message) }

func TestCoroPollResumeStatusV2DeadlineHandshake(t *testing.T) {
	state := CoroPollParkV2{
		context:  coroPollOwnerTestDescriptorContext,
		interest: coro.PollInterestRead,
		deadline: 50,
	}
	for _, test := range []struct {
		name     string
		current  int64
		result   coro.PollOperationResult
		want     uint32
		wantOkay bool
	}{
		{name: "unchanged-readiness", current: 50, result: coro.PollOperationReady, want: coroPollResumeReadyV2, wantOkay: true},
		{name: "changed-deadline-recheck", current: 75, result: coro.PollOperationReady, want: coroPollResumeTimeoutV2, wantOkay: true},
		{name: "removed-deadline-recheck", current: 0, result: coro.PollOperationReady, want: coroPollResumeTimeoutV2, wantOkay: true},
		{name: "closing", current: 75, result: coro.PollOperationClosing, want: coroPollResumeClosingV2, wantOkay: true},
		{name: "timeout", current: 75, result: coro.PollOperationTimeout, want: coroPollResumeTimeoutV2, wantOkay: true},
		{name: "invalid", current: 50, result: coro.PollOperationResultInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			coroPollOwnerTestDescriptorState.deadline = test.current
			got, ok := coroPollResumeStatusV2(&state, test.result)
			if got != test.want || ok != test.wantOkay {
				t.Fatalf("resume status = (%d, %t), want (%d, %t)", got, ok, test.want, test.wantOkay)
			}
		})
	}
	coroPollOwnerTestDescriptorState.deadline = 0
}

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
		!coroProgramExecutorRegistryV1State.CanRelease() ||
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
	coroProgramPollSourceV1State = coro.PollOperationSource{}
	coroPollOwnerTestDescriptorState.operation = coro.OperationID{}
	coroPollOwnerTestDescriptorState.interest = coro.PollInterestInvalid
	coroPollOwnerTestDescriptorState.deadline = 0
	coroPollOwnerTestDescriptorState.closing = false
}

func TestCoroPollOwnerV2ParkEventResumeAndRecycle(t *testing.T) {
	testCoroPollOwnerV2ParkEventResumeAndRecycle(t, coro.PollOperationResultInvalid)
}

func TestCoroPollRetainedReadinessAfterConcurrentPublication(t *testing.T) {
	for _, first := range []coro.PollOperationResult{coro.PollOperationReady, coro.PollOperationClosing} {
		t.Run(map[coro.PollOperationResult]string{coro.PollOperationReady: "ready", coro.PollOperationClosing: "closing"}[first], func(t *testing.T) {
			testCoroPollOwnerV2ParkEventResumeAndRecycle(t, first)
		})
	}
}

func testCoroPollOwnerV2ParkEventResumeAndRecycle(t *testing.T, first coro.PollOperationResult) {
	t.Helper()
	fd := int32(17)
	var reader, writer *os.File
	if first != coro.PollOperationResultInvalid {
		var err error
		reader, writer, err = os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		defer writer.Close()
		fd = int32(reader.Fd())
	}
	if coroProgramExecutorBoundV1State || coroProgramExecutorHandleV1State != (coro.ExecutorHandle{}) ||
		coroProgramExecutorDriverV1State != (coro.ExecutorDriver{}) ||
		!coroProgramExecutorRegistryV1State.CanRelease() ||
		!coroProgramPollSourceV1State.CanRelease() {
		t.Fatal("Poll V2 owner globals are not initially releasable")
	}
	executor, ok := coroProgramExecutorRegistryV1State.Register()
	if !ok || !coro.BindExecutorSourceCatalog(
		&coroProgramExecutorDriverV1State,
		&coroProgramPV1State,
		&coroProgramExecutorRegistryV1State,
		executor,
		coro.ExecutorSourceCatalog{Poll: &coroProgramPollSourceV1State},
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
		coroPollOwnerTestDescriptorContext,
		fd,
		uint32(coro.PollInterestRead),
		0,
	)
	if !validCoroPollParkV2(&state) || state.executor != executor {
		t.Fatalf("invalid prepared Poll V2 ABI state: %+v", state)
	}
	operation := state.operation
	if coroPollOwnerTestDescriptorState.operation != operation ||
		coroPollOwnerTestDescriptorState.interest != coro.PollInterestRead {
		t.Fatalf("Poll V2 descriptor did not retain exact operation: %+v", coroPollOwnerTestDescriptorState)
	}
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
	want := coroPollResumeClosingV2
	if first == coro.PollOperationResultInvalid {
		__llgo_coro_poll_post_closing_or_abort_v1(
			coroPollOwnerTestDescriptorContext,
			uint32(coro.PollInterestRead),
		)
	} else {
		// Preserve the real owner snapshot and real poll revents, then publish
		// the competing fact before importing that retained readiness event.
		// This fixes the race ordering without a timing-dependent sleep or a
		// production callback hook.
		snapshot, active, ok := coro.SnapshotExecutorPollOperation(&coroProgramExecutorDriverV1State, operation.LocalSlot()-1)
		if !ok || !active || snapshot.ID != operation || snapshot.FD != fd {
			t.Fatalf("snapshot poll owner = (%+v, %t, %t)", snapshot, active, ok)
		}
		coroNativePollEntriesV1[0] = corodoorbell.PollFD{FD: -1}
		coroNativePollEntriesV1[1] = corodoorbell.PollFD{FD: snapshot.FD, Events: corodoorbell.PollRead}
		coroNativePollIDsV2[0] = snapshot.ID
		if _, err := writer.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		if ready, errno := corodoorbell.WaitPollSet(&coroNativePollEntriesV1[0], 2, 0); ready != 1 || errno != 0 || coroNativePollEntriesV1[1].Revents&corodoorbell.PollRead == 0 {
			t.Fatalf("physical retained readiness = (%d, %d, %+v)", ready, errno, coroNativePollEntriesV1[1])
		}
		for _, count := range []uint32{0, corodoorbell.PollSetCapacity + 1} {
			if posted, ok := coroTargetPostPollEventsV1(count); ok || posted {
				t.Fatalf("retained import accepted invalid count %d", count)
			}
		}
		coroNativePollIDsV2[0].Generation++
		if posted, ok := coroTargetPostPollEventsV1(2); ok || posted {
			t.Fatal("retained import accepted stale operation")
		}
		coroNativePollIDsV2[0] = coro.OperationID{}
		if posted, ok := coroTargetPostPollEventsV1(2); ok || posted {
			t.Fatal("retained import accepted invalid operation")
		}
		coroNativePollIDsV2[0] = snapshot.ID
		if result := coro.PollOperationPostResult(__llgo_coro_poll_post_event_v2(operation.SourceSlot, operation.Generation, uint32(first))); result != coro.PollOperationPosted {
			t.Fatalf("publish competing poll fact = %d", result)
		}
		if posted, ok := coroTargetPostPollEventsV1(2); !ok || !posted {
			t.Fatalf("import retained readiness after competing publication = (%t, %t)", posted, ok)
		}
		// A second notification must remain harmless and must not create a
		// second delivery or overwrite Closing with Ready.
		if posted, ok := coroTargetPostPollEventsV1(2); !ok || !posted {
			t.Fatalf("repeat retained readiness = (%t, %t)", posted, ok)
		}
		if first == coro.PollOperationReady {
			want = coroPollResumeReadyV2
		}
	}
	if timers, promoted, ok := coro.PollExecutorAt(&coroProgramExecutorDriverV1State, 0); !ok ||
		timers != 0 || promoted != 1 {
		t.Fatalf("scan Poll V2 ABI readiness = (%d, %d, %t)", timers, promoted, ok)
	}
	if first != coro.PollOperationResultInvalid {
		if timers, promoted, ok := coro.PollExecutorAt(&coroProgramExecutorDriverV1State, 0); !ok || timers != 0 || promoted != 0 {
			t.Fatalf("duplicate poll fact promoted again = (%d, %d, %t)", timers, promoted, ok)
		}
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
	if status := __llgo_coro_poll_resume_v2(unsafe.Pointer(frame.g), unsafe.Pointer(&state)); status != want || state != (CoroPollParkV2{}) {
		t.Fatalf("resume Poll V2 ABI = (%d, %+v)", status, state)
	}
	if coroPollOwnerTestDescriptorState.operation.Valid() {
		t.Fatalf("Poll V2 resume retained descriptor operation: %+v", coroPollOwnerTestDescriptorState)
	}
	if first != coro.PollOperationResultInvalid {
		if posted, ok := coroTargetPostPollEventsV1(2); ok || posted {
			t.Fatal("retained import accepted retired operation")
		}
	}
	frame.header.SuspendReason = uint16(coro.SuspendNone)
	frame.header.Lifecycle = uint16(coro.FrameActive)
	finishCoroPollOwnerTestRun(t, frame, action)
}
