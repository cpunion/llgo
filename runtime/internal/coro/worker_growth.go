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

// WorkerGrowth publishes a native worker factory's scalar creation state.
// Its creator lease may span a physical pthread creation, so contenders never
// spin here: only a fully unwound native owner may wait and retry a Busy result.
// Producers and completion callbacks never acquire this lease.
type WorkerGrowth struct {
	state   uint32
	created uint32
	limit   uint32
}

type WorkerGrowthStatus uint8

const (
	WorkerGrowthInvalid WorkerGrowthStatus = iota
	WorkerGrowthAcquired
	WorkerGrowthBusy
	WorkerGrowthClosed
)

const (
	workerGrowthUnused uint32 = iota
	workerGrowthActive
	workerGrowthCreating
	workerGrowthSealed
	workerGrowthFailed
)

func (growth *WorkerGrowth) CanRelease() bool {
	return growth != nil && preemptLoad(&growth.state) == workerGrowthUnused &&
		preemptLoad(&growth.created) == 0 && preemptLoad(&growth.limit) == 0
}

func (growth *WorkerGrowth) Start(limit uint32) bool {
	if growth == nil || limit == 0 || !growth.CanRelease() ||
		!preemptCompareAndSwap(&growth.state, workerGrowthUnused, workerGrowthCreating) {
		return false
	}
	preemptStore(&growth.limit, limit)
	preemptStore(&growth.state, workerGrowthActive)
	return true
}

func (growth *WorkerGrowth) Snapshot() (created, limit uint32, ok bool) {
	if growth == nil {
		return 0, 0, false
	}
	state := preemptLoad(&growth.state)
	if state != workerGrowthActive && state != workerGrowthCreating {
		return 0, 0, false
	}
	limit = preemptLoad(&growth.limit)
	created = preemptLoad(&growth.created)
	return created, limit, limit != 0 && created <= limit
}

func (growth *WorkerGrowth) TryAcquire() (created, limit uint32, status WorkerGrowthStatus) {
	if growth == nil {
		return 0, 0, WorkerGrowthInvalid
	}
	switch preemptLoad(&growth.state) {
	case workerGrowthCreating:
		return 0, 0, WorkerGrowthBusy
	case workerGrowthSealed, workerGrowthFailed:
		return 0, 0, WorkerGrowthClosed
	case workerGrowthActive:
		if !preemptCompareAndSwap(&growth.state, workerGrowthActive, workerGrowthCreating) {
			return 0, 0, WorkerGrowthBusy
		}
		limit = preemptLoad(&growth.limit)
		created = preemptLoad(&growth.created)
		if limit == 0 || created > limit {
			preemptStore(&growth.state, workerGrowthFailed)
			return 0, 0, WorkerGrowthInvalid
		}
		return created, limit, WorkerGrowthAcquired
	default:
		return 0, 0, WorkerGrowthInvalid
	}
}

// PublishNext follows the target's successful creation and thread-ID store.
// The lease remains held until the creator rechecks current queue demand.
func (growth *WorkerGrowth) PublishNext(next uint32) bool {
	if growth == nil || preemptLoad(&growth.state) != workerGrowthCreating ||
		next == 0 || next > preemptLoad(&growth.limit) ||
		next-1 != preemptLoad(&growth.created) {
		return false
	}
	preemptStore(&growth.created, next)
	return true
}

func (growth *WorkerGrowth) Release() bool {
	return growth != nil && preemptCompareAndSwap(&growth.state, workerGrowthCreating, workerGrowthActive)
}

func (growth *WorkerGrowth) Fail() bool {
	return growth != nil && preemptCompareAndSwap(&growth.state, workerGrowthCreating, workerGrowthFailed)
}

// TrySeal is called only after every scheduling owner has stopped. Busy is
// still explicit so an admitted creation cannot publish after close returns.
func (growth *WorkerGrowth) TrySeal() (created uint32, status WorkerGrowthStatus) {
	if growth == nil {
		return 0, WorkerGrowthInvalid
	}
	switch preemptLoad(&growth.state) {
	case workerGrowthCreating:
		return 0, WorkerGrowthBusy
	case workerGrowthSealed, workerGrowthFailed:
		return 0, WorkerGrowthClosed
	case workerGrowthActive:
		if !preemptCompareAndSwap(&growth.state, workerGrowthActive, workerGrowthSealed) {
			return 0, WorkerGrowthBusy
		}
		created = preemptLoad(&growth.created)
		if preemptLoad(&growth.limit) == 0 || created > preemptLoad(&growth.limit) {
			return 0, WorkerGrowthInvalid
		}
		return created, WorkerGrowthAcquired
	default:
		return 0, WorkerGrowthInvalid
	}
}

func (growth *WorkerGrowth) ResetAfterJoin(joined uint32) bool {
	if growth == nil || preemptLoad(&growth.state) != workerGrowthSealed ||
		preemptLoad(&growth.limit) == 0 || joined != preemptLoad(&growth.created) {
		return false
	}
	preemptStore(&growth.created, 0)
	preemptStore(&growth.limit, 0)
	preemptStore(&growth.state, workerGrowthUnused)
	return true
}
