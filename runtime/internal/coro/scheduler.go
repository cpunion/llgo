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

// GState is the scheduler state of one logical Go task.
type GState uint8

const (
	GNew GState = iota
	GRunnable
	GRunning
	GDispatching
	GDead
)

// G owns the stackless frame chain for one logical Go task.
type G struct {
	magic         uint32
	state         GState
	root          *Frame
	active        *Frame
	frames        *Frame
	pending       pendingTransition
	destroyTarget *Frame
	destroyRoot   bool
	nextReady     *G
	queued        bool
}

// P is a deterministic single-P ready queue and resume guard.
type P struct {
	current   *G
	readyHead *G
	readyTail *G
	inResume  bool
	action    Action
}

// ActionKind identifies the next compiler-owned handle operation. The core
// never invokes a callback or inspects a handle: the runtime adapter executes
// each action with a direct call to its llvm.coro wrapper, then commits the
// result through Checked, Resumed, or Destroyed.
type ActionKind uint8

const (
	ActionInvalid ActionKind = iota
	ActionCheckResume
	ActionResume
	ActionCheckDestroy
	ActionDestroy
	ActionComplete
)

// Action is one deterministic scheduler operation. Handle is opaque to the
// core and remains valid only until that operation is committed.
type Action struct {
	Kind   ActionKind
	Handle unsafe.Pointer
}

func setAction(p *P, kind ActionKind, handle unsafe.Pointer) (Action, bool) {
	if p == nil || kind == ActionInvalid || kind == ActionComplete || handle == nil {
		return Action{}, false
	}
	action := Action{Kind: kind, Handle: handle}
	p.action = action
	return action, true
}

func expectedAction(p *P, g *G, action Action, kind ActionKind) bool {
	return p != nil && p.current == g && ValidG(g) && action.Kind == kind && action.Handle != nil &&
		p.action == action
}

// InitG initializes a zero G.
func InitG(g *G) bool {
	if g == nil || g.magic != 0 || g.state != GNew || g.frames != nil || g.active != nil || g.root != nil ||
		g.pending.kind != pendingNone || g.pending.from != nil || g.pending.target != nil ||
		g.destroyTarget != nil || g.destroyRoot || g.nextReady != nil || g.queued {
		return false
	}
	g.magic = gMagic
	return true
}

// AdoptRoot associates an initial-suspended root frame with g.
func AdoptRoot(g *G, handle unsafe.Pointer) bool {
	if !ValidG(g) || g.state != GNew || g.root != nil || g.active != nil || g.pending.kind != pendingNone {
		return false
	}
	root := findFrame(g, handle)
	if root == nil || root.parent != nil || root.header == nil || root.header.Parent != nil ||
		root.state != FrameInitialSuspended {
		return false
	}
	g.root = root
	g.active = root
	g.state = GRunnable
	return true
}

// Enqueue appends a runnable G to p exactly once.
func Enqueue(p *P, g *G) bool {
	if p == nil || !ValidG(g) || g.state != GRunnable || g.queued || g.nextReady != nil {
		return false
	}
	g.queued = true
	if p.readyTail == nil {
		p.readyHead = g
	} else {
		p.readyTail.nextReady = g
	}
	p.readyTail = g
	return true
}

func dequeue(p *P) *G {
	if p == nil || p.readyHead == nil {
		return nil
	}
	g := p.readyHead
	p.readyHead = g.nextReady
	if p.readyHead == nil {
		p.readyTail = nil
	}
	g.nextReady = nil
	g.queued = false
	return g
}

// NextRunnable removes the next ready G. It returns ok=false when a scheduler
// operation is already in progress; an empty ready queue is (nil, true).
func NextRunnable(p *P) (g *G, ok bool) {
	if p == nil || p.current != nil || p.inResume || p.action.Kind != ActionInvalid {
		return nil, false
	}
	return dequeue(p), true
}

