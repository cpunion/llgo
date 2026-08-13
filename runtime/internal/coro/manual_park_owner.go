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

// CurrentExecutorManualDriver resolves the route-aware owner-event source in
// the compiler park/resume window. Producers retain only the returned
// OperationID; the driver and every Go pointer remain owner-thread-local.
func CurrentExecutorManualDriver(g *G) (*ExecutorDriver, ExecutorHandle, RouteID, bool) {
	driver, handle, route, ok := currentExecutorParkDriver(g)
	if !ok || driver.sources.manual == nil || driver.sources.manual.owner != driver.p ||
		driver.sources.manual.route != route {
		return nil, ExecutorHandle{}, 0, false
	}
	return driver, handle, route, true
}

// CurrentExecutorManualReservation combines current-G authentication with the
// common one-event source preflight. current distinguishes an invalid owner
// from ordinary catalog exhaustion so the runtime may grow stable pages only
// on the latter path.
func CurrentExecutorManualReservation(
	g *G,
) (
	driver *ExecutorDriver,
	handle ExecutorHandle,
	route RouteID,
	reservation ManualOperationReservation,
	current bool,
	reserved bool,
) {
	driver, handle, route, current = CurrentExecutorManualDriver(g)
	if !current {
		return nil, ExecutorHandle{}, 0, ManualOperationReservation{}, false, false
	}
	reservation, reserved = driver.sources.manual.preflightManualReservationOwned(driver.p)
	return
}

func currentExecutorManualSource(driver *ExecutorDriver, g *G) (*ManualOperationSource, bool) {
	if driver == nil || !ValidG(g) || g.active == nil {
		return nil, false
	}
	current, _, _, ok := CurrentExecutorManualDriver(g)
	if !ok || current != driver {
		return nil, false
	}
	return driver.sources.manual, true
}

// PrepareSingleManualPark installs one irreversible owner-event candidate in
// the current ParkState. The source-specific registry must publish its POD
// identity before the caller executes llvm.coro.suspend.
func PrepareSingleManualPark(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	source *ManualOperationSource,
	wait *WaitSetRecord,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	if g == nil || g.runP == nil {
		return ParkTicket{}, OperationID{}, false
	}
	reservation, ok := PreflightManualOperationReservation(g.runP, source)
	if !ok {
		return ParkTicket{}, OperationID{}, false
	}
	return prepareSingleManualParkReserved(
		g, handle, header, source, wait, reservation, caseID, seed,
	)
}

func prepareSingleManualParkReserved(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	source *ManualOperationSource,
	wait *WaitSetRecord,
	reservation ManualOperationReservation,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	if g != nil && g.park.taskCancelKind == TaskCancelNone &&
		g.park.taskCancelPhase == taskCancelIdle {
		return prepareSingleManualParkOrdinary(
			g, handle, header, source, wait, reservation, caseID, seed,
		)
	}
	if !ValidG(g) || handle == nil || header == nil || source == nil || wait == nil ||
		*wait != (WaitSetRecord{}) || caseID == 0 || !resumeGateTaken(g) || g.runP == nil ||
		!validManualOperationReservationHeader(source, g.runP, reservation) {
		return ParkTicket{}, OperationID{}, false
	}
	ticket, ok := BeginParkSet(&g.park, 1, seed)
	if !ok || !PrepareWaitSetRecord(wait, g, ticket) {
		return ParkTicket{}, OperationID{}, false
	}
	id, ok := source.reserveAndAttachManualReservation(
		g.runP, &g.park, ticket, wait, caseID, reservation,
	)
	if !ok || !SealParkSet(&g.park, ticket) || !PrepareParkSet(g, handle, header, ticket, wait) {
		return ParkTicket{}, OperationID{}, false
	}
	return ticket, id, true
}

func prepareSingleManualParkOrdinary(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	source *ManualOperationSource,
	wait *WaitSetRecord,
	reservation ManualOperationReservation,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	prepared, ok := preflightSingleParkPreparation(g, handle, header, wait, seed)
	if !ok || source == nil ||
		!validManualOperationReservationHeader(source, prepared.p, reservation) {
		return ParkTicket{}, OperationID{}, false
	}
	prepared.begin()
	id, ok := source.reserveAndAttachManualSlot(
		&g.park, prepared.ticket, wait, caseID, reservation,
	)
	if !ok || !prepared.commit(id, caseID) {
		return ParkTicket{}, OperationID{}, false
	}
	return prepared.ticket, id, true
}

