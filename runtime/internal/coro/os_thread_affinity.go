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

// validOSThreadEnqueue proves the persistent half of a locked G/P relation.
// Unrelated unlocked Gs may remain queued on the island, but they are not
// executable until the owner releases it.
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
	if p.osThreadLockOwner == nil {
		return g.osThreadLockDepth == 0
	}
	return p.osThreadLockOwner == g && g.osThreadLockDepth != 0
}

func validOSThreadOwnerHeader(p *P) bool {
	if p == nil {
		return false
	}
	g := p.osThreadLockOwner
	if g == nil {
		return true
	}
	if !ValidG(g) || g.osThreadLockDepth == 0 ||
		g.transferState != runnableTransferGIdle {
		return false
	}
	if g.runP != nil {
		return g.runP == p && p.current == g && !g.queued && !g.waiting
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
	if !enterCriticalContext(g) || g.osThreadLockDepth == ^uint8(0) {
		return false
	}
	p := g.runP
	if p == nil || p.current != g {
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
	g.osThreadLockDepth++
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
	if p == nil || p.current != g {
		return false
	}
	if g.osThreadLockDepth == 0 {
		return p.osThreadLockOwner != g
	}
	if p.osThreadLockOwner != g {
		return false
	}
	g.osThreadLockDepth--
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
	return p != nil && p.current == g && p.osThreadLockOwner == g
}

// releaseOSThreadLockForExit closes the logical lease before terminal G
// publication. A later native M-retirement policy may replace the physical
// island when a user exits without balancing the lock; scheduler ownership
// must nevertheless never retain a reclaimable G pointer.
func releaseOSThreadLockForExit(p *P, g *G) bool {
	if p == nil || g == nil || p.current != g || g.runP != p {
		return false
	}
	if g.osThreadLockDepth == 0 {
		return p.osThreadLockOwner != g
	}
	if p.osThreadLockOwner != g {
		return false
	}
	g.osThreadLockDepth = 0
	p.osThreadLockOwner = nil
	return true
}
