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

// ExecutionQuota is the process-level managed-execution gate shared by a
// bounded executor fleet. It limits only entry into a physical coroutine
// resume. An executor which has no permit remains alive and may continue to
// service its route-local timer, poll, channel, cancellation, and transfer
// sources.
//
// This separation keeps every P/route identity stable while GOMAXPROCS changes:
// shrinking the logical execution limit never destroys a source owner or moves
// an outstanding operation. The same acquire/release boundary is also the
// future blocking-compensation handoff point.
//
// All concurrently observed fields are uint32 atomics. Two packed holder bits
// per physical route make a double acquire/release fail closed without a
// dynamic array access, goroutine, thread, callback, or function value in the
// quota object.
type ExecutionQuota struct {
	lifecycle uint32
	limit     uint32
	active    uint32
	waiters   uint32
	holders   uint32
}

type executionQuotaLifecycle uint32

const (
	executionQuotaUnused executionQuotaLifecycle = iota
	executionQuotaActive
	executionQuotaSealed
	executionQuotaRetired
)

type executionQuotaHolderState uint32

const (
	executionQuotaHolderIdle executionQuotaHolderState = iota
	executionQuotaHolderClaiming
	executionQuotaHolderHeld
	executionQuotaHolderReleasing
)

const executionQuotaHolderBits uint32 = 2

func executionQuotaRouteMasks(route RouteID) (holderMask, waiterMask uint32, shift uint32, ok bool) {
	// The packed word deliberately supports at most sixteen physical routes.
	// This scheduler profile currently freezes ExecutorFleetCapacity at eight.
	if route == 0 || uint32(route) > uint32(ExecutorFleetCapacity) ||
		ExecutorFleetCapacity > 32/executionQuotaHolderBits {
		return 0, 0, 0, false
	}
	routeIndex := uint32(route) - 1
	shift = routeIndex * executionQuotaHolderBits
	return uint32(3) << shift, uint32(1) << routeIndex, shift, true
}

func executionQuotaTransitionHolder(
	quota *ExecutionQuota,
	route RouteID,
	from, to executionQuotaHolderState,
) (waiterMask uint32, ok bool) {
	holderMask, waiterMask, shift, valid := executionQuotaRouteMasks(route)
	if quota == nil || !valid {
		return 0, false
	}
	fromBits := uint32(from) << shift
	toBits := uint32(to) << shift
	for {
		holders := preemptLoad(&quota.holders)
		if holders&holderMask != fromBits {
			return 0, false
		}
		next := holders&^holderMask | toBits
		if preemptCompareAndSwap(&quota.holders, holders, next) {
			return waiterMask, true
		}
	}
}

func executionQuotaSetWaiter(quota *ExecutionQuota, mask uint32) bool {
	if quota == nil || mask == 0 {
		return false
	}
	for {
		waiters := preemptLoad(&quota.waiters)
		if waiters&mask != 0 {
			return true
		}
		if preemptCompareAndSwap(&quota.waiters, waiters, waiters|mask) {
			return true
		}
	}
}

func executionQuotaClearWaiter(quota *ExecutionQuota, mask uint32) bool {
	if quota == nil || mask == 0 {
		return false
	}
	for {
		waiters := preemptLoad(&quota.waiters)
		if waiters&mask == 0 {
			return true
		}
		if preemptCompareAndSwap(&quota.waiters, waiters, waiters&^mask) {
			return true
		}
	}
}

func executionQuotaReleaseActive(quota *ExecutionQuota) bool {
	if quota == nil {
		return false
	}
	for {
		active := preemptLoad(&quota.active)
		if active == 0 {
			return false
		}
		if preemptCompareAndSwap(&quota.active, active, active-1) {
			return true
		}
	}
}

// Start publishes the first and only active lifetime. limit is a logical
// GOMAXPROCS value and may exceed the fixed physical fleet capacity; actual
// concurrent execution still cannot exceed the number of physical routes.
func (quota *ExecutionQuota) Start(limit uint32) bool {
	if quota == nil || limit == 0 ||
		preemptLoad(&quota.lifecycle) != uint32(executionQuotaUnused) ||
		preemptLoad(&quota.limit) != 0 || preemptLoad(&quota.active) != 0 ||
		preemptLoad(&quota.waiters) != 0 || preemptLoad(&quota.holders) != 0 {
		return false
	}
	preemptStore(&quota.limit, limit)
	if preemptCompareAndSwap(
		&quota.lifecycle,
		uint32(executionQuotaUnused),
		uint32(executionQuotaActive),
	) {
		return true
	}
	preemptStore(&quota.limit, 0)
	return false
}

