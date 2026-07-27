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

const (
	// Keep the exceptional user-visible LockOSThread nesting and the
	// scheduler's temporary synchronous-foreign-reentry affinity in one byte.
	// The latter is deliberately not a user lock: unmatched UnlockOSThread
	// calls must not release it, while every physical-owner invariant may treat
	// either portion as an exact G-to-M affinity.
	osThreadUserLockMask      uint8 = 1<<7 - 1
	osThreadForeignReentryBit uint8 = 1 << 7
)

func osThreadUserLockDepth(g *G) uint8 {
	if g == nil {
		return 0
	}
	return g.osThreadLockDepth & osThreadUserLockMask
}

func osThreadForeignReentryAffined(g *G) bool {
	return g != nil && g.osThreadLockDepth&osThreadForeignReentryBit != 0
}

func enterOSThreadForeignReentryAffinity(p *P, g *G) bool {
	if p == nil || !ValidG(g) || p.osThreadSuspend != osThreadSuspendAttached ||
		p.osThreadLockOwner != nil || osThreadForeignReentryAffined(g) {
		return false
	}
	g.osThreadLockDepth |= osThreadForeignReentryBit
	p.osThreadLockOwner = g
	return true
}

func exitOSThreadForeignReentryAffinity(p *P, g *G) bool {
	if p == nil || !ValidG(g) || p.osThreadSuspend != osThreadSuspendAttached ||
		p.osThreadLockOwner != nil || !osThreadForeignReentryAffined(g) {
		return false
	}
	g.osThreadLockDepth &^= osThreadForeignReentryBit
	return true
}

type osThreadSuspendPhase uint8

const (
	// Attached is the zero state: the physical M which owns P is also the only
	// M permitted to resume the locked G.
	osThreadSuspendAttached osThreadSuspendPhase = iota
	// Park keeps the locked G dormant until a source promotes it runnable.
	osThreadSuspendPark
	// YieldNeedsPeer requires the replacement M to complete one peer physical
	// Action before the yielding owner may regain P.
	osThreadSuspendYieldNeedsPeer
	osThreadSuspendYieldPeerServiced
)

// validOSThreadEnqueue proves the persistent half of a locked G/P relation.
// Unrelated unlocked Gs may remain queued on the island; whether the current
// physical owner may execute them is a separate phase check.
func validOSThreadEnqueue(p *P, g *G) bool {
	if p == nil || g == nil {
		return false
	}
	if g.osThreadLockDepth == 0 {
		return p.osThreadLockOwner != g
	}
	return p.osThreadLockOwner == g
}

func validOSThreadRunOwner(p *P, g *G) bool {
	if p == nil || g == nil {
		return false
	}
	owner := p.osThreadLockOwner
	if owner == nil {
		return p.osThreadSuspend == osThreadSuspendAttached &&
			g.osThreadLockDepth == 0
	}
	switch p.osThreadSuspend {
	case osThreadSuspendAttached:
		return owner == g && g.osThreadLockDepth != 0
	case osThreadSuspendPark,
		osThreadSuspendYieldNeedsPeer,
		osThreadSuspendYieldPeerServiced:
		return owner != g && g.osThreadLockDepth == 0
	default:
		return false
	}
}

func validOSThreadOwnerHeader(p *P) bool {
	if p == nil {
		return false
	}
	g := p.osThreadLockOwner
	if g == nil {
		return p.osThreadSuspend == osThreadSuspendAttached
	}
	if !ValidG(g) || g.osThreadLockDepth == 0 ||
		g.transferState != runnableTransferGIdle {
		return false
	}
	switch p.osThreadSuspend {
	case osThreadSuspendAttached:
		if g.runP != nil {
			return g.runP == p && p.current == g && !g.queued && !g.waiting
		}
	case osThreadSuspendPark:
		if g.runP != nil || p.current == g {
			return false
		}
	case osThreadSuspendYieldNeedsPeer, osThreadSuspendYieldPeerServiced:
		if g.runP != nil || p.current == g || !g.queued {
			return false
		}
	default:
		return false
	}
	if g.queued {
		return g.state == GRunnable && !g.waiting
	}
	return g.state == GWaiting && g.waiting
}