func PrepareCurrentExecutorManualPark(
	driver *ExecutorDriver,
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, ExecutorHandle, bool) {
	source, ok := currentExecutorManualSource(driver, g)
	if !ok {
		return ParkTicket{}, OperationID{}, ExecutorHandle{}, false
	}
	ticket, operation, ok := PrepareSingleManualPark(g, handle, header, source, wait, caseID, seed)
	return ticket, operation, driver.handle, ok
}

// PrepareCurrentExecutorManualParkReserved consumes the slot capability
// returned by CurrentExecutorManualReservation. The call must remain in the
// same compiler-owned no-suspend park interval; exact driver/P/source identity
// prevents a capability from crossing an owner transition.
func PrepareCurrentExecutorManualParkReserved(
	driver *ExecutorDriver,
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	reservation ManualOperationReservation,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, ExecutorHandle, bool) {
	if driver == nil || g == nil || driver.magic != executorDriverMagic ||
		driver.state != executorDriverActive || driver.p == nil || driver.p != g.runP ||
		driver.sources.manual == nil ||
		!validManualOperationReservationHeader(driver.sources.manual, driver.p, reservation) {
		return ParkTicket{}, OperationID{}, ExecutorHandle{}, false
	}
	ticket, operation, ok := prepareSingleManualParkReserved(
		g, handle, header, driver.sources.manual, wait, reservation, caseID, seed,
	)
	return ticket, operation, driver.handle, ok
}

// PrepareCurrentExecutorManualCleanupParkReserved extends the one-event
// reservation transaction through installation of its fixed keyed/manual
// cleanup descriptor. The compiler/runtime caller must consume the reservation
// and all frame-local storage in one no-suspend interval. The park builder has
// already performed the complete one-link audit, so the final scalar relation
// is sufficient and avoids immediately walking the same G/P/source graph again.
func PrepareCurrentExecutorManualCleanupParkReserved(
	driver *ExecutorDriver,
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	reservation ManualOperationReservation,
	packet *ResumePacket,
	plan *ResumeCleanupPlan,
	context unsafe.Pointer,
	entry *OperationID,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, ExecutorHandle, bool) {
	if packet == nil || plan == nil || context == nil || entry == nil ||
		*packet != (ResumePacket{}) || *plan != (ResumeCleanupPlan{}) ||
		*entry != (OperationID{}) {
		return ParkTicket{}, OperationID{}, ExecutorHandle{}, false
	}
	ticket, operation, executor, ok := PrepareCurrentExecutorManualParkReserved(
		driver, g, handle, header, wait, reservation, caseID, seed,
	)
	if !ok {
		return ParkTicket{}, OperationID{}, ExecutorHandle{}, false
	}
	*entry = operation
	state := &g.park
	if wait.state != waitSetRecordCommitted || wait.work != waitSetWorkIdle ||
		wait.resume != nil || wait.resumeKind != resumeBindingNone || wait.g != g ||
		wait.ticket != ticket || g.active == nil || g.active.parkWait != wait ||
		g.pending.kind != pendingParkSet || g.pending.from != g.active ||
		state.phase != parkParked || state.ticket != ticket || state.expected != 1 ||
		state.attached != 1 || state.head == nil || state.head.previous != nil ||
		state.head.next != nil || state.head.wait != wait || state.head.park != state ||
		state.head.ticket != ticket || state.head.caseID != caseID ||
		state.head.operation == nil || state.head.operation.id != operation ||
		operation.Source() != OperationSourceManual {
		return ParkTicket{}, OperationID{}, ExecutorHandle{}, false
	}
	installWaitSetResumeCleanup(wait, packet, plan, ResumeCleanupBinding{
		Kind:         ResumeCleanupKeyedPark,
		Context:      context,
		Entries:      unsafe.Pointer(entry),
		Count:        1,
		RuntimeCount: 1,
		Stride:       unsafe.Sizeof(OperationID{}),
	})
	return ticket, operation, executor, true
}

// FinishCurrentExecutorManualPark releases the exact source generation after
// the compiler resume gate consumed its ParkTicket. Logical detach has already
// sealed producer admission; this function proves quiescence, releases any
// winner lease, and only then recycles the physical slot.
func FinishCurrentExecutorManualPark(
	driver *ExecutorDriver,
	g *G,
	executor ExecutorHandle,
	operation OperationID,
	lease OperationResultLease,
	discard bool,
) bool {
	source, ok := currentExecutorManualSource(driver, g)
	if !ok || executor != driver.handle || operation.Route() != driver.route ||
		!operation.Valid() || operation.Source() != OperationSourceManual {
		return false
	}
	if lease.Valid() {
		leaseID, leaseOK := lease.ID()
		if !leaseOK || leaseID != operation {
			return false
		}
	}
	p := g.runP
	if !source.ConfirmQuiesced(p, operation) {
		return false
	}
	if lease.Valid() {
		if discard {
			if !source.DiscardResult(p, lease) {
				return false
			}
		} else if !source.TakeResult(p, lease) {
			return false
		}
	}
	return source.Recycle(p, operation)
}

