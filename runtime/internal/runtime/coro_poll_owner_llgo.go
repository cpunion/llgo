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
	context   uintptr
	interest  coro.PollInterest
	deadline  int64
	wait      coro.WaitSetRecord
	packet    coro.ResumePacket
	ticket    coro.ParkTicket
	poll      coro.PollOperationHandle
	operation coro.OperationID
	executor  coro.ExecutorHandle
}

func validCoroPollParkV2(state *CoroPollParkV2) bool {
	return state != nil && state.magic == coroPollParkMagicV2 && state.ticket.Valid() &&
		state.context != 0 &&
		(state.interest == coro.PollInterestRead || state.interest == coro.PollInterestWrite) &&
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
	context uintptr,
	fd int32,
	interest uint32,
	deadline int64,
) {
	state := (*CoroPollParkV2)(storage)
	pollInterest := coro.PollInterest(interest)
	if g == nil || handle == nil || header == nil || state == nil || context == 0 ||
		(pollInterest != coro.PollInterestRead && pollInterest != coro.PollInterestWrite) ||
		*state != (CoroPollParkV2{}) {
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
		pollInterest,
		deadline,
	)
	if !prepared || executor != wantExecutor || operation.Route() != wantRoute {
		coroPollAbortV2("cannot prepare coroutine Poll V2 park")
		return
	}
	state.magic = coroPollParkMagicV2
	state.context = context
	state.interest = pollInterest
	state.deadline = deadline
	state.ticket = ticket
	state.poll = poll
	state.operation = operation
	state.executor = executor
	if !coro.BindSingleWaitSetResumePacket(&state.wait, &state.packet, operation) {
		coroPollAbortV2("cannot bind coroutine Poll V2 resume packet")
		return
	}
	closing, published := coroPollDescPublishOperationV1(context, pollInterest, operation)
	if !published {
		coroPollAbortV2("cannot publish coroutine Poll V2 descriptor operation")
		return
	}
	if closing {
		result := coroProgramPostPollEventV2(operation, coro.PollOperationClosing)
		if result != coro.PollOperationPosted && result != coro.PollOperationPostDuplicate {
			coroPollAbortV2("cannot post coroutine Poll V2 closing handshake")
			return
		}
		return
	}
	currentDeadline, deadlineOK := coroPollDescDeadlineV1(context, pollInterest)
	if !deadlineOK {
		coroPollAbortV2("cannot reload coroutine Poll V2 descriptor deadline")
		return
	}
	if currentDeadline != deadline {
		result := coroProgramPostPollEventV2(operation, coro.PollOperationReady)
		if result != coro.PollOperationPosted && result != coro.PollOperationPostDuplicate {
			coroPollAbortV2("cannot post coroutine Poll V2 deadline handshake")
			return
		}
	}
}

// __llgo_coro_poll_resume_v2 consumes the frame-local result packet. The old
// owner already copied or discarded the Poll result and recycled the exact
// source generation. Descriptor cleanup below uses only the retained opaque
// scalar context and remains valid after a P-neutral transfer.
//
//export __llgo_coro_poll_resume_v2
func __llgo_coro_poll_resume_v2(g, storage unsafe.Pointer) uint32 {
	state := (*CoroPollParkV2)(storage)
	if g == nil || !validCoroPollParkV2(state) {
		coroPollAbortV2("invalid coroutine Poll V2 resume ABI")
		return 0
	}
	task := (*coro.G)(g)
	outcome, caseID, cancel, resultKind, small, taken := coro.TakeResumePacket(
		task,
		state.ticket,
		&state.packet,
		nil,
	)
	if !taken {
		coroPollAbortV2("invalid coroutine Poll V2 run decision")
		return 0
	}
	status := uint32(0)
	result := coro.PollOperationResult(small)
	switch {
	case outcome == coro.ParkOutcomeCompleted && caseID == 1 && cancel == coro.TaskCancelNone &&
		resultKind == coro.ResumeResultPoll && result != coro.PollOperationResultInvalid:
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelNone &&
		resultKind == coro.ResumeResultNone && small == coro.ResumeSmallInvalid:
		status = coroPollResumeOperationCanceledV2
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelAbort &&
		resultKind == coro.ResumeResultNone && small == coro.ResumeSmallInvalid:
		status = coroPollResumeTaskAbortV2
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelShutdown &&
		resultKind == coro.ResumeResultNone && small == coro.ResumeSmallInvalid:
		status = coroPollResumeShutdownV2
	default:
		coroPollAbortV2("unsupported coroutine Poll V2 run decision")
		return 0
	}
	if !coroPollDescClearOperationV1(state.context, state.interest, state.operation) {
		coroPollAbortV2("cannot clear coroutine Poll V2 descriptor operation")
		return 0
	}
	if outcome == coro.ParkOutcomeCompleted {
		var mapped bool
		status, mapped = coroPollResumeStatusV2(state, result)
		if !mapped {
			coroPollAbortV2("invalid coroutine Poll V2 result")
			return 0
		}
	}
	*state = CoroPollParkV2{}
	return status
}

