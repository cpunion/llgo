//go:build (llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test) || coro_poll_owner_test

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
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

const coroPollParkMagicV2 uint32 = 0x43504c32 // "CPL2"

const (
	coroPollResumeReadyV2 uint32 = iota + 1
	coroPollResumeClosingV2
	coroPollResumeTimeoutV2
	coroPollResumeOperationCanceledV2
	coroPollResumeTaskAbortV2
	coroPollResumeShutdownV2
)

// CoroPollParkV2 is opaque compiler-owned current-frame storage. The retained
// reactor receives only operation's two uint32 words; it never receives this
// address, wait, G, frame header, or executor/source pointer.
type CoroPollParkV2 struct {
	magic     uint32
	wait      coro.WaitSetRecord
	ticket    coro.ParkTicket
	poll      coro.PollOperationHandle
	operation coro.OperationID
	executor  coro.ExecutorHandle
}

func validCoroPollParkV2(state *CoroPollParkV2) bool {
	return state != nil && state.magic == coroPollParkMagicV2 && state.ticket.Valid() &&
		state.poll.Valid() && state.operation.Valid() &&
		state.operation.Source() == coro.OperationSourcePoll &&
		state.operation.LocalSlot() == state.poll.Slot &&
		state.operation.Generation == state.poll.Generation &&
		state.executor.Slot != 0 && state.executor.Generation != 0
}

func coroPollAbortV2(message string) {
	coroRuntimeAbort(message)
	for {
	}
}

// __llgo_coro_poll_park_v2 installs one source-aware fd-direction wait. The
// compiler calls it after publishing SuspendPark/FrameSuspended and then
// immediately executes llvm.coro.suspend. deadline is absolute monotonic time;
// zero disables the operation deadline.
//
//export __llgo_coro_poll_park_v2
func __llgo_coro_poll_park_v2(
	g, handle, header, storage unsafe.Pointer,
	fd int32,
	interest uint32,
	deadline int64,
) {
	state := (*CoroPollParkV2)(storage)
	if g == nil || handle == nil || header == nil || state == nil || *state != (CoroPollParkV2{}) {
		coroPollAbortV2("invalid coroutine Poll V2 park ABI")
		return
	}
	task := (*coro.G)(g)
	driver, wantExecutor, wantRoute, ok := coro.CurrentExecutorPollDriver(task)
	if !ok {
		coroPollAbortV2("cannot resolve coroutine Poll V2 owner")
		return
	}
	ticket, poll, operation, executor, prepared := coro.PrepareCurrentExecutorPollPark(
		driver,
		task,
		handle,
		(*coro.HeaderV1)(header),
		&state.wait,
		1,
		1,
		fd,
		coro.PollInterest(interest),
		deadline,
	)
	if !prepared || executor != wantExecutor || operation.Route() != wantRoute {
		coroPollAbortV2("cannot prepare coroutine Poll V2 park")
		return
	}
	state.magic = coroPollParkMagicV2
	state.ticket = ticket
	state.poll = poll
	state.operation = operation
	state.executor = executor
}

// __llgo_coro_poll_resume_v2 consumes the exact decision and releases the
// winner result (or discarded late winner) before recycling source storage.
//
//export __llgo_coro_poll_resume_v2
func __llgo_coro_poll_resume_v2(g, storage unsafe.Pointer) uint32 {
	state := (*CoroPollParkV2)(storage)
	if g == nil || !validCoroPollParkV2(state) {
		coroPollAbortV2("invalid coroutine Poll V2 resume ABI")
		return 0
	}
	task := (*coro.G)(g)
	outcome, caseID, lease, cancel, taken := coro.TakeRunDecision(task, state.ticket)
	if !taken {
		coroPollAbortV2("invalid coroutine Poll V2 run decision")
		return 0
	}
	driver, executor, route, ok := coro.CurrentExecutorPollDriver(task)
	if !ok || executor != state.executor || route != state.operation.Route() {
		coroPollAbortV2("coroutine Poll V2 resumed on the wrong source")
		return 0
	}
	discard := outcome == coro.ParkOutcomeCanceled
	status := uint32(0)
	switch {
	case outcome == coro.ParkOutcomeCompleted && caseID == 1 && lease.Valid() && cancel == coro.TaskCancelNone:
		discard = false
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelNone && !lease.Valid():
		status = coroPollResumeOperationCanceledV2
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelAbort:
		status = coroPollResumeTaskAbortV2
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelShutdown:
		status = coroPollResumeShutdownV2
	default:
		coroPollAbortV2("unsupported coroutine Poll V2 run decision")
		return 0
	}
	result, finished := coro.FinishCurrentExecutorPollPark(
		driver,
		task,
		state.executor,
		state.poll,
		state.operation,
		lease,
		discard,
	)
	if !finished {
		coroPollAbortV2("cannot retire coroutine Poll V2 source")
		return 0
	}
	if outcome == coro.ParkOutcomeCompleted {
		switch result {
		case coro.PollOperationReady:
			status = coroPollResumeReadyV2
		case coro.PollOperationClosing:
			status = coroPollResumeClosingV2
		case coro.PollOperationTimeout:
			status = coroPollResumeTimeoutV2
		default:
			coroPollAbortV2("invalid coroutine Poll V2 result")
			return 0
		}
	}
	*state = CoroPollParkV2{}
	return status
}

