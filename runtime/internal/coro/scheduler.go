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
	GCanceling
	GWaiting
	GDead
	// GPanicking destroys suspended-await ancestors deepest-to-root after the
	// active final-suspended panic frame has passed its normal done check.
	GPanicking
)

// G owns the stackless frame chain for one logical Go task.
type G struct {
	magic   uint32
	preempt uint32
	state   GState
	// taskControlLeases occupies existing pointer-alignment padding. It is
	// owner-P-only and counts only explicitly exported task endpoints, so an
	// ordinary G pays no size or registry cost. Terminal storage cannot be
	// reclaimed until the last endpoint has completed its strong close.
	taskControlLeases uint8
	// runAction occupies the remaining pointer-alignment padding. A non-zero
	// value means that a bounded executor returned one physical handle action
	// to the ready tail before starting it. Only check-resume, check-destroy,
	// and direct panic-destroy continuations may cross that stable boundary.
	runAction ActionKind
	// transferState reuses the last byte before pointer alignment. Ordinary Gs
	// remain zero; a durable runnable-transfer slot marks it non-zero while no P
	// queue owns the continuation. This prevents a stray Enqueue from creating
	// a second scheduler owner without increasing G's native or wasm32 size.
	transferState runnableTransferGState
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
	// park is the common multi-source logical wait cell. The legacy one-token
	// fields above remain during migration; new sources must target park. It
	// also owns the one-byte task stop token so park commit cannot forget it.
	park ParkState
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

	// panicRecord is task-local. It must never be discovered through TLS or a
	// process-global current-G slot. panicUnwind is scheduler-thread-only and is
	// set only after the published active frame returns from llvm.coro.resume.
	panicRecord PanicRecord
	panicUnwind bool
}

const (
	// These values occupy only G.preempt's low preemptStateBits. A zero G starts
	// with preemption disabled. InitG publishes preemptIdle only after every
	// scheduler invariant has been validated and initialized; root destruction
	// returns the state bits to disabled at depth zero before exposing GDead.
	preemptDisabled uint32 = iota
	preemptIdle
	preemptRequested
)

const (
	scheduleIdle uint32 = iota
	scheduleRequested
	scheduleStopping
	scheduleDisabled
)

const (
	executorModeUnbound uint32 = iota
	executorModeBound
)

func preemptAddress(g *G) *uint32 {
	return (*uint32)(unsafe.Add(unsafe.Pointer(g), unsafe.Offsetof(G{}.preempt)))
}

// P is a deterministic single-P ready queue and resume guard.
type P struct {
	// schedule and executorMode are the only P fields that legacy asynchronous
	// requesters may inspect. Platform completion shims never retain P: a bound
	// executor makes RequestSchedule fail and uses its stable ExecutorHandle.
	schedule     uint32
	executorMode uint32
	// executor is scheduler-thread-only and is published before executorMode.
	executor *ExecutorDriver
	// channelSource is the canonical Channel commit-domain catalog for this P.
	// A second source cannot self-certify that a frame-local SelectClaim is no
	// longer referenced by the first source and therefore may not bind beside
	// it. P is not a frozen wire layout; C1 may evolve this pointer into a
	// canonical sharded catalog while preserving whole-domain Reset proof.
	channelSource *ChannelOperationSource

	current   *G
	readyHead *G
	readyTail *G
	// waitHead/waitTail remain the legacy WaitToken queue. V2 waits use
	// frame-local WaitSetRecords so an affected task can be removed in O(1)
	// without adding a permanent prev link to every G.
	waitHead         *G
	waitTail         *G
	parkWaitHead     *WaitSetRecord
	parkWaitTail     *WaitSetRecord
	affectedWaitHead *WaitSetRecord
	affectedWaitTail *WaitSetRecord
	inResume         bool
	action           Action
	// runDecision is populated immediately before ActionResume and must be
	// consumed by the compiler-generated resume prologue before control can
	// publish another scheduler transition. It scales with P, not G.
	runDecision      RunDecision
	runDecisionTaken bool

	// servicePreemptBudget is scheduler-thread-only. A non-zero value belongs
	// to current's run slice and counts legal compiler safepoints until the
	// scheduler must regain ownership to service runnable work and every bound
	// event source. Every successful BeginRunG loads one full quantum; an idle
	// P keeps the exact zero value.
	servicePreemptBudget uint32
}

// servicePreemptPollBudget bounds how many legal compiler safepoints one G may
// cross before returning ownership to the scheduler service loop. This is a
// deterministic safepoint budget rather than a wall-clock quantum: the yield
// lets the executor drain all durable event sources, publish ready work, and
// make the next scheduling decision without depending on a particular source.
const servicePreemptPollBudget uint32 = 64

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
	// ActionCancelDestroy asks the runtime shutdown adapter to call
	// llvm.coro.destroy directly on one suspended frame. It never performs a
	// coro.done check and never resumes an ancestor frame.
	ActionCancelDestroy
	// ActionCancelComplete transfers one fully destroyed spawned G to the task
	// storage reclaimer.
	ActionCancelComplete
	// ActionPanicDestroy directly destroys one suspended-await ancestor after
	// the active panic frame has already gone through CheckDestroy/Destroy.
	// It must never be preceded by coro.done or followed by coro.resume.
	ActionPanicDestroy
	// ActionPanicComplete exposes a stable task-local PanicRecord to the runtime
	// adapter after every frame has been destroyed deepest-to-root.
	ActionPanicComplete
	// ActionTerminalExecutorClose transfers control to the target adapter after
	// the last LLVM handle has already been destroyed and the bound executor gate
	// has been sealed. It carries no handle: the adapter must strong-join the
	// target ingress shim and call ConfirmTerminalExecutorClose, which performs
	// the final source scan, unbinds the executor, and commits terminal state
	// without exposing the destroyed handle again.
	ActionTerminalExecutorClose
	// ActionCommitDestroy is a handle-free post-destroy receipt. The bounded
	// runner publishes it only after ReleaseFrame removed the final root and
	// every pointer to the freed LLVM handle was discarded. Terminal close and
	// the legacy schedule-disable race are separate, explicitly unbounded
	// compatibility boundaries.
	ActionCommitDestroy
)

