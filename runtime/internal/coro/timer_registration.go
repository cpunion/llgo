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

// TimerRegistrationPageCapacity is the allocation-free granularity of the
// target-neutral timer catalog. Every table includes one inline page; a target
// may attach additional stable pages before binding the table.
const TimerRegistrationPageCapacity = 64

// TimerRegistrationCapacity is the default capacity of an unconfigured table.
// Keep this compatibility name for small and embedded profiles; it is not the
// capacity of a table after ConfigureTimerRegistrationPages succeeds.
const TimerRegistrationCapacity = TimerRegistrationPageCapacity

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

// timerRegistrationMode prevents the legacy WaitToken protocol and the V2
// OperationRecord protocol from interpreting the same live generation. A free
// slot has no mode; its record may retain only canonical reusable V2 residue.
type timerRegistrationMode uint8

const (
	timerRegistrationModeNone timerRegistrationMode = iota
	timerRegistrationModeV1
	timerRegistrationModeV2
)

type timerRegistrationSlot struct {
	state      timerRegistrationState
	mode       timerRegistrationMode
	generation uint32
	p          *P
	token      *WaitToken
	ticket     WaitTicket
	deadline   int64

	// A controlled V2 timer may be invalidated by another running G through a
	// stable logical identity. controller is an opaque, non-owning key (the
	// parked manager G retains the Go object); controlWord is that object's
	// exact logical generation. Both are zero for V1 and ordinary Sleep.
	controller  uintptr
	controlWord uint32

	// Owner-P-only V2 suffix. Timers have no external producer, so the stable
	// OperationRecord itself is the only additional physical-source state.
	record OperationRecord
}

// TimerRegistrationPage is stable storage supplied by a target profile. Its
// fields are intentionally private: only the owner-side catalog may interpret
// a page, while native, WASM, embedded, and bare-metal runtimes may choose the
// number of statically reserved pages without changing a handle or callback
// ABI. Attaching a page performs no allocation.
type TimerRegistrationPage struct {
	slots [TimerRegistrationPageCapacity]timerRegistrationSlot
}

// TimerRegistrationTable is a paged durable source for one-shot absolute
// monotonic deadlines. Its configured pages are frozen while bound. All methods
// are scheduler-owner-only and must be serialized. Unlike
// WaitRegistrationTable, this source has no producer admission path: the single
// executor discovers expiry by scanning at a fresh monotonic timestamp and
// bounds its retained-doorbell poll by NextDeadline.
//
// An Active slot is the source of truth even when its deadline passes between
// the scheduler's final scan and CommitSleep. The target must use the returned
// absolute deadline, sample the monotonic clock again, and enter poll with a
// rounded-up timeout. A timeout or a pipe wake returns ownership to the
// scheduler, which scans the table again before acknowledging requests.
//
// The table must live at a stable address while bound. A delivered or canceled
// V1 slot remains live through exact WaitToken consumption; a V2 slot remains
// live through logical detach and, for a winner, exact result-lease release.
// Both modes retain the shared physical generation until typed recycle clears
// owner pointers, and neither mode may invoke the other's terminal API.
type TimerRegistrationTable struct {
	slots      [TimerRegistrationPageCapacity]timerRegistrationSlot
	extraPages []TimerRegistrationPage
	owner      *P
	route      RouteID
	scanLimit  uint32
}

// TimerRegistrationConfiguredCapacity returns the exact number of addressable
// slots in table. The linear one-based slot remains directly encodable in the
// frozen 15-bit OperationID local field.
func TimerRegistrationConfiguredCapacity(table *TimerRegistrationTable) uint32 {
	if table == nil {
		return 0
	}
	return uint32(1+len(table.extraPages)) * TimerRegistrationPageCapacity
}

func timerRegistrationScanLimit(table *TimerRegistrationTable) (uint32, bool) {
	if table == nil {
		return 0, false
	}
	capacity := TimerRegistrationConfiguredCapacity(table)
	return table.scanLimit, validSourceScanLimit(table.scanLimit, capacity)
}

