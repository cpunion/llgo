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
// ExecutorRegistry gate and scheduler-owned durable wait registrations. It is
// never retained by a platform callback: the platform ABI remains the two POD
// handles carried by PostWaitAndRequest.
//
// Every method is scheduler-owner-only. The driver, P, registry, and table must
// remain at stable addresses from BindExecutor through ConfirmExecutorClose.
// A real target surrounds a successful PrepareExecutorSleep with its retained
// wait and calls WakeExecutor after a real or spurious wake.
//
// This first driver deliberately owns exactly one P and one registration
// table. It provides the handle-free last-G terminal close handoff, while the
// target-specific join dispatcher, timer/channel/syscall source sets, and
// multi-P executor migration remain later layers.
type ExecutorDriver struct {
	magic        uint32
	state        executorDriverState
	p            *P
	registry     *ExecutorRegistry
	handle       ExecutorHandle
	waits        *WaitRegistrationTable
	terminalKind ActionKind
}

type executorDriverState uint8

const (
	executorDriverUnbound executorDriverState = iota
	executorDriverActive
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
	return (driver.state == executorDriverTerminalClosing) == validTerminalState &&
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

func activeExecutorHandle(registry *ExecutorRegistry, handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	return ok && preemptLoad(&slot.generation) == handle.Generation &&
		preemptLoad(&slot.state) == uint32(executorActive) && preemptLoad(&slot.inflight) == 0 &&
		preemptLoad(&slot.gate) == 0
}

func idleExecutorScheduler(p *P) bool {
	return p != nil && p.current == nil && !p.inResume && p.action.Kind == ActionInvalid && p.action.Handle == nil &&
		validReadyQueue(p) && validWaitQueue(p)
}

// BindExecutor attaches a newly registered exact-zero executor gate and an
// empty registration table to one P. Publication is pointer-first and atomic
// mode-last so legacy asynchronous RequestSchedule calls fail without reading
// scheduler-owned binding fields. The caller must already have strongly
// quiesced every legacy source that knew this P, including a call paused before
// its executorMode load; executorMode is a capability guard, not a refcounted
// admission barrier for migration from the legacy ABI.
func BindExecutor(driver *ExecutorDriver, p *P, registry *ExecutorRegistry, handle ExecutorHandle, waits *WaitRegistrationTable) bool {
	if driver == nil || driver.magic != 0 || driver.state != executorDriverUnbound || driver.p != nil ||
		driver.registry != nil || driver.handle != (ExecutorHandle{}) || driver.waits != nil ||
		driver.terminalKind != ActionInvalid ||
		p == nil || p.executor != nil || preemptLoad(&p.executorMode) != executorModeUnbound ||
		preemptLoad(&p.schedule) != scheduleIdle || !idleExecutorScheduler(p) ||
		p.readyHead != nil || p.readyTail != nil || p.waitHead != nil || p.waitTail != nil ||
		!activeExecutorHandle(registry, handle) || !bindRegistrationTable(waits, p) {
		return false
	}
	driver.magic = executorDriverMagic
	driver.state = executorDriverActive
	driver.p = p
	driver.registry = registry
	driver.handle = handle
	driver.waits = waits
	p.executor = driver
	preemptStore(&p.executorMode, executorModeBound)
	return true
}

func drainExecutorSources(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive || !idleExecutorScheduler(driver.p) {
		return 0, 0, false
	}
	drained, ok = driver.waits.drainFor(driver.p)
	if !ok {
		// A prior slot delivery is irreversible. Preserve partial progress just
		// like an I/O count returned with an error; callers must still fail closed.
		return drained, 0, false
	}
	promoted, ok = pollReady(driver.p)
	return drained, promoted, ok
}

func pollExecutor(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive || !idleExecutorScheduler(driver.p) {
		return 0, 0, false
	}
	for {
		firstDrained, firstPromoted, passOK := drainExecutorSources(driver)
		drained += firstDrained
		promoted += firstPromoted
		if !passOK {
			return drained, promoted, false
		}
		if _, ackOK := driver.registry.Acknowledge(driver.handle); !ackOK {
			return drained, promoted, false
		}

		// This pass is unconditional. A producer may have coalesced into the
		// request that Acknowledge just cleared, and pending is only advisory.
		recheckDrained, recheckPromoted, recheckOK := drainExecutorSources(driver)
		drained += recheckDrained
		promoted += recheckPromoted
		if !recheckOK {
			return drained, promoted, false
		}
		if recheckDrained == 0 && !driver.waits.Pending() &&
			!driver.registry.ObserveRequested(driver.handle) {
			return drained, promoted, true
		}
	}
}

// PollExecutor services the bound durable source set after a running G has
// yielded or while the scheduler otherwise owns P. It is the only place that
// acknowledges the stable executor request.
func PollExecutor(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	return pollExecutor(driver)
}

func leaveExecutorIdleAndPoll(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	left, valid := driver.registry.LeaveIdle(driver.handle)
	if !valid || !left {
		return 0, 0, false
	}
	driver.state = executorDriverActive
	return pollExecutor(driver)
}

// PrepareExecutorSleep services current work and, only when parked Gs remain
// with no runnable work, executes ArmIdle, an unconditional final source scan,
// and exact CommitSleep. A true sleep result authorizes the target to enter its
// retained wait. false,true means work or a racing request won and the
// scheduler should continue without blocking.
func PrepareExecutorSleep(driver *ExecutorDriver) (sleep bool, ok bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive || !idleExecutorScheduler(driver.p) {
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

// WakeExecutor leaves a committed retained wait and immediately services all
// durable sources. It also accepts a spurious target wake while the gate still
// contains exact IdleArmed.
func WakeExecutor(driver *ExecutorDriver) (drained, promoted int, ok bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverSleeping || !idleExecutorScheduler(driver.p) {
		return 0, 0, false
	}
	return leaveExecutorIdleAndPoll(driver)
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
		!registrationTableEmpty(driver.waits, driver.p) {
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
	return ok && drained == 0 && registrationTableEmpty(driver.waits, driver.p)
}

func retireExecutorBinding(driver *ExecutorDriver, restoreAction *Action) bool {
	if !validExecutorDriver(driver) ||
		!driver.registry.ConfirmQuiesced(driver.handle) || !driver.registry.Retire(driver.handle) {
		return false
	}
	p, waits := driver.p, driver.waits
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
		driver.terminalKind != ActionInvalid || !registrationTableEmpty(driver.waits, p) {
		return nil, false
	}
	return driver, true
}

func settleTerminalExecutorClose(driver *ExecutorDriver, p *P) bool {
	for {
		if !validExecutorDriver(driver) || driver.state != executorDriverActive || driver.p != p ||
			driver.terminalKind != ActionInvalid || !registrationTableEmpty(driver.waits, p) {
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
		if !ok || drained != 0 || !registrationTableEmpty(driver.waits, p) {
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
