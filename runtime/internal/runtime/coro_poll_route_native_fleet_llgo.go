//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && llgo_coro_native_fleet && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

// The fleet ingress retains its route lease across exact source publication,
// executor request, and physical doorbell delivery. No descriptor callback
// reconstructs an executor from an fd or an ExecutorHandle.
func coroTargetPostPollOperationV2(
	id coro.OperationID,
	status coro.PollOperationResult,
) coro.PollOperationPostResult {
	result := coroNativeFleetPostPollV1(id, status)
	switch result.Route {
	case coro.OperationRoutePosted:
		return coro.PollOperationPosted
	case coro.OperationRoutePostCoalesced:
		return coro.PollOperationPostDuplicate
	case coro.OperationRoutePostSourceClosed, coro.OperationRoutePostClosed:
		return coro.PollOperationPostClosed
	case coro.OperationRoutePostSourceStale, coro.OperationRoutePostStale:
		return coro.PollOperationPostStale
	default:
		return coro.PollOperationPostInvalid
	}
}
