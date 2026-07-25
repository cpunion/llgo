//go:build llgo && llgo_coro && !baremetal && !coro_runtime_adapter_test && (wasm || tinygo.wasm || llgo_coro_host)

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

import corort "github.com/goplus/llgo/runtime/internal/runtime"

func coroRealtime() (sec int64, nsec int32) {
	sec, nsec, ok := corort.CoroWallTime()
	if !ok {
		// Targets without a civil-time source retain the existing bare-metal
		// convention of the Unix epoch. Monotonic time remains available and
		// is still attached by time_now for duration and deadline arithmetic.
		return 0, 0
	}
	return sec, nsec
}
