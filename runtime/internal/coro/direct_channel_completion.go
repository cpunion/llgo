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

// DirectChannelCompletion is the frame-local rendezvous record used by an
// ordinary one-case unbuffered channel park. Unlike ChannelOperationSource it
// is not a general producer descriptor: the hchan queue itself pins the frame,
// serializes the typed effect, and admits exactly one match or cancellation.
// Cross-executor matchers publish this node into the exact owner's MPSC inbox;
// select, buffered channels, and independently retained callbacks continue to
// use the durable OperationID source protocol.
//
// The fields are private because the typed hchan adapter must use the closed
// begin/effect/publish API below. The node may cross physical threads only while
// its owning frame is parked and the target has retained that executor route.
type DirectChannelCompletion struct {
	// next must remain the first word: the executor's permanent stub and every
	// completion expose the same atomic intrusive-link address to the MPSC
	// queue without converting pointers through uintptr.
	next      unsafe.Pointer
	owner     *ExecutorDriver
	wait      *WaitSetRecord
	context   unsafe.Pointer
	route     RouteID
	state     uint32
	small     uint32
	preferred uint32
}

// DirectChannelParkStorageV1 is the core-owned prefix of the compiler's
// one-case channel spill. Keeping Wait, Completion, and Ticket in one typed
// object makes their address relation structural: runtime adapters pass one
// capability instead of decomposing it into several raw pointers which the
// core then has to correlate again. The fields are exported only because the
// adjacent runtime package embeds this prefix; their contained scheduler state
// remains opaque.
//
// Field order intentionally matches the former CoroChanParkV1 prefix, so this
// refactor does not increase native, WASM32, embedded, or bare-metal frames.
type DirectChannelParkStorageV1 struct {
	Wait       WaitSetRecord
	Completion DirectChannelCompletion
	Ticket     ParkTicket
}

type directChannelCompletionState uint32

const (
	directChannelCompletionUnused directChannelCompletionState = iota
	directChannelCompletionBound
	directChannelCompletionEffect
	directChannelCompletionMatched
	directChannelCompletionCanceled
	directChannelCompletionPublished
	directChannelCompletionTaken
	directChannelCompletionMaterialized
)

// DirectChannelCompletionBeginResult is the hchan-locked arbitration result.
// Canceled means the owner already selected task cancellation and the dequeued
// waiter is stale. Acquired grants the caller the sole typed-effect interval.
type DirectChannelCompletionBeginResult uint8

const (
	DirectChannelCompletionBeginInvalid DirectChannelCompletionBeginResult = iota
	DirectChannelCompletionBeginCanceled
	DirectChannelCompletionBeginAcquired
)

// DirectChannelCompletionFinishResult tells the typed hchan adapter whether
// the owner-local scheduler transaction completed the peer immediately, the
// core published it into that owner's inbox, or a target route still has to
// publish and request the remote owner. Only the inline case may retire the
// hchan waiter before a later materialization reduction observes it.
type DirectChannelCompletionFinishResult uint8

const (
	DirectChannelCompletionFinishInvalid DirectChannelCompletionFinishResult = iota
	DirectChannelCompletionFinishInline
	DirectChannelCompletionFinishOwnerPublished
	DirectChannelCompletionFinishNeedsTarget
)

func directChannelCompletionLiveState(state directChannelCompletionState) bool {
	return state >= directChannelCompletionBound && state <= directChannelCompletionTaken
}

