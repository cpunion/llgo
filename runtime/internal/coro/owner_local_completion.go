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

import "unsafe"

// ownerLocalCompletionCursor is the scheduler-owned fast lane for a physical
// completion produced by the G which currently owns this exact P. The producer
// publishes the typed source result while it already owns the source's runtime
// synchronization domain, then appends only the affected logical wait here.
// The scheduler resolves it after llvm.coro.resume returns; it never resumes a
// peer recursively or performs typed cleanup inside the producer's lock.
//
// Cross-P, callback, timer, poller, and foreign-thread producers cannot enter
// this queue. They retain the durable source pending + executor request +
// A/ack/B protocol. workNext is sufficient because a locally queued record was
// required to be owner-idle before publication.
type ownerLocalCompletionCursor struct {
	head    *WaitSetRecord
	tail    *WaitSetRecord
	resolve publishedEpochResolveCursor
	// scratch is scheduler-owner-only transient storage for one source
	// reduction. Keeping it with the stable driver avoids forcing LLGo's
	// conservative escape analysis to heap-allocate a publishedEpochResolveStep
	// every time serviceExecutorRunLocal passes its address across a function
	// boundary. It is always cleared before that service returns and is not part
	// of the durable completion state.
	scratch publishedEpochResolveStep
}

type ownerLocalCompletionAdmission uint8

const (
	ownerLocalCompletionRejected ownerLocalCompletionAdmission = iota
	ownerLocalCompletionIdle
	ownerLocalCompletionAffectedHead
)

// ownerLocalDirectChannelCleanupHeader is the hot selector for an already
// certified direct cleanup continuation. Every pointer and immutable descriptor
// field below was fully audited immediately before directChannel was installed;
// only CommitResumeCleanupStep may mutate this interval, and it can change only
// phase/index/small through its exact plan token. The final source reduction
// calls ownerLocalDirectChannelCleanupState once before any retirement effect.
func ownerLocalDirectChannelCleanupHeader(
	cursor *publishedEpochResolveCursor,
) (*WaitSetRecord, *ResumeCleanupPlan, *OperationID, bool) {
	if cursor == nil || !cursor.directChannel || cursor.phase != publishedEpochResolvePromote ||
		cursor.park != (parkResolutionCursor{}) || cursor.link != nil || cursor.claim != nil ||
		cursor.forced != nil || cursor.waitRetry || cursor.waitAwait || cursor.hasChannel ||
		cursor.claimOwned || cursor.wait == nil || cursor.batchTail == nil ||
		cursor.batchTail.workNext != nil || cursor.nextWait != cursor.wait.workNext {
		return nil, nil, nil, false
	}
	wait := cursor.wait
	if wait.state != waitSetRecordActive || wait.resumeKind != resumeBindingCleanup ||
		wait.resume == nil || wait.g == nil || wait.work != waitSetWorkResolving ||
		wait.g.park.phase != parkConsumed {
		return nil, nil, nil, false
	}
	plan := (*ResumeCleanupPlan)(wait.resume)
	if plan == nil || plan.packet == nil || plan.claim == nil || plan.context == nil ||
		plan.entries == nil || plan.kind != ResumeCleanupChannelDirect || plan.index != 0 {
		return nil, nil, nil, false
	}
	if plan.phase != resumeCleanupRuntime && plan.phase != resumeCleanupConfirm {
		return nil, nil, nil, false
	}
	id := (*OperationID)(unsafe.Add(plan.entries, plan.idOffset))
	return wait, plan, id, true
}

