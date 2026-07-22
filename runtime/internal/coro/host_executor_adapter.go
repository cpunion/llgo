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

// HostActionKindV1 is the pointer-free capability requested from an embedding
// event loop. Schedule asks for a later (never recursive) host turn. Alarm asks
// for a one-shot absolute monotonic deadline. Cancel actions must be
// acknowledged before the adapter can reuse the callback epoch or retire.
type HostActionKindV1 uint32

const (
	HostActionNoneV1 HostActionKindV1 = iota
	HostActionScheduleV1
	HostActionAlarmV1
	HostActionCancelScheduleV1
	HostActionCancelAlarmV1
)

// HostCallbackCauseV1 identifies which previously-taken action caused a clean
// host re-entry. It is deliberately a uint32 ABI rather than a Go callback.
type HostCallbackCauseV1 uint32

const (
	HostCallbackInvalidV1 HostCallbackCauseV1 = iota
	HostCallbackScheduleV1
	HostCallbackAlarmV1
)

// HostActionV1 is a padding-free 32-byte POD host boundary. Deadline uses two
// uint32 words so wasm32 does not require a host i64/BigInt calling convention.
// Reserved remains zero in V1.
type HostActionV1 struct {
	Kind               uint32
	ExecutorSlot       uint32
	ExecutorGeneration uint32
	Epoch              uint32
	DeadlineLo         uint32
	DeadlineHi         uint32
	Reserved0          uint32
	Reserved1          uint32
}

type HostCallbackClaimResult uint8

const (
	HostCallbackClaimInvalid HostCallbackClaimResult = iota
	HostCallbackClaimed
	HostCallbackClaimStale
)

type HostAdapterCompletion uint8

const (
	HostAdapterCompletionInvalid HostAdapterCompletion = iota
	HostAdapterCompletionPending
	HostAdapterCompletionComplete
)

const (
	hostAdapterUnused uint32 = iota
	hostAdapterStarting
	hostAdapterActive
	hostAdapterClosing
	hostAdapterRetired
)

const (
	hostContinuationNone uint32 = iota
	hostContinuationRun
	hostContinuationWait
	hostContinuationClose
)

const (
	hostDispatchIdle uint32 = iota
	hostDispatchRequested
	hostDispatchArmed
	hostDispatchDelivered
	hostDispatchCancelRequested
	hostDispatchCancelIssued
)

// HostExecutorAdapter is the target-neutral, allocation-free state machine for
// a host-driven single-P executor. It stores only POD identities and uint32
// atomics. No host action contains a P/G, Go pointer, coroutine handle, source
// table pointer, or callback function.
//
// A host obtains actions with NextAction, arranges them after returning from
// the current runtime entry, and later calls ClaimCallback with the exact
// executor generation and epoch. callbackIngress covers the Claim-to-runtime
// tail without retaining the callback across the scheduler call: close seals
// both ingress domains and returns Pending until those tails have left.
// Storage is a permanent single-start tombstone after Retire.
type HostExecutorAdapter struct {
	lifecycle uint32

	handleSlot       uint32
	handleGeneration uint32

	continuation uint32
	activeEpoch  uint32
	closeEpoch   uint32
	cancelEpoch  uint32
	deadlineLo   uint32
	deadlineHi   uint32
	hasDeadline  uint32

	schedule uint32
	alarm    uint32

	ingress         TargetIngress
	callbackIngress TargetIngress
}

func hostAdapterHandle(adapter *HostExecutorAdapter) ExecutorHandle {
	if adapter == nil {
		return ExecutorHandle{}
	}
	return ExecutorHandle{
		Slot:       preemptLoad(&adapter.handleSlot),
		Generation: preemptLoad(&adapter.handleGeneration),
	}
}

func validHostAdapterHandle(adapter *HostExecutorAdapter, handle ExecutorHandle) bool {
	return adapter != nil && handle.Slot != 0 && handle.Generation != 0 && hostAdapterHandle(adapter) == handle
}

