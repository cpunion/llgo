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

// ResumePanicBoundaryActive proves that handle is the exact leaf physical
// coroutine executing inside the current scheduler ActionResume episode. It
// accepts both the outer action handle and a bounded inline descendant. The
// native runtime uses this before publishing a short-lived legacy defer
// boundary; no scheduler state is changed here.
func ResumePanicBoundaryActive(g *G, handle unsafe.Pointer) bool {
	if !ValidG(g) || handle == nil || g.active == nil || g.active.handle != handle ||
		g.active.state != FrameActive || g.runP == nil || g.state != GRunning {
		return false
	}
	p := g.runP
	return p.inResume && activeResumeOwnedByAction(g)
}

// ResumePanicBoundaryReturning proves that handle names one still-open native
// resume activation on the current inline ancestry. A direct parent resume can
// return while the scheduler's logical active frame remains a deeper child
// which just published a slow transition; requiring handle==g.active at normal
// pop would therefore reject a valid unwind of those nested machine calls.
// Push and signal landing intentionally retain the stricter leaf predicate
// above. The synthetic defer chain independently authenticates which one of
// these live activations is the top machine boundary being popped.
func ResumePanicBoundaryReturning(g *G, handle unsafe.Pointer) bool {
	if !ValidG(g) || handle == nil || g.active == nil || g.runP == nil ||
		g.state != GRunning || !g.runP.inResume {
		return false
	}
	p := g.runP
	if !expectedAction(p, g, p.action, ActionResume) {
		return false
	}
	if g.active.handle == p.action.Handle {
		if p.inlineAwaitDepth != 0 {
			return false
		}
	} else {
		root := findFrame(g, p.action.Handle)
		if root == nil ||
			!validInlineAwaitReturningAncestry(g.active, root) {
			return false
		}
		// A slow transition keeps the deepest logical frame active while each
		// generated caller suspends and closes its native inline boundary.
		// Once that unwind reaches depth zero, only the scheduler's outer
		// ActionResume boundary is still live.  A descendant boundary at depth
		// zero would be stale even though its frame remains in the logical
		// ancestry.
		if p.inlineAwaitDepth == 0 && handle != p.action.Handle {
			return false
		}
	}
	frame := findFrame(g, handle)
	if frame == nil || frame.owner != g {
		return false
	}
	if frame == g.active {
		return frame.state == FrameActive
	}
	return frame.state == FrameSuspended && validInlineAwaitReturningAncestry(g.active, frame)
}

// ResumePanicBoundaryMayLand is the stronger post-longjmp certificate. A
// recoverable synchronous panic may leave source execution only after the
// compiler resume prologue consumed the exact run decision and before any
// scheduler transition was published. Critical sections fail closed: their
// invariants cannot be abandoned by a signal landing.
func ResumePanicBoundaryMayLand(g *G, handle unsafe.Pointer) bool {
	return ResumePanicBoundaryActive(g, handle) && resumeGateTaken(g) &&
		g.pending == (pendingTransition{}) && g.destroyTarget == nil && !g.destroyRoot &&
		g.spawnChild == nil && g.spawnParent == nil && g.spawnP == nil && !g.waiting
}
