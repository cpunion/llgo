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

// waitSetRecordState is owner-P-only. A WaitSetRecord is caller storage which
// is live only across one direct V2 park; in production the compiler spills it
// into the direct-parking LLVM coroutine frame. Tests and bootstrap adapters
// may instead retain one at any other stable address.
type waitSetRecordState uint8

const (
	waitSetRecordUnused waitSetRecordState = iota
	waitSetRecordPreparing
	waitSetRecordCommitted
	waitSetRecordActive
)

type waitSetWorkState uint8

const (
	waitSetWorkIdle waitSetWorkState = iota
	waitSetWorkQueued
	waitSetWorkResolving
	// ResolvingDirty is defensive support for an owner-side source operation
	// which publishes another sticky fact while the record is being resolved.
	waitSetWorkResolvingDirty
	// AwaitingExternal retains a logically resolved/detaching wait without
	// putting it on the owner work queue. The source must call
	// MarkWaitSetAffected when its physical acknowledgement becomes sticky.
	waitSetWorkAwaitingExternal
)

// WaitSetRecord contains the queue links which exist only while one G is
// physically parked. In particular activePrev is not a permanent G field.
// The record is 48 bytes on 64-bit targets and 28 bytes on 32-bit/WASM32.
// None of its fields are producer-concurrent: callbacks and IRQs retain only
// an OperationID and the source owner enqueues this record after draining it.
type WaitSetRecord struct {
	g          *G
	activePrev *WaitSetRecord
	activeNext *WaitSetRecord
	workNext   *WaitSetRecord
	ticket     ParkTicket
	state      waitSetRecordState
	work       waitSetWorkState
	_          [2]byte
}

// PrepareWaitSetRecord binds zero caller storage to one preparing logical
// park. It must run before any record-aware OperationRecord is attached or
// made producer-visible, so initialization itself cannot fail after admission.
func PrepareWaitSetRecord(record *WaitSetRecord, g *G, ticket ParkTicket) bool {
	if record == nil || *record != (WaitSetRecord{}) || !ValidG(g) || !validParkTicket(ticket) ||
		!validParkState(&g.park) || g.park.phase != parkPreparing || g.park.ticket != ticket {
		return false
	}
	record.g = g
	record.ticket = ticket
	record.state = waitSetRecordPreparing
	return true
}

func validPreparingWaitSetRecord(record *WaitSetRecord, state *ParkState, ticket ParkTicket) bool {
	return record != nil && record.g != nil && &record.g.park == state && record.ticket == ticket &&
		record.state == waitSetRecordPreparing && record.work == waitSetWorkIdle &&
		record.activePrev == nil && record.activeNext == nil && record.workNext == nil
}

func validCommittedWaitSetRecord(record *WaitSetRecord, g *G, frame *Frame) bool {
	return record != nil && record.g == g && frame != nil && frame.owner == g && frame.parkWait == record &&
		record.ticket == g.park.ticket && record.state == waitSetRecordCommitted &&
		record.work == waitSetWorkIdle && record.activePrev == nil && record.activeNext == nil && record.workNext == nil
}

func validParkWaitQueueHeader(p *P) bool {
	return p != nil && (p.parkWaitHead == nil) == (p.parkWaitTail == nil) &&
		(p.parkWaitHead == nil || p.parkWaitHead.activePrev == nil && p.parkWaitTail.activeNext == nil)
}

func validAffectedWaitQueueHeader(p *P) bool {
	return p != nil && (p.affectedWaitHead == nil) == (p.affectedWaitTail == nil) &&
		(p.affectedWaitTail == nil || p.affectedWaitTail.workNext == nil)
}