func (adapter *HostExecutorAdapter) Start(handle ExecutorHandle, hostOwnsSlice bool) bool {
	// A legacy command pump has no consumer for queued host actions. Reject it
	// before publishing any ingress state; only an embedding-owned RunSlice
	// reactor may activate this adapter.
	if adapter == nil || !hostOwnsSlice || handle.Slot == 0 || handle.Generation == 0 ||
		!adapter.ingress.CanReleaseResources() || !adapter.callbackIngress.CanReleaseResources() ||
		!preemptCompareAndSwap(&adapter.lifecycle, hostAdapterUnused, hostAdapterStarting) {
		return false
	}
	if !adapter.ingress.Start() || !adapter.callbackIngress.Start() {
		// A partial start is terminal. Reusing a possibly visible ingress word
		// would turn a delayed callback into an ABA alias.
		preemptStore(&adapter.lifecycle, hostAdapterRetired)
		return false
	}
	preemptStore(&adapter.handleGeneration, handle.Generation)
	preemptStore(&adapter.handleSlot, handle.Slot)
	preemptStore(&adapter.lifecycle, hostAdapterActive)
	return true
}

func (adapter *HostExecutorAdapter) begin(handle ExecutorHandle, epoch, kind uint32, deadline int64, hasDeadline bool) bool {
	if adapter == nil || epoch == 0 || kind != hostContinuationRun && kind != hostContinuationWait ||
		preemptLoad(&adapter.lifecycle) != hostAdapterActive || !validHostAdapterHandle(adapter, handle) ||
		preemptLoad(&adapter.continuation) != hostContinuationNone || preemptLoad(&adapter.activeEpoch) != 0 ||
		preemptLoad(&adapter.schedule) != hostDispatchIdle || preemptLoad(&adapter.alarm) != hostDispatchIdle ||
		deadline < 0 {
		return false
	}
	word := uint64(deadline)
	preemptStore(&adapter.deadlineLo, uint32(word))
	preemptStore(&adapter.deadlineHi, uint32(word>>32))
	if hasDeadline {
		preemptStore(&adapter.hasDeadline, 1)
	} else {
		preemptStore(&adapter.hasDeadline, 0)
	}
	preemptStore(&adapter.activeEpoch, epoch)
	preemptStore(&adapter.continuation, kind)
	if kind == hostContinuationRun {
		preemptStore(&adapter.schedule, hostDispatchRequested)
	} else if hasDeadline {
		preemptStore(&adapter.alarm, hostDispatchRequested)
	}
	return true
}

func (adapter *HostExecutorAdapter) BeginRun(handle ExecutorHandle, epoch uint32) bool {
	return adapter.begin(handle, epoch, hostContinuationRun, 0, false)
}

func (adapter *HostExecutorAdapter) BeginWait(handle ExecutorHandle, epoch uint32, deadline int64, hasDeadline bool) bool {
	return adapter.begin(handle, epoch, hostContinuationWait, deadline, hasDeadline)
}

func fillHostAction(adapter *HostExecutorAdapter, out *HostActionV1, kind HostActionKindV1, epoch uint32, deadline bool) bool {
	if adapter == nil || out == nil || kind == HostActionNoneV1 || epoch == 0 {
		return false
	}
	handle := hostAdapterHandle(adapter)
	if handle.Slot == 0 || handle.Generation == 0 {
		return false
	}
	*out = HostActionV1{
		Kind:               uint32(kind),
		ExecutorSlot:       handle.Slot,
		ExecutorGeneration: handle.Generation,
		Epoch:              epoch,
	}
	if deadline {
		out.DeadlineLo = preemptLoad(&adapter.deadlineLo)
		out.DeadlineHi = preemptLoad(&adapter.deadlineHi)
	}
	return true
}

