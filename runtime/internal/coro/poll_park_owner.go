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

// CurrentExecutorPollDriver resolves the exact retained poll source during
// the compiler's narrow SuspendPark/FrameSuspended transition window. It adds
// only the typed source proof to currentExecutorParkDriver's common exact
// G/P/frame/executor validation.
func CurrentExecutorPollDriver(g *G) (*ExecutorDriver, ExecutorHandle, RouteID, bool) {
	driver, handle, route, ok := currentExecutorParkDriver(g)
	if !ok || driver.sources.poll == nil || driver.sources.poll.owner != driver.p ||
		driver.sources.poll.route != route {
		return nil, ExecutorHandle{}, 0, false
	}
	return driver, handle, route, true
}

func currentExecutorPollSource(driver *ExecutorDriver, g *G) (*PollOperationSource, bool) {
	if driver == nil || !ValidG(g) || g.active == nil {
		return nil, false
	}
	current, _, _, ok := CurrentExecutorPollDriver(g)
	if !ok || current != driver {
		return nil, false
	}
	return driver.sources.poll, true
}

// PrepareSinglePollPark installs one fd-direction operation into the current
// compiler-owned ParkState. The retained reactor discovers it later through a
// pointer-free snapshot; no kernel callback or Go pointer becomes reachable
// during this transaction.
func PrepareSinglePollPark(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	source *PollOperationSource,
	wait *WaitSetRecord,
	caseID uint32,
	seed uint32,
	fd int32,
	interest PollInterest,
	deadline int64,
) (ParkTicket, PollOperationHandle, OperationID, bool) {
	if !ValidG(g) || handle == nil || header == nil || source == nil || wait == nil ||
		*wait != (WaitSetRecord{}) || caseID == 0 || fd < 0 || !validPollInterest(interest) || deadline < 0 ||
		!resumeGateTaken(g) || g.runP == nil || source.owner != g.runP ||
		!CanReservePollOperationV2(g.runP, source) {
		return ParkTicket{}, PollOperationHandle{}, OperationID{}, false
	}
	ticket, ok := BeginParkSet(&g.park, 1, seed)
	if !ok || !PrepareWaitSetRecord(wait, g, ticket) {
		return ParkTicket{}, PollOperationHandle{}, OperationID{}, false
	}
	poll, operation, ok := source.ReserveAndAttachPollOperationV2(
		g.runP, &g.park, ticket, wait, caseID, fd, interest, deadline,
	)
	if !ok || !SealParkSet(&g.park, ticket) || !PrepareParkSet(g, handle, header, ticket, wait) {
		return ParkTicket{}, PollOperationHandle{}, OperationID{}, false
	}
	return ticket, poll, operation, true
}

// PrepareCurrentExecutorPollPark binds a frame-local park to the exact source
// and executor generation resolved from its current G. executor and operation
// are the only source identities retained across suspension.
func PrepareCurrentExecutorPollPark(
	driver *ExecutorDriver,
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	caseID uint32,
	seed uint32,
	fd int32,
	interest PollInterest,
	deadline int64,
) (ParkTicket, PollOperationHandle, OperationID, ExecutorHandle, bool) {
	source, ok := currentExecutorPollSource(driver, g)
	if !ok {
		return ParkTicket{}, PollOperationHandle{}, OperationID{}, ExecutorHandle{}, false
	}
	ticket, poll, operation, ok := PrepareSinglePollPark(
		g, handle, header, source, wait, caseID, seed, fd, interest, deadline,
	)
	return ticket, poll, operation, driver.handle, ok
}

// FinishSinglePollPark consumes or discards the exact selected result, then
// recycles its physical source generation. A cancellation resolved before a
// readiness fact carries no lease and therefore returns Invalid with ok=true.
func FinishSinglePollPark(
	g *G,
	source *PollOperationSource,
	poll PollOperationHandle,
	operation OperationID,
	lease OperationResultLease,
	discard bool,
) (PollOperationResult, bool) {
	if !ValidG(g) || !resumeGateTaken(g) || g.runP == nil || source == nil ||
		source.owner != g.runP || !operation.Valid() || operation.Source() != OperationSourcePoll {
		return PollOperationResultInvalid, false
	}
	want, ok := pollOperationIDFor(source, poll.Slot-1, poll.Generation)
	if !ok || want != operation || operation.Route() != source.route {
		return PollOperationResultInvalid, false
	}
	result := PollOperationResultInvalid
	if lease.Valid() {
		leaseID, leaseOK := lease.ID()
		if !leaseOK || leaseID != operation {
			return PollOperationResultInvalid, false
		}
		if discard {
			result, ok = source.DiscardPollOperationV2Result(g.runP, poll, lease)
		} else {
			result, ok = source.TakePollOperationV2Result(g.runP, poll, lease)
		}
		if !ok || result == PollOperationResultInvalid {
			return PollOperationResultInvalid, false
		}
	} else if !discard {
		return PollOperationResultInvalid, false
	}
	if !source.RecyclePollOperationV2(g.runP, poll) {
		return PollOperationResultInvalid, false
	}
	return result, true
}

// FinishCurrentExecutorPollPark proves resume occurred on the same exact
// executor/source route before releasing frame-owned storage.
func FinishCurrentExecutorPollPark(
	driver *ExecutorDriver,
	g *G,
	executor ExecutorHandle,
	poll PollOperationHandle,
	operation OperationID,
	lease OperationResultLease,
	discard bool,
) (PollOperationResult, bool) {
	source, ok := currentExecutorPollSource(driver, g)
	if !ok || executor != driver.handle || operation.Route() != driver.route {
		return PollOperationResultInvalid, false
	}
	return FinishSinglePollPark(g, source, poll, operation, lease, discard)
}