func validBoundDirectChannelCompletion(record *WaitSetRecord, completion *DirectChannelCompletion) bool {
	if record == nil || completion == nil || !record.directChannel ||
		record.resumeKind != resumeBindingDirectChannel || record.resume != unsafe.Pointer(completion) ||
		completion.wait != record || completion.context == nil ||
		completion.owner == nil || !completion.route.Valid() || completion.owner.route != completion.route ||
		completion.owner.handle.Slot == 0 || completion.owner.handle.Generation == 0 ||
		!validParkTicket(record.ticket) {
		return false
	}
	state := directChannelCompletionState(preemptLoad(&completion.state))
	if !directChannelCompletionLiveState(state) {
		return false
	}
	small := preemptLoad(&completion.small)
	preferred := RouteID(preemptLoad(&completion.preferred))
	if preferred != 0 && !preferred.Valid() {
		return false
	}
	switch state {
	case directChannelCompletionBound, directChannelCompletionCanceled:
		return small == uint32(ResumeSmallInvalid)
	case directChannelCompletionEffect, directChannelCompletionMatched, directChannelCompletionPublished,
		directChannelCompletionTaken:
		// Published/Taken can represent cancellation; the task token and zero
		// result distinguish it at the owner materialization boundary.
		return small <= uint32(^uint8(0))
	default:
		return false
	}
}

// ValidDirectChannelCompletion is the typed runtime's frame-binding check.
// It exposes no scheduler/source internals and is valid only before owner
// materialization clears the node.
func ValidDirectChannelCompletion(
	completion *DirectChannelCompletion,
	wait *WaitSetRecord,
) bool {
	return validBoundDirectChannelCompletion(wait, completion)
}

func validCommittedCompactDirectChannelPark(g *G, frame *Frame, wait *WaitSetRecord) bool {
	if wait == nil || wait.resumeKind != resumeBindingDirectChannel || !wait.directChannel ||
		wait.resume == nil {
		return false
	}
	completion := (*DirectChannelCompletion)(wait.resume)
	if completion.wait != wait || completion.context == nil ||
		frame.owner != g || frame.parkWait != wait || wait.g != g ||
		wait.state != waitSetRecordCommitted || wait.work != waitSetWorkIdle ||
		wait.activePrev != nil || wait.activeNext != nil || wait.workNext != nil {
		return false
	}
	completionState := directChannelCompletionState(preemptLoad(&completion.state))
	small := preemptLoad(&completion.small)
	if !directChannelCompletionLiveState(completionState) ||
		(completionState == directChannelCompletionBound || completionState == directChannelCompletionCanceled) &&
			small != uint32(ResumeSmallInvalid) {
		return false
	}
	// PrepareCurrentDirectChannelPark is the sole producer of pendingParkSet's
	// compact binding, and generated code suspends immediately after the runtime
	// call. The identity, queue-link, and concurrently mutable completion word
	// above are the independent commit boundary. The remaining
	// ParkState payload is owner-only construction data already certified by
	// that pending transition; no producer can change it before Resumed.
	park := &g.park
	return park.ticket == wait.ticket && park.phase == parkParked && !park.resolving &&
		park.taskCancelKind == TaskCancelNone && park.taskCancelPhase == taskCancelIdle &&
		park.cancelKind == ParkCancelNone && park.outcome == ParkOutcomePending
}