func takeHostDispatchAction(adapter *HostExecutorAdapter, state *uint32, epoch uint32, action, cancel HostActionKindV1, deadline bool, out *HostActionV1) (taken, retry bool) {
	switch current := preemptLoad(state); current {
	case hostDispatchCancelRequested:
		if !preemptCompareAndSwap(state, current, hostDispatchCancelIssued) {
			return false, true
		}
		return fillHostAction(adapter, out, cancel, epoch, false), false
	case hostDispatchRequested:
		if !preemptCompareAndSwap(state, current, hostDispatchArmed) {
			return false, true
		}
		return fillHostAction(adapter, out, action, epoch, deadline), false
	default:
		return false, false
	}
}

// NextAction transfers at most one physical scheduling obligation to the host.
// It never calls the host and therefore cannot recursively enter the executor.
func (adapter *HostExecutorAdapter) NextAction(out *HostActionV1) bool {
	if adapter == nil || out == nil {
		return false
	}
	*out = HostActionV1{}
	for attempt := 0; attempt != 8; attempt++ {
		lifecycle := preemptLoad(&adapter.lifecycle)
		epoch := preemptLoad(&adapter.activeEpoch)
		if lifecycle == hostAdapterClosing {
			epoch = preemptLoad(&adapter.cancelEpoch)
		}
		// Do not arm replacement work while a physical cancellation is still
		// in flight. The host must acknowledge the exact old epoch first.
		if preemptLoad(&adapter.alarm) == hostDispatchCancelIssued ||
			preemptLoad(&adapter.schedule) == hostDispatchCancelIssued {
			return false
		}
		if taken, retry := takeHostDispatchAction(adapter, &adapter.alarm, epoch, HostActionAlarmV1, HostActionCancelAlarmV1, true, out); taken {
			return true
		} else if retry {
			continue
		}
		if taken, retry := takeHostDispatchAction(adapter, &adapter.schedule, epoch, HostActionScheduleV1, HostActionCancelScheduleV1, false, out); taken {
			return true
		} else if retry {
			continue
		}
		if lifecycle != hostAdapterClosing || preemptLoad(&adapter.schedule) != hostDispatchIdle ||
			preemptLoad(&adapter.alarm) != hostDispatchIdle || !adapter.ingress.Quiesced() ||
			!adapter.callbackIngress.Quiesced() {
			return false
		}
		closeEpoch := preemptLoad(&adapter.closeEpoch)
		if closeEpoch == 0 || !preemptCompareAndSwap(&adapter.schedule, hostDispatchIdle, hostDispatchArmed) {
			continue
		}
		return fillHostAction(adapter, out, HostActionScheduleV1, closeEpoch, false)
	}
	return false
}

// NextDeadline snapshots the currently retained wait deadline without
// consuming its Alarm action. Embedded and bare-metal main loops can use this
// to program or recheck a hardware compare before deciding to WFI/WFE.
func (adapter *HostExecutorAdapter) NextDeadline(out *HostActionV1) bool {
	if adapter == nil || out == nil {
		return false
	}
	*out = HostActionV1{}
	if preemptLoad(&adapter.lifecycle) != hostAdapterActive ||
		preemptLoad(&adapter.continuation) != hostContinuationWait ||
		preemptLoad(&adapter.hasDeadline) != 1 {
		return false
	}
	epoch := preemptLoad(&adapter.activeEpoch)
	if epoch == 0 || !fillHostAction(adapter, out, HostActionAlarmV1, epoch, true) ||
		preemptLoad(&adapter.lifecycle) != hostAdapterActive ||
		preemptLoad(&adapter.continuation) != hostContinuationWait ||
		preemptLoad(&adapter.activeEpoch) != epoch || preemptLoad(&adapter.hasDeadline) != 1 {
		*out = HostActionV1{}
		return false
	}
	return true
}

func hostDispatchForCause(adapter *HostExecutorAdapter, cause HostCallbackCauseV1) *uint32 {
	if adapter == nil {
		return nil
	}
	switch cause {
	case HostCallbackScheduleV1:
		return &adapter.schedule
	case HostCallbackAlarmV1:
		return &adapter.alarm
	default:
		return nil
	}
}

