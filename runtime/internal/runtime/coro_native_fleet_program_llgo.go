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

// coroNativeFleetStartProgramV1 adopts the already-bound program executor as
// route 1 and creates one independent fleet-owned route 2. The program globals
// remain authoritative; the fleet domain retains only stable references until
// its route, ingress, backend, and driver have been strongly retired.
func coroNativeFleetStartProgramV1() bool {
	owners := coroNativeFleetDomainOwnersV1{
		p:      &coroProgramPV1State,
		driver: &coroProgramExecutorDriverV1State,
		sources: coro.ExecutorSourceCatalog{
			Waits:   &coroProgramWaitTableV1State,
			Timers:  &coroProgramTimerTableV1State,
			Poll:    &coroProgramPollSourceV1State,
			Worker:  &coroProgramWorkerSourceV1State,
			Channel: &coroProgramChannelSourceV1State,
			Control: &coroProgramTaskControlSourceV1State,
		},
	}
	return coroNativeFleetStartDomainsV1(&coroNativeFleetV1State, &owners)
}
