//go:build (!baremetal && (wasm || tinygo.wasm)) || (go1.23 && llgo && llgo_coro && !coro_runtime_adapter_test && (baremetal || llgo_coro_host))

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

import corort "github.com/xgo-dev/llgo/runtime/internal/runtime"

// WebAssembly and baremetal use the embedding's monotonic clock. The board or
// host publishes a fresh sample before each clean scheduler entry; standard
// timers and the Timer/Park source therefore use one deadline domain. Reading
// the clock neither waits nor invokes a platform callback.
func nanotime1() int64 {
	now, ok := corort.CoroMonotonicNano()
	if !ok {
		throw("runtime: host monotonic clock unavailable")
	}
	return now
}
