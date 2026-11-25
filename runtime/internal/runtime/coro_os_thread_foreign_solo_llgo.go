//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
	"github.com/xgo-dev/llgo/runtime/internal/coroworker"
)

//go:linkname coroWorkerCallSolo4V1 C.__llgo_coro_worker_call_solo4_v1
func coroWorkerCallSolo4V1(
	function, traceTarget uintptr,
	argc uint32,
	a0, a1, a2, a3 uintptr,
	r1Address, r2Address, errnoAddress uintptr,
) uint32

//go:linkname coroWorkerCallSoloV1 C.__llgo_coro_worker_call_solo_v1
func coroWorkerCallSoloV1(
	function, traceTarget uintptr,
	argc uint32,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
	r1Address, r2Address, errnoAddress uintptr,
) uint32

// coroNativeSyscallSoloDirectV1 executes a verified solo llgo.syscall without
// materializing coroNativeForeignBoundaryV1 or coroworker.Result on the native
// stack. Hardware fault attribution remains identical to the general path.
func coroNativeSyscallSoloDirectV1(
	task *coro.G,
	function, traceTarget uintptr,
	argc uint32,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
	r1, r2, errno *uintptr,
) uint32 {
	if function == 0 || traceTarget == 0 || argc > coroworker.MaxArgs ||
		r1 == nil || r2 == nil || errno == nil ||
		r1 == r2 || r1 == errno || r2 == errno {
		coroRuntimeAbort("invalid native direct foreign call")
	}
	var fault uint32
	if argc <= 4 {
		fault = coroWorkerCallSolo4V1(
			function, traceTarget, argc,
			a0, a1, a2, a3,
			uintptr(unsafe.Pointer(r1)),
			uintptr(unsafe.Pointer(r2)),
			uintptr(unsafe.Pointer(errno)),
		)
	} else {
		fault = coroWorkerCallSoloV1(
			function, traceTarget, argc,
			a0, a1, a2, a3, a4, a5, a6, a7, a8,
			uintptr(unsafe.Pointer(r1)),
			uintptr(unsafe.Pointer(r2)),
			uintptr(unsafe.Pointer(errno)),
		)
	}
	if fault == uint32(coroworker.FaultNone) {
		return coroWorkerResumeSuccessV1
	}
	return coroNativeSyscallSoloFaultV1(task, traceTarget, fault, r1)
}

//go:noinline
func coroNativeSyscallSoloFaultV1(
	task *coro.G,
	traceTarget uintptr,
	fault uint32,
	r1 *uintptr,
) uint32 {
	faultPC := *r1
	*r1 = 0
	switch uintptr(fault) {
	case coroworker.FaultMemory:
		if !StoreCoroWorkerFaultPCs(task, faultPC, traceTarget) {
			coroRuntimeAbort("native direct foreign fault has no traceback identity")
		}
		return coroWorkerResumeFaultMemoryV1
	case coroworker.FaultDivide:
		if !StoreCoroWorkerFaultPCs(task, faultPC, traceTarget) {
			coroRuntimeAbort("native direct foreign fault has no traceback identity")
		}
		return coroWorkerResumeFaultDivideV1
	default:
		coroRuntimeAbort("native direct foreign call failed")
		return 0
	}
}
