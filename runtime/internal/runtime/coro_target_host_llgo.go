//go:build llgo && llgo_coro && !coro_runtime_adapter_test && (wasm || baremetal || llgo_coro_host) && !(llgo_coro_native_pipe && (darwin || linux) && !baremetal)

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

import "github.com/goplus/llgo/runtime/internal/coro"

// The host profile is a pull ABI, not a platform import masquerading as one.
// JS/WASM wrappers, WASI reactors, RTOS tasks, and embedded main loops consume
// the same pointer-free actions but provide their own scheduling/alarm glue.
// Consequently this file has no pthread, libuv, BDWGC, libc, or allocation
// dependency. A platform which cannot consume Schedule/Alarm actions must not
// select llgo_coro_host and remains on the fail-closed target.
var (
	coroHostTargetV1State coro.HostExecutorAdapter
	coroHostClockV1State  coro.HostMonotonicClock
)

const (
	coroHostProfileJSV1 uint32 = iota + 1
	coroHostProfileWASIV1
	coroHostProfileBaremetalV1
	coroHostProfileEmbeddedV1
	coroHostProfileWasmReactorV1

	coroHostProfileKindMaskV1           uint32 = 0xff
	coroHostCapabilityScheduleV1        uint32 = 1 << 8
	coroHostCapabilityAlarmV1           uint32 = 1 << 9
	coroHostCapabilityReactorPollV1     uint32 = 1 << 10
	coroHostCapabilityExternalReactorV1 uint32 = 1 << 31
)

func coroTargetExecutorStartV1(handle coro.ExecutorHandle) bool {
	// The compiler's ordinary command entry still owns a native-only V2 loop.
	// Until a platform wrapper owns the host reactor it selects LegacyV1; reject
	// that mode loudly instead of returning Suspended and silently abandoning a
	// queued action. Embeddings enter through RunSliceV2 after publishing time.
	return coroHostPlatformProfileV1&coroHostProfileKindMaskV1 != 0 &&
		coroProgramDriverModeV2State == coroProgramDriverModeSliceV2 &&
		handle == coroProgramExecutorHandleV1State && coroHostTargetV1State.Start(handle, true)
}

func coroTargetBeginExecutorRunV2(handle coro.ExecutorHandle, epoch uint32) coroTargetRunRequestResultV2 {
	if !coroHostTargetV1State.BeginRun(handle, epoch) {
		return coroTargetRunRequestInvalidV2
	}
	// Queued means NextAction owns a durable obligation. It never invokes the
	// host before this runtime entry has returned.
	return coroTargetRunRequestQueuedV2
}

func coroTargetConsumeExecutorRunV2(handle coro.ExecutorHandle, epoch uint32) bool {
	return coroHostTargetV1State.CompleteRun(handle, epoch) == coro.HostAdapterCompletionComplete
}

func coroTargetBeginExecutorWaitV1(handle coro.ExecutorHandle, epoch uint32, deadline int64, hasDeadline bool) coroTargetDispatchResultV1 {
	if !coroHostTargetV1State.BeginWait(handle, epoch, deadline, hasDeadline) {
		return coroTargetDispatchInvalidV1
	}
	return coroTargetDispatchPendingV1
}

func coroTargetPollExecutorWakeV1(handle coro.ExecutorHandle, epoch uint32) coroTargetDispatchResultV1 {
	switch coroHostTargetV1State.CompleteWait(handle, epoch) {
	case coro.HostAdapterCompletionComplete:
		return coroTargetDispatchCompleteV1
	case coro.HostAdapterCompletionPending:
		return coroTargetDispatchPendingV1
	default:
		return coroTargetDispatchInvalidV1
	}
}

func coroTargetBeginExecutorCloseV1(handle coro.ExecutorHandle, epoch uint32) coroTargetDispatchResultV1 {
	switch coroHostTargetV1State.BeginClose(handle, epoch) {
	case coro.HostAdapterCompletionComplete:
		return coroTargetDispatchCompleteV1
	case coro.HostAdapterCompletionPending:
		return coroTargetDispatchPendingV1
	default:
		return coroTargetDispatchInvalidV1
	}
}

func coroTargetPollExecutorCloseV1(handle coro.ExecutorHandle, epoch uint32) coroTargetDispatchResultV1 {
	switch coroHostTargetV1State.CompleteClose(handle, epoch) {
	case coro.HostAdapterCompletionComplete:
		return coroTargetDispatchCompleteV1
	case coro.HostAdapterCompletionPending:
		return coroTargetDispatchPendingV1
	default:
		return coroTargetDispatchInvalidV1
	}
}

func coroHostRequestExecutorV1(handle coro.ExecutorHandle) (coro.ExecutorRequestResult, bool) {
	if !coroHostTargetV1State.EnterProducer(handle) {
		return coro.ExecutorRequestClosed, false
	}
	result := coroProgramExecutorRegistryV1State.Request(handle)
	wakeOK := !coro.ExecutorRequestNeedsDoorbell(result) || coroHostTargetV1State.RequestWake(handle)
	leaveOK := coroHostTargetV1State.LeaveProducer()
	return result, wakeOK && leaveOK
}

func coroTargetRequestExecutorV1(handle coro.ExecutorHandle) bool {
	result, ok := coroHostRequestExecutorV1(handle)
	return ok && (result == coro.ExecutorRequestPublished || result == coro.ExecutorRequestCoalesced ||
		result == coro.ExecutorRequestIdleWake)
}