// PrepareCurrentDirectChannelPark builds the source-free one-case park. Every
// fallible owner/frame observation precedes the no-fail write suffix. The
// caller publishes its typed hchan waiter only after this returns successfully.
func PrepareCurrentDirectChannelPark(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	storage *DirectChannelParkStorageV1,
) (*ExecutorDriver, RouteID) {
	if g == nil || handle == nil || header == nil || storage == nil {
		return nil, 0
	}
	wait := &storage.Wait
	completion := &storage.Completion
	context := unsafe.Pointer(storage)
	p := g.runP
	driver := (*ExecutorDriver)(nil)
	if p != nil {
		driver = p.executor
	}
	// The bounded runner retains run.issued across the complete physical
	// llvm.coro.resume. Generated code can therefore present an exact one-shot
	// compiler park capability: CheckedExecutorRun already authenticated the
	// immutable P/G/frame binding, and the channel hook owns its spill storage
	// from the preceding resume prologue through this no-suspend call. Keep the
	// arbitrary-caller path below for compatibility adapters and tests.
	if driver != nil && driver.run.issued == ActionCheckResume {
		// run.issued is retained across the physical resume and can be written
		// only by the validated bounded selector. Together with current/inResume
		// it already freezes the driver, route, action kind, running G, and empty
		// scheduler queues. The compiler's park-spill magic (checked by the typed
		// caller) certifies fresh Wait/Completion storage. Recheck only mutable
		// facts which this operation itself consumes.
		if p.current != g || !p.inResume || !p.runDecisionTaken ||
			!gPreemptEnabledAtDepthZero(g) || g.pending.kind != pendingNone {
			return nil, 0
		}
		frame := g.active
		if frame == nil || frame.handle != handle || frame.header != header ||
			frame.state != FrameActive || frame.parkWait != nil {
			return nil, 0
		}
		if p.inlineAwaitDepth == 0 {
			if p.action.Handle != handle {
				return nil, 0
			}
		} else if !resumeActionOwnsActive(g, p.action, p.inlineAwaitDepth) {
			return nil, 0
		}

		previous := g.park.ticket
		switch g.park.phase {
		case parkIdle:
			if previous != (ParkTicket{}) {
				return nil, 0
			}
		case parkDelivered:
			if !validParkTicket(previous) {
				return nil, 0
			}
		default:
			return prepareCurrentDirectChannelParkCompatibility(
				g, handle, header, storage,
			)
		}
		// This issued path is deliberately flat. nextParkTicket is shared by
		// arbitrary source transactions and is not inlined into this already
		// sizeable boundary by LLVM; spelling its three scalar rollover cases
		// here avoids a hot aggregate-return call without changing the ticket
		// sequence.
		ticket := previous
		if ticket == (ParkTicket{}) {
			ticket.generation = 1
		} else if !validParkTicket(ticket) {
			return nil, 0
		} else if ticket.generation != ^uint32(0) {
			ticket.generation++
		} else if ticket.epoch != ^uint32(0) {
			ticket.epoch++
			ticket.generation = 1
		} else {
			return nil, 0
		}

		// The direct compiler spill is exact zero storage at this boundary
		// except for ParkState's retained generation/Delivered phase. Initialize
		// only live words; promotion and the resume prologue clear them before
		// the lifecycle capability is made reusable.
		g.park.ticket = ticket
		g.park.phase = parkParked
		completion.owner = driver
		completion.wait = wait
		completion.context = context
		completion.route = driver.route
		completion.state = uint32(directChannelCompletionBound)
		wait.g = g
		wait.resume = unsafe.Pointer(completion)
		wait.ticket = ticket
		wait.state = waitSetRecordCommitted
		wait.resumeKind = resumeBindingDirectChannel
		wait.directChannel = true
		frame.parkWait = wait
		g.pending.kind = pendingParkSet
		g.pending.directChannel = true
		g.pending.from = frame
		storage.Ticket = ticket
		return driver, driver.route
	}
	return prepareCurrentDirectChannelParkCompatibility(
		g, handle, header, storage,
	)
}

