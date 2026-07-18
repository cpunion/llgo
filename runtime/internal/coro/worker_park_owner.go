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
		!validWorkerOperationOwner(source, g.runP) {
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
) bool {
	if !ValidG(g) || !resumeGateTaken(g) || g.runP == nil || source == nil || !id.Valid() ||
		!validWorkerOperationOwner(source, g.runP) || discard && out != nil || !discard && lease.Valid() && out == nil {
		return false
	}
	if lease.Valid() {
		leaseID, ok := lease.ID()
		if !ok || leaseID != id {
			return false
		}
	}
	p := g.runP
	if !source.ConfirmQuiesced(p, id) {
		return false
	}
	if lease.Valid() {
		var released bool
		if discard {
			released = source.DiscardResult(p, lease)
		} else {
			released = source.TakeResult(p, lease, out)
		}
		if !released {
			return false
		}
	} else if out != nil {
		return false
	}
	return source.Recycle(p, id)
}
