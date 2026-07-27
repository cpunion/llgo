//go:build !llgo || !llgo_coro || !llgo_coro_native_pipe || !llgo_coro_native_timer || !(darwin || linux) || baremetal || coro_runtime_adapter_test

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

func coroTargetAfterStableRunActionV1(*coro.P, *coro.ExecutorDriver) bool {
	return true
}

func coroTargetDrainProgramTransfersV1(*coro.P, *coro.ExecutorDriver) (bool, bool) {
	return false, true
}

func coroTargetRequestProgramRunnableV1(*coro.P, *coro.ExecutorDriver) bool {
	return true
}

func coroTargetBeforeProgramRunSliceV1(p *coro.P, driver *coro.ExecutorDriver) bool {
	_, ok := coroTargetDrainProgramTransfersV1(p, driver)
	return ok
}

// Single-owner and host-return targets do not have a second physical M to
// claim a detached locked suspension. They retain the existing attached
// semantics until their target adapter explicitly implements this protocol.
func coroTargetPrepareOSThreadSuspendV1(
	*coro.P,
	*coro.ExecutorDriver,
	*coro.G,
	coro.Action,
) (bool, bool) {
	return false, true
}

func coroTargetHandleOSThreadSuspendV1(
	*coro.P,
	*coro.ExecutorDriver,
	*coro.G,
	coro.Action,
) bool {
	return false
}

func coroTargetStopForOSThreadReturnV1(*coro.ExecutorDriver) (bool, bool) {
	return false, true
}

// Single-thread realms have no distinct reusable physical owner to retire.
// Native pthread targets override this hook; host profiles acknowledge the
// logical LockOSThread lease after the core has removed every G/P pointer.
func coroTargetRetirePhysicalOwnerV1(*coro.P, *coro.ExecutorDriver) bool {
	return true
}
