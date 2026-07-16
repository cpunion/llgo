//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_ingress_test && (darwin || linux) && !baremetal

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

import "github.com/goplus/llgo/runtime/internal/coro"

// __llgo_coro_native_ingress_audit_closed_v1 is linked only by the focused
// native ingress E2E. Each bit identifies retained state after the production
// runner has returned, so the test verifies more than process exit status.
//
//export __llgo_coro_native_ingress_audit_closed_v1
func __llgo_coro_native_ingress_audit_closed_v1() uint32 {
	var failures uint32
	if coroProgramLifecycleV1State != coroProgramCompleteV1 ||
		coroProgramContinuationV1State != coroProgramContinuationNoneV1 ||
		!coroProgramDriveAdmissionV1State.CanRelease() {
		failures |= 1 << 0
	}
	if coroProgramExecutorBoundV1State || coroProgramExecutorHandleV1State != (coro.ExecutorHandle{}) ||
		coroProgramExecutorDriverV1State != (coro.ExecutorDriver{}) {
		failures |= 1 << 1
	}
	if !coroProgramExecutorRegistryV1State.CanRelease() || !coroProgramWaitTableV1State.CanRelease() {
		failures |= 1 << 2
	}
	state := &coroNativeTargetV1State
	if state.started || state.handle != (coro.ExecutorHandle{}) || state.waitEpoch != 0 {
		failures |= 1 << 3
	}
	if !state.ingress.CanReleaseResources() || !state.ingress.Retired() || state.ingress.Quiesced() {
		failures |= 1 << 4
	}
	if !state.doorbell.Closed() {
		failures |= 1 << 5
	}
	if !coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
		failures |= 1 << 6
	}
	return failures
}
