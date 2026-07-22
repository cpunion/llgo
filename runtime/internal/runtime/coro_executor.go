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

// The first production runner owns one statically addressed executor domain.
// Platform callback ABIs retain only POD operation and executor identities;
// none of these Go objects or their addresses crosses a target callback
// boundary.
var (
	coroProgramExecutorRegistryV1State  coro.ExecutorRegistry
	coroProgramTimerTableV1State        coro.TimerRegistrationTable
	coroProgramPollSourceV1State        coro.PollOperationSource
	coroProgramManualSourceV2State      coro.ManualOperationSource
	coroProgramWorkerSourceV1State      coro.WorkerOperationSource
	coroProgramChannelSourceV1State     coro.ChannelOperationSource
	coroProgramTaskControlSourceV1State coro.TaskControlSource
	coroProgramExecutorDriverV1State    coro.ExecutorDriver
	coroProgramExecutorHandleV1State    coro.ExecutorHandle
	coroProgramExecutorBoundV1State     bool
)

// coroTaskControlTokenV1 is the complete foreign-producer capability. The
// operation words identify an exact source route, local slot, and generation;
// the executor words identify the exact request/doorbell generation. It
// intentionally contains no G, P, frame, source, or driver pointer.
type coroTaskControlTokenV1 struct {
	SourceSlot         uint32
	SourceGeneration   uint32
	ExecutorSlot       uint32
	ExecutorGeneration uint32
}

var (
	_ [16 - unsafe.Sizeof(coroTaskControlTokenV1{})]byte
	_ [unsafe.Sizeof(coroTaskControlTokenV1{}) - 16]byte
	_ [4 - unsafe.Alignof(coroTaskControlTokenV1{})]byte
	_ [unsafe.Alignof(coroTaskControlTokenV1{}) - 4]byte
)

func makeCoroTaskControlTokenV1(id coro.OperationID, executor coro.ExecutorHandle) (coroTaskControlTokenV1, bool) {
	if !id.Valid() || id.Source() != coro.OperationSourceControl || executor.Slot == 0 || executor.Generation == 0 {
		return coroTaskControlTokenV1{}, false
	}
	return coroTaskControlTokenV1{
		SourceSlot:         id.SourceSlot,
		SourceGeneration:   id.Generation,
		ExecutorSlot:       executor.Slot,
		ExecutorGeneration: executor.Generation,
	}, true
}

func (token coroTaskControlTokenV1) endpoint() (coro.OperationID, coro.ExecutorHandle, bool) {
	id := coro.OperationID{SourceSlot: token.SourceSlot, Generation: token.SourceGeneration}
	executor := coro.ExecutorHandle{Slot: token.ExecutorSlot, Generation: token.ExecutorGeneration}
	if !id.Valid() || id.Source() != coro.OperationSourceControl || executor.Slot == 0 || executor.Generation == 0 {
		return coro.OperationID{}, coro.ExecutorHandle{}, false
	}
	return id, executor, true
}

type coroTargetDispatchResultV1 uint8

const (
	coroTargetDispatchInvalidV1 coroTargetDispatchResultV1 = iota
	coroTargetDispatchCompleteV1
	coroTargetDispatchPendingV1
)

// A host-run request is executor-scoped scheduling state, not an operation or
// G continuation. Inline means the target does not self-post: the caller first
// returns across the V2 program ABI and its fixed-stack host loop re-enters with
// the tuple. Queued means a future host turn remains durable when Begin returns.
// An early queued callback must handle Repost by arranging that same tuple
// again after the current ABI call; every callback also treats Ignored as a
// settled stale/duplicate turn. No result permits recursive scheduler entry.
type coroTargetRunRequestResultV2 uint8

const (
	coroTargetRunRequestInvalidV2 coroTargetRunRequestResultV2 = iota
	coroTargetRunRequestInlineV2
	coroTargetRunRequestQueuedV2
)

