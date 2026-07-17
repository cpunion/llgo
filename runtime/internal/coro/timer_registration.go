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

// TimerRegistrationCapacity is the number of one-shot monotonic timers in one
// fixed table. Registration is allocation-free and fails transactionally when
// every slot is live.
const TimerRegistrationCapacity = 64

// TimerRegistrationHandle identifies one exact timer slot generation. It is
// scheduler-owned in the first single-executor backend: no platform thread or
// callback retains this handle, a WaitToken, or an LLVM coroutine handle.
type TimerRegistrationHandle struct {
	Slot       uint32
	Generation uint32
}

// TimerRegistrationPrepareResult distinguishes ordinary rejection from an
// impossible WaitToken rollback failure. A poisoned result requires fail-stop;
// the token may still contain an unpublished armed generation.
type TimerRegistrationPrepareResult uint8

const (
	TimerRegistrationPrepareInvalid TimerRegistrationPrepareResult = iota
	TimerRegistrationPrepared
	TimerRegistrationPrepareRejected
	TimerRegistrationPreparePoisoned
)

type timerRegistrationState uint8

const (
	timerRegistrationFree timerRegistrationState = iota
	timerRegistrationInitializing
	timerRegistrationActive
	timerRegistrationDelivered
	timerRegistrationCanceled
)

type timerRegistrationSlot struct {
	state      timerRegistrationState
	generation uint32
	p          *P
	token      *WaitToken
	ticket     WaitTicket
	deadline   int64
}

// TimerRegistrationTable is a fixed-capacity durable source for one-shot
// absolute monotonic deadlines. All methods are scheduler-owner-only and must
// be serialized. Unlike WaitRegistrationTable, this source has no producer
// admission path: the single executor discovers expiry by scanning at a fresh
// monotonic timestamp and bounds its retained-doorbell poll by NextDeadline.
//
// An Active slot is the source of truth even when its deadline passes between
// the scheduler's final scan and CommitSleep. The target must use the returned
// absolute deadline, sample the monotonic clock again, and enter poll with a
// rounded-up timeout. A timeout or a pipe wake returns ownership to the
// scheduler, which scans the table again before acknowledging requests.
//
// The table must live at a stable address while bound. A delivered or canceled
// slot remains live until the scheduler has consumed the exact WaitToken
// outcome and Retire clears its owner pointers.
type TimerRegistrationTable struct {
	slots [TimerRegistrationCapacity]timerRegistrationSlot
	owner *P
}

// PrepareTimerRegistration arms token and publishes an absolute monotonic
// deadline as one owner-side transaction. A capacity or validation failure
// consumes the unpublished ticket as cancellation, preserving its generation
// so the token can be reused without admitting an ABA alias.
func PrepareTimerRegistration(p *P, table *TimerRegistrationTable, token *WaitToken, deadline int64) (WaitTicket, TimerRegistrationHandle, TimerRegistrationPrepareResult) {
	ticket, ok := ArmWait(token)
	if !ok {
		return 0, TimerRegistrationHandle{}, TimerRegistrationPrepareInvalid
	}
	handle, ok := table.Register(p, token, ticket, deadline)
	if !ok {
		if !rollbackArmedWait(token, ticket) {
			return 0, TimerRegistrationHandle{}, TimerRegistrationPreparePoisoned
		}
		return 0, TimerRegistrationHandle{}, TimerRegistrationPrepareRejected
	}
	return ticket, handle, TimerRegistrationPrepared
}

func timerRegistrationSlotFor(table *TimerRegistrationTable, handle TimerRegistrationHandle) (*timerRegistrationSlot, bool) {
	if table == nil || handle.Slot == 0 || handle.Slot > TimerRegistrationCapacity || handle.Generation == 0 {
		return nil, false
	}
	return &table.slots[handle.Slot-1], true
}

func validLiveTimerRegistration(slot *timerRegistrationSlot, owner *P) bool {
	return slot != nil && slot.generation != 0 && slot.p != nil &&
		(owner == nil || slot.p == owner) && slot.token != nil &&
		validWaitTicket(slot.ticket) && slot.deadline >= 0
}

