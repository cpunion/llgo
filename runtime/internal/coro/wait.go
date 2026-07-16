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

// WaitToken is a target-neutral, allocation-free logical outcome cell. A
// platform worker, host callback, RTOS ISR handoff, or bare-metal event source
// retains only a WaitRegistrationHandle and posts into a stable registration
// table; it never receives this token or touches G/P state or an LLVM handle.
//
// The generation and state share one atomic word so a late completion cannot
// wake a later reuse of the same cell (the classic cancellation/ABA race). A
// token is intentionally exhausted after 2^29-1 generations rather than
// wrapping and accepting a stale ticket. The additional states atomically
// claim one exact waiter, preserve completion versus cancellation through
// scheduler consumption, and store no target-dependent pointer. A WaitToken
// must not be copied after its first ArmWait.
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
	// State zero is the unused zero value at generation zero. At a non-zero
	// generation it records a consumed completion, which preserves the winning
	// outcome without spending a ninth state bit.
	waitUnused waitState = iota
	waitArmed
	waitReady
	waitParked
	waitParkedReady
	waitConsumedCanceled
	waitCanceled
	waitParkedCanceled
)

// WaitCancelResult classifies an exact-generation cancellation attempt. A
// caller must distinguish a completion that already won from a duplicate or
// stale cancellation; treating every losing CAS as an ordinary false result
// would make operation teardown and result ownership ambiguous.
type WaitCancelResult uint8

const (
	WaitCancelInvalid WaitCancelResult = iota
	WaitCancelWon
	WaitCancelCompletionWon
	WaitCancelAlreadyCanceled
)

// WaitOutcome is the terminal result consumed by the scheduler after one
// exact wait generation has also been claimed by a G.
type WaitOutcome uint8

