//go:build coro_runtime_adapter_test || coro_native_fleet_test

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
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

// The isolated scheduler/fleet adapter tests do not link the typed hchan
// implementation. A direct-channel step would therefore be an invalid fixture
// input; keep the reducer's production dependency explicit and fail closed if
// a test accidentally manufactures one.
func coroMaterializeDirectChannelCompletionV1(*coro.DirectChannelCompletion) bool {
	return false
}

// Named-source adapters exercise the target-neutral scheduler without loading
// runtime2.go and platform getg implementations into the host Go runtime.
// Linked runtime islands separately verify real logical runtime-G ownership.
func coroBindRuntimeContext(task, parent *coro.G, main bool) bool {
	return task != nil
}

func coroBindTaskAllocationRuntimeContext(task, parent *coro.G) bool {
	return task != nil
}

var coroTestRuntimeContextV1 byte

func coroCaptureRuntimeContextV1() unsafe.Pointer {
	return unsafe.Pointer(&coroTestRuntimeContextV1)
}

func coroEnterRuntimeContextFrom(task *coro.G, current unsafe.Pointer) (coroRuntimeContextActivationV1, bool) {
	return coroRuntimeContextActivationV1{}, task != nil && current != nil
}

func coroEnterRuntimeContext(task *coro.G) (coroRuntimeContextActivationV1, bool) {
	return coroEnterRuntimeContextFrom(task, coroCaptureRuntimeContextV1())
}

func coroLeaveRuntimeContext(task *coro.G, activation coroRuntimeContextActivationV1) bool {
	return task != nil
}

func coroReleaseRuntimeContext(task *coro.G, local unsafe.Pointer) bool {
	return task != nil && local != nil
}
