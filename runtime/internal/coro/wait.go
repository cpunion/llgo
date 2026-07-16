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

// WaitToken is a target-neutral, allocation-free completion cell. A platform
// worker, host callback, RTOS ISR handoff, or bare-metal event source may only
// call CompleteWait; it never touches G/P state or an LLVM coroutine handle.
//
// The generation and state share one atomic word so a late completion cannot
// wake a later reuse of the same cell (the classic cancellation/ABA race). A
// token is intentionally exhausted after 2^29-1 generations rather than
// wrapping and accepting a stale ticket. The additional states atomically
// claim one exact waiter without storing a target-dependent pointer in the
// completion cell. A WaitToken must not be copied after its first ArmWait.
type WaitToken struct {
	word uint32
}

// WaitTicket identifies one exact arm of a WaitToken. The zero value is never
// valid. It is safe to copy into a stable foreign-operation argument record.
type WaitTicket uint32

const (
	waitStateBits = 3
	waitStateMask = 1<<waitStateBits - 1
	waitMaxGen    = ^uint32(0) >> waitStateBits
)

type waitState uint32

const (
	waitUnused waitState = iota
	waitArmed
	waitReady
	waitParked
	waitParkedReady
	waitConsumed
)

func waitWord(generation uint32, state waitState) uint32 {
	return generation<<waitStateBits | uint32(state)
}

func waitGeneration(word uint32) uint32 {
	return word >> waitStateBits
}

func waitWordState(word uint32) waitState {
	return waitState(word & waitStateMask)
}

func validWaitTicket(ticket WaitTicket) bool {
	return ticket != 0 && uint32(ticket) <= waitMaxGen
}

// ArmWait starts one new completion generation. Only the scheduler/operation
// submitter may arm a token, and only while it is unused or fully consumed.
func ArmWait(token *WaitToken) (WaitTicket, bool) {
	if token == nil {
		return 0, false
	}
	for {
		old := preemptLoad(&token.word)
		state := waitWordState(old)
		if state != waitUnused && state != waitConsumed {
			return 0, false
		}
		generation := waitGeneration(old) + 1
		if generation == 0 || generation > waitMaxGen {
			return 0, false
		}
		armed := waitWord(generation, waitArmed)
		if preemptCompareAndSwap(&token.word, old, armed) {
			return WaitTicket(generation), true
		}
	}
}

// CompleteWait publishes completion of one exact generation. Writes to the
// stable result record must happen before this call. The atomic CAS publishes
// them to the scheduler that consumes the ready ticket. Duplicate, stale, and
// not-yet-armed completions fail closed. This operation deliberately touches
// neither P/G queues nor an LLVM handle. After a successful completion, the
// platform adapter separately calls RequestSchedule on the stable owning P and
// wakes its executor; that producer must quiesce before the P can terminate.
func CompleteWait(token *WaitToken, ticket WaitTicket) bool {
	if token == nil || !validWaitTicket(ticket) {
		return false
	}
	generation := uint32(ticket)
	for {
		old := preemptLoad(&token.word)
		if waitGeneration(old) != generation {
			return false
		}
		var ready waitState
		switch waitWordState(old) {
		case waitArmed:
			ready = waitReady
		case waitParked:
			ready = waitParkedReady
		default:
			return false
		}
		if preemptCompareAndSwap(&token.word, old, waitWord(generation, ready)) {
			return true
		}
	}
}

// claimWait binds one exact generation to one scheduler waiter. Completion is
// permitted to race on either side of this transition; the two claimed states
// preserve whether the result was already published. No second G can claim
// the same token/ticket pair.
func claimWait(token *WaitToken, ticket WaitTicket) bool {
	if token == nil || !validWaitTicket(ticket) {
		return false
	}
	generation := uint32(ticket)
	for {
		old := preemptLoad(&token.word)
		if waitGeneration(old) != generation {
			return false
		}
		var claimed waitState
		switch waitWordState(old) {
		case waitArmed:
			claimed = waitParked
		case waitReady:
			claimed = waitParkedReady
		default:
			return false
		}
		if preemptCompareAndSwap(&token.word, old, waitWord(generation, claimed)) {
			return true
		}
	}
}

func validClaimedWait(token *WaitToken, ticket WaitTicket) bool {
	if token == nil || !validWaitTicket(ticket) {
		return false
	}
	word := preemptLoad(&token.word)
	if waitGeneration(word) != uint32(ticket) {
		return false
	}
	state := waitWordState(word)
	return state == waitParked || state == waitParkedReady
}

func consumeWait(token *WaitToken, ticket WaitTicket) bool {
	if token == nil || !validWaitTicket(ticket) {
		return false
	}
	ready := waitWord(uint32(ticket), waitParkedReady)
	return preemptCompareAndSwap(&token.word, ready, waitWord(uint32(ticket), waitConsumed))
}