// prepareCurrentDirectChannelParkCompatibility retains the complete
// arbitrary-caller implementation. It is split only so the issued path can
// fail over for the legacy parkConsumed shape without recursively selecting
// itself again.
func prepareCurrentDirectChannelParkCompatibility(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	storage *DirectChannelParkStorageV1,
) (*ExecutorDriver, RouteID) {
	wait := &storage.Wait
	completion := &storage.Completion
	context := unsafe.Pointer(storage)
	p := g.runP
	driver := (*ExecutorDriver)(nil)
	if p != nil {
		driver = p.executor
	}
	frame, action := g.active, Action{}
	if p != nil {
		action = p.action
	}
	if p == nil || p.current != g || !p.inResume || driver == nil || driver.p != p ||
		!driver.route.Valid() || action.Kind != ActionResume || action.Flags != 0 ||
		action.Handle == nil || p.runDecision != (RunDecision{}) || !p.runDecisionTaken ||
		!gPreemptEnabledAtDepthZero(g) || g.pending.kind != pendingNone ||
		g.spawnChild != nil || g.waiting || g.park.resolving ||
		g.park.taskCancelKind != TaskCancelNone || g.park.taskCancelPhase != taskCancelIdle ||
		(wait.state != waitSetRecordUnused || wait.resume != nil) ||
		directChannelCompletionState(preemptLoad(&completion.state)) != directChannelCompletionUnused {
		return nil, 0
	}
	if frame == nil || frame.handle != handle || frame.header != header || frame.state != FrameActive ||
		frame.parkWait != nil || header.G != unsafe.Pointer(g) ||
		header.SuspendReason != uint16(SuspendPark) || header.Lifecycle != uint16(FrameSuspended) {
		return nil, 0
	}
	if p.inlineAwaitDepth == 0 {
		if action.Handle != handle {
			return nil, 0
		}
	} else if !resumeActionOwnsActive(g, action, p.inlineAwaitDepth) {
		return nil, 0
	}
	switch g.park.phase {
	case parkIdle:
		if g.park.ticket != (ParkTicket{}) {
			return nil, 0
		}
	case parkDelivered:
		if !validParkTicket(g.park.ticket) || g.park.cancelKind != ParkCancelNone ||
			g.park.outcome != ParkOutcomePending {
			return nil, 0
		}
	case parkConsumed:
		if !validReusableDirectChannelParkState(&g.park) {
			return nil, 0
		}
	default:
		return nil, 0
	}
	ticket, ok := nextParkTicket(g.park.ticket)
	if !ok {
		return nil, 0
	}
	g.park = ParkState{ticket: ticket, phase: parkParked}
	*completion = DirectChannelCompletion{
		owner: driver, wait: wait, context: context,
		route: driver.route, state: uint32(directChannelCompletionBound),
	}
	*wait = WaitSetRecord{
		g: g, resume: unsafe.Pointer(completion), ticket: ticket,
		state: waitSetRecordCommitted, resumeKind: resumeBindingDirectChannel,
		directChannel: true,
	}
	frame.parkWait = wait
	g.pending = pendingTransition{kind: pendingParkSet, directChannel: true, from: frame}
	storage.Ticket = ticket
	return driver, driver.route
}

// BeginDirectChannelCompletion claims the typed hchan effect. The hchan lock
// protects queue linkage and payload storage; this atomic word arbitrates only
// against owner-side task cancellation.
func BeginDirectChannelCompletion(completion *DirectChannelCompletion) DirectChannelCompletionBeginResult {
	if completion == nil {
		return DirectChannelCompletionBeginInvalid
	}
	for {
		switch directChannelCompletionState(preemptLoad(&completion.state)) {
		case directChannelCompletionBound:
			if preemptCompareAndSwap(
				&completion.state,
				uint32(directChannelCompletionBound),
				uint32(directChannelCompletionEffect),
			) {
				return DirectChannelCompletionBeginAcquired
			}
		case directChannelCompletionCanceled:
			return DirectChannelCompletionBeginCanceled
		case directChannelCompletionPublished:
			if preemptLoad(&completion.small) == uint32(ResumeSmallInvalid) {
				return DirectChannelCompletionBeginCanceled
			}
			return DirectChannelCompletionBeginInvalid
		default:
			return DirectChannelCompletionBeginInvalid
		}
	}
}

// AbortDirectChannelCompletion releases an acquired pre-effect claim. It is
// used only when another endpoint cannot be committed under the same hchan
// lock; no typed payload or waiter status may have changed yet.
func AbortDirectChannelCompletion(completion *DirectChannelCompletion) bool {
	return completion != nil && preemptLoad(&completion.small) == uint32(ResumeSmallInvalid) &&
		preemptCompareAndSwap(
			&completion.state,
			uint32(directChannelCompletionEffect),
			uint32(directChannelCompletionBound),
		)
}

