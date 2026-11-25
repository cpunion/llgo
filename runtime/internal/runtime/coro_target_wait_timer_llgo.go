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
	"github.com/xgo-dev/llgo/runtime/internal/coro"
	"github.com/xgo-dev/llgo/runtime/internal/coroclock"
	"github.com/xgo-dev/llgo/runtime/internal/corodoorbell"
)

const coroNativePollSetEntriesV1 = coroNativePollCapacityV1 + 1

var (
	_ [corodoorbell.PollSetCapacity - coroNativePollSetEntriesV1]byte
	_ [coroNativePollSetEntriesV1 - corodoorbell.PollSetCapacity]byte

	// The single native executor owns these reusable fixed-profile buffers.
	// Keeping them out of a coroutine or host call frame preserves the stackless
	// memory model even when all 1024 logical operations are active.
	coroNativePollEntriesV1   [coroNativePollSetEntriesV1]corodoorbell.PollFD
	coroNativePollIDsV2       [coroNativePollCapacityV1]coro.OperationID
	coroNativePollPrevCountV1 uint32
)

const coroNativePollFastProbeBudgetV1 uint32 = 1

func coroTargetBuildPollSnapshotV1(
	pipe *corodoorbell.Pipe,
	deadline int64,
	hasDeadline bool,
	checkDeadline bool,
) (uint32, bool) {
	if pipe == nil {
		return 0, false
	}
	doorbellFD, ok := pipe.ReadFD()
	if !ok {
		return 0, false
	}
	entries := &coroNativePollEntriesV1
	operations := &coroNativePollIDsV2
	entries[0] = corodoorbell.PollFD{FD: doorbellFD, Events: corodoorbell.PollRead}
	count := uint32(1)
	configuredCapacity := coro.PollOperationConfiguredCapacity(&coroProgramPollSourceV1State)
	if configuredCapacity == 0 || configuredCapacity > coroNativePollCapacityV1 {
		return 0, false
	}
	scanLimit, scanOK := coro.PollOperationScanLimit(&coroProgramPollSourceV1State)
	if !scanOK || scanLimit > configuredCapacity {
		return 0, false
	}
	for index := uint32(0); index < scanLimit; index++ {
		snapshot, active, snapshotOK := coro.SnapshotExecutorPollOperation(&coroProgramExecutorDriverV1State, index)
		if !snapshotOK {
			return 0, false
		}
		if !active {
			continue
		}
		if checkDeadline && snapshot.Deadline > 0 && (!hasDeadline || snapshot.Deadline < deadline) {
			// The source-set aggregate deadline is authoritative. A snapshot
			// earlier than it means the idle transaction was not coherent.
			return 0, false
		}
		events := int16(0)
		switch snapshot.Interest {
		case coro.PollInterestRead:
			events = corodoorbell.PollRead
		case coro.PollInterestWrite:
			events = corodoorbell.PollWrite
		default:
			return 0, false
		}
		entries[count] = corodoorbell.PollFD{FD: snapshot.FD, Events: events}
		operations[count-1] = snapshot.ID
		count++
	}
	// Clear only stale slots left over from a wider previous snapshot instead of
	// zeroing all 1025 entries on every poll wait.
	prev := coroNativePollPrevCountV1
	for index := count; index < prev; index++ {
		entries[index] = corodoorbell.PollFD{}
		operations[index-1] = coro.OperationID{}
	}
	coroNativePollPrevCountV1 = count
	return count, true
}

// coroNativeTryFastPollCompletionV1 performs a tiny bounded non-blocking probe
// of active poll descriptors before entering the full idle/sleep transition.
// On loopback TCP where kernel delivery completes within a few microseconds,
// this avoids PrepareExecutorSleepAt/CommitExecutorSleepAt/WakeExecutorAt,
// 3x monotonic clock reads, ExecutionQuota release/reacquire, and a full Mach
// thread sleep/wakeup round-trip while immediately falling back to blocking
// sleep whenever no descriptor becomes ready.
func coroNativeTryFastPollCompletionV1(driver *coro.ExecutorDriver) (ready, ok bool) {
	woke, posted, valid := coroNativeTryFastPollCompletionDetailedV1(driver)
	return woke || posted, valid
}

