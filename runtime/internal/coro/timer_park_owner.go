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

// CurrentExecutorTimerDriver resolves the exact timer source owner during the
// narrow compiler park/resume-hook window. The current physical frame has
// already published SuspendPark/FrameSuspended, but llvm.coro.suspend (or the
// post-resume activation) has not completed yet. This is intentionally
// separate from CurrentExecutorDriver, whose ordinary owner-call contract
// requires an active frame header.
//
// The returned driver is an owner-thread capability and must not cross the
// suspension. A frame retains only the pointer-free executor handle plus the
// routed timer OperationID returned by PrepareCurrentExecutorTimerPark.
func CurrentExecutorTimerDriver(g *G) (*ExecutorDriver, ExecutorHandle, RouteID, bool) {
	driver, handle, route, ok := currentExecutorParkDriver(g)
	if !ok || driver.sources.timers == nil || driver.sources.timers.owner != driver.p ||
		driver.sources.timers.route != route {
		return nil, ExecutorHandle{}, 0, false
	}
	return driver, handle, route, true
}

func currentExecutorTimerTable(driver *ExecutorDriver, g *G) (*TimerRegistrationTable, bool) {
	if driver == nil || !ValidG(g) || g.active == nil {
		return nil, false
	}
	current, _, _, ok := CurrentExecutorTimerDriver(g)
	if !ok || current != driver {
		return nil, false
	}
	return driver.sources.timers, true
}

// CanReserveTimerV2 is the allocation-free preflight for a compiler-owned
// timer park. It runs before BeginParkSet so ordinary capacity exhaustion or
// generation exhaustion cannot strand a partially prepared logical wait.
func CanReserveTimerV2(p *P, table *TimerRegistrationTable) bool {
	if table == nil || p == nil || table.owner != p || !table.route.Valid() {
		return false
	}
	_, _, ok := nextReusableTimerRegistrationSlot(table)
	return ok
}

// PrepareSingleTimerPark installs one source-aware one-shot timer into the
// current compiler-owned ParkState. Timers have no callback/backend commit:
// after success the caller may immediately execute llvm.coro.suspend.
func PrepareSingleTimerPark(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	table *TimerRegistrationTable,
	wait *WaitSetRecord,
	caseID uint32,
	seed uint32,
	deadline int64,
) (ParkTicket, TimerRegistrationHandle, OperationID, bool) {
	return prepareSingleTimerPark(
		g, handle, header, table, wait, caseID, seed, deadline, 0, nil, 0,
	)
}

// PrepareSingleControlledTimerPark is the source-aware counterpart used by
// the standard time.Timer manager. Its logical controller generation is
// indexed by the timer table for Stop/Reset while the physical operation and
// all cleanup ownership remain in the ordinary V2 ParkSet transaction.
func PrepareSingleControlledTimerPark(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	table *TimerRegistrationTable,
	wait *WaitSetRecord,
	caseID uint32,
	seed uint32,
	deadline int64,
	controller uintptr,
	control *uint32,
	expected uint32,
) (ParkTicket, TimerRegistrationHandle, OperationID, bool) {
	return prepareSingleTimerPark(
		g, handle, header, table, wait, caseID, seed, deadline, controller, control, expected,
	)
}

func prepareSingleTimerPark(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	table *TimerRegistrationTable,
	wait *WaitSetRecord,
	caseID uint32,
	seed uint32,
	deadline int64,
	controller uintptr,
	control *uint32,
	expected uint32,
) (ParkTicket, TimerRegistrationHandle, OperationID, bool) {
	if !ValidG(g) || handle == nil || header == nil || table == nil || wait == nil ||
		*wait != (WaitSetRecord{}) || caseID == 0 || deadline < 0 || !resumeGateTaken(g) ||
		g.runP == nil || table.owner != g.runP || !CanReserveTimerV2(g.runP, table) ||
		(control == nil && (controller != 0 || expected != 0)) ||
		(control != nil && (controller == 0 || expected == 0)) {
		return ParkTicket{}, TimerRegistrationHandle{}, OperationID{}, false
	}
	ticket, ok := BeginParkSet(&g.park, 1, seed)
	if !ok || !PrepareWaitSetRecord(wait, g, ticket) {
		return ParkTicket{}, TimerRegistrationHandle{}, OperationID{}, false
	}
	var timer TimerRegistrationHandle
	if control == nil {
		timer, ok = table.ReserveAndAttachTimerV2(g.runP, &g.park, ticket, wait, caseID, deadline)
	} else {
		timer, ok = table.ReserveAndAttachControlledTimerV2(
			g.runP, &g.park, ticket, wait, caseID, deadline, controller, control, expected,
		)
	}
	id, idOK := timerRegistrationIDForHandle(table, timer)
	if !ok || !idOK {
		return ParkTicket{}, TimerRegistrationHandle{}, OperationID{}, false
	}
	if !SealParkSet(&g.park, ticket) || !PrepareParkSet(g, handle, header, ticket, wait) {
		return ParkTicket{}, TimerRegistrationHandle{}, OperationID{}, false
	}
	return ticket, timer, id, true
}

