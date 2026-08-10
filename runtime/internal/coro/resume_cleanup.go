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

// ResumeCleanupKind is the closed runtime materialization switch. It describes
// only the typed frame nodes which the source-neutral core cannot inspect.
// Source confirmation, result ownership, generation recycle, and runnable
// promotion remain common core phases.
type ResumeCleanupKind uint8

const (
	ResumeCleanupInvalid ResumeCleanupKind = iota
	ResumeCleanupChannelDirect
	ResumeCleanupChannelSelect
	ResumeCleanupHostOperation
	ResumeCleanupHostOperationDeadline
	ResumeCleanupKeyedPark
)

type resumeCleanupPhase uint8

const (
	resumeCleanupIdle resumeCleanupPhase = iota
	resumeCleanupBound
	resumeCleanupRuntime
	resumeCleanupConfirm
	resumeCleanupClaim
	resumeCleanupResult
	resumeCleanupRecycle
	resumeCleanupFinalize
)

// ResumeCleanupBinding is a trusted compiler/runtime description of one
// frame-local typed cleanup. Entries is a logical-case array; an OperationID at
// Entries+i*Stride+IDOffset is zero for a disabled case. Context is interpreted
// only by the direct runtime switch selected by Kind.
//
// The descriptor is consumed at bind time and is never producer-visible.
type ResumeCleanupBinding struct {
	Kind         ResumeCleanupKind
	Context      unsafe.Pointer
	Entries      unsafe.Pointer
	Claim        *SelectClaim
	Count        uint32
	RuntimeCount uint32
	Stride       uintptr
	IDOffset     uintptr
}

// ResumeCleanupPlan is compiler-spilled continuation state for bounded
// owner-side materialization. It is not a callback, interface, Future, heap
// task, or permanent G field. Runtime cleanup advances one typed runtime step
// per ExecutorRunStepMaterialize; every later core call confirms or recycles
// at most one exact source generation. The runtime-step count and physical
// source count are deliberately independent.
type ResumeCleanupPlan struct {
	packet   *ResumePacket
	claim    *SelectClaim
	context  unsafe.Pointer
	entries  unsafe.Pointer
	lease    OperationResultLease
	ticket   ParkTicket
	stride   uintptr
	idOffset uintptr
	count    uint32
	runtime  uint32
	index    uint32
	caseID   uint32
	outcome  ParkOutcome
	kind     ResumeCleanupKind
	phase    resumeCleanupPhase
	result   ResumeResultKind
	small    uint8
}

func validResumeCleanupKind(kind ResumeCleanupKind) bool {
	return kind >= ResumeCleanupChannelDirect && kind <= ResumeCleanupKeyedPark
}

func resumeCleanupUsesChannelClaim(kind ResumeCleanupKind) bool {
	return kind == ResumeCleanupChannelDirect || kind == ResumeCleanupChannelSelect
}

func validResumeCleanupPacket(plan *ResumeCleanupPlan, ticket ParkTicket) bool {
	if plan == nil || plan.packet == nil || plan.packet.state != resumePacketBound ||
		plan.packet.ticket != ticket || !validParkTicket(ticket) ||
		plan.packet.source != (OperationID{}) || plan.packet.caseID != 0 ||
		plan.packet.outcome != ParkOutcomePending ||
		plan.packet.result != ResumeResultNone ||
		plan.packet.small != ResumeSmallInvalid {
		return false
	}
	if plan.result == ResumeResultScalar {
		return (plan.phase == resumeCleanupRecycle || plan.phase == resumeCleanupFinalize) &&
			plan.packet.scalar.Valid()
	}
	return plan.packet.scalar == (ScalarResultPayloadV1{})
}

func validResumeCleanupRange(entries unsafe.Pointer, count uint32, stride, idOffset uintptr) bool {
	if entries == nil || count == 0 || count > MaxSelectOperationCases ||
		stride < unsafe.Sizeof(OperationID{}) || idOffset > stride-unsafe.Sizeof(OperationID{}) {
		return false
	}
	last := uintptr(count - 1)
	return last == 0 || stride <= (^uintptr(0)-idOffset)/last
}

