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

// ExecutorSourceSet is the statically assembled set of durable event sources
// owned by one executor/P. It deliberately contains no interface or function
// value: targets with a closed source catalog can compile the protocol as
// direct calls, including targets whose dynamic coroutine dispatch is not yet
// available.
//
// Source-specific submission, cancellation, detach, and quiescence remain in
// each source module. The executor sees only the aggregate scan, pending,
// deadline, empty, bind, and unbind operations below. Adding another source
// therefore extends this file rather than duplicating the executor's
// drain/ack/idle/close transactions.
//
// A source publishes an exact operation outcome; it does not directly choose a
// select winner or enqueue a G. The scheduler-owned park state consumes all
// candidate outcomes, resolves winner/cancellation, and exposes at most one
// runnable G during the promotion step. The current one-token wait path is the
// first park-state implementation, not a SourceSet restriction.
//
// The set is embedded in ExecutorDriver and must remain at a stable address
// from bind through unbind. Its fields are scheduler-owner-only; producers
// retain only their source's scalar handle and the ExecutorHandle doorbell.
type ExecutorSourceSet struct {
	magic   uint32
	owner   *P
	route   RouteID
	timers  *TimerRegistrationTable
	poll    *PollOperationSource
	manual  *ManualOperationSource
	worker  *WorkerOperationSource
	channel *ChannelOperationSource
	control *TaskControlSource
}

const executorSourceSetMagic uint32 = 0x53524331 // "SRC1"

type executorSourceScan struct {
	completed   int
	timers      int
	poll        int
	manual      int
	manualLost  int
	worker      int
	workerLost  int
	channel     int
	channelLost int
	control     int
	controlLate int
	// applyVisits is executor work charged once per exact ParkLink candidate
	// dispatched after logical resolution. It is independent of source capacity
	// and is the unit a bounded scheduler-service budget can consume.
	applyVisits int
	promoted    int
	deadline    int64
	hasDeadline bool
	// epochs is a white-box diagnostic count. A successful active Poll adds
	// exactly two, so uint8 cannot wrap; placing it after hasDeadline consumes
	// existing tail padding on both 32-bit and 64-bit targets.
	epochs uint8
}

func (scan *executorSourceScan) add(other executorSourceScan) {
	scan.epochs += other.epochs
	scan.completed += other.completed
	scan.timers += other.timers
	scan.poll += other.poll
	scan.manual += other.manual
	scan.manualLost += other.manualLost
	scan.worker += other.worker
	scan.workerLost += other.workerLost
	scan.channel += other.channel
	scan.channelLost += other.channelLost
	scan.control += other.control
	scan.controlLate += other.controlLate
	scan.applyVisits += other.applyVisits
	scan.promoted += other.promoted
	// Every successful source-set scan reports the complete current deadline
	// view, so the last scan is authoritative rather than a minimum of stale
	// observations from earlier passes.
	scan.deadline = other.deadline
	scan.hasDeadline = other.hasDeadline
}

func validExecutorSourceSet(sources *ExecutorSourceSet, p *P) bool {
	if sources == nil || sources.magic != executorSourceSetMagic || p == nil || sources.owner != p ||
		!sources.route.Valid() ||
		(sources.channel == nil) != (p.channelSource == nil) ||
		sources.channel != nil && p.channelSource != sources.channel {
		return false
	}
	_, timerScanOK := timerRegistrationScanLimit(sources.timers)
	_, pollScanOK := PollOperationScanLimit(sources.poll)
	_, manualScanOK := ManualOperationScanLimit(sources.manual)
	_, workerScanOK := workerOperationScanLimit(sources.worker)
	_, channelScanOK := channelOperationScanLimit(sources.channel)
	_, controlScanOK := taskControlScanLimit(sources.control)
	return (sources.timers == nil || timerScanOK && sources.timers.owner == p && sources.timers.route == sources.route) &&
		(sources.poll == nil || pollScanOK && sources.poll.owner == p && sources.poll.route == sources.route) &&
		(sources.manual == nil || manualScanOK && sources.manual.owner == p && sources.manual.route == sources.route) &&
		(sources.worker == nil || workerScanOK && sources.worker.owner == p && sources.worker.route == sources.route) &&
		(sources.channel == nil || channelScanOK && sources.channel.owner == p && sources.channel.route == sources.route) &&
		(sources.control == nil || controlScanOK && sources.control.owner == p && sources.control.route == sources.route)
}

