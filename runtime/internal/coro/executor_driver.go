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

// ExecutorDriver is the target-neutral single-P bridge between a stable
// ExecutorRegistry gate and a scheduler-owned durable source set. It is never
// retained by a platform callback: producers publish into a source and wake the
// executor through its stable POD handle.
//
// Every method is scheduler-owner-only. The driver, P, registry, and table must
// remain at stable addresses from BindExecutor through ConfirmExecutorClose.
// A real target surrounds a successful PrepareExecutorSleep with its retained
// source poll and calls WakeExecutor after a real or spurious wake.
//
// This first driver deliberately owns exactly one P and one statically
// assembled ExecutorSourceSet. Targets with deadline sources must use the
// explicit At APIs and supply monotonic timestamps; the driver never retains a
// clock callback, interface, or function value. It provides the handle-free
// last-G terminal close handoff, while the target-specific join dispatcher and
// multi-P executor migration remain later layers.
type ExecutorDriver struct {
	magic         uint32
	state         executorDriverState
	p             *P
	registry      *ExecutorRegistry
	handle        ExecutorHandle
	route         RouteID
	sources       ExecutorSourceSet
	poll          executorPollTransaction
	local         ownerLocalCompletionCursor
	run           executorRunCursor
	prepareNow    int64
	hasPrepareNow bool
	terminalKind  ActionKind
}

type executorDriverState uint8

const (
	executorDriverUnbound executorDriverState = iota
	executorDriverActive
	executorDriverIdlePreparing
	executorDriverSleeping
	executorDriverClosing
	executorDriverTerminalClosing
)

const executorDriverMagic uint32 = 0x45584431 // "EXD1"

// validExecutorDriverHeader is the O(1) owner gate for a selected runner
// reduction. It checks the immutable binding, reciprocal P/source identities,
// and the local run cursor, but deliberately does not re-audit every source
// capacity or walk the in-progress logical resolution cursor. The concrete
// source or resolution reduction performs those exact checks before mutation.
func validExecutorDriverHeader(driver *ExecutorDriver) bool {
	if driver == nil || driver.magic != executorDriverMagic || driver.state == executorDriverUnbound {
		return false
	}
	terminalKind := driver.terminalKind
	validTerminalState := driver.state == executorDriverTerminalClosing &&
		(terminalKind == ActionDestroy || terminalKind == ActionPanicDestroy)
	if !validTerminalState && terminalKind != ActionInvalid {
		return false
	}
	validPrepareState := driver.state == executorDriverIdlePreparing
	if validPrepareState != driver.hasPrepareNow || !driver.hasPrepareNow && driver.prepareNow != 0 ||
		driver.hasPrepareNow && driver.prepareNow < 0 {
		return false
	}
	return (driver.state == executorDriverTerminalClosing) == validTerminalState &&
		driver.p != nil && driver.registry != nil && driver.handle.Slot != 0 && driver.handle.Generation != 0 &&
		driver.route.Valid() && driver.sources.route == driver.route &&
		driver.p.executor == driver && preemptLoad(&driver.p.executorMode) == executorModeBound &&
		validExecutorSourceSetHeader(&driver.sources, driver.p) &&
		validOwnerLocalCompletionHeader(&driver.local, driver.p) &&
		validExecutorRunCursor(&driver.run, driver.p)
}

func validExecutorDriver(driver *ExecutorDriver) bool {
	return validExecutorDriverHeader(driver) &&
		validExecutorSourceSet(&driver.sources, driver.p) &&
		validExecutorPollTransaction(&driver.poll, &driver.sources) &&
		validOwnerLocalCompletion(&driver.local, driver.p)
}

func validExecutorDriverForP(driver *ExecutorDriver, p *P) bool {
	return validExecutorDriver(driver) && driver.state == executorDriverActive && driver.p == p
}

func validExecutorDriverHeaderForP(driver *ExecutorDriver, p *P) bool {
	return validExecutorDriverHeader(driver) && driver.state == executorDriverActive && driver.p == p
}

func validRunningExecutorOwner(driver *ExecutorDriver) bool {
	if !validExecutorDriverHeader(driver) || driver.state != executorDriverActive {
		return false
	}
	p := driver.p
	g := p.current
	return g != nil && p.inResume && expectedAction(p, g, p.action, ActionResume) &&
		p.runDecision == (RunDecision{}) &&
		g.state == GRunning && g.active != nil && g.active.state == FrameActive &&
		activeResumeOwnedByAction(g) && g.active.header != nil &&
		g.active.header.G == unsafe.Pointer(g) &&
		g.active.header.SuspendReason == uint16(SuspendNone) &&
		g.active.header.Lifecycle == uint16(FrameActive) &&
		g.pending.kind == pendingNone
}