func resumeCleanupIDAt(plan *ResumeCleanupPlan, index uint32) *OperationID {
	if plan == nil || index >= plan.count ||
		!validResumeCleanupRange(plan.entries, plan.count, plan.stride, plan.idOffset) {
		return nil
	}
	return (*OperationID)(unsafe.Add(plan.entries, uintptr(index)*plan.stride+plan.idOffset))
}

func executorSupportsResumeCleanupSource(sources *ExecutorSourceSet, id OperationID) bool {
	if sources == nil || !id.Valid() || id.Route() != sources.route {
		return false
	}
	switch id.Source() {
	case OperationSourceChannel:
		return sources.channel != nil
	case OperationSourceManual:
		return sources.manual != nil
	case OperationSourceWorker:
		return sources.worker != nil
	case OperationSourceTimer:
		return sources.timers != nil
	case OperationSourcePoll:
		return sources.poll != nil
	default:
		return false
	}
}

func validResumeCleanupSourceShape(
	kind ResumeCleanupKind,
	index uint32,
	id OperationID,
) bool {
	switch kind {
	case ResumeCleanupChannelDirect, ResumeCleanupChannelSelect:
		return id == (OperationID{}) || id.Source() == OperationSourceChannel
	case ResumeCleanupHostOperation:
		return index == 0 && id.Valid() && id.Source() == OperationSourceWorker
	case ResumeCleanupHostOperationDeadline:
		if index == 0 {
			return id.Valid() && id.Source() == OperationSourceWorker
		}
		return index == 1 && (id == (OperationID{}) || id.Source() == OperationSourceTimer)
	case ResumeCleanupKeyedPark:
		return index == 0 && id.Valid() && id.Source() == OperationSourceManual
	default:
		return false
	}
}

func validResumeCleanupRuntimeShape(binding ResumeCleanupBinding) bool {
	switch binding.Kind {
	case ResumeCleanupChannelDirect:
		return binding.Count == 1 && binding.RuntimeCount == 1 && binding.Claim != nil
	case ResumeCleanupChannelSelect:
		return binding.RuntimeCount == binding.Count && binding.Claim != nil
	case ResumeCleanupHostOperation:
		return binding.Count == 1 && binding.RuntimeCount == 1 && binding.Claim == nil
	case ResumeCleanupHostOperationDeadline:
		return binding.Count == 2 && binding.RuntimeCount == 1 && binding.Claim == nil
	case ResumeCleanupKeyedPark:
		return binding.Count == 1 && binding.RuntimeCount == 1 && binding.Claim == nil
	default:
		return false
	}
}

func validResumeCleanupPlanShape(plan *ResumeCleanupPlan) bool {
	if plan == nil {
		return false
	}
	switch plan.kind {
	case ResumeCleanupChannelDirect:
		return plan.count == 1 && plan.runtime == 1 && plan.claim != nil
	case ResumeCleanupChannelSelect:
		return plan.runtime == plan.count && plan.claim != nil
	case ResumeCleanupHostOperation:
		return plan.count == 1 && plan.runtime == 1 && plan.claim == nil
	case ResumeCleanupHostOperationDeadline:
		return plan.count == 2 && plan.runtime == 1 && plan.claim == nil
	case ResumeCleanupKeyedPark:
		return plan.count == 1 && plan.runtime == 1 && plan.claim == nil
	default:
		return false
	}
}

