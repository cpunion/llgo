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
	waits   *WaitRegistrationTable
	timers  *TimerRegistrationTable
	manual  *ManualOperationSource
	control *TaskControlSource
}

const executorSourceSetMagic uint32 = 0x53524331 // "SRC1"

type executorSourceScan struct {
	completed   int
	waits       int
	timers      int
	manual      int
	manualLost  int
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
	scan.waits += other.waits
	scan.timers += other.timers
	scan.manual += other.manual
	scan.manualLost += other.manualLost
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
		!sources.route.Valid() || sources.waits == nil || sources.waits.owner != p {
		return false
	}
	return (sources.timers == nil || sources.timers.owner == p && sources.timers.route == sources.route) &&
		(sources.manual == nil || sources.manual.owner == p && sources.manual.route == sources.route) &&
		(sources.control == nil || sources.control.owner == p && sources.control.route == sources.route)
}

// ExecutorSourceCatalog is the frozen direct-call source catalog for one
// executor. Waits remains mandatory during the V1 migration; every additional
// source is optional and extends the common transaction without adding another
// scheduler driver or interface dispatch layer.
type ExecutorSourceCatalog struct {
	Waits   *WaitRegistrationTable
	Timers  *TimerRegistrationTable
	Manual  *ManualOperationSource
	Control *TaskControlSource
}

// bindExecutorSourceSet binds every statically configured source as one
// transaction. A later-source failure rolls back earlier empty bindings and
// leaves the source set exact-zero.
func bindExecutorSourceSetAtRoute(sources *ExecutorSourceSet, p *P, route RouteID, catalog ExecutorSourceCatalog) bool {
	if sources == nil || *sources != (ExecutorSourceSet{}) || p == nil || !route.Valid() || catalog.Waits == nil ||
		!bindRegistrationTable(catalog.Waits, p) {
		return false
	}
	if catalog.Timers != nil && !bindTimerRegistrationTableAtRoute(catalog.Timers, p, route) {
		_ = unbindRegistrationTable(catalog.Waits, p)
		return false
	}
	if catalog.Manual != nil && !BindManualOperationSourceAtRoute(catalog.Manual, p, route) {
		if catalog.Timers != nil {
			_ = unbindTimerRegistrationTable(catalog.Timers, p)
		}
		_ = unbindRegistrationTable(catalog.Waits, p)
		return false
	}
	if catalog.Control != nil && !BindTaskControlSourceAtRoute(catalog.Control, p, route) {
		if catalog.Manual != nil {
			_ = UnbindManualOperationSource(catalog.Manual, p)
		}
		if catalog.Timers != nil {
			_ = unbindTimerRegistrationTable(catalog.Timers, p)
		}
		_ = unbindRegistrationTable(catalog.Waits, p)
		return false
	}
	sources.magic = executorSourceSetMagic
	sources.owner = p
	sources.route = route
	sources.waits = catalog.Waits
	sources.timers = catalog.Timers
	sources.manual = catalog.Manual
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
	return sources != nil && sources.timers != nil
}

func (sources *ExecutorSourceSet) acceptsScan(p *P, now int64, withDeadline bool) bool {
	return validExecutorSourceSet(sources, p) &&
		withDeadline == sources.usesMonotonicTime() && (!withDeadline || now >= 0)
}

func (sources *ExecutorSourceSet) waitTable() *WaitRegistrationTable {
	if sources == nil {
		return nil
	}
	return sources.waits
}

