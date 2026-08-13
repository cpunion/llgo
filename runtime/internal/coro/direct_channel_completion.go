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
	wait *WaitSetRecord,
	completion *DirectChannelCompletion,
	context unsafe.Pointer,
) (ParkTicket, *ExecutorDriver, RouteID, bool) {
	if g == nil || handle == nil || header == nil || wait == nil || completion == nil || context == nil {
		return ParkTicket{}, nil, 0, false
	}
	p := g.runP
	driver := (*ExecutorDriver)(nil)
	if p != nil {
		driver = p.executor
	}
	frame, action := g.active, Action{}
	if p != nil {
		action = p.action
	}
	// p.current/inResume plus the private ActionResume episode is the immutable
	// executor-binding certificate established by CheckedExecutorRun. A driver
	// cannot close, rebind its route/catalog, or transfer this P while generated
	// code is active below llvm.coro.resume. The source-free direct record needs
	// only the exact driver back-pointer and route; its mutable park/frame state
	// is still audited below before any waiter becomes visible.
	if p == nil || p.current != g || !p.inResume || driver == nil || driver.p != p ||
		!driver.route.Valid() || action.Kind != ActionResume || action.Flags != 0 ||
		action.Handle == nil || p.runDecision != (RunDecision{}) || !p.runDecisionTaken ||
		!gPreemptEnabledAtDepthZero(g) || g.pending.kind != pendingNone ||
		g.spawnChild != nil || g.waiting || g.park.resolving ||
		g.park.taskCancelKind != TaskCancelNone || g.park.taskCancelPhase != taskCancelIdle ||
		(wait.state != waitSetRecordUnused || wait.resume != nil) ||
		directChannelCompletionState(preemptLoad(&completion.state)) != directChannelCompletionUnused {
		return ParkTicket{}, nil, 0, false
	}
	if frame == nil || frame.handle != handle || frame.header != header || frame.state != FrameActive ||
		frame.parkWait != nil || header.G != unsafe.Pointer(g) ||
		header.SuspendReason != uint16(SuspendPark) || header.Lifecycle != uint16(FrameSuspended) {
		return ParkTicket{}, nil, 0, false
	}
	if p.inlineAwaitDepth == 0 {
		if action.Handle != handle {
			return ParkTicket{}, nil, 0, false
		}
	} else if !resumeActionOwnsActive(g, action, p.inlineAwaitDepth) {
		return ParkTicket{}, nil, 0, false
	}
	// The compiler frame allocator and the prior resume own whole-storage
	// clearing. The state words above reject live reuse; every record is replaced
	// below, so comparing three complete structs against zero would only read
	// construction bytes that cannot be independently published. ParkState is
	// likewise a private resume receipt. Consumed is the legacy compatibility
	// shape and retains its complete validator.
	switch g.park.phase {
	case parkIdle:
		if g.park.ticket != (ParkTicket{}) {
			return ParkTicket{}, nil, 0, false
		}
	case parkDelivered:
		if !validParkTicket(g.park.ticket) || g.park.cancelKind != ParkCancelNone ||
			g.park.outcome != ParkOutcomePending {
			return ParkTicket{}, nil, 0, false
		}
	case parkConsumed:
		if !validReusableDirectChannelParkState(&g.park) {
			return ParkTicket{}, nil, 0, false
		}
	default:
		return ParkTicket{}, nil, 0, false
	}
	ticket, ok := nextParkTicket(g.park.ticket)
	if !ok {
		return ParkTicket{}, nil, 0, false
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
	g.pending = pendingTransition{kind: pendingParkSet, from: frame}
	return ticket, driver, driver.route, true
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

func directChannelCompletionActiveOnCurrent(
	current *G,
	driver *ExecutorDriver,
	completion *DirectChannelCompletion,
) bool {
	if current == nil || driver == nil || driver.p == nil || current.runP != driver.p ||
		driver.p.current != current || !driver.p.inResume || !driver.p.runDecisionTaken ||
		driver.p.executor != driver || completion == nil ||
		completion.owner != driver || completion.wait == nil ||
		completion.wait.state != waitSetRecordActive || completion.wait.work != waitSetWorkIdle ||
		completion.wait.workNext != nil {
		return false
	}
	return true
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
	ticket := wait.ticket
	preemptStore(&completion.small, uint32(resultSmall))
	kind, phase := state.taskCancelKind, state.taskCancelPhase
	*state = ParkState{
		ticket: ticket, phase: parkMaterialized,
		seed:           preemptLoad(&completion.preferred),
		taskCancelKind: kind, taskCancelPhase: phase,
		outcome: outcome, winnerCase: caseID,
	}
	preemptStore(&completion.state, uint32(directChannelCompletionMaterialized))
	promoteReadyWaitSetUnchecked(p, wait)
	driver.run.blocked = false
	driver.run.actionsSinceSource = 0
	driver.run.readyDebt = true
	return true
}

// TryCompleteCurrentDirectChannel completes the common same-executor
// rendezvous without an inbox or source reduction. current is the exact
// compiler-carried task already used to derive driver for this no-suspend
// runtime call; the exact wait/park/packet validator below remains the mutation
// gate. handled=false is a clean fallback for a waiter which has not yet
// reached Active or belongs elsewhere.
func TryCompleteCurrentDirectChannel(
	current *G,
	driver *ExecutorDriver,
	completion *DirectChannelCompletion,
	small uint8,
	preferred RouteID,
) (handled, ok bool) {
	if completion == nil || small == ResumeSmallInvalid || preferred != 0 && !preferred.Valid() ||
		preemptLoad(&completion.state) != uint32(directChannelCompletionEffect) {
		return false, false
	}
	if !directChannelCompletionActiveOnCurrent(current, driver, completion) {
		return false, true
	}
	preemptStore(&completion.small, uint32(small))
	preemptStore(&completion.preferred, uint32(preferred))
	return true, completeDirectChannelWait(
		driver, completion, directChannelCompletionEffect, small,
	)
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
		driver.state != executorDriverActive {
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