// OwnerLocalManualCompletion is a transient, stack-only capability for the
// exact interval between owner-side logical completion and runtime-private
// keyed cleanup. It never crosses a suspension and exposes no scheduler or
// source pointer to the runtime package; that package receives only the closed
// ResumeCleanupStep it already knows how to materialize.
type OwnerLocalManualCompletion struct {
	current *G
	driver  *ExecutorDriver
	wait    *WaitSetRecord
	plan    *ResumeCleanupPlan
	id      OperationID
}

// BeginOwnerLocalManualCompletionCurrent collapses the common same-P,
// one-event keyed park through logical resolution and source detach. The
// runtime must materialize the returned ResumeCleanupStep and then call
// FinishOwnerLocalManualCompletionCurrent before it returns to user code.
//
// handled=false, ok=true is a clean fallback and changes no source state. Once
// handled is true, ok=false is a fail-closed invariant failure after the exact
// mailbox was claimed; callers must not retry through the external source
// path. Multi-event, cancellation, cross-P, occupied-source, and non-keyed
// cleanup shapes retain the durable mailbox + request + A/ack/B protocol.
func BeginOwnerLocalManualCompletionCurrent(
	current *G,
	driver *ExecutorDriver,
	id OperationID,
) (
	completion OwnerLocalManualCompletion,
	cleanup ResumeCleanupStep,
	handled bool,
	ok bool,
) {
	if current == nil || driver == nil || driver.magic != executorDriverMagic ||
		driver.state != executorDriverActive || driver.p == nil || driver.p.executor != driver ||
		driver.p.current != current || current.runP != driver.p ||
		driver.sources.manual == nil || driver.sources.manual.owner != driver.p ||
		driver.sources.manual.route != driver.route || !id.Valid() ||
		id.Source() != OperationSourceManual || id.Route() != driver.route {
		return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, false, true
	}
	p, source := driver.p, driver.sources.manual
	slot, slotOK := manualOperationSlotFor(source, id)
	if !slotOK || preemptLoad(&slot.generation) != id.Generation {
		return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, false, false
	}
	record := &slot.record
	wait := record.link.wait
	admission := ownerLocalCompletionAdmissionForCurrent(driver, wait)
	if source.scanLimit != 1 || id.LocalSlot() != 1 || preemptLoad(&source.pending) != 0 ||
		source.affectedHead != 0 || source.affectedTail != 0 || slot.nextAffected != 0 ||
		preemptLoad(&slot.mailbox) != uint32(manualOperationMailboxEmpty) ||
		producerSourceLifecycle(preemptLoad(&slot.state)) != producerSourceActive ||
		admission == ownerLocalCompletionRejected ||
		!emptyOwnerLocalCompletion(&driver.local) || driver.poll != (executorPollTransaction{}) {
		return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, false, true
	}
	plan := (*ResumeCleanupPlan)(wait.resume)
	entry := resumeCleanupIDAt(plan, 0)
	state := &wait.g.park
	if wait.resumeKind != resumeBindingCleanup || plan == nil ||
		!validTrustedResumeCleanupPlanState(wait, plan) ||
		plan.phase != resumeCleanupBound || plan.kind != ResumeCleanupKeyedPark ||
		plan.count != 1 || plan.runtime != 1 || plan.index != 0 || plan.claim != nil ||
		entry == nil || *entry != id || plan.context == nil ||
		record.id != id || record.phase != operationActive ||
		record.disposition != OperationDispositionPending || record.resolutionApplied ||
		record.link.operation != record || record.link.park != state ||
		record.link.ticket != wait.ticket || record.resultState != operationResultEmpty ||
		state.phase != parkParked || state.resolving || state.expected != 1 || state.attached != 1 ||
		state.cancelKind != ParkCancelNone || state.taskCancelKind != TaskCancelNone ||
		state.taskCancelPhase != taskCancelIdle ||
		!validOperationCandidate(record) ||
		operationCandidateMode(record) != OperationCommitIrreversibleCompletion ||
		operationCandidateState(record) != OperationCommitIdle ||
		operationCandidateIsPublished(record) ||
		!operationCandidatePendingResultStorageValid(record) {
		return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, false, true
	}

	// Request the bounded safepoint before the irreversible mailbox claim. If
	// another producer wins the CAS, the request is harmless and the ordinary
	// source path remains complete.
	if !RequestPreempt(current) {
		return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, false, false
	}
	if !preemptCompareAndSwap(
		&slot.mailbox,
		uint32(manualOperationMailboxEmpty),
		uint32(manualOperationMailboxDraining),
	) {
		return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, false, true
	}
	handled = true
	if PublishOperationCompletion(record, id) != OperationCompletionPublished {
		return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, true, false
	}
	preemptStore(&slot.mailbox, uint32(manualOperationMailboxDelivered))

	// Consume the exact owner-work admission without entering either the local
	// FIFO or the external source epoch. The admission was validated after the
	// currently running G and owner P were authenticated and no suspension can
	// occur in this transaction.
	switch admission {
	case ownerLocalCompletionIdle:
		if wait.work != waitSetWorkIdle || wait.workNext != nil {
			return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, true, false
		}
	case ownerLocalCompletionAffectedHead:
		if wait.work != waitSetWorkQueued || p.affectedWaitHead != wait {
			return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, true, false
		}
		next := wait.workNext
		if next == nil && p.affectedWaitTail != wait {
			return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, true, false
		}
		p.affectedWaitHead = next
		if next == nil {
			p.affectedWaitTail = nil
		}
		wait.workNext = nil
	default:
		return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, true, false
	}
	wait.work = waitSetWorkResolving
	resolution, result := resolveAffectedOperationPublishedEpoch(record, id)
	if result != affectedOperationResolved ||
		resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}) ||
		source.ApplyOne(p, id, record) != OperationApplyDetached {
		return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, true, false
	}
	if retry, await, progressOK := finishWaitSetApplyProgress(wait, false, false); !progressOK || retry || await || !beginResumeCleanup(wait, plan) {
		return OwnerLocalManualCompletion{}, ResumeCleanupStep{}, true, false
	}
	cleanup = ResumeCleanupStep{
		Kind:       plan.kind,
		Context:    plan.context,
		Index:      plan.index,
		WinnerCase: plan.caseID,
		Outcome:    plan.outcome,
		plan:       plan,
	}
	completion = OwnerLocalManualCompletion{
		current: current,
		driver:  driver,
		wait:    wait,
		plan:    plan,
		id:      id,
	}
	return completion, cleanup, true, true
}

