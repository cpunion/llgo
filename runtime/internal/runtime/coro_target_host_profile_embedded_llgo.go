//go:build llgo && llgo_coro && llgo_coro_host && !wasm && !baremetal && !coro_runtime_adapter_test && !(llgo_coro_native_pipe && (darwin || linux))

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

// An explicit embedded host supplies a main-loop notification and one-shot
// alarm implementation. This profile makes no pthread/RTOS/OS assumption.
const coroHostPlatformProfileV1 = coroHostProfileEmbeddedV1 |
	coroHostCapabilityScheduleV1 | coroHostCapabilityAlarmV1 | coroHostCapabilityExternalReactorV1