func validActiveParkStateHeader(state *ParkState, ticket ParkTicket) bool {
	if state == nil || state.ticket != ticket || !validParkTicket(ticket) ||
		!validTaskCancelState(state.taskCancelKind, state.taskCancelPhase) ||
		state.cancelKind > ParkCancelShutdown || state.attached > state.expected {
		return false
	}
	switch state.phase {
	case parkParked:
		return state.attached == state.expected && state.outcome == ParkOutcomePending &&
			(state.hasDefault || state.winnerCase == 0) &&
			validPendingParkCommitCursor(state) &&
			(state.attached == 0) == (state.head == nil) &&
			(state.head == nil || state.head.previous == nil)
	case parkDetaching:
		if state.attached == 0 || state.head == nil || state.head.previous != nil {
			return false
		}
	case parkReady:
		if state.attached != 0 || state.head != nil {
			return false
		}
	default:
		return false
	}
	switch state.outcome {
	case ParkOutcomeCompleted:
		return !state.hasDefault && state.cancelKind < ParkCancelTaskAbort && state.winnerID.Valid() &&
			state.winnerRecord != nil && state.winnerRecord.id == state.winnerID
	case ParkOutcomeCanceled:
		return !state.hasDefault && state.cancelKind != ParkCancelNone && state.winnerCase == 0 &&
			state.winnerID == (OperationID{}) && state.winnerRecord == nil
	case ParkOutcomeDefault:
		return state.hasDefault && state.cancelKind == ParkCancelNone && state.winnerID == (OperationID{}) && state.winnerRecord == nil
	default:
		return false
	}
}

// validActiveWaitSetRecordFast checks only local queue endpoints and scalar
// ParkState headers. It never walks candidate ParkLinks and is the predicate
// used by fact publication, cancellation, affected pop, and promotion.
func validActiveWaitSetRecordFast(p *P, record *WaitSetRecord) bool {
	if p == nil || record == nil || record.state != waitSetRecordActive || !validParkTicket(record.ticket) ||
		record.g == nil || !ValidG(record.g) || record.g.state != GWaiting || !record.g.waiting ||
		record.g.waitToken != nil || record.g.waitTicket != 0 || record.g.nextWait != nil ||
		record.g.queued || record.g.nextReady != nil || record.g.runP != nil || record.g.active == nil ||
		record.g.active.parkWait != record || !validActiveParkStateHeader(&record.g.park, record.ticket) {
		return false
	}
	if record.activePrev == nil {
		if p.parkWaitHead != record {
			return false
		}
	} else if record.activePrev.activeNext != record {
		return false
	}
	if record.activeNext == nil {
		return p.parkWaitTail == record
	}
	return record.activeNext.activePrev == record
}

func validActiveWaitSetRecord(p *P, record *WaitSetRecord) bool {
	return validActiveWaitSetRecordFast(p, record) && validParkSetWaitingG(record.g)
}

// validParkWaitQueue is the allocation-free full audit retained for tests,
// shutdown, and fail-stop diagnostics. Hot executor passes use only the O(1)
// header predicate plus validation of the affected records they actually pop.
func validParkWaitQueue(p *P) bool {
	if !validParkWaitQueueHeader(p) || !validAffectedWaitQueueHeader(p) {
		return false
	}
	for slow, fast := p.parkWaitHead, p.parkWaitHead; fast != nil && fast.activeNext != nil; {
		slow = slow.activeNext
		fast = fast.activeNext.activeNext
		if slow == fast {
			return false
		}
	}
	var tail *WaitSetRecord
	for record := p.parkWaitHead; record != nil; record = record.activeNext {
		if !validActiveWaitSetRecord(p, record) {
			return false
		}
		tail = record
	}
	if tail != p.parkWaitTail {
		return false
	}
	for slow, fast := p.affectedWaitHead, p.affectedWaitHead; fast != nil && fast.workNext != nil; {
		slow = slow.workNext
		fast = fast.workNext.workNext
		if slow == fast {
			return false
		}
	}
	var affectedTail *WaitSetRecord
	for record := p.affectedWaitHead; record != nil; record = record.workNext {
		if record.work != waitSetWorkQueued || !validActiveWaitSetRecord(p, record) {
			return false
		}
		affectedTail = record
	}
	return affectedTail == p.affectedWaitTail
}

func canAppendAffectedWaitSet(p *P, record *WaitSetRecord) bool {
	if !validParkWaitQueueHeader(p) || !validAffectedWaitQueueHeader(p) ||
		!validActiveWaitSetRecordFast(p, record) {
		return false
	}
	switch record.work {
	case waitSetWorkQueued:
		return true
	case waitSetWorkResolving:
		return true
	case waitSetWorkResolvingDirty:
		return true
	case waitSetWorkAwaitingExternal:
		return record.workNext == nil
	case waitSetWorkIdle:
		if record.workNext != nil {
			return false
		}
	default:
		return false
	}
	return true
}

