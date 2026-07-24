//go:build coro_native_fleet_test && (darwin || linux) && !baremetal && !llgo

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
	stdruntime "runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

type coroNativeFleetTestTask struct {
	g           *coro.G
	handle      unsafe.Pointer
	header      *coro.HeaderV1
	descriptor  *coro.FrameDescriptorV1
	storage     unsafe.Pointer
	raw         unsafe.Pointer
	frameSize   uintptr
	frameAlign  uintptr
	allocation  uintptr
	memory      []uintptr
	resumeCalls uint32
	runFailure  string
	complete    bool
	destroyed   bool

	pollMode      bool
	pollFD        int32
	pollDriver    *coro.ExecutorDriver
	pollWait      coro.WaitSetRecord
	pollTicket    coro.ParkTicket
	pollHandle    coro.PollOperationHandle
	pollOperation coro.OperationID
	pollExecutor  coro.ExecutorHandle
	pollPrepared  bool
	pollFinished  bool
}

var coroNativeFleetRunTestV1 struct {
	sync.Mutex
	tasks map[unsafe.Pointer]*coroNativeFleetTestTask
}

func findCoroNativeFleetRunTestTaskV1(handle unsafe.Pointer) *coroNativeFleetTestTask {
	coroNativeFleetRunTestV1.Lock()
	defer coroNativeFleetRunTestV1.Unlock()
	return coroNativeFleetRunTestV1.tasks[handle]
}

func failCoroNativeFleetRunTestTaskV1(task *coroNativeFleetTestTask, message string) {
	if task == nil {
		return
	}
	coroNativeFleetRunTestV1.Lock()
	if task.runFailure == "" {
		task.runFailure = message
	}
	coroNativeFleetRunTestV1.Unlock()
}

// The isolated native-fleet test links the production reducer but not the
// allocator-backed runtime task reclaimer. Its runner tasks are static.
func coroReleaseCompletedTask(g *coro.G) bool {
	return coro.ReclaimableG(g)
}

//go:linkname testCoroNativeFleetHandleDone C.__llgo_coro_done_v1
func testCoroNativeFleetHandleDone(handle unsafe.Pointer) bool {
	task := findCoroNativeFleetRunTestTaskV1(handle)
	if task == nil {
		panic("native fleet done wrapper received an unknown handle")
	}
	return task.complete
}

//go:linkname testCoroNativeFleetHandleResume C.__llgo_coro_resume_v1
func testCoroNativeFleetHandleResume(handle unsafe.Pointer) {
	task := findCoroNativeFleetRunTestTaskV1(handle)
	if task == nil {
		panic("native fleet resume wrapper received an unknown handle")
	}
	expected := coro.ParkTicket{}
	if task.pollPrepared && !task.pollFinished {
		expected = task.pollTicket
	}
	outcome, caseID, lease, control, taken := coro.TakeRunDecision(task.g, expected)
	if task.pollMode {
		switch {
		case !task.pollPrepared:
			if !taken || outcome != coro.ParkOutcomePending || caseID != 0 ||
				lease != (coro.OperationResultLease{}) || control != coro.TaskCancelNone {
				failCoroNativeFleetRunTestTaskV1(task, "invalid initial poll resume decision")
				return
			}
			task.header.SuspendReason = uint16(coro.SuspendNone)
			task.header.Lifecycle = uint16(coro.FrameActive)
			task.header.SuspendReason = uint16(coro.SuspendPark)
			task.header.Lifecycle = uint16(coro.FrameSuspended)
			ticket, poll, operation, executor, ok := coro.PrepareCurrentExecutorPollPark(
				task.pollDriver,
				task.g,
				handle,
				task.header,
				&task.pollWait,
				1,
				901,
				task.pollFD,
				coro.PollInterestRead,
				0,
			)
			if !ok {
				failCoroNativeFleetRunTestTaskV1(task, "prepare routed poll park failed")
				return
			}
			task.pollTicket = ticket
			task.pollHandle = poll
			task.pollOperation = operation
			task.pollExecutor = executor
			task.pollPrepared = true
			task.resumeCalls++
			return
		case !task.pollFinished:
			if !taken || outcome != coro.ParkOutcomeCompleted || caseID != 1 ||
				!lease.Valid() || control != coro.TaskCancelNone {
				failCoroNativeFleetRunTestTaskV1(task, "invalid completed poll resume decision")
				return
			}
			result, ok := coro.FinishCurrentExecutorPollPark(
				task.pollDriver,
				task.g,
				task.pollExecutor,
				task.pollHandle,
				task.pollOperation,
				lease,
				false,
			)
			if !ok || result != coro.PollOperationReady {
				failCoroNativeFleetRunTestTaskV1(task, "finish routed poll park failed")
				return
			}
			task.pollFinished = true
			task.header.SuspendReason = uint16(coro.SuspendFrameComplete)
			task.header.Lifecycle = uint16(coro.FrameFinalSuspended)
			if !coro.PrepareComplete(task.g, handle, task.header) {
				failCoroNativeFleetRunTestTaskV1(task, "prepare routed poll completion failed")
				return
			}
			task.complete = true
			task.resumeCalls++
			return
		default:
			failCoroNativeFleetRunTestTaskV1(task, "completed poll task resumed again")
			return
		}
	}
	if !taken || outcome != coro.ParkOutcomePending || caseID != 0 ||
		lease != (coro.OperationResultLease{}) || control != coro.TaskCancelNone {
		failCoroNativeFleetRunTestTaskV1(task, "invalid normal resume decision")
		return
	}
	task.header.SuspendReason = uint16(coro.SuspendNone)
	task.header.Lifecycle = uint16(coro.FrameActive)
	task.header.SuspendReason = uint16(coro.SuspendYield)
	task.header.Lifecycle = uint16(coro.FrameSuspended)
	if !coro.PrepareYield(task.g, handle, task.header) {
		failCoroNativeFleetRunTestTaskV1(task, "prepare yield failed")
		return
	}
	coroNativeFleetRunTestV1.Lock()
	task.resumeCalls++
	coroNativeFleetRunTestV1.Unlock()
}