// EnterOSThreadLock binds the currently executing logical G to its current
// physical P/M ownership island. It is owner-only and may be called only from
// the exact compiler resume window; no TLS or process-global current-G lookup
// participates in the proof.
func EnterOSThreadLock(g *G) bool {
	if !enterCriticalContext(g) || osThreadUserLockDepth(g) == osThreadUserLockMask {
		return false
	}
	p := g.runP
	if p == nil || p.current != g ||
		p.osThreadSuspend != osThreadSuspendAttached {
		return false
	}
	if g.osThreadLockDepth == 0 {
		if p.osThreadLockOwner != nil {
			return false
		}
		p.osThreadLockOwner = g
	} else if p.osThreadLockOwner != g {
		return false
	}
	g.osThreadLockDepth = g.osThreadLockDepth&osThreadForeignReentryBit |
		(osThreadUserLockDepth(g) + 1)
	return true
}

// ExitOSThreadLock releases one external LockOSThread nesting level. Matching
// Go behavior, an unmatched UnlockOSThread is a no-op. The outermost release
// makes the island's already queued peers executable again.
func ExitOSThreadLock(g *G) bool {
	if !enterCriticalContext(g) {
		return false
	}
	p := g.runP
	if p == nil || p.current != g ||
		p.osThreadSuspend != osThreadSuspendAttached {
		return false
	}
	if osThreadUserLockDepth(g) == 0 {
		if osThreadForeignReentryAffined(g) {
			return p.osThreadLockOwner == g
		}
		return p.osThreadLockOwner != g
	}
	if p.osThreadLockOwner != g {
		return false
	}
	g.osThreadLockDepth = g.osThreadLockDepth&osThreadForeignReentryBit |
		(osThreadUserLockDepth(g) - 1)
	if g.osThreadLockDepth == 0 {
		p.osThreadLockOwner = nil
	}
	return true
}

// CurrentOSThreadLocked reports the exact running task's lock state. Compiler
// foreign-call lowering uses it only to choose between same-M execution and
// the ordinary any-thread worker; it is not an arbitrary-G routing API.
func CurrentOSThreadLocked(g *G) bool {
	if !enterCriticalContext(g) || g.osThreadLockDepth == 0 {
		return false
	}
	p := g.runP
	return p != nil && p.current == g && p.osThreadLockOwner == g &&
		p.osThreadSuspend == osThreadSuspendAttached
}

// releaseOSThreadLockForExit closes the logical lease before terminal G
// publication. retireOwner distinguishes a balanced/unlocked exit from a G
// which still held LockOSThread: the scheduler must clear both logical pointers
// in either case, while the target must terminate the latter physical owner.
func releaseOSThreadLockForExit(p *P, g *G) (retireOwner, ok bool) {
	if p == nil || g == nil || p.current != g || g.runP != p ||
		p.osThreadSuspend != osThreadSuspendAttached ||
		osThreadForeignReentryAffined(g) {
		return false, false
	}
	if g.osThreadLockDepth == 0 {
		return false, p.osThreadLockOwner != g
	}
	if p.osThreadLockOwner != g {
		return false, false
	}
	g.osThreadLockDepth = 0
	p.osThreadLockOwner = nil
	return true, true
}

func osThreadRunnableDemandAllowed(p *P) bool {
	if p == nil {
		return false
	}
	if p.osThreadLockOwner == nil {
		return p.osThreadSuspend == osThreadSuspendAttached
	}
	switch p.osThreadSuspend {
	case osThreadSuspendPark,
		osThreadSuspendYieldNeedsPeer,
		osThreadSuspendYieldPeerServiced:
		return true
	default:
		return false
	}
}

func osThreadSuspendPeerReady(p *P, owner *G) bool {
	if p == nil || owner == nil {
		return false
	}
	for candidate := p.readyHead; candidate != nil; candidate = candidate.nextReady {
		if candidate != owner && candidate.osThreadLockDepth == 0 {
			return true
		}
	}
	return false
}

// PrepareOSThreadSuspendHandoff converts one already committed locked
// ActionYield or ActionPark into a P-local detached phase. It records no
// physical owner. The target must publish its generation-bound M baton only
// after required=true and must call Abort before any claimant starts if that
// publication cannot be completed.
//
// Yield without an already runnable peer remains attached and resumes
// immediately. This avoids creating an M merely to hand the same G back.
func PrepareOSThreadSuspendHandoff(
	driver *ExecutorDriver,
	task *G,
	kind ActionKind,
) (required, ok bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive ||
		driver.run.issued != ActionInvalid ||
		driver.poll.phase != executorPollIdle {
		return false, false
	}
	p := driver.p
	if p == nil || task == nil ||
		!idleExecutorScheduler(p) || !validReadyQueue(p) ||
		!validSchedulerWaitQueues(p) ||
		!completedExecutorRunAction(p, task, Action{Kind: kind}) {
		return false, false
	}
	// The target observes every committed Yield/Park. An unlocked task needs no
	// physical-owner handoff, including a peer running while another locked
	// owner is detached. Only the exact locked owner may enter a new detached
	// phase; every partial or mismatched lock relation remains invalid.
	if task.osThreadLockDepth == 0 {
		return false, true
	}
	if p.osThreadLockOwner != task ||
		p.osThreadSuspend != osThreadSuspendAttached {
		return false, false
	}
	switch kind {
	case ActionYield:
		if !osThreadSuspendPeerReady(p, task) {
			return false, true
		}
		p.osThreadSuspend = osThreadSuspendYieldNeedsPeer
	case ActionPark:
		p.osThreadSuspend = osThreadSuspendPark
	default:
		return false, true
	}
	return true, validOSThreadOwnerHeader(p)
}