func coroProgramBindExecutorV1() bool {
	if coroProgramExecutorBoundV1State ||
		coroProgramExecutorHandleV1State != (coro.ExecutorHandle{}) ||
		coroProgramExecutorDriverV1State != (coro.ExecutorDriver{}) ||
		!coroProgramExecutorRegistryV1State.CanRelease() ||
		!coroProgramTimerTableV1State.CanRelease() ||
		!coroProgramPollSourceV1State.CanRelease() ||
		!coroProgramManualSourceV2State.CanRelease() ||
		!coroProgramWorkerSourceV1State.CanRelease() ||
		!coroProgramChannelSourceV1State.CanRelease() ||
		!coroProgramTaskControlSourceV1State.CanRelease() {
		return false
	}
	handle, ok := coroProgramExecutorRegistryV1State.Register()
	if !ok || !coroProgramBindExecutorDriverV1(
		&coroProgramExecutorDriverV1State,
		&coroProgramPV1State,
		&coroProgramExecutorRegistryV1State,
		handle,
	) || !coroProgramDriveAdmissionV1State.PublishExecutor(handle.Slot, handle.Generation) {
		return false
	}
	coroProgramExecutorHandleV1State = handle
	coroProgramExecutorBoundV1State = true
	return true
}

func coroProgramExecutorRetiredV1() bool {
	if !coroProgramExecutorBoundV1State ||
		coroProgramExecutorHandleV1State == (coro.ExecutorHandle{}) ||
		coroProgramExecutorDriverV1State != (coro.ExecutorDriver{}) ||
		!coroProgramExecutorRegistryV1State.CanRelease() ||
		!coroProgramTimerTableV1State.CanRelease() ||
		!coroProgramPollSourceV1State.CanRelease() ||
		!coroProgramManualSourceV2State.CanRelease() ||
		!coroProgramWorkerSourceV1State.CanRelease() ||
		!coroProgramChannelSourceV1State.CanRelease() ||
		!coroProgramTaskControlSourceV1State.CanRelease() {
		return false
	}
	if !coroTargetExecutorRetiredV1(coroProgramExecutorHandleV1State) {
		return false
	}
	coroProgramExecutorBoundV1State = false
	coroProgramExecutorHandleV1State = coro.ExecutorHandle{}
	return true
}

func coroProgramRegisterTaskControlV1(task *coroG) (coroTaskControlTokenV1, bool) {
	if !coroProgramExecutorBoundV1State || task == nil ||
		coroProgramExecutorHandleV1State == (coro.ExecutorHandle{}) {
		return coroTaskControlTokenV1{}, false
	}
	id, ok := coro.RegisterCurrentExecutorTaskControl(&coroProgramExecutorDriverV1State, task)
	if !ok {
		return coroTaskControlTokenV1{}, false
	}
	return makeCoroTaskControlTokenV1(id, coroProgramExecutorHandleV1State)
}

func coroProgramTaskControlEndpointV1(token coroTaskControlTokenV1) (coro.OperationID, coro.ExecutorHandle, bool) {
	id, executor, ok := token.endpoint()
	if !ok || !coroProgramExecutorBoundV1State || executor != coroProgramExecutorHandleV1State {
		return coro.OperationID{}, coro.ExecutorHandle{}, false
	}
	route, routed := coroProgramTaskControlSourceV1State.Route()
	if !routed || id.Route() != route {
		return coro.OperationID{}, coro.ExecutorHandle{}, false
	}
	return id, executor, true
}

func coroProgramBeginCloseTaskControlV1(task *coroG, token coroTaskControlTokenV1) bool {
	id, _, ok := coroProgramTaskControlEndpointV1(token)
	return ok && coro.BeginCloseCurrentExecutorTaskControl(&coroProgramExecutorDriverV1State, task, id)
}

func coroProgramFinishCloseTaskControlV1(task *coroG, token coroTaskControlTokenV1) bool {
	id, _, ok := coroProgramTaskControlEndpointV1(token)
	return ok && coro.FinishCloseCurrentExecutorTaskControl(&coroProgramExecutorDriverV1State, task, id)
}

