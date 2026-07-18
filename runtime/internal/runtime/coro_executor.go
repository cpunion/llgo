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
// Platform callback ABIs retain only coroProgramExecutorHandleV1State and wait
// registration handles; none of these Go objects or their addresses crosses a
// target callback boundary.
var (
	coroProgramExecutorRegistryV1State coro.ExecutorRegistry
	coroProgramWaitTableV1State        coro.WaitRegistrationTable
	coroProgramTimerTableV1State       coro.TimerRegistrationTable
	coroProgramWorkerSourceV1State     coro.WorkerOperationSource
	coroProgramChannelSourceV1State    coro.ChannelOperationSource
	coroProgramExecutorDriverV1State   coro.ExecutorDriver
	coroProgramExecutorHandleV1State   coro.ExecutorHandle
	coroProgramExecutorBoundV1State    bool
)

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
		!coroProgramWaitTableV1State.CanRelease() ||
		!coroProgramTimerTableV1State.CanRelease() ||
		!coroProgramWorkerSourceV1State.CanRelease() ||
		!coroProgramChannelSourceV1State.CanRelease() {
		return false
	}
	handle, ok := coroProgramExecutorRegistryV1State.Register()
	if !ok || !coroProgramBindExecutorDriverV1(
		&coroProgramExecutorDriverV1State,
		&coroProgramPV1State,
		&coroProgramExecutorRegistryV1State,
		handle,
		&coroProgramWaitTableV1State,
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
		!coroProgramWaitTableV1State.CanRelease() ||
		!coroProgramTimerTableV1State.CanRelease() ||
		!coroProgramWorkerSourceV1State.CanRelease() ||
		!coroProgramChannelSourceV1State.CanRelease() {
		return false
	}
	coroProgramExecutorBoundV1State = false
	coroProgramExecutorHandleV1State = coro.ExecutorHandle{}
	return true
}

func coroProgramPrepareWaitV1(token *coro.WaitToken) (coro.WaitTicket, coro.WaitRegistrationHandle, coro.ExecutorHandle, coro.WaitRegistrationPrepareResult) {
	if !coroProgramExecutorBoundV1State || token == nil ||
		coroProgramExecutorHandleV1State == (coro.ExecutorHandle{}) {
		return 0, coro.WaitRegistrationHandle{}, coro.ExecutorHandle{}, coro.WaitRegistrationPrepareInvalid
	}
	ticket, wait, result := coro.PrepareExecutorWaitRegistration(
		&coroProgramExecutorDriverV1State,
		token,
	)
	if result != coro.WaitRegistrationPrepared {
		return 0, coro.WaitRegistrationHandle{}, coro.ExecutorHandle{}, result
	}
	return ticket, wait, coroProgramExecutorHandleV1State, result
}

func coroProgramRollbackWaitV1(token *coro.WaitToken, ticket coro.WaitTicket, wait coro.WaitRegistrationHandle) bool {
	return coroProgramExecutorBoundV1State && token != nil &&
		coro.RollbackExecutorWaitRegistration(&coroProgramExecutorDriverV1State, token, ticket, wait)
}

func coroProgramRetireCompletedWaitV1(token *coro.WaitToken, ticket coro.WaitTicket, wait coro.WaitRegistrationHandle) bool {
	return coroProgramExecutorBoundV1State && token != nil &&
		coro.RetireCompletedExecutorWait(&coroProgramExecutorDriverV1State, token, ticket, wait)
}

func validCoroWaitOutputWordsV1(token unsafe.Pointer, ticket, waitSlot, waitGeneration, executorSlot, executorGeneration *uint32) bool {
	return token != nil && ticket != nil && waitSlot != nil && waitGeneration != nil && executorSlot != nil && executorGeneration != nil &&
		unsafe.Pointer(ticket) != token && unsafe.Pointer(waitSlot) != token && unsafe.Pointer(waitGeneration) != token &&
		unsafe.Pointer(executorSlot) != token && unsafe.Pointer(executorGeneration) != token &&
		ticket != waitSlot && ticket != waitGeneration && ticket != executorSlot && ticket != executorGeneration &&
		waitSlot != waitGeneration && waitSlot != executorSlot && waitSlot != executorGeneration &&
		waitGeneration != executorSlot && waitGeneration != executorGeneration && executorSlot != executorGeneration
}

// __llgo_coro_wait_prepare_v1 is the bounded owner-side half of platform wait
// submission. It atomically pairs a fresh token ticket with one durable wait
// registration and returns only the ticket plus the four POD words that a
// producer may retain. If registration fails after ArmWait, the unpublished
// ticket is rolled back to a consumed cancellation and remains safely reusable.
//
//export __llgo_coro_wait_prepare_v1
func __llgo_coro_wait_prepare_v1(token unsafe.Pointer, ticket, waitSlot, waitGeneration, executorSlot, executorGeneration *uint32) bool {
	if !validCoroWaitOutputWordsV1(token, ticket, waitSlot, waitGeneration, executorSlot, executorGeneration) {
		return false
	}
	*ticket = 0
	*waitSlot = 0
	*waitGeneration = 0
	*executorSlot = 0
	*executorGeneration = 0
	preparedTicket, wait, executor, result := coroProgramPrepareWaitV1((*coro.WaitToken)(token))
	if result == coro.WaitRegistrationPreparePoisoned {
		coroRuntimeAbort("coroutine wait prepare rollback failed")
		return false
	}
	if result != coro.WaitRegistrationPrepared {
		return false
	}
	*ticket = uint32(preparedTicket)
	*waitSlot = wait.Slot
	*waitGeneration = wait.Generation
	*executorSlot = executor.Slot
	*executorGeneration = executor.Generation
	return true
}

// __llgo_coro_wait_rollback_v1 is used only when an operation submission
// failed before it could start a callback and the caller has proved that
// source quiesced. It is invalid after coroPark or producer publication.
//
//export __llgo_coro_wait_rollback_v1
func __llgo_coro_wait_rollback_v1(token unsafe.Pointer, ticket, waitSlot, waitGeneration uint32) bool {
	return token != nil && coroProgramRollbackWaitV1(
		(*coro.WaitToken)(token),
		coro.WaitTicket(ticket),
		coro.WaitRegistrationHandle{Slot: waitSlot, Generation: waitGeneration},
	)
}

// __llgo_coro_wait_retire_completed_v1 releases a delivered registration only
// after its synchronous-style continuation has resumed and strongly joined or
// unregistered the external source that knew the POD handle.
//
//export __llgo_coro_wait_retire_completed_v1
func __llgo_coro_wait_retire_completed_v1(token unsafe.Pointer, ticket, waitSlot, waitGeneration uint32) bool {
	return token != nil && coroProgramRetireCompletedWaitV1(
		(*coro.WaitToken)(token),
		coro.WaitTicket(ticket),
		coro.WaitRegistrationHandle{Slot: waitSlot, Generation: waitGeneration},
	)
}
