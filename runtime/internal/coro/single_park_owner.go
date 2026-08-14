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

// singleParkPreparation is a stack-only capability for the common compiler
// owned one-event park transaction. Source-specific preflight runs between
// preflightSingleParkPreparation and begin, while no suspension or owner
// transition is possible. Channel, manual, and later single-source adapters
// therefore share one frame/ParkState proof without sharing producer policy.
type singleParkPreparation struct {
	g      *G
	p      *P
	frame  *Frame
	wait   *WaitSetRecord
	ticket ParkTicket
	seed   uint32
}

func preflightSingleParkPreparation(
	g *G,
	handle unsafe.Pointer,
	header *HeaderV1,
	wait *WaitSetRecord,
	seed uint32,
) (singleParkPreparation, bool) {
	if !ValidG(g) || handle == nil || header == nil || wait == nil ||
		*wait != (WaitSetRecord{}) || !resumeGateTaken(g) || g.runP == nil ||
		g.pending.kind != pendingNone || g.spawnChild != nil || g.waiting ||
		!validReusableSingleParkState(&g.park) || g.park.attached != 0 || g.park.head != nil {
		return singleParkPreparation{}, false
	}
	frame := findFrame(g, handle)
	if frame == nil || frame != g.active || frame.header != header || frame.state != FrameActive ||
		frame.parkWait != nil || header.SuspendReason != uint16(SuspendPark) ||
		header.Lifecycle != uint16(FrameSuspended) {
		return singleParkPreparation{}, false
	}
	ticket, ok := nextParkTicket(g.park.ticket)
	if !ok {
		return singleParkPreparation{}, false
	}
	return singleParkPreparation{
		g:      g,
		p:      g.runP,
		frame:  frame,
		wait:   wait,
		ticket: ticket,
		seed:   seed ^ ticket.generation*0x9e3779b9 ^ ticket.epoch*0x85ebca6b,
	}, true
}

func (prepared *singleParkPreparation) begin() {
	prepared.g.park = ParkState{
		ticket:   prepared.ticket,
		phase:    parkPreparing,
		expected: 1,
		seed:     prepared.seed,
	}
	prepared.wait.g = prepared.g
	prepared.wait.ticket = prepared.ticket
	prepared.wait.state = waitSetRecordPreparing
}

func (prepared *singleParkPreparation) commit(id OperationID, caseID uint32) bool {
	state := &prepared.g.park
	state.seed = 0
	state.phase = parkParked
	if !validPreparedDirectChannelParkState(state, prepared.wait, id, caseID) {
		return false
	}
	prepared.wait.state = waitSetRecordCommitted
	prepared.frame.parkWait = prepared.wait
	prepared.g.pending = pendingTransition{kind: pendingParkSet, from: prepared.frame}
	return true
}