// PrepareCurrentExecutorTimerPark selects the timer source through the exact
// suspending G/P/driver binding. executor is returned as a pointer-free source
// generation and must be retained beside operation until resume cleanup.
func PrepareCurrentExecutorTimerPark(
	driver *ExecutorDriver,
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	caseID uint32,
	seed uint32,
	deadline int64,
) (ParkTicket, TimerRegistrationHandle, OperationID, ExecutorHandle, bool) {
	table, ok := currentExecutorTimerTable(driver, g)
	if !ok {
		return ParkTicket{}, TimerRegistrationHandle{}, OperationID{}, ExecutorHandle{}, false
	}
	ticket, timer, operation, ok := PrepareSingleTimerPark(
		g, handle, header, table, wait, caseID, seed, deadline,
	)
	return ticket, timer, operation, driver.handle, ok
}

// PrepareCurrentExecutorControlledTimerPark binds the same exact current
// executor/source proof to a controlled standard-library timer generation.
func PrepareCurrentExecutorControlledTimerPark(
	driver *ExecutorDriver,
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	caseID uint32,
	seed uint32,
	deadline int64,
	controller uintptr,
	control *uint32,
	expected uint32,
) (ParkTicket, TimerRegistrationHandle, OperationID, ExecutorHandle, bool) {
	table, ok := currentExecutorTimerTable(driver, g)
	if !ok {
		return ParkTicket{}, TimerRegistrationHandle{}, OperationID{}, ExecutorHandle{}, false
	}
	ticket, timer, operation, ok := PrepareSingleControlledTimerPark(
		g, handle, header, table, wait, caseID, seed, deadline, controller, control, expected,
	)
	return ticket, timer, operation, driver.handle, ok
}

// FinishSingleTimerPark releases a detached timer after the resume gate has
// taken its exact decision. A winner lease is copied or discarded before the
// physical generation becomes reusable; an ordinary canceled timer carries no
// lease and proceeds directly to recycle.
func FinishSingleTimerPark(
	g *G,
	table *TimerRegistrationTable,
	timer TimerRegistrationHandle,
	operation OperationID,
	lease OperationResultLease,
	discard bool,
) bool {
	if !ValidG(g) || !resumeGateTaken(g) || g.runP == nil || table == nil ||
		table.owner != g.runP || !operation.Valid() || operation.Source() != OperationSourceTimer {
		return false
	}
	want, ok := timerRegistrationIDForHandle(table, timer)
	if !ok || want != operation {
		return false
	}
	if lease.Valid() {
		leaseID, leaseOK := lease.ID()
		if !leaseOK || leaseID != operation {
			return false
		}
		if discard {
			if !table.DiscardTimerV2Result(g.runP, timer, lease) {
				return false
			}
		} else if !table.TakeTimerV2Result(g.runP, timer, lease) {
			return false
		}
	}
	return table.RecycleTimerV2(g.runP, timer)
}

// FinishCurrentExecutorTimerPark proves that resume occurs on the same exact
// executor/source generation that prepared the timer before releasing it.
func FinishCurrentExecutorTimerPark(
	driver *ExecutorDriver,
	g *G,
	executor ExecutorHandle,
	timer TimerRegistrationHandle,
	operation OperationID,
	lease OperationResultLease,
	discard bool,
) bool {
	table, ok := currentExecutorTimerTable(driver, g)
	return ok && executor == driver.handle && operation.Route() == driver.route &&
		FinishSingleTimerPark(g, table, timer, operation, lease, discard)
}