func coroNativeTryFastPollCompletionDetailedV1(driver *coro.ExecutorDriver) (woke, posted, ok bool) {
	if driver != &coroProgramExecutorDriverV1State {
		return false, false, true
	}
	scanLimit, scanOK := coro.PollOperationScanLimit(&coroProgramPollSourceV1State)
	if !scanOK {
		return false, false, false
	}
	if scanLimit == 0 {
		return false, false, true
	}
	domain, domainOK := coroNativeFleetActiveDomainForRouteV1(1)
	if !domainOK {
		return false, false, true
	}
	pipe := &domain.doorbell
	count, built := coroTargetBuildPollSnapshotV1(pipe, 0, false, false)
	if !built || count <= 1 {
		return false, false, built
	}
	entries := &coroNativePollEntriesV1
	for attempt := uint32(0); attempt < coroNativePollFastProbeBudgetV1; attempt++ {
		for index := uint32(0); index < count; index++ {
			entries[index].Revents = 0
		}
		nReady, errno := corodoorbell.WaitPollSet(&entries[0], count, 0)
		if nReady < 0 {
			if corodoorbell.PollInterrupted(errno) {
				continue
			}
			// Let the authoritative blocking wait path classify descriptor errors.
			return false, false, true
		}
		if nReady == 0 {
			continue
		}
		if entries[0].Revents&corodoorbell.PollBadFD != 0 {
			return false, false, true
		}
		if entries[0].Revents&(corodoorbell.PollRead|corodoorbell.PollError|corodoorbell.PollHangup) != 0 {
			if !pipe.Drain() {
				return false, false, false
			}
			woke = true
		}
		var postOK bool
		posted, postOK = coroTargetPostPollEventsV1(count)
		if !postOK {
			return false, false, false
		}
		if woke || posted {
			return woke, posted, true
		}
		return false, false, true
	}
	return false, false, true
}

func coroTargetWaitExecutorV1(pipe *corodoorbell.Pipe, deadline int64, hasDeadline bool) bool {
	if pipe == nil || deadline < 0 || !hasDeadline && deadline != 0 {
		return false
	}
	count, ok := coroTargetBuildPollSnapshotV1(pipe, deadline, hasDeadline, true)
	if !ok {
		return false
	}
	entries := &coroNativePollEntriesV1

	for {
		if retained, retainedOK := pipe.ConsumeRetainedWake(); !retainedOK {
			return false
		} else if retained {
			return true
		}

		timeoutMS := corodoorbell.PollFaultContainmentMilliseconds
		if hasDeadline {
			now, clockOK := coroclock.MonotonicNano()
			if !clockOK {
				return false
			}
			var reached bool
			timeoutMS, reached, ok = corodoorbell.DeadlinePollTimeout(now, deadline)
			if !ok {
				return false
			}
			if reached {
				// A fresh WakeExecutorAt sample returns all due timer and
				// poll deadlines to the unified reducer; a physical timeout is
				// not itself a completion.
				return true
			}
		}
		for index := uint32(0); index < count; index++ {
			entries[index].Revents = 0
		}
		ready, errno := corodoorbell.WaitPollSet(&entries[0], count, timeoutMS)
		if ready < 0 {
			if corodoorbell.PollInterrupted(errno) {
				continue
			}
			return false
		}
		if ready == 0 {
			continue
		}

		woke := false
		if entries[0].Revents&corodoorbell.PollBadFD != 0 {
			return false
		}
		if entries[0].Revents&(corodoorbell.PollRead|corodoorbell.PollError|corodoorbell.PollHangup) != 0 {
			if !pipe.Drain() {
				return false
			}
			woke = true
		}
		posted, postOK := coroTargetPostPollEventsV1(count)
		if !postOK {
			return false
		}
		if woke || posted {
			return true
		}
		return false
	}
}
