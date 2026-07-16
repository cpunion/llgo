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
	GWaiting
	GDead
)

// G owns the stackless frame chain for one logical Go task.
type G struct {
	magic         uint32
	preempt       uint32
	state         GState
	root          *Frame
	active        *Frame
	frames        *Frame
	pending       pendingTransition
	destroyTarget *Frame
	destroyRoot   bool
	nextReady     *G
	queued        bool
	waitToken     *WaitToken
	waitTicket    WaitTicket
	nextWait      *G
	waiting       bool
	// runP is scheduler-thread-only. An asynchronous producer requests a
	// reschedule through P's atomic gate and never reads this pointer.
	runP *P

	// spawnChild is non-nil only while this running G owns a begin/commit spawn
	// transaction. The child remains reachable through the current P's root G
	// while its initial-suspended root frame is being created.
	spawnChild  *G
	spawnParent *G
	spawnP      *P

	// taskStorage owns the separately allocated scheduler G for a spawned
	// goroutine. Static bootstrap Gs leave these fields zero. Target allocators
	// must provide scanned/root memory whenever a collector is enabled.
	taskStorage unsafe.Pointer
	taskSize    uintptr
	taskState   taskStorageState
}

const (
	// A zero G starts with preemption disabled. InitG publishes preemptIdle only
	// after every scheduler invariant has been validated and initialized; root
	// destruction returns the gate to disabled before exposing GDead.
	preemptDisabled uint32 = iota
	preemptIdle
	preemptRequested
)

const (
	scheduleIdle uint32 = iota
	scheduleRequested
	scheduleDisabled
)

func preemptAddress(g *G) *uint32 {
	return (*uint32)(unsafe.Add(unsafe.Pointer(g), unsafe.Offsetof(G{}.preempt)))
}

// P is a deterministic single-P ready queue and resume guard.
type P struct {
	// schedule is the only P field touched by asynchronous completion
	// producers. All queue and current-G fields remain scheduler-thread-only.
	schedule  uint32
	current   *G
	readyHead *G
	readyTail *G
	waitHead  *G
	waitTail  *G
	inResume  bool
	action    Action
}

// ActionKind identifies either the next compiler-owned handle operation or a
// terminal control event for the current scheduler slice. The core never
// invokes a callback or inspects a handle: the runtime adapter executes each
// handle action with a direct call to its llvm.coro wrapper, then commits the
// result through Checked, Resumed, or Destroyed.
type ActionKind uint8

const (
	ActionInvalid ActionKind = iota
	ActionCheckResume
	ActionResume
	ActionCheckDestroy
	ActionDestroy
	ActionComplete
	// ActionYield is a terminal control event for one scheduler slice. No
	// handle operation is associated with it: Resumed has already retained the
	// active frame and appended its G to the ready queue. The outer scheduler
	// must select the next runnable G before resuming this one again.
	ActionYield
	// ActionPark terminates the current scheduler slice after an exact wait
	// ticket has been linked into P's wait set. A platform event source may now
	// complete the ticket; only PollReady/NextRunnable can enqueue its G again.
	ActionPark
)

// Action is one deterministic scheduler operation or control event. Handle is
// opaque to the core and is non-nil only for a handle operation; terminal
// ActionComplete and ActionYield events carry no handle.
type Action struct {
	Kind   ActionKind
	Handle unsafe.Pointer
}

func setAction(p *P, kind ActionKind, handle unsafe.Pointer) (Action, bool) {
	if p == nil || kind == ActionInvalid || kind == ActionComplete || kind == ActionYield || kind == ActionPark || handle == nil {
		return Action{}, false
	}
	action := Action{Kind: kind, Handle: handle}
	p.action = action
	return action, true
}

func expectedAction(p *P, g *G, action Action, kind ActionKind) bool {
	return p != nil && p.current == g && ValidG(g) && action.Kind == kind && action.Handle != nil &&
		p.action == action && g.runP == p
}

// InitG initializes a zero G.
func InitG(g *G) bool {
	if g == nil || g.magic != 0 || preemptLoad(preemptAddress(g)) != preemptDisabled || g.state != GNew || g.frames != nil || g.active != nil || g.root != nil ||
		g.pending.kind != pendingNone || g.pending.from != nil || g.pending.target != nil || g.pending.wait != nil || g.pending.ticket != 0 ||
		g.destroyTarget != nil || g.destroyRoot || g.nextReady != nil || g.queued ||
		g.waitToken != nil || g.waitTicket != 0 || g.nextWait != nil || g.waiting || g.runP != nil ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		g.taskStorage != nil || g.taskSize != 0 || g.taskState != taskStorageStatic {
		return false
	}
	g.magic = gMagic
	// Publish the atomic request gate last. RequestPreempt deliberately reads no
	// other G field, so an asynchronous requester can never observe a partially
	// initialized scheduler object.
	preemptStore(preemptAddress(g), preemptIdle)
	return true
}

