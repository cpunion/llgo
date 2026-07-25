//go:build llgo && llgo_coro && !coro_runtime_adapter_test && (wasm || tinygo.wasm || baremetal || llgo_coro_host) && !(llgo_coro_native_pipe && (darwin || linux) && !baremetal)

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

import "github.com/goplus/llgo/runtime/internal/coro"

// The host profile owns one exact route. Publish the manual-source completion
// before requesting a pointer-free Schedule obligation from the pull adapter;
// the registry claim alone is not a scheduler-visible completion fact.
func coroTargetPostKeyedOperationV2(id coro.OperationID) bool {
	if !id.Valid() || id.Source() != coro.OperationSourceManual ||
		id.Route() != coro.RouteID(1) || !coroProgramExecutorBoundV1State ||
		coroProgramExecutorHandleV1State == (coro.ExecutorHandle{}) {
		return false
	}
	if coroProgramManualSourceV2State.Post(id) != coro.ManualOperationPosted {
		return false
	}
	return coroTargetRequestExecutorV1(coroProgramExecutorHandleV1State)
}
