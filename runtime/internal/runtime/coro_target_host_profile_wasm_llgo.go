//go:build llgo && llgo_coro && (wasm || tinygo.wasm) && !js && !wasip1 && !wasi && !wasip2 && !baremetal && !coro_runtime_adapter_test

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

// Unknown/custom wasm environments use only the explicit pull reactor ABI.
// They are not silently treated as either JS microtasks or WASI poll_oneoff.
const coroHostPlatformProfileV1 = coroHostProfileWasmReactorV1 |
	coroHostCapabilityScheduleV1 | coroHostCapabilityAlarmV1 |
	coroHostCapabilityOperationV1 | coroHostCapabilityExternalReactorV1
