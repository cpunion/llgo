//go:build llgo && llgo_coro && llgo_coro_native_pipe && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package runtime

import (
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/coroworker"
)

// coroWorkerResultPayloadV1 is the common typed boundary between the native
// worker and both completion-delivery profiles. Keeping it independent of the
// park owner lets closed runtime islands select only the completion closure.
func coroWorkerResultPayloadV1(
	r1, r2, errno, fault, faultPC, faultTarget uintptr,
) (coro.ScalarResultPayloadV1, bool) {
	if fault == coroworker.FaultNone {
		if faultPC != 0 || faultTarget != 0 {
			return coro.ScalarResultPayloadV1{}, false
		}
		return coro.MakeScalarResultPayloadV1(
			coro.ScalarResultKindWords,
			coro.ScalarResultFlags(coroworker.FaultNone),
			3,
			uint64(r1),
			uint64(r2),
			uint64(errno),
		)
	}
	if (fault != coroworker.FaultMemory && fault != coroworker.FaultDivide) ||
		r1 != 0 || r2 != 0 || errno != 0 || faultPC == 0 || faultTarget == 0 {
		return coro.ScalarResultPayloadV1{}, false
	}
	return coro.MakeScalarResultPayloadV1(
		coro.ScalarResultKindWords,
		coro.ScalarResultFlags(fault),
		3,
		uint64(faultPC),
		uint64(faultTarget),
		0,
	)
}