// CurrentExecutorDriver resolves the exact executor which owns g's currently
// executing physical coroutine frame. It is a managed owner-call boundary,
// not an arbitrary-G lookup API: the compiler resume prologue must already
// have consumed the current run decision, and a runnable-transfer generation
// must not be in flight.
//
// The returned driver is scheduler-owner-only and must not be retained across
// suspension or passed to a producer. Foreign callbacks retain only the POD
// handle/route identities returned beside it and resolve those through their
// target route registry. This lookup deliberately uses g.runP and P.executor;
// it performs no TLS lookup, global current-G lookup, P scan, or route scan.
func CurrentExecutorDriver(g *G) (*ExecutorDriver, ExecutorHandle, RouteID, bool) {
	if !ValidG(g) || g.transferState != runnableTransferGIdle || !resumeGateTaken(g) {
		return nil, ExecutorHandle{}, 0, false
	}
	p := g.runP
	driver := p.executor
	if !validRunningExecutorOwner(driver) || driver.p != p || p.current != g ||
		driver.handle.Slot == 0 || driver.handle.Generation == 0 || !driver.route.Valid() ||
		driver.sources.route != driver.route {
		return nil, ExecutorHandle{}, 0, false
	}
	slot, ok := executorSlot(driver.registry, driver.handle)
	if !ok || preemptLoad(&slot.generation) != driver.handle.Generation ||
		preemptLoad(&slot.state) != uint32(executorActive) {
		return nil, ExecutorHandle{}, 0, false
	}
	return driver, driver.handle, driver.route, true
}

// currentExecutorParkDriver resolves the exact executor during the narrow
// compiler park/resume-hook window. The active frame has already published
// SuspendPark/FrameSuspended while the scheduler still owns the same
// ActionResume episode. Typed adapters add only their closed source-owner
// validation after this common proof.
func currentExecutorParkDriver(g *G) (*ExecutorDriver, ExecutorHandle, RouteID, bool) {
	if !ValidG(g) || g.transferState != runnableTransferGIdle || !resumeGateTaken(g) ||
		g.runP == nil || g.active == nil || g.active.handle == nil || g.active.header == nil {
		return nil, ExecutorHandle{}, 0, false
	}
	p := g.runP
	driver := p.executor
	handle := g.active.handle
	header := g.active.header
	if !validExecutorDriverHeaderForP(driver, p) || p.current != g || !p.inResume ||
		!expectedAction(p, g, p.action, ActionResume) || !activeResumeOwnedByAction(g) ||
		g.state != GRunning || g.active.state != FrameActive ||
		g.active.handle != handle || g.active.header != header ||
		header.G != unsafe.Pointer(g) || header.SuspendReason != uint16(SuspendPark) ||
		header.Lifecycle != uint16(FrameSuspended) || !driver.route.Valid() ||
		driver.handle.Slot == 0 || driver.handle.Generation == 0 {
		return nil, ExecutorHandle{}, 0, false
	}
	slot, ok := executorSlot(driver.registry, driver.handle)
	if !ok || preemptLoad(&slot.generation) != driver.handle.Generation ||
		preemptLoad(&slot.state) != uint32(executorActive) {
		return nil, ExecutorHandle{}, 0, false
	}
	return driver, driver.handle, driver.route, true
}

// CurrentExecutorSourceCatalog returns the exact owner P and direct-call source
// catalog during the compiler park/resume-hook window. Target adapters use this
// narrow capability to grow stable source storage before beginning an
// irreversible ParkSet transaction. The returned pointers must not cross the
// subsequent llvm.coro.suspend or any producer ABI.
func CurrentExecutorSourceCatalog(
	driver *ExecutorDriver,
	g *G,
) (*P, ExecutorSourceCatalog, bool) {
	current, _, route, ok := currentExecutorParkDriver(g)
	if !ok || current != driver || driver.sources.owner != g.runP ||
		driver.sources.route != route || !validExecutorSourceSet(&driver.sources, g.runP) {
		return nil, ExecutorSourceCatalog{}, false
	}
	return g.runP, ExecutorSourceCatalog{
		Timers:  driver.sources.timers,
		Poll:    driver.sources.poll,
		Manual:  driver.sources.manual,
		Worker:  driver.sources.worker,
		Channel: driver.sources.channel,
		Control: driver.sources.control,
	}, true
}

// RegisterCurrentExecutorTaskControl exports one generation-stable,
// pointer-free cancellation endpoint for the task which is executing on this
// exact driver. The returned OperationID is the only task identity a foreign
// producer may retain. In particular, neither G, P, ExecutorDriver nor source
// storage crosses the producer boundary.
//
// A future multi-P runtime may forward the ID's Route through an executor
// route registry before moving the task. Keeping registration current-task
// only prevents today's single-P ownership proof from becoming an accidental
// arbitrary-G lookup API.
func RegisterCurrentExecutorTaskControl(driver *ExecutorDriver, task *G) (OperationID, bool) {
	if !validRunningExecutorOwner(driver) || driver.p.current != task || driver.sources.control == nil {
		return OperationID{}, false
	}
	return RegisterTaskControl(driver.sources.control, driver.p, task)
}