func timerRegistrationSlotAt(table *TimerRegistrationTable, index uint32) (*timerRegistrationSlot, bool) {
	capacity := TimerRegistrationConfiguredCapacity(table)
	if index >= capacity {
		return nil, false
	}
	if index < TimerRegistrationPageCapacity {
		return &table.slots[index], true
	}
	page := index/TimerRegistrationPageCapacity - 1
	offset := index % TimerRegistrationPageCapacity
	return &table.extraPages[page].slots[offset], true
}

// ConfigureTimerRegistrationPages attaches stable allocation-free catalog
// storage before the table is bound. The slice header is scheduler-owned; no
// page pointer crosses a target callback boundary. Repeating the exact same
// configuration is idempotent so a native runtime may bind, unbind, and bind
// its singleton executor again. An empty unbound table may monotonically expose
// more pages from the same backing pool; configured pages are never detached.
func ConfigureTimerRegistrationPages(table *TimerRegistrationTable, pages []TimerRegistrationPage) bool {
	if table == nil || table.owner != nil || len(pages) > int(operationLocalMask/TimerRegistrationPageCapacity)-1 {
		return false
	}
	existing := len(table.extraPages)
	if existing != 0 && (len(pages) < existing || len(pages) == 0 || &table.extraPages[0] != &pages[0]) {
		return false
	}
	if len(pages) == existing {
		return true
	}
	for index := uint32(0); index < TimerRegistrationConfiguredCapacity(table); index++ {
		slot, ok := timerRegistrationSlotAt(table, index)
		if !ok || !reusableTimerRegistrationSlot(slot, table.route, index) {
			return false
		}
	}
	for page := existing; page < len(pages); page++ {
		for offset := range pages[page].slots {
			index := uint32(page+1)*TimerRegistrationPageCapacity + uint32(offset)
			if !reusableTimerRegistrationSlot(&pages[page].slots[offset], table.route, index) {
				return false
			}
		}
	}
	table.extraPages = pages
	return true
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
	if table == nil || handle.Slot == 0 || handle.Generation == 0 {
		return nil, false
	}
	return timerRegistrationSlotAt(table, handle.Slot-1)
}

func validTimerRegistrationHeader(slot *timerRegistrationSlot, owner *P) bool {
	return slot != nil && slot.generation != 0 && slot.p != nil &&
		(owner == nil || slot.p == owner) && slot.deadline >= 0
}

func validTimerRegistrationRecordResidue(slot *timerRegistrationSlot, route RouteID, index uint32) bool {
	if slot == nil {
		return false
	}
	if slot.record == (OperationRecord{}) {
		return true
	}
	id := slot.record.id
	return route.Valid() && slot.generation != 0 && id.Valid() && id.Source() == OperationSourceTimer &&
		id.Route() == route && id.LocalSlot() == index+1 &&
		id.Generation <= slot.generation && slot.record == (OperationRecord{id: id, phase: operationReusable})
}

func validLiveTimerRegistrationV1(slot *timerRegistrationSlot, owner *P, route RouteID, index uint32) bool {
	return validTimerRegistrationHeader(slot, owner) && slot.mode == timerRegistrationModeV1 &&
		slot.token != nil && validWaitTicket(slot.ticket) &&
		slot.controller == 0 && slot.controlWord == 0 &&
		validTimerRegistrationRecordResidue(slot, route, index)
}

func validTimerRegistrationController(controller uintptr, controlWord uint32) bool {
	return controller == 0 && controlWord == 0 || controller != 0 && controlWord != 0
}

func timerRegistrationOperationID(route RouteID, index uint32, generation uint32) (OperationID, bool) {
	return MakeOperationIDAtRoute(OperationSourceTimer, route, index+1, generation)
}

func timerRegistrationIDForHandle(table *TimerRegistrationTable, handle TimerRegistrationHandle) (OperationID, bool) {
	if table == nil || handle.Slot == 0 {
		return OperationID{}, false
	}
	return MakeOperationIDAtRoute(OperationSourceTimer, table.route, handle.Slot, handle.Generation)
}