// coroPollResumeStatusV2 distinguishes a real readiness result from the
// Ready wake used by runtime_pollSetDeadline to make an owner re-register.
// Comparing the descriptor's current scalar deadline with the value retained
// in the coroutine frame closes the cross-P update race without sharing Go
// pointers or adding another producer mailbox kind. Timeout is the existing
// runtime_pollWait recheck path: it returns a timeout only when the current
// descriptor deadline is still expired, otherwise it retries registration.
func coroPollResumeStatusV2(state *CoroPollParkV2, result coro.PollOperationResult) (uint32, bool) {
	if state == nil {
		return 0, false
	}
	switch result {
	case coro.PollOperationReady:
		currentDeadline, ok := coroPollDescDeadlineV1(state.context, state.interest)
		if !ok {
			return 0, false
		}
		if currentDeadline != state.deadline {
			return coroPollResumeTimeoutV2, true
		}
		return coroPollResumeReadyV2, true
	case coro.PollOperationClosing:
		return coroPollResumeClosingV2, true
	case coro.PollOperationTimeout:
		return coroPollResumeTimeoutV2, true
	default:
		return 0, false
	}
}

// coroProgramPostPollEventV2 is the common exact-operation ingress. A fleet
// OperationID is routed through the strong-joined target registry; the legacy
// single-P target retains its direct owner-return path. In either profile the
// durable source result is published before its exact executor is requested.
func coroProgramPostPollEventV2(
	id coro.OperationID,
	status coro.PollOperationResult,
) coro.PollOperationPostResult {
	if !id.Valid() || id.Source() != coro.OperationSourcePoll ||
		(status != coro.PollOperationReady && status != coro.PollOperationClosing) {
		return coro.PollOperationPostInvalid
	}
	return coroTargetPostPollOperationV2(id, status)
}

// __llgo_coro_poll_post_event_v2 imports one POD OperationID plus a scalar
// status. It is used both by retained reactors and descriptor close/deadline
// handshakes; route is the complete destination identity in a native fleet.
//
//export __llgo_coro_poll_post_event_v2
func __llgo_coro_poll_post_event_v2(sourceSlot, generation, status uint32) uint32 {
	id := coro.OperationID{SourceSlot: sourceSlot, Generation: generation}
	return uint32(coroProgramPostPollEventV2(id, coro.PollOperationResult(status)))
}

func coroProgramUpdatePollDeadlineV1(context uintptr, interest coro.PollInterest, deadline int64) coro.PollOperationUpdateResult {
	operation, ok := coroPollDescLoadOperationV1(context, interest)
	if !ok {
		return coro.PollOperationUpdateInvalid
	}
	if !operation.Valid() {
		return coro.PollOperationUpdateClosed
	}
	_ = deadline
	switch coroProgramPostPollEventV2(operation, coro.PollOperationReady) {
	case coro.PollOperationPosted, coro.PollOperationPostDuplicate:
		return coro.PollOperationUpdated
	case coro.PollOperationPostClosed:
		return coro.PollOperationUpdateClosed
	case coro.PollOperationPostStale:
		return coro.PollOperationUpdateStale
	default:
		return coro.PollOperationUpdateInvalid
	}
}

func coroProgramPostPollClosingV1(context uintptr, interest coro.PollInterest) coro.PollOperationPostResult {
	operation, ok := coroPollDescLoadOperationV1(context, interest)
	if !ok {
		return coro.PollOperationPostInvalid
	}
	if !operation.Valid() {
		return coro.PollOperationPostClosed
	}
	return coroProgramPostPollEventV2(operation, coro.PollOperationClosing)
}

//export __llgo_coro_poll_update_deadline_or_abort_v1
func __llgo_coro_poll_update_deadline_or_abort_v1(context uintptr, interest uint32, deadline int64) {
	switch coroProgramUpdatePollDeadlineV1(
		context,
		coro.PollInterest(interest),
		deadline,
	) {
	case coro.PollOperationUpdated, coro.PollOperationUpdateClosed, coro.PollOperationUpdateStale:
		return
	default:
		coroRuntimeAbort("coroutine poll deadline update failed")
		for {
		}
	}
}

//export __llgo_coro_poll_post_closing_or_abort_v1
func __llgo_coro_poll_post_closing_or_abort_v1(context uintptr, interest uint32) {
	switch coroProgramPostPollClosingV1(context, coro.PollInterest(interest)) {
	case coro.PollOperationPosted, coro.PollOperationPostDuplicate,
		coro.PollOperationPostClosed, coro.PollOperationPostStale:
		return
	default:
		coroRuntimeAbort("coroutine poll closing post failed")
		for {
		}
	}
}
