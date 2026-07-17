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
// ExecutorRegistry gate and a scheduler-owned durable source set. It is
// never retained by a platform callback: the platform ABI remains the two POD
// handles carried by PostWaitAndRequest.
//
// Every method is scheduler-owner-only. The driver, P, registry, and table must
// remain at stable addresses from BindExecutor through ConfirmExecutorClose.
// A real target surrounds a successful PrepareExecutorSleep with its retained
// wait and calls WakeExecutor after a real or spurious wake.
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
	sources       ExecutorSourceSet
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

func validExecutorDriver(driver *ExecutorDriver) bool {
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
		driver.p.executor == driver && preemptLoad(&driver.p.executorMode) == executorModeBound &&
		validExecutorSourceSet(&driver.sources, driver.p)
}

func validExecutorDriverForP(driver *ExecutorDriver, p *P) bool {
	return validExecutorDriver(driver) && driver.state == executorDriverActive && driver.p == p
}

func validRunningExecutorOwner(driver *ExecutorDriver) bool {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive {
		return false
	}
	p := driver.p
	g := p.current
	return g != nil && p.inResume && expectedAction(p, g, p.action, ActionResume) &&
		p.runDecision == (RunDecision{}) &&
		g.state == GRunning && g.active != nil && g.active.state == FrameActive &&
		g.active.handle == p.action.Handle && g.active.header != nil &&
		g.active.header.G == unsafe.Pointer(g) &&
		g.active.header.SuspendReason == uint16(SuspendNone) &&
		g.active.header.Lifecycle == uint16(FrameActive) &&
		g.pending.kind == pendingNone && g.waitToken == nil && g.waitTicket == 0
}

// PrepareExecutorWaitRegistration is the only production owner entry for
// arming a platform wait. It is accepted solely from the currently resumed
// frame on this exact executor; producer threads must use the POD post ABI.
func PrepareExecutorWaitRegistration(driver *ExecutorDriver, token *WaitToken) (WaitTicket, WaitRegistrationHandle, WaitRegistrationPrepareResult) {
	if !validRunningExecutorOwner(driver) {
		return 0, WaitRegistrationHandle{}, WaitRegistrationPrepareInvalid
	}
	return PrepareWaitRegistration(driver.p, driver.sources.waitTable(), token)
}

// RollbackExecutorWaitRegistration is owner-only and valid before coroPark
// when external submission never made the POD handle callback-reachable.
func RollbackExecutorWaitRegistration(driver *ExecutorDriver, token *WaitToken, ticket WaitTicket, wait WaitRegistrationHandle) bool {
	return validRunningExecutorOwner(driver) && driver.sources.waitTable().RollbackPreparedWait(wait, token, ticket)
}

// RetireCompletedExecutorWait is owner-only and valid after the matching park
// resumed and the external source was strongly joined or unregistered.
func RetireCompletedExecutorWait(driver *ExecutorDriver, token *WaitToken, ticket WaitTicket, wait WaitRegistrationHandle) bool {
	return validRunningExecutorOwner(driver) && driver.sources.waitTable().RetireCompletedWait(wait, token, ticket)
}

// PrepareExecutorTimerRegistration is the only production owner entry for an
// absolute monotonic one-shot timer. It is valid only for a timer-bound driver
// while its exact frame is running.
func PrepareExecutorTimerRegistration(driver *ExecutorDriver, token *WaitToken, deadline int64) (WaitTicket, TimerRegistrationHandle, TimerRegistrationPrepareResult) {
	if !validRunningExecutorOwner(driver) {
		return 0, TimerRegistrationHandle{}, TimerRegistrationPrepareInvalid
	}
	timers := driver.sources.timerTable()
	if timers == nil {
		return 0, TimerRegistrationHandle{}, TimerRegistrationPrepareInvalid
	}
	return PrepareTimerRegistration(driver.p, timers, token, deadline)
}

// RollbackExecutorTimerRegistration releases a timer that was prepared by the
// running owner but was never made visible to coroPark.
func RollbackExecutorTimerRegistration(driver *ExecutorDriver, token *WaitToken, ticket WaitTicket, timer TimerRegistrationHandle) bool {
	if !validRunningExecutorOwner(driver) {
		return false
	}
	timers := driver.sources.timerTable()
	return timers != nil && timers.RollbackPreparedTimer(timer, token, ticket)
}

