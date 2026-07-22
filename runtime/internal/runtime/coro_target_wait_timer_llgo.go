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
	"github.com/goplus/llgo/runtime/internal/coroclock"
	"github.com/goplus/llgo/runtime/internal/corodoorbell"
)

const coroNativePollSetEntriesV1 = coroNativePollCapacityV1 + 1

var (
	_ [corodoorbell.PollSetCapacity - coroNativePollSetEntriesV1]byte
	_ [coroNativePollSetEntriesV1 - corodoorbell.PollSetCapacity]byte

	// The single native executor owns these reusable fixed-profile buffers.
	// Keeping them out of a coroutine or host call frame preserves the stackless
	// memory model even when all 1024 logical operations are active.
	coroNativePollEntriesV1 [coroNativePollSetEntriesV1]corodoorbell.PollFD
	coroNativePollIDsV2     [coroNativePollCapacityV1]coro.OperationID
)

func coroTargetWaitExecutorV1(pipe *corodoorbell.Pipe, deadline int64, hasDeadline bool) bool {
	if pipe == nil || deadline < 0 || !hasDeadline && deadline != 0 {
		return false
	}
	doorbellFD, ok := pipe.ReadFD()
	if !ok {
		return false
	}

	entries := &coroNativePollEntriesV1
	operations := &coroNativePollIDsV2
	for index := range entries {
		entries[index] = corodoorbell.PollFD{}
	}
	for index := range operations {
		operations[index] = coro.OperationID{}
	}
	entries[0] = corodoorbell.PollFD{FD: doorbellFD, Events: corodoorbell.PollRead}
	count := uint32(1)
	configuredCapacity := coro.PollOperationConfiguredCapacity(&coroProgramPollSourceV1State)
	if configuredCapacity != coroNativePollCapacityV1 {
		return false
	}
	scanLimit, scanOK := coro.PollOperationScanLimit(&coroProgramPollSourceV1State)
	if !scanOK || scanLimit > configuredCapacity {
		return false
	}
	for index := uint32(0); index < scanLimit; index++ {
		snapshot, active, snapshotOK := coro.SnapshotExecutorPollOperation(&coroProgramExecutorDriverV1State, index)
		if !snapshotOK {
			return false
		}
		if !active {
			continue
		}
		if snapshot.Deadline > 0 && (!hasDeadline || snapshot.Deadline < deadline) {
			// The source-set aggregate deadline is authoritative. A snapshot
			// earlier than it means the idle transaction was not coherent.
			return false
		}
		events := int16(0)
		switch snapshot.Interest {
		case coro.PollInterestRead:
			events = corodoorbell.PollRead
		case coro.PollInterestWrite:
			events = corodoorbell.PollWrite
		default:
			return false
		}
		entries[count] = corodoorbell.PollFD{FD: snapshot.FD, Events: events}
		operations[count-1] = snapshot.ID
		count++
	}

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
				// A fresh WakeExecutorAt sample publishes all due timer and poll
				// deadlines; a physical timeout is not itself a completion.
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
		posted := false
		for entry := uint32(1); entry < count; entry++ {
			if entries[entry].Revents == 0 {
				continue
			}
			if result := coro.PostExecutorPollEvent(
				&coroProgramExecutorDriverV1State,
				operations[entry-1],
				coro.PollOperationReady,
			); result != coro.PollOperationPosted {
				return false
			}
			posted = true
		}
		if woke || posted {
			return true
		}
		return false
	}
}