// BeginCloseCurrentExecutorTaskControl seals one exact endpoint while its
// task is executing on the owner P. A producer admitted before this call may
// still finish Post; FinishCloseCurrentExecutorTaskControl therefore remains
// a separate strong-join confirmation.
func BeginCloseCurrentExecutorTaskControl(driver *ExecutorDriver, task *G, id OperationID) bool {
	if !validRunningExecutorOwner(driver) || driver.p.current != task || driver.sources.control == nil {
		return false
	}
	slot, ok := taskControlSlotFor(driver.sources.control, id)
	registered, owned := registeredTaskControlDelivery(driver.sources.control, driver.p, slot, id)
	return ok && owned && registered == task && BeginCloseTaskControl(driver.sources.control, driver.p, id)
}

// FinishCloseCurrentExecutorTaskControl consumes the caller's proof that all
// users of the POD endpoint have stopped, then releases its task lease and
// generation. It returns false without retiring the slot while an admitted
// Post or an owner-side cancellation fact is still outstanding.
func FinishCloseCurrentExecutorTaskControl(driver *ExecutorDriver, task *G, id OperationID) bool {
	if !validRunningExecutorOwner(driver) || driver.p.current != task || driver.sources.control == nil {
		return false
	}
	slot, ok := taskControlSlotFor(driver.sources.control, id)
	registered, owned := registeredTaskControlDelivery(driver.sources.control, driver.p, slot, id)
	if !ok || !owned || registered != task || !ConfirmTaskControlQuiesced(driver.sources.control, driver.p, id) {
		return false
	}
	return RetireTaskControl(driver.sources.control, driver.p, id)
}

// UpdateExecutorPollDeadlineExact dispatches a descriptor deadline update by
// the same routed OperationID retained by the reactor. It accepts both frozen
// poll protocols without probing one after the other on failure.
func UpdateExecutorPollDeadlineExact(
	driver *ExecutorDriver,
	id OperationID,
	deadline int64,
) PollOperationUpdateResult {
	if !validRunningExecutorOwner(driver) || deadline < 0 || !id.Valid() ||
		id.Source() != OperationSourcePoll || id.Route() != driver.route {
		return PollOperationUpdateInvalid
	}
	source := driver.sources.pollSource()
	if source == nil || id.LocalSlot() == 0 || id.LocalSlot() > PollOperationConfiguredCapacity(source) {
		return PollOperationUpdateInvalid
	}
	slot, ok := pollOperationSlotAt(source, id.LocalSlot()-1)
	if !ok || slot.generation != id.Generation {
		return PollOperationUpdateStale
	}
	return source.UpdatePollOperationV2Deadline(driver.p, id, deadline)
}

// SnapshotExecutorPollOperation is the only target reactor view of the paged
// poll catalog. It remains valid while the driver is active, preparing, or in
// its retained sleep; the target never reads private source slots.
func SnapshotExecutorPollOperation(
	driver *ExecutorDriver,
	index uint32,
) (PollOperationSnapshot, bool, bool) {
	if !validExecutorDriver(driver) {
		return PollOperationSnapshot{}, false, false
	}
	source := driver.sources.pollSource()
	if source == nil {
		return PollOperationSnapshot{}, false, false
	}
	return source.SnapshotAt(driver.p, index)
}

// PostExecutorPollEvent imports one pointer-free exact-generation event from
// the retained reactor. OperationID identifies both V1 and V2 physical slots;
// the owner selects the already-frozen protocol mode only after validating the
// driver route and shared generation. No fallback from one protocol to the
// other is attempted on an invalid source invariant.
func PostExecutorPollEvent(
	driver *ExecutorDriver,
	id OperationID,
	result PollOperationResult,
) PollOperationPostResult {
	if !validExecutorDriver(driver) || !id.Valid() || id.Source() != OperationSourcePoll ||
		id.Route() != driver.route ||
		(result != PollOperationReady && result != PollOperationClosing) {
		return PollOperationPostInvalid
	}
	source := driver.sources.pollSource()
	if source == nil || source.owner != driver.p || source.route != driver.route ||
		id.LocalSlot() == 0 || id.LocalSlot() > PollOperationConfiguredCapacity(source) {
		return PollOperationPostInvalid
	}
	slot, ok := pollOperationSlotAt(source, id.LocalSlot()-1)
	if !ok || slot.generation != id.Generation {
		return PollOperationPostStale
	}
	return source.PostPollOperationV2(id, result)
}

func activeExecutorHandle(registry *ExecutorRegistry, handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	return ok && preemptLoad(&slot.generation) == handle.Generation &&
		preemptLoad(&slot.state) == uint32(executorActive) && preemptLoad(&slot.inflight) == 0 &&
		preemptLoad(&slot.gate) == 0
}

func idleExecutorScheduler(p *P) bool {
	return p != nil && p.current == nil && !p.inResume && p.inlineAwaitDepth == 0 &&
		p.action.Kind == ActionInvalid && p.action.Handle == nil &&
		p.runDecision == (RunDecision{}) && !p.runDecisionTaken && p.servicePreemptBudget == 0 &&
		validReadyQueueHeader(p) && validOSThreadOwnerHeader(p) &&
		validParkWaitQueueHeader(p) && validAffectedWaitQueueHeader(p)
}