//go:linkname testCoroNativeFleetHandleDestroy C.__llgo_coro_destroy_v1
func testCoroNativeFleetHandleDestroy(handle unsafe.Pointer) {
	task := findCoroNativeFleetRunTestTaskV1(handle)
	if task != nil && task.complete {
		raw, total, ok := coro.ReleaseFrame(
			task.g,
			task.storage,
			task.frameSize,
			task.frameAlign,
			unsafe.Pointer(task.descriptor),
		)
		if !ok || raw != task.raw || total != task.allocation {
			failCoroNativeFleetRunTestTaskV1(task, "release completed routed poll frame failed")
			return
		}
		task.destroyed = true
		return
	}
	failCoroNativeFleetRunTestTaskV1(task, "yield-only fleet task was destroyed")
}

func newCoroNativeFleetTestTask(t *testing.T, source *coro.P) *coroNativeFleetTestTask {
	t.Helper()
	task := &coroNativeFleetTestTask{
		g:          new(coro.G),
		handle:     unsafe.Pointer(new(byte)),
		descriptor: &coro.FrameDescriptorV1{Version: 1, ResultAlign: 1},
		frameSize:  64,
	}
	if !coro.InitG(task.g) {
		t.Fatal("initialize fleet transfer task")
	}
	task.frameAlign = unsafe.Alignof(uintptr(0))
	total, ok := coro.FrameAllocationSize(task.frameSize, task.frameAlign)
	if !ok {
		t.Fatal("compute fleet transfer frame allocation")
	}
	task.allocation = total
	task.memory = make([]uintptr, (total+unsafe.Sizeof(uintptr(0))-1)/unsafe.Sizeof(uintptr(0)))
	task.raw = unsafe.Pointer(&task.memory[0])
	storage, ok := coro.RegisterFrame(
		task.g,
		task.raw,
		total,
		task.frameSize,
		task.frameAlign,
		unsafe.Pointer(task.descriptor),
	)
	if !ok {
		t.Fatal("register fleet transfer frame")
	}
	task.storage = storage
	task.header = &coro.HeaderV1{
		G:          unsafe.Pointer(task.g),
		Descriptor: unsafe.Pointer(task.descriptor),
		Lifecycle:  uint16(coro.FrameInitialSuspended),
	}
	if !coro.PublishFrame(task.g, task.handle, task.header, storage) ||
		!coro.AdoptRoot(task.g, task.handle) || !coro.Enqueue(source, task.g) {
		t.Fatal("publish fleet transfer task")
	}
	return task
}

type coroNativeFleetTestOperation struct {
	state  coro.ParkState
	ticket coro.ParkTicket
	id     coro.OperationID
}

func reserveCoroNativeFleetManualV1(
	t *testing.T,
	domain *coroNativeFleetDomainV1,
	seed uint32,
) *coroNativeFleetTestOperation {
	t.Helper()
	operation := new(coroNativeFleetTestOperation)
	var ok bool
	operation.ticket, ok = coro.BeginParkSet(&operation.state, 1, seed)
	if !ok {
		t.Fatal("begin fleet manual park set")
	}
	operation.id, ok = domain.manual.ReserveAndAttach(&domain.p, &operation.state, operation.ticket, seed)
	if !ok || !coro.SealParkSet(&operation.state, operation.ticket) ||
		!coro.CommitParkSet(&operation.state, operation.ticket) {
		t.Fatal("reserve fleet manual operation")
	}
	return operation
}

