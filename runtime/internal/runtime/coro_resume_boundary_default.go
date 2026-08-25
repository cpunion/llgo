//go:build (!llgo_coro || baremetal || wasm || tinygo.wasm || (!darwin && !linux)) && !coro_runtime_adapter_test && !coro_native_fleet_test

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

	"github.com/xgo-dev/llgo/runtime/internal/coro"
)

func coroHandleResumePhysicalV1(_ *coro.G, handle unsafe.Pointer, _ bool) bool {
	coroHandleResume(handle)
	return true
}