const (
	WaitOutcomeInvalid WaitOutcome = iota
	WaitOutcomeCompleted
	WaitOutcomeCanceled
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
// The previous generation's terminal outcome remains queryable until this CAS
// publishes the new generation.
func ArmWait(token *WaitToken) (WaitTicket, bool) {
	if token == nil {
		return 0, false
	}
	for {
		old := preemptLoad(&token.word)
		state := waitWordState(old)
		if state != waitUnused && state != waitConsumedCanceled {
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

// rollbackArmedWait abandons a ticket that has not been published to a
// completion producer and has not been claimed by a G. It preserves the
// generation and records a consumed cancellation instead of restoring the
// previous word, so a stale ticket can never become valid again. The next
// ArmWait advances to a fresh generation.
func rollbackArmedWait(token *WaitToken, ticket WaitTicket) bool {
	if token == nil || !validWaitTicket(ticket) {
		return false
	}
	generation := uint32(ticket)
	return preemptCompareAndSwap(
		&token.word,
		waitWord(generation, waitArmed),
		waitWord(generation, waitConsumedCanceled),
	)
}

// consumeUnclaimedCanceledWait completes rollback after a prepared
// registration has been strongly quiesced before any G could claim it. This
// transition is deliberately unavailable to ordinary cancellation: once a G
// has claimed the ticket, only the scheduler may consume its outcome.
func consumeUnclaimedCanceledWait(token *WaitToken, ticket WaitTicket) bool {
	if token == nil || !validWaitTicket(ticket) {
		return false
	}
	generation := uint32(ticket)
	return preemptCompareAndSwap(
		&token.word,
		waitWord(generation, waitCanceled),
		waitWord(generation, waitConsumedCanceled),
	)
}

// CompleteWait publishes completion of one exact generation. Writes to the
// stable result record must happen before this call. The atomic CAS publishes
// them to the scheduler that consumes the ready ticket. Duplicate, stale, and
// not-yet-armed completions fail closed. This operation deliberately touches
// neither P/G queues nor an LLVM handle. WaitRegistrationTable.Drain normally
// calls it after acquiring a Posted slot, then requests scheduling. In-runtime
// wait owners may also call it when they already prove result/token lifetime;
// a platform callback must use Post instead of retaining token or P pointers.
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

// publishWaitCancellation publishes cancellation of one exact generation.
// Completion and cancellation race on the same atomic word, so exactly one
// outcome wins and neither can overwrite the other. Cancellation may win
// before claimWait; claimWait preserves that outcome while binding the
// generation to a G.
//
// Cancellation metadata, when present, must be written before this call and
// must not share storage with a completion producer's result. A successful CAS
// publishes that metadata to the scheduler's later consumeWait operation.
// This low-level operation is deliberately unexported: a platform-backed wait
// must reach it only through WaitRegistrationTable.ConfirmQuiesced, after new
// callbacks have been excluded and the backend has acknowledged unregister.
func publishWaitCancellation(token *WaitToken, ticket WaitTicket) WaitCancelResult {
	if token == nil || !validWaitTicket(ticket) {
		return WaitCancelInvalid
	}
	generation := uint32(ticket)
	for {
		old := preemptLoad(&token.word)
		if waitGeneration(old) != generation {
			return WaitCancelInvalid
		}
		var canceled waitState
		switch waitWordState(old) {
		case waitArmed:
			canceled = waitCanceled
		case waitParked:
			canceled = waitParkedCanceled
		case waitReady, waitParkedReady, waitUnused:
			return WaitCancelCompletionWon
		case waitCanceled, waitParkedCanceled, waitConsumedCanceled:
			return WaitCancelAlreadyCanceled
		default:
			return WaitCancelInvalid
		}
		if preemptCompareAndSwap(&token.word, old, waitWord(generation, canceled)) {
			return WaitCancelWon
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
		case waitCanceled:
			claimed = waitParkedCanceled
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
	return state == waitParked || state == waitParkedReady || state == waitParkedCanceled
}

func consumeWait(token *WaitToken, ticket WaitTicket) (WaitOutcome, bool) {
	if token == nil || !validWaitTicket(ticket) {
		return WaitOutcomeInvalid, false
	}
	generation := uint32(ticket)
	for {
		old := preemptLoad(&token.word)
		if waitGeneration(old) != generation {
			return WaitOutcomeInvalid, false
		}
		var outcome WaitOutcome
		switch waitWordState(old) {
		case waitParkedReady:
			outcome = WaitOutcomeCompleted
		case waitParkedCanceled:
			outcome = WaitOutcomeCanceled
		default:
			return WaitOutcomeInvalid, false
		}
		consumed := waitUnused
		if outcome == WaitOutcomeCanceled {
			consumed = waitConsumedCanceled
		}
		if preemptCompareAndSwap(&token.word, old, waitWord(generation, consumed)) {
			return outcome, true
		}
	}
}

// WaitOutcomeOf reports the terminal winner for one exact generation before
// or after scheduler consumption. It lets the resumed synchronous-style
// continuation select the completion or cancellation result without trusting
// loser-written payload fields. The result remains stable until ArmWait
// publishes a later generation.
func WaitOutcomeOf(token *WaitToken, ticket WaitTicket) (WaitOutcome, bool) {
	if token == nil || !validWaitTicket(ticket) {
		return WaitOutcomeInvalid, false
	}
	word := preemptLoad(&token.word)
	if waitGeneration(word) != uint32(ticket) {
		return WaitOutcomeInvalid, false
	}
	switch waitWordState(word) {
	case waitReady, waitParkedReady, waitUnused:
		return WaitOutcomeCompleted, true
	case waitCanceled, waitParkedCanceled, waitConsumedCanceled:
		return WaitOutcomeCanceled, true
	default:
		return WaitOutcomeInvalid, false
	}
}

func consumedWait(token *WaitToken, ticket WaitTicket) (WaitOutcome, bool) {
	if token == nil || !validWaitTicket(ticket) {
		return WaitOutcomeInvalid, false
	}
	word := preemptLoad(&token.word)
	if waitGeneration(word) != uint32(ticket) {
		return WaitOutcomeInvalid, false
	}
	switch waitWordState(word) {
	case waitUnused:
		return WaitOutcomeCompleted, true
	case waitConsumedCanceled:
		return WaitOutcomeCanceled, true
	default:
		return WaitOutcomeInvalid, false
	}
}
