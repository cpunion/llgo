//go:build !baremetal && (wasm || tinygo.wasm)

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

// A freestanding WebAssembly core module has no process-signal capability.
// Keep the standard runtime hooks link-complete without importing libuv,
// pthread, or a fictitious blocking receive loop.
func signal_enable(uint32)  {}
func signal_disable(uint32) {}
func signal_ignore(uint32)  {}

func signal_ignored(uint32) bool {
	return false
}

func signal_recv() uint32 {
	throw("runtime: process signals are unavailable on this WebAssembly target")
	return 0
}

func signalWaitUntilIdle() {}
