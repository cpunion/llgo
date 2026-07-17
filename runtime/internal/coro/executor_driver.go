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
// ExecutorRegistry gate and scheduler-owned durable wait and timer
// registrations. It is
// never retained by a platform callback: the platform ABI remains the two POD
// handles carried by PostWaitAndRequest.
//
// Every method is scheduler-owner-only. The driver, P, registry, and table must
// remain at stable addresses from BindExecutor through ConfirmExecutorClose.
// A real target surrounds a successful PrepareExecutorSleep with its retained
// wait and calls WakeExecutor after a real or spurious wake.
//
// This first driver deliberately owns exactly one P, one wait table, and an
// optional timer table. Targets with timers must use the explicit At APIs and
// supply monotonic timestamps; the driver never retains a clock callback or an
// interface value. It provides the handle-free last-G terminal close handoff,
// while the target-specific join dispatcher, channel/syscall source sets, and
// multi-P executor migration remain later layers.
type ExecutorDriver struct {
	magic         uint32
	state         executorDriverState
	p             *P
	registry      *ExecutorRegistry
	handle        ExecutorHandle
	waits         *WaitRegistrationTable
	timers        *TimerRegistrationTable
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
	validTimers := driver.timers == nil || driver.timers.owner == driver.p
	return (driver.state == executorDriverTerminalClosing) == validTerminalState && validTimers &&
		driver.p != nil && driver.registry != nil && driver.handle.Slot != 0 && driver.handle.Generation != 0 &&
		driver.waits != nil && driver.p.executor == driver &&
		preemptLoad(&driver.p.executorMode) == executorModeBound && driver.waits.owner == driver.p
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
	return PrepareWaitRegistration(driver.p, driver.waits, token)
}

// RollbackExecutorWaitRegistration is owner-only and valid before coroPark
// when external submission never made the POD handle callback-reachable.
func RollbackExecutorWaitRegistration(driver *ExecutorDriver, token *WaitToken, ticket WaitTicket, wait WaitRegistrationHandle) bool {
	return validRunningExecutorOwner(driver) && driver.waits.RollbackPreparedWait(wait, token, ticket)
}

// RetireCompletedExecutorWait is owner-only and valid after the matching park
// resumed and the external source was strongly joined or unregistered.
func RetireCompletedExecutorWait(driver *ExecutorDriver, token *WaitToken, ticket WaitTicket, wait WaitRegistrationHandle) bool {
	return validRunningExecutorOwner(driver) && driver.waits.RetireCompletedWait(wait, token, ticket)
}

// PrepareExecutorTimerRegistration is the only production owner entry for an
// absolute monotonic one-shot timer. It is valid only for a timer-bound driver
// while its exact frame is running.
func PrepareExecutorTimerRegistration(driver *ExecutorDriver, token *WaitToken, deadline int64) (WaitTicket, TimerRegistrationHandle, TimerRegistrationPrepareResult) {
	if !validRunningExecutorOwner(driver) || driver.timers == nil {
		return 0, TimerRegistrationHandle{}, TimerRegistrationPrepareInvalid
	}
	return PrepareTimerRegistration(driver.p, driver.timers, token, deadline)
}

// RollbackExecutorTimerRegistration releases a timer that was prepared by the
// running owner but was never made visible to coroPark.
func RollbackExecutorTimerRegistration(driver *ExecutorDriver, token *WaitToken, ticket WaitTicket, timer TimerRegistrationHandle) bool {
	return validRunningExecutorOwner(driver) && driver.timers != nil &&
		driver.timers.RollbackPreparedTimer(timer, token, ticket)
}

// CancelExecutorTimerRegistration publishes cancellation from the exact
// running owner. The matching terminal outcome must still be consumed before
// the timer can be retired.
func CancelExecutorTimerRegistration(driver *ExecutorDriver, timer TimerRegistrationHandle) WaitCancelResult {
	if !validRunningExecutorOwner(driver) || driver.timers == nil {
		return WaitCancelInvalid
	}
	return driver.timers.Cancel(timer)
}