// ExecutorSourceCatalog is the frozen direct-call source catalog for one
// executor. Every source is optional; an empty catalog is valid for portable
// yield/await-only targets.
type ExecutorSourceCatalog struct {
	Timers  *TimerRegistrationTable
	Poll    *PollOperationSource
	Manual  *ManualOperationSource
	Worker  *WorkerOperationSource
	Channel *ChannelOperationSource
	Control *TaskControlSource
}

// bindExecutorSourceSet binds every statically configured source as one
// transaction. A later-source failure rolls back earlier empty bindings and
// leaves the source set exact-zero.
func bindExecutorSourceSetAtRoute(sources *ExecutorSourceSet, p *P, route RouteID, catalog ExecutorSourceCatalog) bool {
	if sources == nil || *sources != (ExecutorSourceSet{}) || p == nil || !route.Valid() || p.channelSource != nil {
		return false
	}
	if catalog.Timers != nil && !bindTimerRegistrationTableAtRoute(catalog.Timers, p, route) {
		return false
	}
	if catalog.Poll != nil && !BindPollOperationSourceAtRoute(catalog.Poll, p, route) {
		if catalog.Timers != nil {
			_ = unbindTimerRegistrationTable(catalog.Timers, p)
		}
		return false
	}
	if catalog.Manual != nil && !BindManualOperationSourceAtRoute(catalog.Manual, p, route) {
		if catalog.Poll != nil {
			_ = UnbindPollOperationSource(catalog.Poll, p)
		}
		if catalog.Timers != nil {
			_ = unbindTimerRegistrationTable(catalog.Timers, p)
		}
		return false
	}
	if catalog.Worker != nil && !BindWorkerOperationSourceAtRoute(catalog.Worker, p, route) {
		if catalog.Manual != nil {
			_ = UnbindManualOperationSource(catalog.Manual, p)
		}
		if catalog.Poll != nil {
			_ = UnbindPollOperationSource(catalog.Poll, p)
		}
		if catalog.Timers != nil {
			_ = unbindTimerRegistrationTable(catalog.Timers, p)
		}
		return false
	}
	if catalog.Channel != nil && !BindChannelOperationSourceAtRoute(catalog.Channel, p, route) {
		if catalog.Worker != nil {
			_ = UnbindWorkerOperationSource(catalog.Worker, p)
		}
		if catalog.Manual != nil {
			_ = UnbindManualOperationSource(catalog.Manual, p)
		}
		if catalog.Poll != nil {
			_ = UnbindPollOperationSource(catalog.Poll, p)
		}
		if catalog.Timers != nil {
			_ = unbindTimerRegistrationTable(catalog.Timers, p)
		}
		return false
	}
	if catalog.Control != nil && !BindTaskControlSourceAtRoute(catalog.Control, p, route) {
		if catalog.Channel != nil {
			_ = UnbindChannelOperationSource(catalog.Channel, p)
		}
		if catalog.Worker != nil {
			_ = UnbindWorkerOperationSource(catalog.Worker, p)
		}
		if catalog.Manual != nil {
			_ = UnbindManualOperationSource(catalog.Manual, p)
		}
		if catalog.Poll != nil {
			_ = UnbindPollOperationSource(catalog.Poll, p)
		}
		if catalog.Timers != nil {
			_ = unbindTimerRegistrationTable(catalog.Timers, p)
		}
		return false
	}
	sources.magic = executorSourceSetMagic
	sources.owner = p
	sources.route = route
	sources.timers = catalog.Timers
	sources.poll = catalog.Poll
	sources.manual = catalog.Manual
	sources.worker = catalog.Worker
	sources.channel = catalog.Channel
	sources.control = catalog.Control
	return true
}

