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
	stdruntime "runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

type coroNativeFleetTestTask struct {
	g          *coro.G
	handle     unsafe.Pointer
	header     *coro.HeaderV1
	descriptor *coro.FrameDescriptorV1
	memory     []uintptr
}

func newCoroNativeFleetTestTask(t *testing.T, source *coro.P) *coroNativeFleetTestTask {
	t.Helper()
	task := &coroNativeFleetTestTask{
		g:          new(coro.G),
		handle:     unsafe.Pointer(new(byte)),
		descriptor: &coro.FrameDescriptorV1{Version: 1, ResultAlign: 1},
	}
	if !coro.InitG(task.g) {
		t.Fatal("initialize fleet transfer task")
	}
	const frameSize = uintptr(64)
	align := unsafe.Alignof(uintptr(0))
	total, ok := coro.FrameAllocationSize(frameSize, align)
	if !ok {
		t.Fatal("compute fleet transfer frame allocation")
	}
	task.memory = make([]uintptr, (total+unsafe.Sizeof(uintptr(0))-1)/unsafe.Sizeof(uintptr(0)))
	raw := unsafe.Pointer(&task.memory[0])
	storage, ok := coro.RegisterFrame(task.g, raw, total, frameSize, align, unsafe.Pointer(task.descriptor))
	if !ok {
		t.Fatal("register fleet transfer frame")
	}
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
	first := &coroNativeFleetV1State.domains[0]
	second := &coroNativeFleetV1State.domains[1]
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
	pollID, pollOK := coro.MakeOperationIDAtRoute(coro.OperationSourcePoll, 2, 1, 1)
	if !timerOK || !pollOK ||
		coroNativeFleetPostV1(timerID, coro.ScalarResultPayloadV1{}, coro.TaskCancelNone) != coroNativeFleetInvalidIngressV1() ||
		coroNativeFleetPostV1(pollID, coro.ScalarResultPayloadV1{}, coro.TaskCancelNone) != coroNativeFleetInvalidIngressV1() {
		t.Fatal("native fleet accepted timer/poll before route support")
	}

	type ownerResult struct {
		index                 int
		moved                 uint32
		more                  bool
		drained, promoted     int
		beginOK, drainOK      bool
		sourceOK              bool
		pollOK, ownerFinishOK bool
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
				result.moved, result.more, result.drainOK = coroNativeFleetDrainOwnerEpochV1(handle, epoch, 1)
				result.sourceOK = settleCoroNativeFleetStandaloneSourcesV1(
					&coroNativeFleetV1State.domains[index],
				)
				result.drained, result.promoted, result.pollOK = coroNativeFleetPollOwnerEpochV1(handle, epoch)
				result.ownerFinishOK = coroNativeFleetFinishOwnerEpochV1(handle, epoch)
			}
			owners <- result
		}(index, handle)
	}
	ownerGroup.Wait()
	close(owners)
	for result := range owners {
		if !result.beginOK || !result.drainOK || result.moved != 1 || result.more || !result.sourceOK ||
			!result.pollOK || result.drained != 0 || result.promoted != 0 || !result.ownerFinishOK {
			t.Fatalf("fleet owner %d = %+v", result.index, result)
		}
	}
	for index, domain := range [2]*coroNativeFleetDomainV1{first, second} {
		if runnable, ok := coro.NextRunnable(&domain.p); !ok || runnable != tasks[index].g {
			t.Fatalf("fleet domain %d runnable = (%p, %t), want %p", index, runnable, ok, tasks[index].g)
		} else if !coro.Enqueue(&domain.p, runnable) {
			t.Fatalf("restore fleet domain %d runnable", index)
		}
		finishCoroNativeFleetManualV1(t, domain, manual[index])
		finishCoroNativeFleetWorkerV1(t, domain, worker[index], payload[index])
	}

	var sink coro.P
	var sinkMailbox coro.RunnableTransferMailbox
	if !coro.BindRunnableTransferMailbox(&sinkMailbox, &sink) {
		t.Fatal("bind fleet test sink mailbox")
	}
	for index, domain := range [2]*coroNativeFleetDomainV1{first, second} {
		if _, ok := coro.PublishPNeutralRunnable(&sinkMailbox, &domain.p, tasks[index].g); !ok {
			t.Fatalf("release fleet domain %d runnable", index)
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