func validCoroTaskControlOutputWordsV1(task unsafe.Pointer, sourceSlot, sourceGeneration, executorSlot, executorGeneration *uint32) bool {
	return task != nil && sourceSlot != nil && sourceGeneration != nil && executorSlot != nil && executorGeneration != nil &&
		unsafe.Pointer(sourceSlot) != task && unsafe.Pointer(sourceGeneration) != task &&
		unsafe.Pointer(executorSlot) != task && unsafe.Pointer(executorGeneration) != task &&
		sourceSlot != sourceGeneration && sourceSlot != executorSlot && sourceSlot != executorGeneration &&
		sourceGeneration != executorSlot && sourceGeneration != executorGeneration && executorSlot != executorGeneration
}

// __llgo_coro_task_control_register_v1 is a compiler/runtime owner call. It
// accepts the current managed G only long enough to allocate an endpoint, then
// returns a four-word POD token. Foreign producers must never receive task.
//
//export __llgo_coro_task_control_register_v1
func __llgo_coro_task_control_register_v1(task unsafe.Pointer, sourceSlot, sourceGeneration, executorSlot, executorGeneration *uint32) bool {
	if !validCoroTaskControlOutputWordsV1(task, sourceSlot, sourceGeneration, executorSlot, executorGeneration) {
		return false
	}
	*sourceSlot = 0
	*sourceGeneration = 0
	*executorSlot = 0
	*executorGeneration = 0
	token, ok := coroProgramRegisterTaskControlV1((*coroG)(task))
	if !ok {
		return false
	}
	*sourceSlot = token.SourceSlot
	*sourceGeneration = token.SourceGeneration
	*executorSlot = token.ExecutorSlot
	*executorGeneration = token.ExecutorGeneration
	return true
}

// __llgo_coro_task_control_post_v1 is the complete foreign-producer ABI. Its
// arguments and result are POD; target-specific code retains its ingress lease
// across durable Post, exact executor request, doorbell publication, and Leave.
// The low result byte is TaskControlPostResult and the next byte is
// ExecutorRequestResult.
//
//export __llgo_coro_task_control_post_v1
func __llgo_coro_task_control_post_v1(sourceSlot, sourceGeneration, executorSlot, executorGeneration, kindWord uint32) uint32 {
	token := coroTaskControlTokenV1{
		SourceSlot:         sourceSlot,
		SourceGeneration:   sourceGeneration,
		ExecutorSlot:       executorSlot,
		ExecutorGeneration: executorGeneration,
	}
	// Do not read executor/source owner globals on the producer side. The
	// target dispatcher acquires its stable ingress lease before validating the
	// route and executor generation against production storage.
	id, executor, ok := token.endpoint()
	if !ok || kindWord != uint32(coro.TaskCancelAbort) && kindWord != uint32(coro.TaskCancelShutdown) {
		return uint32(coro.TaskControlPostInvalid) | uint32(coro.ExecutorRequestInvalid)<<8
	}
	result := coroTargetPostTaskControlV1(id, coro.TaskCancelKind(kindWord), executor)
	return uint32(result.Control) | uint32(result.Executor)<<8
}

// The close calls are owner-only. Begin seals the exact producer generation;
// Finish succeeds only after admitted producers and the final cancellation
// fact have quiesced. A caller must return to the executor between retries so
// the common source transaction can drain a concurrently posted request.
//
//export __llgo_coro_task_control_close_begin_v1
func __llgo_coro_task_control_close_begin_v1(task unsafe.Pointer, sourceSlot, sourceGeneration, executorSlot, executorGeneration uint32) bool {
	return task != nil && coroProgramBeginCloseTaskControlV1((*coroG)(task), coroTaskControlTokenV1{
		SourceSlot: sourceSlot, SourceGeneration: sourceGeneration,
		ExecutorSlot: executorSlot, ExecutorGeneration: executorGeneration,
	})
}

//export __llgo_coro_task_control_close_finish_v1
func __llgo_coro_task_control_close_finish_v1(task unsafe.Pointer, sourceSlot, sourceGeneration, executorSlot, executorGeneration uint32) bool {
	return task != nil && coroProgramFinishCloseTaskControlV1((*coroG)(task), coroTaskControlTokenV1{
		SourceSlot: sourceSlot, SourceGeneration: sourceGeneration,
		ExecutorSlot: executorSlot, ExecutorGeneration: executorGeneration,
	})
}
