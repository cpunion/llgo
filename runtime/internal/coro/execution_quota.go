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
// bounded executor fleet. A held route represents one physical owner leasing
// a logical P across a bounded scheduler run slice, rather than a lock acquired
// around every individual llvm.coro.resume. An executor which has no lease
// remains alive and may continue to service route-local timer, poll, channel,
// cancellation, and transfer sources until it first needs managed execution.
//
// This separation keeps every P/route identity stable while GOMAXPROCS changes:
// shrinking the logical execution limit never destroys a source owner or moves
// an outstanding operation. The same acquire/release boundary is also the
// blocking-call compensation temporarily hands this same lease to a replacement
// physical owner and reacquires it before the suspended owner resumes Go code.
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

// BindExecutorServicePressure gives one already-bound executor a read-only
// view of this quota's waiter publication word. The quota and driver must both
// remain at stable addresses through driver retirement. This is startup-only:
// a runtime binds every fleet route after Start and before any managed resume.
//
// The word is advisory, not a second admission gate. A compiler safepoint only
// uses a nonzero value to return to the scheduler; TryAcquire and Release remain
// the sole owners of managed-execution admission.
func BindExecutorServicePressure(driver *ExecutorDriver, quota *ExecutionQuota) bool {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		driver.servicePressure != nil || quota == nil ||
		preemptLoad(&quota.lifecycle) != uint32(executionQuotaActive) {
		return false
	}
	driver.servicePressure = &quota.waiters
	return true
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

// WaiterMask returns the exact bounded physical routes which published quota
// contention. It is an advisory wake snapshot, not a second admission gate:
// each route still rechecks TryAcquire after its retained doorbell fires.
// A contender which races after this snapshot observes newly available quota
// in TryAcquire's final recheck and therefore cannot lose a wake.
func (quota *ExecutionQuota) WaiterMask() (uint32, bool) {
	if quota == nil || preemptLoad(&quota.lifecycle) != uint32(executionQuotaActive) {
		return 0, false
	}
	mask := preemptLoad(&quota.waiters)
	if preemptLoad(&quota.lifecycle) != uint32(executionQuotaActive) {
		return 0, false
	}
	return mask & ((uint32(1) << uint32(ExecutorFleetCapacity)) - 1), true
}

// Held reports whether the exact physical route currently owns its P lease.
// Idle and Held are stable query results; Claiming/Releasing is an in-flight
// same-route ownership transition and therefore fails closed for an owner-side
// handoff decision.
func (quota *ExecutionQuota) Held(route RouteID) (held, ok bool) {
	if quota == nil {
		return false, false
	}
	lifecycle := executionQuotaLifecycle(preemptLoad(&quota.lifecycle))
	if lifecycle != executionQuotaActive && lifecycle != executionQuotaSealed {
		return false, false
	}
	holderMask, _, shift, valid := executionQuotaRouteMasks(route)
	if !valid {
		return false, false
	}
	state := executionQuotaHolderState((preemptLoad(&quota.holders) & holderMask) >> shift)
	if current := executionQuotaLifecycle(preemptLoad(&quota.lifecycle)); current != lifecycle {
		return false, false
	}
	switch state {
	case executionQuotaHolderIdle:
		return false, true
	case executionQuotaHolderHeld:
		return true, true
	default:
		return false, false
	}
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

// TryAcquire attempts to grant one exact physical route a bounded P lease.
// acquired=false, ok=true is ordinary quota
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
	// A route whose bit was already published is an awakened contender. A
	// freshly released holder has no bit; if it immediately races back into
	// TryAcquire while another route is waiting, it must join the waiter set
	// instead of repeatedly stealing the permit before that owner's doorbell
	// wake can run. This is advisory FIFO at the physical-route boundary, not a
	// ticket lock: uncontended acquisition retains the one-CAS fast path.
	wasWaiting := preemptLoad(&quota.waiters)&mask != 0
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
			if !wasWaiting && preemptLoad(&quota.waiters)&^mask != 0 {
				if !executionQuotaSetWaiter(quota, mask) {
					_, _ = executionQuotaTransitionHolder(
						quota,
						route,
						executionQuotaHolderClaiming,
						executionQuotaHolderIdle,
					)
					return false, false
				}
				if _, deferred := executionQuotaTransitionHolder(
					quota,
					route,
					executionQuotaHolderClaiming,
					executionQuotaHolderIdle,
				); !deferred {
					return false, false
				}
				return false, true
			}
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

// Release closes the exact route's bounded P lease. wake is a sticky hint that
// the caller must snapshot and ring the exact waiter routes. Each contender
// clears only its own waiter bit after it successfully rechecks or acquires.
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