// BindExecutor attaches a newly registered exact-zero executor gate and an
// empty registration table to one P. Publication is pointer-first and atomic
// mode-last so legacy asynchronous RequestSchedule calls fail without reading
// scheduler-owned binding fields. The caller must already have strongly
// quiesced every legacy source that knew this P, including a call paused before
// its executorMode load; executorMode is a capability guard, not a refcounted
// admission barrier for migration from the legacy ABI.
func bindExecutorAtRoute(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, route RouteID, catalog ExecutorSourceCatalog) bool {
	if driver == nil || driver.magic != 0 || driver.state != executorDriverUnbound || driver.p != nil ||
		driver.registry != nil || driver.handle != (ExecutorHandle{}) || driver.route != 0 || driver.sources != (ExecutorSourceSet{}) ||
		driver.poll != (executorPollTransaction{}) ||
		driver.local != (ownerLocalCompletionCursor{}) ||
		driver.run != (executorRunCursor{}) ||
		driver.prepareNow != 0 || driver.hasPrepareNow ||
		driver.terminalKind != ActionInvalid ||
		p == nil || p.executor != nil || preemptLoad(&p.executorMode) != executorModeUnbound ||
		p.osThreadLockOwner != nil || p.foreignReentry != nil ||
		preemptLoad(&p.schedule) != scheduleIdle || !idleExecutorScheduler(p) ||
		p.readyHead != nil || p.readyTail != nil || !emptySchedulerWaitQueues(p) ||
		!route.Valid() || !activeExecutorHandle(registry, handle) || !bindExecutorSourceSetAtRoute(&driver.sources, p, route, catalog) {
		return false
	}
	driver.magic = executorDriverMagic
	driver.state = executorDriverActive
	driver.p = p
	driver.registry = registry
	driver.handle = handle
	driver.route = route
	p.executor = driver
	preemptStore(&p.executorMode, executorModeBound)
	return true
}

func bindExecutor(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, catalog ExecutorSourceCatalog) bool {
	return bindExecutorAtRoute(driver, p, registry, handle, RouteID(1), catalog)
}

// BindExecutorSourceCatalog binds the frozen direct-call source catalog.
func BindExecutorSourceCatalog(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, catalog ExecutorSourceCatalog) bool {
	return bindExecutor(driver, p, registry, handle, catalog)
}

func BindExecutorSourceCatalogAtRoute(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, route RouteID, catalog ExecutorSourceCatalog) bool {
	return bindExecutorAtRoute(driver, p, registry, handle, route, catalog)
}

func (driver *ExecutorDriver) Route() (RouteID, bool) {
	if !validExecutorDriver(driver) {
		return 0, false
	}
	return driver.route, true
}

func publishExecutorSourcesInState(driver *ExecutorDriver, now int64, withDeadline bool, state executorDriverState) (scan executorSourceScan, ok bool) {
	if !validExecutorDriver(driver) || driver.state != state || driver.poll.phase != executorPollIdle ||
		!emptyExecutorRunCursor(driver) || !idleExecutorScheduler(driver.p) {
		return executorSourceScan{}, false
	}
	return driver.sources.publishPass(driver.p, now, withDeadline)
}

func publishExecutorSourcesAt(driver *ExecutorDriver, now int64, withDeadline bool) (scan executorSourceScan, ok bool) {
	return publishExecutorSourcesInState(driver, now, withDeadline, executorDriverActive)
}

func publishExecutorSources(driver *ExecutorDriver) (drained int, ok bool) {
	scan, ok := publishExecutorSourcesAt(driver, 0, false)
	return scan.completed, ok
}

// serviceExecutorPublishedEpochAt performs one bounded publication epoch:
// every configured source is visited once, then the complete owner-claimed
// snapshot is resolved, detached, and promoted. Producers never mutate that
// snapshot; facts not claimed by this pass remain durable for a later epoch.
func serviceExecutorPublishedEpochAt(driver *ExecutorDriver, now int64, withDeadline bool) (scan executorSourceScan, ok bool) {
	scan, ok = publishExecutorSourcesAt(driver, now, withDeadline)
	if !ok {
		return scan, false
	}
	scan.epochs = 1
	scan.promoted, scan.applyVisits, ok = driver.sources.resolvePublishedEpoch(driver.p)
	return scan, ok
}

func pollExecutorSourcesAt(driver *ExecutorDriver, now int64, withDeadline bool) (total executorSourceScan, ok bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		!emptyExecutorRunCursor(driver) || !idleExecutorScheduler(driver.p) {
		return executorSourceScan{}, false
	}
	if driver.poll.phase != executorPollIdle || !driver.sources.acceptsScan(driver.p, now, withDeadline) {
		return executorSourceScan{}, false
	}
	// The compatibility entry keeps advancing bounded catalog and common
	// resolution slices until the current A/ack/B transaction completes, so its
	// old call boundary remains unchanged. The explicit host API returns after
	// its reduction budget; this legacy wrapper supplies the outer iteration.
	budget, budgetOK := executorMinPollBudget(&driver.sources)
	if !budgetOK {
		return executorSourceScan{}, false
	}
	for {
		var progress ExecutorPollProgress
		var polled bool
		total, progress, polled = pollExecutorSliceAt(driver, now, withDeadline, budget)
		if !polled {
			return total, false
		}
		if progress.Complete {
			return total, true
		}
	}
}