// RequestPreempt coalesces one asynchronous request while g's atomic preemption
// gate is enabled. It deliberately reads no scheduler-owned G fields: state and
// frame-chain transitions are non-atomic and remain confined to the scheduler
// thread. InitG enables the gate only after initialization, and terminal root
// destruction disables it so a late requester cannot resurrect residual state.
//
// A dynamically allocated G is not a stable asynchronous handle. Compiler
// safepoints and the scheduler may call RequestPreempt while they synchronously
// own that G; platform/event producers must retain the stable P instead and use
// RequestSchedule. This lifetime rule is what makes per-G task reclamation safe
// without a per-request heap reference or epoch protocol.
func RequestPreempt(g *G) bool {
	if g == nil {
		return false
	}
	gate := preemptAddress(g)
	switch preemptLoad(gate) {
	case preemptRequested:
		return true
	case preemptIdle:
		if preemptCompareAndSwap(gate, preemptIdle, preemptRequested) {
			return true
		}
		// CAS has no spurious failure. Another requester may have coalesced
		// this request, or terminal destruction may have disabled the gate.
		return preemptLoad(gate) == preemptRequested
	default:
		return false
	}
}

// PollPreempt consumes a pending request only while execution is inside the
// exact active frame of a running G. Invalid or transitional calls fail closed
// without losing a request that a later legal safepoint must observe.
func PollPreempt(g *G) bool {
	if g == nil || !ValidG(g) || g.state != GRunning || g.active == nil ||
		g.active.owner != g || g.active.handle == nil || g.active.header == nil ||
		g.active.state != FrameActive || g.active.header.G != unsafe.Pointer(g) ||
		g.active.header.SuspendReason != uint16(SuspendNone) ||
		g.active.header.Lifecycle != uint16(FrameActive) || g.pending.kind != pendingNone || g.spawnChild != nil {
		return false
	}
	requested := preemptCompareAndSwap(preemptAddress(g), preemptRequested, preemptIdle)
	// A platform completion cannot safely inspect non-atomic P.current to find
	// this G. Consume the owning P's coalesced scheduling request at the same
	// compiler safepoint instead. Consume both gates when both are set so one
	// event causes at most one yield.
	if g.runP != nil && preemptCompareAndSwap(&g.runP.schedule, scheduleRequested, scheduleIdle) {
		requested = true
	}
	return requested
}

// RequestSchedule coalesces one asynchronous request for the G currently
// executing on p, without reading any scheduler-owned P or G field. A wait
// completion producer calls CompleteWait first, then RequestSchedule, then the
// platform-specific executor/event-loop wake primitive. A running coroutine
// observes the request at PollPreempt; an idle scheduler consumes it while
// polling completed waits.
//
// p must remain alive and no logical G using it may enter its final Destroyed
// transition until every producer that can call RequestSchedule is quiescent.
// The last terminal transition atomically disables this gate: a request that
// wins that race prevents terminal success, while a request linearized after
// terminal disable fails without touching scheduler state.
func RequestSchedule(p *P) bool {
	if p == nil {
		return false
	}
	switch preemptLoad(&p.schedule) {
	case scheduleRequested:
		return true
	case scheduleIdle:
		if preemptCompareAndSwap(&p.schedule, scheduleIdle, scheduleRequested) {
			return true
		}
		return preemptLoad(&p.schedule) == scheduleRequested
	default:
		return false
	}
}

