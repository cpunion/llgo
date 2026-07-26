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

// ResumeResultKind is the closed pointer-free result union retained in a
// compiler-provided ResumePacket. Typed channel element data already lives in
// frame slots; ResumeResultChannel carries only the selected operation status.
type ResumeResultKind uint8

const (
	ResumeResultNone ResumeResultKind = iota
	ResumeResultScalar
	ResumeResultPoll
	ResumeResultChannel
)

const ResumeSmallInvalid uint8 = 0

type resumePacketState uint8

const (
	resumePacketEmpty resumePacketState = iota
	resumePacketBound
	resumePacketMaterialized
)

type resumeBindingKind uint8

const (
	resumeBindingNone resumeBindingKind = iota
	resumeBindingSingle
	resumeBindingCleanup
	resumeBindingMaterialized
)

// ResumePacket is stable storage in the direct-parking LLVM coroutine frame.
// Bound temporarily identifies one exact old-route source generation.
// Materialized is P-neutral: source is zero, any payload has been copied by the
// original owner, and the old source result lease/generation no longer exists.
//
// The object is POD and has the same 52-byte/alignment-4 layout on native,
// WASM32, embedded, and bare-metal targets. It is not a Future, callback
// object, registry entry, or permanent G field.
type ResumePacket struct {
	ticket  ParkTicket
	source  OperationID
	scalar  ScalarResultPayloadV1
	caseID  uint32
	outcome ParkOutcome
	result  ResumeResultKind
	small   uint8
	state   resumePacketState
}

var (
	_ [52 - unsafe.Sizeof(ResumePacket{})]byte
	_ [unsafe.Sizeof(ResumePacket{}) - 52]byte
	_ [4 - unsafe.Alignof(ResumePacket{})]byte
	_ [unsafe.Alignof(ResumePacket{}) - 4]byte
)

func supportedSingleResumeSource(id OperationID) bool {
	if id == (OperationID{}) {
		return true
	}
	if !id.Valid() {
		return false
	}
	switch id.Source() {
	case OperationSourceTimer, OperationSourceManual, OperationSourcePoll, OperationSourceWorker:
		return true
	default:
		return false
	}
}

func executorSupportsSingleResumeSource(sources *ExecutorSourceSet, id OperationID) bool {
	if sources == nil {
		return false
	}
	if id == (OperationID{}) {
		return true
	}
	if !id.Valid() || id.Route() != sources.route {
		return false
	}
	switch id.Source() {
	case OperationSourceTimer:
		return sources.timers != nil
	case OperationSourceManual:
		return sources.manual != nil
	case OperationSourcePoll:
		return sources.poll != nil
	case OperationSourceWorker:
		return sources.worker != nil
	default:
		return false
	}
}

func validBoundResumePacket(packet *ResumePacket, ticket ParkTicket) bool {
	return packet != nil && packet.state == resumePacketBound && packet.ticket == ticket &&
		validParkTicket(ticket) && supportedSingleResumeSource(packet.source) &&
		packet.scalar == (ScalarResultPayloadV1{}) && packet.caseID == 0 &&
		packet.outcome == ParkOutcomePending && packet.result == ResumeResultNone &&
		packet.small == ResumeSmallInvalid
}

func validMaterializedResumePacket(packet *ResumePacket) bool {
	if packet == nil || packet.state != resumePacketMaterialized || !validParkTicket(packet.ticket) ||
		packet.source != (OperationID{}) {
		return false
	}
	switch packet.outcome {
	case ParkOutcomeCompleted, ParkOutcomeDefault:
		if packet.caseID == 0 {
			return false
		}
	case ParkOutcomeCanceled:
		if packet.caseID != 0 {
			return false
		}
	default:
		return false
	}
	if packet.outcome != ParkOutcomeCompleted {
		return packet.result == ResumeResultNone && packet.scalar == (ScalarResultPayloadV1{}) &&
			packet.small == ResumeSmallInvalid
	}
	switch packet.result {
	case ResumeResultNone:
		return packet.scalar == (ScalarResultPayloadV1{}) && packet.small == ResumeSmallInvalid
	case ResumeResultScalar:
		return packet.scalar.Valid() && packet.small == ResumeSmallInvalid
	case ResumeResultPoll:
		return packet.scalar == (ScalarResultPayloadV1{}) &&
			PollOperationResult(packet.small) >= PollOperationReady &&
			PollOperationResult(packet.small) <= PollOperationTimeout
	case ResumeResultChannel:
		return packet.scalar == (ScalarResultPayloadV1{}) && packet.small != ResumeSmallInvalid
	default:
		return false
	}
}

