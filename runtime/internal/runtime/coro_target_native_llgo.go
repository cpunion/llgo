//go:build llgo && llgo_coro && llgo_coro_native_pipe && (darwin || linux) && !baremetal && !coro_runtime_adapter_test && !llgo_coro_native_fleet

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
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/corodoorbell"
)

type coroNativeTargetStateV1 struct {
	ingress   coro.TargetIngress
	doorbell  corodoorbell.Pipe
	handle    coro.ExecutorHandle
	waitEpoch uint32
	runEpoch  uint32
	started   bool
}

func coroTargetBeginExecutorRunV2(handle coro.ExecutorHandle, epoch uint32) coroTargetRunRequestResultV2 {
	state := &coroNativeTargetV1State
	if !state.started || state.handle != handle || epoch == 0 || state.runEpoch != 0 || state.waitEpoch != 0 {
		return coroTargetRunRequestInvalidV2
	}
	state.runEpoch = epoch
	return coroTargetRunRequestInlineV2
}

func coroTargetConsumeExecutorRunV2(handle coro.ExecutorHandle, epoch uint32) bool {
	state := &coroNativeTargetV1State
	if !state.started || state.handle != handle || epoch == 0 || state.runEpoch != epoch {
		return false
	}
	state.runEpoch = 0
	return true
}

var coroNativeTargetV1State coroNativeTargetStateV1

// CoroNativePollServerDescriptorV1 exposes only the stable identity of the
// shared native executor doorbell to the standard-library runtime poll shim.
// It does not expose either descriptor for I/O or transfer target ownership.
func CoroNativePollServerDescriptorV1(fd uintptr) bool {
	return coroNativeTargetV1State.doorbell.OwnsDescriptor(fd)
}

func coroTargetExecutorStartV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeTargetV1State
	if state.started || state.handle != (coro.ExecutorHandle{}) || !state.ingress.CanReleaseResources() ||
		!coroNativeWorkerPoolCanReleaseV1() ||
		handle != coroProgramExecutorHandleV1State || handle.Slot == 0 || handle.Generation == 0 ||
		!state.doorbell.Open() {
		return false
	}
	state.handle = handle
	if !state.ingress.Start() {
		_ = state.doorbell.Close()
		state.handle = coro.ExecutorHandle{}
		return false
	}
	state.started = true
	if !coroNativeWorkerPoolStartV1(handle) {
		state.started = false
		sealed := state.ingress.Seal()
		retired := sealed && state.ingress.Retire()
		closed := state.doorbell.Close()
		state.handle = coro.ExecutorHandle{}
		if !retired || !closed {
			coroRuntimeAbort("native coroutine target start rollback failed")
		}
		return false
	}
	return true
}

func coroTargetBeginExecutorWaitV1(handle coro.ExecutorHandle, epoch uint32, deadline int64, hasDeadline bool) coroTargetDispatchResultV1 {
	state := &coroNativeTargetV1State
	if !state.started || state.handle != handle || epoch == 0 || state.waitEpoch != 0 || state.runEpoch != 0 {
		return coroTargetDispatchInvalidV1
	}
	state.waitEpoch = epoch
	if !coroTargetWaitExecutorV1(&state.doorbell, deadline, hasDeadline) {
		return coroTargetDispatchInvalidV1
	}
	state.waitEpoch = 0
	return coroTargetDispatchCompleteV1
}

func coroTargetPollExecutorWakeV1(coro.ExecutorHandle, uint32) coroTargetDispatchResultV1 {
	// The native executor blocks its one current owner thread in BeginWait.
	// It never returns Pending and therefore never re-enters through continue.
	return coroTargetDispatchInvalidV1
}

// coroTargetRequestExecutorV1 is the common durable-source wake tail for
// channel commits. It holds the target ingress lease across registry request,
// optional pipe ring, and Leave so native shutdown cannot retire the static
// target state in the Post -> Request -> Doorbell window.
func coroTargetRequestExecutorV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeTargetV1State
	if !state.ingress.Enter() {
		return false
	}
	if !state.started || state.handle != handle {
		_, _ = state.ingress.Leave()
		return false
	}
	result := coroProgramExecutorRegistryV1State.Request(handle)
	accepted := result == coro.ExecutorRequestPublished || result == coro.ExecutorRequestCoalesced ||
		result == coro.ExecutorRequestIdleWake
	ringOK := true
	if coro.ExecutorRequestNeedsDoorbell(result) {
		ringOK = state.doorbell.Ring()
	}
	_, leaveOK := state.ingress.Leave()
	return accepted && ringOK && leaveOK
}