func pollExecutor(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	scan, ok := pollExecutorSourcesAt(driver, 0, false)
	return scan.completed, scan.promoted, ok
}

// PollExecutor services the bound durable source set after a running G has
// yielded or while the scheduler otherwise owns P. It is the only place that
// acknowledges the stable executor request.
func PollExecutor(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	if driver == nil || driver.sources.usesMonotonicTime() {
		return 0, 0, false
	}
	return pollExecutor(driver)
}

// PollExecutorAt services the complete deadline-capable source set with one
// explicit monotonic sample.
func PollExecutorAt(driver *ExecutorDriver, now int64) (timers, promoted int, ok bool) {
	if driver == nil || !driver.sources.usesMonotonicTime() {
		return 0, 0, false
	}
	scan, ok := pollExecutorSourcesAt(driver, now, true)
	return scan.timers, scan.promoted, ok
}

// NextExecutorTimerDeadline exposes the scheduler owner's current earliest
// active absolute deadline without draining it. Platform wait adapters use it
// to choose a sleep deadline; scheduler-service preemption is independent of
// this timer query. The query deliberately accepts no clock or callback.
func NextExecutorTimerDeadline(driver *ExecutorDriver) (deadline int64, hasDeadline, ok bool) {
	if !validExecutorDriver(driver) || !driver.sources.usesMonotonicTime() || driver.state != executorDriverActive ||
		driver.poll.phase != executorPollIdle || !idleExecutorScheduler(driver.p) {
		return 0, false, false
	}
	return driver.sources.nextDeadline(driver.p)
}

func leaveExecutorIdle(driver *ExecutorDriver) bool {
	left, valid := driver.registry.LeaveIdle(driver.handle)
	if !valid || !left {
		return false
	}
	driver.prepareNow = 0
	driver.hasPrepareNow = false
	driver.state = executorDriverActive
	return true
}

func leaveExecutorIdleForRun(driver *ExecutorDriver) bool {
	if !leaveExecutorIdle(driver) {
		return false
	}
	// Facts discovered during the idle transaction may require a typed runtime
	// materialization step. Retain only a source-work hint here; the unified
	// reducer owns every later source-neutral and direct-runtime reduction.
	driver.run.sourceMore = true
	return validExecutorDriver(driver)
}

// PrepareExecutorSleep executes ArmIdle, an unconditional source fact scan,
// and exact CommitSleep only when no runnable exists and parked Gs remain.
// Source resolution stays in the unified runner. A true sleep result authorizes
// the target to enter its retained wait. false,true means work or a racing
// request won and the scheduler should continue without blocking.
func PrepareExecutorSleep(driver *ExecutorDriver) (sleep bool, ok bool) {
	if !validExecutorDriver(driver) || driver.sources.usesMonotonicTime() || driver.state != executorDriverActive ||
		!emptyExecutorRunCursor(driver) || !idleExecutorScheduler(driver.p) {
		return false, false
	}
	if runnableForOSThreadOwner(driver.p) || !HasWaiting(driver.p) {
		return false, true
	}
	if !driver.registry.ArmIdle(driver.handle) {
		// Request won the exact zero-gate race. Its fact remains durable and the
		// unified runner will service it after this compatibility boundary.
		if !driver.registry.ObserveRequested(driver.handle) {
			return false, false
		}
		driver.run.sourceMore = true
		return false, validExecutorDriver(driver)
	}

	// Scan facts, not just pending, after publishing IdleArmed. This closes a
	// producer paused between Posted and its advisory pending store.
	drained, scanOK := publishExecutorSources(driver)
	if !scanOK {
		// ArmIdle succeeded from exact zero, so the only legal gates here are
		// IdleArmed with or without Requested and LeaveIdle must disarm either.
		// The source-scan failure is already fatal even if corruption also makes
		// this best-effort cleanup fail.
		_, _ = driver.registry.LeaveIdle(driver.handle)
		return false, false
	}
	hasWork := drained != 0 || runnableForOSThreadOwner(driver.p) || driver.sources.pending(driver.p) ||
		driver.registry.ObserveRequested(driver.handle) || preemptLoad(&driver.p.schedule) != scheduleIdle
	if hasWork {
		if !leaveExecutorIdleForRun(driver) {
			return false, false
		}
		return false, true
	}
	if !driver.registry.CommitSleep(driver.handle) {
		if !leaveExecutorIdleForRun(driver) {
			return false, false
		}
		return false, true
	}
	driver.state = executorDriverSleeping
	return true, true
}