// RetireCompletedExecutorTimer validates and retires a consumed timer
// completion from its resumed synchronous continuation.
func RetireCompletedExecutorTimer(driver *ExecutorDriver, token *WaitToken, ticket WaitTicket, timer TimerRegistrationHandle) bool {
	return validRunningExecutorOwner(driver) && driver.timers != nil &&
		driver.timers.RetireCompletedTimer(timer, token, ticket)
}

// RetireCanceledExecutorTimer validates and retires a consumed timer
// cancellation from its resumed synchronous continuation.
func RetireCanceledExecutorTimer(driver *ExecutorDriver, token *WaitToken, ticket WaitTicket, timer TimerRegistrationHandle) bool {
	return validRunningExecutorOwner(driver) && driver.timers != nil &&
		driver.timers.RetireCanceledTimer(timer, token, ticket)
}

func activeExecutorHandle(registry *ExecutorRegistry, handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	return ok && preemptLoad(&slot.generation) == handle.Generation &&
		preemptLoad(&slot.state) == uint32(executorActive) && preemptLoad(&slot.inflight) == 0 &&
		preemptLoad(&slot.gate) == 0
}

func idleExecutorScheduler(p *P) bool {
	return p != nil && p.current == nil && !p.inResume && p.action.Kind == ActionInvalid && p.action.Handle == nil &&
		p.timerPreemptBudget == 0 && validReadyQueue(p) && validWaitQueue(p)
}

// BindExecutor attaches a newly registered exact-zero executor gate and an
// empty registration table to one P. Publication is pointer-first and atomic
// mode-last so legacy asynchronous RequestSchedule calls fail without reading
// scheduler-owned binding fields. The caller must already have strongly
// quiesced every legacy source that knew this P, including a call paused before
// its executorMode load; executorMode is a capability guard, not a refcounted
// admission barrier for migration from the legacy ABI.
func bindExecutor(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, waits *WaitRegistrationTable, timers *TimerRegistrationTable) bool {
	if driver == nil || driver.magic != 0 || driver.state != executorDriverUnbound || driver.p != nil ||
		driver.registry != nil || driver.handle != (ExecutorHandle{}) || driver.waits != nil || driver.timers != nil ||
		driver.prepareNow != 0 || driver.hasPrepareNow ||
		driver.terminalKind != ActionInvalid ||
		p == nil || p.executor != nil || preemptLoad(&p.executorMode) != executorModeUnbound ||
		preemptLoad(&p.schedule) != scheduleIdle || !idleExecutorScheduler(p) ||
		p.readyHead != nil || p.readyTail != nil || p.waitHead != nil || p.waitTail != nil ||
		!activeExecutorHandle(registry, handle) || !bindRegistrationTable(waits, p) {
		return false
	}
	if timers != nil && !bindTimerRegistrationTable(timers, p) {
		// The wait table was empty when bound above, so rollback cannot lose a
		// registration. Preserve a zero driver and unbound P on rejection.
		_ = unbindRegistrationTable(waits, p)
		return false
	}
	driver.magic = executorDriverMagic
	driver.state = executorDriverActive
	driver.p = p
	driver.registry = registry
	driver.handle = handle
	driver.waits = waits
	driver.timers = timers
	p.executor = driver
	preemptStore(&p.executorMode, executorModeBound)
	return true
}

func BindExecutor(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, waits *WaitRegistrationTable) bool {
	return bindExecutor(driver, p, registry, handle, waits, nil)
}

// BindExecutorWithTimers attaches both durable source tables. A timer-bound
// driver accepts only the explicit At poll/sleep/wake APIs, so omitting a
// monotonic timestamp fails closed instead of silently delaying expiry.
func BindExecutorWithTimers(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, waits *WaitRegistrationTable, timers *TimerRegistrationTable) bool {
	return timers != nil && bindExecutor(driver, p, registry, handle, waits, timers)
}

type executorSourceScan struct {
	waits    int
	timers   int
	promoted int
	deadline int64
	hasTimer bool
}

func (scan *executorSourceScan) add(other executorSourceScan) {
	scan.waits += other.waits
	scan.timers += other.timers
	scan.promoted += other.promoted
	scan.deadline = other.deadline
	scan.hasTimer = other.hasTimer
}