// FinishOwnerLocalManualCompletionCurrent consumes a successfully prepared
// completion after the runtime has committed its private cleanup step. The
// source-neutral single-operation cleanup closes admission, releases the
// result lease, recycles the slot, materializes the ResumePacket, and promotes
// the peer without another scheduler source epoch.
func FinishOwnerLocalManualCompletionCurrent(
	completion *OwnerLocalManualCompletion,
) bool {
	if completion == nil || completion.current == nil || completion.driver == nil ||
		completion.wait == nil || completion.plan == nil || !completion.id.Valid() {
		return false
	}
	current, driver, wait, plan := completion.current, completion.driver, completion.wait, completion.plan
	p := driver.p
	if driver.magic != executorDriverMagic || driver.state != executorDriverActive ||
		p == nil || p.executor != driver || p.current != current || current.runP != p ||
		driver.sources.manual == nil || driver.sources.manual.owner != p ||
		driver.sources.manual.route != driver.route || completion.id.Route() != driver.route ||
		wait.resumeKind != resumeBindingCleanup || wait.resume != unsafe.Pointer(plan) ||
		wait.work != waitSetWorkResolving || plan.phase != resumeCleanupConfirm ||
		plan.kind != ResumeCleanupKeyedPark || plan.count != 1 || plan.runtime != 1 ||
		plan.index != 0 || plan.claim != nil || !validTrustedResumeCleanupPlanState(wait, plan) {
		return false
	}
	finalized, cleanupOK := advanceResumeCleanupCore(&driver.sources, p, wait, plan)
	if !cleanupOK || !finalized || wait.resumeKind != resumeBindingMaterialized ||
		wait.work != waitSetWorkResolving || wait.g.park.phase != parkMaterialized ||
		!promoteReadyWaitSet(&driver.sources, p, wait) {
		return false
	}
	*completion = OwnerLocalManualCompletion{}
	return true
}