// ClaimCallback consumes one action previously returned by NextAction. Active
// callbacks take a callbackIngress lease which the caller must finish after
// the program continuation returns. The special close callback is admitted
// after both producer ingress domains are sealed and needs no such lease.
func (adapter *HostExecutorAdapter) ClaimCallback(handle ExecutorHandle, epoch uint32, cause HostCallbackCauseV1) (HostCallbackClaimResult, bool) {
	state := hostDispatchForCause(adapter, cause)
	if state == nil || epoch == 0 || handle.Slot == 0 || handle.Generation == 0 {
		return HostCallbackClaimInvalid, false
	}
	lifecycle := preemptLoad(&adapter.lifecycle)
	if !validHostAdapterHandle(adapter, handle) {
		return HostCallbackClaimStale, false
	}
	if lifecycle == hostAdapterClosing && epoch == preemptLoad(&adapter.closeEpoch) && cause == HostCallbackScheduleV1 {
		if preemptCompareAndSwap(state, hostDispatchArmed, hostDispatchDelivered) {
			return HostCallbackClaimed, false
		}
		return HostCallbackClaimStale, false
	}
	if lifecycle != hostAdapterActive || epoch != preemptLoad(&adapter.activeEpoch) ||
		preemptLoad(&adapter.continuation) == hostContinuationNone {
		return HostCallbackClaimStale, false
	}
	if !adapter.ingress.Enter() {
		return HostCallbackClaimStale, false
	}
	if !adapter.callbackIngress.Enter() {
		_, _ = adapter.ingress.Leave()
		return HostCallbackClaimStale, false
	}
	if preemptLoad(&adapter.lifecycle) != hostAdapterActive || !validHostAdapterHandle(adapter, handle) ||
		epoch != preemptLoad(&adapter.activeEpoch) || !preemptCompareAndSwap(state, hostDispatchArmed, hostDispatchDelivered) {
		_, _ = adapter.callbackIngress.Leave()
		_, _ = adapter.ingress.Leave()
		return HostCallbackClaimStale, false
	}
	_, leaveOK := adapter.ingress.Leave()
	if !leaveOK {
		_, _ = adapter.callbackIngress.Leave()
		return HostCallbackClaimInvalid, false
	}
	return HostCallbackClaimed, true
}

// FinishCallback is the absolute final target-state access of an active host
// callback. Repost rearms the same tuple only after the current runtime entry
// has returned; every other disposition drops an unconsumed delivered action.
func (adapter *HostExecutorAdapter) FinishCallback(handle ExecutorHandle, epoch uint32, cause HostCallbackCauseV1, lease, repost bool) bool {
	if adapter == nil || !lease {
		return false
	}
	state := hostDispatchForCause(adapter, cause)
	ok := state != nil
	if ok && preemptLoad(state) == hostDispatchDelivered {
		if repost && preemptLoad(&adapter.lifecycle) == hostAdapterActive && validHostAdapterHandle(adapter, handle) &&
			epoch == preemptLoad(&adapter.activeEpoch) {
			ok = preemptCompareAndSwap(state, hostDispatchDelivered, hostDispatchRequested)
		} else {
			ok = preemptCompareAndSwap(state, hostDispatchDelivered, hostDispatchIdle)
		}
	}
	_, leaveOK := adapter.callbackIngress.Leave()
	return ok && leaveOK
}

// RepostCloseCallback restores the already-transferred close callback after a
// DriveAdmission deferral. The host already owns the tuple returned by the
// Repost result, so Armed (not Requested/NextAction) is the exact state.
func (adapter *HostExecutorAdapter) RepostCloseCallback(handle ExecutorHandle, epoch uint32) bool {
	return adapter != nil && epoch != 0 && preemptLoad(&adapter.lifecycle) == hostAdapterClosing &&
		validHostAdapterHandle(adapter, handle) && preemptLoad(&adapter.closeEpoch) == epoch &&
		preemptCompareAndSwap(&adapter.schedule, hostDispatchDelivered, hostDispatchArmed)
}

