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

// WorkerParkFinishResult identifies the exact owner-side retirement phase.
// The operation is fail-stop once ConfirmQuiesced succeeds, so a boolean would
// hide whether a rare invariant failure preceded or followed that boundary.
type WorkerParkFinishResult uint8

const (
	WorkerParkFinishInvalid WorkerParkFinishResult = iota
	WorkerParkFinishComplete
	WorkerParkFinishContextInvalid
	WorkerParkFinishLeaseInvalid
	WorkerParkFinishNotQuiesced
	WorkerParkFinishResultReleaseFailed
	WorkerParkFinishRecycleFailed
)

func (result WorkerParkFinishResult) Finished() bool {
	return result == WorkerParkFinishComplete
}

// CurrentExecutorWorkerDriver resolves the exact worker source owner during
// the narrow compiler worker-hook window. Unlike CurrentExecutorDriver, this
// entry deliberately requires the active frame to have already published its
// SuspendPark/FrameSuspended header: the compiler calls the park hook after
// that publication, and calls the resume cleanup hook before restoring the
// header to FrameActive.
//
// The resume gate, G/P/action, physical handle/header, executor generation,
// route, and worker-source owner must all agree. The returned driver is an
// owner-thread capability only; it must not be retained across suspension or
// placed in a worker job. The owner may use the returned executor handle for
// bounded backend admission; a backend job retains only the OperationID
// produced by PrepareCurrentExecutorWorkerPark and by-value scalar arguments.
func CurrentExecutorWorkerDriver(g *G) (*ExecutorDriver, ExecutorHandle, RouteID, bool) {
	driver, handle, route, ok := currentExecutorParkDriver(g)
	if !ok || driver.sources.worker == nil || !validWorkerOperationOwner(driver.sources.worker, driver.p) ||
		driver.sources.worker.route != route {
		return nil, ExecutorHandle{}, 0, false
	}
	return driver, handle, route, true
}

// ExecutorWorkerPhysicalCancelRequested is the target-adapter view of one
// exact submitted operation. It keeps P and source ownership private while
// allowing a host transport to mirror the common logical cancel bit into its
// own physical request catalog after a scheduler source pass.
func ExecutorWorkerPhysicalCancelRequested(
	driver *ExecutorDriver,
	id OperationID,
) (requested bool, ok bool) {
	if !validExecutorDriver(driver) || driver.sources.worker == nil ||
		id.Route() != driver.route {
		return false, false
	}
	return WorkerOperationPhysicalCancelRequested(
		driver.sources.worker,
		driver.p,
		id,
	)
}

func currentExecutorWorkerSource(
	driver *ExecutorDriver,
	g *G,
) (*WorkerOperationSource, bool) {
	if driver == nil || !ValidG(g) || g.active == nil {
		return nil, false
	}
	current, _, _, ok := CurrentExecutorWorkerDriver(g)
	if !ok || current != driver {
		return nil, false
	}
	return driver.sources.worker, true
}

// PrepareCurrentExecutorWorkerPark is the route-exact owner entry. It selects
// the worker catalog through the current G's P/driver binding rather than a
// process-global source. Target fleet construction and completion routing are
// separate adapter responsibilities.
func PrepareCurrentExecutorWorkerPark(
	driver *ExecutorDriver,
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	source, ok := currentExecutorWorkerSource(driver, g)
	if !ok || handle == nil || header == nil || g.active == nil ||
		g.active.handle != handle || g.active.header != header {
		return ParkTicket{}, OperationID{}, false
	}
	return PrepareSingleWorkerPark(g, handle, header, source, wait, caseID, seed)
}

// PrepareCurrentExecutorWorkerParkCompiler fuses the compiler-generated
// one-worker transaction through packet binding and backend-submission commit.
// The runtime has already resolved driver with CurrentExecutorWorkerDriver and
// reserved its bounded physical queue in the same no-suspend interval.
//
// Ordinary operation builds and audits one exact ParkLink, selects one source
// slot, and publishes one packet without re-entering the generic N-way park
// layers. A pending task stop retains the complete cancellation-aware path.
func PrepareCurrentExecutorWorkerParkCompiler(
	driver *ExecutorDriver,
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	packet *ResumePacket,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	if driver == nil || g == nil || packet == nil || *packet != (ResumePacket{}) ||
		driver.magic != executorDriverMagic || driver.state != executorDriverActive ||
		driver.p == nil || driver.p != g.runP || driver.p.executor != driver ||
		driver.p.current != g || driver.sources.worker == nil || caseID == 0 {
		return ParkTicket{}, OperationID{}, false
	}
	p, source := driver.p, driver.sources.worker
	if !validWorkerOperationOwner(source, p) || source.route != driver.route {
		return ParkTicket{}, OperationID{}, false
	}

	// CommitParkSet must translate a stop which arrived before this hook into
	// logical cancellation, so that uncommon shape deliberately keeps every
	// generic proof. The packet and submission still become one ABI result.
	if g.park.taskCancelKind != TaskCancelNone || g.park.taskCancelPhase != taskCancelIdle {
		ticket, id, ok := PrepareSingleWorkerPark(
			g, handle, header, source, wait, caseID, seed,
		)
		if !ok || !BindSingleWaitSetResumePacket(wait, packet, id) ||
			!source.MarkSubmitted(p, id) {
			return ParkTicket{}, OperationID{}, false
		}
		return ticket, id, true
	}

	prepared, ok := preflightSingleParkPreparation(g, handle, header, wait, seed)
	if !ok || prepared.p != p {
		return ParkTicket{}, OperationID{}, false
	}
	reservation, ok := source.preflightWorkerReservationOwned(p)
	if !ok {
		return ParkTicket{}, OperationID{}, false
	}
	prepared.begin()
	id, ok := source.reserveAndAttachWorkerSlot(
		&g.park, prepared.ticket, wait, caseID, reservation,
	)
	if !ok || !prepared.commit(id, caseID) {
		return ParkTicket{}, OperationID{}, false
	}
	installSingleWaitSetResumePacket(wait, packet, id)
	if !source.markWorkerReservationSubmitted(p, reservation, id) {
		return ParkTicket{}, OperationID{}, false
	}
	return prepared.ticket, id, true
}

