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

func coroNativeWorkerDeliveryReadyV1(
	delivery coroNativeWorkerDeliveryV1,
	handle coro.ExecutorHandle,
	route coro.RouteID,
) bool {
	return delivery == coroNativeWorkerDeliveryFleetV1 &&
		handle == (coro.ExecutorHandle{}) && route == 0 &&
		coroNativeFleetWorkerTransportReadyV1()
}

func coroNativeWorkerSubmissionOwnerProfileV1(
	state *coroNativeWorkerPoolV1,
	handle coro.ExecutorHandle,
	route coro.RouteID,
) bool {
	return state != nil && state.delivery == coroNativeWorkerDeliveryFleetV1 &&
		state.handle == (coro.ExecutorHandle{}) && state.route == 0 &&
		coroNativeFleetWorkerSubmissionOwnerV1(handle, route)
}

// __llgo_coro_native_worker_complete_v1 is the only C-worker-to-Go edge in the
// fleet profile. The POD OperationID route selects the exact logical source,
// request gate, and doorbell while one target-ingress lease protects their
// complete tail; no C function address is used for routing.
//
//export __llgo_coro_native_worker_complete_v1
func __llgo_coro_native_worker_complete_v1(
	sourceSlot, generation uint32,
	r1, r2, errno, fault, faultPC, faultTarget uintptr,
) uint32 {
	state := &coroNativeWorkerPoolV1State
	id := coro.OperationID{SourceSlot: sourceSlot, Generation: generation}
	if !state.started || state.delivery != coroNativeWorkerDeliveryFleetV1 ||
		state.handle != (coro.ExecutorHandle{}) || state.route != 0 ||
		!id.Valid() || id.Source() != coro.OperationSourceWorker {
		return 0
	}
	payload, ok := coroWorkerResultPayloadV1(r1, r2, errno, fault, faultPC, faultTarget)
	if !ok {
		return 0
	}
	result := coroNativeFleetPostWorkerV1(id, payload)
	accepted := result.Executor == coro.ExecutorRequestPublished ||
		result.Executor == coro.ExecutorRequestIdleWake ||
		result.Executor == coro.ExecutorRequestCoalesced
	if result.Route != coro.OperationRoutePosted || !accepted {
		return 0
	}
	return 1
}