func coroTargetPostTaskControlV1(id coro.OperationID, kind coro.TaskCancelKind, executor coro.ExecutorHandle) coro.TaskControlExecutorPostResult {
	invalid := coro.TaskControlExecutorPostResult{
		Control:  coro.TaskControlPostInvalid,
		Executor: coro.ExecutorRequestInvalid,
	}
	if !coroHostTargetV1State.EnterProducer(executor) {
		return invalid
	}
	if executor != coroProgramExecutorHandleV1State {
		_ = coroHostTargetV1State.LeaveProducer()
		return invalid
	}
	result := coro.PostTaskControlAndRequest(
		&coroProgramTaskControlSourceV1State,
		id,
		kind,
		&coroProgramExecutorRegistryV1State,
		executor,
	)
	wakeOK := !coro.ExecutorRequestNeedsDoorbell(result.Executor) || coroHostTargetV1State.RequestWake(executor)
	leaveOK := coroHostTargetV1State.LeaveProducer()
	if !wakeOK || !leaveOK {
		return invalid
	}
	return result
}

// __llgo_coro_host_next_action_v1 transfers one durable Schedule, Alarm, or
// Cancel obligation to the embedding loop. It returns HostActionNoneV1 when
// no action is currently available. The host must never call Continue inline
// from this function; Schedule always means a later host turn.
//
//export __llgo_coro_host_next_action_v1
func __llgo_coro_host_next_action_v1(out *coro.HostActionV1) uint32 {
	if out == nil || !coroHostTargetV1State.NextAction(out) {
		if out != nil {
			*out = coro.HostActionV1{}
		}
		return uint32(coro.HostActionNoneV1)
	}
	return out.Kind
}

// __llgo_coro_host_profile_v1 freezes the host-owned reactor contract. The low
// byte is the target kind; upper bits are capabilities. It does not claim that
// the ordinary command _start owns or pumps such a reactor.
//
//export __llgo_coro_host_profile_v1
func __llgo_coro_host_profile_v1() uint32 {
	return coroHostPlatformProfileV1
}

// __llgo_coro_host_next_deadline_v1 is a non-consuming main-loop query. The
// same exact tuple is also present in the Alarm action and RunSlice result;
// exposing it separately lets an embedded loop reprogram a hardware compare
// after unrelated notifications without retaining runtime pointers.
//
//export __llgo_coro_host_next_deadline_v1
func __llgo_coro_host_next_deadline_v1(out *coro.HostActionV1) bool {
	return out != nil && coroHostTargetV1State.NextDeadline(out)
}

//export __llgo_coro_host_publish_time_v1
func __llgo_coro_host_publish_time_v1(nowLo, nowHi uint32) bool {
	return coroHostClockV1State.Publish(nowLo, nowHi)
}

//export __llgo_coro_host_ack_cancel_v1
func __llgo_coro_host_ack_cancel_v1(executorSlot, executorGeneration, epoch, kind uint32) bool {
	return coroHostTargetV1State.AcknowledgeCancel(
		coro.ExecutorHandle{Slot: executorSlot, Generation: executorGeneration},
		epoch,
		coro.HostActionKindV1(kind),
	)
}

// __llgo_coro_host_continue_slice_v1 is the only production host callback
// entry for this profile. Claim validates the exact generation/epoch and
// callback cause before scheduler state is touched. Repost/Suspended rearm the
// same tuple only after coroProgramContinueSliceV2 has returned.
//
//export __llgo_coro_host_continue_slice_v1
func __llgo_coro_host_continue_slice_v1(
	executorSlot, executorGeneration, epoch, cause, nowLo, nowHi, budget uint32,
	out *coroProgramRunResultV2,
) uint32 {
	if out == nil || budget == 0 || !coroHostClockV1State.Publish(nowLo, nowHi) {
		if out != nil {
			*out = coroProgramRunResultV2{}
		}
		return uint32(coroProgramDriveInvalidV2)
	}
	handle := coro.ExecutorHandle{Slot: executorSlot, Generation: executorGeneration}
	callbackCause := coro.HostCallbackCauseV1(cause)
	claim, lease := coroHostTargetV1State.ClaimCallback(handle, epoch, callbackCause)
	switch claim {
	case coro.HostCallbackClaimStale:
		*out = coroProgramRunResultV2{}
		return uint32(coroProgramDriveIgnoredV2)
	case coro.HostCallbackClaimed:
	default:
		*out = coroProgramRunResultV2{}
		return uint32(coroProgramDriveInvalidV2)
	}
	status := coroProgramContinueSliceV2(executorSlot, executorGeneration, epoch, budget, out)
	if lease {
		repost := status == uint32(coroProgramDriveRepostV2) || status == uint32(coroProgramDriveSuspendedV2)
		if !coroHostTargetV1State.FinishCallback(handle, epoch, callbackCause, true, repost) {
			*out = coroProgramRunResultV2{}
			return uint32(coroProgramDriveInvalidV2)
		}
	} else if status == uint32(coroProgramDriveRepostV2) || status == uint32(coroProgramDriveSuspendedV2) {
		if !coroHostTargetV1State.RepostCloseCallback(handle, epoch) {
			*out = coroProgramRunResultV2{}
			return uint32(coroProgramDriveInvalidV2)
		}
	}
	return status
}