// ownerLocalDirectChannelCleanupState performs the one complete scalar audit
// at the effectful retirement boundary. The two intervening runner selections
// use only ownerLocalDirectChannelCleanupHeader, so arbitrary graph validation
// is never multiplied by the number of scheduler host entries.
func ownerLocalDirectChannelCleanupState(
	cursor *publishedEpochResolveCursor,
) (*WaitSetRecord, *ResumeCleanupPlan, *OperationID, bool) {
	wait, plan, id, headerOK := ownerLocalDirectChannelCleanupHeader(cursor)
	if !headerOK || !ValidG(wait.g) || wait.ticket != wait.g.park.ticket ||
		!validParkTicket(wait.ticket) || wait.g.state != GWaiting || !wait.g.waiting ||
		wait.g.queued || wait.g.nextReady != nil || wait.g.runP != nil ||
		wait.g.transferState != runnableTransferGIdle || wait.g.active == nil ||
		wait.g.active.parkWait != wait {
		return nil, nil, nil, false
	}
	idSize := unsafe.Sizeof(OperationID{})
	if plan.count != 1 || plan.runtime != 1 || plan.ticket != wait.ticket ||
		plan.stride < idSize || plan.idOffset > plan.stride-idSize || plan.caseID != 1 ||
		plan.outcome != ParkOutcomeCompleted || !plan.lease.Valid() ||
		plan.result != ResumeResultNone || selectClaimLoad(plan.claim) != selectClaimClaimed ||
		plan.phase == resumeCleanupRuntime && plan.small != ResumeSmallInvalid ||
		plan.phase == resumeCleanupConfirm && plan.small == ResumeSmallInvalid {
		return nil, nil, nil, false
	}
	packet := plan.packet
	if packet.state != resumePacketBound || packet.ticket != wait.ticket ||
		packet.source != (OperationID{}) || packet.scalar != (ScalarResultPayloadV1{}) ||
		packet.caseID != 0 || packet.outcome != ParkOutcomePending ||
		packet.result != ResumeResultNone || packet.small != ResumeSmallInvalid {
		return nil, nil, nil, false
	}
	leaseID, leaseOK := plan.lease.ID()
	if !id.Valid() || id.Source() != OperationSourceChannel || !leaseOK || leaseID != *id ||
		plan.lease.ticket != wait.ticket {
		return nil, nil, nil, false
	}
	state := &wait.g.park
	if state.phase != parkConsumed || state.resolving || state.expected != 1 ||
		state.attached != 0 || state.seed != 0 || state.hasDefault ||
		state.taskCancelKind != TaskCancelNone || state.taskCancelPhase != taskCancelIdle ||
		state.cancelKind != ParkCancelNone || state.outcome != ParkOutcomeCompleted ||
		state.winnerCase != 1 || state.winnerID != *id || state.winnerRecord != nil ||
		state.head != nil {
		return nil, nil, nil, false
	}
	return wait, plan, id, true
}

func validOwnerLocalDirectChannelCursor(
	cursor *publishedEpochResolveCursor,
	p *P,
) bool {
	wait, _, _, ok := ownerLocalDirectChannelCleanupHeader(cursor)
	if !ok || p == nil {
		return false
	}
	if wait.activePrev == nil {
		if p.parkWaitHead != wait {
			return false
		}
	} else if wait.activePrev.activeNext != wait {
		return false
	}
	if wait.activeNext == nil {
		return p.parkWaitTail == wait
	}
	return wait.activeNext.activePrev == wait
}

func validOwnerLocalCompletionHeader(local *ownerLocalCompletionCursor, p *P) bool {
	if local == nil || p == nil || (local.head == nil) != (local.tail == nil) ||
		local.tail != nil && local.tail.workNext != nil {
		return false
	}
	if local.resolve.phase == publishedEpochResolveIdle {
		if local.resolve != (publishedEpochResolveCursor{}) {
			return false
		}
	} else if local.head != nil || local.resolve.directChannel &&
		!validOwnerLocalDirectChannelCursor(&local.resolve, p) ||
		!local.resolve.directChannel && !validPublishedEpochResolveCursor(&local.resolve, p) {
		return false
	}
	return local.head == nil || local.head.work == waitSetWorkQueued &&
		validActiveWaitSetRecordFast(p, local.head)
}

func validOwnerLocalCompletion(local *ownerLocalCompletionCursor, p *P) bool {
	if !validOwnerLocalCompletionHeader(local, p) {
		return false
	}
	var tail *WaitSetRecord
	for slow, fast := local.head, local.head; fast != nil && fast.workNext != nil; {
		slow = slow.workNext
		fast = fast.workNext.workNext
		if slow == fast {
			return false
		}
	}
	for record := local.head; record != nil; record = record.workNext {
		if record.work != waitSetWorkQueued || !validActiveWaitSetRecordFast(p, record) {
			return false
		}
		tail = record
	}
	return tail == local.tail
}

func emptyOwnerLocalCompletion(local *ownerLocalCompletionCursor) bool {
	return local != nil && local.head == nil && local.tail == nil &&
		local.resolve == (publishedEpochResolveCursor{}) &&
		local.scratch == (publishedEpochResolveStep{})
}

func ownerLocalCompletionAdmissionFor(
	driver *ExecutorDriver,
	record *WaitSetRecord,
) ownerLocalCompletionAdmission {
	if driver == nil || !validRunningExecutorOwner(driver) {
		return ownerLocalCompletionRejected
	}
	return ownerLocalCompletionAdmissionForCurrent(driver, record)
}