// PrepareExecutorSleepAt performs the first half of timer-aware retained-wait
// admission. It publishes IdleArmed and scans every durable source fact at now
// without resolving the resulting epoch. true,true leaves the driver in an
// explicit idle-preparing state and requires the caller to take a fresh
// monotonic sample and call CommitExecutorSleepAt. false,true means work won
// and the unified runner must continue. A failure never leaves a newly armed
// idle gate behind.
func prepareExecutorSleepAt(
	driver *ExecutorDriver,
	now int64,
	allowEmpty bool,
) (prepared bool, ok bool) {
	if !validExecutorDriver(driver) || !driver.sources.usesMonotonicTime() || driver.state != executorDriverActive ||
		!emptyExecutorRunCursor(driver) || !idleExecutorScheduler(driver.p) || now < 0 {
		return false, false
	}
	if runnableForOSThreadOwner(driver.p) || !allowEmpty && !HasWaiting(driver.p) {
		return false, true
	}
	if !driver.registry.ArmIdle(driver.handle) {
		// Request won the exact zero-gate race. Defer source service to the
		// unified runner so typed materialization stays resumable.
		if !driver.registry.ObserveRequested(driver.handle) {
			return false, false
		}
		driver.run.sourceMore = true
		return false, validExecutorDriver(driver)
	}

	// Scan facts, not just pending, after publishing IdleArmed. Commit performs
	// another complete scan at a caller-supplied fresh timestamp.
	scan, scanOK := publishExecutorSourcesAt(driver, now, true)
	if !scanOK {
		_ = leaveExecutorIdle(driver)
		return false, false
	}
	hasWork := scan.completed != 0 || runnableForOSThreadOwner(driver.p) ||
		driver.sources.pending(driver.p) || driver.registry.ObserveRequested(driver.handle) ||
		preemptLoad(&driver.p.schedule) != scheduleIdle
	if hasWork {
		if !leaveExecutorIdleForRun(driver) {
			return false, false
		}
		return false, true
	}
	driver.prepareNow = now
	driver.hasPrepareNow = true
	driver.state = executorDriverIdlePreparing
	return true, true
}

func PrepareExecutorSleepAt(driver *ExecutorDriver, now int64) (prepared bool, ok bool) {
	return prepareExecutorSleepAt(driver, now, false)
}

// PrepareExecutorStandbyAt is the ordinary fleet-domain counterpart to
// PrepareExecutorSleepAt. An empty P has no command-main completion meaning:
// it may arm the exact executor gate and wait for a routed transfer, source
// completion, or shutdown request. The transaction otherwise uses the same
// complete source scans and final CommitExecutorSleepAt proof; runnable work
// or a racing request still wins without blocking.
func PrepareExecutorStandbyAt(driver *ExecutorDriver, now int64) (prepared bool, ok bool) {
	return prepareExecutorSleepAt(driver, now, true)
}

// CommitExecutorSleepAt finishes timer-aware retained-wait admission after the
// target has sampled its monotonic clock again. It unconditionally rescans the
// complete source set at now before exact CommitSleep. A successful sleep
// returns the earliest still-active absolute deadline; a future deadline is a
// poll bound, not runnable work. Passing an invalid timestamp aborts a pending
// preparation and restores the active driver.
func CommitExecutorSleepAt(driver *ExecutorDriver, now int64) (sleep bool, deadline int64, hasDeadline, ok bool) {
	if !validExecutorDriver(driver) || !driver.sources.usesMonotonicTime() ||
		driver.state != executorDriverIdlePreparing || !emptyExecutorRunCursor(driver) || !idleExecutorScheduler(driver.p) {
		return false, 0, false, false
	}
	if now < driver.prepareNow {
		_ = leaveExecutorIdle(driver)
		return false, 0, false, false
	}

	scan, scanOK := publishExecutorSourcesInState(driver, now, true, executorDriverIdlePreparing)
	if !scanOK {
		_ = leaveExecutorIdle(driver)
		return false, 0, false, false
	}
	hasWork := scan.completed != 0 || runnableForOSThreadOwner(driver.p) ||
		driver.sources.pending(driver.p) || driver.registry.ObserveRequested(driver.handle) ||
		preemptLoad(&driver.p.schedule) != scheduleIdle
	if hasWork {
		if !leaveExecutorIdleForRun(driver) {
			return false, 0, false, false
		}
		return false, 0, false, true
	}
	if scan.hasDeadline && scan.deadline <= now {
		_ = leaveExecutorIdle(driver)
		return false, 0, false, false
	}
	if !driver.registry.CommitSleep(driver.handle) {
		if !leaveExecutorIdleForRun(driver) {
			return false, 0, false, false
		}
		return false, 0, false, true
	}
	driver.prepareNow = 0
	driver.hasPrepareNow = false
	driver.state = executorDriverSleeping
	return true, scan.deadline, scan.hasDeadline, true
}

func wakeExecutorRun(driver *ExecutorDriver, now int64, withDeadline bool) bool {
	if !validExecutorDriver(driver) || driver.sources.usesMonotonicTime() != withDeadline ||
		driver.state != executorDriverSleeping || !emptyExecutorRunCursor(driver) ||
		!idleExecutorScheduler(driver.p) || withDeadline && now < 0 {
		return false
	}
	return leaveExecutorIdleForRun(driver)
}

