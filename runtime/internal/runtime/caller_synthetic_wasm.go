//go:build llgo && wasm && !(wasip1 && llgo.wasi_threads)

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

// WebAssembly function values are table indexes, not aligned text addresses.
// Reserve a separate tagged PC domain instead of aliasing small table indexes
// with each goroutine's first synthetic frame. The two low bits retain the
// existing Caller/Callers distinction, including CallersFrames' pc-1 lookup.
const callerSyntheticPCNamespace = uintptr(1) << (unsafe.Sizeof(uintptr(0))*8 - 1)

// Only interned, immutable records are process-owned. Live shadow stacks and
// panic snapshots remain goroutine-local. Keep the registry reachable for the
// process lifetime: a uintptr PC does not keep its originating goroutine alive.
//
// This backend has one mutator. These runtime helpers have no cooperative
// safepoints, and allocation-triggered GC queues finalizers without invoking
// user callbacks, so interning cannot switch goroutines or reenter itself.
// No lock is held while growing the globally rooted slices; in particular,
// GC during growth cannot deadlock on a symbolization lock. A future threaded
// backend or allocation-stack profiler must supply its own synchronization or
// suppress profiler recursion before using this registry.
var wasmCallerSyntheticRegistry callerLocationStore

func callerSyntheticRegistryFor(*callerLocationStore) *callerLocationStore {
	return &wasmCallerSyntheticRegistry
}

func callerSyntheticLookupStore() *callerLocationStore {
	return &wasmCallerSyntheticRegistry
}

// IsWasmSyntheticPC distinguishes logical PCs from actual function indexes.
func IsWasmSyntheticPC(pc uintptr) bool {
	return pc&callerSyntheticPCNamespace != 0 && pc&callerPCMask != 0
}

func callerSyntheticEntryPC(pc uintptr) uintptr {
	// Synthetic runtime frames have no physical entry. Give Caller, Callers,
	// and pc-1 lookups the same stable entry for the same interned record.
	return pc&^callerPCMask | callerPCValue
}
