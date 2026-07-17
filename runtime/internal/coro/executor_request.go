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

// ExecutorRequestCapacity is the number of stable executor request gates in
// one fixed registry. The first runtime profile uses one entry; the explicit
// capacity keeps embedded/static-memory exhaustion deterministic.
const ExecutorRequestCapacity = 8

// ExecutorHandle is the complete platform-facing executor identity. Platform
// code retains only these two uint32 values and never a P, G, Go pointer, wait
// token, or LLVM coroutine handle.
type ExecutorHandle struct {
	Slot       uint32
	Generation uint32
}

// ExecutorRequestResult classifies one platform request. IdleWake requires a
// platform doorbell. Published means the executor is still running and will
// observe the request at a compiler safepoint; Coalesced relies on that first
// request or its already-retained doorbell.
type ExecutorRequestResult uint8

const (
	ExecutorRequestInvalid ExecutorRequestResult = iota
	ExecutorRequestPublished
	ExecutorRequestIdleWake
	ExecutorRequestCoalesced
	ExecutorRequestClosed
	ExecutorRequestStale
)

func ExecutorRequestNeedsDoorbell(result ExecutorRequestResult) bool {
	return result == ExecutorRequestIdleWake
}

const (
	executorGateRequested uint32 = 1 << iota
	executorGateIdleArmed
	executorGateClosed
	executorGateMask = executorGateRequested | executorGateIdleArmed | executorGateClosed
)

type executorLifecycle uint32

const (
	executorFree executorLifecycle = iota
	executorInitializing
	executorActive
	executorClosing
	executorQuiesced
)

const (
	executorProducerClosed = uint32(1 << 31)
	executorProducerMask   = executorProducerClosed - 1
)

type executorRequestSlot struct {
	// Every platform-visible word is an aligned uint32 atomic. The first slice
	// deliberately has no scheduler-owned pointer suffix.
	state      uint32
	generation uint32
	inflight   uint32
	gate       uint32
}

// ExecutorRegistry owns stable request gates. It must remain at a stable
// address until the platform backend has strongly unregistered/joined every
// callback and CanRelease reports true; it must not be copied after first use.
//
// Request is the only producer-concurrent mutating method. Register,
// Acknowledge, ArmIdle, LeaveIdle, BeginClose, ConfirmQuiesced, Retire, and
// CanRelease belong to one scheduler owner and are serialized. ObserveRequested
// may run at a compiler safepoint on that same executor.
//
// The request gate is advisory. Posted wait slots, timer epochs, and other
// durable sources remain the truth. The scheduler protocol is:
//
//  1. drain all durable sources;
//  2. Acknowledge the coalesced request;
//  3. recheck every durable source and loop if any appeared before the ack;
//  4. ArmIdle with a 0 -> IdleArmed CAS and recheck sources once more;
//  5. CommitSleep against the exact IdleArmed word;
//  6. enter the platform's retained-doorbell wait.
//
// A successful CommitSleep is not by itself a blocking primitive. The target
// wait must retain a doorbell delivered after that CAS but before the physical
// block (for example an eventfd/pipe byte, a latched event-loop task, or an
// interrupt-pending bit). After wake the scheduler calls LeaveIdle before it
// drains and acknowledges.
//
// ObserveRequested never acknowledges. A running G only yields; the scheduler
// clears the request after it has regained ownership and drained sources.
type ExecutorRegistry struct {
	slots [ExecutorRequestCapacity]executorRequestSlot
}

func executorSlot(registry *ExecutorRegistry, handle ExecutorHandle) (*executorRequestSlot, bool) {
	if registry == nil || handle.Slot == 0 || handle.Slot > ExecutorRequestCapacity || handle.Generation == 0 {
		return nil, false
	}
	return &registry.slots[handle.Slot-1], true
}

func executorAcquireProducer(slot *executorRequestSlot) bool {
	if slot == nil {
		return false
	}
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&executorProducerClosed != 0 || inflight&executorProducerMask == executorProducerMask {
			return false
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight+1) {
			return true
		}
	}
}

func executorReleaseProducer(slot *executorRequestSlot) {
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&executorProducerMask == 0 {
			return
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight-1) {
			return
		}
	}
}

func executorSealProducers(slot *executorRequestSlot) bool {
	if slot == nil {
		return false
	}
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&executorProducerClosed != 0 {
			return true
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight|executorProducerClosed) {
			return true
		}
	}
}

func executorProducersQuiesced(slot *executorRequestSlot) bool {
	return slot != nil && preemptLoad(&slot.inflight) == executorProducerClosed
}

func executorFreeSlotReusable(generation, inflight, gate uint32) bool {
	pristine := generation == 0 && inflight == 0 && gate == 0
	retired := generation != 0 && inflight == executorProducerClosed && gate == executorGateClosed
	return pristine || retired
}