// FinishDirectChannelCompletion closes the typed effect and returns the exact
// owner identity needed by the target publication shim.
func FinishDirectChannelCompletion(
	completion *DirectChannelCompletion,
	small uint8,
	preferred RouteID,
) (*ExecutorDriver, RouteID, bool) {
	if completion == nil || small == ResumeSmallInvalid || completion.owner == nil ||
		!completion.route.Valid() || preferred != 0 && !preferred.Valid() ||
		preemptLoad(&completion.state) != uint32(directChannelCompletionEffect) {
		return nil, 0, false
	}
	preemptStore(&completion.small, uint32(small))
	preemptStore(&completion.preferred, uint32(preferred))
	if !preemptCompareAndSwap(
		&completion.state,
		uint32(directChannelCompletionEffect),
		uint32(directChannelCompletionMatched),
	) {
		return nil, 0, false
	}
	return completion.owner, completion.route, true
}

// validActiveDirectChannelWaitHeader proves the owner-local queue and G/frame
// placement without re-entering the generic resume-binding validator. The
// caller immediately checks the compact completion, packet, and complete
// ParkState below, so repeating those same frame fields through
// validActiveWaitSetRecordFast would add no independent certificate.
func validActiveDirectChannelWaitHeader(p *P, record *WaitSetRecord) bool {
	if p == nil || record == nil || record.state != waitSetRecordActive ||
		!validParkTicket(record.ticket) || record.g == nil || record.g.magic != gMagic ||
		record.g.state != GWaiting || !record.g.waiting || record.g.queued ||
		record.g.nextReady != nil || record.g.runP != nil ||
		record.g.transferState != runnableTransferGIdle || record.g.active == nil ||
		record.g.active.parkWait != record {
		return false
	}
	if record.activePrev == nil {
		if p.parkWaitHead != record {
			return false
		}
	} else if record.activePrev.activeNext != record {
		return false
	}
	if record.activeNext == nil {
		return p.parkWaitTail == record
	}
	return record.activeNext.activePrev == record
}

func completeDirectChannelWait(
	driver *ExecutorDriver,
	completion *DirectChannelCompletion,
	expected directChannelCompletionState,
	small uint8,
) bool {
	if driver == nil || completion == nil || completion.owner != driver || driver.p == nil ||
		completion.wait == nil || completion.context == nil ||
		completion.route != driver.route || preemptLoad(&completion.state) != uint32(expected) ||
		preemptLoad(&completion.small) != uint32(small) {
		return false
	}
	wait, p := completion.wait, driver.p
	if wait.state != waitSetRecordActive || wait.work != waitSetWorkIdle || wait.workNext != nil ||
		wait.resumeKind != resumeBindingDirectChannel || wait.resume != unsafe.Pointer(completion) ||
		!validActiveDirectChannelWaitHeader(p, wait) || !validReadyQueueHeader(p) || p.readyCount == ^uint32(0) {
		return false
	}
	g, frame := wait.g, wait.g.active
	if frame == nil || frame.parkWait != wait || g.spawnChild != nil || g.spawnParent != nil || g.spawnP != nil {
		return false
	}
	state := &g.park
	// The compact active wait has no source/candidate list: after the committed
	// transition is activated, only task cancellation may mutate ParkState
	// before this exact completion winner. Recheck the correlation and mutable
	// cancellation fields; construction-only zero payload was certified at the
	// pending transition and has no independent writer.
	if state.ticket != wait.ticket || state.phase != parkParked || state.resolving ||
		state.outcome != ParkOutcomePending {
		return false
	}
	canceled := state.taskCancelPhase == taskCancelRequested
	if canceled {
		if !validTaskCancelKind(state.taskCancelKind) || state.cancelKind < ParkCancelTaskAbort {
			return false
		}
	} else if state.taskCancelKind != TaskCancelNone || state.taskCancelPhase != taskCancelIdle ||
		state.cancelKind != ParkCancelNone || small == ResumeSmallInvalid {
		return false
	}
	if expected == directChannelCompletionTaken && small == ResumeSmallInvalid && !canceled {
		return false
	}
	if expected == directChannelCompletionEffect && canceled {
		return false
	}

	outcome, caseID, resultSmall := ParkOutcomeCompleted, uint32(1), small
	if canceled {
		outcome, caseID, resultSmall = ParkOutcomeCanceled, 0, ResumeSmallInvalid
	}
	materializeDirectChannelWaitUnchecked(
		driver, completion, wait, state, resultSmall, outcome, caseID,
	)
	return true
}