// ownerLocalCompletionAdmissionForCurrent is the transient-capability half of
// ownerLocalCompletionAdmissionFor. Callers must have obtained driver together
// with the currently running G from CurrentExecutorDriver and must not cross a
// suspension before this call. The reciprocal G/P/source identities are
// checked at that public boundary; repeating the complete running-frame proof
// here would turn every same-P channel handoff into another full scheduler
// validation.
func ownerLocalCompletionAdmissionForCurrent(
	driver *ExecutorDriver,
	record *WaitSetRecord,
) ownerLocalCompletionAdmission {
	if driver == nil || driver.p == nil ||
		!validOwnerLocalCompletionHeader(&driver.local, driver.p) ||
		driver.local.resolve != (publishedEpochResolveCursor{}) || record == nil ||
		!validActiveWaitSetRecordFast(driver.p, record) {
		return ownerLocalCompletionRejected
	}
	if record.work == waitSetWorkIdle && record.workNext == nil {
		return ownerLocalCompletionIdle
	}
	// A newly parked wait carries one mandatory initial visit. If it is the
	// exact affected FIFO head, the stronger same-owner completion can consume
	// that visit without scanning or unlinking an interior node. Validate the
	// new head as well so the O(1) splice cannot conceal a malformed prefix.
	p := driver.p
	if record.work != waitSetWorkQueued || p.affectedWaitHead != record ||
		!validAffectedWaitQueueHeader(p) {
		return ownerLocalCompletionRejected
	}
	next := record.workNext
	if next == nil {
		if p.affectedWaitTail != record {
			return ownerLocalCompletionRejected
		}
	} else if next == record || p.affectedWaitTail == record ||
		next.work != waitSetWorkQueued || !validActiveWaitSetRecordFast(p, next) {
		return ownerLocalCompletionRejected
	}
	return ownerLocalCompletionAffectedHead
}

// ownerLocalDirectCompletionAdmissionForCurrent consumes the stronger frozen
// direct-channel capability after prepareOwnerLocalDirectHeld has matched its
// source generation, claim, cleanup plan, and exact owner P. Resumed performed
// the complete ParkState/G/active-queue audit before setting record Active;
// another G running on that same P excludes every owner mutation here.
//
// The compact lane accepts only an empty local resolver and an isolated
// mandatory affected visit. More complex FIFO shapes fall back to
// ownerLocalCompletionAdmissionForCurrent and retain its complete checks.
func ownerLocalDirectCompletionAdmissionForCurrent(
	driver *ExecutorDriver,
	record *WaitSetRecord,
) ownerLocalCompletionAdmission {
	if driver == nil || driver.p == nil || record == nil ||
		!emptyOwnerLocalCompletion(&driver.local) || record.state != waitSetRecordActive {
		return ownerLocalCompletionRejected
	}
	if record.work == waitSetWorkIdle && record.workNext == nil {
		return ownerLocalCompletionIdle
	}
	p := driver.p
	if record.work != waitSetWorkQueued || record.workNext != nil ||
		p.affectedWaitHead != record || p.affectedWaitTail != record {
		return ownerLocalCompletionRejected
	}
	return ownerLocalCompletionAffectedHead
}

func appendOwnerLocalCompletionUnchecked(
	driver *ExecutorDriver,
	record *WaitSetRecord,
	admission ownerLocalCompletionAdmission,
) {
	if admission == ownerLocalCompletionAffectedHead {
		next := record.workNext
		driver.p.affectedWaitHead = next
		if next == nil {
			driver.p.affectedWaitTail = nil
		}
		record.workNext = nil
	}
	record.work = waitSetWorkQueued
	if driver.local.tail == nil {
		driver.local.head = record
	} else {
		driver.local.tail.workNext = record
	}
	driver.local.tail = record
}