func validResumeCleanupBindingForWait(
	record *WaitSetRecord,
	packet *ResumePacket,
	plan *ResumeCleanupPlan,
	binding ResumeCleanupBinding,
) bool {
	if record == nil || packet == nil || plan == nil || *packet != (ResumePacket{}) ||
		*plan != (ResumeCleanupPlan{}) || !validResumeCleanupKind(binding.Kind) ||
		binding.Context == nil || !validResumeCleanupRuntimeShape(binding) ||
		!validResumeCleanupRange(binding.Entries, binding.Count, binding.Stride, binding.IDOffset) ||
		record.g == nil || record.g.runP == nil {
		return false
	}
	p, state := record.g.runP, &record.g.park
	sources := &p.executor.sources
	if !validExecutorSourceSet(sources, p) || state.expected == 0 || state.expected > binding.Count ||
		resumeCleanupUsesChannelClaim(binding.Kind) &&
			(!validChannelOperationOwner(sources.channel, p) || p.channelSource != sources.channel ||
				selectClaimLoad(binding.Claim) != selectClaimOpen) {
		return false
	}
	physical := uint32(0)
	for index := uint32(0); index < binding.Count; index++ {
		id := (*OperationID)(unsafe.Add(
			binding.Entries,
			uintptr(index)*binding.Stride+binding.IDOffset,
		))
		if !validResumeCleanupSourceShape(binding.Kind, index, *id) {
			return false
		}
		if *id == (OperationID{}) {
			continue
		}
		if !executorSupportsResumeCleanupSource(sources, *id) {
			return false
		}
		if id.Source() == OperationSourceChannel {
			slot, ok := channelOperationSlotFor(sources.channel, *id)
			if !ok || preemptLoad(&slot.generation) != id.Generation ||
				preemptLoad(&slot.state) != uint32(producerSourceActive) ||
				preemptLoad(&slot.external) != uint32(channelExternalExposed) ||
				slot.claim != binding.Claim || slot.record.id != *id ||
				slot.record.phase != operationActive || slot.record.link.operation != &slot.record ||
				slot.record.link.park != state || slot.record.link.wait != record ||
				slot.record.link.ticket != record.ticket || slot.record.link.caseID != index+1 {
				return false
			}
		}
		physical++
	}
	if physical != state.expected {
		return false
	}
	linked := uint32(0)
	for link := state.head; link != nil; link = link.next {
		if link.caseID == 0 || link.caseID > binding.Count || link.operation == nil {
			return false
		}
		id := (*OperationID)(unsafe.Add(
			binding.Entries,
			uintptr(link.caseID-1)*binding.Stride+binding.IDOffset,
		))
		if *id != link.operation.id || link.operation.link.wait != record ||
			link.operation.link.ticket != record.ticket {
			return false
		}
		linked++
	}
	return linked == physical
}

// BindWaitSetResumeCleanup opts a typed park into owner-side materialization.
// The complete source range and closed runtime-hook shape are audited once
// here; later reductions use only the retained fixed descriptor and exact IDs.
func BindWaitSetResumeCleanup(
	record *WaitSetRecord,
	packet *ResumePacket,
	plan *ResumeCleanupPlan,
	binding ResumeCleanupBinding,
) bool {
	if record == nil || record.resume != nil || record.resumeKind != resumeBindingNone ||
		record.state != waitSetRecordCommitted || record.work != waitSetWorkIdle ||
		record.activePrev != nil || record.activeNext != nil || record.workNext != nil ||
		record.g == nil || !ValidG(record.g) || !validParkTicket(record.ticket) ||
		record.g.park.ticket != record.ticket || record.g.park.phase != parkParked ||
		record.g.pending.kind != pendingParkSet || record.g.pending.from != record.g.active ||
		record.g.active == nil || record.g.active.parkWait != record {
		return false
	}
	p := record.g.runP
	if p == nil || !validExecutorDriverForP(p.executor, p) || p.current != record.g ||
		!validResumeCleanupBindingForWait(record, packet, plan, binding) {
		return false
	}
	*packet = ResumePacket{
		ticket: record.ticket,
		state:  resumePacketBound,
	}
	*plan = ResumeCleanupPlan{
		packet:   packet,
		claim:    binding.Claim,
		context:  binding.Context,
		entries:  binding.Entries,
		ticket:   record.ticket,
		stride:   binding.Stride,
		idOffset: binding.IDOffset,
		count:    binding.Count,
		runtime:  binding.RuntimeCount,
		kind:     binding.Kind,
		phase:    resumeCleanupBound,
	}
	record.resume = unsafe.Pointer(plan)
	record.resumeKind = resumeBindingCleanup
	return validResumeCleanupPlan(record, plan)
}

