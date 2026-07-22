//go:build !(llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && llgo_coro_native_fleet && (darwin || linux) && !baremetal && !coro_runtime_adapter_test)

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

// Non-fleet targets own exactly route 1. OperationID still participates in
// validation so a stale or accidentally foreign endpoint cannot fall back to
// the process-global executor handle.
func coroTargetRequestChannelOperationV1(id coro.OperationID) bool {
	return id.Valid() && id.Source() == coro.OperationSourceChannel && id.Route() == coro.RouteID(1) &&
		coroProgramExecutorBoundV1State &&
		coroProgramExecutorHandleV1State != (coro.ExecutorHandle{}) &&
		coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State)
}
