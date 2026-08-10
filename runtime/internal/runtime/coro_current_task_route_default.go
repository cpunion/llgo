//go:build (!llgo || !llgo_coro || !llgo_coro_native_pipe || !llgo_coro_native_timer || !(darwin || linux) || baremetal) && !coro_runtime_adapter_test && !coro_native_fleet_test && !coro_channel_adapter_test

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

// Targets without a multi-route fleet have no useful producer-locality
// destination. Keeping this a compile-time zero also avoids treating the
// command/host executor's route-1 identity as a migration contract.
func coroCurrentTaskRouteV1() coro.RouteID {
	return 0
}
