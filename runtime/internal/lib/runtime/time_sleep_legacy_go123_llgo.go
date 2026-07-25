//go:build go1.23 && !baremetal && (!llgo_coro || coro_runtime_adapter_test || (!(llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux)) && !wasm && !tinygo.wasm && !llgo_coro_host))

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

import _ "unsafe"

//go:linkname timeSleep time.Sleep
func timeSleep(ns int64) {
	if ns <= 0 {
		return
	}
	done := make(chan struct{}, 1)
	r := &runtimeTimer{
		when: runtimeNano() + ns,
		f:    timeSleepWake,
		arg:  done,
	}
	startRuntimeTimer(r)
	<-done
	stopRuntimeTimer(r)
}

func timeSleepWake(arg any, _ uintptr, _ int64) {
	ch := arg.(chan struct{})
	ch <- struct{}{}
}
