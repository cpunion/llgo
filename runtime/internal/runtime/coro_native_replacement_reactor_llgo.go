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

import "github.com/goplus/llgo/runtime/internal/coro"

type coroNativeReplacementArmedWaitV1 struct {
	physical coroNativeFleetPhysicalWaitV1
	slot     uint32
}

type coroNativeReplacementWaitResultV1 uint8

const (
	coroNativeReplacementWaitInvalidV1 coroNativeReplacementWaitResultV1 = iota
	coroNativeReplacementWaitRetryV1
	coroNativeReplacementWaitWakeV1
	coroNativeReplacementWaitDeadlineV1
)

func coroNativeReplacementDisarmWaitV1(
	domain *coroNativeFleetDomainV1,
	slot uint32,
) bool {
	return domain != nil && slot != 0 &&
		coroNativeAtomicCASV1(&domain.borrowedWait, slot, 0)
}

// coroNativeReplacementPrepareWaitV1 publishes borrowedWait before its final
// owner-state recheck. Requests published before the marker are observed by
// ExecutorOwnerWaitPending; later requests see the marker and ring this
// route's retained doorbell even though the logical executor remains Active.
func coroNativeReplacementPrepareWaitV1(
	handle coro.ExecutorFleetHandle,
	slot uint32,
) (wait coroNativeReplacementArmedWaitV1, shouldWait, ok bool) {
	domain, _, valid := coroNativeFleetWaitStorageV1(handle)
	if !valid || slot == 0 ||
		coroNativeAtomicLoadV1(
			&coroNativeMDirectoryV1State.active[handle.Route-1],
		) != slot ||
		!coroNativeAtomicCASV1(&domain.borrowedWait, 0, slot) {
		return wait, false, false
	}
	driver := domain.driverOwnerV1()
	if pending, pendingOK := coro.ExecutorOwnerWaitPending(driver); !pendingOK {
		_ = coroNativeReplacementDisarmWaitV1(domain, slot)
		return wait, false, false
	} else if pending {
		if !coroNativeReplacementDisarmWaitV1(domain, slot) {
			return wait, false, false
		}
		return wait, false, true
	}

	deadline, hasDeadline, deadlineOK := coro.NextExecutorTimerDeadline(driver)
	if !deadlineOK || deadline < 0 || !hasDeadline && deadline != 0 {
		_ = coroNativeReplacementDisarmWaitV1(domain, slot)
		return wait, false, false
	}
	physical, built := coroNativeFleetBuildPhysicalWaitV1(
		handle,
		deadline,
		hasDeadline,
	)
	if !built || coroNativeAtomicLoadV1(&domain.borrowedWait) != slot ||
		coroNativeAtomicLoadV1(
			&coroNativeMDirectoryV1State.active[handle.Route-1],
		) != slot {
		_ = coroNativeReplacementDisarmWaitV1(domain, slot)
		return wait, false, false
	}
	return coroNativeReplacementArmedWaitV1{
		physical: physical,
		slot:     slot,
	}, true, true
}

// coroNativeReplacementWaitPassAtV1 performs one finite raw poll pass while
// the replacement keeps the logical driver active. It never calls
// PrepareExecutorStandby, CommitSleep, WakeExecutorAt, or finishes the peer's
// owner epoch. Deadline merely asks the common bounded reducer to service its
// unified source transaction on the next pass.
func coroNativeReplacementWaitPassAtV1(
	wait coroNativeReplacementArmedWaitV1,
	now int64,
) coroNativeReplacementWaitResultV1 {
	domain, _, ok := coroNativeFleetWaitStorageV1(wait.physical.handle)
	if !ok || wait.slot == 0 || now < 0 ||
		coroNativeAtomicLoadV1(&domain.borrowedWait) != wait.slot ||
		coroNativeAtomicLoadV1(
			&coroNativeMDirectoryV1State.active[wait.physical.handle.Route-1],
		) != wait.slot {
		return coroNativeReplacementWaitInvalidV1
	}
	switch coroNativeFleetWaitPhysicalPassAtV1(
		wait.physical,
		now,
		coroNativeFleetPhysicalWaitActiveV1,
	) {
	case coroNativeFleetPhysicalWaitRetryV1:
		return coroNativeReplacementWaitRetryV1
	case coroNativeFleetPhysicalWaitWakeV1:
		return coroNativeReplacementWaitWakeV1
	case coroNativeFleetPhysicalWaitDeadlineV1:
		return coroNativeReplacementWaitDeadlineV1
	default:
		return coroNativeReplacementWaitInvalidV1
	}
}