func appendAffectedWaitSetUnchecked(p *P, record *WaitSetRecord) {
	switch record.work {
	case waitSetWorkQueued, waitSetWorkResolvingDirty:
		return
	case waitSetWorkResolving:
		record.work = waitSetWorkResolvingDirty
		return
	case waitSetWorkIdle, waitSetWorkAwaitingExternal:
	}
	record.work = waitSetWorkQueued
	if p.affectedWaitTail == nil {
		p.affectedWaitHead = record
	} else {
		p.affectedWaitTail.workNext = record
	}
	p.affectedWaitTail = record
}

func appendAffectedWaitSet(p *P, record *WaitSetRecord) bool {
	if !canAppendAffectedWaitSet(p, record) {
		return false
	}
	appendAffectedWaitSetUnchecked(p, record)
	return true
}

// MarkWaitSetAffected is the owner-P bridge used after an owner-side
// completion or logical cancellation becomes sticky. It never allocates and
// coalesces every candidate/source fact for the same logical wait-set.
func MarkWaitSetAffected(p *P, record *WaitSetRecord) bool {
	return appendAffectedWaitSet(p, record)
}

// RequestWaitSetCancel publishes an owner-side logical cancellation and makes
// the exact active wait-set visible to the next published-epoch resolver. The queue
// preflight runs before the monotonic cancellation mutation, making a valid
// call allocation-free and failure-atomic.
func RequestWaitSetCancel(p *P, record *WaitSetRecord, kind ParkCancelKind) bool {
	if !canAppendAffectedWaitSet(p, record) || record.g.park.phase != parkParked ||
		record.g.park.winnerRecord != nil || kind < ParkCancelOperation || kind > ParkCancelShutdown {
		return false
	}
	if kind > record.g.park.cancelKind {
		record.g.park.cancelKind = kind
	}
	appendAffectedWaitSetUnchecked(p, record)
	return true
}

func activateWaitSetRecord(p *P, g *G, record *WaitSetRecord) bool {
	if !validParkWaitQueueHeader(p) || !validAffectedWaitQueueHeader(p) ||
		!validCommittedWaitSetRecord(record, g, g.active) || g.state != GWaiting || g.waiting ||
		g.nextWait != nil || g.waitToken != nil || g.waitTicket != 0 || g.queued || g.nextReady != nil || g.runP != nil ||
		!validParkState(&g.park) || g.park.phase != parkParked {
		return false
	}
	record.activePrev = p.parkWaitTail
	record.state = waitSetRecordActive
	g.waiting = true
	if p.parkWaitTail == nil {
		p.parkWaitHead = record
	} else {
		p.parkWaitTail.activeNext = record
	}
	p.parkWaitTail = record

	// Every newly parked set receives one initial visit. This catches an owner
	// completion published during preparation without adding an always-live G
	// flag; a still-pending snapshot is simply removed from the work queue. All
	// affected-queue and record-idle preconditions were checked before the
	// active-list mutation, so there is no fallible step after scheduler commit.
	appendAffectedWaitSetUnchecked(p, record)
	return true
}

// resolveAffectedWaitSets detaches the current FIFO as one published-epoch batch.
// Pending initial visits are discarded; terminal or already-detaching parks
// remain in the returned linear batch until every source has applied and
// detached its OperationRecords.
func resolveAffectedWaitSets(p *P, sources *ExecutorSourceSet) (batchHead, batchTail *WaitSetRecord, total CompletionResolution, ok bool) {
	if !validParkWaitQueueHeader(p) || !validAffectedWaitQueueHeader(p) {
		return nil, nil, CompletionResolution{}, false
	}
	head := p.affectedWaitHead
	p.affectedWaitHead, p.affectedWaitTail = nil, nil
	for record := head; record != nil; {
		next := record.workNext
		record.workNext = nil
		if record.work != waitSetWorkQueued || !validActiveWaitSetRecordFast(p, record) {
			return batchHead, batchTail, total, false
		}
		record.work = waitSetWorkResolving

		keep := false
		switch record.g.park.phase {
		case parkParked:
			var resolution CompletionResolution
			var resolved bool
			if sources == nil {
				resolution, resolved = ResolveParkSnapshot(&record.g.park, record.ticket)
			} else {
				resolution, resolved = sources.resolveCommitCapablePark(&record.g.park, record.ticket)
			}
			if !resolved || resolution.WaitSets != 1 {
				return batchHead, batchTail, total, false
			}
			total.WaitSets += resolution.WaitSets
			total.Completed += resolution.Completed
			total.Canceled += resolution.Canceled
			total.Defaulted += resolution.Defaulted
			total.Winners += resolution.Winners
			total.Losers += resolution.Losers
			keep = resolution.Completed+resolution.Canceled+resolution.Defaulted != 0
		case parkDetaching, parkReady:
			keep = true
		default:
			return batchHead, batchTail, total, false
		}
		if keep {
			if batchTail == nil {
				batchHead = record
			} else {
				batchTail.workNext = record
			}
			batchTail = record
		} else if record.work == waitSetWorkResolvingDirty {
			record.work = waitSetWorkIdle
			if !appendAffectedWaitSet(p, record) {
				return batchHead, batchTail, total, false
			}
		} else {
			record.work = waitSetWorkIdle
		}
		record = next
	}
	return batchHead, batchTail, total, true
}