func settleOtherHostDispatch(state *uint32) HostAdapterCompletion {
	for attempt := 0; attempt != 4; attempt++ {
		switch current := preemptLoad(state); current {
		case hostDispatchIdle:
			return HostAdapterCompletionComplete
		case hostDispatchRequested:
			if preemptCompareAndSwap(state, current, hostDispatchIdle) {
				return HostAdapterCompletionComplete
			}
		case hostDispatchArmed:
			if preemptCompareAndSwap(state, current, hostDispatchCancelRequested) {
				return HostAdapterCompletionPending
			}
		case hostDispatchCancelRequested, hostDispatchCancelIssued:
			return HostAdapterCompletionPending
		default:
			return HostAdapterCompletionInvalid
		}
	}
	return HostAdapterCompletionPending
}

func (adapter *HostExecutorAdapter) complete(handle ExecutorHandle, epoch, kind uint32) HostAdapterCompletion {
	if adapter == nil || epoch == 0 || preemptLoad(&adapter.lifecycle) != hostAdapterActive ||
		!validHostAdapterHandle(adapter, handle) || preemptLoad(&adapter.activeEpoch) != epoch ||
		preemptLoad(&adapter.continuation) != kind {
		return HostAdapterCompletionInvalid
	}
	var delivered, other *uint32
	if preemptLoad(&adapter.schedule) == hostDispatchDelivered {
		delivered, other = &adapter.schedule, &adapter.alarm
	} else if preemptLoad(&adapter.alarm) == hostDispatchDelivered {
		delivered, other = &adapter.alarm, &adapter.schedule
	} else {
		return HostAdapterCompletionInvalid
	}
	if settled := settleOtherHostDispatch(other); settled != HostAdapterCompletionComplete {
		return settled
	}
	if !preemptCompareAndSwap(delivered, hostDispatchDelivered, hostDispatchIdle) {
		return HostAdapterCompletionInvalid
	}
	preemptStore(&adapter.hasDeadline, 0)
	preemptStore(&adapter.deadlineLo, 0)
	preemptStore(&adapter.deadlineHi, 0)
	preemptStore(&adapter.activeEpoch, 0)
	preemptStore(&adapter.continuation, hostContinuationNone)
	return HostAdapterCompletionComplete
}

func (adapter *HostExecutorAdapter) CompleteRun(handle ExecutorHandle, epoch uint32) HostAdapterCompletion {
	return adapter.complete(handle, epoch, hostContinuationRun)
}

func (adapter *HostExecutorAdapter) CompleteWait(handle ExecutorHandle, epoch uint32) HostAdapterCompletion {
	return adapter.complete(handle, epoch, hostContinuationWait)
}

// RequestWake maps an IdleWake gate transition to an immediate host turn. An
// alarm already transferred to the host is canceled first; NextAction always
// emits cancellation before the replacement Schedule action.
func (adapter *HostExecutorAdapter) RequestWake(handle ExecutorHandle) bool {
	if adapter == nil || preemptLoad(&adapter.lifecycle) != hostAdapterActive || !validHostAdapterHandle(adapter, handle) ||
		preemptLoad(&adapter.continuation) != hostContinuationWait || preemptLoad(&adapter.activeEpoch) == 0 {
		return false
	}
cancelAlarm:
	for attempt := 0; attempt != 4; attempt++ {
		switch alarm := preemptLoad(&adapter.alarm); alarm {
		case hostDispatchIdle, hostDispatchCancelRequested, hostDispatchCancelIssued:
			break cancelAlarm
		case hostDispatchRequested:
			if preemptCompareAndSwap(&adapter.alarm, alarm, hostDispatchIdle) {
				break cancelAlarm
			}
		case hostDispatchArmed:
			if preemptCompareAndSwap(&adapter.alarm, alarm, hostDispatchCancelRequested) {
				break cancelAlarm
			}
		case hostDispatchDelivered:
			// The alarm callback already owns the exact wake.
			return true
		default:
			return false
		}
	}
	for {
		switch schedule := preemptLoad(&adapter.schedule); schedule {
		case hostDispatchIdle:
			if preemptCompareAndSwap(&adapter.schedule, schedule, hostDispatchRequested) {
				return true
			}
		case hostDispatchRequested, hostDispatchArmed, hostDispatchDelivered:
			return true
		default:
			return false
		}
	}
}