// AdoptRoot associates an initial-suspended root frame with g.
func AdoptRoot(g *G, handle unsafe.Pointer) bool {
	if !ValidG(g) || g.state != GNew || g.root != nil || g.active != nil || g.pending.kind != pendingNone ||
		g.waitToken != nil || g.waitTicket != 0 || g.nextWait != nil || g.waiting || g.runP != nil {
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
	if p == nil || !ValidG(g) || g.state != GRunnable || g.queued || g.nextReady != nil ||
		g.waiting || g.nextWait != nil || g.waitToken != nil || g.waitTicket != 0 || g.runP != nil ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil {
		return false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
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

func enqueueWait(p *P, g *G) bool {
	if p == nil || !ValidG(g) || g.state != GWaiting || g.waiting || g.nextWait != nil ||
		g.waitToken == nil || g.waitTicket == 0 || g.queued || g.nextReady != nil ||
		g.runP != nil || !validClaimedWait(g.waitToken, g.waitTicket) {
		return false
	}
	g.waiting = true
	if p.waitTail == nil {
		if p.waitHead != nil {
			g.waiting = false
			return false
		}
		p.waitHead = g
	} else {
		if p.waitHead == nil || p.waitTail.nextWait != nil {
			g.waiting = false
			return false
		}
		p.waitTail.nextWait = g
	}
	p.waitTail = g
	return true
}

func validReadyQueue(p *P) bool {
	if p == nil || (p.readyHead == nil) != (p.readyTail == nil) ||
		(p.readyTail != nil && p.readyTail.nextReady != nil) {
		return false
	}
	if p.readyHead == nil {
		return true
	}
	// Queue links are scheduler-thread-only, so one allocation-free cycle pass
	// followed by exact tail/element validation is stable for this operation.
	for slow, fast := p.readyHead, p.readyHead; fast != nil && fast.nextReady != nil; {
		slow = slow.nextReady
		fast = fast.nextReady.nextReady
		if slow == fast {
			return false
		}
	}
	var tail *G
	for g := p.readyHead; g != nil; g = g.nextReady {
		if !ValidG(g) || g.state != GRunnable || !g.queued || g.waiting || g.nextWait != nil ||
			g.waitToken != nil || g.waitTicket != 0 || g.runP != nil ||
			g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil {
			return false
		}
		tail = g
	}
	return tail == p.readyTail
}

func validWaitQueue(p *P) bool {
	if p == nil || (p.waitHead == nil) != (p.waitTail == nil) ||
		(p.waitTail != nil && p.waitTail.nextWait != nil) {
		return false
	}
	if p.waitHead == nil {
		return true
	}
	for slow, fast := p.waitHead, p.waitHead; fast != nil && fast.nextWait != nil; {
		slow = slow.nextWait
		fast = fast.nextWait.nextWait
		if slow == fast {
			return false
		}
	}
	var tail *G
	for g := p.waitHead; g != nil; g = g.nextWait {
		if !ValidG(g) || g.state != GWaiting || !g.waiting || g.waitToken == nil || g.waitTicket == 0 ||
			g.queued || g.nextReady != nil || g.runP != nil ||
			g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil || !validClaimedWait(g.waitToken, g.waitTicket) {
			return false
		}
		tail = g
	}
	return tail == p.waitTail
}

// pollReady is scheduler-thread-only. It consumes completed tickets in wait
// insertion order and appends their Gs to the ready queue. A merely armed
// ticket is a normal not-ready state; every stale/corrupt state fails closed.
func pollReady(p *P) (int, bool) {
	if p == nil || p.current != nil || p.inResume || p.action.Kind != ActionInvalid ||
		!validReadyQueue(p) || !validWaitQueue(p) {
		return 0, false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return 0, false
	}
	// There is no running G to preempt. Observing the idle scheduler is itself
	// sufficient acknowledgement of an asynchronous scheduling request.
	preemptCompareAndSwap(&p.schedule, scheduleRequested, scheduleIdle)
	promoted := 0
	var previous *G
	for g := p.waitHead; g != nil; {
		next := g.nextWait
		if !ValidG(g) || g.state != GWaiting || !g.waiting || g.waitToken == nil || g.waitTicket == 0 ||
			g.queued || g.nextReady != nil {
			return promoted, false
		}
		word := preemptLoad(&g.waitToken.word)
		if waitGeneration(word) != uint32(g.waitTicket) {
			return promoted, false
		}
		switch waitWordState(word) {
		case waitParked:
			previous = g
			g = next
			continue
		case waitParkedReady:
			if !consumeWait(g.waitToken, g.waitTicket) {
				// A platform completion only transitions Armed->Ready, so failure
				// here means another scheduler consumer or corrupted ownership.
				return promoted, false
			}
		default:
			return promoted, false
		}
		if previous == nil {
			p.waitHead = next
		} else {
			previous.nextWait = next
		}
		if p.waitTail == g {
			p.waitTail = previous
		}
		g.nextWait = nil
		g.waiting = false
		g.waitToken = nil
		g.waitTicket = 0
		g.state = GRunnable
		if !Enqueue(p, g) {
			return promoted, false
		}
		promoted++
		g = next
	}
	return promoted, true
}

// PollReady promotes every completed platform wait while the scheduler is
// idle. It never polls or calls platform code; completion producers publish by
// CompleteWait and separately wake the owning executor/event loop.
func PollReady(p *P) (int, bool) {
	return pollReady(p)
}

// HasWaiting reports whether an otherwise idle P owns parked Gs. The runtime
// adapter uses this distinction to wait for a host/platform event instead of
// misreporting an empty ready queue as program completion.
func HasWaiting(p *P) bool {
	return p != nil && p.waitHead != nil && p.waitTail != nil
}

// NextRunnable removes the next ready G. It returns ok=false when a scheduler
// operation is already in progress; an empty ready queue is (nil, true).
func NextRunnable(p *P) (g *G, ok bool) {
	if p == nil || p.current != nil || p.inResume || p.action.Kind != ActionInvalid {
		return nil, false
	}
	if preemptLoad(&p.schedule) == scheduleDisabled {
		// Preserve the ordinary drain-loop contract after the last G atomically
		// sealed the P. A disabled P is not reusable, and any residual queue is
		// corruption rather than runnable work.
		return nil, validReadyQueue(p) && validWaitQueue(p) && p.readyHead == nil && p.waitHead == nil
	}
	if _, ok := pollReady(p); !ok {
		return nil, false
	}
	return dequeue(p), true
}

func dispatchPending(g *G, resumed *Frame) (destroy *Frame, yielded bool, ok bool) {
	pending := g.pending
	g.pending = pendingTransition{}
	if pending.from != resumed {
		return nil, false, false
	}
	switch pending.kind {
	case pendingAwait:
		child := pending.target
		if child == nil || child.parent != resumed || resumed.header == nil || child.header == nil ||
			pending.wait != nil || pending.ticket != 0 ||
			resumed.header.Lifecycle != uint16(FrameSuspended) ||
			child.header.Lifecycle != uint16(FrameInitialSuspended) {
			return nil, false, false
		}
		resumed.state = FrameSuspended
		g.active = child
		return nil, false, true
	case pendingComplete:
		if pending.target != nil || pending.wait != nil || pending.ticket != 0 || resumed.header == nil ||
			resumed.header.Lifecycle != uint16(FrameFinalSuspended) {
			return nil, false, false
		}
		g.active = resumed.parent
		resumed.state = FrameDestroyPending
		resumed.header.Lifecycle = uint16(FrameDestroyPending)
		g.destroyTarget = resumed
		return resumed, false, true
	case pendingYield:
		if pending.target != nil || pending.wait != nil || pending.ticket != 0 || resumed.header == nil ||
			resumed.header.SuspendReason != uint16(SuspendYield) ||
			resumed.header.Lifecycle != uint16(FrameSuspended) {
			return nil, false, false
		}
		resumed.state = FrameSuspended
		return nil, true, true
	case pendingPark:
		if pending.target != nil || resumed.header == nil ||
			resumed.header.SuspendReason != uint16(SuspendPark) ||
			resumed.header.Lifecycle != uint16(FrameSuspended) ||
			!validClaimedWait(pending.wait, pending.ticket) || g.waitToken != nil || g.waitTicket != 0 || g.waiting || g.nextWait != nil {
			return nil, false, false
		}
		resumed.state = FrameSuspended
		g.waitToken = pending.wait
		g.waitTicket = pending.ticket
		return nil, false, true
	default:
		return nil, false, false
	}
}

// BeginRunG starts one runnable G and requests a done check before its first
// resume. Nested drivers are rejected by the P guards.
func BeginRunG(p *P, g *G) (Action, bool) {
	if p == nil || p.current != nil || p.inResume || p.action.Kind != ActionInvalid ||
		!ValidG(g) || g.state != GRunnable || g.active == nil || g.root == nil ||
		g.destroyTarget != nil || g.destroyRoot || g.queued || g.nextReady != nil ||
		g.waitToken != nil || g.waitTicket != 0 || g.nextWait != nil || g.waiting || g.runP != nil ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil {
		return Action{}, false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return Action{}, false
	}
	frame := g.active
	if frame.handle == nil || frame.header == nil ||
		(frame.state != FrameInitialSuspended && frame.state != FrameSuspended) {
		return Action{}, false
	}
	if p.readyHead != nil && !RequestPreempt(g) {
		return Action{}, false
	}
	p.current = g
	g.state = GRunning
	g.runP = p
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
	destroy, yielded, ok := dispatchPending(g, resumed)
	if !ok {
		return Action{}, false
	}
	if yielded {
		// BeginRunG guarantees that a running G has no ready-queue link. Check
		// the remaining queue invariants before committing any state so a
		// corrupted queue cannot leave a half-requeued G behind.
		if g.queued || g.nextReady != nil || (p.readyHead == nil) != (p.readyTail == nil) ||
			(p.readyTail != nil && p.readyTail.nextReady != nil) {
			return Action{}, false
		}
		g.state = GRunnable
		g.runP = nil
		p.current = nil
		p.action = Action{}
		if !Enqueue(p, g) {
			return Action{}, false
		}
		return Action{Kind: ActionYield}, true
	}
	if g.waitToken != nil {
		if g.queued || g.nextReady != nil || (p.waitHead == nil) != (p.waitTail == nil) ||
			(p.waitTail != nil && p.waitTail.nextWait != nil) {
			return Action{}, false
		}
		g.state = GWaiting
		g.runP = nil
		p.current = nil
		p.action = Action{}
		if !enqueueWait(p, g) {
			return Action{}, false
		}
		return Action{Kind: ActionPark}, true
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
	if isRoot {
		if g.active != nil || g.frames != nil || !validReadyQueue(p) || !validWaitQueue(p) {
			return Action{}, false
		}
		schedule := preemptLoad(&p.schedule)
		if schedule != scheduleIdle && schedule != scheduleRequested {
			return Action{}, false
		}
		// Disable only when this root is the last G owned by the P. Otherwise
		// ready/waiting peers still need the gate. CAS makes terminal success and
		// a late asynchronous producer request one exact total order.
		if p.readyHead == nil && p.waitHead == nil &&
			!preemptCompareAndSwap(&p.schedule, scheduleIdle, scheduleDisabled) {
			return Action{}, false
		}
		g.destroyRoot = false
		g.root = nil
		// Disable requests before publishing the terminal scheduler state. A
		// requester that observed idle before this store can only CAS against the
		// now-disabled gate and fail; an earlier successful CAS is overwritten.
		preemptStore(preemptAddress(g), preemptDisabled)
		g.state = GDead
		g.runP = nil
		p.current = nil
		p.action = Action{}
		return Action{Kind: ActionComplete}, true
	}
	g.destroyRoot = false
	g.state = GRunning
	if g.active == nil {
		return Action{}, false
	}
	return setAction(p, ActionCheckResume, g.active.handle)
}

// AcknowledgeTerminalSchedule classifies and consumes the one non-corruption
// failure of Destroyed: an asynchronous RequestSchedule won the final
// idle-to-disabled race after the last frame had already been destroyed. The
// runtime adapter may retry the same ActionDestroy without invoking
// llvm.coro.destroy again. Any queue, action, or G-state mismatch fails closed.
func AcknowledgeTerminalSchedule(p *P, g *G, action Action) bool {
	return expectedAction(p, g, action, ActionDestroy) && !p.inResume &&
		g.state == GDispatching && g.destroyTarget == nil && g.destroyRoot &&
		g.active == nil && g.frames == nil && p.readyHead == nil && p.readyTail == nil &&
		p.waitHead == nil && p.waitTail == nil && validReadyQueue(p) && validWaitQueue(p) &&
		preemptCompareAndSwap(&p.schedule, scheduleRequested, scheduleIdle)
}

// TerminalG reports whether a scheduler run completely consumed g and left p
// idle. This is a deliberately strict terminal-state check for program
// startup: a dead G state alone is insufficient if any frame, transition,
// ready-queue link, destruction bookkeeping, or P operation survived.
func TerminalG(p *P, g *G) bool {
	return p != nil && p.current == nil && p.readyHead == nil && p.readyTail == nil &&
		p.waitHead == nil && p.waitTail == nil &&
		preemptLoad(&p.schedule) == scheduleDisabled && !p.inResume && p.action.Kind == ActionInvalid && p.action.Handle == nil &&
		ValidG(g) && preemptLoad(preemptAddress(g)) == preemptDisabled && g.state == GDead && g.root == nil && g.active == nil && g.frames == nil &&
		g.pending.kind == pendingNone && g.pending.from == nil && g.pending.target == nil && g.pending.wait == nil && g.pending.ticket == 0 &&
		g.destroyTarget == nil && !g.destroyRoot && g.nextReady == nil && !g.queued &&
		g.waitToken == nil && g.waitTicket == 0 && g.nextWait == nil && !g.waiting && g.runP == nil &&
		g.spawnChild == nil && g.spawnParent == nil && g.spawnP == nil && validTerminalTaskStorage(g)
}