// bindExecutorSourceSet is the route-1 compatibility transaction.
func bindExecutorSourceSet(sources *ExecutorSourceSet, p *P, catalog ExecutorSourceCatalog) bool {
	return bindExecutorSourceSetAtRoute(sources, p, RouteID(1), catalog)
}

func (sources *ExecutorSourceSet) Route() (RouteID, bool) {
	if sources == nil || !sources.route.Valid() {
		return 0, false
	}
	return sources.route, true
}

func (sources *ExecutorSourceSet) usesMonotonicTime() bool {
	return sources != nil && (sources.timers != nil || sources.poll != nil)
}

func (sources *ExecutorSourceSet) acceptsScan(p *P, now int64, withDeadline bool) bool {
	return validExecutorSourceSet(sources, p) &&
		withDeadline == sources.usesMonotonicTime() && (!withDeadline || now >= 0)
}

func (sources *ExecutorSourceSet) timerTable() *TimerRegistrationTable {
	if sources == nil {
		return nil
	}
	return sources.timers
}

func (sources *ExecutorSourceSet) pollSource() *PollOperationSource {
	if sources == nil {
		return nil
	}
	return sources.poll
}

// publishPass consumes one complete bounded source-catalog pass without
// resolving a logical wait or promoting a G. Each source claims only the facts
// visible to that bounded pass; facts arriving later remain durable in the
// source mailbox and keep its pending/request state for a later epoch. The
// owner-visible OperationRecord and affected-wait snapshot is stable after the
// pass because producers only mutate source mailboxes. Partial completion
// counts are retained on failure.
func (sources *ExecutorSourceSet) publishPass(p *P, now int64, withDeadline bool) (scan executorSourceScan, ok bool) {
	if !sources.acceptsScan(p, now, withDeadline) {
		return executorSourceScan{}, false
	}
	if sources.timers != nil {
		scan.timers, scan.deadline, scan.hasDeadline, ok = sources.timers.drainDueFor(p, now)
		scan.completed += scan.timers
		if !ok {
			return scan, false
		}
	}
	if sources.poll != nil {
		completed, deadline, hasDeadline, pollOK := sources.poll.drainFor(p, now)
		scan.poll = completed
		scan.completed += completed
		if !pollOK {
			return scan, false
		}
		if hasDeadline && (!scan.hasDeadline || deadline < scan.deadline) {
			scan.deadline, scan.hasDeadline = deadline, true
		}
	}
	if sources.manual != nil {
		published, lost, manualOK := sources.manual.PublishPass(p)
		scan.manual = int(published)
		scan.manualLost = int(lost)
		scan.completed += scan.manual + scan.manualLost
		if !manualOK {
			return scan, false
		}
	}
	if sources.worker != nil {
		published, lost, workerOK := sources.worker.PublishPass(p)
		scan.worker = int(published)
		scan.workerLost = int(lost)
		scan.completed += scan.worker + scan.workerLost
		if !workerOK {
			return scan, false
		}
	}
	if sources.channel != nil {
		if !sources.channel.beginPublishPass(p) {
			return scan, false
		}
		limit, valid := channelOperationScanLimit(sources.channel)
		if !valid {
			return scan, false
		}
		for index := uint32(0); index < limit; index++ {
			published, lost, channelOK := sources.channel.publishSlot(p, index)
			scan.channel += int(published)
			scan.channelLost += int(lost)
			scan.completed += int(published + lost)
			if !channelOK {
				return scan, false
			}
		}
	}
	if sources.control != nil {
		delivered, late, controlOK := sources.control.PublishPass(p)
		scan.control = int(delivered)
		scan.controlLate = int(late)
		scan.completed += scan.control + scan.controlLate
		if !controlOK {
			return scan, false
		}
	}
	return scan, true
}