// coroTargetPostTaskControlV1 is the native foreign-producer transaction. The
// target ingress lease starts before the stable TaskControlSource is touched
// and ends only after the exact executor request and optional doorbell byte.
// Terminal close therefore joins the otherwise-dangerous Post -> Request ->
// Ring window without allowing a producer to retain any Go pointer.
func coroTargetPostTaskControlV1(id coro.OperationID, kind coro.TaskCancelKind, executor coro.ExecutorHandle) coro.TaskControlExecutorPostResult {
	invalid := coro.TaskControlExecutorPostResult{
		Control:  coro.TaskControlPostInvalid,
		Executor: coro.ExecutorRequestInvalid,
	}
	state := &coroNativeTargetV1State
	if !state.ingress.Enter() {
		return invalid
	}
	if !state.started || state.handle != executor || executor != coroProgramExecutorHandleV1State {
		_, _ = state.ingress.Leave()
		return invalid
	}
	result := coro.PostTaskControlAndRequest(
		&coroProgramTaskControlSourceV1State,
		id,
		kind,
		&coroProgramExecutorRegistryV1State,
		executor,
	)
	ringOK := true
	if coro.ExecutorRequestNeedsDoorbell(result.Executor) {
		ringOK = state.doorbell.Ring()
	}
	_, leaveOK := state.ingress.Leave()
	if !ringOK || !leaveOK {
		return invalid
	}
	return result
}

func coroTargetBeginExecutorCloseV1(handle coro.ExecutorHandle, epoch uint32) coroTargetDispatchResultV1 {
	state := &coroNativeTargetV1State
	if !state.started || state.handle != handle || epoch == 0 || state.waitEpoch != 0 || state.runEpoch != 0 ||
		!coroNativeWorkerPoolStopV1(handle) || !state.ingress.Seal() {
		return coroTargetDispatchInvalidV1
	}

	// Producer shims are bounded nonblocking leaves. Do not wait on the same
	// pipe here: a producer admitted before Seal can observe the core's already
	// closed request gate and legitimately have no IdleWake byte to write. The
	// atomic load also handles a pipe byte coalesced before that producer's
	// final Leave. No pthread is created per G (or per executor). Strong join
	// intentionally has no global timeout: a producer that never reaches Leave
	// blocks shutdown rather than allowing its target state or FD to be reused.
	for !state.ingress.Quiesced() {
		if _, ok := state.doorbell.WaitBounded(1); !ok {
			return coroTargetDispatchInvalidV1
		}
	}
	if !state.doorbell.Close() || !state.ingress.Retire() {
		return coroTargetDispatchInvalidV1
	}
	state.started = false
	state.handle = coro.ExecutorHandle{}
	return coroTargetDispatchCompleteV1
}

func coroTargetPollExecutorCloseV1(coro.ExecutorHandle, uint32) coroTargetDispatchResultV1 {
	// Native close is a synchronous strong join.
	return coroTargetDispatchInvalidV1
}

// coroNativePostWaitV1 is the complete native producer ingress shim. A future
// syscall/timer adapter can expose the same four-word POD ABI directly to C:
// no table pointer, P/G pointer, wait token, or LLVM coroutine handle crosses
// the boundary. The durable wait slot and request are published before the
// optional nonblocking pipe write. Leave is the absolute last target access.
func coroNativePostWaitV1(waitSlot, waitGeneration, executorSlot, executorGeneration uint32) coro.WaitExecutorPostResult {
	state := &coroNativeTargetV1State
	if !state.ingress.Enter() {
		return coro.WaitExecutorPostResult{
			Wait:     coro.WaitRegistrationPostClosed,
			Executor: coro.ExecutorRequestClosed,
		}
	}

	wait := coro.WaitRegistrationHandle{Slot: waitSlot, Generation: waitGeneration}
	executor := coro.ExecutorHandle{Slot: executorSlot, Generation: executorGeneration}
	if executor != state.handle {
		_, _ = state.ingress.Leave()
		return coro.WaitExecutorPostResult{
			Wait:     coro.WaitRegistrationPostInvalid,
			Executor: coro.ExecutorRequestInvalid,
		}
	}
	result := coro.PostWaitAndRequest(
		&coroProgramWaitTableV1State,
		wait,
		&coroProgramExecutorRegistryV1State,
		executor,
	)
	ringOK := true
	if coro.ExecutorRequestNeedsDoorbell(result.Executor) {
		ringOK = state.doorbell.Ring()
	}
	_, leaveOK := state.ingress.Leave()
	// Do not touch state, doorbell, registry, or table after Leave: a close
	// owner may already have closed and retired all of them.
	if !ringOK || !leaveOK {
		return coro.WaitExecutorPostResult{
			Wait:     coro.WaitRegistrationPostInvalid,
			Executor: coro.ExecutorRequestInvalid,
		}
	}
	return result
}

// __llgo_coro_native_post_wait_v1 is the producer-thread ABI for a completed
// native operation. In nogc builds an ordinary pthread may enter it. A
// collecting build may enter only from a runtime/collector-registered producer
// thread (for example one created with GC_pthread_create); arbitrary foreign
// threads are not yet supported. Signal handlers and ISRs must not call it.
// The low byte is WaitRegistrationPostResult and the next byte is
// ExecutorRequestResult. Every argument and result word is POD; managed
// scheduler ownership remains behind the permanent static ingress shim.
//
//export __llgo_coro_native_post_wait_v1
func __llgo_coro_native_post_wait_v1(waitSlot, waitGeneration, executorSlot, executorGeneration uint32) uint32 {
	result := coroNativePostWaitV1(waitSlot, waitGeneration, executorSlot, executorGeneration)
	return uint32(result.Wait) | uint32(result.Executor)<<8
}