func validResumeCleanupPlan(record *WaitSetRecord, plan *ResumeCleanupPlan) bool {
	if record == nil || plan == nil || record.resume != unsafe.Pointer(plan) ||
		record.resumeKind != resumeBindingCleanup || plan.packet == nil ||
		!validResumeCleanupPacket(plan, record.ticket) ||
		plan.context == nil ||
		plan.ticket != record.ticket || !validResumeCleanupKind(plan.kind) ||
		!validResumeCleanupRange(plan.entries, plan.count, plan.stride, plan.idOffset) ||
		!validResumeCleanupPlanShape(plan) || plan.outcome > ParkOutcomeDefault ||
		resumeCleanupUsesChannelClaim(plan.kind) != (plan.claim != nil) {
		return false
	}
	switch plan.phase {
	case resumeCleanupBound:
		return plan.index == 0 && plan.caseID == 0 && plan.outcome == ParkOutcomePending &&
			plan.lease == (OperationResultLease{}) && plan.result == ResumeResultNone &&
			plan.small == ResumeSmallInvalid &&
			record.g != nil &&
			(record.g.park.phase == parkParked || record.g.park.phase == parkDetaching ||
				record.g.park.phase == parkReady)
	case resumeCleanupRuntime:
		return plan.index < plan.runtime && plan.outcome != ParkOutcomePending &&
			record.g != nil && record.g.park.phase == parkConsumed
	case resumeCleanupConfirm, resumeCleanupRecycle:
		return plan.index < plan.count && plan.outcome != ParkOutcomePending &&
			record.g != nil && record.g.park.phase == parkConsumed
	case resumeCleanupClaim, resumeCleanupResult, resumeCleanupFinalize:
		return plan.index == 0 && plan.outcome != ParkOutcomePending &&
			record.g != nil && record.g.park.phase == parkConsumed
	default:
		return false
	}
}

func beginResumeCleanup(record *WaitSetRecord, plan *ResumeCleanupPlan) bool {
	if !validResumeCleanupPlan(record, plan) || plan.phase != resumeCleanupBound {
		return false
	}
	state := &record.g.park
	physicalOutcome := state.outcome
	outcome, caseID, lease, ok := ConsumeParkSet(state, record.ticket)
	if !ok || outcome != physicalOutcome {
		return false
	}
	switch outcome {
	case ParkOutcomeCompleted:
		if caseID == 0 || caseID > plan.count || !lease.Valid() {
			return false
		}
		id := resumeCleanupIDAt(plan, caseID-1)
		leaseID, leaseOK := lease.ID()
		if id == nil || !leaseOK || *id != leaseID {
			return false
		}
	case ParkOutcomeCanceled:
		if caseID != 0 || lease.Valid() {
			return false
		}
	default:
		return false
	}
	plan.outcome, plan.caseID, plan.lease = outcome, caseID, lease
	plan.phase = resumeCleanupRuntime
	return validResumeCleanupPlan(record, plan)
}

// ResumeCleanupStep is one direct-runtime typed cleanup reduction. The
// unexported plan token binds CommitResumeCleanupStep to the exact outstanding
// frame/index; callers can inspect only the closed kind, context, index, and
// logical decision needed by their direct switch.
type ResumeCleanupStep struct {
	Kind       ResumeCleanupKind
	Context    unsafe.Pointer
	Index      uint32
	WinnerCase uint32
	Outcome    ParkOutcome
	plan       *ResumeCleanupPlan
}

func pendingResumeCleanupStep(driver *ExecutorDriver) (ResumeCleanupStep, bool) {
	if driver == nil || driver.poll.resolve.phase != publishedEpochResolvePromote {
		return ResumeCleanupStep{}, false
	}
	record := driver.poll.resolve.wait
	if record == nil || record.resumeKind != resumeBindingCleanup {
		return ResumeCleanupStep{}, false
	}
	plan := (*ResumeCleanupPlan)(record.resume)
	if !validResumeCleanupPlan(record, plan) || plan.phase != resumeCleanupRuntime {
		return ResumeCleanupStep{}, false
	}
	return ResumeCleanupStep{
		Kind:       plan.kind,
		Context:    plan.context,
		Index:      plan.index,
		WinnerCase: plan.caseID,
		Outcome:    plan.outcome,
		plan:       plan,
	}, true
}