// resolvePublishedEpoch is the only SourceSet entry that may resolve logical
// park state and publish runnable work. The caller must have completed exactly
// one full bounded source-catalog pass. It resolves the owner-claimed snapshot
// immediately; it does not wait for producer mailboxes or the executor request
// bit to become quiet. Keeping this separate prevents static source order from
// becoming a select tie breaker.
func (sources *ExecutorSourceSet) applyOne(p *P, link *ParkLink) OperationApplyResult {
	if !validExecutorSourceSet(sources, p) || link == nil || link.operation == nil ||
		link.operation.link.operation != link.operation || &link.operation.link != link ||
		link.operation.phase != operationActive || link.operation.id.Source() == OperationSourceInvalid {
		return OperationApplyInvalid
	}
	switch link.operation.id.Source() {
	case OperationSourceTimer:
		if sources.timers == nil {
			return OperationApplyInvalid
		}
		return sources.timers.ApplyTimerV2One(p, link.operation.id, link.operation)
	case OperationSourcePoll:
		if sources.poll == nil {
			return OperationApplyInvalid
		}
		return sources.poll.ApplyPollOperationV2One(p, link.operation.id, link.operation)
	case OperationSourceManual:
		if sources.manual == nil {
			return OperationApplyInvalid
		}
		return sources.manual.ApplyOne(p, link.operation.id, link.operation)
	case OperationSourceWorker:
		if sources.worker == nil {
			return OperationApplyInvalid
		}
		return sources.worker.ApplyOne(p, link.operation.id, link.operation)
	case OperationSourceChannel:
		if sources.channel == nil {
			return OperationApplyInvalid
		}
		return sources.channel.ApplyOne(p, link.operation.id, link.operation)
	default:
		// A V2 ParkLink from a source absent from this frozen direct-call catalog
		// is a binding/programming error, not deferred backend work.
		return OperationApplyInvalid
	}
}

// tryCommitReadyCandidate is the one static dispatch boundary between seeded
// logical selection and a source's atomic ReadyThenTryCommit operation. No
// interface or function value enters ParkState. Timer, Manual and Worker
// candidates are contractually IrreversibleCompletion and therefore can never
// reach this method. Poll and Channel are the two closed-catalog ReadyThen
// sources: both bind an exact result only after the seeded resolver selects
// their current readiness generation.
func (sources *ExecutorSourceSet) tryCommitReadyCandidate(request ParkCommitRequest, owner selectClaimOwner) (ParkCommitAttempt, bool) {
	id, ok := request.ID()
	if !ok || sources == nil || !currentParkCommitRequest(request) {
		return ParkCommitAttempt{}, false
	}
	switch id.Source() {
	case OperationSourcePoll:
		if sources.poll == nil {
			return ParkCommitAttempt{}, false
		}
		return sources.poll.TryCommitPollOperationV2(request)
	case OperationSourceChannel:
		if sources.channel == nil {
			return ParkCommitAttempt{}, false
		}
		return sources.channel.TryCommit(request, owner)
	case OperationSourceTimer, OperationSourceManual, OperationSourceWorker:
		return ParkCommitAttempt{}, false
	default:
		return ParkCommitAttempt{}, false
	}
}

func (sources *ExecutorSourceSet) selectCommitDomainFor(link *ParkLink) (claim *SelectClaim, forced, channel, ok bool) {
	if link == nil || link.operation == nil || &link.operation.link != link {
		return nil, false, false, false
	}
	if link.operation.id.Source() != OperationSourceChannel {
		return nil, false, false, true
	}
	if sources == nil || sources.channel == nil {
		return nil, false, true, false
	}
	claim, ok = sources.channel.ClaimFor(link.operation)
	return claim, operationCandidateExternallyCommitted(link.operation), true, ok
}

func (sources *ExecutorSourceSet) resolveCommitCapablePark(state *ParkState, ticket ParkTicket) (CompletionResolution, bool) {
	previousSeed := uint32(0)
	if state != nil {
		previousSeed = state.seed
	}
	var attempt ParkCommitAttempt
	for {
		resolution, request, status := ResolveParkSnapshotStep(state, ticket, attempt)
		switch status {
		case ParkResolvePending, ParkResolveResolved:
			return resolution, true
		case ParkResolveNeedsCommit:
			var ok bool
			attempt, ok = sources.tryCommitReadyCandidate(request, selectClaimOwner{})
			if !ok {
				abortParkCommitCompatibility(state, ticket, request, previousSeed)
				return CompletionResolution{}, false
			}
		default:
			return CompletionResolution{}, false
		}
	}
}

