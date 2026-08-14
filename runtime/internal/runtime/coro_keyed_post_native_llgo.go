//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

func coroTargetPostKeyedOperationV2(id coro.OperationID) bool {
	if !id.Valid() || id.Source() != coro.OperationSourceManual {
		return false
	}
	domain, ok := coroNativeFleetActiveDomainForRouteV1(id.Route())
	if !ok {
		return false
	}
	// A keyed semaphore/notify completion is committed by a currently running
	// managed G. Physical-owner stop joins that execution before route teardown,
	// so it must not acquire the external callback ingress: task cancellation at
	// main return could otherwise destroy the G between Enter and Leave and make
	// backend retirement wait forever. Raw C/host producers continue to use the
	// exported coroNativeFleetPostV1 path and its strong ingress join.
	result := coroNativeFleetV1State.fleet.PostManualAndRequest(id)
	return result.Route == coro.OperationRoutePosted &&
		coro.ExecutorRequestAccepted(result.Executor) &&
		coroNativeFleetFinishExecutorRequestV1(domain, result.Executor)
}