// Limit returns the current logical execution limit while the quota is live.
func (quota *ExecutionQuota) Limit() (uint32, bool) {
	if quota == nil || preemptLoad(&quota.lifecycle) != uint32(executionQuotaActive) {
		return 0, false
	}
	limit := preemptLoad(&quota.limit)
	return limit, limit != 0 &&
		preemptLoad(&quota.lifecycle) == uint32(executionQuotaActive)
}

// Usage returns a coherent-enough diagnostic snapshot. Limit changes and
// acquire/release operations each remain individually linearizable; callers
// must not use the pair as a new admission protocol.
func (quota *ExecutionQuota) Usage() (limit, active uint32, ok bool) {
	limit, ok = quota.Limit()
	if !ok {
		return 0, 0, false
	}
	active = preemptLoad(&quota.active)
	if preemptLoad(&quota.lifecycle) != uint32(executionQuotaActive) {
		return 0, 0, false
	}
	return limit, active, true
}

// SetLimit atomically changes the logical execution limit and returns the
// previous value. A shrink never revokes an in-flight resume; it prevents new
// acquisitions until active falls below the new limit. wake reports whether a
// blocked route may need its retained doorbell rung after a growth.
func (quota *ExecutionQuota) SetLimit(limit uint32) (previous uint32, wake, ok bool) {
	if quota == nil || limit == 0 ||
		preemptLoad(&quota.lifecycle) != uint32(executionQuotaActive) {
		return 0, false, false
	}
	for {
		previous = preemptLoad(&quota.limit)
		if previous == 0 {
			return 0, false, false
		}
		if previous == limit ||
			preemptCompareAndSwap(&quota.limit, previous, limit) {
			if preemptLoad(&quota.lifecycle) != uint32(executionQuotaActive) {
				return 0, false, false
			}
			return previous, limit > previous && preemptLoad(&quota.waiters) != 0, true
		}
	}
}

// TryAcquire attempts to grant one exact physical route permission to enter a
// managed coroutine resume. acquired=false, ok=true is ordinary quota
// contention. The waiter bit is published before the final availability
// recheck, closing the release-before-sleep lost-wake window.
func (quota *ExecutionQuota) TryAcquire(route RouteID) (acquired, ok bool) {
	if quota == nil ||
		preemptLoad(&quota.lifecycle) != uint32(executionQuotaActive) {
		return false, false
	}
	mask, claimed := executionQuotaTransitionHolder(
		quota,
		route,
		executionQuotaHolderIdle,
		executionQuotaHolderClaiming,
	)
	if !claimed {
		return false, false
	}
	for {
		if preemptLoad(&quota.lifecycle) != uint32(executionQuotaActive) {
			_ = executionQuotaClearWaiter(quota, mask)
			_, _ = executionQuotaTransitionHolder(
				quota,
				route,
				executionQuotaHolderClaiming,
				executionQuotaHolderIdle,
			)
			return false, false
		}
		limit := preemptLoad(&quota.limit)
		active := preemptLoad(&quota.active)
		if limit != 0 && active < limit {
			if !preemptCompareAndSwap(&quota.active, active, active+1) {
				continue
			}
			// A concurrent shrink which linearized before the active CAS must
			// prevent this later acquisition. A shrink after the CAS observes
			// this resume as already in flight and intentionally does not revoke it.
			live := preemptLoad(&quota.lifecycle) == uint32(executionQuotaActive)
			within := preemptLoad(&quota.limit) >= active+1
			if live && within {
				_, held := executionQuotaTransitionHolder(
					quota,
					route,
					executionQuotaHolderClaiming,
					executionQuotaHolderHeld,
				)
				if held {
					_ = executionQuotaClearWaiter(quota, mask)
					return true, true
				}
			}
			_ = executionQuotaReleaseActive(quota)
			_ = executionQuotaClearWaiter(quota, mask)
			_, _ = executionQuotaTransitionHolder(
				quota,
				route,
				executionQuotaHolderClaiming,
				executionQuotaHolderIdle,
			)
			return false, live
		}
		if !executionQuotaSetWaiter(quota, mask) {
			_, _ = executionQuotaTransitionHolder(
				quota,
				route,
				executionQuotaHolderClaiming,
				executionQuotaHolderIdle,
			)
			return false, false
		}
		if preemptLoad(&quota.lifecycle) != uint32(executionQuotaActive) {
			_ = executionQuotaClearWaiter(quota, mask)
			_, _ = executionQuotaTransitionHolder(
				quota,
				route,
				executionQuotaHolderClaiming,
				executionQuotaHolderIdle,
			)
			return false, false
		}
		if preemptLoad(&quota.active) < preemptLoad(&quota.limit) {
			_ = executionQuotaClearWaiter(quota, mask)
			continue
		}
		if _, abandoned := executionQuotaTransitionHolder(
			quota,
			route,
			executionQuotaHolderClaiming,
			executionQuotaHolderIdle,
		); !abandoned {
			return false, false
		}
		return false, true
	}
}

