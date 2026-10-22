//go:build ignore

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
	"github.com/xgo-dev/llgo/runtime/internal/coro"
	"github.com/xgo-dev/llgo/runtime/internal/corofleet"
)

// Test-island adapters expose only the existing pointer-free control endpoint.
// They do not change production runtime code or bypass its owner/close checks.
func CoroRotationControlRegisterV1() uint64 {
	task, driver, _ := coroCurrentTaskV1()
	id, ok := coro.RegisterCurrentExecutorTaskControl(driver, task)
	if !ok {
		return 0
	}
	return uint64(id.SourceSlot)<<32 | uint64(id.Generation)
}
func CoroRotationControlCloseV1(endpoint uint64) bool {
	task, driver, _ := coroCurrentTaskV1()
	id := coro.OperationID{SourceSlot: uint32(endpoint >> 32), Generation: uint32(endpoint)}
	return coro.BeginCloseCurrentExecutorTaskControl(driver, task, id) && coro.FinishCloseCurrentExecutorTaskControl(driver, task, id)
}

// This raw test-island barrier has no managed safepoint: the locked child must
// finish on this exact M after the foreign caller requests its return.
func CoroRotationWaitParentReturnV1() bool {
	_, driver, _ := coroCurrentTaskV1()
	owner, _, _, _, ok := coroNativeMCurrentOwnerV1(driver)
	if !ok || !owner.baton.Valid() {
		return false
	}
	parent, ok := coroNativeMOwnerForSlotV1(owner.parentSlot)
	if !ok {
		return false
	}
	for !parent.handoff.ReturnRequested(owner.baton) {
		if corofleet.Yield() != 0 {
			return false
		}
	}
	return true
}
