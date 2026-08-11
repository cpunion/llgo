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

// publishedEpochResolvePhase is the owner-P-only continuation for the common
// half of one source publication epoch. Source catalog publication freezes the
// sticky operation snapshot before this state starts. Each call to
// resolvePublishedEpochStep then performs at most one candidate scan, one
// candidate ApplyOne, one wait-set state transition, or one promotion.
type publishedEpochResolvePhase uint8

const (
	publishedEpochResolveIdle publishedEpochResolvePhase = iota
	publishedEpochResolveDiscover
	publishedEpochResolvePark
	publishedEpochResolveApply
	publishedEpochResolveFinish
	publishedEpochResolvePromote
)

// publishedEpochResolveCursor is embedded in ExecutorDriver's poll
// transaction and is never visible to a producer. The affected FIFO itself is
// the stable batch storage: nextWait snapshots one workNext link while the
// current record is allowed to be dirtied or requeued, and link snapshots one
// source-owned ParkLink while ApplyOne may detach its predecessor.
//
// No interface, function value, allocation, or target pointer crosses this
// boundary. source dispatch remains the direct switch in ExecutorSourceSet.
type publishedEpochResolveCursor struct {
	wait       *WaitSetRecord
	nextWait   *WaitSetRecord
	batchTail  *WaitSetRecord
	link       *ParkLink
	claim      *SelectClaim
	forced     *OperationRecord
	park       parkResolutionCursor
	phase      publishedEpochResolvePhase
	waitRetry  bool
	waitAwait  bool
	hasChannel bool
	claimOwned bool
	_          [3]byte
}

type publishedEpochResolveStep struct {
	resolution    CompletionResolution
	applyVisits   int
	promoted      int
	retryBudget   bool
	awaitExternal bool
	complete      bool
}

