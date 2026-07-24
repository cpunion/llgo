//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !wasm && !coro_runtime_adapter_test

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

// The stackless coroutine architecture does not install the legacy SA_SIGINFO
// callback that walks a native stack and turns a hardware fault into a Go
// panic. Language nil/bounds/divide faults use compiler-owned ExplicitStatus
// edges instead. Keep the ordinary panic traceback hook link-complete while
// reporting that no legacy hardware-fault snapshot exists.
func faultTraceback(skip int) bool {
	_ = skip
	return false
}

func clearFaultTraceback() {}

func faultTracebackActive() bool {
	return false
}