// materializeDirectChannelWaitUnchecked is the common no-fail owner mutation
// after either the routed inbox or the current-owner hchan transaction has
// authenticated its exact completion generation. It contains no observation
// or branch so the two entry gates cannot drift in the state they publish.
func materializeDirectChannelWaitUnchecked(
	driver *ExecutorDriver,
	completion *DirectChannelCompletion,
	wait *WaitSetRecord,
	state *ParkState,
	resultSmall uint8,
	outcome ParkOutcome,
	caseID uint32,
) {
	preemptStore(&completion.small, uint32(resultSmall))
	// A compact Parked state owns no source/link payload. Its ticket and task
	// cancellation receipt remain live; materialization changes only this small
	// scalar overlay. Avoid clearing and reconstructing the complete ParkState.
	state.phase = parkMaterialized
	state.directChannel = true
	state.seed = preemptLoad(&completion.preferred)
	state.cancelKind = ParkCancelNone
	state.outcome = outcome
	state.winnerCase = caseID
	preemptStore(&completion.state, uint32(directChannelCompletionMaterialized))
	promoteReadyWaitSetUnchecked(driver.p, wait)
	driver.run.blocked = false
	driver.run.actionsSinceSource = 0
	driver.run.readyDebt = true
}

// FinishDirectChannelCompletionFromCompilerTask is the fused completion
// boundary for a compiler-owned one-case channel operation. The hidden task is
// already available to the typed hchan path; deriving its P/driver here avoids
// decomposing that private scheduler capability in runtime and then replaying
// the same relation at a second package boundary.
//
// A valid current task supplies the preferred producer route and admits the
// owner-local materialization fast path. Compatibility/foreign callers may
// pass nil and an advisory fallback route; they retain the routed publication
// path. NeedsTarget returns the exact owner and route which the target shim
// must retain and request.
func FinishDirectChannelCompletionFromCompilerTask(
	current *G,
	completion *DirectChannelCompletion,
	small uint8,
	fallback RouteID,
) (*ExecutorDriver, RouteID, DirectChannelCompletionFinishResult) {
	if completion == nil || small == ResumeSmallInvalid {
		return nil, 0, DirectChannelCompletionFinishInvalid
	}
	preferred := fallback
	var currentDriver *ExecutorDriver
	if current != nil {
		p := current.runP
		if p != nil && p.current == current && p.inResume {
			driver := p.executor
			if driver != nil && driver.p == p && driver.route.Valid() &&
				driver.sources.route == driver.route {
				currentDriver = driver
				preferred = driver.route
			}
		}
	}
	if preferred != 0 && !preferred.Valid() {
		return nil, 0, DirectChannelCompletionFinishInvalid
	}
	if currentDriver != nil && completion.owner == currentDriver {
		p := currentDriver.p
		wait := completion.wait
		// The legacy prepare-then-try ABI can match the current task before its
		// committed waiter has crossed llvm.coro.resume and become Active. That
		// record cannot be promoted while the task is still running; authenticate
		// the exact adjacent pending capability and let the ordinary completion
		// inbox materialize it after Resumed activates the wait. The fused V2 ABI
		// probes before preparing and never takes this compatibility edge.
		if wait != nil && wait.state == waitSetRecordCommitted && wait.g == current {
			frame := current.active
			if frame == nil || current.pending.kind != pendingParkSet ||
				!current.pending.directChannel || current.pending.from != frame ||
				!validCommittedCompactDirectChannelPark(current, frame, wait) {
				return nil, 0, DirectChannelCompletionFinishInvalid
			}
		} else {
			// The hchan lock and Effect state are the cross-thread arbitration
			// boundary; current/inResume makes every scheduler field owner-only.
			// Recheck the live generation and active-list correlations immediately
			// consumed by the no-fail materialization suffix.
			if completion.route != currentDriver.route || wait == nil || wait.g == nil ||
				preemptLoad(&completion.state) != uint32(directChannelCompletionEffect) ||
				wait.state != waitSetRecordActive || !wait.directChannel ||
				wait.resume != unsafe.Pointer(completion) {
				return nil, 0, DirectChannelCompletionFinishInvalid
			}
			g := wait.g
			frame := g.active
			if frame == nil || frame.parkWait != wait {
				return nil, 0, DirectChannelCompletionFinishInvalid
			}
			state := &g.park
			if state.ticket != wait.ticket || state.phase != parkParked ||
				state.taskCancelKind != TaskCancelNone || state.taskCancelPhase != taskCancelIdle ||
				state.cancelKind != ParkCancelNone || p.readyCount == ^uint32(0) {
				return nil, 0, DirectChannelCompletionFinishInvalid
			}
			preemptStore(&completion.preferred, uint32(preferred))
			materializeDirectChannelWaitUnchecked(
				currentDriver, completion, wait, state, small, ParkOutcomeCompleted, 1,
			)
			return nil, 0, DirectChannelCompletionFinishInline
		}
	}
	owner, route, ok := FinishDirectChannelCompletion(completion, small, preferred)
	if !ok {
		return nil, 0, DirectChannelCompletionFinishInvalid
	}
	if currentDriver == owner {
		if !PublishExecutorDirectChannelCompletion(owner, completion) {
			return nil, 0, DirectChannelCompletionFinishInvalid
		}
		return nil, 0, DirectChannelCompletionFinishOwnerPublished
	}
	return owner, route, DirectChannelCompletionFinishNeedsTarget
}

