//go:build (darwin || linux) && !baremetal && (amd64 || arm64)

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

package coro

// ExecutionQuota publishes the logical limit, exact route holders and waiters
// in one word. A successful CAS grants or returns a complete permit. A waiter
// either publishes against the same state the releaser sees or retries against
// a newer state, so releasing a permit cannot race past waiter registration.
type ExecutionQuota struct {
	state uint64
}

const (
	executionQuotaSize              = 8
	executionQuotaRouteBits         = ExecutorFleetCapacity
	executionQuotaHeldMask   uint64 = (1 << executionQuotaRouteBits) - 1
	executionQuotaWaiterMask        = executionQuotaHeldMask << executionQuotaRouteBits
	executionQuotaCountShift        = 2 * executionQuotaRouteBits
	executionQuotaCountMask  uint64 = 0xf << executionQuotaCountShift
	executionQuotaPermit     uint64 = 1 << executionQuotaCountShift
	executionQuotaPhaseShift        = executionQuotaCountShift + 4
	executionQuotaPhaseMask  uint64 = 3 << executionQuotaPhaseShift
	executionQuotaLimitShift        = 32
	executionQuotaLimitMask  uint64 = uint64(^uint32(0)) << executionQuotaLimitShift
)

var _ [executionQuotaLimitShift - executionQuotaPhaseShift - 2]struct{}

func quotaPhase(state uint64) executionQuotaLifecycle {
	return executionQuotaLifecycle((state & executionQuotaPhaseMask) >> executionQuotaPhaseShift)
}

func quotaRouteBit(route RouteID) uint64 {
	if route == 0 || route > RouteID(ExecutorFleetCapacity) {
		return 0
	}
	return uint64(1) << (route - 1)
}

func (quota *ExecutionQuota) hasWaiters() bool {
	return preemptLoad64(&quota.state)&executionQuotaWaiterMask != 0
}

func (quota *ExecutionQuota) Start(limit uint32) bool {
	return quota != nil && limit != 0 && preemptCompareAndSwap64(
		&quota.state, 0, uint64(limit)<<executionQuotaLimitShift|uint64(executionQuotaActive)<<executionQuotaPhaseShift,
	)
}

func (quota *ExecutionQuota) Limit() (uint32, bool) {
	limit, _, ok := quota.Usage()
	return limit, ok
}

func (quota *ExecutionQuota) Usage() (limit, active uint32, ok bool) {
	if quota == nil {
		return 0, 0, false
	}
	state := preemptLoad64(&quota.state)
	if quotaPhase(state) != executionQuotaActive {
		return 0, 0, false
	}
	return uint32(state >> executionQuotaLimitShift), uint32((state & executionQuotaCountMask) >> executionQuotaCountShift), true
}

func (quota *ExecutionQuota) WaiterMask() (uint32, bool) {
	if quota == nil {
		return 0, false
	}
	state := preemptLoad64(&quota.state)
	if quotaPhase(state) != executionQuotaActive {
		return 0, false
	}
	return uint32((state & executionQuotaWaiterMask) >> executionQuotaRouteBits), true
}

func (quota *ExecutionQuota) Held(route RouteID) (held, ok bool) {
	bit := quotaRouteBit(route)
	if quota == nil || bit == 0 {
		return false, false
	}
	state := preemptLoad64(&quota.state)
	phase := quotaPhase(state)
	if phase != executionQuotaActive && phase != executionQuotaSealed {
		return false, false
	}
	return state&bit != 0, true
}

func (quota *ExecutionQuota) SetLimit(limit uint32) (previous uint32, wake, ok bool) {
	if quota == nil || limit == 0 {
		return 0, false, false
	}
	for {
		state := preemptLoad64(&quota.state)
		if quotaPhase(state) != executionQuotaActive {
			return 0, false, false
		}
		previous = uint32(state >> executionQuotaLimitShift)
		next := state&^executionQuotaLimitMask | uint64(limit)<<executionQuotaLimitShift
		if next == state || preemptCompareAndSwap64(&quota.state, state, next) {
			return previous, limit > previous && state&executionQuotaWaiterMask != 0, true
		}
	}
}

func (quota *ExecutionQuota) TryAcquire(route RouteID) (acquired, ok bool) {
	bit := quotaRouteBit(route)
	if quota == nil || bit == 0 {
		return false, false
	}
	waiter := bit << executionQuotaRouteBits
	for {
		state := preemptLoad64(&quota.state)
		if quotaPhase(state) != executionQuotaActive || state&bit != 0 {
			return false, false
		}
		limit := uint32(state >> executionQuotaLimitShift)
		active := uint32((state & executionQuotaCountMask) >> executionQuotaCountShift)
		waiters := state & executionQuotaWaiterMask
		// A route with published debt may compete with other awakened routes.
		// A fresh route joins the waiters before barging past an outstanding wake.
		acquired = active < limit && (waiters == 0 || waiters&waiter != 0)
		next := state | waiter
		if acquired {
			next = (state&^waiter | bit) + executionQuotaPermit
		}
		if next == state || preemptCompareAndSwap64(&quota.state, state, next) {
			return acquired, true
		}
	}
}

func (quota *ExecutionQuota) Release(route RouteID) (wake, ok bool) {
	bit := quotaRouteBit(route)
	if quota == nil || bit == 0 {
		return false, false
	}
	for {
		state := preemptLoad64(&quota.state)
		phase := quotaPhase(state)
		if (phase != executionQuotaActive && phase != executionQuotaSealed) || state&bit == 0 {
			return false, false
		}
		next := (state &^ bit) - executionQuotaPermit
		if preemptCompareAndSwap64(&quota.state, state, next) {
			return phase == executionQuotaActive && state&executionQuotaWaiterMask != 0, true
		}
	}
}

func (quota *ExecutionQuota) Seal() (wake, ok bool) {
	if quota == nil {
		return false, false
	}
	for {
		state := preemptLoad64(&quota.state)
		if quotaPhase(state) != executionQuotaActive {
			return false, false
		}
		next := state&^executionQuotaPhaseMask | uint64(executionQuotaSealed)<<executionQuotaPhaseShift
		if preemptCompareAndSwap64(&quota.state, state, next) {
			return state&executionQuotaWaiterMask != 0, true
		}
	}
}

func (quota *ExecutionQuota) Quiesced() bool {
	if quota == nil {
		return false
	}
	state := preemptLoad64(&quota.state)
	return quotaPhase(state) == executionQuotaSealed && state&executionQuotaHeldMask == 0
}

func (quota *ExecutionQuota) Retire() bool {
	if quota == nil {
		return false
	}
	state := preemptLoad64(&quota.state)
	return quotaPhase(state) == executionQuotaSealed && state&executionQuotaHeldMask == 0 &&
		preemptCompareAndSwap64(&quota.state, state, uint64(executionQuotaRetired)<<executionQuotaPhaseShift)
}

func (quota *ExecutionQuota) CanRelease() bool {
	if quota == nil {
		return false
	}
	state := preemptLoad64(&quota.state)
	return state == 0 || state == uint64(executionQuotaRetired)<<executionQuotaPhaseShift
}