// WakeExecutor leaves a retained no-deadline wait and defers all source
// service to NextExecutorRunStep. Production runtimes use this boundary because
// typed resume materialization may require a direct runtime reduction between
// source-neutral steps.
func WakeExecutor(driver *ExecutorDriver) bool {
	return wakeExecutorRun(driver, 0, false)
}

// WakeExecutorAt is the monotonic-time counterpart to WakeExecutor. The
// timestamp validates the target wake sample; each later bounded source
// reduction receives its own fresh sample through NextExecutorRunStepAt.
func WakeExecutorAt(driver *ExecutorDriver, now int64) bool {
	return wakeExecutorRun(driver, now, true)
}

func canBeginExecutorClose(driver *ExecutorDriver) bool {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive || !idleExecutorScheduler(driver.p) ||
		driver.poll.phase != executorPollIdle || !emptyExecutorRunCursor(driver) || driver.terminalKind != ActionInvalid ||
		!emptySchedulerWaitQueues(driver.p) ||
		!driver.sources.empty(driver.p) {
		return false
	}
	schedule := preemptLoad(&driver.p.schedule)
	if schedule != scheduleIdle && schedule != scheduleDisabled {
		return false
	}
	return true
}

// BeginExecutorClose seals a quiescent driver before physical backend
// unregister/join. Runnable Gs may remain for command cancellation, but no
// running or parked G and no live registration may still depend on the backend.
// Terminal and command shutdown reject a bound driver, so callers must finish
// this close before entering those state machines.
func BeginExecutorClose(driver *ExecutorDriver) bool {
	if !canBeginExecutorClose(driver) {
		return false
	}
	if !driver.registry.BeginClose(driver.handle) {
		return false
	}
	driver.state = executorDriverClosing
	return true
}

func finalDrainExecutorSources(driver *ExecutorDriver) bool {
	if !validExecutorDriver(driver) {
		return false
	}
	scan, ok := driver.sources.drainForClose(driver.p)
	return ok && scan.completed == 0
}

func retireExecutorBinding(driver *ExecutorDriver, restoreAction *Action) bool {
	if !validExecutorDriver(driver) ||
		!driver.sources.empty(driver.p) ||
		!driver.registry.ConfirmQuiesced(driver.handle) || !driver.registry.Retire(driver.handle) {
		return false
	}
	p := driver.p
	if !unbindExecutorSourceSet(&driver.sources, p) {
		return false
	}
	p.executor = nil
	*driver = ExecutorDriver{}
	if restoreAction != nil {
		p.action = *restoreAction
	}
	preemptStore(&p.executorMode, executorModeUnbound)
	return true
}

// ConfirmExecutorClose records the caller's strong join of the complete target
// shim, including pre-lease entry and the Request-to-doorbell tail. It retires
// the stable generation and unbinds the empty source set and P.
func ConfirmExecutorClose(driver *ExecutorDriver) bool {
	if !validExecutorDriver(driver) || driver.state != executorDriverClosing || !idleExecutorScheduler(driver.p) ||
		driver.terminalKind != ActionInvalid ||
		!emptySchedulerWaitQueues(driver.p) ||
		!finalDrainExecutorSources(driver) {
		return false
	}
	return retireExecutorBinding(driver, nil)
}

func terminalExecutorRootPending(p *P, g *G, kind ActionKind) bool {
	if p == nil || g == nil || p.current != g || p.inResume || p.inlineAwaitDepth != 0 ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken ||
		!ValidG(g) || !gPreemptDepthZero(g) || g.runP != p || g.destroyTarget != nil || !g.destroyRoot ||
		g.active != nil || g.frames != nil ||
		p.readyHead != nil || p.readyTail != nil || !emptySchedulerWaitQueues(p) ||
		!validReadyQueue(p) || !validSchedulerWaitQueues(p) || preemptLoad(&p.schedule) != scheduleIdle {
		return false
	}
	switch kind {
	case ActionDestroy:
		if g.state != GDispatching {
			return false
		}
		if g.panicUnwind {
			return publishedPanicRecord(&g.panicRecord)
		}
		return emptyPanicRecord(&g.panicRecord)
	case ActionPanicDestroy:
		return g.state == GPanicking && g.panicUnwind && publishedPanicRecord(&g.panicRecord)
	default:
		return false
	}
}

func terminalExecutorCloseCandidate(p *P, g *G, kind ActionKind) (*ExecutorDriver, bool) {
	if !terminalExecutorRootPending(p, g, kind) ||
		preemptLoad(&p.executorMode) != executorModeBound {
		return nil, false
	}
	driver := p.executor
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		driver.poll.phase != executorPollIdle || driver.terminalKind != ActionInvalid || !driver.sources.canBeginTerminalClose(p) {
		return nil, false
	}
	return driver, true
}

