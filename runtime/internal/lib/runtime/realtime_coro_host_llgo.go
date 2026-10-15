//go:build llgo && llgo_coro && !coro_runtime_adapter_test && (wasm || tinygo.wasm || baremetal || llgo_coro_host)

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

import corort "github.com/xgo-dev/llgo/runtime/internal/runtime"

func coroRealtime() (sec int64, nsec int32) {
	sec, nsec, ok := corort.CoroWallTime()
	if !ok {
		throw("coro wall time requires a published host clock")
	}
	return sec, nsec
}