func validLiveTimerRegistrationV2(slot *timerRegistrationSlot, owner *P, route RouteID, index uint32) bool {
	if !validTimerRegistrationHeader(slot, owner) || slot.mode != timerRegistrationModeV2 ||
		slot.token != nil || slot.ticket != 0 || !validTimerRegistrationController(slot.controller, slot.controlWord) {
		return false
	}
	id, ok := timerRegistrationOperationID(route, index, slot.generation)
	return ok && slot.record.Matches(id)
}

func validLiveTimerRegistration(slot *timerRegistrationSlot, owner *P, route RouteID, index uint32) bool {
	switch slot.mode {
	case timerRegistrationModeV1:
		return validLiveTimerRegistrationV1(slot, owner, route, index)
	case timerRegistrationModeV2:
		return validLiveTimerRegistrationV2(slot, owner, route, index)
	default:
		return false
	}
}

func reusableTimerRegistrationSlot(slot *timerRegistrationSlot, route RouteID, index uint32) bool {
	if slot == nil || slot.state != timerRegistrationFree || slot.mode != timerRegistrationModeNone ||
		slot.p != nil || slot.token != nil || slot.ticket != 0 || slot.deadline != 0 ||
		slot.controller != 0 || slot.controlWord != 0 {
		return false
	}
	return validTimerRegistrationRecordResidue(slot, route, index)
}

// Register reserves one legacy V1 timer slot for an already-armed token.
// deadline is an absolute monotonic nanosecond value; zero represents an
// immediately due timer. Register is scheduler-owner-only.
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
	for index := uint32(0); index < TimerRegistrationConfiguredCapacity(table); index++ {
		slot, slotOK := timerRegistrationSlotAt(table, index)
		if !slotOK || slot.generation == ^uint32(0) || !reusableTimerRegistrationSlot(slot, table.route, index) {
			continue
		}
		slot.state = timerRegistrationInitializing
		if !raiseSourceScanLimit(&table.scanLimit, index, TimerRegistrationConfiguredCapacity(table)) {
			return TimerRegistrationHandle{}, false
		}
		slot.generation++
		if slot.generation == 0 {
			// Generation exhaustion was checked before publication. Keep this
			// defensive state fail-closed rather than reopening an ABA window.
			return TimerRegistrationHandle{}, false
		}
		slot.p = p
		slot.mode = timerRegistrationModeV1
		slot.token = token
		slot.ticket = ticket
		slot.deadline = deadline
		slot.state = timerRegistrationActive
		return TimerRegistrationHandle{Slot: index + 1, Generation: slot.generation}, true
	}
	return TimerRegistrationHandle{}, false
}

