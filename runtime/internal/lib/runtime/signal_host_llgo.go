//go:build llgo && llgo_coro && llgo_coro_host && !baremetal && !wasm && !tinygo.wasm && !windows && !coro_runtime_adapter_test && !(llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux))

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

// An explicit host-owned reactor has no process-signal admission protocol.
// Do not start a separate event loop, block its executor, or modify dispositions
// owned by the embedder. Keep the standard hooks link-complete, but reject their
// use until the host ABI supplies an exact signal source and cancellation join.
const hostSignalUnavailable = "runtime: process signals are unavailable in the host-owned coroutine profile"

func signal_enable(uint32)  { throw(hostSignalUnavailable) }
func signal_disable(uint32) { throw(hostSignalUnavailable) }
func signal_ignore(uint32)  { throw(hostSignalUnavailable) }

func signal_ignored(uint32) bool {
	throw(hostSignalUnavailable)
	return false
}

func signal_recv() uint32 {
	throw(hostSignalUnavailable)
	return 0
}

func signalWaitUntilIdle() { throw(hostSignalUnavailable) }
