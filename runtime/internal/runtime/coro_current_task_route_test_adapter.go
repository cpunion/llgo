//go:build coro_runtime_adapter_test || coro_native_fleet_test || coro_channel_adapter_test

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

var (
	coroCurrentTaskTestV1      *coro.G
	coroCurrentTaskRouteTestV1 coro.RouteID
)

func coroCurrentTaskV1() (*coro.G, coro.RouteID) {
	return coroCurrentTaskTestV1, coroCurrentTaskRouteTestV1
}

func coroCurrentTaskRouteV1() coro.RouteID {
	_, route := coroCurrentTaskV1()
	return route
}