// Register publishes one new stable executor generation. It is
// scheduler-owner-only and allocation-free.
func (registry *ExecutorRegistry) Register() (ExecutorHandle, bool) {
	if registry == nil {
		return ExecutorHandle{}, false
	}
	for index := range registry.slots {
		slot := &registry.slots[index]
		if preemptLoad(&slot.state) != uint32(executorFree) {
			continue
		}
		generation := preemptLoad(&slot.generation)
		if generation == ^uint32(0) {
			continue
		}
		inflight := preemptLoad(&slot.inflight)
		gate := preemptLoad(&slot.gate)
		if !executorFreeSlotReusable(generation, inflight, gate) ||
			!preemptCompareAndSwap(&slot.state, uint32(executorFree), uint32(executorInitializing)) {
			continue
		}
		if !executorSealProducers(slot) || !executorProducersQuiesced(slot) {
			// Invalid pre-registration ingress leaves this slot fail-closed.
			continue
		}
		generation++
		if generation == 0 {
			return ExecutorHandle{}, false
		}
		preemptStore(&slot.generation, generation)
		preemptStore(&slot.gate, 0)
		if !preemptCompareAndSwap(&slot.inflight, executorProducerClosed, 0) {
			return ExecutorHandle{}, false
		}
		preemptStore(&slot.state, uint32(executorActive))
		return ExecutorHandle{Slot: uint32(index) + 1, Generation: generation}, true
	}
	return ExecutorHandle{}, false
}

// Request publishes one coalesced executor request. A producer must publish
// its durable source before this call and ring the platform doorbell only when
// ExecutorRequestNeedsDoorbell reports true.
func (registry *ExecutorRegistry) Request(handle ExecutorHandle) ExecutorRequestResult {
	slot, ok := executorSlot(registry, handle)
	if !ok {
		return ExecutorRequestInvalid
	}
	if !executorAcquireProducer(slot) {
		// Do not inspect the slot after a denied lease: strong backend shutdown
		// may release the stable registry as soon as all entered calls return.
		return ExecutorRequestClosed
	}
	if preemptLoad(&slot.generation) != handle.Generation {
		executorReleaseProducer(slot)
		return ExecutorRequestStale
	}
	if preemptLoad(&slot.state) != uint32(executorActive) {
		executorReleaseProducer(slot)
		return ExecutorRequestClosed
	}
	for {
		gate := preemptLoad(&slot.gate)
		if gate&^executorGateMask != 0 || gate&executorGateClosed != 0 {
			executorReleaseProducer(slot)
			return ExecutorRequestClosed
		}
		if gate&executorGateRequested != 0 {
			executorReleaseProducer(slot)
			return ExecutorRequestCoalesced
		}
		if !preemptCompareAndSwap(&slot.gate, gate, gate|executorGateRequested) {
			continue
		}
		executorReleaseProducer(slot)
		if gate&executorGateIdleArmed != 0 {
			return ExecutorRequestIdleWake
		}
		return ExecutorRequestPublished
	}
}

// ObserveRequested is an acquire observation for a running compiler safepoint.
// It never clears the request or drains a source.
func (registry *ExecutorRegistry) ObserveRequested(handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	if !ok || preemptLoad(&slot.generation) != handle.Generation {
		return false
	}
	gate := preemptLoad(&slot.gate)
	return gate&^executorGateMask == 0 && gate&executorGateClosed == 0 && gate&executorGateRequested != 0
}

// Acknowledge clears the advisory request after the scheduler has drained all
// durable sources. The caller must recheck those sources after this CAS because
// a producer may have coalesced immediately before the clear.
func (registry *ExecutorRegistry) Acknowledge(handle ExecutorHandle) (bool, bool) {
	slot, ok := executorSlot(registry, handle)
	if !ok || preemptLoad(&slot.generation) != handle.Generation {
		return false, false
	}
	for {
		gate := preemptLoad(&slot.gate)
		switch gate {
		case 0:
			return false, true
		case executorGateRequested:
			if preemptCompareAndSwap(&slot.gate, executorGateRequested, 0) {
				return true, true
			}
		default:
			return false, false
		}
	}
}

// ArmIdle publishes the final scheduler intention to enter a retained-doorbell
// platform wait. It succeeds only from the exact active zero gate.
func (registry *ExecutorRegistry) ArmIdle(handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	return ok && preemptLoad(&slot.generation) == handle.Generation &&
		preemptLoad(&slot.state) == uint32(executorActive) &&
		preemptCompareAndSwap(&slot.gate, 0, executorGateIdleArmed)
}

// CommitSleep is the final scheduler-side validation after ArmIdle and the
// last durable-source recheck. It succeeds only while the gate remains exactly
// IdleArmed. A request that won before this CAS makes it fail; one that wins
// afterward must ring the target's retained doorbell.
func (registry *ExecutorRegistry) CommitSleep(handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	return ok && preemptLoad(&slot.generation) == handle.Generation &&
		preemptLoad(&slot.state) == uint32(executorActive) &&
		preemptCompareAndSwap(&slot.gate, executorGateIdleArmed, executorGateIdleArmed)
}