// PrepareCurrentExecutorWorkerTimerPark installs one external operation and,
// when deadline is non-zero, one absolute timer in the same ParkSet. A zero
// deadline keeps the operation controllable without manufacturing an infinite
// physical alarm. For a real deadline, either physical completion or the timer
// wins, and the ordinary source-resolution pass requests physical cancellation
// of the losing submitted worker operation before the G may resume.
func PrepareCurrentExecutorWorkerTimerPark(
	driver *ExecutorDriver,
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	workerCaseID uint32,
	timerCaseID uint32,
	seed uint32,
	deadline int64,
) (
	ticket ParkTicket,
	worker OperationID,
	timer TimerRegistrationHandle,
	timerOperation OperationID,
	executor ExecutorHandle,
	ok bool,
) {
	workerSource, workerOK := currentExecutorWorkerSource(driver, g)
	timerTable, timerOK := currentExecutorTimerTable(driver, g)
	hasTimer := deadline > 0
	if !workerOK || !timerOK || handle == nil || header == nil || wait == nil ||
		*wait != (WaitSetRecord{}) || workerCaseID == 0 || timerCaseID == 0 ||
		workerCaseID == timerCaseID || deadline < 0 || g.active == nil ||
		g.active.handle != handle || g.active.header != header ||
		hasTimer && !CanReserveTimerV2(g.runP, timerTable) {
		return ParkTicket{}, OperationID{}, TimerRegistrationHandle{}, OperationID{}, ExecutorHandle{}, false
	}
	if !CanReserveWorkerOperation(g.runP, workerSource) {
		return ParkTicket{}, OperationID{}, TimerRegistrationHandle{}, OperationID{}, ExecutorHandle{}, false
	}
	expected := uint32(1)
	if hasTimer {
		expected = 2
	}
	ticket, ok = BeginParkSet(&g.park, expected, seed)
	if !ok || !PrepareWaitSetRecord(wait, g, ticket) {
		return ParkTicket{}, OperationID{}, TimerRegistrationHandle{}, OperationID{}, ExecutorHandle{}, false
	}
	worker, ok = workerSource.ReserveAndAttachWait(
		g.runP, &g.park, ticket, wait, workerCaseID,
	)
	if !ok {
		return ParkTicket{}, OperationID{}, TimerRegistrationHandle{}, OperationID{}, ExecutorHandle{}, false
	}
	if hasTimer {
		timer, ok = timerTable.ReserveAndAttachTimerV2(
			g.runP, &g.park, ticket, wait, timerCaseID, deadline,
		)
		var timerIDOK bool
		timerOperation, timerIDOK = timerRegistrationIDForHandle(timerTable, timer)
		if !ok || !timerIDOK {
			return ticket, worker, timer, timerOperation, driver.handle, false
		}
	}
	if !SealParkSet(&g.park, ticket) ||
		!PrepareParkSet(g, handle, header, ticket, wait) {
		// Both sources were allocation-free preflighted on the sole owner P.
		// A failure after the first attachment is therefore corruption rather
		// than ordinary capacity pressure; retain it fail-closed for the caller.
		return ticket, worker, timer, timerOperation, driver.handle, false
	}
	return ticket, worker, timer, timerOperation, driver.handle, true
}

// CommitCurrentExecutorWorkerSubmission records the irreversible backend
// handoff only against the worker source bound to g's current driver.
func CommitCurrentExecutorWorkerSubmission(
	driver *ExecutorDriver,
	g *G,
	id OperationID,
) bool {
	source, ok := currentExecutorWorkerSource(driver, g)
	return ok && CommitWorkerSubmission(g, source, id)
}