func reserveCoroNativeFleetWorkerV1(
	t *testing.T,
	domain *coroNativeFleetDomainV1,
	seed uint32,
) *coroNativeFleetTestOperation {
	t.Helper()
	operation := new(coroNativeFleetTestOperation)
	var ok bool
	operation.ticket, ok = coro.BeginParkSet(&operation.state, 1, seed)
	if !ok {
		t.Fatal("begin fleet worker park set")
	}
	operation.id, ok = domain.worker.ReserveAndAttach(&domain.p, &operation.state, operation.ticket, seed)
	if !ok || !coro.SealParkSet(&operation.state, operation.ticket) ||
		!coro.CommitParkSet(&operation.state, operation.ticket) ||
		!domain.worker.MarkSubmitted(&domain.p, operation.id) {
		t.Fatal("reserve fleet worker operation")
	}
	return operation
}

func finishCoroNativeFleetManualV1(
	t *testing.T,
	domain *coroNativeFleetDomainV1,
	operation *coroNativeFleetTestOperation,
) {
	t.Helper()
	outcome, _, lease, consumed := coro.ConsumeParkSet(&operation.state, operation.ticket)
	quiesced := domain.manual.ConfirmQuiesced(&domain.p, operation.id)
	taken := quiesced && domain.manual.TakeResult(&domain.p, lease)
	recycled := taken && domain.manual.Recycle(&domain.p, operation.id)
	if !consumed || outcome != coro.ParkOutcomeCompleted || !quiesced || !taken || !recycled {
		t.Fatalf("finish fleet manual operation %+v: outcome=%d consumed=%t quiesced=%t taken=%t recycled=%t lease=%+v",
			operation.id, outcome, consumed, quiesced, taken, recycled, lease)
	}
}

func finishCoroNativeFleetWorkerV1(
	t *testing.T,
	domain *coroNativeFleetDomainV1,
	operation *coroNativeFleetTestOperation,
	want coro.ScalarResultPayloadV1,
) {
	t.Helper()
	outcome, _, lease, consumed := coro.ConsumeParkSet(&operation.state, operation.ticket)
	var got coro.ScalarResultPayloadV1
	quiesced := domain.worker.ConfirmQuiesced(&domain.p, operation.id)
	taken := quiesced && domain.worker.TakeResult(&domain.p, lease, &got)
	recycled := taken && domain.worker.Recycle(&domain.p, operation.id)
	if !consumed || outcome != coro.ParkOutcomeCompleted || !quiesced || !taken || got != want || !recycled {
		t.Fatalf("finish fleet worker operation %+v: outcome=%d consumed=%t quiesced=%t taken=%t recycled=%t got %+v want %+v",
			operation.id, outcome, consumed, quiesced, taken, recycled, got, want)
	}
}

// The production scheduler attaches operation records to frame-local
// WaitSetRecords. This source-island test deliberately uses standalone
// ParkStates so it can verify target routing without manufacturing a physical
// LLVM resume. Settle the two source-owned records first, then let the real
// ExecutorDriver poll perform the common request acknowledgement.
func settleCoroNativeFleetStandaloneSourcesV1(domain *coroNativeFleetDomainV1) bool {
	manualPublished, manualLost, manualPublishOK := domain.manual.PublishPass(&domain.p)
	workerPublished, workerLost, workerPublishOK := domain.worker.PublishPass(&domain.p)
	if !manualPublishOK || !workerPublishOK || manualPublished != 1 || manualLost != 0 ||
		workerPublished != 1 || workerLost != 0 {
		return false
	}
	want := coro.CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}
	manualResolution, manualDuplicates, manualResolveOK := domain.manual.ResolveAffectedPublishedEpoch(&domain.p)
	workerResolution, workerDuplicates, workerResolveOK := domain.worker.ResolveAffectedPublishedEpoch(&domain.p)
	if !manualResolveOK || !workerResolveOK || manualResolution != want || workerResolution != want ||
		manualDuplicates != 0 || workerDuplicates != 0 {
		return false
	}
	manualApplied, manualDetached, manualApplyOK := domain.manual.ApplyAndDetach(&domain.p)
	workerApplied, workerDetached, workerApplyOK := domain.worker.ApplyAndDetach(&domain.p)
	return manualApplyOK && workerApplyOK && manualApplied == 1 && manualDetached == 1 &&
		workerApplied == 1 && workerDetached == 1
}