// OSThreadSuspendHandoffStatus observes the target-neutral half of an ordinary
// locked suspension. detached remains true across a peer Action or source
// transaction; returnable becomes true only at a stable scheduler boundary
// after a parked owner is runnable or a yielding owner's one peer Action debt
// has been satisfied.
func OSThreadSuspendHandoffStatus(
	driver *ExecutorDriver,
) (detached, returnable, ok bool) {
	if !validExecutorDriver(driver) || driver.state != executorDriverActive {
		return false, false, false
	}
	p := driver.p
	if !validOSThreadOwnerHeader(p) {
		return false, false, false
	}
	switch p.osThreadSuspend {
	case osThreadSuspendAttached:
		return false, false, true
	case osThreadSuspendPark,
		osThreadSuspendYieldNeedsPeer,
		osThreadSuspendYieldPeerServiced:
		detached = true
	default:
		return false, false, false
	}
	if driver.run.issued != ActionInvalid ||
		driver.poll.phase != executorPollIdle ||
		!idleExecutorScheduler(p) {
		return true, false, true
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return true, false, true
	}
	owner := p.osThreadLockOwner
	if owner == nil || !owner.queued || owner.state != GRunnable {
		return true, false, true
	}
	switch p.osThreadSuspend {
	case osThreadSuspendPark, osThreadSuspendYieldPeerServiced:
		return true, true, true
	default:
		return true, false, true
	}
}

// AbortOSThreadSuspendHandoff restores attached selection before a physical
// claimant has started. A serviced yield cannot be rolled back through this
// boundary because another M has already executed managed work.
func AbortOSThreadSuspendHandoff(driver *ExecutorDriver, task *G) bool {
	detached, _, ok := OSThreadSuspendHandoffStatus(driver)
	if !ok || !detached || driver == nil || task == nil {
		return false
	}
	p := driver.p
	if p.osThreadLockOwner != task ||
		p.osThreadSuspend == osThreadSuspendYieldPeerServiced {
		return false
	}
	switch p.osThreadSuspend {
	case osThreadSuspendPark, osThreadSuspendYieldNeedsPeer:
		p.osThreadSuspend = osThreadSuspendAttached
		return validOSThreadOwnerHeader(p)
	default:
		return false
	}
}

// RestoreOSThreadSuspendHandoff consumes the logical detached phase only after
// the target has proved its physical baton Returned and joined/recycled the
// exact compensation owner.
func RestoreOSThreadSuspendHandoff(driver *ExecutorDriver, task *G) bool {
	detached, returnable, ok := OSThreadSuspendHandoffStatus(driver)
	if !ok || !detached || !returnable || driver == nil || task == nil {
		return false
	}
	p := driver.p
	if p.osThreadLockOwner != task || !validReadyQueue(p) ||
		!validSchedulerWaitQueues(p) {
		return false
	}
	p.osThreadSuspend = osThreadSuspendAttached
	return validOSThreadOwnerHeader(p)
}

func validOSThreadPeerActionCommit(p *P, task *G) bool {
	if p == nil || task == nil {
		return false
	}
	switch p.osThreadSuspend {
	case osThreadSuspendAttached:
		return true
	case osThreadSuspendPark,
		osThreadSuspendYieldNeedsPeer,
		osThreadSuspendYieldPeerServiced:
		return p.osThreadLockOwner != nil &&
			task != p.osThreadLockOwner &&
			task.osThreadLockDepth == 0
	default:
		return false
	}
}

func commitOSThreadPeerAction(p *P) {
	if p != nil && p.osThreadSuspend == osThreadSuspendYieldNeedsPeer {
		p.osThreadSuspend = osThreadSuspendYieldPeerServiced
	}
}
