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

const (
	// PhysicalThreadDefaultLimit matches runtime/debug.SetMaxThreads.
	PhysicalThreadDefaultLimit uint32 = 10_000
	// PhysicalThreadMaximumLimit matches the signed runtime maxmcount field.
	PhysicalThreadMaximumLimit uint32 = 1<<31 - 1
)

type physicalThreadCapacityLifecycle uint32

const (
	physicalThreadCapacityUnused physicalThreadCapacityLifecycle = iota
	physicalThreadCapacityActive
	physicalThreadCapacityFailed
)

// PhysicalThreadCapacity is the target-owned physical M/thread ledger. It is
// separate from ExecutionQuota: GOMAXPROCS limits concurrent managed resumes,
// while this ledger covers every live runtime-created OS thread, including
// blocked workers, clean-owner factory threads, and parked standby Ms.
//
// A short atomic guard makes limit changes and create/exit reservations one
// linearizable transaction without a host mutex, allocation, callback, or
// scheduler dependency. No caller may hold the guard across pthread_create,
// join, or a managed suspension.
type PhysicalThreadCapacity struct {
	guard     uint32
	lifecycle uint32
	limit     uint32
	live      uint32
}

func physicalThreadCapacityLock(capacity *PhysicalThreadCapacity) bool {
	if capacity == nil {
		return false
	}
	for !preemptCompareAndSwap(&capacity.guard, 0, 1) {
		if preemptLoad(&capacity.lifecycle) == uint32(physicalThreadCapacityFailed) {
			return false
		}
	}
	return true
}

func physicalThreadCapacityUnlock(capacity *PhysicalThreadCapacity) {
	preemptStore(&capacity.guard, 0)
}

// Start publishes the process-lifetime ledger with the already-running
// program M included. Runtime-created threads reserve before creation and roll
// back the reservation if creation fails.
func (capacity *PhysicalThreadCapacity) Start(live, limit uint32) bool {
	if capacity == nil || live == 0 || limit == 0 ||
		limit > PhysicalThreadMaximumLimit || live > limit ||
		!physicalThreadCapacityLock(capacity) {
		return false
	}
	ok := preemptLoad(&capacity.lifecycle) == uint32(physicalThreadCapacityUnused) &&
		preemptLoad(&capacity.limit) == 0 && preemptLoad(&capacity.live) == 0
	if ok {
		preemptStore(&capacity.limit, limit)
		preemptStore(&capacity.live, live)
		preemptStore(&capacity.lifecycle, uint32(physicalThreadCapacityActive))
	}
	physicalThreadCapacityUnlock(capacity)
	return ok
}

// Reserve accounts for one new physical thread before its creation.
// accepted=false, ok=true is ordinary SetMaxThreads capacity exhaustion.
func (capacity *PhysicalThreadCapacity) Reserve() (accepted, ok bool) {
	if !physicalThreadCapacityLock(capacity) {
		return false, false
	}
	if preemptLoad(&capacity.lifecycle) == uint32(physicalThreadCapacityActive) {
		limit := preemptLoad(&capacity.limit)
		live := preemptLoad(&capacity.live)
		ok = limit != 0 && live != 0 && live <= limit
		if ok && live < limit {
			preemptStore(&capacity.live, live+1)
			accepted = true
		}
	}
	physicalThreadCapacityUnlock(capacity)
	return accepted, ok
}

// Release accounts for one physical thread only after join, or immediately
// before a permanently retired M invokes pthread_exit. The program M remains
// included for the process lifetime and therefore cannot be released here.
func (capacity *PhysicalThreadCapacity) Release() bool {
	if !physicalThreadCapacityLock(capacity) {
		return false
	}
	ok := false
	if preemptLoad(&capacity.lifecycle) == uint32(physicalThreadCapacityActive) {
		live := preemptLoad(&capacity.live)
		if live > 1 {
			preemptStore(&capacity.live, live-1)
			ok = true
		}
	}
	physicalThreadCapacityUnlock(capacity)
	return ok
}

// SetLimit implements the atomic state change underlying
// runtime/debug.SetMaxThreads. within reports whether the current physical
// thread count satisfies the new limit. The standard API makes a false result
// process-fatal after the new value has linearized.
func (capacity *PhysicalThreadCapacity) SetLimit(
	limit uint32,
) (previous, live uint32, within, ok bool) {
	if capacity == nil || limit > PhysicalThreadMaximumLimit ||
		!physicalThreadCapacityLock(capacity) {
		return 0, 0, false, false
	}
	if preemptLoad(&capacity.lifecycle) == uint32(physicalThreadCapacityActive) {
		previous = preemptLoad(&capacity.limit)
		live = preemptLoad(&capacity.live)
		ok = previous != 0 && live != 0
		if ok {
			preemptStore(&capacity.limit, limit)
			within = limit != 0 && live <= limit
		}
	}
	physicalThreadCapacityUnlock(capacity)
	return previous, live, within, ok
}

// Usage returns one coherent diagnostic snapshot.
func (capacity *PhysicalThreadCapacity) Usage() (limit, live uint32, ok bool) {
	if !physicalThreadCapacityLock(capacity) {
		return 0, 0, false
	}
	if preemptLoad(&capacity.lifecycle) == uint32(physicalThreadCapacityActive) {
		limit = preemptLoad(&capacity.limit)
		live = preemptLoad(&capacity.live)
		ok = limit != 0 && live != 0
	}
	physicalThreadCapacityUnlock(capacity)
	return limit, live, ok
}