func validWaitSetResumeBinding(record *WaitSetRecord) bool {
	if record == nil {
		return false
	}
	switch record.resumeKind {
	case resumeBindingNone:
		return record.resume == nil
	case resumeBindingSingle:
		return validBoundResumePacket((*ResumePacket)(record.resume), record.ticket)
	case resumeBindingCleanup:
		return validResumeCleanupPlan(record, (*ResumeCleanupPlan)(record.resume))
	case resumeBindingMaterialized:
		return validMaterializedResumePacket((*ResumePacket)(record.resume))
	default:
		return false
	}
}

// BindSingleWaitSetResumePacket opts one zero/one-source direct park into
// owner-side result materialization. It runs after PrepareParkSet committed the
// exact candidate relation and before Resumed publishes the G as waiting.
//
// Multi-event select uses a later range plan because every loser generation
// and typed hchan queue node must be retired before transfer; accepting it here
// would make a partially neutral G look stealable.
func BindSingleWaitSetResumePacket(record *WaitSetRecord, packet *ResumePacket, source OperationID) bool {
	if record == nil || packet == nil || *packet != (ResumePacket{}) ||
		record.resume != nil || record.resumeKind != resumeBindingNone ||
		record.state != waitSetRecordCommitted || record.work != waitSetWorkIdle ||
		record.activePrev != nil || record.activeNext != nil || record.workNext != nil ||
		record.g == nil || !ValidG(record.g) || !validParkTicket(record.ticket) ||
		record.g.park.ticket != record.ticket || record.g.park.phase != parkParked ||
		record.g.pending.kind != pendingParkSet || record.g.pending.from != record.g.active ||
		record.g.active == nil || record.g.active.parkWait != record ||
		!supportedSingleResumeSource(source) {
		return false
	}
	p := record.g.runP
	if p == nil || !validExecutorDriverForP(p.executor, p) || p.current != record.g ||
		!executorSupportsSingleResumeSource(&p.executor.sources, source) {
		return false
	}
	state := &record.g.park
	if source == (OperationID{}) {
		if state.expected != 0 || state.attached != 0 || state.head != nil {
			return false
		}
	} else if state.expected != 1 || state.attached != 1 || state.head == nil ||
		state.head.next != nil || state.head.operation == nil || state.head.operation.id != source {
		return false
	}
	*packet = ResumePacket{
		ticket: record.ticket,
		source: source,
		state:  resumePacketBound,
	}
	record.resume = unsafe.Pointer(packet)
	record.resumeKind = resumeBindingSingle
	return true
}