// PublishExecutorDirectChannelCompletion appends one terminal frame node to
// the exact owner's lock-free MPSC stack. Route/target glue must retain its
// executor ingress until this publication and the subsequent request finish.
func PublishExecutorDirectChannelCompletion(
	driver *ExecutorDriver,
	completion *DirectChannelCompletion,
) bool {
	if driver == nil || completion == nil || completion.owner != driver ||
		completion.route != driver.route || driver.magic != executorDriverMagic ||
		driver.p == nil || driver.p.executor != driver ||
		preemptLoadPointer(&driver.directChannelHead) == nil {
		return false
	}
	state := directChannelCompletionState(preemptLoad(&completion.state))
	if state != directChannelCompletionMatched && state != directChannelCompletionCanceled {
		return false
	}
	if !preemptCompareAndSwap(
		&completion.state, uint32(state), uint32(directChannelCompletionPublished),
	) {
		return false
	}
	preemptStorePointer(&completion.next, nil)
	previous := preemptSwapPointer(&driver.directChannelHead, unsafe.Pointer(completion))
	if previous == nil || previous == unsafe.Pointer(completion) {
		return false
	}
	// Publication linearizes at the exchange, but the consumer cannot reach the
	// node until this release link is visible. The target request happens only
	// after the link, so a producer paused in this narrow interval cannot strand
	// a sleeping executor.
	preemptStorePointer((*unsafe.Pointer)(previous), unsafe.Pointer(completion))
	return true
}

// DirectChannelCompletionSnapshot exposes only the typed runtime reduction
// payload after the owner has removed the node from its inbox.
func DirectChannelCompletionSnapshot(
	completion *DirectChannelCompletion,
) (context unsafe.Pointer, small uint8, matched bool, ok bool) {
	if completion == nil || preemptLoad(&completion.state) != uint32(directChannelCompletionTaken) ||
		completion.context == nil || preemptLoad(&completion.small) > uint32(^uint8(0)) {
		return nil, ResumeSmallInvalid, false, false
	}
	small = uint8(preemptLoad(&completion.small))
	return completion.context, small, small != ResumeSmallInvalid, true
}