// applyResolvedWaitSetBatch dispatches source-specific apply through only the
// candidate links retained by the resolved batch. Detach mutates the intrusive
// list, so next is captured before each direct source call. A budget retry
// leaves the exact link attached and requeues the wait-set; an external-fact
// wait stays off owner work until its source marks the record affected again.
func finishWaitSetApplyProgress(wait *WaitSetRecord, retryBudget, awaitExternal bool) (retry, await, ok bool) {
	if wait == nil {
		return false, false, false
	}
	switch wait.work {
	case waitSetWorkResolvingDirty:
		// Re-observe after every ApplyOne. A source may publish another owner-side
		// sticky fact while applying an earlier candidate; that dirty fact must
		// beat AwaitExternal and keep the record runnable for epoch B.
		return true, false, true
	case waitSetWorkResolving:
		if retryBudget {
			return true, false, true
		}
		if awaitExternal {
			wait.work = waitSetWorkAwaitingExternal
			return false, true, true
		}
		return false, false, true
	default:
		return false, false, false
	}
}

func (sources *ExecutorSourceSet) applyResolvedWaitSetBatchProgress(p *P, batch *WaitSetRecord) (visits int, retryBudget, awaitExternal, ok bool) {
	if !validExecutorSourceSet(sources, p) {
		return 0, false, false, false
	}
	for wait := batch; wait != nil; wait = wait.workNext {
		if !validActiveWaitSetRecordFast(p, wait) ||
			(wait.work != waitSetWorkResolving && wait.work != waitSetWorkResolvingDirty) ||
			(wait.g.park.phase != parkDetaching && wait.g.park.phase != parkReady) {
			return visits, retryBudget, awaitExternal, false
		}
		state := &wait.g.park
		waitRetry, waitAwait := wait.work == waitSetWorkResolvingDirty, false
		for link := state.head; link != nil; {
			next := link.next
			if link.park != state || link.wait != wait || link.ticket != wait.ticket ||
				link.operation == nil || link.operation.link.operation != link.operation {
				return visits, retryBudget, awaitExternal, false
			}
			visits++
			switch sources.applyOne(p, link) {
			case OperationApplyDetached:
				// The source cleared this exact embedded link. next remains stable
				// source-owned storage even when its predecessor changed.
			case OperationApplyRetryBudget:
				if link.park != state || link.wait != wait || link.operation == nil ||
					&link.operation.link != link || link.operation.phase != operationActive {
					return visits, retryBudget, awaitExternal, false
				}
				waitRetry = true
			case OperationApplyAwaitExternalFact:
				if link.park != state || link.wait != wait || link.operation == nil ||
					&link.operation.link != link || link.operation.phase != operationActive {
					return visits, retryBudget, awaitExternal, false
				}
				waitAwait = true
			default:
				return visits, retryBudget, awaitExternal, false
			}
			link = next
		}
		waitRetry, waitAwait, settled := finishWaitSetApplyProgress(wait, waitRetry, waitAwait)
		if !settled {
			return visits, retryBudget, awaitExternal, false
		}
		retryBudget = retryBudget || waitRetry
		awaitExternal = awaitExternal || waitAwait
	}
	return visits, retryBudget, awaitExternal, true
}

func (sources *ExecutorSourceSet) applyResolvedWaitSetBatch(p *P, batch *WaitSetRecord) (visits int, ok bool) {
	visits, _, _, ok = sources.applyResolvedWaitSetBatchProgress(p, batch)
	return visits, ok
}

func (sources *ExecutorSourceSet) resolvePublishedEpochProgress(p *P) (promoted, applyVisits int, retryBudget, awaitExternal, ok bool) {
	if !validExecutorSourceSet(sources, p) {
		return 0, 0, false, false, false
	}
	var cursor publishedEpochResolveCursor
	for {
		step, advanced := resolvePublishedEpochStep(sources, p, &cursor)
		if !advanced {
			return promoted, applyVisits, retryBudget, awaitExternal, false
		}
		promoted += step.promoted
		applyVisits += step.applyVisits
		retryBudget = retryBudget || step.retryBudget
		awaitExternal = awaitExternal || step.awaitExternal
		if step.complete {
			return promoted, applyVisits, retryBudget, awaitExternal, true
		}
	}
}