func settleTerminalExecutorClose(driver *ExecutorDriver, p *P, terminal *G) bool {
	for {
		if !validExecutorDriver(driver) || driver.state != executorDriverActive || driver.p != p ||
			driver.terminalKind != ActionInvalid || terminal == nil || !driver.sources.canBeginTerminalClose(p) {
			return false
		}
		_, ok := driver.sources.publishTerminalPass(p, terminal)
		if !ok {
			return false
		}
		if _, ok = driver.registry.Acknowledge(driver.handle); !ok {
			return false
		}

		// Recheck the complete durable source set after acknowledgement. If a
		// request wins the following exact close race, loop and repeat the same
		// transaction; the destroyed LLVM handle is not part of this path.
		scan, ok := driver.sources.publishTerminalPass(p, terminal)
		if !ok {
			return false
		}
		if scan.completed != 0 || driver.sources.pending(p) || driver.registry.ObserveRequested(driver.handle) {
			continue
		}
		if driver.registry.BeginClose(driver.handle) {
			return true
		}
		if !driver.registry.ObserveRequested(driver.handle) {
			return false
		}
	}
}

// beginTerminalExecutorClose seals a bound executor after the last LLVM frame
// has already been destroyed. Only the logical commit kind is moved into the
// scheduler-owned driver; the freed root pointer and physical handle are both
// discarded before P publishes a handle-free control action, so neither a
// target adapter nor an asynchronous GC scan can retain or reuse them.
func beginTerminalExecutorClose(p *P, g *G, kind ActionKind) (Action, bool) {
	driver, ok := terminalExecutorCloseCandidate(p, g, kind)
	if !ok || !validActionFlags(p.action) ||
		!driver.sources.beginTerminalClose(p) || !settleTerminalExecutorClose(driver, p, g) {
		return Action{}, false
	}
	driver.terminalKind = kind
	driver.state = executorDriverTerminalClosing
	g.root = nil
	closeAction := Action{
		Kind:  ActionTerminalExecutorClose,
		Flags: p.action.Flags,
	}
	p.action = closeAction
	return closeAction, true
}

func terminalExecutorCloseDriver(p *P, g *G, action Action) (*ExecutorDriver, bool) {
	if p == nil || action.Kind != ActionTerminalExecutorClose || !validActionFlags(action) || action.Handle != nil ||
		p.action != action || preemptLoad(&p.executorMode) != executorModeBound {
		return nil, false
	}
	driver := p.executor
	if !validExecutorDriver(driver) || driver.state != executorDriverTerminalClosing ||
		!terminalExecutorRootPending(p, g, driver.terminalKind) {
		return nil, false
	}
	return driver, true
}

// TerminalExecutorCloseDriver returns the opaque driver whose target ingress
// shim must be strongly unregistered and joined for a terminal close action.
// The caller must not confirm a different driver; the core retains only a
// logical commit kind and has already discarded the physical destroy handle.
// The pointer stays in scheduler-owned stable runtime storage; a host callback
// ABI must continue to carry only its target POD identity/doorbell, never this
// Go pointer.
func TerminalExecutorCloseDriver(p *P, g *G, action Action) (*ExecutorDriver, bool) {
	return terminalExecutorCloseDriver(p, g, action)
}

// ConfirmTerminalExecutorClose records the caller's strong target join, scans
// durable sources one final time, retires and unbinds the executor, then commits
// the already-destroyed normal or panic root. It derives P, G, the close marker,
// and the private commit token entirely from stable scheduler state, so an
// asynchronous WASM/embedded backend need not retain a native caller stack.
// The returned action can only be a post-destroy scheduler action; the original
// LLVM handle is never returned.
func ConfirmTerminalExecutorClose(driver *ExecutorDriver) (*G, Action, bool) {
	if !validExecutorDriver(driver) {
		return nil, Action{}, false
	}
	p, g, action := driver.p, driver.p.current, driver.p.action
	want, ok := terminalExecutorCloseDriver(p, g, action)
	if !ok || want != driver {
		return nil, Action{}, false
	}
	// The adapter's strong join covers both a TaskControl Post admitted before
	// its endpoint seal and that call's later ExecutorRegistry Request/doorbell
	// tail. Drain those final Closing mailboxes before proving both admission
	// domains quiescent. Terminal-late facts are normal discards because the
	// final root was already destroyed before ActionTerminalExecutorClose.
	if _, drainOK := driver.sources.publishTerminalPass(p, g); !drainOK ||
		!driver.sources.canFinishTerminalClose(p) || !driver.registry.canConfirmQuiesced(driver.handle) {
		return nil, Action{}, false
	}
	if !driver.sources.finishTerminalClose(p) {
		return nil, Action{}, false
	}
	kind := driver.terminalKind
	if !retireExecutorBinding(driver, nil) {
		return nil, Action{}, false
	}
	for {
		next, committed := commitRootDestroyedCompatibility(p, g, kind)
		if !committed && acknowledgeRootTerminalSchedule(p, g, kind) {
			continue
		}
		if !committed || next.Handle != nil ||
			(next.Kind != ActionComplete && next.Kind != ActionPanicComplete) {
			return nil, Action{}, false
		}
		return g, next, true
	}
}
