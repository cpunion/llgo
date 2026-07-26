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
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/corodoorbell"
	"github.com/goplus/llgo/runtime/internal/corofleet"
)

const coroNativeMaximumLogicalProcsV1 uint32 = 1<<31 - 1

func coroNativeInitialExecutionLimitV1() (uint32, bool) {
	limit := corofleet.OwnerCount(coroNativeMaximumLogicalProcsV1)
	return limit, limit != 0 && limit <= coroNativeMaximumLogicalProcsV1
}

func coroNativeFleetExecutionDomainV1(
	driver *coro.ExecutorDriver,
) (*coroNativeFleetDomainV1, coro.RouteID, bool) {
	if driver == nil {
		return nil, 0, false
	}
	route, ok := driver.Route()
	if !ok || uint32(route) > coroNativeFleetV1State.domainCount {
		return nil, 0, false
	}
	domain := &coroNativeFleetV1State.domains[uint32(route)-1]
	return domain, route, domain.lifecycle == coroNativeFleetDomainActiveV1 &&
		domain.driverOwnerV1() == driver &&
		domain.handle.Route == uint32(route)
}

func coroNativeFleetRingExecutionWaitersV1() bool {
	state := &coroNativeFleetV1State
	if state.lifecycle != coroNativeFleetActiveV1 ||
		state.domainCount != coroNativeFleetDomainCapacityV1 {
		return false
	}
	for index := uint32(0); index < state.domainCount; index++ {
		domain := &state.domains[index]
		if domain.lifecycle != coroNativeFleetDomainActiveV1 ||
			!domain.doorbell.Ring() {
			return false
		}
	}
	return true
}

func coroTargetAcquireManagedExecutionV1(driver *coro.ExecutorDriver) (bool, bool) {
	_, route, ok := coroNativeFleetExecutionDomainV1(driver)
	if !ok {
		return false, false
	}
	return coroNativeFleetV1State.execution.TryAcquire(route)
}

func coroTargetReleaseManagedExecutionV1(driver *coro.ExecutorDriver) bool {
	_, route, ok := coroNativeFleetExecutionDomainV1(driver)
	if !ok {
		return false
	}
	wake, released := coroNativeFleetV1State.execution.Release(route)
	return released && (!wake || coroNativeFleetRingExecutionWaitersV1())
}

// A route which still owns runnable work waits only on its retained doorbell;
// it does not enter the executor driver's idle protocol. Release or a limit
// increase rings every sticky contender, while the bounded timeout preserves a
// stop/fault recheck even if a platform write is unexpectedly lost.
func coroTargetWaitManagedExecutionV1(driver *coro.ExecutorDriver) bool {
	domain, _, ok := coroNativeFleetExecutionDomainV1(driver)
	if !ok {
		return false
	}
	_, ok = domain.doorbell.WaitBounded(corodoorbell.PollFaultContainmentMilliseconds)
	return ok
}

// coroTargetLeaveManagedExecutionForSameMBlockV1 temporarily returns the
// current route's managed-resume permit while its LockOSThread G executes an
// unavoidable blocking foreign call on the same physical M. The coroutine
// frame and route owner epoch remain exclusively owned by that M; only the
// process-wide execution quota is released, allowing an already-runnable peer
// route to compensate even when GOMAXPROCS is one.
//
// This is deliberately paired with the reacquire function below. The outer
// run-step reducer still owns one permit receipt for this ActionResume and will
// release it after llvm.coro.resume returns, so the same route must reacquire
// before the foreign boundary returns to managed code.
func coroTargetLeaveManagedExecutionForSameMBlockV1(driver *coro.ExecutorDriver) bool {
	return coroTargetReleaseManagedExecutionV1(driver)
}

// coroTargetReenterManagedExecutionAfterSameMBlockV1 restores the exact
// ActionResume permit before the locked G can touch managed state again.
// Contention is ordinary: the route waits on its retained doorbell, which is
// rung by every quota release/expansion, and retries without exposing the
// issued physical action to another scheduler owner.
func coroTargetReenterManagedExecutionAfterSameMBlockV1(driver *coro.ExecutorDriver) bool {
	for {
		acquired, ok := coroTargetAcquireManagedExecutionV1(driver)
		if !ok {
			return false
		}
		if acquired {
			return true
		}
		if !coroTargetWaitManagedExecutionV1(driver) {
			return false
		}
	}
}

// CoroGOMAXPROCS implements the standard runtime.GOMAXPROCS query/set contract
// over the logical managed-execution quota. Values above the current bounded
// physical fleet are retained and reported; the physical topology merely
// provides a lower implementation ceiling and never violates that maximum.
func CoroGOMAXPROCS(n int) int {
	limit, ok := coroNativeFleetV1State.execution.Limit()
	if !ok {
		return 1
	}
	if n <= 0 {
		return int(limit)
	}
	next := uint32(n)
	if uint64(n) > uint64(coroNativeMaximumLogicalProcsV1) {
		next = coroNativeMaximumLogicalProcsV1
	}
	previous, wake, changed := coroNativeFleetV1State.execution.SetLimit(next)
	if !changed || wake && !coroNativeFleetRingExecutionWaitersV1() {
		coroRuntimeAbort("native coroutine execution quota resize failed")
		return int(limit)
	}
	return int(previous)
}