func (adapter *HostExecutorAdapter) EnterProducer(handle ExecutorHandle) bool {
	if adapter == nil || preemptLoad(&adapter.lifecycle) != hostAdapterActive || !adapter.ingress.Enter() {
		return false
	}
	if preemptLoad(&adapter.lifecycle) == hostAdapterActive && validHostAdapterHandle(adapter, handle) {
		return true
	}
	_, _ = adapter.ingress.Leave()
	return false
}

func (adapter *HostExecutorAdapter) LeaveProducer() bool {
	if adapter == nil {
		return false
	}
	_, ok := adapter.ingress.Leave()
	return ok
}

func cancelHostDispatch(state *uint32) bool {
	for attempt := 0; attempt != 4; attempt++ {
		switch current := preemptLoad(state); current {
		case hostDispatchIdle:
			return true
		case hostDispatchRequested:
			if preemptCompareAndSwap(state, current, hostDispatchIdle) {
				return true
			}
		case hostDispatchArmed:
			if preemptCompareAndSwap(state, current, hostDispatchCancelRequested) {
				return true
			}
		case hostDispatchCancelRequested, hostDispatchCancelIssued:
			return true
		case hostDispatchDelivered:
			// The callback already crossed the physical boundary and owns a
			// callbackIngress tail. It is no longer cancelable. Close remains
			// Pending until FinishCallback drops Delivered and leaves that tail.
			return true
		default:
			return false
		}
	}
	return false
}

// BeginClose seals new producer/callback admission before requesting physical
// cancellation. Complete means both ingress domains were already strongly
// joined; Pending requires the host to drain Cancel actions and then schedule
// the returned close epoch through NextAction.
func (adapter *HostExecutorAdapter) BeginClose(handle ExecutorHandle, epoch uint32) HostAdapterCompletion {
	if adapter == nil || epoch == 0 || preemptLoad(&adapter.lifecycle) != hostAdapterActive ||
		!validHostAdapterHandle(adapter, handle) {
		return HostAdapterCompletionInvalid
	}
	oldEpoch := preemptLoad(&adapter.activeEpoch)
	preemptStore(&adapter.cancelEpoch, oldEpoch)
	preemptStore(&adapter.closeEpoch, epoch)
	if !preemptCompareAndSwap(&adapter.lifecycle, hostAdapterActive, hostAdapterClosing) ||
		!adapter.ingress.Seal() || !adapter.callbackIngress.Seal() ||
		!cancelHostDispatch(&adapter.alarm) || !cancelHostDispatch(&adapter.schedule) {
		return HostAdapterCompletionInvalid
	}
	preemptStore(&adapter.continuation, hostContinuationClose)
	preemptStore(&adapter.activeEpoch, epoch)
	if preemptLoad(&adapter.alarm) == hostDispatchIdle && preemptLoad(&adapter.schedule) == hostDispatchIdle &&
		adapter.ingress.Quiesced() && adapter.callbackIngress.Quiesced() {
		if !adapter.ingress.Retire() || !adapter.callbackIngress.Retire() {
			return HostAdapterCompletionInvalid
		}
		preemptStore(&adapter.lifecycle, hostAdapterRetired)
		return HostAdapterCompletionComplete
	}
	return HostAdapterCompletionPending
}