// CancelExecutorTimerRegistration publishes cancellation from the exact
// running owner. The matching terminal outcome must still be consumed before
// the timer can be retired.
func CancelExecutorTimerRegistration(driver *ExecutorDriver, timer TimerRegistrationHandle) WaitCancelResult {
	if !validRunningExecutorOwner(driver) {
		return WaitCancelInvalid
	}
	timers := driver.sources.timerTable()
	if timers == nil {
		return WaitCancelInvalid
	}
	return timers.Cancel(timer)
}

// RetireCompletedExecutorTimer validates and retires a consumed timer
// completion from its resumed synchronous continuation.
func RetireCompletedExecutorTimer(driver *ExecutorDriver, token *WaitToken, ticket WaitTicket, timer TimerRegistrationHandle) bool {
	if !validRunningExecutorOwner(driver) {
		return false
	}
	timers := driver.sources.timerTable()
	return timers != nil && timers.RetireCompletedTimer(timer, token, ticket)
}

// RetireCanceledExecutorTimer validates and retires a consumed timer
// cancellation from its resumed synchronous continuation.
func RetireCanceledExecutorTimer(driver *ExecutorDriver, token *WaitToken, ticket WaitTicket, timer TimerRegistrationHandle) bool {
	if !validRunningExecutorOwner(driver) {
		return false
	}
	timers := driver.sources.timerTable()
	return timers != nil && timers.RetireCanceledTimer(timer, token, ticket)
}

func activeExecutorHandle(registry *ExecutorRegistry, handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	return ok && preemptLoad(&slot.generation) == handle.Generation &&
		preemptLoad(&slot.state) == uint32(executorActive) && preemptLoad(&slot.inflight) == 0 &&
		preemptLoad(&slot.gate) == 0
}

func idleExecutorScheduler(p *P) bool {
	return p != nil && p.current == nil && !p.inResume && p.action.Kind == ActionInvalid && p.action.Handle == nil &&
		p.runDecision == (RunDecision{}) && !p.runDecisionTaken && p.servicePreemptBudget == 0 &&
		validReadyQueueHeader(p) && validWaitQueueHeader(p) && validParkWaitQueueHeader(p) && validAffectedWaitQueueHeader(p)
}

// BindExecutor attaches a newly registered exact-zero executor gate and an
// empty registration table to one P. Publication is pointer-first and atomic
// mode-last so legacy asynchronous RequestSchedule calls fail without reading
// scheduler-owned binding fields. The caller must already have strongly
// quiesced every legacy source that knew this P, including a call paused before
// its executorMode load; executorMode is a capability guard, not a refcounted
// admission barrier for migration from the legacy ABI.
func bindExecutor(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, catalog ExecutorSourceCatalog) bool {
	if driver == nil || driver.magic != 0 || driver.state != executorDriverUnbound || driver.p != nil ||
		driver.registry != nil || driver.handle != (ExecutorHandle{}) || driver.sources != (ExecutorSourceSet{}) ||
		driver.prepareNow != 0 || driver.hasPrepareNow ||
		driver.terminalKind != ActionInvalid ||
		p == nil || p.executor != nil || preemptLoad(&p.executorMode) != executorModeUnbound ||
		preemptLoad(&p.schedule) != scheduleIdle || !idleExecutorScheduler(p) ||
		p.readyHead != nil || p.readyTail != nil || !emptySchedulerWaitQueues(p) ||
		!activeExecutorHandle(registry, handle) || !bindExecutorSourceSet(&driver.sources, p, catalog) {
		return false
	}
	driver.magic = executorDriverMagic
	driver.state = executorDriverActive
	driver.p = p
	driver.registry = registry
	driver.handle = handle
	p.executor = driver
	preemptStore(&p.executorMode, executorModeBound)
	return true
}

func BindExecutor(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, waits *WaitRegistrationTable) bool {
	return bindExecutor(driver, p, registry, handle, ExecutorSourceCatalog{Waits: waits})
}

// BindExecutorWithTimers preserves the timer-aware V1 binding ABI while
// assembling one durable source set. A deadline-capable set accepts only the
// explicit At poll/sleep/wake APIs, so omitting a monotonic timestamp fails
// closed instead of silently delaying expiry.
func BindExecutorWithTimers(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, waits *WaitRegistrationTable, timers *TimerRegistrationTable) bool {
	return timers != nil && bindExecutor(driver, p, registry, handle, ExecutorSourceCatalog{Waits: waits, Timers: timers})
}