func drainExecutorSourcesInState(driver *ExecutorDriver, now int64, withTimers bool, state executorDriverState) (scan executorSourceScan, ok bool) {
	if !validExecutorDriver(driver) || driver.state != state || !idleExecutorScheduler(driver.p) {
		return executorSourceScan{}, false
	}
	if withTimers != (driver.timers != nil) || withTimers && now < 0 {
		return executorSourceScan{}, false
	}
	scan.waits, ok = driver.waits.drainFor(driver.p)
	if !ok {
		// A prior slot delivery is irreversible. Preserve partial progress just
		// like an I/O count returned with an error; callers must still fail closed.
		return scan, false
	}
	if withTimers {
		scan.timers, scan.deadline, scan.hasTimer, ok = driver.timers.drainDueFor(driver.p, now)
		if !ok {
			return scan, false
		}
	}
	scan.promoted, ok = pollReady(driver.p)
	return scan, ok
}

func drainExecutorSourcesAt(driver *ExecutorDriver, now int64, withTimers bool) (scan executorSourceScan, ok bool) {
	return drainExecutorSourcesInState(driver, now, withTimers, executorDriverActive)
}

func drainExecutorSources(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	scan, ok := drainExecutorSourcesAt(driver, 0, false)
	return scan.waits, scan.promoted, ok
}

func pollExecutorSourcesAt(driver *ExecutorDriver, now int64, withTimers bool) (total executorSourceScan, ok bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive || !idleExecutorScheduler(driver.p) {
		return executorSourceScan{}, false
	}
	if withTimers != (driver.timers != nil) || withTimers && now < 0 {
		return executorSourceScan{}, false
	}
	for {
		first, passOK := drainExecutorSourcesAt(driver, now, withTimers)
		total.add(first)
		if !passOK {
			return total, false
		}
		if _, ackOK := driver.registry.Acknowledge(driver.handle); !ackOK {
			return total, false
		}

		// This pass is unconditional. A producer may have coalesced into the
		// request that Acknowledge just cleared, and pending is only advisory.
		recheck, recheckOK := drainExecutorSourcesAt(driver, now, withTimers)
		total.add(recheck)
		if !recheckOK {
			return total, false
		}
		if recheck.waits == 0 && recheck.timers == 0 && !driver.waits.Pending() &&
			!driver.registry.ObserveRequested(driver.handle) {
			return total, true
		}
	}
}

func pollExecutor(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	scan, ok := pollExecutorSourcesAt(driver, 0, false)
	return scan.waits, scan.promoted, ok
}

// PollExecutor services the bound durable source set after a running G has
// yielded or while the scheduler otherwise owns P. It is the only place that
// acknowledges the stable executor request.
func PollExecutor(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	if driver == nil || driver.timers != nil {
		return 0, 0, false
	}
	return pollExecutor(driver)
}

// PollExecutorAt services both durable source tables with one explicit
// monotonic sample. Every drain/ack/unconditional-rescan transaction drains
// wait posts, completes due timers, and promotes ready Gs in that order.
func PollExecutorAt(driver *ExecutorDriver, now int64) (waits, timers, promoted int, ok bool) {
	if driver == nil || driver.timers == nil {
		return 0, 0, 0, false
	}
	scan, ok := pollExecutorSourcesAt(driver, now, true)
	return scan.waits, scan.timers, scan.promoted, ok
}

