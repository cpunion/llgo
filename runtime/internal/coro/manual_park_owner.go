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
	if !ValidG(g) || handle == nil || header == nil || source == nil || wait == nil ||
		*wait != (WaitSetRecord{}) || caseID == 0 || !resumeGateTaken(g) || g.runP == nil ||
		!validManualOperationOwner(source, g.runP) {
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