// __llgo_coro_poll_post_event_v2 is the singleton retained-reactor owner-return
// import ABI. Its input is exactly one POD OperationID plus a scalar status.
// It is not a foreign-thread ingress: this first implementation reads the
// statically retained driver/handle and is called only after the single
// executor's target wait returns on its owner. A future callback or multi-P
// reactor must instead resolve a route through a stable ingress registry and
// retain one lease across both source publication and executor request.
//
// The owner-return path rings the current executor doorbell only after durable
// exact-generation posting; a wrong route, stale generation, or inactive
// executor fails closed.
//
//export __llgo_coro_poll_post_event_v2
func __llgo_coro_poll_post_event_v2(sourceSlot, generation, status uint32) uint32 {
	if !coroProgramExecutorBoundV1State || coroProgramExecutorHandleV1State == (coro.ExecutorHandle{}) {
		return uint32(coro.PollOperationPostInvalid)
	}
	id := coro.OperationID{SourceSlot: sourceSlot, Generation: generation}
	result := coro.PostExecutorPollEvent(
		&coroProgramExecutorDriverV1State,
		id,
		coro.PollOperationResult(status),
	)
	if result == coro.PollOperationPosted && !coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State) {
		return uint32(coro.PollOperationPostInvalid)
	}
	return uint32(result)
}

func coroProgramFindActivePollSnapshotV2(fd int32, interest coro.PollInterest) (coro.PollOperationSnapshot, bool, bool) {
	if !coroProgramExecutorBoundV1State || fd < 0 ||
		(interest != coro.PollInterestRead && interest != coro.PollInterestWrite) {
		return coro.PollOperationSnapshot{}, false, false
	}
	var found coro.PollOperationSnapshot
	scanLimit, scanOK := coro.PollOperationScanLimit(&coroProgramPollSourceV1State)
	if !scanOK {
		return coro.PollOperationSnapshot{}, false, false
	}
	for index := uint32(0); index < scanLimit; index++ {
		snapshot, active, ok := coro.SnapshotExecutorPollOperation(&coroProgramExecutorDriverV1State, index)
		if !ok {
			return coro.PollOperationSnapshot{}, false, false
		}
		if !active || snapshot.FD != fd || snapshot.Interest != interest {
			continue
		}
		if found.ID.Valid() {
			return coro.PollOperationSnapshot{}, false, false
		}
		found = snapshot
	}
	return found, found.ID.Valid(), true
}

func coroProgramUpdatePollDeadlineV1(fd int32, interest coro.PollInterest, deadline int64) coro.PollOperationUpdateResult {
	snapshot, found, ok := coroProgramFindActivePollSnapshotV2(fd, interest)
	if !ok {
		return coro.PollOperationUpdateInvalid
	}
	if !found {
		return coro.PollOperationUpdateClosed
	}
	result := coro.UpdateExecutorPollDeadlineExact(&coroProgramExecutorDriverV1State, snapshot.ID, deadline)
	if result == coro.PollOperationUpdated && !coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State) {
		return coro.PollOperationUpdateInvalid
	}
	return result
}

func coroProgramPostPollClosingV1(fd int32, interest coro.PollInterest) coro.PollOperationPostResult {
	snapshot, found, ok := coroProgramFindActivePollSnapshotV2(fd, interest)
	if !ok {
		return coro.PollOperationPostInvalid
	}
	if !found {
		return coro.PollOperationPostClosed
	}
	result := coro.PostExecutorPollEvent(
		&coroProgramExecutorDriverV1State,
		snapshot.ID,
		coro.PollOperationClosing,
	)
	if result == coro.PollOperationPosted && !coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State) {
		return coro.PollOperationPostInvalid
	}
	return result
}

//export __llgo_coro_poll_update_deadline_or_abort_v1
func __llgo_coro_poll_update_deadline_or_abort_v1(fd int32, interest uint32, deadline int64) {
	switch coroProgramUpdatePollDeadlineV1(
		fd,
		coro.PollInterest(interest),
		deadline,
	) {
	case coro.PollOperationUpdated, coro.PollOperationUpdateClosed:
		return
	default:
		coroRuntimeAbort("coroutine poll deadline update failed")
		for {
		}
	}
}

//export __llgo_coro_poll_post_closing_or_abort_v1
func __llgo_coro_poll_post_closing_or_abort_v1(fd int32, interest uint32) {
	switch coroProgramPostPollClosingV1(fd, coro.PollInterest(interest)) {
	case coro.PollOperationPosted, coro.PollOperationPostDuplicate, coro.PollOperationPostClosed:
		return
	default:
		coroRuntimeAbort("coroutine poll closing post failed")
		for {
		}
	}
}