// NextExecutorTimerDeadline exposes the scheduler owner's current earliest
// active absolute deadline without draining it. BeginRunG queries this while
// the scheduler is idle and arms its fixed safepoint budget so a continuously
// runnable G cannot hide timer pressure. The query deliberately accepts no
// clock or callback.
func NextExecutorTimerDeadline(driver *ExecutorDriver) (deadline int64, hasDeadline, ok bool) {
	if !validExecutorDriver(driver) || driver.timers == nil || driver.state != executorDriverActive ||
		!idleExecutorScheduler(driver.p) {
		return 0, false, false
	}
	return driver.timers.nextDeadlineFor(driver.p)
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
	if !validExecutorDriver(driver) || driver.timers != nil || driver.state != executorDriverActive || !idleExecutorScheduler(driver.p) {
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
	drained, promoted, scanOK := drainExecutorSources(driver)
	if !scanOK {
		// ArmIdle succeeded from exact zero, so the only legal gates here are
		// IdleArmed with or without Requested and LeaveIdle must disarm either.
		// The source-scan failure is already fatal even if corruption also makes
		// this best-effort cleanup fail.
		_, _ = driver.registry.LeaveIdle(driver.handle)
		return false, false
	}
	hasWork := drained != 0 || promoted != 0 || driver.p.readyHead != nil || driver.waits.Pending() ||
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
// admission. It services both source tables at now, publishes IdleArmed, and
// scans both sources once more. true,true leaves the driver in an explicit
// idle-preparing state and requires the caller to take a fresh monotonic sample
// and call CommitExecutorSleepAt. false,true means work won and the driver is
// active. A failure never leaves a newly armed idle gate behind.
func PrepareExecutorSleepAt(driver *ExecutorDriver, now int64) (prepared bool, ok bool) {
	if !validExecutorDriver(driver) || driver.timers == nil || driver.state != executorDriverActive ||
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
	scan, scanOK := drainExecutorSourcesAt(driver, now, true)
	if !scanOK {
		_ = leaveExecutorIdle(driver)
		return false, false
	}
	hasWork := scan.waits != 0 || scan.timers != 0 || scan.promoted != 0 || driver.p.readyHead != nil ||
		driver.waits.Pending() || driver.registry.ObserveRequested(driver.handle) ||
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
// target has sampled its monotonic clock again. It unconditionally rescans
// wait posts and timers at now before exact CommitSleep. A successful sleep
// returns the earliest still-active absolute deadline; a future deadline is a
// poll bound, not runnable work. Passing an invalid timestamp aborts a pending
// preparation and restores the active driver.
func CommitExecutorSleepAt(driver *ExecutorDriver, now int64) (sleep bool, deadline int64, hasDeadline, ok bool) {
	if !validExecutorDriver(driver) || driver.timers == nil ||
		driver.state != executorDriverIdlePreparing || !idleExecutorScheduler(driver.p) {
		return false, 0, false, false
	}
	if now < driver.prepareNow {
		_ = leaveExecutorIdle(driver)
		return false, 0, false, false
	}

	scan, scanOK := drainExecutorSourcesInState(driver, now, true, executorDriverIdlePreparing)
	if !scanOK {
		_ = leaveExecutorIdle(driver)
		return false, 0, false, false
	}
	hasWork := scan.waits != 0 || scan.timers != 0 || scan.promoted != 0 || driver.p.readyHead != nil ||
		driver.waits.Pending() || driver.registry.ObserveRequested(driver.handle) ||
		preemptLoad(&driver.p.schedule) != scheduleIdle
	if hasWork {
		if _, ok = leaveExecutorIdleAndPollAt(driver, now); !ok {
			return false, 0, false, false
		}
		return false, 0, false, true
	}
	if scan.hasTimer && scan.deadline <= now {
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
	return true, scan.deadline, scan.hasTimer, true
}

// WakeExecutor leaves a committed retained wait and immediately services all
// durable sources. It also accepts a spurious target wake while the gate still
// contains exact IdleArmed.
func WakeExecutor(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	if !validExecutorDriver(driver) || driver.timers != nil || driver.state != executorDriverSleeping || !idleExecutorScheduler(driver.p) {
		return 0, 0, false
	}
	return leaveExecutorIdleAndPoll(driver)
}

// WakeExecutorAt leaves a committed timer-aware retained wait and services both
// source tables using the target's fresh post-wake monotonic sample.
func WakeExecutorAt(driver *ExecutorDriver, now int64) (waits, timers, promoted int, ok bool) {
	if !validExecutorDriver(driver) || driver.timers == nil || driver.state != executorDriverSleeping ||
		!idleExecutorScheduler(driver.p) || now < 0 {
		return 0, 0, 0, false
	}
	scan, ok := leaveExecutorIdleAndPollAt(driver, now)
	return scan.waits, scan.timers, scan.promoted, ok
}

func executorTimerTableEmpty(driver *ExecutorDriver, p *P) bool {
	return driver != nil && (driver.timers == nil || timerRegistrationTableEmpty(driver.timers, p))
}

// BeginExecutorClose seals a quiescent driver before physical backend
// unregister/join. Runnable Gs may remain for command cancellation, but no
// running or parked G and no live registration may still depend on the backend.
// Terminal and command shutdown reject a bound driver, so callers must finish
// this close before entering those state machines.
func BeginExecutorClose(driver *ExecutorDriver) bool {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive || !idleExecutorScheduler(driver.p) ||
		driver.terminalKind != ActionInvalid ||
		driver.p.waitHead != nil || driver.p.waitTail != nil ||
		!registrationTableEmpty(driver.waits, driver.p) || !executorTimerTableEmpty(driver, driver.p) {
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
	drained, ok := driver.waits.drainFor(driver.p)
	return ok && drained == 0 && registrationTableEmpty(driver.waits, driver.p) &&
		executorTimerTableEmpty(driver, driver.p)
}

func retireExecutorBinding(driver *ExecutorDriver, restoreAction *Action) bool {
	if !validExecutorDriver(driver) ||
		!registrationTableEmpty(driver.waits, driver.p) || !executorTimerTableEmpty(driver, driver.p) ||
		!driver.registry.ConfirmQuiesced(driver.handle) || !driver.registry.Retire(driver.handle) {
		return false
	}
	p, waits, timers := driver.p, driver.waits, driver.timers
	if timers != nil && !unbindTimerRegistrationTable(timers, p) {
		return false
	}
	if !unbindRegistrationTable(waits, p) {
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
// the stable generation and unbinds the empty wait table and P.
func ConfirmExecutorClose(driver *ExecutorDriver) bool {
	if !validExecutorDriver(driver) || driver.state != executorDriverClosing || !idleExecutorScheduler(driver.p) ||
		driver.terminalKind != ActionInvalid ||
		driver.p.waitHead != nil || driver.p.waitTail != nil ||
		!finalDrainExecutorSources(driver) {
		return false
	}
	return retireExecutorBinding(driver, nil)
}

func terminalExecutorRootPending(p *P, g *G, kind ActionKind) bool {
	if p == nil || g == nil || p.current != g || p.inResume ||
		!ValidG(g) || g.runP != p || g.destroyTarget != nil || !g.destroyRoot ||
		g.active != nil || g.frames != nil ||
		p.readyHead != nil || p.readyTail != nil || p.waitHead != nil || p.waitTail != nil ||
		!validReadyQueue(p) || !validWaitQueue(p) || preemptLoad(&p.schedule) != scheduleIdle {
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
		driver.terminalKind != ActionInvalid || !registrationTableEmpty(driver.waits, p) ||
		!executorTimerTableEmpty(driver, p) {
		return nil, false
	}
	return driver, true
}

func settleTerminalExecutorClose(driver *ExecutorDriver, p *P) bool {
	for {
		if !validExecutorDriver(driver) || driver.state != executorDriverActive || driver.p != p ||
			driver.terminalKind != ActionInvalid || !registrationTableEmpty(driver.waits, p) ||
			!executorTimerTableEmpty(driver, p) {
			return false
		}
		drained, ok := driver.waits.drainFor(p)
		if !ok || drained != 0 {
			return false
		}
		if _, ok = driver.registry.Acknowledge(driver.handle); !ok {
			return false
		}

		// Recheck the complete durable source set after acknowledgement. If a
		// request wins the following exact close race, loop and repeat the same
		// transaction; the destroyed LLVM handle is not part of this path.
		drained, ok = driver.waits.drainFor(p)
		if !ok || drained != 0 || !registrationTableEmpty(driver.waits, p) ||
			!executorTimerTableEmpty(driver, p) {
			return false
		}
		if driver.waits.Pending() || driver.registry.ObserveRequested(driver.handle) {
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
	if !ok || !settleTerminalExecutorClose(driver, p) {
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
	if !ok || want != driver || !finalDrainExecutorSources(driver) {
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