func (sources *ExecutorSourceSet) resolvePublishedEpoch(p *P) (promoted, applyVisits int, ok bool) {
	promoted, applyVisits, _, _, ok = sources.resolvePublishedEpochProgress(p)
	return promoted, applyVisits, ok
}

// pending reports producer-published facts that require another owner scan.
// Deadline sources are sampled by drain and represented by the aggregate
// deadline; future deadlines are not pending runnable work.
func (sources *ExecutorSourceSet) pending(p *P) bool {
	return validExecutorSourceSet(sources, p) &&
		(p.affectedWaitHead != nil || sources.poll != nil && sources.poll.Pending() ||
			sources.manual != nil && sources.manual.Pending() ||
			sources.worker != nil && sources.worker.Pending() ||
			sources.channel != nil && sources.channel.Pending() ||
			sources.control != nil && sources.control.Pending())
}

func (sources *ExecutorSourceSet) nextDeadline(p *P) (deadline int64, hasDeadline, ok bool) {
	if !validExecutorSourceSet(sources, p) || !sources.usesMonotonicTime() {
		return 0, false, false
	}
	if sources.timers != nil {
		timerDeadline, timerHas, timerOK := sources.timers.nextDeadlineFor(p)
		if !timerOK {
			return 0, false, false
		}
		deadline, hasDeadline = timerDeadline, timerHas
	}
	if sources.poll != nil {
		pollDeadline, pollHas, pollOK := sources.poll.nextDeadlineFor(p)
		if !pollOK {
			return 0, false, false
		}
		if pollHas && (!hasDeadline || pollDeadline < deadline) {
			deadline, hasDeadline = pollDeadline, true
		}
	}
	return deadline, hasDeadline, true
}

func (sources *ExecutorSourceSet) empty(p *P) bool {
	return validExecutorSourceSet(sources, p) &&
		(sources.timers == nil || timerRegistrationTableEmpty(sources.timers, p)) &&
		(sources.poll == nil || pollOperationSourceEmpty(sources.poll, p)) &&
		(sources.manual == nil || manualOperationSourceEmpty(sources.manual, p)) &&
		(sources.worker == nil || workerOperationSourceEmpty(sources.worker, p)) &&
		(sources.channel == nil || channelOperationSourceEmpty(sources.channel, p)) &&
		(sources.control == nil || taskControlSourceEmpty(sources.control, p))
}

// canBeginTerminalClose differs from empty only for TaskControlSource. A task
// endpoint is allowed to outlive the final LLVM frame specifically so its G
// storage remains pinned until the host/export shim is strongly joined. Every
// operation-producing source must already be empty; the terminal-close action
// then owns sealing and retiring the remaining control endpoints.
func (sources *ExecutorSourceSet) canBeginTerminalClose(p *P) bool {
	return validExecutorSourceSet(sources, p) &&
		(sources.timers == nil || timerRegistrationTableEmpty(sources.timers, p)) &&
		(sources.poll == nil || pollOperationSourceEmpty(sources.poll, p)) &&
		(sources.manual == nil || manualOperationSourceEmpty(sources.manual, p)) &&
		(sources.worker == nil || workerOperationSourceEmpty(sources.worker, p)) &&
		(sources.channel == nil || channelOperationSourceEmpty(sources.channel, p)) &&
		(sources.control == nil || taskControlSourceCanBeginTerminalClose(sources.control, p))
}

func (sources *ExecutorSourceSet) beginTerminalClose(p *P) bool {
	if !sources.canBeginTerminalClose(p) {
		return false
	}
	return sources.control == nil || beginTaskControlSourceTerminalClose(sources.control, p)
}