func (sources *ExecutorSourceSet) timerTable() *TimerRegistrationTable {
	if sources == nil {
		return nil
	}
	return sources.timers
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
	scan.waits, ok = sources.waits.drainFor(p)
	scan.completed += scan.waits
	if !ok {
		return scan, false
	}
	if sources.timers != nil {
		scan.timers, scan.deadline, scan.hasDeadline, ok = sources.timers.drainDueFor(p, now)
		scan.completed += scan.timers
		if !ok {
			return scan, false
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
	case OperationSourceManual:
		if sources.manual == nil {
			return OperationApplyInvalid
		}
		return sources.manual.ApplyOne(p, link.operation.id, link.operation)
	default:
		// A V2 ParkLink from a source absent from this frozen direct-call catalog
		// is a binding/programming error, not deferred backend work.
		return OperationApplyInvalid
	}
}

// applyResolvedWaitSetBatch dispatches source-specific apply through only the
// candidate links retained by the resolved batch. Detach mutates the intrusive
// list, so next is captured before each direct source call. A deferred source
// must leave its exact link attached; promotion then requeues that wait-set for
// the next bounded epoch without any capacity or all-G scan.
func (sources *ExecutorSourceSet) applyResolvedWaitSetBatch(p *P, batch *WaitSetRecord) (visits int, ok bool) {
	if !validExecutorSourceSet(sources, p) {
		return 0, false
	}
	for wait := batch; wait != nil; wait = wait.workNext {
		if !validActiveWaitSetRecordFast(p, wait) ||
			(wait.work != waitSetWorkResolving && wait.work != waitSetWorkResolvingDirty) ||
			(wait.g.park.phase != parkDetaching && wait.g.park.phase != parkReady) {
			return visits, false
		}
		state := &wait.g.park
		for link := state.head; link != nil; {
			next := link.next
			if link.park != state || link.wait != wait || link.ticket != wait.ticket ||
				link.operation == nil || link.operation.link.operation != link.operation {
				return visits, false
			}
			visits++
			switch sources.applyOne(p, link) {
			case OperationApplyDetached:
				// The source cleared this exact embedded link. next remains stable
				// source-owned storage even when its predecessor changed.
			case OperationApplyDeferred:
				if link.park != state || link.wait != wait || link.operation == nil ||
					&link.operation.link != link || link.operation.phase != operationActive {
					return visits, false
				}
			default:
				return visits, false
			}
			link = next
		}
	}
	return visits, true
}

func (sources *ExecutorSourceSet) resolvePublishedEpoch(p *P) (promoted, applyVisits int, ok bool) {
	if !validExecutorSourceSet(sources, p) {
		return 0, 0, false
	}
	// Phase one resolves every source's affected entries against the same
	// complete sticky snapshot. Timer V2 completion publication marks its
	// WaitSetRecord directly and therefore has no source-local affected chain;
	// importantly, timer publication still completed before this phase. Any V2
	// source which does retain a local chain must resolve it here before the
	// shared wait-set batch and before any source-specific ApplyOne call.
	if sources.manual != nil {
		standalone, valid := sources.manual.standaloneAffected(p)
		if !valid || standalone {
			// A source-local (link.wait == nil) entry has no resolved batch link.
			// Fail before consuming it rather than silently leaving an attached
			// terminal operation outside the production apply transaction.
			return 0, 0, false
		}
		resolution, duplicates, resolved := sources.manual.ResolveAffectedPublishedEpoch(p)
		if !resolved || resolution != (CompletionResolution{}) || duplicates != 0 {
			return 0, 0, false
		}
	}
	batch, _, _, resolved := resolveAffectedWaitSets(p)
	if !resolved {
		return 0, 0, false
	}
	// Phase two walks only the resolved batch's candidate links and directly
	// dispatches each exact source identity. All source resolve passes above are
	// complete before any source applies or detaches, so static source order can
	// neither select a winner nor hide a cross-source loser.
	applyVisits, ok = sources.applyResolvedWaitSetBatch(p, batch)
	if !ok {
		return 0, applyVisits, false
	}
	promoted, ok = promoteResolvedWaitSets(p, batch)
	if !ok {
		return promoted, applyVisits, false
	}
	legacyPromoted, legacyOK := pollReady(p)
	return promoted + legacyPromoted, applyVisits, legacyOK
}

// pending reports producer-published facts that require another owner scan.
// Deadline sources are sampled by drain and represented by the aggregate
// deadline; future deadlines are not pending runnable work.
func (sources *ExecutorSourceSet) pending(p *P) bool {
	return validExecutorSourceSet(sources, p) &&
		(p.affectedWaitHead != nil || sources.waits.Pending() || sources.manual != nil && sources.manual.Pending() ||
			sources.control != nil && sources.control.Pending())
}

func (sources *ExecutorSourceSet) nextDeadline(p *P) (deadline int64, hasDeadline, ok bool) {
	if !validExecutorSourceSet(sources, p) || sources.timers == nil {
		return 0, false, false
	}
	return sources.timers.nextDeadlineFor(p)
}

func (sources *ExecutorSourceSet) empty(p *P) bool {
	return validExecutorSourceSet(sources, p) && registrationTableEmpty(sources.waits, p) &&
		(sources.timers == nil || timerRegistrationTableEmpty(sources.timers, p)) &&
		(sources.manual == nil || manualOperationSourceEmpty(sources.manual, p)) &&
		(sources.control == nil || taskControlSourceEmpty(sources.control, p))
}

// canBeginTerminalClose differs from empty only for TaskControlSource. A task
// endpoint is allowed to outlive the final LLVM frame specifically so its G
// storage remains pinned until the host/export shim is strongly joined. Every
// operation-producing source must already be empty; the terminal-close action
// then owns sealing and retiring the remaining control endpoints.
func (sources *ExecutorSourceSet) canBeginTerminalClose(p *P) bool {
	return validExecutorSourceSet(sources, p) && registrationTableEmpty(sources.waits, p) &&
		(sources.timers == nil || timerRegistrationTableEmpty(sources.timers, p)) &&
		(sources.manual == nil || manualOperationSourceEmpty(sources.manual, p)) &&
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
	return validExecutorSourceSet(sources, p) && registrationTableEmpty(sources.waits, p) &&
		(sources.timers == nil || timerRegistrationTableEmpty(sources.timers, p)) &&
		(sources.manual == nil || manualOperationSourceEmpty(sources.manual, p)) &&
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
	scan.waits, ok = sources.waits.drainFor(p)
	scan.completed = scan.waits
	if !ok {
		return scan, false
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
	if sources.manual != nil && !UnbindManualOperationSource(sources.manual, p) {
		return false
	}
	if sources.timers != nil && !unbindTimerRegistrationTable(sources.timers, p) {
		return false
	}
	if !unbindRegistrationTable(sources.waits, p) {
		return false
	}
	*sources = ExecutorSourceSet{}
	return true
}