// completeOwnerLocalDirectChannelInline consumes the same-P capability which
// prepareOwnerLocalDirectHeld established before the hchan effect. The source
// admission and private select claim have already made that effect terminal;
// for an ordinary, uncanceled one-case park there is no typed runtime work to
// defer to an ExecutorRunStep. Collapse the fixed owner-only resolve/apply/
// materialize sequence and append the peer to this P's ready queue. Select,
// cancellation, an occupied local resolver, and every cross-owner completion
// retain the ordinary owner-local or durable source path.
//
// handled=false is a clean pre-effect-shape fallback and makes no mutation.
// Once handled is true, ok=false is an invariant failure after the physical
// channel effect and must remain fail-closed.
func completeOwnerLocalDirectChannelInline(
	driver *ExecutorDriver,
	wait *WaitSetRecord,
	admission ownerLocalCompletionAdmission,
	source *ChannelOperationSource,
	slot *channelOperationSlot,
	id OperationID,
	small uint8,
) (handled, ok bool) {
	if driver == nil || wait == nil || wait.g == nil || source == nil || slot == nil || !id.Valid() ||
		small == ResumeSmallInvalid || driver.p == nil || source != driver.sources.channel ||
		source.owner != driver.p || source.route != driver.route || id.Route() != driver.route ||
		driver.local.head != nil || driver.local.tail != nil ||
		driver.local.resolve != (publishedEpochResolveCursor{}) ||
		driver.local.scratch != (publishedEpochResolveStep{}) {
		return false, true
	}
	p, g := driver.p, wait.g
	state, frame := &g.park, g.active
	if !wait.directChannel || wait.state != waitSetRecordActive ||
		g.state != GWaiting || !g.waiting || g.queued || g.nextReady != nil || g.runP != nil ||
		g.transferState != runnableTransferGIdle || frame == nil || frame.parkWait != wait ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		state.phase != parkParked || state.resolving || state.ticket != wait.ticket ||
		!validParkTicket(wait.ticket) || state.expected != 1 || state.attached != 1 ||
		state.seed > 1 || state.hasDefault || state.cancelKind != ParkCancelNone ||
		state.taskCancelKind != TaskCancelNone || state.taskCancelPhase != taskCancelIdle ||
		state.outcome != ParkOutcomePending || state.winnerCase != 0 ||
		state.winnerID != (OperationID{}) || state.winnerRecord != nil {
		return false, true
	}
	plan := (*ResumeCleanupPlan)(wait.resume)
	if !validDirectChannelBoundResumeState(wait, plan) ||
		*(*OperationID)(plan.entries) != id || selectClaimLoad(plan.claim) != selectClaimClaimed {
		return true, false
	}
	// CommitOwnerLocalDirectWithResult sealed producer ingress immediately
	// after publishing the physical result and terminal claim. Do not begin the
	// irreversible inline detach until every producer admitted before that seal
	// has left. The already prepared owner-local FIFO retries ApplyOne after
	// quiescence without losing the committed typed result.
	if preemptLoad(&slot.state) != uint32(producerSourceClosing) ||
		!producerSourceSlotQuiesced(&slot.producerSourceSlot) {
		return false, true
	}
	record, link := &slot.record, &slot.record.link
	preferred, physicalSmall, physicalOK := channelPhysicalCompletion(preemptLoad(&slot.physical))
	ready, readyOK := channelOperationReadyAt(source, id.LocalSlot()-1)
	schedule := preemptLoad(&p.schedule)
	if !physicalOK || physicalSmall != small || preferred != 0 && !preferred.Valid() ||
		!readyOK || ready || preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxEmpty) ||
		preemptLoad(&slot.external) != uint32(channelExternalExposed) ||
		preemptLoad(&slot.externalLease)&1 != 0 || slot.claim != plan.claim ||
		record.id != id || record.phase != operationActive || record.cancelRequested || record.quiesced ||
		!operationCandidateExternallyCommitted(record) ||
		link.park != state || link.wait != wait || link.operation != record ||
		link.ticket != wait.ticket || link.caseID != 1 || link.previous != nil || link.next != nil ||
		state.head != link || p.externalWaitCount == 0 ||
		!validReadyQueueHeader(p) || p.readyCount == ^uint32(0) ||
		(schedule != scheduleIdle && schedule != scheduleRequested) {
		return true, false
	}
	previous, next := wait.activePrev, wait.activeNext
	if previous == nil {
		if p.parkWaitHead != wait {
			return true, false
		}
	} else if previous.activeNext != wait {
		return true, false
	}
	if next == nil {
		if p.parkWaitTail != wait {
			return true, false
		}
	} else if next.activePrev != wait {
		return true, false
	}

	var affectedNext *WaitSetRecord
	switch admission {
	case ownerLocalCompletionIdle:
		if wait.work != waitSetWorkIdle || wait.workNext != nil {
			return true, false
		}
	case ownerLocalCompletionAffectedHead:
		if wait.work != waitSetWorkQueued || p.affectedWaitHead != wait {
			return true, false
		}
		affectedNext = wait.workNext
		if affectedNext == nil {
			if p.affectedWaitTail != wait {
				return true, false
			}
		} else if affectedNext == wait || p.affectedWaitTail == wait ||
			affectedNext.work != waitSetWorkQueued {
			return true, false
		}
	default:
		return true, false
	}

	// Every fallible observation is complete above. Producer ingress is sealed
	// and quiesced, the claim excludes a resolver, and this P excludes owner
	// mutation, so the intermediate Detaching/Ready/Consumed and
	// Detached/Quiesced/Taken states are not externally observable. Commit their
	// no-fail write halves as one closed transaction and publish Free/Runnable
	// only after their complete suffixes are canonical.
	if admission == ownerLocalCompletionAffectedHead {
		p.affectedWaitHead = affectedNext
		if affectedNext == nil {
			p.affectedWaitTail = nil
		}
	}
	packet, claim, entry := plan.packet, plan.claim, (*OperationID)(plan.entries)
	ticket := wait.ticket

	slot.claim = nil
	preemptStore(&slot.external, 0)
	preemptStore(&slot.physical, uint32(channelPhysicalIdle))
	*record = OperationRecord{id: id, phase: operationReusable}
	source.reserveCursor = 0
	preemptStore(&slot.state, uint32(producerSourceFree))
	preemptStore(&claim.state, uint32(selectClaimOpen))

	*entry = OperationID{}
	*packet = ResumePacket{
		ticket: ticket, caseID: 1, outcome: ParkOutcomeCompleted,
		result: ResumeResultChannel, small: small, state: resumePacketMaterialized,
	}
	*plan = ResumeCleanupPlan{}
	p.externalWaitCount--
	*state = ParkState{
		ticket: ticket, phase: parkMaterialized, seed: uint32(preferred),
		outcome: ParkOutcomeCompleted, winnerCase: 1,
	}
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
	*wait = WaitSetRecord{}
	g.waiting = false
	g.state = GRunnable
	appendRunnableUnchecked(p, g)
	// This is the inline equivalent of a complete serviceExecutorRunLocal
	// reduction. Pay one ready continuation before an unrelated source epoch;
	// otherwise the newly parked current G's mandatory initial visit would make
	// the runner scan every catalog before dispatching the peer we just woke.
	driver.run.blocked = false
	driver.run.actionsSinceSource = 0
	driver.run.readyDebt = true
	return true, true
}

