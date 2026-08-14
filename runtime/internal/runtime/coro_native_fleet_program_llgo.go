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

// coroNativeFleetStartProgramV1 adopts the already-bound program executor as
// route 1 and creates count-1 independent fleet-owned routes. The program
// globals remain authoritative; the fleet domain retains only stable
// references until every route, ingress, backend, and driver is strongly
// retired.
func coroNativeFleetStartProgramV1(count uint32) bool {
	var worker *coro.WorkerOperationSource
	if coroProgramWorkerCapabilityV2() {
		worker = &coroProgramWorkerSourceV1State
	}
	owners := coroNativeFleetDomainOwnersV1{
		p:      &coroProgramPV1State,
		driver: &coroProgramExecutorDriverV1State,
		sources: coro.ExecutorSourceCatalog{
			Timers:  &coroProgramTimerTableV1State,
			Poll:    &coroProgramPollSourceV1State,
			Manual:  &coroProgramManualSourceV2State,
			Worker:  worker,
			Channel: &coroProgramChannelSourceV1State,
			Control: &coroProgramTaskControlSourceV1State,
		},
	}
	return coroNativeFleetStartDomainsV1(&coroNativeFleetV1State, &owners, count)
}