func (adapter *HostExecutorAdapter) CompleteClose(handle ExecutorHandle, epoch uint32) HostAdapterCompletion {
	if adapter == nil || epoch == 0 || preemptLoad(&adapter.lifecycle) != hostAdapterClosing ||
		!validHostAdapterHandle(adapter, handle) || preemptLoad(&adapter.closeEpoch) != epoch ||
		preemptLoad(&adapter.continuation) != hostContinuationClose ||
		preemptLoad(&adapter.schedule) != hostDispatchDelivered || preemptLoad(&adapter.alarm) != hostDispatchIdle ||
		!adapter.ingress.Quiesced() || !adapter.callbackIngress.Quiesced() {
		return HostAdapterCompletionInvalid
	}
	preemptStore(&adapter.schedule, hostDispatchIdle)
	if !adapter.ingress.Retire() || !adapter.callbackIngress.Retire() {
		return HostAdapterCompletionInvalid
	}
	preemptStore(&adapter.lifecycle, hostAdapterRetired)
	return HostAdapterCompletionComplete
}

func (adapter *HostExecutorAdapter) AcknowledgeCancel(handle ExecutorHandle, epoch uint32, kind HostActionKindV1) bool {
	if adapter == nil || epoch == 0 || !validHostAdapterHandle(adapter, handle) {
		return false
	}
	lifecycle := preemptLoad(&adapter.lifecycle)
	wantEpoch := preemptLoad(&adapter.activeEpoch)
	if lifecycle == hostAdapterClosing {
		wantEpoch = preemptLoad(&adapter.cancelEpoch)
	}
	if lifecycle != hostAdapterActive && lifecycle != hostAdapterClosing || epoch != wantEpoch {
		return false
	}
	var state *uint32
	switch kind {
	case HostActionCancelScheduleV1:
		state = &adapter.schedule
	case HostActionCancelAlarmV1:
		state = &adapter.alarm
	default:
		return false
	}
	return preemptCompareAndSwap(state, hostDispatchCancelIssued, hostDispatchIdle)
}

func (adapter *HostExecutorAdapter) CanRelease() bool {
	return adapter != nil && preemptLoad(&adapter.lifecycle) == hostAdapterRetired &&
		adapter.ingress.Retired() && adapter.callbackIngress.Retired()
}

// HostMonotonicClock is a uint32 seqlock for a host-supplied absolute
// monotonic time. It is suitable for wasm32 and targets without lock-free i64
// atomics. Publish rejects regressions and exhaustion; Snapshot never tears.
type HostMonotonicClock struct {
	sequence uint32
	lo       uint32
	hi       uint32
}

func (clock *HostMonotonicClock) Snapshot() (int64, bool) {
	if clock == nil {
		return 0, false
	}
	for attempt := 0; attempt != 8; attempt++ {
		before := preemptLoad(&clock.sequence)
		if before == 0 || before&1 != 0 {
			continue
		}
		lo := preemptLoad(&clock.lo)
		hi := preemptLoad(&clock.hi)
		if preemptLoad(&clock.sequence) != before {
			continue
		}
		word := uint64(hi)<<32 | uint64(lo)
		if word > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(word), true
	}
	return 0, false
}

func (clock *HostMonotonicClock) Publish(lo, hi uint32) bool {
	if clock == nil || hi&uint32(1<<31) != 0 {
		return false
	}
	nextWord := uint64(hi)<<32 | uint64(lo)
	for attempt := 0; attempt != 8; attempt++ {
		sequence := preemptLoad(&clock.sequence)
		if sequence&1 != 0 || sequence >= ^uint32(0)-1 {
			continue
		}
		if sequence != 0 {
			currentWord := uint64(preemptLoad(&clock.hi))<<32 | uint64(preemptLoad(&clock.lo))
			if nextWord < currentWord || preemptLoad(&clock.sequence) != sequence {
				if nextWord < currentWord {
					return false
				}
				continue
			}
		}
		if !preemptCompareAndSwap(&clock.sequence, sequence, sequence+1) {
			continue
		}
		preemptStore(&clock.lo, lo)
		preemptStore(&clock.hi, hi)
		preemptStore(&clock.sequence, sequence+2)
		return true
	}
	return false
}