func ownerLocalCompletionPending(driver *ExecutorDriver) bool {
	// Include a lone tail so malformed selected state enters the exact cold
	// validator and fails closed instead of being skipped by the hot selector.
	return driver != nil && (driver.local.head != nil || driver.local.tail != nil ||
		driver.local.resolve.phase != publishedEpochResolveIdle)
}

// initializeOwnerLocalCompletionResolutionAfterHeader consumes the exact
// owner-local FIFO certified by resolveOwnerLocalCompletionStep. It has one
// caller and must remain immediately after that caller's ready/park/affected
// and local-header gate; repeating those O(1) graph checks here made every
// direct handoff validate the same active WaitSetRecord twice before mutation.
func initializeOwnerLocalCompletionResolutionAfterHeader(
	driver *ExecutorDriver,
	step *publishedEpochResolveStep,
) bool {
	if driver == nil || step == nil || driver.local.resolve != (publishedEpochResolveCursor{}) ||
		driver.local.head == nil || driver.local.tail == nil {
		return false
	}
	head, tail := driver.local.head, driver.local.tail
	driver.local.resolve.batchTail = tail
	if !startPublishedEpochWait(&driver.sources, &driver.local.resolve, head) {
		driver.local.resolve.batchTail = nil
		return false
	}
	driver.local.head, driver.local.tail = nil, nil
	return true
}

// resolveOwnerLocalCompletionStep performs one ordinary published-epoch
// resolution reduction, but starts from the exact owner-local FIFO instead of
// rescanning every source and acknowledging a doorbell which was never needed.
// Source-specific ApplyOne and typed materialization remain identical to the
// external path.
func resolveOwnerLocalCompletionStep(
	driver *ExecutorDriver,
	step *publishedEpochResolveStep,
) (ok bool) {
	if driver == nil || step == nil {
		return false
	}
	*step = publishedEpochResolveStep{}
	p, cursor := driver.p, &driver.local.resolve
	if p == nil || !validReadyQueueHeader(p) || !validParkWaitQueueHeader(p) ||
		!validAffectedWaitQueueHeader(p) || !validOwnerLocalCompletionHeader(&driver.local, p) {
		return false
	}
	if cursor.phase == publishedEpochResolveIdle {
		if !initializeOwnerLocalCompletionResolutionAfterHeader(driver, step) {
			return false
		}
	} else if !cursor.directChannel && !validPublishedEpochResolveCursor(cursor, p) {
		return false
	}
	if handled, directOK := resolveOwnerLocalDirectChannelStep(driver, cursor, step); handled {
		return directOK
	}

	switch cursor.phase {
	case publishedEpochResolveDiscover:
		ok = resolvePublishedEpochDiscoverStep(&driver.sources, p, cursor, step)
	case publishedEpochResolvePark:
		ok = resolvePublishedEpochParkStep(&driver.sources, p, cursor, step)
	case publishedEpochResolveApply:
		ok = resolvePublishedEpochApplyStep(&driver.sources, p, cursor, step)
	case publishedEpochResolveFinish:
		ok = resolvePublishedEpochFinishStep(cursor, step)
	case publishedEpochResolvePromote:
		ok = resolvePublishedEpochPromoteStep(&driver.sources, p, cursor, step)
	default:
		ok = false
	}
	return ok
}

