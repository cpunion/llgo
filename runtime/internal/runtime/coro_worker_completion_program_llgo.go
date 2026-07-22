//go:build llgo && llgo_coro && llgo_coro_native_pipe && !llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

// The target profile owns both completion delivery and submission authority.
// Keeping these decisions in the same mutually exclusive file prevents the
// default production island from acquiring a link-time dependency on fleet
// storage that is not part of its explicit runtime source closure.
func coroNativeWorkerDeliveryReadyV1(
	delivery coroNativeWorkerDeliveryV1,
	handle coro.ExecutorHandle,
	route coro.RouteID,
) bool {
	workerRoute, routeOK := coroProgramWorkerSourceV1State.Route()
	return delivery == coroNativeWorkerDeliveryProgramV1 &&
		coroProgramExecutorBoundV1State && handle == coroProgramExecutorHandleV1State &&
		handle.Slot != 0 && handle.Generation != 0 && routeOK && route == workerRoute
}

func coroNativeWorkerSubmissionOwnerProfileV1(
	state *coroNativeWorkerPoolV1,
	handle coro.ExecutorHandle,
	route coro.RouteID,
) bool {
	return state != nil && state.delivery == coroNativeWorkerDeliveryProgramV1 &&
		state.handle == handle && state.route == route &&
		coroProgramExecutorBoundV1State && handle == coroProgramExecutorHandleV1State
}

// __llgo_coro_native_worker_complete_v1 is the only C-worker-to-Go edge in the
// single-P compatibility profile. Fleet delivery selects a mutually exclusive
// target file rather than a function pointer or reverse address lookup.
//
//export __llgo_coro_native_worker_complete_v1
func __llgo_coro_native_worker_complete_v1(
	sourceSlot, generation uint32,
	r1, r2, errno uintptr,
) uint32 {
	state := &coroNativeWorkerPoolV1State
	id := coro.OperationID{SourceSlot: sourceSlot, Generation: generation}
	if !state.started || state.delivery != coroNativeWorkerDeliveryProgramV1 ||
		state.handle.Slot == 0 || state.handle.Generation == 0 || id.Route() != state.route ||
		!id.Valid() || id.Source() != coro.OperationSourceWorker {
		return 0
	}
	payload, ok := coro.MakeScalarResultPayloadV1(
		coro.ScalarResultKindWords,
		0,
		3,
		uint64(r1),
		uint64(r2),
		uint64(errno),
	)
	if !ok || coroProgramWorkerSourceV1State.Post(id, payload) != coro.WorkerOperationPosted ||
		!coroTargetRequestExecutorV1(state.handle) {
		return 0
	}
	return 1
}