// Register reserves one timer slot for an already-armed token. deadline is an
// absolute monotonic nanosecond value; zero represents an immediately due
// timer. Register is scheduler-owner-only.
func (table *TimerRegistrationTable) Register(p *P, token *WaitToken, ticket WaitTicket, deadline int64) (TimerRegistrationHandle, bool) {
	if table == nil || p == nil || token == nil || !validWaitTicket(ticket) || deadline < 0 ||
		(table.owner != nil && table.owner != p) {
		return TimerRegistrationHandle{}, false
	}
	word := preemptLoad(&token.word)
	if waitGeneration(word) != uint32(ticket) || waitWordState(word) != waitArmed {
		return TimerRegistrationHandle{}, false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return TimerRegistrationHandle{}, false
	}
	for index := range table.slots {
		slot := &table.slots[index]
		if slot.state != timerRegistrationFree || slot.generation == ^uint32(0) ||
			slot.p != nil || slot.token != nil || slot.ticket != 0 || slot.deadline != 0 {
			continue
		}
		slot.state = timerRegistrationInitializing
		slot.generation++
		if slot.generation == 0 {
			// Generation exhaustion was checked before publication. Keep this
			// defensive state fail-closed rather than reopening an ABA window.
			return TimerRegistrationHandle{}, false
		}
		slot.p = p
		slot.token = token
		slot.ticket = ticket
		slot.deadline = deadline
		slot.state = timerRegistrationActive
		return TimerRegistrationHandle{Slot: uint32(index) + 1, Generation: slot.generation}, true
	}
	return TimerRegistrationHandle{}, false
}

// NextDeadline returns the earliest active absolute monotonic deadline. The
// boolean pair is (hasDeadline, validTable). Delivered and canceled slots stay
// live for retirement but do not constrain the next physical poll.
func (table *TimerRegistrationTable) NextDeadline() (deadline int64, hasDeadline, ok bool) {
	if table == nil || table.owner != nil {
		return 0, false, false
	}
	return table.nextDeadlineFor(nil)
}

func (table *TimerRegistrationTable) nextDeadlineFor(owner *P) (deadline int64, hasDeadline, ok bool) {
	if table == nil || table.owner != owner {
		return 0, false, false
	}
	for index := range table.slots {
		slot := &table.slots[index]
		switch slot.state {
		case timerRegistrationFree:
			if slot.p != nil || slot.token != nil || slot.ticket != 0 || slot.deadline != 0 {
				return 0, false, false
			}
		case timerRegistrationActive:
			if !validLiveTimerRegistration(slot, owner) {
				return 0, false, false
			}
			if !hasDeadline || slot.deadline < deadline {
				deadline, hasDeadline = slot.deadline, true
			}
		case timerRegistrationDelivered, timerRegistrationCanceled:
			if !validLiveTimerRegistration(slot, owner) {
				return 0, false, false
			}
		default:
			return 0, false, false
		}
	}
	return deadline, hasDeadline, true
}

// DrainDue completes every Active timer whose deadline is at or before now.
// It returns the number completed plus the earliest still-active deadline.
// The tuple ends with (hasDeadline, validTable). It does not mutate scheduler
// queues; ExecutorDriver will pair this scan with pollReady in the same durable
// source transaction.
func (table *TimerRegistrationTable) DrainDue(now int64) (completed int, deadline int64, hasDeadline, ok bool) {
	if table == nil || table.owner != nil || now < 0 {
		return 0, 0, false, false
	}
	return table.drainDueFor(nil, now)
}

func (table *TimerRegistrationTable) drainDueFor(owner *P, now int64) (completed int, deadline int64, hasDeadline, ok bool) {
	if table == nil || table.owner != owner || now < 0 {
		return 0, 0, false, false
	}
	for index := range table.slots {
		slot := &table.slots[index]
		switch slot.state {
		case timerRegistrationFree:
			if slot.p != nil || slot.token != nil || slot.ticket != 0 || slot.deadline != 0 {
				return completed, 0, false, false
			}
		case timerRegistrationActive:
			if !validLiveTimerRegistration(slot, owner) {
				return completed, 0, false, false
			}
			if slot.deadline <= now {
				if !CompleteWait(slot.token, slot.ticket) {
					// Prior completions are irreversible. Preserve partial progress
					// and keep this slot Active and fail-closed for diagnosis.
					return completed, 0, false, false
				}
				slot.state = timerRegistrationDelivered
				completed++
				continue
			}
			if !hasDeadline || slot.deadline < deadline {
				deadline, hasDeadline = slot.deadline, true
			}
		case timerRegistrationDelivered, timerRegistrationCanceled:
			if !validLiveTimerRegistration(slot, owner) {
				return completed, 0, false, false
			}
		default:
			return completed, 0, false, false
		}
	}
	return completed, deadline, hasDeadline, true
}

