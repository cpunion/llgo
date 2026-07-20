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

// G.preempt is one packed atomic word. The low two bits retain the original
// disabled/idle/requested request gate; the remaining bits are an owner-G-only
// nested critical depth. RequestPreempt may race with depth changes, so neither
// side is allowed to store a reconstructed word without a successful CAS.
const (
	preemptStateBits  uint32 = 2
	preemptStateMask  uint32 = 1<<preemptStateBits - 1
	preemptDepthUnit  uint32 = 1 << preemptStateBits
	preemptDepthLimit uint32 = ^uint32(0) >> preemptStateBits
)

func preemptWordState(word uint32) uint32 {
	return word & preemptStateMask
}

func preemptWordDepth(word uint32) uint32 {
	return word >> preemptStateBits
}

func validPreemptState(state uint32) bool {
	return state == preemptDisabled || state == preemptIdle || state == preemptRequested
}

func preemptWordAtDepth(state, depth uint32) (uint32, bool) {
	if !validPreemptState(state) || depth > preemptDepthLimit {
		return 0, false
	}
	return depth<<preemptStateBits | state, true
}

func loadGPreempt(g *G) uint32 {
	return preemptLoad(preemptAddress(g))
}

func gPreemptStateAtDepthZero(g *G, state uint32) bool {
	return g != nil && validPreemptState(state) && loadGPreempt(g) == state
}

func gPreemptEnabled(g *G) bool {
	if g == nil {
		return false
	}
	state := preemptWordState(loadGPreempt(g))
	return state == preemptIdle || state == preemptRequested
}

func gPreemptEnabledAtDepthZero(g *G) bool {
	if g == nil {
		return false
	}
	word := loadGPreempt(g)
	state := preemptWordState(word)
	return preemptWordDepth(word) == 0 && (state == preemptIdle || state == preemptRequested)
}

func gPreemptDepthZero(g *G) bool {
	if g == nil {
		return false
	}
	word := loadGPreempt(g)
	return preemptWordDepth(word) == 0 && validPreemptState(preemptWordState(word))
}

// compareAndSwapGPreemptStateAtDepthZero changes only a zero-depth gate. It is
// used at initialization, migration, and terminal ownership boundaries where
// retaining a non-zero depth would be a correctness failure, not state to
// preserve.
func compareAndSwapGPreemptStateAtDepthZero(g *G, old, new uint32) bool {
	if g == nil || !validPreemptState(old) || !validPreemptState(new) {
		return false
	}
	return preemptCompareAndSwap(preemptAddress(g), old, new)
}

// disableGPreempt closes a live request gate without clobbering a concurrent
// RequestPreempt. A non-zero depth is rejected even if the state bits already
// read disabled: terminal ownership may never hide an unbalanced critical
// region.
func disableGPreempt(g *G) bool {
	if g == nil {
		return false
	}
	gate := preemptAddress(g)
	for {
		word := preemptLoad(gate)
		if preemptWordDepth(word) != 0 {
			return false
		}
		switch preemptWordState(word) {
		case preemptDisabled:
			return true
		case preemptIdle, preemptRequested:
			if preemptCompareAndSwap(gate, word, preemptDisabled) {
				return true
			}
		default:
			return false
		}
	}
}

// enterCriticalContext is the exact compiler-hook execution window. The
// structural resume gate deliberately permits an existing critical depth;
// ordinary coroutine hooks use resumeGateTaken and therefore require depth 0.
func enterCriticalContext(g *G) bool {
	return ValidG(g) && resumeGateStructurallyTaken(g) &&
		g.transferState == runnableTransferGIdle && g.active != nil &&
		g.active.owner == g && g.active.handle != nil && g.active.header != nil &&
		g.active.state == FrameActive && g.active.header.G == unsafe.Pointer(g) &&
		g.active.header.SuspendReason == uint16(SuspendNone) &&
		g.active.header.Lifecycle == uint16(FrameActive) &&
		g.pending.kind == pendingNone && g.spawnChild == nil &&
		releasableParkState(&g.park)
}

// EnterCritical increments the current running G's preemption mask. It never
// consumes a pending request. Overflow, a disabled/malformed gate, or a call
// outside the exact active compiler resume window fails without mutation.
func EnterCritical(g *G) bool {
	if !enterCriticalContext(g) {
		return false
	}
	gate := preemptAddress(g)
	for {
		word := preemptLoad(gate)
		depth := preemptWordDepth(word)
		state := preemptWordState(word)
		if (state != preemptIdle && state != preemptRequested) || depth == preemptDepthLimit {
			return false
		}
		if preemptCompareAndSwap(gate, word, word+preemptDepthUnit) {
			return true
		}
	}
}

// pollPreemptDepthZero is the single consuming legal-safepoint core shared by
// PollPreempt and the outermost ExitCritical. The caller must first ensure a
// zero critical depth. A bound executor request stays published for scheduler
// drain/ack; the G gate and legacy unbound P gate retain their original
// consume-on-poll behavior.
func pollPreemptDepthZero(g *G) (bool, bool) {
	if !enterCriticalContext(g) || !gPreemptDepthZero(g) {
		return false, false
	}
	p := g.runP
	if p == nil || p.current != g {
		return false, false
	}
	word := loadGPreempt(g)
	state := preemptWordState(word)
	if preemptWordDepth(word) != 0 || (state != preemptIdle && state != preemptRequested) {
		return false, false
	}
	mode := preemptLoad(&p.executorMode)
	switch mode {
	case executorModeBound:
		driver := p.executor
		if !validExecutorDriverForP(driver, p) {
			return false, false
		}
	case executorModeUnbound:
		schedule := preemptLoad(&p.schedule)
		if schedule != scheduleIdle && schedule != scheduleRequested {
			return false, false
		}
	default:
		return false, false
	}
	budget := p.servicePreemptBudget
	if budget == 0 || budget > servicePreemptPollBudget {
		return false, false
	}

	// No fallible validation remains below this point. A request that races the
	// preflight is either consumed by the exact CAS or remains sticky for the
	// next safepoint.
	requested := compareAndSwapGPreemptStateAtDepthZero(g, preemptRequested, preemptIdle)
	if mode == executorModeBound {
		driver := p.executor
		if driver.registry.ObserveRequested(driver.handle) {
			requested = true
		}
	} else if preemptCompareAndSwap(&p.schedule, scheduleRequested, scheduleIdle) {
		requested = true
	}
	if !requested {
		if budget == 1 {
			p.servicePreemptBudget = servicePreemptPollBudget
			requested = true
		} else {
			p.servicePreemptBudget = budget - 1
		}
	}
	return requested, true
}

// ExitCritical decrements one nested mask. Nested exits cannot yield. The
// outermost exit performs one ordinary consuming preemption poll and reports
// whether the compiler must use its normal Yield lowering. Task cancellation
// is only observed here: the next scheduler resume gate remains its single
// claimant.
func ExitCritical(g *G) (mustYield, ok bool) {
	if !enterCriticalContext(g) {
		return false, false
	}
	gate := preemptAddress(g)
	for {
		word := preemptLoad(gate)
		depth := preemptWordDepth(word)
		state := preemptWordState(word)
		if (state != preemptIdle && state != preemptRequested) || depth == 0 {
			return false, false
		}
		if !preemptCompareAndSwap(gate, word, word-preemptDepthUnit) {
			continue
		}
		if depth != 1 {
			return false, true
		}
		requested, valid := pollPreemptDepthZero(g)
		if !valid {
			return false, false
		}
		return requested || g.park.taskCancelPhase == taskCancelRequested, true
	}
}
