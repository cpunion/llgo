//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !wasm && !tinygo.wasm && !coro_runtime_adapter_test

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
	_ "unsafe"

	rtdebug "github.com/xgo-dev/llgo/runtime/internal/runtime"
)

//go:linkname c_installCoroFaultHandler C.llgo_install_coro_fault_handler
func c_installCoroFaultHandler(cb func(uintptr, uintptr, uintptr, int32, uint32))

//go:linkname c_coroFaultCaptureDone C.llgo_fault_capture_done
func c_coroFaultCaptureDone()

func init() {
	c_installCoroFaultHandler(onCoroFault)
}

// onCoroFault enters the ordinary synchronous panic machinery while the
// nearest physical llvm.coro.resume owns a synthetic defer boundary. Plain Go
// frames below that boundary retain their existing defer/recover semantics;
// an unhandled panic is staged and converted by the compiler resume gate.
func onCoroFault(pc, _, addr uintptr, sig int32, policy uint32) {
	// A zero interrupted PC is still a real signal event on targets whose
	// ucontext adapter does not expose that register. The packed M state keeps
	// the policy authoritative even when the diagnostic PC is unavailable.
	disposition := rtdebug.StoreCoroSignalFault(pc+1, addr, policy)
	if disposition == rtdebug.CoroSignalFaultReject {
		// Returning to the C trampoline restores the default disposition and
		// re-raises this exact signal. In particular, no Go defer/recover can
		// turn an unexpected non-nil fault into a panic without opt-in.
		return
	}
	// Re-arm the handler and unblock fault signals before PanicSignal can
	// siglongjmp to the native coroutine boundary.
	c_coroFaultCaptureDone()
	rtdebug.PanicSignalAt(
		int(sig), addr, disposition == rtdebug.CoroSignalFaultPanicAddress,
	)
}

// Stackless terminal reporting consumes the task/runtime panic-PC store. The
// legacy package-global dynunwind snapshot is deliberately not consulted.
func faultTraceback(skip int) bool {
	_ = skip
	return false
}

func clearFaultTraceback() {}

func faultTracebackActive() bool {
	return false
}
