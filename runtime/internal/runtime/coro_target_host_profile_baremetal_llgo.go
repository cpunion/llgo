//go:build llgo && llgo_coro && baremetal && !coro_runtime_adapter_test

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package runtime

// Baremetal maps Schedule to a sticky notification checked by the next main
// loop iteration and Alarm to a hardware compare. Neither hook may allocate;
// an IRQ publishes POD source state and notification only.
const coroHostPlatformProfileV1 = coroHostProfileBaremetalV1 |
	coroHostCapabilityScheduleV1 | coroHostCapabilityAlarmV1 | coroHostCapabilityExternalReactorV1