// ReserveAndAttachTimerV2 reserves one shared physical timer generation and
// attaches its stable OperationRecord to a scheduler-integrated logical
// wait-set. The table's generation is authoritative across alternating V1 and
// V2 uses of the same slot. A failed preparation consumes no published timer;
// if a V2 generation was prepared before attachment failed, it is retained as
// reusable residue so no copied identity can alias a later reservation.
func (table *TimerRegistrationTable) reserveAndAttachTimerV2(
	p *P,
	state *ParkState,
	ticket ParkTicket,
	wait *WaitSetRecord,
	caseID uint32,
	deadline int64,
	controller uintptr,
	controlWord uint32,
) (TimerRegistrationHandle, bool) {
	if table == nil || p == nil || table.owner != p || !table.route.Valid() || state == nil || wait == nil || deadline < 0 {
		return TimerRegistrationHandle{}, false
	}
	if !validTimerRegistrationController(controller, controlWord) {
		return TimerRegistrationHandle{}, false
	}
	for index := uint32(0); index < TimerRegistrationConfiguredCapacity(table); index++ {
		slot, slotOK := timerRegistrationSlotAt(table, index)
		if !slotOK || slot.generation == ^uint32(0) || !reusableTimerRegistrationSlot(slot, table.route, index) {
			continue
		}
		slot.state = timerRegistrationInitializing
		if !raiseSourceScanLimit(&table.scanLimit, index, TimerRegistrationConfiguredCapacity(table)) {
			return TimerRegistrationHandle{}, false
		}
		desired, idOK := timerRegistrationOperationID(table.route, index, slot.generation+1)
		if !idOK || !PrepareOperationAtGeneration(&slot.record, desired) {
			slot.state = timerRegistrationFree
			continue
		}
		// Install the shared physical generation before any later failure can
		// expose a copied desired ID to reuse.
		slot.generation = desired.Generation
		if !AttachParkWaitOperation(state, ticket, wait, &slot.record, caseID) {
			if !AbortReservedOperation(&slot.record, desired) {
				return TimerRegistrationHandle{}, false
			}
			slot.state = timerRegistrationFree
			return TimerRegistrationHandle{}, false
		}
		slot.mode = timerRegistrationModeV2
		slot.p = p
		slot.deadline = deadline
		slot.controller = controller
		slot.controlWord = controlWord
		slot.state = timerRegistrationActive
		return TimerRegistrationHandle{Slot: index + 1, Generation: desired.Generation}, true
	}
	return TimerRegistrationHandle{}, false
}

func (table *TimerRegistrationTable) ReserveAndAttachTimerV2(
	p *P,
	state *ParkState,
	ticket ParkTicket,
	wait *WaitSetRecord,
	caseID uint32,
	deadline int64,
) (TimerRegistrationHandle, bool) {
	return table.reserveAndAttachTimerV2(p, state, ticket, wait, caseID, deadline, 0, 0)
}

// ReserveAndAttachControlledTimerV2 installs one V2 timer with the exact
// logical generation used by time.Timer Stop/Reset. controller is a non-owning
// scalar key; the parked manager frame keeps the object alive. The atomic
// control recheck closes the change-before-publication race by publishing an
// ordinary operation cancellation on the still-preparing ParkState. The
// initial affected visit after park commit performs the normal detach path.
func (table *TimerRegistrationTable) ReserveAndAttachControlledTimerV2(
	p *P,
	state *ParkState,
	ticket ParkTicket,
	wait *WaitSetRecord,
	caseID uint32,
	deadline int64,
	controller uintptr,
	control *uint32,
	expected uint32,
) (TimerRegistrationHandle, bool) {
	if controller == 0 || control == nil || expected == 0 {
		return TimerRegistrationHandle{}, false
	}
	handle, ok := table.reserveAndAttachTimerV2(
		p, state, ticket, wait, caseID, deadline, controller, expected,
	)
	if !ok {
		return TimerRegistrationHandle{}, false
	}
	if preemptLoad(control) != expected && !RequestParkCancel(state, ticket, ParkCancelOperation) {
		// Every input and the complete preparing ParkState were validated before
		// reservation. Failure here is corruption, so retain the physical
		// generation fail-closed instead of pretending the attach rolled back.
		return handle, false
	}
	return handle, true
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
	limit, valid := timerRegistrationScanLimit(table)
	if !valid {
		return 0, false, false
	}
	for index := uint32(0); index < limit; index++ {
		slot, slotOK := timerRegistrationSlotAt(table, index)
		if !slotOK {
			return 0, false, false
		}
		switch slot.state {
		case timerRegistrationFree:
			if !reusableTimerRegistrationSlot(slot, table.route, index) {
				return 0, false, false
			}
		case timerRegistrationActive:
			if !validLiveTimerRegistration(slot, owner, table.route, index) {
				return 0, false, false
			}
			if !hasDeadline || slot.deadline < deadline {
				deadline, hasDeadline = slot.deadline, true
			}
		case timerRegistrationDelivered, timerRegistrationCanceled:
			if !validLiveTimerRegistration(slot, owner, table.route, index) {
				return 0, false, false
			}
		default:
			return 0, false, false
		}
	}
	return deadline, hasDeadline, true
}

