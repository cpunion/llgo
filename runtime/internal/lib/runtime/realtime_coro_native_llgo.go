//go:build llgo && llgo_coro && !baremetal && !coro_runtime_adapter_test && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !wasm && !tinygo.wasm && !llgo_coro_host

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

import ct "github.com/goplus/llgo/runtime/internal/clite/time"

func coroRealtime() (sec int64, nsec int32) {
	var value ct.Timespec
	ct.ClockGettime(ct.CLOCK_REALTIME, &value)
	return int64(value.Sec), int32(value.Nsec)
}
