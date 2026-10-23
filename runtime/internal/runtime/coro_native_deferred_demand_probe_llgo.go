//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test && llgo_coro_deferred_demand_probe

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

import "unsafe"

var deferredDemandProbeWords unsafe.Pointer
var deferredDemandProbePhase uint32

func DeferredDemandProbeEnable(words unsafe.Pointer) {
	deferredDemandProbeWords = words
	coroNativeAtomicStoreV1(&deferredDemandProbePhase, 1)
}
func coroNativeDeferredDemandProbeAfterArmV1(boundary *coroNativeForeignBoundaryV1) {
	if !coroNativeAtomicCASV1(&deferredDemandProbePhase, 1, 2) {
		return
	}
	words := (*[4]uint32)(deferredDemandProbeWords)
	coroNativeAtomicStoreV1(&words[2], 1)
	if !coroNativeMActivateDeferredReplacementV1(boundary.domain) {
		coroRuntimeAbort("probe cannot start deferred replacement")
		return
	}
	for spins := uint64(0); coroNativeAtomicLoadV1(&words[0]) == 0; spins++ {
		if spins == 1000000000 {
			coroRuntimeAbort("probe replacement did not enter bounded leaf")
			return
		}
	}
	if coroNativeAtomicLoadV1(&words[0]) != 1 {
		coroRuntimeAbort("probe bounded leaf timed out")
		return
	}
}
func coroNativeDeferredDemandProbeAfterReadV1(ok bool) {
	if !coroNativeAtomicCASV1(&deferredDemandProbePhase, 2, 3) {
		return
	}
	words := (*[4]uint32)(deferredDemandProbeWords)
	if ok {
		coroNativeAtomicStoreV1(&words[3], 1)
	} else {
		coroNativeAtomicStoreV1(&words[3], 2)
	}
	println("deferred probe demand", ok)
	coroNativeAtomicStoreV1(&words[1], 1)
}