// ownerLocalDirectChannelPlan recognizes only the compiler-owned one-case
// channel cleanup binding. The plan and ParkState together are the proof that
// this is not a select, timer, poll, worker, or cancellation-only event. A
// failed recognition is an ordinary fallback to the common persisted cursor.
func ownerLocalDirectChannelPlan(
	driver *ExecutorDriver,
	cursor *publishedEpochResolveCursor,
) (*WaitSetRecord, *ResumeCleanupPlan, *OperationID, *channelOperationSlot, bool) {
	if driver == nil || cursor == nil || driver.sources.channel == nil {
		return nil, nil, nil, nil, false
	}
	var wait *WaitSetRecord
	var plan *ResumeCleanupPlan
	var id *OperationID
	if cursor.directChannel {
		var stateOK bool
		wait, plan, id, stateOK = ownerLocalDirectChannelCleanupState(cursor)
		if !stateOK || plan.phase != resumeCleanupConfirm {
			return nil, nil, nil, nil, false
		}
	} else {
		if cursor.wait == nil || cursor.wait.resumeKind != resumeBindingCleanup {
			return nil, nil, nil, nil, false
		}
		wait = cursor.wait
		if wait.directChannel {
			var headerOK bool
			plan, headerOK = directChannelBoundResumeHeader(wait)
			if !headerOK {
				return nil, nil, nil, nil, false
			}
		} else {
			plan = (*ResumeCleanupPlan)(wait.resume)
			if !validTrustedResumeCleanupPlanState(wait, plan) {
				return nil, nil, nil, nil, false
			}
		}
		if plan.kind != ResumeCleanupChannelDirect || plan.count != 1 ||
			plan.runtime != 1 || plan.claim == nil {
			return nil, nil, nil, nil, false
		}
		id = resumeCleanupIDAt(plan, 0)
	}
	if id == nil || !id.Valid() || id.Source() != OperationSourceChannel ||
		id.Route() != driver.sources.route {
		return nil, nil, nil, nil, false
	}
	slot, slotOK := channelOperationSlotFor(driver.sources.channel, *id)
	if !slotOK || preemptLoad(&slot.generation) != id.Generation ||
		selectClaimLoad(plan.claim) != selectClaimClaimed || slot.record.id != *id {
		return nil, nil, nil, nil, false
	}
	// ApplyOne deliberately destroys the source-to-frame relation before typed
	// runtime materialization: it clears slot.claim and DetachParkWaitOperation
	// clears the complete ParkLink. Recognize the two sides of that lifetime with
	// separate proofs. Requiring the attached shape after materialization would
	// silently send every direct operation through the generic five-phase tail.
	switch {
	case cursor.phase == publishedEpochResolveDiscover && plan.phase == resumeCleanupBound:
		if slot.claim != plan.claim ||
			slot.record.phase != operationActive || slot.record.resolutionApplied ||
			slot.record.link.wait != wait || slot.record.link.park != &wait.g.park ||
			slot.record.link.ticket != wait.ticket ||
			slot.record.link.operation != &slot.record {
			return nil, nil, nil, nil, false
		}
	case cursor.phase == publishedEpochResolvePromote && plan.phase == resumeCleanupConfirm:
		if preemptLoad(&slot.state) != uint32(producerSourceClosing) ||
			!producerSourceSlotQuiesced(&slot.producerSourceSlot) ||
			preemptLoad(&slot.mailbox) != uint32(channelMailboxEmpty) ||
			preemptLoad(&slot.external) != uint32(channelExternalExposed) ||
			preemptLoad(&slot.externalLease)&1 != 0 || slot.claim != nil ||
			slot.record.phase != operationDetached || !slot.record.resolutionApplied ||
			slot.record.link != (ParkLink{}) {
			return nil, nil, nil, nil, false
		}
	default:
		return nil, nil, nil, nil, false
	}
	return wait, plan, id, slot, true
}

