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
	Kind     ResumeCleanupKind
	Context  unsafe.Pointer
	Entries  unsafe.Pointer
	Source   *ChannelOperationSource
	Claim    *SelectClaim
	Count    uint32
	Stride   uintptr
	IDOffset uintptr
}

// ResumeCleanupPlan is compiler-spilled continuation state for bounded
// owner-side materialization. It is not a callback, interface, Future, heap
// task, or permanent G field. Runtime cleanup advances one logical case per
// ExecutorRunStepMaterialize; every later core call confirms or recycles at
// most one exact source generation.
type ResumeCleanupPlan struct {
	packet   *ResumePacket
	source   *ChannelOperationSource
	claim    *SelectClaim
	context  unsafe.Pointer
	entries  unsafe.Pointer
	lease    OperationResultLease
	ticket   ParkTicket
	stride   uintptr
	idOffset uintptr
	count    uint32
	index    uint32
	caseID   uint32
	outcome  ParkOutcome
	kind     ResumeCleanupKind
	phase    resumeCleanupPhase
	small    uint8
	_        [1]byte
}

func validResumeCleanupKind(kind ResumeCleanupKind) bool {
	return kind == ResumeCleanupChannelDirect || kind == ResumeCleanupChannelSelect
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

func validResumeCleanupBindingForWait(
	record *WaitSetRecord,
	packet *ResumePacket,
	plan *ResumeCleanupPlan,
	binding ResumeCleanupBinding,
) bool {
	if record == nil || packet == nil || plan == nil || *packet != (ResumePacket{}) ||
		*plan != (ResumeCleanupPlan{}) || !validResumeCleanupKind(binding.Kind) ||
		binding.Context == nil || binding.Source == nil || binding.Claim == nil ||
		!validResumeCleanupRange(binding.Entries, binding.Count, binding.Stride, binding.IDOffset) ||
		record.g == nil || record.g.runP == nil {
		return false
	}
	p, state := record.g.runP, &record.g.park
	if !validChannelOperationOwner(binding.Source, p) || p.channelSource != binding.Source ||
		selectClaimLoad(binding.Claim) != selectClaimOpen || state.expected == 0 ||
		state.expected > binding.Count {
		return false
	}
	physical := uint32(0)
	for index := uint32(0); index < binding.Count; index++ {
		id := (*OperationID)(unsafe.Add(
			binding.Entries,
			uintptr(index)*binding.Stride+binding.IDOffset,
		))
		if *id == (OperationID{}) {
			continue
		}
		slot, ok := channelOperationSlotFor(binding.Source, *id)
		if !ok || preemptLoad(&slot.generation) != id.Generation ||
			preemptLoad(&slot.state) != uint32(producerSourceActive) ||
			preemptLoad(&slot.external) != uint32(channelExternalExposed) ||
			slot.claim != binding.Claim || slot.record.id != *id ||
			slot.record.phase != operationActive || slot.record.link.operation != &slot.record ||
			slot.record.link.park != state || slot.record.link.wait != record ||
			slot.record.link.ticket != record.ticket || slot.record.link.caseID != index+1 {
			return false
		}
		physical++
	}
	if physical != state.expected {
		return false
	}
	return binding.Kind != ResumeCleanupChannelDirect ||
		binding.Count == 1 && physical == 1
}

// BindWaitSetResumeCleanup opts a typed channel park into owner-side
// materialization after all exact source endpoints are Exposed and before any
// hchan queue node is made reachable. The complete range is audited once here;
// later reductions use only the retained fixed descriptor and exact IDs.
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
		source:   binding.Source,
		claim:    binding.Claim,
		context:  binding.Context,
		entries:  binding.Entries,
		ticket:   record.ticket,
		stride:   binding.Stride,
		idOffset: binding.IDOffset,
		count:    binding.Count,
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
		!validBoundResumePacket(plan.packet, record.ticket) ||
		plan.source == nil || plan.claim == nil || plan.context == nil ||
		plan.ticket != record.ticket || !validResumeCleanupKind(plan.kind) ||
		!validResumeCleanupRange(plan.entries, plan.count, plan.stride, plan.idOffset) ||
		plan.index > plan.count || plan.outcome > ParkOutcomeDefault {
		return false
	}
	switch plan.phase {
	case resumeCleanupBound:
		return plan.index == 0 && plan.caseID == 0 && plan.outcome == ParkOutcomePending &&
			plan.lease == (OperationResultLease{}) && plan.small == ResumeSmallInvalid &&
			record.g != nil &&
			(record.g.park.phase == parkParked || record.g.park.phase == parkDetaching ||
				record.g.park.phase == parkReady)
	case resumeCleanupRuntime:
		return plan.index < plan.count && plan.outcome != ParkOutcomePending &&
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

// CommitResumeCleanupStep completes the exact outstanding typed runtime case.
// small is zero for every loser/canceled case and is the closed runtime status
// for the physical winner.
func CommitResumeCleanupStep(step ResumeCleanupStep, small uint8) bool {
	plan := step.plan
	if plan == nil || plan.phase != resumeCleanupRuntime || plan.index != step.Index ||
		plan.kind != step.Kind || plan.context != step.Context ||
		plan.caseID != step.WinnerCase || plan.outcome != step.Outcome ||
		step.Index >= plan.count {
		return false
	}
	selected := plan.outcome == ParkOutcomeCompleted && plan.caseID == plan.index+1
	if selected {
		if small == ResumeSmallInvalid || plan.small != ResumeSmallInvalid {
			return false
		}
		plan.small = small
	} else if small != ResumeSmallInvalid {
		return false
	}
	plan.index++
	if plan.index == plan.count {
		plan.index = 0
		plan.phase = resumeCleanupConfirm
	}
	return true
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
		sources.channel != plan.source || !validResumeCleanupPlan(record, plan) {
		return false, false
	}
	switch plan.phase {
	case resumeCleanupConfirm:
		if plan.index < plan.count {
			id := resumeCleanupIDAt(plan, plan.index)
			if id == nil || *id != (OperationID{}) && !plan.source.ConfirmQuiesced(p, *id) {
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
		if !plan.source.ResetSelectClaim(p, plan.claim) {
			return false, false
		}
		plan.phase = resumeCleanupResult
		return false, true
	case resumeCleanupResult:
		if plan.lease.Valid() {
			var released bool
			if plan.outcome == ParkOutcomeCompleted {
				released = plan.source.TakeResult(p, plan.lease)
			} else {
				released = plan.source.DiscardResult(p, plan.lease)
			}
			if !released {
				return false, false
			}
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
				if !plan.source.Recycle(p, *id) {
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
		if plan.outcome == ParkOutcomeCompleted && plan.small == ResumeSmallInvalid ||
			plan.outcome != ParkOutcomeCompleted && plan.small != ResumeSmallInvalid {
			return false, false
		}
		packet, ticket, outcome, caseID, small := plan.packet, plan.ticket, plan.outcome, plan.caseID, plan.small
		if !materializedParkState(&record.g.park, ticket, outcome, caseID) {
			return false, false
		}
		result := ResumeResultNone
		if outcome == ParkOutcomeCompleted {
			result = ResumeResultChannel
		}
		*packet = ResumePacket{
			ticket:  ticket,
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