// validPublishedEpochResolvingWait is the O(1) active-record counterpart used
// only while ParkState.resolving deliberately makes the stable/full validators
// reject the transient state. It validates queue ownership and scalar headers;
// parkResolutionCursor validates the exact candidate and adjacent links.
func validPublishedEpochResolvingWait(p *P, wait *WaitSetRecord) bool {
	if p == nil || wait == nil || wait.state != waitSetRecordActive ||
		(wait.work != waitSetWorkResolving && wait.work != waitSetWorkResolvingDirty) ||
		wait.g == nil || !ValidG(wait.g) || wait.g.state != GWaiting || !wait.g.waiting ||
		wait.g.queued || wait.g.nextReady != nil || wait.g.runP != nil || wait.g.active == nil ||
		wait.g.active.parkWait != wait || wait.ticket != wait.g.park.ticket || !wait.g.park.resolving {
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

func validPublishedEpochResolveCursor(cursor *publishedEpochResolveCursor, p *P) bool {
	if cursor == nil || p == nil {
		return false
	}
	if cursor.phase == publishedEpochResolveIdle {
		return *cursor == (publishedEpochResolveCursor{})
	}
	if cursor.phase < publishedEpochResolveDiscover || cursor.phase > publishedEpochResolvePromote ||
		cursor.wait == nil || cursor.wait.g == nil || cursor.wait.state != waitSetRecordActive ||
		cursor.wait.ticket != cursor.wait.g.park.ticket || cursor.batchTail == nil || cursor.batchTail.workNext != nil {
		return false
	}
	if cursor.nextWait != cursor.wait.workNext {
		return false
	}
	switch cursor.phase {
	case publishedEpochResolveDiscover:
		return cursor.park == (parkResolutionCursor{}) && !cursor.claimOwned &&
			cursor.wait.work == waitSetWorkResolving && validActiveWaitSetRecordFast(p, cursor.wait) &&
			cursor.wait.g.park.phase == parkParked &&
			(cursor.link == nil || validPublishedEpochWaitLink(cursor.wait, cursor.link))
	case publishedEpochResolvePark:
		return cursor.link == nil && !cursor.waitRetry && !cursor.waitAwait &&
			(cursor.claim == nil && !cursor.claimOwned || cursor.claim != nil &&
				(cursor.claimOwned && selectClaimLoad(cursor.claim) == selectClaimAcquiring ||
					!cursor.claimOwned && cursor.forced != nil && selectClaimLoad(cursor.claim) == selectClaimClaimed)) &&
			validPublishedEpochResolvingWait(p, cursor.wait) &&
			validParkResolutionCursor(&cursor.wait.g.park, cursor.wait.ticket, &cursor.park)
	case publishedEpochResolveApply:
		return cursor.park == (parkResolutionCursor{}) && validActiveWaitSetRecordFast(p, cursor.wait) &&
			(cursor.wait.g.park.phase == parkDetaching || cursor.wait.g.park.phase == parkReady)
	case publishedEpochResolveFinish, publishedEpochResolvePromote:
		return cursor.park == (parkResolutionCursor{}) && cursor.link == nil &&
			validActiveWaitSetRecordFast(p, cursor.wait) &&
			(cursor.wait.g.park.phase == parkDetaching || cursor.wait.g.park.phase == parkReady ||
				cursor.phase == publishedEpochResolvePromote &&
					cursor.wait.resumeKind == resumeBindingCleanup &&
					cursor.wait.g.park.phase == parkConsumed)
	default:
		return false
	}
}

func validPublishedEpochWaitLink(wait *WaitSetRecord, link *ParkLink) bool {
	if wait == nil || link == nil || wait.g == nil || link.park != &wait.g.park || link.wait != wait ||
		link.ticket != wait.ticket || link.operation == nil || &link.operation.link != link ||
		link.operation.link.operation != link.operation || link.operation.phase != operationActive {
		return false
	}
	if link.previous == nil {
		if wait.g.park.head != link {
			return false
		}
	} else if link.previous.next != link || link.previous.rank >= link.rank {
		return false
	}
	return link.next == nil || link.next.previous == link && link.rank < link.next.rank
}

// startPublishedEpochWait binds the next record after the caller has validated
// its owner P. It performs only O(1) bookkeeping; the same reduction is charged
// to the logical candidate, finish, or promotion action selected below.
func startPublishedEpochWait(sources *ExecutorSourceSet, cursor *publishedEpochResolveCursor, wait *WaitSetRecord) bool {
	if cursor == nil || wait == nil || wait.work != waitSetWorkQueued || wait.g == nil {
		return false
	}
	phase := wait.g.park.phase
	if phase != parkParked && phase != parkDetaching && phase != parkReady {
		return false
	}
	discoverChannel := sources != nil && sources.channel != nil
	if phase == parkParked && !discoverChannel &&
		!beginParkSnapshotResolution(&wait.g.park, wait.ticket, &cursor.park, false) {
		return false
	}
	cursor.wait = wait
	cursor.nextWait = wait.workNext
	cursor.link = nil
	cursor.waitRetry = false
	cursor.waitAwait = false
	wait.work = waitSetWorkResolving
	switch phase {
	case parkParked:
		if discoverChannel {
			cursor.phase = publishedEpochResolveDiscover
			cursor.link = wait.g.park.head
		} else {
			cursor.phase = publishedEpochResolvePark
		}
	case parkDetaching, parkReady:
		cursor.phase = publishedEpochResolveApply
		cursor.link = wait.g.park.head
	default:
		return false
	}
	return true
}

func completePublishedEpochCursor(cursor *publishedEpochResolveCursor, step *publishedEpochResolveStep) {
	*cursor = publishedEpochResolveCursor{}
	step.complete = true
}

func initializePublishedEpochResolution(sources *ExecutorSourceSet, p *P, cursor *publishedEpochResolveCursor, step *publishedEpochResolveStep) bool {
	if cursor == nil || *cursor != (publishedEpochResolveCursor{}) || p == nil ||
		!validParkWaitQueueHeader(p) || !validAffectedWaitQueueHeader(p) {
		return false
	}
	if sources != nil {
		if !validExecutorSourceSetHeader(sources, p) {
			return false
		}
		if sources.manual != nil {
			standalone, ok := sources.manual.standaloneAffected(p)
			if !ok || standalone {
				return false
			}
		}
	}
	head, tail := p.affectedWaitHead, p.affectedWaitTail
	if head != nil {
		if tail == nil || !validActiveWaitSetRecordFast(p, head) {
			return false
		}
		cursor.batchTail = tail
		if !startPublishedEpochWait(sources, cursor, head) {
			cursor.batchTail = nil
			return false
		}
		p.affectedWaitHead, p.affectedWaitTail = nil, nil
		return true
	}
	completePublishedEpochCursor(cursor, step)
	return true
}

func finishPendingPublishedEpochWait(sources *ExecutorSourceSet, p *P, cursor *publishedEpochResolveCursor, step *publishedEpochResolveStep) bool {
	wait := cursor.wait
	if wait.work == waitSetWorkResolvingDirty {
		wait.work = waitSetWorkIdle
		wait.workNext = nil
		if !appendAffectedWaitSet(p, wait) {
			return false
		}
	} else if wait.work == waitSetWorkResolving {
		wait.work = waitSetWorkIdle
		wait.workNext = nil
	} else {
		return false
	}
	step.resolution.WaitSets = 1
	return advancePublishedEpochWaitAfterCleared(sources, cursor, p, step)
}

func advancePublishedEpochWaitAfterCleared(sources *ExecutorSourceSet, cursor *publishedEpochResolveCursor, p *P, step *publishedEpochResolveStep) bool {
	if cursor == nil || p == nil || step == nil || cursor.wait == nil || cursor.wait.workNext != nil {
		return false
	}
	next := cursor.nextWait
	batchTail := cursor.batchTail
	cursor.wait = nil
	cursor.nextWait = nil
	cursor.link = nil
	cursor.park = parkResolutionCursor{}
	cursor.claim = nil
	cursor.forced = nil
	cursor.hasChannel = false
	cursor.claimOwned = false
	cursor.waitRetry = false
	cursor.waitAwait = false
	if next != nil {
		// batchTail remains the exact endpoint of the detached snapshot.
		cursor.batchTail = batchTail
		return validActiveWaitSetRecordFast(p, next) && startPublishedEpochWait(sources, cursor, next)
	}
	cursor.batchTail = nil
	completePublishedEpochCursor(cursor, step)
	return true
}

// restorePublishedEpochDiscovery restores the exact unprocessed affected FIFO
// before ParkState enters resolving. An observed Acquiring/Committing state or
// a failed owner CAS is a certificate for this reduction to yield the whole
// source epoch; the peer may already have rolled the shared state back to Open
// by the time restoration runs. Claimed with no forced record means its sticky
// mailbox landed behind this source cursor, so A/ack/B (or the next transaction
// after B) must publish that exact fact.
func restorePublishedEpochDiscovery(
	p *P,
	cursor *publishedEpochResolveCursor,
	step *publishedEpochResolveStep,
	retry bool,
	claimCertificate uint32,
) bool {
	if !validPublishedEpochResolveCursor(cursor, p) || cursor.phase != publishedEpochResolveDiscover ||
		cursor.claim == nil || cursor.claimOwned || cursor.wait.work != waitSetWorkResolving ||
		cursor.batchTail == nil || cursor.batchTail.workNext != nil || !validAffectedWaitQueueHeader(p) {
		return false
	}
	if (retry && claimCertificate != selectClaimAcquiring && claimCertificate != selectClaimCommitting &&
		claimCertificate != selectClaimContended) || (!retry && claimCertificate != selectClaimClaimed) {
		return false
	}
	wait, tail := cursor.wait, cursor.batchTail
	wait.work = waitSetWorkQueued
	if p.affectedWaitHead == nil {
		p.affectedWaitHead, p.affectedWaitTail = wait, tail
	} else {
		tail.workNext = p.affectedWaitHead
		p.affectedWaitHead = wait
	}
	*cursor = publishedEpochResolveCursor{}
	step.retryBudget = retry
	step.complete = true
	return validAffectedWaitQueueHeader(p) && validActiveWaitSetRecordFast(p, wait)
}

func resolvePublishedEpochDiscoverStep(sources *ExecutorSourceSet, p *P, cursor *publishedEpochResolveCursor, step *publishedEpochResolveStep) bool {
	wait, state := cursor.wait, &cursor.wait.g.park
	if cursor.claim != nil {
		claimState := selectClaimLoad(cursor.claim)
		if claimState == selectClaimAcquiring || claimState == selectClaimCommitting {
			return restorePublishedEpochDiscovery(p, cursor, step, true, claimState)
		}
	}
	if cursor.link != nil {
		link := cursor.link
		claim, forced, channel, ok := sources.selectCommitDomainFor(link)
		if !ok || channel && state.expected > 1 && claim == nil {
			return false
		}
		if channel {
			if cursor.hasChannel && cursor.claim != claim {
				return false
			}
			cursor.hasChannel = true
			cursor.claim = claim
			if forced {
				if cursor.forced != nil && cursor.forced != link.operation {
					return false
				}
				cursor.forced = link.operation
			}
		}
		cursor.link = link.next
		return true
	}

	if cursor.claim == nil {
		if !beginParkSnapshotResolution(state, wait.ticket, &cursor.park, false) {
			return false
		}
		cursor.phase = publishedEpochResolvePark
		return true
	}

	claimState := selectClaimLoad(cursor.claim)
	if cursor.forced != nil {
		switch claimState {
		case selectClaimAcquiring, selectClaimCommitting:
			return restorePublishedEpochDiscovery(p, cursor, step, true, claimState)
		case selectClaimClaimed:
			if !beginForcedParkSnapshotResolution(state, wait.ticket, &cursor.park, cursor.forced) {
				return false
			}
			cursor.phase = publishedEpochResolvePark
			return true
		default:
			return false
		}
	}

	switch ownerState := selectClaimOwnerAcquire(cursor.claim); ownerState {
	case selectClaimOpen:
		cursor.claimOwned = true
		if !beginParkSnapshotResolution(state, wait.ticket, &cursor.park, false) {
			_ = selectClaimOwnerReleasePending(cursor.claim)
			cursor.claimOwned = false
			return false
		}
		cursor.phase = publishedEpochResolvePark
		return true
	case selectClaimAcquiring, selectClaimCommitting, selectClaimContended:
		return restorePublishedEpochDiscovery(p, cursor, step, true, ownerState)
	case selectClaimClaimed:
		return restorePublishedEpochDiscovery(p, cursor, step, false, ownerState)
	default:
		return false
	}
}

// abortPublishedEpochReadyCommit restores the unprocessed suffix of the
// detached affected snapshot when this scheduler/source catalog has no static
// ReadyThen dispatcher. The exact batch tail makes restoration O(1), including
// producer facts already queued behind the frozen snapshot.
func abortPublishedEpochReadyCommit(p *P, cursor *publishedEpochResolveCursor) bool {
	if !validPublishedEpochResolveCursor(cursor, p) || cursor.phase != publishedEpochResolvePark ||
		cursor.park.phase != parkResolutionCommit || cursor.wait.work != waitSetWorkResolving ||
		cursor.batchTail == nil || cursor.batchTail.workNext != nil ||
		!validAffectedWaitQueueHeader(p) {
		return false
	}
	wait, tail := cursor.wait, cursor.batchTail
	if !abortParkSnapshotCommit(&wait.g.park, wait.ticket, &cursor.park) {
		return false
	}
	if cursor.claimOwned && !selectClaimOwnerReleasePending(cursor.claim) {
		return false
	}
	wait.work = waitSetWorkQueued
	if p.affectedWaitHead == nil {
		p.affectedWaitHead, p.affectedWaitTail = wait, tail
	} else {
		tail.workNext = p.affectedWaitHead
		p.affectedWaitHead = wait
	}
	*cursor = publishedEpochResolveCursor{}
	return validAffectedWaitQueueHeader(p) && validActiveWaitSetRecordFast(p, wait)
}

func retryPublishedEpochReadyCommit(p *P, cursor *publishedEpochResolveCursor, step *publishedEpochResolveStep) bool {
	if !abortPublishedEpochReadyCommit(p, cursor) {
		return false
	}
	step.retryBudget = true
	step.complete = true
	return true
}

func resolvePublishedEpochParkStep(sources *ExecutorSourceSet, p *P, cursor *publishedEpochResolveCursor, step *publishedEpochResolveStep) bool {
	wait := cursor.wait
	state := &wait.g.park
	var attempt ParkCommitAttempt
	if cursor.park.phase == parkResolutionCommit {
		request, ok := parkResolutionCommitRequest(state, wait.ticket, &cursor.park)
		if !ok {
			return false
		}
		if sources == nil {
			abortPublishedEpochReadyCommit(p, cursor)
			return false
		}
		attempt, ok = sources.tryCommitReadyCandidate(request, selectClaimOwner{claim: cursor.claim, held: cursor.claimOwned})
		if !ok {
			abortPublishedEpochReadyCommit(p, cursor)
			return false
		}
	}
	resolution, request, status := resolveParkSnapshotBoundedStep(state, wait.ticket, &cursor.park, attempt)
	switch status {
	case parkResolveRetryBudget:
		return request.Valid() && currentParkCommitRequest(request) &&
			retryPublishedEpochReadyCommit(p, cursor, step)
	case parkResolveProgress:
		return true
	case ParkResolveNeedsCommit:
		return request.Valid() && currentParkCommitRequest(request)
	case ParkResolvePending:
		if cursor.claimOwned && !selectClaimOwnerReleasePending(cursor.claim) {
			return false
		}
		cursor.claim, cursor.forced, cursor.claimOwned, cursor.hasChannel = nil, nil, false, false
		step.resolution = resolution
		return finishPendingPublishedEpochWait(sources, p, cursor, step)
	case ParkResolveResolved:
		if cursor.claimOwned && !selectClaimOwnerReleaseTerminal(cursor.claim) {
			return false
		}
		cursor.claim, cursor.forced, cursor.claimOwned, cursor.hasChannel = nil, nil, false, false
		step.resolution = resolution
		cursor.phase = publishedEpochResolveApply
		cursor.link = state.head
		cursor.waitRetry = wait.work == waitSetWorkResolvingDirty
		cursor.waitAwait = false
		return true
	default:
		return false
	}
}

func resolvePublishedEpochApplyStep(sources *ExecutorSourceSet, p *P, cursor *publishedEpochResolveCursor, step *publishedEpochResolveStep) bool {
	wait := cursor.wait
	state := &wait.g.park
	if sources == nil {
		cursor.link = nil
		cursor.phase = publishedEpochResolveFinish
		return true
	}
	link := cursor.link
	if link == nil {
		cursor.phase = publishedEpochResolveFinish
		return true
	}
	if !validPublishedEpochWaitLink(wait, link) ||
		(state.phase != parkDetaching && state.phase != parkReady) {
		return false
	}
	next := link.next
	step.applyVisits = 1
	switch sources.applyOne(p, link) {
	case OperationApplyDetached:
	case OperationApplyRetryBudget:
		if !validPublishedEpochWaitLink(wait, link) {
			return false
		}
		cursor.waitRetry = true
	case OperationApplyAwaitExternalFact:
		if !validPublishedEpochWaitLink(wait, link) {
			return false
		}
		cursor.waitAwait = true
	default:
		return false
	}
	cursor.link = next
	if next == nil {
		cursor.phase = publishedEpochResolveFinish
	}
	return true
}

func resolvePublishedEpochFinishStep(cursor *publishedEpochResolveCursor, step *publishedEpochResolveStep) bool {
	wait := cursor.wait
	if cursor.link != nil {
		return false
	}
	retry, await, ok := finishWaitSetApplyProgress(wait, cursor.waitRetry, cursor.waitAwait)
	if !ok {
		return false
	}
	step.retryBudget = retry
	step.awaitExternal = await
	cursor.waitRetry = retry
	cursor.waitAwait = await
	cursor.phase = publishedEpochResolvePromote
	return true
}

func resolvePublishedEpochPromoteStep(sources *ExecutorSourceSet, p *P, cursor *publishedEpochResolveCursor, step *publishedEpochResolveStep) bool {
	wait := cursor.wait
	if wait == nil || wait.workNext != cursor.nextWait {
		return false
	}
	if wait.resumeKind == resumeBindingCleanup {
		plan := (*ResumeCleanupPlan)(wait.resume)
		if !validResumeCleanupPlan(wait, plan) {
			return false
		}
		switch plan.phase {
		case resumeCleanupBound:
			switch wait.g.park.phase {
			case parkReady:
				return beginResumeCleanup(wait, plan)
			case parkDetaching:
				// Bound cleanup cannot consume the park until every physical
				// candidate has detached. Keep both ordinary dispositions:
				// AwaitingExternal stays off owner work, while RetryBudget (or
				// a dirty owner publication) falls through to the common
				// requeue below. A later epoch reaches parkReady and starts
				// typed materialization exactly once.
			default:
				return false
			}
		case resumeCleanupRuntime:
			// The unified runner must return ExecutorRunStepMaterialize so the
			// direct runtime switch can remove exactly one typed queue node.
			return false
		default:
			finalized, ok := advanceResumeCleanupCore(sources, p, wait, plan)
			if !ok {
				return false
			}
			if !finalized {
				return true
			}
			if !promoteReadyWaitSet(sources, p, wait) {
				return false
			}
			wait.workNext = nil
			step.promoted = 1
			return advancePublishedEpochWaitAfterCleared(sources, cursor, p, step)
		}
	}
	wait.workNext = nil
	if wait.work == waitSetWorkAwaitingExternal {
		if wait.g.park.phase != parkDetaching {
			return false
		}
	} else {
		dirty := wait.work == waitSetWorkResolvingDirty
		if wait.work != waitSetWorkResolving && !dirty {
			return false
		}
		wait.work = waitSetWorkResolving
		switch wait.g.park.phase {
		case parkReady:
			if !promoteReadyWaitSet(sources, p, wait) {
				return false
			}
			step.promoted = 1
		case parkDetaching:
			wait.work = waitSetWorkIdle
			if !appendAffectedWaitSet(p, wait) {
				return false
			}
		case parkParked:
			if !dirty {
				return false
			}
			wait.work = waitSetWorkIdle
			if !appendAffectedWaitSet(p, wait) {
				return false
			}
		default:
			return false
		}
	}
	return advancePublishedEpochWaitAfterCleared(sources, cursor, p, step)
}

// resolvePublishedEpochStep advances exactly one explicitly charged common
// resolution action. The caller owns step so its scratch storage remains in
// the caller's activation instead of becoming a heap-backed named result when
// the phase helpers take its address. Passing nil sources selects the
// scheduler-only path used by unbound PollReady: terminal links remain
// attached and are requeued for their explicit owner-side detach.
func resolvePublishedEpochStep(
	sources *ExecutorSourceSet,
	p *P,
	cursor *publishedEpochResolveCursor,
	step *publishedEpochResolveStep,
) (ok bool) {
	if step == nil {
		return false
	}
	*step = publishedEpochResolveStep{}
	if cursor == nil || p == nil || !validReadyQueueHeader(p) ||
		!validParkWaitQueueHeader(p) || !validAffectedWaitQueueHeader(p) {
		return false
	}
	if cursor.phase == publishedEpochResolveIdle {
		if !initializePublishedEpochResolution(sources, p, cursor, step) {
			return false
		}
		if step.complete {
			return true
		}
	} else if !validPublishedEpochResolveCursor(cursor, p) {
		return false
	}

	switch cursor.phase {
	case publishedEpochResolveDiscover:
		ok = resolvePublishedEpochDiscoverStep(sources, p, cursor, step)
	case publishedEpochResolvePark:
		ok = resolvePublishedEpochParkStep(sources, p, cursor, step)
	case publishedEpochResolveApply:
		ok = resolvePublishedEpochApplyStep(sources, p, cursor, step)
	case publishedEpochResolveFinish:
		ok = resolvePublishedEpochFinishStep(cursor, step)
	case publishedEpochResolvePromote:
		ok = resolvePublishedEpochPromoteStep(sources, p, cursor, step)
	default:
		ok = false
	}
	return ok
}
