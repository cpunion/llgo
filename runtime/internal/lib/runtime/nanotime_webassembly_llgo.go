//go:build !baremetal && (wasm || tinygo.wasm)

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

import corort "github.com/goplus/llgo/runtime/internal/runtime"

// WebAssembly has no synchronous OS clock requirement in the scheduler core.
// The embedding publishes monotonic time at clean host turns; all standard
// timer code observes that same sample, so deadline calculation and wakeup use
// one clock domain.
func nanotime1() int64 {
	now, ok := corort.CoroMonotonicNano()
	if !ok {
		throw("runtime: host monotonic clock unavailable")
	}
	return now
}