// RequestCurrentExecutorWorkerParkCancel closes a configuration-change race
// while the compiler-owned ParkState is still preparing or sealed. Unlike
// RequestCancelID, it does not require an already active WaitSetRecord: the
// scheduler's mandatory initial affected visit observes the sticky
// ParkCancelOperation immediately after CommitParkSet.
func RequestCurrentExecutorWorkerParkCancel(
	driver *ExecutorDriver,
	g *G,
	id OperationID,
	ticket ParkTicket,
) bool {
	source, ok := currentExecutorWorkerSource(driver, g)
	if !ok || !id.Valid() || ticket == (ParkTicket{}) {
		return false
	}
	slot, slotOK := workerOperationSlotFor(source, id)
	return slotOK && preemptLoad(&slot.generation) == id.Generation &&
		preemptLoad(&slot.state) == uint32(producerSourceActive) &&
		slot.submitted && slot.record.Matches(id) &&
		slot.record.phase == operationActive &&
		slot.record.link.park == &g.park &&
		slot.record.link.ticket == ticket &&
		RequestParkCancel(&g.park, ticket, ParkCancelOperation)
}

// FinishCurrentExecutorWorkerPark strongly retires the exact current owner's
// worker generation after completion or cancellation cleanup.
func FinishCurrentExecutorWorkerPark(
	driver *ExecutorDriver,
	g *G,
	id OperationID,
	lease OperationResultLease,
	discard bool,
	out *ScalarResultPayloadV1,
) WorkerParkFinishResult {
	source, ok := currentExecutorWorkerSource(driver, g)
	if !ok {
		return WorkerParkFinishContextInvalid
	}
	return FinishSingleWorkerPark(g, source, id, lease, discard, out)
}

// PrepareSingleWorkerPark installs one irreversible worker completion in the
// current compiler-owned ParkState. The caller must still make the backend
// submission durable and call CommitWorkerSubmission before executing
// llvm.coro.suspend. wait lives in the coroutine frame; the producer receives
// only the returned pointer-free OperationID.
func PrepareSingleWorkerPark(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	source *WorkerOperationSource,
	wait *WaitSetRecord,
	caseID uint32,
	seed uint32,
) (ParkTicket, OperationID, bool) {
	if !ValidG(g) || handle == nil || header == nil || source == nil || wait == nil ||
		*wait != (WaitSetRecord{}) || caseID == 0 || !resumeGateTaken(g) || g.runP == nil ||
		!CanReserveWorkerOperation(g.runP, source) {
		return ParkTicket{}, OperationID{}, false
	}
	ticket, ok := BeginParkSet(&g.park, 1, seed)
	if !ok || !PrepareWaitSetRecord(wait, g, ticket) {
		return ParkTicket{}, OperationID{}, false
	}
	id, ok := source.ReserveAndAttachWait(g.runP, &g.park, ticket, wait, caseID)
	if !ok || !SealParkSet(&g.park, ticket) || !PrepareParkSet(g, handle, header, ticket, wait) {
		return ParkTicket{}, OperationID{}, false
	}
	return ticket, id, true
}

// CommitWorkerSubmission records the no-return backend handoff. A backend must
// preflight its bounded queue before calling this function; once it succeeds,
// failure to enqueue is a runtime invariant violation and must fail-stop rather
// than exposing a worker generation with no future physical fact.
func CommitWorkerSubmission(g *G, source *WorkerOperationSource, id OperationID) bool {
	return ValidG(g) && resumeGateTaken(g) && g.runP != nil &&
		validWorkerOperationOwner(source, g.runP) && source.MarkSubmitted(g.runP, id)
}

// FinishSingleWorkerPark releases one result/cancellation only after the
// source apply phase has observed physical completion, detached the ParkLink,
// and the compiler resume gate has taken the exact ticket decision.
func FinishSingleWorkerPark(
	g *G,
	source *WorkerOperationSource,
	id OperationID,
	lease OperationResultLease,
	discard bool,
	out *ScalarResultPayloadV1,
) WorkerParkFinishResult {
	if !ValidG(g) || !resumeGateTaken(g) || g.runP == nil || source == nil || !id.Valid() ||
		!validWorkerOperationOwner(source, g.runP) || discard && out != nil || !discard && lease.Valid() && out == nil {
		return WorkerParkFinishContextInvalid
	}
	if lease.Valid() {
		leaseID, ok := lease.ID()
		if !ok || leaseID != id {
			return WorkerParkFinishLeaseInvalid
		}
	}
	p := g.runP
	if !source.ConfirmQuiesced(p, id) {
		return WorkerParkFinishNotQuiesced
	}
	if lease.Valid() {
		var released bool
		if discard {
			released = source.DiscardResult(p, lease)
		} else {
			released = source.TakeResult(p, lease, out)
		}
		if !released {
			return WorkerParkFinishResultReleaseFailed
		}
	} else if out != nil {
		return WorkerParkFinishLeaseInvalid
	}
	if !source.Recycle(p, id) {
		return WorkerParkFinishRecycleFailed
	}
	return WorkerParkFinishComplete
}