// Release closes the exact route's physical resume interval. wake is a sticky
// hint: ringing all bounded route doorbells is safe, and each contender clears
// only its own waiter bit after it successfully rechecks or acquires.
func (quota *ExecutionQuota) Release(route RouteID) (wake, ok bool) {
	lifecycle := executionQuotaLifecycle(preemptLoad(&quota.lifecycle))
	if lifecycle != executionQuotaActive && lifecycle != executionQuotaSealed {
		return false, false
	}
	if _, marked := executionQuotaTransitionHolder(
		quota,
		route,
		executionQuotaHolderHeld,
		executionQuotaHolderReleasing,
	); !marked || !executionQuotaReleaseActive(quota) {
		return false, false
	}
	if _, released := executionQuotaTransitionHolder(
		quota,
		route,
		executionQuotaHolderReleasing,
		executionQuotaHolderIdle,
	); !released {
		return false, false
	}
	return lifecycle == executionQuotaActive && preemptLoad(&quota.waiters) != 0, true
}

// Seal prevents new managed resumes. Existing holders may still release.
func (quota *ExecutionQuota) Seal() (wake, ok bool) {
	if quota == nil || !preemptCompareAndSwap(
		&quota.lifecycle,
		uint32(executionQuotaActive),
		uint32(executionQuotaSealed),
	) {
		return false, false
	}
	return preemptLoad(&quota.waiters) != 0, true
}

func (quota *ExecutionQuota) Quiesced() bool {
	if quota == nil ||
		preemptLoad(&quota.lifecycle) != uint32(executionQuotaSealed) ||
		preemptLoad(&quota.active) != 0 ||
		preemptLoad(&quota.holders) != 0 {
		return false
	}
	return true
}

// Retire leaves a permanent process-lifetime tombstone after every physical
// holder has joined. Delayed contenders can only observe rejection.
func (quota *ExecutionQuota) Retire() bool {
	if !quota.Quiesced() {
		return false
	}
	for {
		waiters := preemptLoad(&quota.waiters)
		if waiters == 0 || preemptCompareAndSwap(&quota.waiters, waiters, 0) {
			break
		}
	}
	return preemptCompareAndSwap(
		&quota.lifecycle,
		uint32(executionQuotaSealed),
		uint32(executionQuotaRetired),
	)
}

func (quota *ExecutionQuota) CanRelease() bool {
	if quota == nil {
		return false
	}
	lifecycle := executionQuotaLifecycle(preemptLoad(&quota.lifecycle))
	return lifecycle == executionQuotaUnused &&
		preemptLoad(&quota.limit) == 0 &&
		preemptLoad(&quota.active) == 0 &&
		preemptLoad(&quota.waiters) == 0 &&
		preemptLoad(&quota.holders) == 0 ||
		lifecycle == executionQuotaRetired &&
			preemptLoad(&quota.active) == 0 &&
			preemptLoad(&quota.waiters) == 0 &&
			preemptLoad(&quota.holders) == 0
}