// Cancel publishes cancellation for one exact timer generation. It is
// owner-only and has no backend-unregister phase because this source has no
// producer or callback. Completion and cancellation still race on WaitToken's
// atomic outcome word, and the winning outcome determines retirement.
func (table *TimerRegistrationTable) Cancel(handle TimerRegistrationHandle) WaitCancelResult {
	slot, ok := timerRegistrationSlotFor(table, handle)
	if !ok || table.owner != nil && slot.p != table.owner || slot.generation != handle.Generation ||
		!validLiveTimerRegistration(slot, table.owner) {
		return WaitCancelInvalid
	}
	switch slot.state {
	case timerRegistrationActive:
		result := publishWaitCancellation(slot.token, slot.ticket)
		switch result {
		case WaitCancelWon, WaitCancelAlreadyCanceled:
			slot.state = timerRegistrationCanceled
		case WaitCancelCompletionWon:
			slot.state = timerRegistrationDelivered
		default:
			return WaitCancelInvalid
		}
		return result
	case timerRegistrationDelivered:
		return WaitCancelCompletionWon
	case timerRegistrationCanceled:
		return WaitCancelAlreadyCanceled
	default:
		return WaitCancelInvalid
	}
}

// RollbackPreparedTimer releases a timer that has not yet been claimed by a G.
// It publishes and consumes cancellation, then retires the exact generation.
func (table *TimerRegistrationTable) RollbackPreparedTimer(handle TimerRegistrationHandle, token *WaitToken, ticket WaitTicket) bool {
	slot, ok := timerRegistrationSlotFor(table, handle)
	if !ok || token == nil || !validWaitTicket(ticket) || slot.generation != handle.Generation ||
		slot.state != timerRegistrationActive || slot.token != token || slot.ticket != ticket ||
		table.Cancel(handle) != WaitCancelWon || !consumeUnclaimedCanceledWait(token, ticket) {
		return false
	}
	return table.Retire(handle)
}

// Retire releases an exact delivered or canceled timer only after the
// scheduler has consumed the matching WaitToken outcome. It clears every Go
// pointer before making the slot reusable with a later generation.
func (table *TimerRegistrationTable) Retire(handle TimerRegistrationHandle) bool {
	slot, ok := timerRegistrationSlotFor(table, handle)
	if !ok || table.owner != nil && slot.p != table.owner || slot.generation != handle.Generation ||
		!validLiveTimerRegistration(slot, table.owner) {
		return false
	}
	want := WaitOutcomeInvalid
	switch slot.state {
	case timerRegistrationDelivered:
		want = WaitOutcomeCompleted
	case timerRegistrationCanceled:
		want = WaitOutcomeCanceled
	default:
		return false
	}
	outcome, consumed := consumedWait(slot.token, slot.ticket)
	if !consumed || outcome != want {
		return false
	}
	slot.p = nil
	slot.token = nil
	slot.ticket = 0
	slot.deadline = 0
	slot.state = timerRegistrationFree
	return true
}

// RetireCompletedTimer validates the synchronous continuation's exact owner
// tuple before retiring a consumed completion.
func (table *TimerRegistrationTable) RetireCompletedTimer(handle TimerRegistrationHandle, token *WaitToken, ticket WaitTicket) bool {
	slot, ok := timerRegistrationSlotFor(table, handle)
	return ok && token != nil && validWaitTicket(ticket) && slot.generation == handle.Generation &&
		slot.state == timerRegistrationDelivered && slot.token == token && slot.ticket == ticket && table.Retire(handle)
}

// RetireCanceledTimer validates the synchronous continuation's exact owner
// tuple before retiring a consumed cancellation.
func (table *TimerRegistrationTable) RetireCanceledTimer(handle TimerRegistrationHandle, token *WaitToken, ticket WaitTicket) bool {
	slot, ok := timerRegistrationSlotFor(table, handle)
	return ok && token != nil && validWaitTicket(ticket) && slot.generation == handle.Generation &&
		slot.state == timerRegistrationCanceled && slot.token == token && slot.ticket == ticket && table.Retire(handle)
}

func timerRegistrationTableEmpty(table *TimerRegistrationTable, owner *P) bool {
	if table == nil || table.owner != owner {
		return false
	}
	for index := range table.slots {
		slot := &table.slots[index]
		if slot.state != timerRegistrationFree || slot.p != nil || slot.token != nil || slot.ticket != 0 || slot.deadline != 0 {
			return false
		}
	}
	return true
}

func bindTimerRegistrationTable(table *TimerRegistrationTable, p *P) bool {
	if p == nil || !timerRegistrationTableEmpty(table, nil) {
		return false
	}
	table.owner = p
	return true
}

func unbindTimerRegistrationTable(table *TimerRegistrationTable, p *P) bool {
	if p == nil || !timerRegistrationTableEmpty(table, p) {
		return false
	}
	table.owner = nil
	return true
}

// CanRelease reports that no live timer or driver binding retains this table.
func (table *TimerRegistrationTable) CanRelease() bool {
	return timerRegistrationTableEmpty(table, nil)
}
