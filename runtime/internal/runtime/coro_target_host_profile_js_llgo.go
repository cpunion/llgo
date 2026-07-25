//go:build llgo && llgo_coro && js && wasm && !baremetal && !coro_runtime_adapter_test

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

// JS owns a nonblocking event-loop reactor. Schedule maps to a later microtask
// (never a synchronous wasm re-entry); Alarm maps to a one-shot host timeout.
// Blocking the current JS callback is not a capability of this profile.
const coroHostPlatformProfileV1 = coroHostProfileJSV1 |
	coroHostCapabilityScheduleV1 | coroHostCapabilityAlarmV1 |
	coroHostCapabilityOperationV1 | coroHostCapabilityExternalReactorV1