func materializeManualResume(
	source *ManualOperationSource,
	p *P,
	id OperationID,
	lease OperationResultLease,
	discard bool,
) bool {
	if source == nil || !source.ConfirmQuiesced(p, id) {
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
	return source.Recycle(p, id)
}

func materializeWorkerResume(
	source *WorkerOperationSource,
	p *P,
	id OperationID,
	lease OperationResultLease,
	discard bool,
	out *ScalarResultPayloadV1,
) bool {
	if source == nil || !source.ConfirmQuiesced(p, id) {
		return false
	}
	if lease.Valid() {
		if discard {
			if out != nil || !source.DiscardResult(p, lease) {
				return false
			}
		} else if out == nil || !source.TakeResult(p, lease, out) {
			return false
		}
	} else if out != nil {
		return false
	}
	return source.Recycle(p, id)
}

func materializeTimerResume(
	table *TimerRegistrationTable,
	p *P,
	id OperationID,
	lease OperationResultLease,
	discard bool,
) bool {
	if table == nil {
		return false
	}
	handle := TimerRegistrationHandle{Slot: id.LocalSlot(), Generation: id.Generation}
	if lease.Valid() {
		if discard {
			if !table.DiscardTimerV2Result(p, handle, lease) {
				return false
			}
		} else if !table.TakeTimerV2Result(p, handle, lease) {
			return false
		}
	}
	return table.RecycleTimerV2(p, handle)
}

func materializePollResume(
	source *PollOperationSource,
	p *P,
	id OperationID,
	lease OperationResultLease,
	discard bool,
	out *PollOperationResult,
) bool {
	if source == nil {
		return false
	}
	handle := PollOperationHandle{Slot: id.LocalSlot(), Generation: id.Generation}
	if lease.Valid() {
		var (
			result PollOperationResult
			ok     bool
		)
		if discard {
			if out != nil {
				return false
			}
			result, ok = source.DiscardPollOperationV2Result(p, handle, lease)
		} else {
			if out == nil {
				return false
			}
			result, ok = source.TakePollOperationV2Result(p, handle, lease)
		}
		if !ok {
			return false
		}
		if out != nil {
			*out = result
		}
	} else if out != nil {
		return false
	}
	return source.RecyclePollOperationV2(p, handle)
}

// materializeSingleResumePacket executes on the original source owner after
// logical detach and before ready-queue publication. No source identity is
// written to the packet after cleanup succeeds.
func materializeSingleResumePacket(sources *ExecutorSourceSet, p *P, record *WaitSetRecord) bool {
	if sources == nil || !validExecutorSourceSet(sources, p) || record == nil ||
		!validActiveWaitSetRecordFast(p, record) || record.work != waitSetWorkResolving ||
		record.g.park.phase != parkReady || record.resumeKind != resumeBindingSingle ||
		!validBoundResumePacket((*ResumePacket)(record.resume), record.ticket) {
		return false
	}
	packet, state := (*ResumePacket)(record.resume), &record.g.park
	sourceID := packet.source
	physicalOutcome := state.outcome
	if physicalOutcome == ParkOutcomeCompleted {
		if !sourceID.Valid() || state.winnerID != sourceID {
			return false
		}
	} else if physicalOutcome != ParkOutcomeCanceled && physicalOutcome != ParkOutcomeDefault {
		return false
	}

	outcome, caseID, lease, ok := ConsumeParkSet(state, record.ticket)
	if !ok || physicalOutcome == ParkOutcomeCompleted && !lease.Valid() ||
		physicalOutcome != ParkOutcomeCompleted && lease.Valid() {
		return false
	}
	switch outcome {
	case ParkOutcomeCompleted, ParkOutcomeDefault:
		if caseID == 0 {
			return false
		}
	case ParkOutcomeCanceled:
		if caseID != 0 {
			return false
		}
	default:
		return false
	}
	discard := outcome != ParkOutcomeCompleted
	var (
		result ResumeResultKind
		scalar ScalarResultPayloadV1
		small  uint8
	)
	if sourceID == (OperationID{}) {
		if (physicalOutcome != ParkOutcomeCanceled && physicalOutcome != ParkOutcomeDefault) || lease.Valid() {
			return false
		}
	} else {
		switch sourceID.Source() {
		case OperationSourceManual:
			if !materializeManualResume(sources.manual, p, sourceID, lease, discard) {
				return false
			}
		case OperationSourceWorker:
			var out *ScalarResultPayloadV1
			if !discard {
				out = &scalar
			}
			if !materializeWorkerResume(sources.worker, p, sourceID, lease, discard, out) {
				return false
			}
			if !discard {
				result = ResumeResultScalar
			}
		case OperationSourceTimer:
			if !materializeTimerResume(sources.timers, p, sourceID, lease, discard) {
				return false
			}
		case OperationSourcePoll:
			var (
				poll PollOperationResult
				out  *PollOperationResult
			)
			if !discard {
				out = &poll
			}
			if !materializePollResume(sources.poll, p, sourceID, lease, discard, out) {
				return false
			}
			if !discard {
				result = ResumeResultPoll
				small = uint8(poll)
			}
		default:
			return false
		}
	}
	if physicalOutcome == ParkOutcomeCompleted && state.phase != parkConsumed {
		return false
	}
	if !materializedParkState(state, record.ticket, outcome, caseID) {
		return false
	}
	*packet = ResumePacket{
		ticket:  record.ticket,
		scalar:  scalar,
		caseID:  caseID,
		outcome: outcome,
		result:  result,
		small:   small,
		state:   resumePacketMaterialized,
	}
	record.resumeKind = resumeBindingMaterialized
	return validMaterializedResumePacket(packet)
}

// TakeResumePacket is the typed resume prologue for packet-backed parks. It
// consumes the P-owned logical decision and frame-local result exactly once.
// A task stop selected after migration suppresses the copied payload without
// consulting or touching the old source.
func TakeResumePacket(
	g *G,
	expected ParkTicket,
	packet *ResumePacket,
	scalarOut *ScalarResultPayloadV1,
) (
	outcome ParkOutcome,
	caseID uint32,
	task TaskCancelKind,
	result ResumeResultKind,
	small uint8,
	ok bool,
) {
	if !ValidG(g) || g.runP == nil || !validMaterializedResumePacket(packet) ||
		packet.ticket != expected {
		return ParkOutcomePending, 0, TaskCancelNone, ResumeResultNone, ResumeSmallInvalid, false
	}
	p := g.runP
	if p.current != g || !p.inResume || g.state != GRunning || p.runDecisionTaken ||
		!expectedAction(p, g, p.action, ActionResume) || !validRunDecision(p.runDecision) {
		return ParkOutcomePending, 0, TaskCancelNone, ResumeResultNone, ResumeSmallInvalid, false
	}
	decision := &p.runDecision
	if !decision.materialized || decision.g != g || decision.ticket != expected ||
		g.park.phase != parkMaterialized || g.park.ticket != expected ||
		packet.outcome != g.park.outcome || packet.caseID != g.park.winnerCase {
		return ParkOutcomePending, 0, TaskCancelNone, ResumeResultNone, ResumeSmallInvalid, false
	}
	var scalar ScalarResultPayloadV1
	if decision.outcome == ParkOutcomeCompleted {
		if decision.caseID != packet.caseID {
			return ParkOutcomePending, 0, TaskCancelNone, ResumeResultNone, ResumeSmallInvalid, false
		}
		switch packet.result {
		case ResumeResultScalar:
			if scalarOut == nil {
				return ParkOutcomePending, 0, TaskCancelNone, ResumeResultNone, ResumeSmallInvalid, false
			}
			scalar = packet.scalar
		case ResumeResultPoll, ResumeResultChannel, ResumeResultNone:
			if scalarOut != nil {
				return ParkOutcomePending, 0, TaskCancelNone, ResumeResultNone, ResumeSmallInvalid, false
			}
		default:
			return ParkOutcomePending, 0, TaskCancelNone, ResumeResultNone, ResumeSmallInvalid, false
		}
		result, small = packet.result, packet.small
	}
	if !deliverMaterializedParkResume(&g.park, expected) {
		return ParkOutcomePending, 0, TaskCancelNone, ResumeResultNone, ResumeSmallInvalid, false
	}
	if scalarOut != nil {
		*scalarOut = scalar
	}
	outcome, caseID, task = decision.outcome, decision.caseID, decision.task
	*packet = ResumePacket{}
	p.runDecision = RunDecision{}
	p.runDecisionTaken = true
	return outcome, caseID, task, result, small, true
}