// CommitResumeCleanupStep completes the exact outstanding typed runtime step.
// small is invalid for source-neutral hooks. Channel hooks alone publish the
// closed runtime wait status for their physical winner.
func CommitResumeCleanupStep(step ResumeCleanupStep, small uint8) bool {
	plan := step.plan
	if plan == nil || plan.phase != resumeCleanupRuntime || plan.index != step.Index ||
		plan.kind != step.Kind || plan.context != step.Context ||
		plan.caseID != step.WinnerCase || plan.outcome != step.Outcome ||
		step.Index >= plan.runtime {
		return false
	}
	if resumeCleanupUsesChannelClaim(plan.kind) {
		selected := plan.outcome == ParkOutcomeCompleted && plan.caseID == plan.index+1
		if selected {
			if small == ResumeSmallInvalid || plan.small != ResumeSmallInvalid {
				return false
			}
			plan.small = small
		} else if small != ResumeSmallInvalid {
			return false
		}
	} else if small != ResumeSmallInvalid {
		return false
	}
	plan.index++
	if plan.index == plan.runtime {
		plan.index = 0
		plan.phase = resumeCleanupConfirm
	}
	return true
}

func confirmResumeCleanupOperation(
	sources *ExecutorSourceSet,
	p *P,
	id OperationID,
) bool {
	if !executorSupportsResumeCleanupSource(sources, id) {
		return false
	}
	switch id.Source() {
	case OperationSourceChannel:
		return sources.channel.ConfirmQuiesced(p, id)
	case OperationSourceManual:
		return sources.manual.ConfirmQuiesced(p, id)
	case OperationSourceWorker:
		return sources.worker.ConfirmQuiesced(p, id)
	case OperationSourceTimer, OperationSourcePoll:
		return true
	default:
		return false
	}
}

func takeResumeCleanupResult(
	sources *ExecutorSourceSet,
	p *P,
	id OperationID,
	lease OperationResultLease,
	scalar *ScalarResultPayloadV1,
	small *uint8,
) (ResumeResultKind, bool) {
	if !executorSupportsResumeCleanupSource(sources, id) || !lease.Valid() ||
		scalar == nil || small == nil || *scalar != (ScalarResultPayloadV1{}) {
		return ResumeResultNone, false
	}
	switch id.Source() {
	case OperationSourceChannel:
		return ResumeResultChannel, *small != ResumeSmallInvalid &&
			sources.channel.TakeResult(p, lease)
	case OperationSourceManual:
		return ResumeResultNone, *small == ResumeSmallInvalid &&
			sources.manual.TakeResult(p, lease)
	case OperationSourceWorker:
		return ResumeResultScalar, *small == ResumeSmallInvalid &&
			sources.worker.TakeResult(p, lease, scalar)
	case OperationSourceTimer:
		handle := TimerRegistrationHandle{Slot: id.LocalSlot(), Generation: id.Generation}
		return ResumeResultNone, *small == ResumeSmallInvalid &&
			sources.timers.TakeTimerV2Result(p, handle, lease)
	case OperationSourcePoll:
		handle := PollOperationHandle{Slot: id.LocalSlot(), Generation: id.Generation}
		result, ok := sources.poll.TakePollOperationV2Result(p, handle, lease)
		if !ok || result < PollOperationReady || result > PollOperationTimeout {
			return ResumeResultNone, false
		}
		*small = uint8(result)
		return ResumeResultPoll, true
	default:
		return ResumeResultNone, false
	}
}

func resumeCleanupCompletionRoute(
	sources *ExecutorSourceSet,
	p *P,
	id OperationID,
) (RouteID, bool) {
	if !executorSupportsResumeCleanupSource(sources, id) {
		return 0, false
	}
	if id.Source() == OperationSourceChannel {
		return sources.channel.CompletionRoute(p, id)
	}
	return 0, true
}