// DrainDue publishes every Active timer whose deadline is at or before now.
// V1 publishes its WaitToken outcome; V2 publishes a sticky OperationRecord
// completion and marks the owning WaitSetRecord, leaving common epoch
// resolution and source-specific detach to ExecutorSourceSet. It returns the
// number published plus the earliest still-active deadline.
// The tuple ends with (hasDeadline, validTable). V2 may coalesce the exact
// owner-only affected queue entry, but it never resolves a wait, detaches a
// source, or promotes a G; ExecutorSourceSet performs those common phases only
// after every source has completed publication.
func (table *TimerRegistrationTable) DrainDue(now int64) (completed int, deadline int64, hasDeadline, ok bool) {
	if table == nil || table.owner != nil || now < 0 {
		return 0, 0, false, false
	}
	return table.drainDueFor(nil, now)
}

// drainDueSlotFor visits one real timer catalog entry. The caller combines
// the returned deadline minima across a complete pass. A bounded pass keeps a
// fixed now sample from its first entry through resolution, matching the
// legacy all-slot DrainDue semantics even when the host yields between slots.
func (table *TimerRegistrationTable) drainDueSlotFor(owner *P, now int64, index uint32) (completed int, deadline int64, hasDeadline, ok bool) {
	if table == nil || table.owner != owner || now < 0 || index >= TimerRegistrationConfiguredCapacity(table) {
		return 0, 0, false, false
	}
	slot, slotOK := timerRegistrationSlotAt(table, index)
	if !slotOK {
		return 0, 0, false, false
	}
	switch slot.state {
	case timerRegistrationFree:
		if !reusableTimerRegistrationSlot(slot, table.route, index) {
			return 0, 0, false, false
		}
	case timerRegistrationActive:
		if !validLiveTimerRegistration(slot, owner, table.route, index) {
			return 0, 0, false, false
		}
		if slot.deadline <= now {
			switch slot.mode {
			case timerRegistrationModeV1:
				if !CompleteWait(slot.token, slot.ticket) {
					// Keep this slot Active and fail-closed for diagnosis.
					return 0, 0, false, false
				}
			case timerRegistrationModeV2:
				id, idOK := timerRegistrationOperationID(table.route, index, slot.generation)
				if !idOK || PublishOperationCompletion(&slot.record, id) != OperationCompletionPublished {
					return 0, 0, false, false
				}
				if slot.record.link.wait == nil || !MarkWaitSetAffected(owner, slot.record.link.wait) {
					// Completion publication is sticky and irreversible. Leave the
					// physical slot Active so it cannot be recycled.
					return 0, 0, false, false
				}
			default:
				return 0, 0, false, false
			}
			slot.state = timerRegistrationDelivered
			return 1, 0, false, true
		}
		return 0, slot.deadline, true, true
	case timerRegistrationDelivered, timerRegistrationCanceled:
		if !validLiveTimerRegistration(slot, owner, table.route, index) {
			return 0, 0, false, false
		}
	default:
		return 0, 0, false, false
	}
	return 0, 0, false, true
}

func (table *TimerRegistrationTable) drainDueFor(owner *P, now int64) (completed int, deadline int64, hasDeadline, ok bool) {
	if table == nil || table.owner != owner || now < 0 {
		return 0, 0, false, false
	}
	limit, valid := timerRegistrationScanLimit(table)
	if !valid {
		return 0, 0, false, false
	}
	for index := uint32(0); index < limit; index++ {
		one, next, hasNext, slotOK := table.drainDueSlotFor(owner, now, index)
		completed += one
		if !slotOK {
			return completed, 0, false, false
		}
		if hasNext && (!hasDeadline || next < deadline) {
			deadline, hasDeadline = next, true
		}
	}
	return completed, deadline, hasDeadline, true
}