func TestCoroNativeFleetProductionIslandsV1(t *testing.T) {
	if !coroNativeFleetStartV1() {
		t.Fatal("start native production fleet")
	}
	firstHandle, firstOK := coroNativeFleetHandleV1(0)
	secondHandle, secondOK := coroNativeFleetHandleV1(1)
	if !firstOK || !secondOK || firstHandle.Route != 1 || secondHandle.Route != 2 ||
		firstHandle.Executor == secondHandle.Executor {
		t.Fatalf("native fleet handles = (%+v, %t), (%+v, %t)", firstHandle, firstOK, secondHandle, secondOK)
	}
	if !coroNativeFleetWorkerTransportReadyV1() {
		t.Fatal("native fleet worker transport preflight rejected bound routes")
	}
	first := &coroNativeFleetV1State.domains[0]
	second := &coroNativeFleetV1State.domains[1]
	coroNativeFleetRunTestV1.Lock()
	coroNativeFleetRunTestV1.tasks = make(map[unsafe.Pointer]*coroNativeFleetTestTask)
	coroNativeFleetRunTestV1.Unlock()
	defer func() {
		coroNativeFleetRunTestV1.Lock()
		coroNativeFleetRunTestV1.tasks = nil
		coroNativeFleetRunTestV1.Unlock()
	}()

	// An empty secondary P is a valid standby executor: it has no command-main
	// completion meaning and must remain wakeable for later routed transfers.
	standbyEpoch, standbyOK := coroNativeFleetBeginOwnerEpochV1(secondHandle)
	if !standbyOK {
		t.Fatal("begin empty fleet standby owner")
	}
	standbyRun := coroNativeFleetRunOwnerEpochV1(secondHandle, standbyEpoch, 10, 8)
	if standbyRun.stop != coroRunIdleV1 {
		t.Fatalf("empty fleet standby run = %+v", standbyRun)
	}
	standby, standbyOK := coroNativeFleetPrepareOwnerWaitAtV1(secondHandle, standbyEpoch, 10, 11)
	if !standbyOK || !standby.Armed || standby.Epoch != standbyEpoch ||
		standby.HasDeadline || standby.Deadline != 0 || second.ownerEpoch != 0 {
		t.Fatalf("empty fleet standby = (%+v, %t), owner=%d", standby, standbyOK, second.ownerEpoch)
	}
	wakeEpoch, timers, promoted, wakeOK := coroNativeFleetWakeOwnerAtV1(secondHandle, 12)
	if !wakeOK || wakeEpoch == 0 || timers != 0 || promoted != 0 ||
		!coroNativeFleetFinishOwnerEpochV1(secondHandle, wakeEpoch) {
		t.Fatalf("spurious fleet standby wake = (%d, %d, %d, %t)",
			wakeEpoch, timers, promoted, wakeOK)
	}

	// Drive one real retained fd wait through the ordinary-domain reducer,
	// fixed poll set, route-aware ingress, exact wake transaction, and typed
	// Poll V2 result cleanup. No helper goroutine owns scheduler state.
	reader, writer, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	defer reader.Close()
	defer writer.Close()
	if reader.Fd() > uintptr(^uint32(0)>>1) {
		t.Fatal("fleet poll test descriptor exceeds int32")
	}
	pollTask := newCoroNativeFleetTestTask(t, &second.p)
	pollTask.pollMode = true
	pollTask.pollFD = int32(reader.Fd())
	pollTask.pollDriver = &second.driver
	coroNativeFleetRunTestV1.Lock()
	coroNativeFleetRunTestV1.tasks[pollTask.handle] = pollTask
	coroNativeFleetRunTestV1.Unlock()
	pollEpoch, pollOwnerOK := coroNativeFleetBeginOwnerEpochV1(secondHandle)
	if !pollOwnerOK {
		t.Fatal("begin routed poll owner")
	}
	pollIdle := false
	for attempt := 0; attempt < 64; attempt++ {
		result := coroNativeFleetRunOwnerEpochV1(secondHandle, pollEpoch, 20, 8)
		if result.stop == coroRunIdleV1 {
			pollIdle = true
			break
		}
		if result.stop != coroRunSliceBudgetV1 {
			t.Fatalf("routed poll park attempt %d = %+v", attempt, result)
		}
	}
	if !pollIdle || !pollTask.pollPrepared || pollTask.pollFinished || pollTask.resumeCalls != 1 ||
		pollTask.runFailure != "" || pollTask.pollOperation.Route() != coro.RouteID(secondHandle.Route) {
		t.Fatalf("routed poll did not reach idle park: idle=%t task=%+v", pollIdle, pollTask)
	}
	pollPlan, pollPlanOK := coroNativeFleetPrepareOwnerWaitAtV1(secondHandle, pollEpoch, 21, 22)
	if !pollPlanOK || !pollPlan.Armed || pollPlan.Epoch != pollEpoch || pollPlan.HasDeadline ||
		pollPlan.Deadline != 0 || second.ownerEpoch != 0 {
		t.Fatalf("prepare routed poll retained wait = (%+v, %t), owner=%d", pollPlan, pollPlanOK, second.ownerEpoch)
	}
	armedPoll, armedPollOK := coroNativeFleetArmOwnerWaitV1(secondHandle, pollPlan)
	if !armedPollOK || armedPoll.Handle != secondHandle || armedPoll.Epoch != pollEpoch ||
		armedPoll.Count != 2 || armedPoll.HasDeadline || armedPoll.Deadline != 0 {
		t.Fatalf("arm routed poll set = (%+v, %t)", armedPoll, armedPollOK)
	}
	if written, err := writer.Write([]byte{1}); err != nil || written != 1 {
		t.Fatalf("publish routed poll readiness = (%d, %v)", written, err)
	}
	if pass := coroNativeFleetWaitOwnerPassAtV1(armedPoll, 23); pass != coroNativeFleetWaitPassWakeV1 {
		t.Fatalf("routed poll physical wait = %d", pass)
	}
	pollEpoch, timers, promoted, pollWakeOK := coroNativeFleetWakeOwnerAtV1(secondHandle, 24)
	if !pollWakeOK || pollEpoch == 0 || timers != 0 || promoted != 1 {
		t.Fatalf("wake routed poll owner = (%d, %d, %d, %t)",
			pollEpoch, timers, promoted, pollWakeOK)
	}
	pollComplete := false
	for attempt := 0; attempt < 64; attempt++ {
		result := coroNativeFleetRunOwnerEpochV1(secondHandle, pollEpoch, 25, 8)
		switch result.stop {
		case coroRunSliceBudgetV1:
			continue
		case coroRunDestroyCommitV1:
			completed, committed := coroNativeFleetCommitOwnerDestroyV1(
				secondHandle, pollEpoch, result.g, result.action,
			)
			if !committed || completed.Kind != coro.ActionComplete || completed.Handle != nil {
				t.Fatalf("commit routed poll task completion = (%+v, %t)", completed, committed)
			}
			pollComplete = true
		default:
			t.Fatalf("routed poll completion attempt %d = %+v", attempt, result)
		}
		if pollComplete {
			break
		}
	}
	if !pollComplete || !pollTask.pollFinished || !pollTask.complete || !pollTask.destroyed ||
		pollTask.resumeCalls != 2 || pollTask.runFailure != "" ||
		!coroNativeFleetEnterOwnerCompatibilityV1(secondHandle, pollEpoch) ||
		!coroNativeFleetFinishOwnerEpochV1(secondHandle, pollEpoch) {
		t.Fatalf("routed poll task did not finish cleanly: complete=%t task=%+v", pollComplete, pollTask)
	}
	firstFD, firstFDOK := first.doorbell.ReadFD()
	secondFD, secondFDOK := second.doorbell.ReadFD()
	if !firstFDOK || !secondFDOK || firstFD == secondFD {
		t.Fatalf("fleet doorbells = (%d, %t), (%d, %t)", firstFD, firstFDOK, secondFD, secondFDOK)
	}
	if !first.doorbell.Ring() {
		t.Fatal("ring first fleet doorbell")
	}
	firstWake, firstWakeOK := first.doorbell.ConsumeRetainedWake()
	secondWake, secondWakeOK := second.doorbell.ConsumeRetainedWake()
	if !firstWakeOK || !firstWake || !secondWakeOK || secondWake {
		t.Fatalf("fleet doorbell isolation = first(%t,%t) second(%t,%t)",
			firstWake, firstWakeOK, secondWake, secondWakeOK)
	}

	manual := [2]*coroNativeFleetTestOperation{
		reserveCoroNativeFleetManualV1(t, first, 701),
		reserveCoroNativeFleetManualV1(t, second, 702),
	}
	worker := [2]*coroNativeFleetTestOperation{
		reserveCoroNativeFleetWorkerV1(t, first, 711),
		reserveCoroNativeFleetWorkerV1(t, second, 712),
	}
	if manual[0].id.Route() != 1 || manual[1].id.Route() != 2 ||
		manual[0].id.LocalSlot() != manual[1].id.LocalSlot() ||
		manual[0].id.Generation != manual[1].id.Generation || manual[0].id == manual[1].id ||
		worker[0].id.Route() != 1 || worker[1].id.Route() != 2 ||
		worker[0].id.LocalSlot() != worker[1].id.LocalSlot() ||
		worker[0].id.Generation != worker[1].id.Generation || worker[0].id == worker[1].id {
		t.Fatalf("fleet operations alias: manual=%+v/%+v worker=%+v/%+v",
			manual[0].id, manual[1].id, worker[0].id, worker[1].id)
	}

	sources := [2]coro.P{}
	tasks := [2]*coroNativeFleetTestTask{
		newCoroNativeFleetTestTask(t, &sources[0]),
		newCoroNativeFleetTestTask(t, &sources[1]),
	}
	for index, handle := range [2]coro.ExecutorFleetHandle{firstHandle, secondHandle} {
		id, request, published := coroNativeFleetPublishPNeutralRunnableV1(handle, &sources[index], tasks[index].g)
		if !published || !id.Valid() || request != coro.ExecutorRequestPublished {
			t.Fatalf("publish fleet transfer %d = (%+v, %d, %t)", index, id, request, published)
		}
	}

	payload := [2]coro.ScalarResultPayloadV1{}
	for index := range payload {
		var ok bool
		payload[index], ok = coro.MakeScalarResultPayloadV1(
			coro.ScalarResultKindWords,
			0,
			3,
			uint64(index+1),
			uint64(index+11),
			uint64(index+21),
		)
		if !ok {
			t.Fatalf("make fleet worker payload %d", index)
		}
	}
	type postResult struct {
		index  int
		manual coro.OperationRouteIngressResult
		worker coro.OperationRouteIngressResult
	}
	posts := make(chan postResult, 2)
	var postGroup sync.WaitGroup
	postGroup.Add(2)
	for index := range manual {
		go func(index int) {
			defer postGroup.Done()
			posts <- postResult{
				index:  index,
				manual: coroNativeFleetPostManualV1(manual[index].id),
				worker: coroNativeFleetPostWorkerV1(worker[index].id, payload[index]),
			}
		}(index)
	}
	postGroup.Wait()
	close(posts)
	for result := range posts {
		if result.manual.Route != coro.OperationRoutePosted || result.manual.Executor != coro.ExecutorRequestCoalesced ||
			result.worker.Route != coro.OperationRoutePosted || result.worker.Executor != coro.ExecutorRequestCoalesced {
			t.Fatalf("fleet completion %d = manual %+v worker %+v", result.index, result.manual, result.worker)
		}
	}
	if !first.manual.Pending() || !second.manual.Pending() || !first.worker.Pending() || !second.worker.Pending() {
		t.Fatal("fleet completion crossed or lost a domain source")
	}

	controlID, controlOK := coro.MakeOperationIDAtRoute(coro.OperationSourceControl, 1, 1, 1)
	if !controlOK {
		t.Fatal("make fleet control probe")
	}
	if result := coroNativeFleetPostTaskControlV1(controlID, coro.TaskCancelAbort); result.Route != coro.OperationRoutePostSourceStale ||
		result.Executor != coro.ExecutorRequestInvalid || first.control.Pending() || second.control.Pending() {
		t.Fatalf("unregistered fleet control completion = %+v", result)
	}
	timerID, timerOK := coro.MakeOperationIDAtRoute(coro.OperationSourceTimer, 1, 1, 1)
	pollID, pollOK := coro.MakeOperationIDAtRoute(coro.OperationSourcePoll, 2, 2, 1)
	if !timerOK || !pollOK ||
		coroNativeFleetPostV1(
			timerID,
			coro.ScalarResultPayloadV1{},
			coro.TaskCancelNone,
			coro.PollOperationResultInvalid,
		) != coroNativeFleetInvalidIngressV1() {
		t.Fatal("native fleet exposed timer as a producer callback")
	}
	if result := coroNativeFleetPostPollV1(pollID, coro.PollOperationReady); result.Route != coro.OperationRoutePostSourceStale || result.Executor != coro.ExecutorRequestInvalid {
		t.Fatalf("unregistered routed fleet poll completion = %+v", result)
	}

	type ownerResult struct {
		index                              int
		moved                              uint32
		more                               bool
		drained, promoted                  int
		beginOK, drainOK                   bool
		sourceOK                           bool
		workerOwnerOK, wrongWorkerRejected bool
		pollOK, ownerFinishOK              bool
	}
	owners := make(chan ownerResult, 2)
	var ownerGroup sync.WaitGroup
	ownerGroup.Add(2)
	for index, handle := range [2]coro.ExecutorFleetHandle{firstHandle, secondHandle} {
		go func(index int, handle coro.ExecutorFleetHandle) {
			defer ownerGroup.Done()
			result := ownerResult{index: index}
			epoch, ok := coroNativeFleetBeginOwnerEpochV1(handle)
			result.beginOK = ok
			if ok {
				route := coro.RouteID(handle.Route)
				result.workerOwnerOK = coroNativeFleetWorkerSubmissionOwnerV1(handle.Executor, route)
				result.wrongWorkerRejected = !coroNativeFleetWorkerSubmissionOwnerV1(
					handle.Executor,
					coro.RouteID(handle.Route%uint32(coroNativeFleetDomainCapacityV1)+1),
				)
				result.moved, result.more, result.drainOK = coroNativeFleetDrainOwnerEpochV1(handle, epoch, 1)
				result.sourceOK = settleCoroNativeFleetStandaloneSourcesV1(
					&coroNativeFleetV1State.domains[index],
				)
				result.drained, result.promoted, result.pollOK = coroNativeFleetPollOwnerEpochV1(handle, epoch, 0)
				result.ownerFinishOK = coroNativeFleetFinishOwnerEpochV1(handle, epoch)
			}
			owners <- result
		}(index, handle)
	}
	ownerGroup.Wait()
	close(owners)
	for result := range owners {
		if !result.beginOK || !result.workerOwnerOK || !result.wrongWorkerRejected ||
			!result.drainOK || result.moved != 1 || result.more || !result.sourceOK ||
			!result.pollOK || result.drained != 0 || result.promoted != 0 || !result.ownerFinishOK {
			t.Fatalf("fleet owner %d = %+v", result.index, result)
		}
	}
	for index, domain := range [2]*coroNativeFleetDomainV1{first, second} {
		finishCoroNativeFleetManualV1(t, domain, manual[index])
		finishCoroNativeFleetWorkerV1(t, domain, worker[index], payload[index])
	}

	var sink coro.P
	var sinkMailbox coro.RunnableTransferMailbox
	if !coro.BindRunnableTransferMailbox(&sinkMailbox, &sink) {
		t.Fatal("bind fleet test sink mailbox")
	}
	coroNativeFleetRunTestV1.Lock()
	coroNativeFleetRunTestV1.tasks[tasks[0].handle] = tasks[0]
	coroNativeFleetRunTestV1.tasks[tasks[1].handle] = tasks[1]
	coroNativeFleetRunTestV1.Unlock()
	for index, handle := range [2]coro.ExecutorFleetHandle{firstHandle, secondHandle} {
		domain := &coroNativeFleetV1State.domains[index]
		published := false
		for attempt := 0; attempt < 32 && !published; attempt++ {
			epoch, beginOK := coroNativeFleetBeginOwnerEpochV1(handle)
			if !beginOK {
				t.Fatalf("begin fleet run owner %d attempt %d", index, attempt)
			}
			result := coroNativeFleetRunOwnerEpochV1(handle, epoch, 0, 8)
			coroNativeFleetRunTestV1.Lock()
			resumes, failure := tasks[index].resumeCalls, tasks[index].runFailure
			coroNativeFleetRunTestV1.Unlock()
			if failure != "" {
				t.Fatalf("fleet run owner %d failed: %s", index, failure)
			}
			if resumes != 0 {
				_, published = coro.PublishPNeutralRunnable(&sinkMailbox, &domain.p, tasks[index].g)
			}
			compatibilityOK := !published || coroNativeFleetEnterOwnerCompatibilityV1(handle, epoch)
			finishOK := coroNativeFleetFinishOwnerEpochV1(handle, epoch)
			if result.stop != coroRunSliceBudgetV1 || result.used != 8 || !compatibilityOK || !finishOK {
				t.Fatalf("fleet run owner %d attempt %d = %+v compatibility:%t finish:%t",
					index, attempt, result, compatibilityOK, finishOK)
			}
		}
		if !published {
			t.Fatalf("fleet run owner %d never reached a P-neutral yield", index)
		}
	}
	if moved, more, ok := coro.DrainPNeutralRunnables(&sinkMailbox, &sink, 2); !ok || moved != 2 || more {
		t.Fatalf("drain fleet test sink = (%d, %t, %t)", moved, more, ok)
	}

	// Hold one exact target callback before it reaches the already-retired core
	// route. Backend retirement must join this pre-route window before it closes
	// the first domain's pipe or retires its driver.
	if !first.ingress.Enter() || !coroNativeFleetBeginRouteCloseV1(firstHandle) ||
		!coroNativeFleetConfirmRouteCloseV1(firstHandle) {
		t.Fatal("begin first fleet route close with admitted target callback")
	}
	if coroNativeFleetRetireDriverV1(firstHandle) {
		t.Fatal("fleet driver retired before target backend join")
	}
	backendDone := make(chan bool, 1)
	go func() {
		backendDone <- coroNativeFleetRetireBackendV1(firstHandle)
	}()
	for attempt := 0; attempt < 32; attempt++ {
		stdruntime.Gosched()
	}
	select {
	case result := <-backendDone:
		t.Fatalf("fleet backend bypassed admitted callback join: %t", result)
	default:
	}
	if late := coroNativeFleetV1State.fleet.PostManualAndRequest(manual[0].id); late.Route != coro.OperationRoutePostClosed ||
		late.Executor != coro.ExecutorRequestInvalid {
		t.Fatalf("paused callback reached retired route: %+v", late)
	}
	if _, leaveOK := first.ingress.Leave(); !leaveOK {
		t.Fatal("leave admitted fleet target callback")
	}
	select {
	case result := <-backendDone:
		if !result {
			t.Fatal("retire first fleet backend after callback join")
		}
	case <-time.After(time.Second):
		t.Fatal("fleet backend join did not converge")
	}

	if !coroNativeFleetBeginRouteCloseV1(secondHandle) || !coroNativeFleetConfirmRouteCloseV1(secondHandle) {
		t.Fatal("strong-close second fleet route")
	}
	if late := coroNativeFleetPostManualV1(manual[1].id); late.Route != coro.OperationRoutePostClosed ||
		late.Executor != coro.ExecutorRequestInvalid {
		t.Fatalf("retired fleet route accepted late completion: %+v", late)
	}
	if !coroNativeFleetRetireBackendV1(secondHandle) {
		t.Fatal("retire second fleet backend")
	}
	for _, handle := range [2]coro.ExecutorFleetHandle{firstHandle, secondHandle} {
		if !coroNativeFleetRetireDriverV1(handle) {
			t.Fatalf("retire fleet driver %+v", handle)
		}
	}
	if late := coroNativeFleetPostManualV1(manual[1].id); late.Route != coro.OperationRoutePostClosed ||
		late.Executor != coro.ExecutorRequestClosed {
		t.Fatalf("retired fleet ingress accepted late completion: %+v", late)
	}
	if !coroNativeFleetAllRetiredV1() {
		t.Fatal("native production fleet retained resources")
	}
	if coroNativeFleetWorkerTransportReadyV1() ||
		coroNativeFleetWorkerSubmissionOwnerV1(firstHandle.Executor, coro.RouteID(firstHandle.Route)) {
		t.Fatal("retired native fleet retained worker transport authority")
	}
	for _, task := range tasks {
		_ = task.header
		_ = task.descriptor
		_ = task.memory
	}
}