func dispatchPending(g *G, resumed *Frame) (destroy *Frame, ok bool) {
	pending := g.pending
	g.pending = pendingTransition{}
	if pending.from != resumed {
		return nil, false
	}
	switch pending.kind {
	case pendingAwait:
		child := pending.target
		if child == nil || child.parent != resumed || resumed.header == nil || child.header == nil ||
			resumed.header.Lifecycle != uint16(FrameSuspended) ||
			child.header.Lifecycle != uint16(FrameInitialSuspended) {
			return nil, false
		}
		resumed.state = FrameSuspended
		g.active = child
		return nil, true
	case pendingComplete:
		if pending.target != nil || resumed.header == nil ||
			resumed.header.Lifecycle != uint16(FrameFinalSuspended) {
			return nil, false
		}
		g.active = resumed.parent
		resumed.state = FrameDestroyPending
		resumed.header.Lifecycle = uint16(FrameDestroyPending)
		g.destroyTarget = resumed
		return resumed, true
	default:
		return nil, false
	}
}

// BeginRunG starts one runnable G and requests a done check before its first
// resume. Nested drivers are rejected by the P guards.
func BeginRunG(p *P, g *G) (Action, bool) {
	if p == nil || p.current != nil || p.inResume || p.action.Kind != ActionInvalid ||
		!ValidG(g) || g.state != GRunnable || g.active == nil || g.root == nil ||
		g.destroyTarget != nil || g.destroyRoot || g.queued || g.nextReady != nil {
		return Action{}, false
	}
	frame := g.active
	if frame.handle == nil || frame.header == nil ||
		(frame.state != FrameInitialSuspended && frame.state != FrameSuspended) {
		return Action{}, false
	}
	p.current = g
	g.state = GRunning
	return setAction(p, ActionCheckResume, frame.handle)
}

// Checked commits an llvm.coro.done result. A resumable frame must not be
// done; a destroy-pending frame must be done. The returned action is always a
// direct Resume or Destroy operation.
func Checked(p *P, g *G, action Action, done bool) (Action, bool) {
	switch action.Kind {
	case ActionCheckResume:
		if !expectedAction(p, g, action, ActionCheckResume) || done || p.inResume ||
			g.state != GRunning || g.active == nil || g.active.handle != action.Handle ||
			g.active.header == nil ||
			(g.active.state != FrameInitialSuspended && g.active.state != FrameSuspended) {
			return Action{}, false
		}
		g.active.state = FrameActive
		p.inResume = true
		return setAction(p, ActionResume, action.Handle)
	case ActionCheckDestroy:
		if !expectedAction(p, g, action, ActionCheckDestroy) || !done || p.inResume ||
			g.state != GDispatching || g.destroyTarget == nil ||
			g.destroyTarget.handle != action.Handle || g.destroyTarget.state != FrameDestroyPending {
			return Action{}, false
		}
		return setAction(p, ActionDestroy, action.Handle)
	default:
		return Action{}, false
	}
}

// Resumed commits the return from a direct llvm.coro.resume call. Coroutine
// hooks must have recorded exactly one await or completion transition while
// the frame was active.
func Resumed(p *P, g *G, action Action) (Action, bool) {
	if !expectedAction(p, g, action, ActionResume) || !p.inResume || g.state != GRunning ||
		g.active == nil || g.active.handle != action.Handle || g.active.state != FrameActive {
		return Action{}, false
	}
	p.inResume = false
	g.state = GDispatching
	resumed := g.active
	destroy, ok := dispatchPending(g, resumed)
	if !ok {
		return Action{}, false
	}
	if destroy != nil {
		// Cache root identity before llvm.coro.destroy synchronously releases
		// the combined allocation. Destroyed must never dereference it.
		g.destroyRoot = destroy == g.root
		return setAction(p, ActionCheckDestroy, destroy.handle)
	}
	g.state = GRunning
	if g.active == nil {
		return Action{}, false
	}
	return setAction(p, ActionCheckResume, g.active.handle)
}

// Destroyed commits the return from a direct llvm.coro.destroy call. The
// compiler deallocation hook must have called ReleaseFrame synchronously.
func Destroyed(p *P, g *G, action Action) (Action, bool) {
	if !expectedAction(p, g, action, ActionDestroy) || p.inResume || g.state != GDispatching ||
		g.destroyTarget != nil {
		return Action{}, false
	}
	isRoot := g.destroyRoot
	g.destroyRoot = false
	if isRoot {
		if g.active != nil || g.frames != nil {
			return Action{}, false
		}
		g.root = nil
		g.state = GDead
		p.current = nil
		p.action = Action{}
		return Action{Kind: ActionComplete}, true
	}
	g.state = GRunning
	if g.active == nil {
		return Action{}, false
	}
	return setAction(p, ActionCheckResume, g.active.handle)
}