// beginOwnerLocalDirectChannelResumeCleanup consumes the exact ordinary
// one-channel winner after ApplyOne has detached it. ownerLocalDirectChannelPlan
// fully audited the immutable frame binding and source generation immediately
// before resolution, and ApplyOne proved the result/disposition transition.
// Repeating validResumeCleanupPlan -> ConsumeParkSet -> validResumeCleanupPlan
// here would traverse that same graph three more times. This gate checks only
// the scalar boundary which those two owner-only operations may have changed,
// then performs the no-fail write half of ConsumeParkSet and beginResumeCleanup.
// Cancellation and every non-direct shape continue through the generic machine.
func beginOwnerLocalDirectChannelResumeCleanup(
	wait *WaitSetRecord,
	plan *ResumeCleanupPlan,
	id *OperationID,
	slot *channelOperationSlot,
) bool {
	if slot == nil {
		return false
	}
	record := &slot.record
	if wait == nil || wait.g == nil || plan == nil || id == nil ||
		wait.state != waitSetRecordActive || wait.work != waitSetWorkResolving ||
		wait.resume != unsafe.Pointer(plan) || wait.resumeKind != resumeBindingCleanup ||
		plan.packet == nil || plan.claim == nil || plan.context == nil || plan.entries == nil ||
		plan.kind != ResumeCleanupChannelDirect || plan.count != 1 || plan.runtime != 1 ||
		plan.index != 0 || plan.phase != resumeCleanupBound || plan.ticket != wait.ticket ||
		plan.outcome != ParkOutcomePending || plan.caseID != 0 ||
		plan.lease != (OperationResultLease{}) || plan.result != ResumeResultNone ||
		plan.small != ResumeSmallInvalid ||
		id != (*OperationID)(unsafe.Add(plan.entries, plan.idOffset)) ||
		selectClaimLoad(plan.claim) != selectClaimClaimed {
		return false
	}
	packet := plan.packet
	if packet.state != resumePacketBound || packet.ticket != wait.ticket ||
		packet.source != (OperationID{}) || packet.scalar != (ScalarResultPayloadV1{}) ||
		packet.caseID != 0 || packet.outcome != ParkOutcomePending ||
		packet.result != ResumeResultNone || packet.small != ResumeSmallInvalid {
		return false
	}
	state := &wait.g.park
	if !validParkTicket(wait.ticket) || state.ticket != wait.ticket || state.phase != parkReady ||
		state.resolving || state.expected != 1 || state.attached != 0 || state.seed != 0 ||
		state.hasDefault || state.taskCancelKind != TaskCancelNone ||
		state.taskCancelPhase != taskCancelIdle || state.cancelKind != ParkCancelNone ||
		state.outcome != ParkOutcomeCompleted || state.winnerCase != 1 ||
		state.winnerID != *id || state.winnerRecord != record || state.head != nil {
		return false
	}
	if !id.Valid() || id.Source() != OperationSourceChannel || record.id != *id ||
		record.phase != operationDetached || record.disposition != OperationDispositionWinner ||
		!record.resolutionApplied || record.cancelRequested || record.quiesced ||
		operationCandidateMode(record) != OperationCommitReadyThenTryCommit ||
		operationCandidateState(record) != OperationCommitCommitted ||
		!operationCandidateIsPublished(record) || record.resultState != operationResultOwned ||
		record.resultTicket != wait.ticket || record.link != (ParkLink{}) {
		return false
	}

	record.resultState = operationResultLeased
	plan.outcome = ParkOutcomeCompleted
	plan.caseID = 1
	plan.lease = OperationResultLease{id: *id, ticket: wait.ticket}
	plan.phase = resumeCleanupRuntime
	state.winnerRecord = nil
	state.seed = 0
	state.phase = parkConsumed
	return true
}

func beginOwnerLocalDirectChannelCompletion(
	driver *ExecutorDriver,
	cursor *publishedEpochResolveCursor,
	step *publishedEpochResolveStep,
	wait *WaitSetRecord,
	plan *ResumeCleanupPlan,
	id *OperationID,
	slot *channelOperationSlot,
) bool {
	if slot == nil {
		return false
	}
	record := &slot.record
	if cursor.phase != publishedEpochResolveDiscover || cursor.link != &record.link ||
		cursor.claim != nil || cursor.forced != nil || cursor.claimOwned || cursor.hasChannel ||
		wait.work != waitSetWorkResolving || plan.phase != resumeCleanupBound {
		return false
	}
	resolution, resolved := resolveForcedSinglePark(&wait.g.park, wait.ticket, record)
	if !resolved {
		return false
	}
	step.resolution = resolution
	cursor.link = wait.g.park.head
	cursor.phase = publishedEpochResolveApply
	cursor.waitRetry = false
	cursor.waitAwait = false

	switch driver.sources.channel.ApplyOne(driver.p, *id, record) {
	case OperationApplyDetached:
		step.applyVisits = 1
	case OperationApplyRetryBudget:
		// A foreign matcher may have acquired an admission immediately before
		// this owner reduction. Preserve the already-resolved Detaching state
		// and let the common bounded Apply phase perform the retry protocol.
		return true
	default:
		return false
	}
	if wait.g.park.phase != parkReady || wait.g.park.head != nil ||
		wait.g.park.attached != 0 {
		return false
	}
	if _, _, progressOK := finishWaitSetApplyProgress(wait, false, false); !progressOK {
		return false
	}
	state := &wait.g.park
	directCleanup := state.outcome == ParkOutcomeCompleted &&
		state.taskCancelKind == TaskCancelNone && state.taskCancelPhase == taskCancelIdle &&
		state.cancelKind == ParkCancelNone
	if directCleanup {
		if !beginOwnerLocalDirectChannelResumeCleanup(wait, plan, id, slot) {
			return false
		}
		_, small, physicalOK := channelPhysicalCompletion(preemptLoad(&slot.physical))
		if physicalOK && small != ResumeSmallInvalid {
			plan.small = small
			plan.phase = resumeCleanupConfirm
		}
	} else if !beginResumeCleanup(wait, plan) {
		return false
	}
	cursor.link = nil
	cursor.phase = publishedEpochResolvePromote
	// Task cancellation may suppress a physically completed channel case while
	// preserving its result lease for discard. That shape intentionally retains
	// the common cleanup machine; certify only the ordinary selected case whose
	// scalar continuation is invariant until typed materialization.
	cursor.directChannel = directCleanup
	return true
}