// Action is one deterministic scheduler operation or control event. Handle is
// opaque to the core and is non-nil only for a handle operation; terminal
// control events carry no handle.
type Action struct {
	Kind   ActionKind
	Handle unsafe.Pointer
}

func setAction(p *P, kind ActionKind, handle unsafe.Pointer) (Action, bool) {
	if p == nil || kind == ActionInvalid || kind == ActionComplete || kind == ActionYield || kind == ActionPark ||
		kind == ActionCancelComplete || kind == ActionPanicComplete || kind == ActionTerminalExecutorClose || handle == nil {
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
	if g == nil || g.magic != 0 || !gPreemptStateAtDepthZero(g, preemptDisabled) || g.state != GNew || g.taskControlLeases != 0 ||
		g.runAction != ActionInvalid || g.transferState != runnableTransferGIdle ||
		g.frames != nil || g.active != nil || g.root != nil ||
		g.pending.kind != pendingNone || g.pending.from != nil || g.pending.target != nil || g.pending.wait != nil || g.pending.ticket != 0 ||
		g.destroyTarget != nil || g.destroyRoot || g.nextReady != nil || g.queued ||
		g.waitToken != nil || g.waitTicket != 0 || g.nextWait != nil || g.waiting || g.runP != nil ||
		g.park != (ParkState{}) ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		g.taskStorage != nil || g.taskSize != 0 || g.taskState != taskStorageStatic ||
		!emptyPanicRecord(&g.panicRecord) || g.panicUnwind {
		return false
	}
	g.magic = gMagic
	// Publish the atomic request gate last. RequestPreempt deliberately reads no
	// other G field, so an asynchronous requester can never observe a partially
	// initialized scheduler object.
	return compareAndSwapGPreemptStateAtDepthZero(g, preemptDisabled, preemptIdle)
}

// RequestPreempt coalesces one asynchronous request while g's atomic preemption
// gate is enabled. It deliberately reads no scheduler-owned G fields: state and
// frame-chain transitions are non-atomic and remain confined to the scheduler
// thread. InitG enables the gate only after initialization, and terminal root
// destruction disables it so a late requester cannot resurrect residual state.
//
// A dynamically allocated G is not a stable asynchronous handle. Compiler
// safepoints and the scheduler may call RequestPreempt while they synchronously
// own that G. Platform wait callbacks retain only WaitRegistrationHandle;
// they first publish that durable handle and then request a stable
// ExecutorHandle. The scheduler-side driver resolves P only after it owns the
// executor again. This lifetime rule makes per-G task reclamation safe without
// a per-request heap reference or epoch protocol.
func RequestPreempt(g *G) bool {
	if g == nil {
		return false
	}
	gate := preemptAddress(g)
	for {
		word := preemptLoad(gate)
		switch preemptWordState(word) {
		case preemptRequested:
			return true
		case preemptIdle:
			requested := word&^preemptStateMask | preemptRequested
			if preemptCompareAndSwap(gate, word, requested) {
				return true
			}
			// CAS has no spurious failure. A depth transition or another
			// requester may have won; retry without dropping either portion.
		case preemptDisabled:
			return false
		default:
			return false
		}
	}
}

// PollPreempt consumes a pending request only while execution is inside the
// exact active frame of a running G. Invalid or transitional calls fail closed
// without losing a request that a later legal safepoint must observe.
func PollPreempt(g *G) bool {
	if g == nil || preemptWordDepth(loadGPreempt(g)) != 0 {
		return false
	}
	requested, valid := pollPreemptDepthZero(g)
	return valid && requested
}

// RequestSchedule is the legacy/internal P request gate. It coalesces one
// request without reading scheduler-owned queue or current-G fields, but it has
// no retained platform doorbell and is therefore rejected after BindExecutor.
// Bound platform callbacks must publish their durable source and call
// ExecutorRegistry.Request through a POD ExecutorHandle instead.
//
// An unbound p must remain alive until every internal source that can call this
// function is quiescent.
// The last terminal transition atomically disables this gate: a request that
// wins that race prevents terminal success, while a request linearized after
// terminal disable fails without touching scheduler state.
func RequestSchedule(p *P) bool {
	if p == nil || preemptLoad(&p.executorMode) != executorModeUnbound {
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
		g.runAction != ActionInvalid || g.transferState != runnableTransferGIdle ||
		!gPreemptStateAtDepthZero(g, preemptIdle) ||
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
		!gPreemptEnabledAtDepthZero(g) ||
		g.waiting || g.nextWait != nil || g.waitToken != nil || g.waitTicket != 0 || g.runP != nil ||
		!validRunnableParkState(&g.park) ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		g.transferState != runnableTransferGIdle || !validRunnableRunAction(g) {
		return false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return false
	}
	appendReadyUnchecked(p, g)
	return true
}

func appendReadyUnchecked(p *P, g *G) {
	g.queued = true
	if p.readyTail == nil {
		p.readyHead = g
	} else {
		p.readyTail.nextReady = g
	}
	p.readyTail = g
}

func prependReadyUnchecked(p *P, g *G) {
	g.queued = true
	g.nextReady = p.readyHead
	p.readyHead = g
	if p.readyTail == nil {
		p.readyTail = g
	}
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

func appendWaiter(p *P, g *G) bool {
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

func enqueueWait(p *P, g *G) bool {
	if p == nil || !ValidG(g) || g.state != GWaiting || g.waiting || g.nextWait != nil ||
		g.waitToken == nil || g.waitTicket == 0 || g.queued || g.nextReady != nil ||
		g.runP != nil || g.transferState != runnableTransferGIdle || !gPreemptEnabledAtDepthZero(g) ||
		!validClaimedWait(g.waitToken, g.waitTicket) ||
		!releasableParkState(&g.park) || g.park.taskCancelKind != TaskCancelNone {
		return false
	}
	return appendWaiter(p, g)
}

func enqueueParkSet(p *P, g *G) bool {
	if p == nil || !ValidG(g) || g.active == nil || g.active.parkWait == nil ||
		g.state != GWaiting || g.waiting || g.nextWait != nil || g.waitToken != nil || g.waitTicket != 0 ||
		g.queued || g.nextReady != nil || g.runP != nil || g.transferState != runnableTransferGIdle ||
		!gPreemptEnabledAtDepthZero(g) ||
		!validParkState(&g.park) || g.park.phase != parkParked {
		return false
	}
	return activateWaitSetRecord(p, g, g.active.parkWait)
}

func validRunnableParkState(state *ParkState) bool {
	if !validParkState(state) {
		return false
	}
	return state.phase == parkIdle || state.phase == parkConsumed || state.phase == parkDelivered || state.phase == parkReady
}

// validRunnableRunAction distinguishes an ordinary runnable suspension from a
// bounded-runner continuation. It is deliberately local: a ready-queue audit
// may validate each element, while the production dequeue path only validates
// the selected G and the queue header.
func validRunnableRunAction(g *G) bool {
	if g == nil {
		return false
	}
	switch g.runAction {
	case ActionInvalid:
		return g.destroyTarget == nil && !g.destroyRoot
	case ActionCheckResume:
		return g.destroyTarget == nil && !g.destroyRoot && !g.panicUnwind &&
			g.active != nil && g.active.handle != nil && g.active.header != nil &&
			(g.active.state == FrameInitialSuspended || g.active.state == FrameSuspended)
	case ActionCheckDestroy:
		return (!g.panicUnwind || publishedPanicRecord(&g.panicRecord)) &&
			g.destroyTarget != nil && g.destroyTarget.handle != nil &&
			g.destroyTarget.state == FrameDestroyPending
	case ActionPanicDestroy:
		return g.panicUnwind && publishedPanicRecord(&g.panicRecord) &&
			g.destroyTarget != nil && g.destroyTarget.handle != nil &&
			g.destroyTarget.state == FrameDestroyPending
	default:
		return false
	}
}

func validLegacyWaitingG(g *G) bool {
	return ValidG(g) && gPreemptEnabledAtDepthZero(g) && g.waitToken != nil && g.waitTicket != 0 &&
		validClaimedWait(g.waitToken, g.waitTicket) && releasableParkState(&g.park) &&
		g.park.taskCancelKind == TaskCancelNone
}

func validParkSetWaitingG(g *G) bool {
	if !ValidG(g) || !gPreemptEnabledAtDepthZero(g) ||
		g.waitToken != nil || g.waitTicket != 0 || !validParkState(&g.park) {
		return false
	}
	return g.park.phase == parkParked || g.park.phase == parkDetaching || g.park.phase == parkReady
}

func validReadyQueueHeader(p *P) bool {
	return p != nil && (p.readyHead == nil) == (p.readyTail == nil) &&
		(p.readyTail == nil || p.readyTail.nextReady == nil)
}

func validReadyQueue(p *P) bool {
	if !validReadyQueueHeader(p) {
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
			!gPreemptEnabledAtDepthZero(g) ||
			g.waitToken != nil || g.waitTicket != 0 || g.runP != nil ||
			g.transferState != runnableTransferGIdle ||
			(g.active == nil && g.runAction != ActionCheckDestroy && g.runAction != ActionPanicDestroy) ||
			(g.active != nil && g.active.parkWait != nil) ||
			!validRunnableParkState(&g.park) ||
			g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil || !validRunnableRunAction(g) {
			return false
		}
		tail = g
	}
	return tail == p.readyTail
}

func validWaitQueueHeader(p *P) bool {
	return p != nil && (p.waitHead == nil) == (p.waitTail == nil) &&
		(p.waitTail == nil || p.waitTail.nextWait == nil)
}

func validWaitQueue(p *P) bool {
	if !validWaitQueueHeader(p) {
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
		if !ValidG(g) || g.state != GWaiting || !g.waiting || g.queued || g.nextReady != nil || g.runP != nil ||
			!gPreemptEnabledAtDepthZero(g) ||
			g.transferState != runnableTransferGIdle ||
			g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
			!validLegacyWaitingG(g) || g.active == nil || g.active.parkWait != nil {
			return false
		}
		tail = g
	}
	return tail == p.waitTail
}

func validSchedulerWaitQueues(p *P) bool {
	return validWaitQueue(p) && validParkWaitQueue(p)
}

func emptySchedulerWaitQueues(p *P) bool {
	return p != nil && p.waitHead == nil && p.waitTail == nil &&
		p.parkWaitHead == nil && p.parkWaitTail == nil &&
		p.affectedWaitHead == nil && p.affectedWaitTail == nil
}

// pollReady is scheduler-thread-only. Legacy WaitTokens retain their migration
// scan. V2 parks are reached only through P's affected queue, so neither their
// logical resolution nor ParkReady promotion walks unrelated waiting Gs.
func pollReady(p *P) (int, bool) {
	if p == nil || p.current != nil || p.inResume || p.action.Kind != ActionInvalid ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken || !validReadyQueueHeader(p) || !validWaitQueue(p) ||
		!validParkWaitQueueHeader(p) || !validAffectedWaitQueueHeader(p) {
		return 0, false
	}
	schedule := preemptLoad(&p.schedule)
	mode := preemptLoad(&p.executorMode)
	if mode == executorModeBound {
		if schedule != scheduleIdle {
			return 0, false
		}
	} else if mode != executorModeUnbound || (schedule != scheduleIdle && schedule != scheduleRequested) {
		return 0, false
	}
	if mode == executorModeUnbound {
		// There is no running G to preempt. Observing the idle scheduler is
		// sufficient acknowledgement of the legacy/internal scheduling gate.
		preemptCompareAndSwap(&p.schedule, scheduleRequested, scheduleIdle)
	}
	var cursor publishedEpochResolveCursor
	promoted := 0
	for {
		step, advanced := resolvePublishedEpochStep(nil, p, &cursor)
		if !advanced {
			return promoted, false
		}
		promoted += step.promoted
		if step.complete {
			return promoted, true
		}
	}
}

// PollReady promotes every completed or safely canceled platform wait while
// the scheduler is idle. For a bound P it first runs the target-neutral
// ExecutorDriver drain/ack/recheck transaction. It never calls target code.
func PollReady(p *P) (int, bool) {
	if p != nil && preemptLoad(&p.executorMode) == executorModeBound {
		_, promoted, ok := PollExecutor(p.executor)
		return promoted, ok
	}
	return pollReady(p)
}

// PollReadyAt is the timer-aware scheduler poll. A bound timer driver requires
// the caller's current monotonic nanoseconds; an unbound P has no target timer
// table and continues to use the legacy internal poll.
func PollReadyAt(p *P, now int64) (int, bool) {
	if p == nil || now < 0 {
		return 0, false
	}
	if preemptLoad(&p.executorMode) == executorModeBound {
		_, _, promoted, ok := PollExecutorAt(p.executor, now)
		return promoted, ok
	}
	return pollReady(p)
}

// HasWaiting reports whether an otherwise idle P owns parked Gs. The runtime
// adapter uses this distinction to wait for a host/platform event instead of
// misreporting an empty ready queue as program completion.
func HasWaiting(p *P) bool {
	return p != nil && (p.waitHead != nil && p.waitTail != nil ||
		p.parkWaitHead != nil && p.parkWaitTail != nil)
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
		return nil, validReadyQueue(p) && validSchedulerWaitQueues(p) && p.readyHead == nil && emptySchedulerWaitQueues(p)
	}
	if _, ok := PollReady(p); !ok {
		return nil, false
	}
	return dequeue(p), true
}

// NextRunnableAt is the timer-aware dequeue path. It prevents a runnable loop
// from bypassing due timers and rejects a timer-bound executor if the caller
// omits or supplies an invalid monotonic timestamp.
func NextRunnableAt(p *P, now int64) (g *G, ok bool) {
	if p == nil || now < 0 || p.current != nil || p.inResume || p.action.Kind != ActionInvalid {
		return nil, false
	}
	if preemptLoad(&p.schedule) == scheduleDisabled {
		return nil, validReadyQueue(p) && validSchedulerWaitQueues(p) && p.readyHead == nil && emptySchedulerWaitQueues(p)
	}
	if _, ok := PollReadyAt(p, now); !ok {
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
			resumed.header.Lifecycle != uint16(FrameFinalSuspended) || !completionMatchesTerminalFrame(resumed) {
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
			!validClaimedWait(pending.wait, pending.ticket) || resumed.parkWait != nil ||
			g.waitToken != nil || g.waitTicket != 0 || g.waiting || g.nextWait != nil {
			return nil, false, false
		}
		resumed.state = FrameSuspended
		g.waitToken = pending.wait
		g.waitTicket = pending.ticket
		return nil, false, true
	case pendingParkSet:
		if pending.target != nil || pending.wait != nil || pending.ticket != 0 || resumed.header == nil ||
			resumed.header.SuspendReason != uint16(SuspendPark) ||
			resumed.header.Lifecycle != uint16(FrameSuspended) ||
			g.waitToken != nil || g.waitTicket != 0 || g.waiting || g.nextWait != nil ||
			!validParkState(&g.park) || g.park.phase != parkParked ||
			!validCommittedWaitSetRecord(resumed.parkWait, g, resumed) {
			return nil, false, false
		}
		resumed.state = FrameSuspended
		return nil, false, true
	case pendingPanic:
		if pending.target != nil || pending.wait != nil || pending.ticket != 0 || resumed.header == nil ||
			resumed.header.SuspendReason != uint16(SuspendPanic) ||
			resumed.header.Lifecycle != uint16(FrameFinalSuspended) ||
			g.panicUnwind || !publishedPanicRecord(&g.panicRecord) {
			return nil, false, false
		}
		g.active = resumed.parent
		resumed.state = FrameDestroyPending
		resumed.header.Lifecycle = uint16(FrameDestroyPending)
		g.destroyTarget = resumed
		g.panicUnwind = true
		return resumed, false, true
	default:
		return nil, false, false
	}
}

func beginRunAction(g *G) (kind ActionKind, handle unsafe.Pointer, state GState, ok bool) {
	if g == nil || !validRunnableRunAction(g) {
		return ActionInvalid, nil, GNew, false
	}
	switch g.runAction {
	case ActionInvalid:
		if g.active == nil || g.active.handle == nil || g.active.header == nil ||
			(g.active.state != FrameInitialSuspended && g.active.state != FrameSuspended) {
			return ActionInvalid, nil, GNew, false
		}
		return ActionCheckResume, g.active.handle, GRunning, true
	case ActionCheckResume:
		return ActionCheckResume, g.active.handle, GRunning, true
	case ActionCheckDestroy:
		return ActionCheckDestroy, g.destroyTarget.handle, GDispatching, true
	case ActionPanicDestroy:
		return ActionPanicDestroy, g.destroyTarget.handle, GPanicking, true
	default:
		return ActionInvalid, nil, GNew, false
	}
}

// queuedDestroyBlockedByTaskCancellation protects the last suspended frame
// until compiler cleanup lowering can consume a sticky task stop. CheckResume
// remains runnable because its synchronous continuation is the cleanup entry.
//
// Once ActionPanicDestroy passes this gate BeginRunG changes the task to
// GPanicking. Owner cancellation APIs do not accept that state, source service
// requires an idle P, and the runner executes the returned action without a
// host boundary. There is therefore no later cancellation injection point on
// the direct panic-destroy path.
func queuedDestroyBlockedByTaskCancellation(g *G) bool {
	if g == nil || g.park.taskCancelPhase != taskCancelRequested {
		return false
	}
	return g.runAction == ActionCheckDestroy || g.runAction == ActionPanicDestroy
}

// BeginRunG starts one runnable G. An ordinary suspension starts with a done
// check; a bounded-runner continuation restores the exact stable action that
// was placed at the ready tail. Nested drivers are rejected by the P guards.
func BeginRunG(p *P, g *G) (Action, bool) {
	if p == nil || p.current != nil || p.inResume || p.action.Kind != ActionInvalid ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken ||
		!ValidG(g) || g.state != GRunnable || g.root == nil ||
		!gPreemptEnabledAtDepthZero(g) ||
		g.queued || g.nextReady != nil ||
		g.waitToken != nil || g.waitTicket != 0 || g.nextWait != nil || g.waiting || g.runP != nil ||
		!validRunnableParkState(&g.park) ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		g.transferState != runnableTransferGIdle || p.servicePreemptBudget != 0 {
		return Action{}, false
	}
	if queuedDestroyBlockedByTaskCancellation(g) {
		return Action{}, false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return Action{}, false
	}
	kind, handle, state, valid := beginRunAction(g)
	if !valid || handle == nil {
		return Action{}, false
	}
	if p.readyHead != nil && !RequestPreempt(g) {
		return Action{}, false
	}
	p.current = g
	p.servicePreemptBudget = servicePreemptPollBudget
	g.state = state
	g.runP = p
	g.runAction = ActionInvalid
	action, ok := setAction(p, kind, handle)
	if !ok {
		return Action{}, false
	}
	return action, true
}

type executorRunQueuePlacement uint8

const (
	executorRunQueueTail executorRunQueuePlacement = iota
	executorRunQueueCommandBootstrapDirectChildHandoff
	executorRunQueueCommandRootDestroy
)

// pauseExecutorRunAction moves one stable post-operation continuation back to
// the ready queue. It is called only after the runtime adapter completed a
// whole physical reduction, so ActionResume and ActionDestroy can never be
// retained across a host boundary. Ordinary continuations retain FIFO order;
// command/bootstrap control placements are selected only by their validating
// exported commit boundaries.
func pauseExecutorRunAction(p *P, g *G, action Action, placement executorRunQueuePlacement) bool {
	if p == nil || g == nil || p.current != g || g.runP != p || p.inResume ||
		p.action != action || action.Handle == nil || g.runAction != ActionInvalid ||
		p.runDecision != (RunDecision{}) || p.runDecisionTaken || p.servicePreemptBudget == 0 ||
		g.queued || g.nextReady != nil || g.waiting || g.nextWait != nil ||
		g.waitToken != nil || g.waitTicket != 0 || !validRunnableParkState(&g.park) ||
		g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil ||
		g.transferState != runnableTransferGIdle || !gPreemptEnabledAtDepthZero(g) ||
		!validReadyQueueHeader(p) || placement > executorRunQueueCommandRootDestroy {
		return false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return false
	}
	switch action.Kind {
	case ActionCheckResume:
		if placement == executorRunQueueCommandRootDestroy {
			return false
		}
		if g.state != GRunning || g.destroyTarget != nil || g.destroyRoot || g.active == nil ||
			g.active.handle != action.Handle || g.active.header == nil ||
			(g.active.state != FrameInitialSuspended && g.active.state != FrameSuspended) {
			return false
		}
	case ActionCheckDestroy:
		if g.state != GDispatching || g.panicUnwind && !publishedPanicRecord(&g.panicRecord) || g.destroyTarget == nil ||
			g.destroyTarget.handle != action.Handle || g.destroyTarget.state != FrameDestroyPending {
			return false
		}
	case ActionPanicDestroy:
		if placement != executorRunQueueTail {
			return false
		}
		if g.state != GPanicking || !g.panicUnwind || !publishedPanicRecord(&g.panicRecord) ||
			g.destroyTarget == nil || g.destroyTarget.handle != action.Handle ||
			g.destroyTarget.state != FrameDestroyPending {
			return false
		}
	default:
		return false
	}

	g.runAction = action.Kind
	g.state = GRunnable
	g.runP = nil
	p.current = nil
	p.servicePreemptBudget = 0
	p.action = Action{}
	if placement != executorRunQueueTail {
		// A frozen command-bootstrap direct CoroRoot step retains the same
		// logical G only for its one child destroy and exact-root resume. After
		// normal-main return, the final root destroy has its separate placement.
		// Every physical operation remains a separately charged later reduction.
		prependReadyUnchecked(p, g)
	} else {
		appendReadyUnchecked(p, g)
	}
	return true
}

// Checked commits an llvm.coro.done result. A resumable frame must not be
// done; a destroy-pending frame must be done. The returned action is always a
// direct Resume or Destroy operation.
func Checked(p *P, g *G, action Action, done bool) (Action, bool) {
	switch action.Kind {
	case ActionCheckResume:
		if !expectedAction(p, g, action, ActionCheckResume) || done || p.inResume ||
			p.runDecision != (RunDecision{}) || p.runDecisionTaken ||
			g.state != GRunning || g.active == nil || g.active.handle != action.Handle ||
			g.active.header == nil ||
			(g.active.state != FrameInitialSuspended && g.active.state != FrameSuspended) {
			return Action{}, false
		}
		if !prepareRunDecision(p, g) {
			return Action{}, false
		}
		g.active.state = FrameActive
		p.inResume = true
		return setAction(p, ActionResume, action.Handle)
	case ActionCheckDestroy:
		if !expectedAction(p, g, action, ActionCheckDestroy) || !done || p.inResume ||
			g.state != GDispatching || g.destroyTarget == nil ||
			g.destroyTarget.handle != action.Handle || g.destroyTarget.state != FrameDestroyPending ||
			g.park.taskCancelPhase == taskCancelRequested {
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
	if !resumeGateTaken(g) || p != g.runP || action != p.action ||
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
	p.runDecisionTaken = false
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
		p.servicePreemptBudget = 0
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
		p.servicePreemptBudget = 0
		p.action = Action{}
		if !enqueueWait(p, g) {
			return Action{}, false
		}
		return Action{Kind: ActionPark}, true
	}
	if g.park.phase == parkParked {
		if g.queued || g.nextReady != nil || !validParkWaitQueueHeader(p) || !validAffectedWaitQueueHeader(p) {
			return Action{}, false
		}
		g.state = GWaiting
		g.runP = nil
		p.current = nil
		p.servicePreemptBudget = 0
		p.action = Action{}
		if !enqueueParkSet(p, g) {
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
	if g.panicUnwind {
		return commitInitialPanicDestroyed(p, g, isRoot)
	}
	if isRoot {
		return commitRootDestroyedCompatibility(p, g, ActionDestroy)
	}
	g.destroyRoot = false
	g.state = GRunning
	if g.active == nil {
		return Action{}, false
	}
	return setAction(p, ActionCheckResume, g.active.handle)
}

// validRootDestroyedCommitMarker distinguishes the legacy physical marker,
// bounded handle-free receipt, and post-join terminal marker without ever
// manufacturing a replacement handle.
func validRootDestroyedCommitMarker(p *P, g *G, kind ActionKind) bool {
	if p == nil || g == nil {
		return false
	}
	switch p.action.Kind {
	case kind:
		// The legacy whole-operation path still owns the just-destroyed
		// physical action. ReleaseFrame unlinked the allocation but the cached
		// root identity is retained until this compatibility commit.
		return p.action.Handle != nil && g.root != nil
	case ActionCommitDestroy:
		// The bounded path discarded both the handle and cached root before it
		// published this receipt.
		return p.action.Handle == nil && g.root == nil
	case ActionTerminalExecutorClose:
		// A successful strong join retires the driver before retrying the
		// logical root commit. No executor, canonical source, or physical
		// handle may survive it.
		return p.action.Handle == nil && g.root == nil &&
			preemptLoad(&p.executorMode) == executorModeUnbound && p.executor == nil && p.channelSource == nil
	default:
		return false
	}
}

// commitRootDestroyedCompatibility owns the legacy full-audit, terminal-close,
// and schedule-disable boundary after a root handle has already been destroyed.
// Its input is a logical commit kind, never a physical or synthetic handle, so
// both the old whole-episode adapter and a bounded handle-free receipt can use
// the same state transition.
func commitRootDestroyedCompatibility(p *P, g *G, kind ActionKind) (Action, bool) {
	if p == nil || g == nil || p.current != g || p.inResume || g.runP != p ||
		g.destroyTarget != nil || !g.destroyRoot || g.active != nil || g.frames != nil ||
		!gPreemptDepthZero(g) ||
		!validRootDestroyedCommitMarker(p, g, kind) ||
		!validReadyQueue(p) || !validSchedulerWaitQueues(p) {
		return Action{}, false
	}
	panicking := g.panicUnwind
	if kind != ActionDestroy && kind != ActionPanicDestroy {
		return Action{}, false
	}
	if panicking {
		wantState := GPanicking
		if kind == ActionDestroy {
			wantState = GDispatching
		}
		if g.state != wantState || !publishedPanicRecord(&g.panicRecord) {
			return Action{}, false
		}
	} else if kind != ActionDestroy || g.state != GDispatching || !emptyPanicRecord(&g.panicRecord) {
		return Action{}, false
	}
	schedule := preemptLoad(&p.schedule)
	if schedule != scheduleIdle && schedule != scheduleRequested {
		return Action{}, false
	}
	// Disable only when this root is the last G owned by the P. Otherwise
	// ready/waiting peers still need the gate. CAS makes terminal success and a
	// late asynchronous producer request one exact total order.
	if p.readyHead == nil && emptySchedulerWaitQueues(p) &&
		(preemptLoad(&p.executorMode) != executorModeUnbound || p.executor != nil) {
		return beginTerminalExecutorClose(p, g, kind)
	}
	if p.readyHead == nil && emptySchedulerWaitQueues(p) &&
		(p.channelSource != nil || !preemptCompareAndSwap(&p.schedule, scheduleIdle, scheduleDisabled)) {
		return Action{}, false
	}
	if !disableGPreempt(g) {
		return Action{}, false
	}
	g.destroyRoot = false
	g.root = nil
	if panicking {
		g.panicUnwind = false
	}
	g.state = GDead
	g.runP = nil
	p.current = nil
	p.servicePreemptBudget = 0
	p.action = Action{}
	if panicking {
		return Action{Kind: ActionPanicComplete}, true
	}
	return Action{Kind: ActionComplete}, true
}

func validBoundedRootHeaders(p *P, g *G, wasRoot bool) bool {
	if p == nil || g == nil || !wasRoot || g.active != nil || g.frames != nil ||
		g.runAction != ActionInvalid || g.transferState != runnableTransferGIdle ||
		!gPreemptEnabledAtDepthZero(g) ||
		!validReadyQueueHeader(p) ||
		!validWaitQueueHeader(p) || !validParkWaitQueueHeader(p) ||
		!validAffectedWaitQueueHeader(p) {
		return false
	}
	schedule := preemptLoad(&p.schedule)
	return schedule == scheduleIdle || schedule == scheduleRequested
}

// finishBoundedRootDestroy commits only O(1) scheduler headers after the
// physical root destroy. A root with peers becomes terminal immediately. The
// last root publishes a handle-free receipt instead of entering executor
// close, rescanning queues, or looping on the legacy schedule CAS.
func finishBoundedRootDestroy(p *P, g *G, wasRoot, panicking bool) (Action, bool) {
	if !validBoundedRootHeaders(p, g, wasRoot) {
		return Action{}, false
	}
	if panicking {
		if !g.panicUnwind || !publishedPanicRecord(&g.panicRecord) || g.state != GPanicking {
			return Action{}, false
		}
	} else if g.panicUnwind || !emptyPanicRecord(&g.panicRecord) || g.state != GDispatching {
		return Action{}, false
	}

	// ReleaseFrame has already freed the combined root allocation. Clear the
	// cached root before publishing any return boundary; destroyRoot remains the
	// logical receipt bit and is never dereferenced.
	if !disableGPreempt(g) {
		return Action{}, false
	}
	g.root = nil
	if p.readyHead == nil && emptySchedulerWaitQueues(p) {
		receipt := Action{Kind: ActionCommitDestroy}
		p.action = receipt
		return receipt, true
	}

	g.destroyRoot = false
	if panicking {
		g.panicUnwind = false
	}
	g.state = GDead
	g.runP = nil
	p.current = nil
	p.servicePreemptBudget = 0
	p.action = Action{}
	if panicking {
		return Action{Kind: ActionPanicComplete}, true
	}
	return Action{Kind: ActionComplete}, true
}

// DestroyedBounded is the production post-destroy commit used by RunSlice.
// It never performs a full queue audit or terminal close and never returns the
// freed action handle. Non-root destruction resumes through a later ready-tail
// reduction; final-root work stops at ActionCommitDestroy.
func DestroyedBounded(p *P, g *G, action Action) (Action, bool) {
	if !expectedAction(p, g, action, ActionDestroy) || p.inResume || g.state != GDispatching ||
		g.destroyTarget != nil || g.runAction != ActionInvalid {
		return Action{}, false
	}
	isRoot := g.destroyRoot
	if g.panicUnwind {
		if !publishedPanicRecord(&g.panicRecord) {
			return Action{}, false
		}
		if g.active != nil {
			if isRoot {
				return Action{}, false
			}
			g.destroyRoot = false
			g.state = GPanicking
			return preparePanicAncestor(p, g, g.active)
		}
		g.state = GPanicking
		return finishBoundedRootDestroy(p, g, isRoot, true)
	}
	if isRoot {
		return finishBoundedRootDestroy(p, g, true, false)
	}
	g.destroyRoot = false
	g.state = GRunning
	if g.active == nil {
		return Action{}, false
	}
	return setAction(p, ActionCheckResume, g.active.handle)
}

func validDestroyCommitReceipt(p *P, g *G, receipt Action) bool {
	if p == nil || g == nil || receipt.Kind != ActionCommitDestroy || receipt.Handle != nil ||
		p.current != g || p.action != receipt || p.inResume || p.runDecision != (RunDecision{}) ||
		p.runDecisionTaken || p.servicePreemptBudget == 0 || !ValidG(g) || g.runP != p ||
		g.runAction != ActionInvalid || g.transferState != runnableTransferGIdle ||
		g.destroyTarget != nil || !g.destroyRoot ||
		g.root != nil || g.active != nil || g.frames != nil ||
		!validReadyQueueHeader(p) || !validWaitQueueHeader(p) ||
		!validParkWaitQueueHeader(p) || !validAffectedWaitQueueHeader(p) {
		return false
	}
	return g.state == GDispatching && !g.panicUnwind && emptyPanicRecord(&g.panicRecord) ||
		g.state == GPanicking && g.panicUnwind && publishedPanicRecord(&g.panicRecord)
}

// CommitDestroyedReceiptCompatibility crosses the explicitly unbounded
// terminal-close compatibility boundary. It passes only the logical normal or
// panic commit kind; no replacement handle is manufactured. An unbound
// schedule race consumes at most one acknowledgement and republishes the
// receipt so a later outer iteration performs the next commit attempt.
func CommitDestroyedReceiptCompatibility(p *P, g *G, receipt Action) (Action, bool) {
	if !validDestroyCommitReceipt(p, g, receipt) {
		return Action{}, false
	}
	kind := ActionDestroy
	if g.state == GPanicking {
		kind = ActionPanicDestroy
	}
	next, ok := commitRootDestroyedCompatibility(p, g, kind)
	if !ok && acknowledgeRootTerminalSchedule(p, g, kind) {
		return receipt, true
	}
	return next, ok
}

func acknowledgeRootTerminalSchedule(p *P, g *G, kind ActionKind) bool {
	if p == nil || g == nil || p.current != g || p.inResume || g.runP != p ||
		preemptLoad(&p.executorMode) != executorModeUnbound || p.executor != nil || p.channelSource != nil ||
		g.destroyTarget != nil || !g.destroyRoot || g.active != nil || g.frames != nil ||
		p.readyHead != nil || p.readyTail != nil || !emptySchedulerWaitQueues(p) ||
		!validReadyQueue(p) || !validSchedulerWaitQueues(p) {
		return false
	}
	if kind == ActionDestroy {
		if g.state != GDispatching || g.panicUnwind && !publishedPanicRecord(&g.panicRecord) ||
			!g.panicUnwind && !emptyPanicRecord(&g.panicRecord) {
			return false
		}
	} else if kind != ActionPanicDestroy || g.state != GPanicking || !g.panicUnwind ||
		!publishedPanicRecord(&g.panicRecord) {
		return false
	}
	return preemptCompareAndSwap(&p.schedule, scheduleRequested, scheduleIdle)
}

// AcknowledgeTerminalSchedule classifies and consumes the one non-corruption
// failure of Destroyed: an asynchronous RequestSchedule won the final
// idle-to-disabled race after the last frame had already been destroyed. The
// runtime adapter may retry the same ActionDestroy without invoking
// llvm.coro.destroy again. Any queue, action, or G-state mismatch fails closed.
func AcknowledgeTerminalSchedule(p *P, g *G, action Action) bool {
	return expectedAction(p, g, action, ActionDestroy) && !p.inResume &&
		preemptLoad(&p.executorMode) == executorModeUnbound && p.executor == nil && p.channelSource == nil &&
		g.state == GDispatching && g.destroyTarget == nil && g.destroyRoot &&
		g.active == nil && g.frames == nil && p.readyHead == nil && p.readyTail == nil &&
		emptySchedulerWaitQueues(p) && validReadyQueue(p) && validSchedulerWaitQueues(p) &&
		preemptCompareAndSwap(&p.schedule, scheduleRequested, scheduleIdle)
}

// TerminalG reports whether a scheduler run completely consumed g and left p
// idle. This is a deliberately strict terminal-state check for program
// startup: a dead G state alone is insufficient if any frame, transition,
// ready-queue link, destruction bookkeeping, or P operation survived.
func TerminalG(p *P, g *G) bool {
	return p != nil && p.current == nil && p.readyHead == nil && p.readyTail == nil &&
		emptySchedulerWaitQueues(p) &&
		preemptLoad(&p.schedule) == scheduleDisabled && preemptLoad(&p.executorMode) == executorModeUnbound &&
		p.executor == nil && p.channelSource == nil &&
		!p.inResume && p.action.Kind == ActionInvalid && p.action.Handle == nil && p.runDecision == (RunDecision{}) && !p.runDecisionTaken && p.servicePreemptBudget == 0 &&
		ValidG(g) && gPreemptStateAtDepthZero(g, preemptDisabled) && g.state == GDead && g.root == nil && g.active == nil && g.frames == nil &&
		g.taskControlLeases == 0 && g.runAction == ActionInvalid && g.transferState == runnableTransferGIdle &&
		g.pending.kind == pendingNone && g.pending.from == nil && g.pending.target == nil && g.pending.wait == nil && g.pending.ticket == 0 &&
		g.destroyTarget == nil && !g.destroyRoot && g.nextReady == nil && !g.queued &&
		g.waitToken == nil && g.waitTicket == 0 && g.nextWait == nil && !g.waiting && g.runP == nil &&
		releasableParkState(&g.park) && g.park.taskCancelKind == TaskCancelNone &&
		g.spawnChild == nil && g.spawnParent == nil && g.spawnP == nil && validTerminalTaskStorage(g) &&
		emptyPanicRecord(&g.panicRecord) && !g.panicUnwind
}
