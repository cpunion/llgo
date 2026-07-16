//go:build llgo && llgo_coro && llgo_coro_native_pipe && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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
	started   bool
}

var coroNativeTargetV1State coroNativeTargetStateV1

func coroTargetExecutorStartV1(handle coro.ExecutorHandle) bool {
	state := &coroNativeTargetV1State
	if state.started || state.handle != (coro.ExecutorHandle{}) || !state.ingress.CanReleaseResources() ||
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
	return true
}

func coroTargetBeginExecutorWaitV1(handle coro.ExecutorHandle, epoch uint32, deadline int64, hasDeadline bool) coroTargetDispatchResultV1 {
	state := &coroNativeTargetV1State
	if !state.started || state.handle != handle || epoch == 0 || state.waitEpoch != 0 {
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

func coroTargetBeginExecutorCloseV1(handle coro.ExecutorHandle, epoch uint32) coroTargetDispatchResultV1 {
	state := &coroNativeTargetV1State
	if !state.started || state.handle != handle || epoch == 0 || state.waitEpoch != 0 || !state.ingress.Seal() {
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