// CommitDirectChannelCompletion consumes the typed runtime cleanup and
// materializes the frame-local completion before publishing the G runnable.
func CommitDirectChannelCompletion(completion *DirectChannelCompletion, small uint8) bool {
	if completion == nil || completion.owner == nil {
		return false
	}
	return completeDirectChannelWait(
		completion.owner, completion, directChannelCompletionTaken, small,
	)
}

func directChannelCompletionForWait(wait *WaitSetRecord) (*DirectChannelCompletion, bool) {
	if wait == nil || wait.resumeKind != resumeBindingDirectChannel || wait.resume == nil {
		return nil, false
	}
	completion := (*DirectChannelCompletion)(wait.resume)
	return completion, validBoundDirectChannelCompletion(wait, completion)
}

func requestDirectChannelCancellation(completion *DirectChannelCompletion) bool {
	if completion == nil || completion.owner == nil {
		return false
	}
	for {
		switch directChannelCompletionState(preemptLoad(&completion.state)) {
		case directChannelCompletionBound:
			if !preemptCompareAndSwap(
				&completion.state,
				uint32(directChannelCompletionBound),
				uint32(directChannelCompletionCanceled),
			) {
				continue
			}
			return PublishExecutorDirectChannelCompletion(completion.owner, completion)
		case directChannelCompletionEffect, directChannelCompletionMatched,
			directChannelCompletionPublished, directChannelCompletionTaken:
			// The physical winner owns completion and will observe the sticky task
			// token while materializing its result.
			return true
		default:
			return false
		}
	}
}

func takeExecutorDirectChannelCompletion(driver *ExecutorDriver) (*DirectChannelCompletion, bool) {
	if driver == nil || driver.directChannelTail == nil {
		return nil, false
	}
	stub := unsafe.Pointer(&driver.directChannelStub)
	tail := driver.directChannelTail
	next := preemptLoadPointer((*unsafe.Pointer)(tail))
	if tail == stub {
		if next == nil {
			return nil, true
		}
		driver.directChannelTail = next
		tail = next
		next = preemptLoadPointer((*unsafe.Pointer)(tail))
	}
	if next == nil {
		head := preemptLoadPointer(&driver.directChannelHead)
		if head == nil {
			return nil, false
		}
		if tail != head {
			// A producer has exchanged the head but has not linked its predecessor
			// yet. It will request this executor after completing that link.
			return nil, true
		}
		preemptStorePointer(&driver.directChannelStub, nil)
		previous := preemptSwapPointer(&driver.directChannelHead, stub)
		if previous == nil || previous == stub {
			return nil, false
		}
		preemptStorePointer((*unsafe.Pointer)(previous), stub)
		next = preemptLoadPointer((*unsafe.Pointer)(tail))
		if next == nil {
			// Another producer may still be closing the predecessor link. The
			// subsequent request is the retry obligation; do not spin here.
			return nil, true
		}
	}
	driver.directChannelTail = next
	preemptStorePointer((*unsafe.Pointer)(tail), nil)
	completion := (*DirectChannelCompletion)(tail)
	if !preemptCompareAndSwap(
		&completion.state,
		uint32(directChannelCompletionPublished),
		uint32(directChannelCompletionTaken),
	) {
		return nil, false
	}
	return completion, true
}

func executorDirectChannelCompletionPending(driver *ExecutorDriver) bool {
	if driver == nil || driver.directChannelTail == nil {
		return false
	}
	stub := unsafe.Pointer(&driver.directChannelStub)
	tail := driver.directChannelTail
	next := preemptLoadPointer((*unsafe.Pointer)(tail))
	if tail == stub {
		return next != nil
	}
	if next != nil {
		return true
	}
	return preemptLoadPointer(&driver.directChannelHead) == tail
}

func executorDirectChannelInboxIdle(driver *ExecutorDriver) bool {
	if driver == nil || driver.directChannelTail == nil {
		return false
	}
	stub := unsafe.Pointer(&driver.directChannelStub)
	return driver.directChannelTail == stub &&
		preemptLoadPointer(&driver.directChannelHead) == stub &&
		preemptLoadPointer(&driver.directChannelStub) == nil
}
