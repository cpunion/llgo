//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test && !coro_native_fleet_test && !coro_channel_adapter_test

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

// coroCurrentTaskRouteV1 resolves producer locality only while a managed
// stackless resume owns the exact executor. Native goroutines, independent
// foreign-thread callbacks, timers, IO reactors, and teardown paths
// deliberately return route zero. A synchronous same-G C-to-Go reentry is
// still inside that managed resume and may inherit its route.
func coroCurrentTaskV1() (*coro.G, coro.RouteID) {
	gp := getg()
	if gp == nil || gp.startfn != nil || gp.startarg == nil {
		return nil, 0
	}
	task := (*coro.G)(gp.startarg)
	if ctx := (*coroRuntimeContext)(coro.TaskLocal(task)); ctx != gp.context || !validCoroRuntimeTaskContext(task, ctx) {
		return nil, 0
	}
	_, _, route, current := coro.CurrentExecutorDriver(task)
	if !current {
		return nil, 0
	}
	return task, route
}

func coroCurrentTaskRouteV1() coro.RouteID {
	_, route := coroCurrentTaskV1()
	return route
}