// publishTerminalPass drains only the control source after the final root has
// been destroyed. The terminal G has no continuation, so its accepted facts
// are counted as normal late discards; facts for any unexpected live task are
// still delivered or preserved by TaskControlSource rather than erased.
func (sources *ExecutorSourceSet) publishTerminalPass(p *P, terminal *G) (scan executorSourceScan, ok bool) {
	if terminal == nil || !sources.canBeginTerminalClose(p) {
		return executorSourceScan{}, false
	}
	if sources.control == nil {
		return scan, true
	}
	delivered, late, controlOK := sources.control.publishTerminalPass(p, terminal)
	scan.control = int(delivered)
	scan.controlLate = int(late)
	scan.completed = scan.control + scan.controlLate
	return scan, controlOK
}

func (sources *ExecutorSourceSet) canFinishTerminalClose(p *P) bool {
	return validExecutorSourceSet(sources, p) &&
		(sources.timers == nil || timerRegistrationTableEmpty(sources.timers, p)) &&
		(sources.poll == nil || pollOperationSourceEmpty(sources.poll, p)) &&
		(sources.manual == nil || manualOperationSourceEmpty(sources.manual, p)) &&
		(sources.worker == nil || workerOperationSourceEmpty(sources.worker, p)) &&
		(sources.channel == nil || channelOperationSourceEmpty(sources.channel, p)) &&
		(sources.control == nil || taskControlSourceCanFinishTerminalClose(sources.control, p))
}

func (sources *ExecutorSourceSet) finishTerminalClose(p *P) bool {
	if !sources.canFinishTerminalClose(p) {
		return false
	}
	if sources.control != nil && !finishTaskControlSourceTerminalClose(sources.control, p) {
		return false
	}
	return sources.empty(p)
}

// drainForClose consumes sources that can publish without a clock sample and
// verifies that the complete set is empty. A deadline source must already be
// empty before close; guessing a timestamp during shutdown would change timer
// semantics.
func (sources *ExecutorSourceSet) drainForClose(p *P) (scan executorSourceScan, ok bool) {
	if !validExecutorSourceSet(sources, p) {
		return executorSourceScan{}, false
	}
	if sources.manual != nil {
		published, lost, manualOK := sources.manual.PublishPass(p)
		scan.manual = int(published)
		scan.manualLost = int(lost)
		scan.completed += scan.manual + scan.manualLost
		if !manualOK {
			return scan, false
		}
	}
	if sources.worker != nil {
		published, lost, workerOK := sources.worker.PublishPass(p)
		scan.worker = int(published)
		scan.workerLost = int(lost)
		scan.completed += scan.worker + scan.workerLost
		if !workerOK {
			return scan, false
		}
	}
	if sources.channel != nil {
		if !sources.channel.beginPublishPass(p) {
			return scan, false
		}
		for index := uint32(0); index < ChannelOperationConfiguredCapacity(sources.channel); index++ {
			published, lost, channelOK := sources.channel.publishSlot(p, index)
			scan.channel += int(published)
			scan.channelLost += int(lost)
			scan.completed += int(published + lost)
			if !channelOK {
				return scan, false
			}
		}
	}
	if sources.control != nil {
		delivered, late, controlOK := sources.control.PublishPass(p)
		scan.control = int(delivered)
		scan.controlLate = int(late)
		scan.completed += scan.control + scan.controlLate
		if !controlOK {
			return scan, false
		}
	}
	if !sources.empty(p) {
		return scan, false
	}
	return scan, true
}

func unbindExecutorSourceSet(sources *ExecutorSourceSet, p *P) bool {
	if !validExecutorSourceSet(sources, p) || !sources.empty(p) {
		return false
	}
	if sources.control != nil && !UnbindTaskControlSource(sources.control, p) {
		return false
	}
	if sources.channel != nil && !UnbindChannelOperationSource(sources.channel, p) {
		return false
	}
	if sources.worker != nil && !UnbindWorkerOperationSource(sources.worker, p) {
		return false
	}
	if sources.manual != nil && !UnbindManualOperationSource(sources.manual, p) {
		return false
	}
	if sources.poll != nil && !UnbindPollOperationSource(sources.poll, p) {
		return false
	}
	if sources.timers != nil && !unbindTimerRegistrationTable(sources.timers, p) {
		return false
	}
	*sources = ExecutorSourceSet{}
	return true
}
