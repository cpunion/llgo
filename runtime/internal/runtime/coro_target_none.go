//go:build !coro_runtime_adapter_test && !(llgo && llgo_coro && llgo_coro_native_pipe && (darwin || linux) && !baremetal) && !(llgo && llgo_coro && (wasm || baremetal || llgo_coro_host) && !(llgo_coro_native_pipe && (darwin || linux) && !baremetal))

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

// The target-neutral production fallback exposes no ingress callback and
// therefore has nothing physical to join. An already sealed, empty executor
// can be acknowledged synchronously. Entering a retained wait is deliberately
// unsupported until a target supplies a real doorbell backend.
func coroTargetExecutorStartV1(handle coro.ExecutorHandle) bool {
	return handle.Slot != 0 && handle.Generation != 0
}

func coroTargetBeginExecutorRunV2(coro.ExecutorHandle, uint32) coroTargetRunRequestResultV2 {
	// A target without a real host-run capability must fail closed. Treating
	// this as Inline would let WASM or an embedded host monopolize its entry.
	return coroTargetRunRequestInvalidV2
}

func coroTargetConsumeExecutorRunV2(coro.ExecutorHandle, uint32) bool {
	return false
}

func coroTargetBeginExecutorCloseV1(handle coro.ExecutorHandle, epoch uint32) coroTargetDispatchResultV1 {
	if handle != coroProgramExecutorHandleV1State || epoch == 0 {
		return coroTargetDispatchInvalidV1
	}
	return coroTargetDispatchCompleteV1
}

func coroTargetPollExecutorCloseV1(coro.ExecutorHandle, uint32) coroTargetDispatchResultV1 {
	return coroTargetDispatchInvalidV1
}

func coroTargetBeginExecutorWaitV1(coro.ExecutorHandle, uint32, int64, bool) coroTargetDispatchResultV1 {
	return coroTargetDispatchInvalidV1
}

func coroTargetPollExecutorWakeV1(coro.ExecutorHandle, uint32) coroTargetDispatchResultV1 {
	return coroTargetDispatchInvalidV1
}

func coroTargetRequestExecutorV1(handle coro.ExecutorHandle) bool {
	result := coroProgramExecutorRegistryV1State.Request(handle)
	return result == coro.ExecutorRequestPublished || result == coro.ExecutorRequestCoalesced
}

func coroTargetPostTaskControlV1(coro.OperationID, coro.TaskCancelKind, coro.ExecutorHandle) coro.TaskControlExecutorPostResult {
	// No host-run/doorbell capability exists on this fallback. Do not accept a
	// cancellation fact which the target cannot guarantee it will service.
	return coro.TaskControlExecutorPostResult{
		Control:  coro.TaskControlPostInvalid,
		Executor: coro.ExecutorRequestInvalid,
	}
}