func TestCoroNativeFleetStartFailureIsPermanentV1(t *testing.T) {
	var state coroNativeFleetStateV1
	// Force only the second fixed domain's preflight to reject. The first route
	// must be fully joined and retired, while the enclosing adapter remains a
	// fail-stop tombstone rather than appearing pristine/restartable.
	state.domains[1].lifecycle = coroNativeFleetDomainFailedV1
	if coroNativeFleetStartStateV1(&state) || state.lifecycle != coroNativeFleetFailedV1 ||
		state.domains[0].lifecycle != coroNativeFleetDomainFailedV1 ||
		!state.domains[0].ingress.Retired() || !state.domains[0].doorbell.Closed() ||
		state.domains[0].driver != (coro.ExecutorDriver{}) || !state.fleet.AllRetired() {
		t.Fatal("partial native fleet start did not fail-stop and strongly retire route 1")
	}
	if coroNativeFleetStartStateV1(&state) {
		t.Fatal("failed native fleet was restartable")
	}
}

func TestCoroNativeFleetAdoptsBoundProgramStorageV1(t *testing.T) {
	var state coroNativeFleetStateV1
	var programP coro.P
	var programDriver coro.ExecutorDriver
	var programRegistry coro.ExecutorRegistry
	var programTimers coro.TimerRegistrationTable
	var programPoll coro.PollOperationSource
	var programManual coro.ManualOperationSource
	var programWorker coro.WorkerOperationSource
	var programChannel coro.ChannelOperationSource
	var programControl coro.TaskControlSource

	executor, registered := programRegistry.Register()
	if !registered || !coro.BindExecutorSourceCatalogAtRoute(
		&programDriver,
		&programP,
		&programRegistry,
		executor,
		1,
		coro.ExecutorSourceCatalog{
			Timers:  &programTimers,
			Poll:    &programPoll,
			Manual:  &programManual,
			Worker:  &programWorker,
			Channel: &programChannel,
			Control: &programControl,
		},
	) {
		t.Fatal("bind program-like executor before native fleet start")
	}
	owners := coroNativeFleetDomainOwnersV1{
		p:      &programP,
		driver: &programDriver,
		sources: coro.ExecutorSourceCatalog{
			Timers:  &programTimers,
			Poll:    &programPoll,
			Manual:  &programManual,
			Worker:  &programWorker,
			Channel: &programChannel,
			Control: &programControl,
		},
	}
	if !coroNativeFleetStartDomainsV1(&state, &owners) {
		t.Fatal("start native fleet around bound program executor")
	}
	program := &state.domains[0]
	peer := &state.domains[1]
	if !program.adopted || program.handle.Route != 1 || program.handle.Executor != executor ||
		program.pOwnerV1() != &programP || program.driverOwnerV1() != &programDriver ||
		program.pollOwnerV1() != &programPoll || program.workerOwnerV1() != &programWorker ||
		peer.adopted || peer.handle.Route != 2 || peer.pOwnerV1() != &peer.p ||
		peer.driverOwnerV1() != &peer.driver {
		t.Fatalf("native adopted/owned domains = program:%+v peer:%+v", program.handle, peer.handle)
	}
	if got := coro.TimerRegistrationConfiguredCapacity(&peer.timers); got !=
		coroNativeTimerPageCountV1*coro.TimerRegistrationPageCapacity {
		t.Fatalf("owned peer timer capacity = %d", got)
	}
	if got := coro.PollOperationConfiguredCapacity(&peer.poll); got !=
		coroNativeSourcePageCountV1*coro.PollOperationPageCapacity {
		t.Fatalf("owned peer poll capacity = %d", got)
	}
	if got := coro.ManualOperationConfiguredCapacity(&peer.manual); got !=
		coroNativeSourcePageCountV1*coro.ManualOperationPageCapacity {
		t.Fatalf("owned peer manual capacity = %d", got)
	}
	if got := coro.WorkerOperationConfiguredCapacity(&peer.worker); got !=
		coroNativeSourcePageCountV1*coro.WorkerOperationPageCapacity {
		t.Fatalf("owned peer worker capacity = %d", got)
	}
	if got := coro.ChannelOperationConfiguredCapacity(&peer.channel); got !=
		coroNativeSourcePageCountV1*coro.ChannelOperationPageCapacity {
		t.Fatalf("owned peer channel capacity = %d", got)
	}
	if !coroNativeFleetAbortActiveDomainV1(&state, peer) ||
		!coroNativeFleetAbortActiveDomainV1(&state, program) {
		t.Fatal("strongly retire adopted native fleet fixture")
	}
	if program.owners != (coroNativeFleetDomainOwnersV1{}) ||
		!programRegistry.CanRelease() || !state.fleet.AllRetired() {
		t.Fatal("adopted native fleet retained external program storage")
	}
}