func recycleResumeCleanupOperation(
	sources *ExecutorSourceSet,
	p *P,
	id OperationID,
) bool {
	if !executorSupportsResumeCleanupSource(sources, id) {
		return false
	}
	switch id.Source() {
	case OperationSourceChannel:
		return sources.channel.Recycle(p, id)
	case OperationSourceManual:
		return sources.manual.Recycle(p, id)
	case OperationSourceWorker:
		return sources.worker.Recycle(p, id)
	case OperationSourceTimer:
		return sources.timers.RecycleTimerV2(
			p,
			TimerRegistrationHandle{Slot: id.LocalSlot(), Generation: id.Generation},
		)
	case OperationSourcePoll:
		return sources.poll.RecyclePollOperationV2(
			p,
			PollOperationHandle{Slot: id.LocalSlot(), Generation: id.Generation},
		)
	default:
		return false
	}
}

// advanceResumeCleanupCore performs one source-neutral bounded reduction after
// the runtime has removed every typed queue node.
func advanceResumeCleanupCore(
	sources *ExecutorSourceSet,
	p *P,
	record *WaitSetRecord,
	plan *ResumeCleanupPlan,
) (finalized bool, ok bool) {
	if sources == nil || p == nil || !validExecutorSourceSet(sources, p) ||
		!validResumeCleanupPlan(record, plan) {
		return false, false
	}
	switch plan.phase {
	case resumeCleanupConfirm:
		if plan.index < plan.count {
			id := resumeCleanupIDAt(plan, plan.index)
			if id == nil || *id != (OperationID{}) &&
				!confirmResumeCleanupOperation(sources, p, *id) {
				return false, false
			}
			plan.index++
			if plan.index == plan.count {
				plan.index = 0
				plan.phase = resumeCleanupClaim
			}
			return false, true
		}
	case resumeCleanupClaim:
		if plan.claim != nil && !sources.channel.ResetSelectClaim(p, plan.claim) {
			return false, false
		}
		plan.phase = resumeCleanupResult
		return false, true
	case resumeCleanupResult:
		if plan.lease.Valid() {
			if plan.outcome != ParkOutcomeCompleted || plan.caseID == 0 {
				return false, false
			}
			id := resumeCleanupIDAt(plan, plan.caseID-1)
			if id == nil || *id == (OperationID{}) {
				return false, false
			}
			result, taken := takeResumeCleanupResult(
				sources,
				p,
				*id,
				plan.lease,
				&plan.packet.scalar,
				&plan.small,
			)
			if !taken {
				return false, false
			}
			plan.result = result
			preferredRoute, routeOK := resumeCleanupCompletionRoute(sources, p, *id)
			if !routeOK || !setConsumedParkPreferredRoute(
				&record.g.park,
				plan.ticket,
				preferredRoute,
			) {
				return false, false
			}
		} else if plan.outcome == ParkOutcomeCompleted {
			return false, false
		}
		plan.lease = OperationResultLease{}
		plan.phase = resumeCleanupRecycle
		return false, true
	case resumeCleanupRecycle:
		if plan.index < plan.count {
			id := resumeCleanupIDAt(plan, plan.index)
			if id == nil {
				return false, false
			}
			if *id != (OperationID{}) {
				if !recycleResumeCleanupOperation(sources, p, *id) {
					return false, false
				}
				*id = OperationID{}
			}
			plan.index++
			if plan.index == plan.count {
				plan.index = 0
				plan.phase = resumeCleanupFinalize
			}
			return false, true
		}
	case resumeCleanupFinalize:
		if plan.outcome != ParkOutcomeCompleted &&
			(plan.result != ResumeResultNone || plan.packet.scalar != (ScalarResultPayloadV1{}) ||
				plan.small != ResumeSmallInvalid) {
			return false, false
		}
		packet := plan.packet
		ticket, outcome, caseID := plan.ticket, plan.outcome, plan.caseID
		result, scalar, small := plan.result, packet.scalar, plan.small
		if !materializedParkState(&record.g.park, ticket, outcome, caseID) {
			return false, false
		}
		*packet = ResumePacket{
			ticket:  ticket,
			scalar:  scalar,
			caseID:  caseID,
			outcome: outcome,
			result:  result,
			small:   small,
			state:   resumePacketMaterialized,
		}
		record.resume = unsafe.Pointer(packet)
		record.resumeKind = resumeBindingMaterialized
		*plan = ResumeCleanupPlan{}
		return true, validMaterializedResumePacket(packet)
	}
	return false, false
}