// BindExecutorSourceCatalog binds a frozen direct-call source catalog. It is
// the extensible entry point; the V1 helpers above retain their exact source
// subsets without creating timer/manual/host API combinations.
func BindExecutorSourceCatalog(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, catalog ExecutorSourceCatalog) bool {
	return bindExecutor(driver, p, registry, handle, catalog)
}

func publishExecutorSourcesInState(driver *ExecutorDriver, now int64, withDeadline bool, state executorDriverState) (scan executorSourceScan, ok bool) {
	if !validExecutorDriver(driver) || driver.state != state || !idleExecutorScheduler(driver.p) {
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
	if !validExecutorDriver(driver) || driver.state != executorDriverActive || !idleExecutorScheduler(driver.p) {
		return executorSourceScan{}, false
	}
	if !driver.sources.acceptsScan(driver.p, now, withDeadline) {
		return executorSourceScan{}, false
	}
	// Epoch A resolves and promotes its complete owner-claimed snapshot
	// immediately. Continuous producer traffic must not delay that promotion.
	first, firstOK := serviceExecutorPublishedEpochAt(driver, now, withDeadline)
	total.add(first)
	if !firstOK {
		return total, false
	}
	if _, ackOK := driver.registry.Acknowledge(driver.handle); !ackOK {
		return total, false
	}

	// Epoch B is unconditional. It closes the post-before-request race around
	// Acknowledge: an earlier coalesced request is caught by this full pass,
	// while a later request remains published for the next Poll. Pending and
	// Requested are therefore scheduling hints after B, never reasons to wait
	// for a producer-silent cut inside this Poll.
	recheck, recheckOK := serviceExecutorPublishedEpochAt(driver, now, withDeadline)
	total.add(recheck)
	return total, recheckOK
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
// explicit monotonic sample. Its wait/timer component counts are retained for
// the V1 adapter ABI; scheduling decisions use the aggregate scan.
func PollExecutorAt(driver *ExecutorDriver, now int64) (waits, timers, promoted int, ok bool) {
	if driver == nil || !driver.sources.usesMonotonicTime() {
		return 0, 0, 0, false
	}
	scan, ok := pollExecutorSourcesAt(driver, now, true)
	return scan.waits, scan.timers, scan.promoted, ok
}

// NextExecutorTimerDeadline exposes the scheduler owner's current earliest
// active absolute deadline without draining it. Platform wait adapters use it
// to choose a sleep deadline; scheduler-service preemption is independent of
// this timer query. The query deliberately accepts no clock or callback.
func NextExecutorTimerDeadline(driver *ExecutorDriver) (deadline int64, hasDeadline, ok bool) {
	if !validExecutorDriver(driver) || !driver.sources.usesMonotonicTime() || driver.state != executorDriverActive ||
		!idleExecutorScheduler(driver.p) {
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

func leaveExecutorIdleAndPoll(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	if !leaveExecutorIdle(driver) {
		return 0, 0, false
	}
	return pollExecutor(driver)
}

func leaveExecutorIdleAndPollAt(driver *ExecutorDriver, now int64) (scan executorSourceScan, ok bool) {
	if !leaveExecutorIdle(driver) {
		return executorSourceScan{}, false
	}
	return pollExecutorSourcesAt(driver, now, true)
}

// PrepareExecutorSleep services current work and, only when parked Gs remain
// with no runnable work, executes ArmIdle, an unconditional final source scan,
// and exact CommitSleep. A true sleep result authorizes the target to enter its
// retained wait. false,true means work or a racing request won and the
// scheduler should continue without blocking.
func PrepareExecutorSleep(driver *ExecutorDriver) (sleep bool, ok bool) {
	if !validExecutorDriver(driver) || driver.sources.usesMonotonicTime() || driver.state != executorDriverActive || !idleExecutorScheduler(driver.p) {
		return false, false
	}
	if _, _, ok = pollExecutor(driver); !ok {
		return false, false
	}
	if driver.p.readyHead != nil || !HasWaiting(driver.p) {
		return false, true
	}
	if !driver.registry.ArmIdle(driver.handle) {
		// Request won the exact zero-gate race. Service it while still active.
		if _, _, ok = pollExecutor(driver); !ok {
			return false, false
		}
		return false, true
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
	hasWork := drained != 0 || driver.p.readyHead != nil || driver.sources.pending(driver.p) ||
		driver.registry.ObserveRequested(driver.handle) || preemptLoad(&driver.p.schedule) != scheduleIdle
	if hasWork {
		if _, _, ok = leaveExecutorIdleAndPoll(driver); !ok {
			return false, false
		}
		return false, true
	}
	if !driver.registry.CommitSleep(driver.handle) {
		if _, _, ok = leaveExecutorIdleAndPoll(driver); !ok {
			return false, false
		}
		return false, true
	}
	driver.state = executorDriverSleeping
	return true, true
}

// PrepareExecutorSleepAt performs the first half of timer-aware retained-wait
// admission. It services the complete source set at now, publishes IdleArmed,
// and scans the complete set once more. true,true leaves the driver in an explicit
// idle-preparing state and requires the caller to take a fresh monotonic sample
// and call CommitExecutorSleepAt. false,true means work won and the driver is
// active. A failure never leaves a newly armed idle gate behind.
func PrepareExecutorSleepAt(driver *ExecutorDriver, now int64) (prepared bool, ok bool) {
	if !validExecutorDriver(driver) || !driver.sources.usesMonotonicTime() || driver.state != executorDriverActive ||
		!idleExecutorScheduler(driver.p) || now < 0 {
		return false, false
	}
	if _, ok = pollExecutorSourcesAt(driver, now, true); !ok {
		return false, false
	}
	if driver.p.readyHead != nil || !HasWaiting(driver.p) {
		return false, true
	}
	if !driver.registry.ArmIdle(driver.handle) {
		// Request won the exact zero-gate race. Service it while still active.
		if _, ok = pollExecutorSourcesAt(driver, now, true); !ok {
			return false, false
		}
		return false, true
	}

	// Scan facts, not just pending, after publishing IdleArmed. Commit performs
	// another complete scan at a caller-supplied fresh timestamp.
	scan, scanOK := publishExecutorSourcesAt(driver, now, true)
	if !scanOK {
		_ = leaveExecutorIdle(driver)
		return false, false
	}
	hasWork := scan.completed != 0 || driver.p.readyHead != nil ||
		driver.sources.pending(driver.p) || driver.registry.ObserveRequested(driver.handle) ||
		preemptLoad(&driver.p.schedule) != scheduleIdle
	if hasWork {
		if _, ok = leaveExecutorIdleAndPollAt(driver, now); !ok {
			return false, false
		}
		return false, true
	}
	driver.prepareNow = now
	driver.hasPrepareNow = true
	driver.state = executorDriverIdlePreparing
	return true, true
}

// CommitExecutorSleepAt finishes timer-aware retained-wait admission after the
// target has sampled its monotonic clock again. It unconditionally rescans the
// complete source set at now before exact CommitSleep. A successful sleep
// returns the earliest still-active absolute deadline; a future deadline is a
// poll bound, not runnable work. Passing an invalid timestamp aborts a pending
// preparation and restores the active driver.
func CommitExecutorSleepAt(driver *ExecutorDriver, now int64) (sleep bool, deadline int64, hasDeadline, ok bool) {
	if !validExecutorDriver(driver) || !driver.sources.usesMonotonicTime() ||
		driver.state != executorDriverIdlePreparing || !idleExecutorScheduler(driver.p) {
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
	hasWork := scan.completed != 0 || driver.p.readyHead != nil ||
		driver.sources.pending(driver.p) || driver.registry.ObserveRequested(driver.handle) ||
		preemptLoad(&driver.p.schedule) != scheduleIdle
	if hasWork {
		if _, ok = leaveExecutorIdleAndPollAt(driver, now); !ok {
			return false, 0, false, false
		}
		return false, 0, false, true
	}
	if scan.hasDeadline && scan.deadline <= now {
		_ = leaveExecutorIdle(driver)
		return false, 0, false, false
	}
	if !driver.registry.CommitSleep(driver.handle) {
		if _, ok = leaveExecutorIdleAndPollAt(driver, now); !ok {
			return false, 0, false, false
		}
		return false, 0, false, true
	}
	driver.prepareNow = 0
	driver.hasPrepareNow = false
	driver.state = executorDriverSleeping
	return true, scan.deadline, scan.hasDeadline, true
}

// WakeExecutor leaves a committed retained wait and immediately services all
// durable sources. It also accepts a spurious target wake while the gate still
// contains exact IdleArmed.
func WakeExecutor(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	if !validExecutorDriver(driver) || driver.sources.usesMonotonicTime() || driver.state != executorDriverSleeping || !idleExecutorScheduler(driver.p) {
		return 0, 0, false
	}
	return leaveExecutorIdleAndPoll(driver)
}

// WakeExecutorAt leaves a committed timer-aware retained wait and services both
// source set using the target's fresh post-wake monotonic sample.
func WakeExecutorAt(driver *ExecutorDriver, now int64) (waits, timers, promoted int, ok bool) {
	if !validExecutorDriver(driver) || !driver.sources.usesMonotonicTime() || driver.state != executorDriverSleeping ||
		!idleExecutorScheduler(driver.p) || now < 0 {
		return 0, 0, 0, false
	}
	scan, ok := leaveExecutorIdleAndPollAt(driver, now)
	return scan.waits, scan.timers, scan.promoted, ok
}

// BeginExecutorClose seals a quiescent driver before physical backend
// unregister/join. Runnable Gs may remain for command cancellation, but no
// running or parked G and no live registration may still depend on the backend.
// Terminal and command shutdown reject a bound driver, so callers must finish
// this close before entering those state machines.
func BeginExecutorClose(driver *ExecutorDriver) bool {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive || !idleExecutorScheduler(driver.p) ||
		driver.terminalKind != ActionInvalid ||
		!emptySchedulerWaitQueues(driver.p) ||
		!driver.sources.empty(driver.p) {
		return false
	}
	schedule := preemptLoad(&driver.p.schedule)
	if schedule != scheduleIdle && schedule != scheduleDisabled {
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
	if p == nil || g == nil || p.current != g || p.inResume ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken ||
		!ValidG(g) || g.runP != p || g.destroyTarget != nil || !g.destroyRoot ||
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

func terminalExecutorCloseCandidate(p *P, g *G, action Action) (*ExecutorDriver, bool) {
	if !expectedAction(p, g, action, action.Kind) || !terminalExecutorRootPending(p, g, action.Kind) ||
		preemptLoad(&p.executorMode) != executorModeBound {
		return nil, false
	}
	driver := p.executor
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		driver.terminalKind != ActionInvalid || !driver.sources.canBeginTerminalClose(p) {
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
func beginTerminalExecutorClose(p *P, g *G, action Action) (Action, bool) {
	driver, ok := terminalExecutorCloseCandidate(p, g, action)
	if !ok || !driver.sources.beginTerminalClose(p) || !settleTerminalExecutorClose(driver, p, g) {
		return Action{}, false
	}
	driver.terminalKind = action.Kind
	driver.state = executorDriverTerminalClosing
	g.root = nil
	closeAction := Action{Kind: ActionTerminalExecutorClose}
	p.action = closeAction
	return closeAction, true
}

func terminalExecutorCloseDriver(p *P, g *G, action Action) (*ExecutorDriver, bool) {
	if p == nil || action.Kind != ActionTerminalExecutorClose || action.Handle != nil ||
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
	// The synthetic token is a stable core-private equality marker. Destroyed
	// and PanicDestroyed never dereference it, and it is never returned to the
	// adapter as a handle operation.
	original := Action{Kind: driver.terminalKind, Handle: unsafe.Pointer(driver)}
	if !retireExecutorBinding(driver, &original) {
		return nil, Action{}, false
	}
	for {
		var next Action
		switch original.Kind {
		case ActionDestroy:
			next, ok = Destroyed(p, g, original)
			if !ok && AcknowledgeTerminalSchedule(p, g, original) {
				continue
			}
		case ActionPanicDestroy:
			next, ok = PanicDestroyed(p, g, original)
			if !ok && AcknowledgePanicTerminalSchedule(p, g, original) {
				continue
			}
		default:
			return nil, Action{}, false
		}
		if !ok || next.Handle != nil ||
			(next.Kind != ActionComplete && next.Kind != ActionPanicComplete) {
			return nil, Action{}, false
		}
		return g, next, true
	}
}
