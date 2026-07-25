//go:build llgo && llgo_coro && (wasip1 || wasi || wasip2) && (wasm || tinygo.wasm) && !baremetal && !coro_runtime_adapter_test

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

// WASI owns a command/reactor loop outside the runtime scheduler entry.
// Schedule maps to a later reactor turn and Alarm to a clock subscription.
// poll_oneoff may be used by that host-owned reactor, but never while a
// RunSlice/Continue activation still owns the scheduler.
const coroHostPlatformProfileV1 = coroHostProfileWASIV1 |
	coroHostCapabilityScheduleV1 | coroHostCapabilityAlarmV1 | coroHostCapabilityReactorPollV1 |
	coroHostCapabilityOperationV1 | coroHostCapabilityExternalReactorV1