func appendRunnableUnchecked(p *P, g *G) {
	g.queued = true
	if p.readyTail == nil {
		p.readyHead = g
	} else {
		p.readyTail.nextReady = g
	}
	p.readyTail = g
}

func promoteReadyWaitSet(p *P, record *WaitSetRecord) bool {
	if !validActiveWaitSetRecordFast(p, record) || record.work != waitSetWorkResolving ||
		record.g.park.phase != parkReady || !validReadyQueueHeader(p) {
		return false
	}
	g := record.g
	frame := g.active
	schedule := preemptLoad(&p.schedule)
	if frame == nil || frame.parkWait != record || g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		(schedule != scheduleIdle && schedule != scheduleRequested) {
		return false
	}
	previous, next := record.activePrev, record.activeNext
	if previous == nil {
		p.parkWaitHead = next
	} else {
		previous.activeNext = next
	}
	if next == nil {
		p.parkWaitTail = previous
	} else {
		next.activePrev = previous
	}
	frame.parkWait = nil
	g.waiting = false
	g.state = GRunnable
	appendRunnableUnchecked(p, g)
	*record = WaitSetRecord{}
	return true
}

// promoteResolvedWaitSets completes the post-source-apply half of one published
// epoch. A still-detaching record stays on the small affected queue; a later
// source acknowledgement therefore never requires rediscovering it by walking
// every parked G.
func promoteResolvedWaitSets(p *P, batch *WaitSetRecord) (promoted int, ok bool) {
	if !validParkWaitQueueHeader(p) || !validAffectedWaitQueueHeader(p) {
		return 0, false
	}
	for record := batch; record != nil; {
		next := record.workNext
		record.workNext = nil
		if record.work != waitSetWorkResolving && record.work != waitSetWorkResolvingDirty &&
			record.work != waitSetWorkAwaitingExternal {
			return promoted, false
		}
		if record.work == waitSetWorkAwaitingExternal {
			if record.g.park.phase != parkDetaching {
				return promoted, false
			}
			record = next
			continue
		}
		dirty := record.work == waitSetWorkResolvingDirty
		record.work = waitSetWorkResolving
		switch record.g.park.phase {
		case parkReady:
			if !promoteReadyWaitSet(p, record) {
				return promoted, false
			}
			promoted++
		case parkDetaching:
			record.work = waitSetWorkIdle
			if !appendAffectedWaitSet(p, record) {
				return promoted, false
			}
		case parkParked:
			if !dirty {
				return promoted, false
			}
			record.work = waitSetWorkIdle
			if !appendAffectedWaitSet(p, record) {
				return promoted, false
			}
		default:
			return promoted, false
		}
		record = next
	}
	return promoted, true
}

// ReleasePreparedWaitSetRecord releases a preparation which never entered the
// scheduler's active V2 queue. Every attached source must already have passed
// the normal abort/detach barrier, leaving no ParkLink that can retain it.
func ReleasePreparedWaitSetRecord(record *WaitSetRecord) bool {
	if record == nil || record.state != waitSetRecordPreparing || record.work != waitSetWorkIdle ||
		record.g == nil || record.activePrev != nil || record.activeNext != nil || record.workNext != nil ||
		!validParkState(&record.g.park) || record.g.park.ticket != record.ticket || record.g.park.attached != 0 ||
		record.g.park.head != nil || (record.g.park.phase != parkReady && record.g.park.phase != parkConsumed) {
		return false
	}
	*record = WaitSetRecord{}
	return true
}