func materializeOwnerLocalDirectChannelResult(
	driver *ExecutorDriver,
	wait *WaitSetRecord,
	plan *ResumeCleanupPlan,
	id *OperationID,
	slot *channelOperationSlot,
) bool {
	if driver == nil || plan == nil || wait == nil || wait.g == nil || id == nil || slot == nil ||
		plan.phase != resumeCleanupConfirm || plan.index != 0 ||
		wait.work != waitSetWorkResolving || wait.g.park.phase != parkConsumed {
		return false
	}
	sources, p := &driver.sources, driver.p
	if p == nil || sources.channel == nil || plan.outcome != ParkOutcomeCompleted ||
		plan.caseID != 1 || !plan.lease.Valid() ||
		plan.small == ResumeSmallInvalid || plan.packet.scalar != (ScalarResultPayloadV1{}) {
		return false
	}
	preferred, finished := sources.channel.finishOwnerLocalDirectChannelResult(
		p, slot, *id, plan.claim, plan.lease,
	)
	if !finished {
		return false
	}
	plan.result = ResumeResultChannel
	wait.g.park.seed = uint32(preferred)
	plan.lease = OperationResultLease{}
	*id = OperationID{}
	packet := plan.packet
	ticket, outcome, caseID := plan.ticket, plan.outcome, plan.caseID
	result, scalar, small := plan.result, packet.scalar, plan.small
	materializeConsumedParkStateUnchecked(&wait.g.park, ticket, outcome, caseID)
	*packet = ResumePacket{
		ticket: ticket, scalar: scalar, caseID: caseID, outcome: outcome,
		result: result, small: small, state: resumePacketMaterialized,
	}
	wait.resume = unsafe.Pointer(packet)
	wait.resumeKind = resumeBindingMaterialized
	*plan = ResumeCleanupPlan{}
	if !validMaterializedResumePacket(packet) {
		return false
	}

	g, frame := wait.g, wait.g.active
	schedule := preemptLoad(&p.schedule)
	if !validMaterializedParkState(&g.park, ticket) || !validReadyQueueHeader(p) ||
		p.readyCount == ^uint32(0) || frame == nil || frame.parkWait != wait ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		(schedule != scheduleIdle && schedule != scheduleRequested) {
		return false
	}
	wait.workNext = nil
	promoteReadyWaitSetUnchecked(p, wait)
	return true
}

// finishOwnerLocalDirectChannelCompletion collapses the one-entry common
// cleanup tail after the runtime has removed its typed hchan node. Every
// operation remains the same source-neutral primitive used by select; only
// the per-phase cursor writes and repeated whole-plan validation disappear.
func finishOwnerLocalDirectChannelCompletion(
	driver *ExecutorDriver,
	cursor *publishedEpochResolveCursor,
	step *publishedEpochResolveStep,
	wait *WaitSetRecord,
	plan *ResumeCleanupPlan,
	id *OperationID,
	slot *channelOperationSlot,
) bool {
	if cursor.phase != publishedEpochResolvePromote ||
		!materializeOwnerLocalDirectChannelResult(driver, wait, plan, id, slot) {
		return false
	}
	step.promoted = 1
	return advancePublishedEpochWaitAfterCleared(&driver.sources, cursor, driver.p, step)
}

// resolveOwnerLocalDirectChannelStep returns handled=false for every shape
// which needs the common multi-event/cross-source state machine.
func resolveOwnerLocalDirectChannelStep(
	driver *ExecutorDriver,
	cursor *publishedEpochResolveCursor,
	step *publishedEpochResolveStep,
) (handled, ok bool) {
	wait, plan, id, slot, direct := ownerLocalDirectChannelPlan(driver, cursor)
	if !direct {
		return false, false
	}
	switch {
	case cursor.phase == publishedEpochResolveDiscover && plan.phase == resumeCleanupBound:
		if !beginOwnerLocalDirectChannelCompletion(
			driver, cursor, step, wait, plan, id, slot,
		) {
			return true, false
		}
		// A committed small result needs no typed runtime hook. The complete
		// Discover -> Apply -> Confirm transition above ran in this same owner
		// reduction, so finish it before re-entering the generic selector and
		// re-auditing the unchanged ready/park/affected queue headers. Large or
		// target-typed results remain at Runtime and return through Materialize.
		if cursor.directChannel && cursor.phase == publishedEpochResolvePromote &&
			plan.phase == resumeCleanupConfirm {
			return true, finishOwnerLocalDirectChannelCompletion(
				driver, cursor, step, wait, plan, id, slot,
			)
		}
		return true, true
	case cursor.phase == publishedEpochResolvePromote && plan.phase == resumeCleanupConfirm:
		return true, finishOwnerLocalDirectChannelCompletion(driver, cursor, step, wait, plan, id, slot)
	default:
		return false, false
	}
}
