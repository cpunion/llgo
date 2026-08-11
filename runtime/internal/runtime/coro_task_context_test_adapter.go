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

import "github.com/goplus/llgo/runtime/internal/coro"

// Named-source adapters exercise the target-neutral scheduler without loading
// runtime2.go and platform getg implementations into the host Go runtime.
// Linked runtime islands separately verify real logical runtime-G ownership.
func coroBindRuntimeContext(task, parent *coro.G, main bool) bool {
	return task != nil
}

func coroBindTaskAllocationRuntimeContext(task, parent *coro.G) bool {
	return task != nil
}

func coroEnterRuntimeContext(task *coro.G) (coroRuntimeContextActivationV1, bool) {
	return coroRuntimeContextActivationV1{}, task != nil
}

func coroLeaveRuntimeContext(task *coro.G, activation coroRuntimeContextActivationV1) bool {
	return task != nil
}

func coroReleaseRuntimeContext(task *coro.G) bool {
	return task != nil
}