// Cancel publishes legacy V1 cancellation for one exact timer generation. It
// is owner-only and has no backend-unregister phase because this source has no
// producer or callback. Completion and cancellation still race on WaitToken's
// atomic outcome word, and the winning outcome determines retirement. A V2
// handle is rejected; V2 operation cancellation uses RequestTimerV2Cancel.
func (table *TimerRegistrationTable) Cancel(handle TimerRegistrationHandle) WaitCancelResult {
	slot, ok := timerRegistrationSlotFor(table, handle)
	if !ok || table.owner != nil && slot.p != table.owner || slot.generation != handle.Generation ||
		!validLiveTimerRegistrationV1(slot, table.owner, table.route, handle.Slot-1) {
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

// CancelControlledV2 publishes operation cancellation for one exact logical
// Timer generation. It first audits the complete catalog so a duplicated
// controller identity cannot partially cancel one of two corrupt slots. A
// completion that is already detached needs no wake: the manager's subsequent
// control-word check suppresses that stale generation after its ready resume.
func (table *TimerRegistrationTable) CancelControlledV2(p *P, controller uintptr, controlWord uint32) bool {
	if table == nil || p == nil || table.owner != p || controller == 0 || controlWord == 0 {
		return false
	}
	var selected *timerRegistrationSlot
	var wait *WaitSetRecord
	for index := uint32(0); index < TimerRegistrationConfiguredCapacity(table); index++ {
		slot, ok := timerRegistrationSlotAt(table, index)
		if !ok {
			return false
		}
		if slot.mode != timerRegistrationModeV2 || slot.controller != controller || slot.controlWord != controlWord {
			continue
		}
		if selected != nil || !validLiveTimerRegistrationV2(slot, p, table.route, index) ||
			slot.state != timerRegistrationActive && slot.state != timerRegistrationDelivered ||
			slot.record.link.wait == nil {
			return false
		}
		selected = slot
		wait = slot.record.link.wait
	}
	return selected != nil && RequestWaitSetCancel(p, wait, ParkCancelOperation)
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

// Retire releases an exact delivered or canceled V1 timer only after the
// scheduler has consumed the matching WaitToken outcome. It clears every Go
// pointer before making the slot reusable with a later generation.
func (table *TimerRegistrationTable) Retire(handle TimerRegistrationHandle) bool {
	slot, ok := timerRegistrationSlotFor(table, handle)
	if !ok || table.owner != nil && slot.p != table.owner || slot.generation != handle.Generation ||
		!validLiveTimerRegistrationV1(slot, table.owner, table.route, handle.Slot-1) {
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
	slot.mode = timerRegistrationModeNone
	slot.token = nil
	slot.ticket = 0
	slot.deadline = 0
	slot.controller = 0
	slot.controlWord = 0
	slot.state = timerRegistrationFree
	return true
}

// RetireCompletedTimer validates the synchronous continuation's exact owner
// tuple before retiring a consumed completion.
func (table *TimerRegistrationTable) RetireCompletedTimer(handle TimerRegistrationHandle, token *WaitToken, ticket WaitTicket) bool {
	slot, ok := timerRegistrationSlotFor(table, handle)
	return ok && token != nil && validWaitTicket(ticket) && slot.generation == handle.Generation &&
		slot.mode == timerRegistrationModeV1 && slot.state == timerRegistrationDelivered &&
		slot.token == token && slot.ticket == ticket && table.Retire(handle)
}

// RetireCanceledTimer validates the synchronous continuation's exact owner
// tuple before retiring a consumed cancellation.
func (table *TimerRegistrationTable) RetireCanceledTimer(handle TimerRegistrationHandle, token *WaitToken, ticket WaitTicket) bool {
	slot, ok := timerRegistrationSlotFor(table, handle)
	return ok && token != nil && validWaitTicket(ticket) && slot.generation == handle.Generation &&
		slot.mode == timerRegistrationModeV1 && slot.state == timerRegistrationCanceled &&
		slot.token == token && slot.ticket == ticket && table.Retire(handle)
}

// RequestTimerV2Cancel publishes logical operation cancellation through the
// common WaitSetRecord gate. It intentionally cannot call the legacy Cancel
// method: a V2 timer owns no WaitToken and ordinary operation cancellation is
// resolved atomically with every completion in the published source epoch.
func (table *TimerRegistrationTable) RequestTimerV2Cancel(p *P, wait *WaitSetRecord) bool {
	return table != nil && table.owner == p && RequestWaitSetCancel(p, wait, ParkCancelOperation)
}

// ApplyTimerV2One applies one resolved timer candidate in O(1). A winning
// timer must have reached Delivered through drainDueFor; select losers and
// canceled waits may close an Active timer before its deadline. With no
// callback or backend producer, logical close is also immediate physical
// quiescence.
func (table *TimerRegistrationTable) ApplyTimerV2One(p *P, id OperationID, record *OperationRecord) OperationApplyResult {
	if table == nil || table.owner != p || id.Source() != OperationSourceTimer || id.Route() != table.route ||
		id.LocalSlot() == 0 || id.LocalSlot() > TimerRegistrationConfiguredCapacity(table) {
		return OperationApplyInvalid
	}
	index := id.LocalSlot() - 1
	slot, slotOK := timerRegistrationSlotAt(table, index)
	if !slotOK {
		return OperationApplyInvalid
	}
	if slot.generation != id.Generation || slot.mode != timerRegistrationModeV2 || &slot.record != record ||
		!validLiveTimerRegistrationV2(slot, p, table.route, index) || slot.record.phase != operationActive {
		return OperationApplyInvalid
	}
	disposition, terminal := OperationDispositionOf(&slot.record, id)
	if !terminal || slot.record.link.park == nil || slot.record.link.wait == nil || slot.record.link.operation != &slot.record ||
		slot.record.link.ticket == (ParkTicket{}) || !validActiveWaitSetRecordFast(p, slot.record.link.wait) {
		return OperationApplyInvalid
	}
	switch disposition {
	case OperationDispositionWinner:
		if slot.state != timerRegistrationDelivered || !operationCandidateIsPublished(&slot.record) {
			return OperationApplyInvalid
		}
	case OperationDispositionLost, OperationDispositionCanceled:
		if slot.state != timerRegistrationActive && slot.state != timerRegistrationDelivered {
			return OperationApplyInvalid
		}
	default:
		return OperationApplyInvalid
	}
	if disposition != OperationDispositionWinner {
		// Timer cancellation is the complete source-specific rollback: after
		// this transition no due delivery can retain or recreate the result.
		slot.state = timerRegistrationCanceled
		if slot.record.resultState == operationResultOwned &&
			!DiscardUnselectedOperationResult(&slot.record, id) {
			return OperationApplyInvalid
		}
	}
	if !AcknowledgeOperationResolution(&slot.record, id, disposition) || !ConfirmOperationQuiesced(&slot.record, id) {
		return OperationApplyInvalid
	}
	park, ticket := slot.record.link.park, slot.record.link.ticket
	// The direct batch validated this exact active link before dispatch and the
	// checks above repeat the timer-local ownership proof. Failure after the two
	// monotonic acknowledgements is therefore fail-stop corruption, not a
	// Deferred retry: replaying an already-applied disposition would be unsafe.
	if !DetachParkWaitOperation(park, ticket, &slot.record, id) {
		return OperationApplyInvalid
	}
	return OperationApplyDetached
}

func (table *TimerRegistrationTable) releaseTimerV2Result(p *P, handle TimerRegistrationHandle, lease OperationResultLease, discard bool) bool {
	slot, ok := timerRegistrationSlotFor(table, handle)
	id, idOK := timerRegistrationIDForHandle(table, handle)
	if !ok || !idOK || table.owner != p || slot.generation != handle.Generation ||
		slot.mode != timerRegistrationModeV2 || slot.state != timerRegistrationDelivered ||
		slot.record.id != id || !validLiveTimerRegistrationV2(slot, p, table.route, handle.Slot-1) ||
		slot.record.disposition != OperationDispositionWinner {
		return false
	}
	if discard {
		return DiscardOperationResult(&slot.record, lease)
	}
	return TakeOperationResult(&slot.record, lease)
}

// TakeTimerV2Result releases the exact winner lease after the synchronous
// continuation has copied the timer result (timers currently carry no payload).
func (table *TimerRegistrationTable) TakeTimerV2Result(p *P, handle TimerRegistrationHandle, lease OperationResultLease) bool {
	return table.releaseTimerV2Result(p, handle, lease, false)
}

// DiscardTimerV2Result releases the same exact lease when cancellation or
// cleanup suppresses the selected continuation. It is separate from Take to
// make generated cleanup intent explicit even though a timer has no payload.
func (table *TimerRegistrationTable) DiscardTimerV2Result(p *P, handle TimerRegistrationHandle, lease OperationResultLease) bool {
	return table.releaseTimerV2Result(p, handle, lease, true)
}

// RecycleTimerV2 releases one detached timer generation. Winner recycle is
// blocked until TakeTimerV2Result or DiscardTimerV2Result consumes its lease.
func (table *TimerRegistrationTable) RecycleTimerV2(p *P, handle TimerRegistrationHandle) bool {
	slot, ok := timerRegistrationSlotFor(table, handle)
	id, idOK := timerRegistrationIDForHandle(table, handle)
	if !ok || !idOK || table.owner != p || slot.generation != handle.Generation ||
		slot.mode != timerRegistrationModeV2 || (slot.state != timerRegistrationDelivered && slot.state != timerRegistrationCanceled) ||
		!validLiveTimerRegistrationV2(slot, p, table.route, handle.Slot-1) ||
		!OperationCanRecycle(&slot.record, id) || !RecycleOperation(&slot.record, id) {
		return false
	}
	slot.state = timerRegistrationFree
	slot.mode = timerRegistrationModeNone
	slot.p = nil
	slot.token = nil
	slot.ticket = 0
	slot.deadline = 0
	slot.controller = 0
	slot.controlWord = 0
	return true
}

func timerRegistrationTableEmpty(table *TimerRegistrationTable, owner *P) bool {
	if table == nil || table.owner != owner {
		return false
	}
	for index := uint32(0); index < TimerRegistrationConfiguredCapacity(table); index++ {
		slot, slotOK := timerRegistrationSlotAt(table, index)
		if !slotOK || !reusableTimerRegistrationSlot(slot, table.route, index) {
			return false
		}
	}
	return true
}

func bindTimerRegistrationTableAtRoute(table *TimerRegistrationTable, p *P, route RouteID) bool {
	if p == nil || !route.Valid() || !timerRegistrationTableEmpty(table, nil) ||
		table.route != 0 && table.route != route {
		return false
	}
	table.route = route
	table.scanLimit = 0
	table.owner = p
	return true
}

// bindTimerRegistrationTable is the route-1 compatibility binding. Timer V1
// handles remain route-free; only V2 OperationIDs use the persistent route.
func bindTimerRegistrationTable(table *TimerRegistrationTable, p *P) bool {
	return bindTimerRegistrationTableAtRoute(table, p, RouteID(1))
}

func unbindTimerRegistrationTable(table *TimerRegistrationTable, p *P) bool {
	if p == nil || !timerRegistrationTableEmpty(table, p) {
		return false
	}
	table.owner = nil
	table.scanLimit = 0
	return true
}

// CanRelease reports that no live timer or driver binding retains this table.
func (table *TimerRegistrationTable) CanRelease() bool {
	return timerRegistrationTableEmpty(table, nil)
}

// Route returns the persistent executor identity used by Timer V2. Unbinding
// clears the owner but never changes or releases an established route.
func (table *TimerRegistrationTable) Route() (RouteID, bool) {
	if table == nil || !table.route.Valid() {
		return 0, false
	}
	return table.route, true
}
