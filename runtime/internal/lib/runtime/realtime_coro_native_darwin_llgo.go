//go:build llgo && llgo_coro && !baremetal && !coro_runtime_adapter_test && llgo_coro_native_pipe && llgo_coro_native_timer && darwin && !wasm && !tinygo.wasm && !llgo_coro_host

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package runtime

// Darwin defines CLOCK_REALTIME = 0 in <time.h>. Reading it by value through
// clock_gettime_nsec_np avoids taking the address of a stack Timespec (which
// would otherwise heap-allocate per time.Now call) and stays on Darwin's fast
// commpage path.
const _CLOCK_REALTIME = 0

func coroRealtime() (sec int64, nsec int32) {
	ns := c_clock_gettime_nsec_np(_CLOCK_REALTIME)
	return int64(ns / 1_000_000_000), int32(ns % 1_000_000_000)
}