// LeaveIdle clears IdleArmed after a real or spurious platform wake while
// preserving a concurrently published Requested or Closed bit.
func (registry *ExecutorRegistry) LeaveIdle(handle ExecutorHandle) (bool, bool) {
	slot, ok := executorSlot(registry, handle)
	if !ok || preemptLoad(&slot.generation) != handle.Generation {
		return false, false
	}
	for {
		gate := preemptLoad(&slot.gate)
		switch gate {
		case 0, executorGateRequested:
			return false, true
		case executorGateIdleArmed, executorGateIdleArmed | executorGateRequested:
			if preemptCompareAndSwap(&slot.gate, gate, gate&^executorGateIdleArmed) {
				return true, true
			}
		default:
			return false, false
		}
	}
}

func (registry *ExecutorRegistry) idleArmed(handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	if !ok || preemptLoad(&slot.generation) != handle.Generation {
		return false
	}
	gate := preemptLoad(&slot.gate)
	return gate&^executorGateMask == 0 && gate&executorGateClosed == 0 && gate&executorGateIdleArmed != 0
}

// BeginClose seals producer admission for backend shutdown. The scheduler must
// leave idle and drain/ack all durable sources first. Its exact 0 -> Closed CAS
// races Request directly: a published request makes close fail without changing
// lifecycle, while a winning close makes that producer report Closed. The
// physical backend then prevents new Request calls and joins all calls that
// entered before their slot lease.
//
// Closing may win after a producer has published a durable source but before
// it calls Request. After the physical backend join, the scheduler must perform
// one final unconditional durable-source drain before ConfirmQuiesced.
func (registry *ExecutorRegistry) BeginClose(handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	if !ok || preemptLoad(&slot.generation) != handle.Generation ||
		preemptLoad(&slot.state) != uint32(executorActive) ||
		!preemptCompareAndSwap(&slot.gate, 0, executorGateClosed) {
		return false
	}
	// Scheduler-owner serialization makes the lifecycle CAS deterministic.
	// Any contract violation remains fail-closed with the gate already Closed.
	if !preemptCompareAndSwap(&slot.state, uint32(executorActive), uint32(executorClosing)) ||
		!executorSealProducers(slot) {
		return false
	}
	return true
}

// ConfirmQuiesced records a strong backend unregister/join acknowledgement:
// no new Request may start and every platform shim that had entered, including
// one paused before taking a slot lease or between Request and its doorbell,
// has returned. The scheduler must already have performed the final
// post-backend-join durable-source drain required by BeginClose.
func (registry *ExecutorRegistry) canConfirmQuiesced(handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	return ok && preemptLoad(&slot.generation) == handle.Generation && executorProducersQuiesced(slot) &&
		preemptLoad(&slot.gate) == executorGateClosed && preemptLoad(&slot.state) == uint32(executorClosing)
}

func (registry *ExecutorRegistry) ConfirmQuiesced(handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	return ok && registry.canConfirmQuiesced(handle) &&
		preemptCompareAndSwap(&slot.state, uint32(executorClosing), uint32(executorQuiesced))
}

func (registry *ExecutorRegistry) Retire(handle ExecutorHandle) bool {
	slot, ok := executorSlot(registry, handle)
	if !ok || preemptLoad(&slot.generation) != handle.Generation || !executorProducersQuiesced(slot) ||
		preemptLoad(&slot.gate) != executorGateClosed ||
		!preemptCompareAndSwap(&slot.state, uint32(executorQuiesced), uint32(executorFree)) {
		return false
	}
	return true
}

func (registry *ExecutorRegistry) CanRelease() bool {
	if registry == nil {
		return false
	}
	for index := range registry.slots {
		slot := &registry.slots[index]
		generation := preemptLoad(&slot.generation)
		inflight := preemptLoad(&slot.inflight)
		gate := preemptLoad(&slot.gate)
		if preemptLoad(&slot.state) != uint32(executorFree) ||
			!executorFreeSlotReusable(generation, inflight, gate) {
			return false
		}
	}
	return true
}

// WaitExecutorPostResult exposes both halves of the platform ingress model.
// A target shim resolves stable registry/table instances internally; its ABI
// still carries only the two POD handles.
type WaitExecutorPostResult struct {
	Wait     WaitRegistrationPostResult
	Executor ExecutorRequestResult
}

// PostWaitAndRequest publishes the durable wait slot before requesting its
// executor. It never drains, touches P/G, or invokes an LLVM coroutine action.
func PostWaitAndRequest(table *WaitRegistrationTable, wait WaitRegistrationHandle, registry *ExecutorRegistry, executor ExecutorHandle) WaitExecutorPostResult {
	result := WaitExecutorPostResult{Wait: table.Post(wait), Executor: ExecutorRequestInvalid}
	if result.Wait == WaitRegistrationPosted {
		result.Executor = registry.Request(executor)
	}
	return result
}
