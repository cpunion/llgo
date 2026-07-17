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

// TaskControlSourceCapacity bounds the number of tasks explicitly exported to
// a host at once. Ordinary goroutines do not consume a slot. Static targets may
// choose a different generated source while preserving the two-word ID ABI.
const TaskControlSourceCapacity = 8

type TaskControlPostResult uint8

const (
	TaskControlPostInvalid TaskControlPostResult = iota
	TaskControlPosted
	TaskControlCoalesced
	TaskControlPostClosed
	TaskControlPostStale
)

type taskControlLifecycle uint32

const (
	taskControlFree taskControlLifecycle = iota
	taskControlInitializing
	taskControlActive
	taskControlClosing
	taskControlQuiesced
)

const (
	taskControlProducerClosed = uint32(1 << 31)
	taskControlProducerMask   = taskControlProducerClosed - 1
)

type taskControlSlot struct {
	// Producer-visible prefix. A host keeps only OperationID and reaches these
	// aligned atomic words through a stable target-owned source.
	state      uint32
	generation uint32
	inflight   uint32
	request    uint32

	// Owner-only suffix. Producers never read or retain the G pointer.
	task *G
}

// TaskControlSource is the cross-thread ingress for cooperative task abort and
// shutdown. Post only merges a durable monotonic request. The owner P later
// drains it through RequestTaskCancellation at the common source quiet cut;
// producer threads never run Go cleanup, touch a ParkState, or resume a frame.
//
// The source has a stable address from Bind through Unbind. A target shim must
// Post before it requests the common executor doorbell. Closing seals producer
// admission; ConfirmQuiesced additionally requires every admitted Post to have
// returned and a final owner drain to have consumed any late request.
type TaskControlSource struct {
	pending uint32
	slots   [TaskControlSourceCapacity]taskControlSlot
	owner   *P
}

func taskControlSlotFor(source *TaskControlSource, id OperationID) (*taskControlSlot, bool) {
	if source == nil || !id.Valid() || id.Source() != OperationSourceControl ||
		id.Slot() == 0 || id.Slot() > TaskControlSourceCapacity {
		return nil, false
	}
	return &source.slots[id.Slot()-1], true
}

func taskControlAcquireProducer(slot *taskControlSlot) bool {
	if slot == nil {
		return false
	}
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&taskControlProducerClosed != 0 || inflight&taskControlProducerMask == taskControlProducerMask {
			return false
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight+1) {
			return true
		}
	}
}

func taskControlReleaseProducer(slot *taskControlSlot) {
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&taskControlProducerMask == 0 {
			return
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight-1) {
			return
		}
	}
}

func taskControlSealProducers(slot *taskControlSlot) bool {
	if slot == nil {
		return false
	}
	for {
		inflight := preemptLoad(&slot.inflight)
		if inflight&taskControlProducerClosed != 0 {
			return true
		}
		if preemptCompareAndSwap(&slot.inflight, inflight, inflight|taskControlProducerClosed) {
			return true
		}
	}
}

func taskControlProducersQuiesced(slot *taskControlSlot) bool {
	return slot != nil && preemptLoad(&slot.inflight) == taskControlProducerClosed
}

func taskControlReusableSlot(slot *taskControlSlot) bool {
	if slot == nil || preemptLoad(&slot.state) != uint32(taskControlFree) ||
		preemptLoad(&slot.request) != uint32(TaskCancelNone) || slot.task != nil {
		return false
	}
	generation := preemptLoad(&slot.generation)
	if generation == 0 {
		return preemptLoad(&slot.inflight) == 0
	}
	return preemptLoad(&slot.inflight) == taskControlProducerClosed
}

func validTaskControlOwner(source *TaskControlSource, p *P) bool {
	return source != nil && p != nil && source.owner == p
}

// RegisterTaskControl allocates an external handle for an already owner-P
// managed task. It is intentionally explicit and owner-only.
func RegisterTaskControl(source *TaskControlSource, p *P, task *G) (OperationID, bool) {
	if !validTaskControlOwner(source, p) || !pOwnsTaskCancellation(p, task) || task.taskControlLeases == ^uint8(0) {
		return OperationID{}, false
	}
	for index := range source.slots {
		slot := &source.slots[index]
		generation := preemptLoad(&slot.generation)
		if generation == ^uint32(0) || !taskControlReusableSlot(slot) ||
			!preemptCompareAndSwap(&slot.state, uint32(taskControlFree), uint32(taskControlInitializing)) {
			continue
		}
		if !taskControlSealProducers(slot) || !taskControlProducersQuiesced(slot) {
			return OperationID{}, false
		}
		id, ok := NextOperationID(OperationID{}, OperationSourceControl, uint32(index)+1)
		if generation != 0 {
			previous, made := MakeOperationID(OperationSourceControl, uint32(index)+1, generation)
			if !made {
				return OperationID{}, false
			}
			id, ok = NextOperationID(previous, OperationSourceControl, uint32(index)+1)
		}
		if !ok {
			return OperationID{}, false
		}
		preemptStore(&slot.request, uint32(TaskCancelNone))
		preemptStore(&slot.generation, id.Generation)
		if !preemptCompareAndSwap(&slot.inflight, taskControlProducerClosed, 0) {
			return OperationID{}, false
		}
		slot.task = task
		task.taskControlLeases++
		preemptStore(&slot.state, uint32(taskControlActive))
		return id, true
	}
	return OperationID{}, false
}

// Post merges kind using Shutdown > Abort. Even an equal/weaker request uses
// a same-value CAS: that RMW orders directly against the owner's take CAS, so
// a producer can never observe an old value and have its request silently
// cleared underneath it.
func (source *TaskControlSource) Post(id OperationID, kind TaskCancelKind) TaskControlPostResult {
	slot, ok := taskControlSlotFor(source, id)
	if !ok || !validTaskCancelKind(kind) {
		return TaskControlPostInvalid
	}
	if !taskControlAcquireProducer(slot) {
		return TaskControlPostClosed
	}
	if preemptLoad(&slot.generation) != id.Generation {
		taskControlReleaseProducer(slot)
		return TaskControlPostStale
	}
	if preemptLoad(&slot.state) != uint32(taskControlActive) {
		taskControlReleaseProducer(slot)
		return TaskControlPostClosed
	}
	for {
		old := TaskCancelKind(preemptLoad(&slot.request))
		if old != TaskCancelNone && !validTaskCancelKind(old) {
			taskControlReleaseProducer(slot)
			return TaskControlPostInvalid
		}
		merged := kind
		if old > merged {
			merged = old
		}
		if !preemptCompareAndSwap(&slot.request, uint32(old), uint32(merged)) {
			continue
		}
		preemptStore(&source.pending, 1)
		taskControlReleaseProducer(slot)
		if old == TaskCancelNone || merged > old {
			return TaskControlPosted
		}
		return TaskControlCoalesced
	}
}

func (source *TaskControlSource) Pending() bool {
	return source != nil && preemptLoad(&source.pending) != 0
}

// taskControlRestoreRequest puts an owner-claimed fact back into the atomic
// producer mailbox after delivery proved temporarily impossible. A producer
// may have published another request after the owner's take, so restoration is
// the same monotonic merge as Post rather than a blind store. Returning false
// leaves a corrupt request word fail-closed.
func taskControlRestoreRequest(source *TaskControlSource, slot *taskControlSlot, kind TaskCancelKind) bool {
	if source == nil || slot == nil || !validTaskCancelKind(kind) {
		return false
	}
	for {
		old := TaskCancelKind(preemptLoad(&slot.request))
		if old != TaskCancelNone && !validTaskCancelKind(old) {
			return false
		}
		merged := kind
		if old > merged {
			merged = old
		}
		if !preemptCompareAndSwap(&slot.request, uint32(old), uint32(merged)) {
			continue
		}
		preemptStore(&source.pending, 1)
		return true
	}
}

func (source *TaskControlSource) publishPass(p *P, terminal *G) (delivered, discarded uint32, ok bool) {
	if !validTaskControlOwner(source, p) {
		return 0, 0, false
	}
	preemptStore(&source.pending, 0)
	for index := range source.slots {
		slot := &source.slots[index]
		var kind TaskCancelKind
		for {
			kind = TaskCancelKind(preemptLoad(&slot.request))
			if kind == TaskCancelNone {
				break
			}
			if !validTaskCancelKind(kind) ||
				!preemptCompareAndSwap(&slot.request, uint32(kind), uint32(TaskCancelNone)) {
				if validTaskCancelKind(kind) {
					continue
				}
				return delivered, discarded, false
			}
			break
		}
		if kind == TaskCancelNone {
			continue
		}
		// Drain at most one merged fact per slot and pass. A producer that
		// publishes after the take leaves pending set for the next pass, so a
		// hot control endpoint cannot starve timer, I/O, or IRQ sources.
		state := taskControlLifecycle(preemptLoad(&slot.state))
		switch state {
		case taskControlActive, taskControlClosing:
			generation := preemptLoad(&slot.generation)
			_, valid := MakeOperationID(OperationSourceControl, uint32(index)+1, generation)
			if !valid || slot.task == nil {
				return delivered, discarded, false
			}
			// Once the final LLVM root has been destroyed there is no user or
			// cleanup continuation into which an admitted-late task stop can be
			// delivered. The terminal completion is already committed; the exact
			// endpoint generation remains pinned until the adapter strong-joins
			// it, so consuming this fact is a normal terminal-late discard.
			if terminal != nil && slot.task == terminal {
				discarded++
				continue
			}
			if RequestTaskCancellation(p, slot.task, kind) {
				delivered++
			} else if slot.task.state == GCanceling || slot.task.state == GPanicking || slot.task.state == GDead {
				// A terminal task has no continuation into which a new stop
				// request can be delivered. Its endpoint generation remains
				// pinned until explicit close/join, so this is a normal late
				// host request rather than a stale pointer or driver failure.
				discarded++
			} else {
				// Legacy waits and a future owner migration may reject delivery
				// without making the request invalid. Preserve the durable fact;
				// a later V2 migration/owner pass must still observe it.
				if !taskControlRestoreRequest(source, slot, kind) {
					return delivered, discarded, false
				}
				return delivered, discarded, false
			}
		default:
			return delivered, discarded, false
		}
	}
	return delivered, discarded, true
}

// PublishPass claims every currently visible request and delivers it on the
// owner P. A fact accepted before the close seal remains durable and is still
// delivered from a closing slot; only an already-terminal task discards it.
func (source *TaskControlSource) PublishPass(p *P) (delivered, discarded uint32, ok bool) {
	return source.publishPass(p, nil)
}

// BeginCloseTaskControl withdraws one exact external endpoint and seals new
// Posts. The target must unregister the handle and strong-join all callers,
// then run a final PublishPass before ConfirmTaskControlQuiesced.
func BeginCloseTaskControl(source *TaskControlSource, p *P, id OperationID) bool {
	slot, ok := taskControlSlotFor(source, id)
	return ok && validTaskControlOwner(source, p) && preemptLoad(&slot.generation) == id.Generation &&
		slot.task != nil &&
		preemptCompareAndSwap(&slot.state, uint32(taskControlActive), uint32(taskControlClosing)) &&
		taskControlSealProducers(slot)
}

func ConfirmTaskControlQuiesced(source *TaskControlSource, p *P, id OperationID) bool {
	slot, ok := taskControlSlotFor(source, id)
	if !ok || !validTaskControlOwner(source, p) || preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.state) != uint32(taskControlClosing) || !taskControlProducersQuiesced(slot) ||
		preemptLoad(&slot.request) != uint32(TaskCancelNone) || slot.task == nil || slot.task.taskControlLeases == 0 {
		return false
	}
	slot.task.taskControlLeases--
	slot.task = nil
	preemptStore(&slot.state, uint32(taskControlQuiesced))
	return true
}

func RetireTaskControl(source *TaskControlSource, p *P, id OperationID) bool {
	slot, ok := taskControlSlotFor(source, id)
	return ok && validTaskControlOwner(source, p) && preemptLoad(&slot.generation) == id.Generation &&
		taskControlProducersQuiesced(slot) && preemptLoad(&slot.request) == uint32(TaskCancelNone) && slot.task == nil &&
		preemptCompareAndSwap(&slot.state, uint32(taskControlQuiesced), uint32(taskControlFree))
}

func validTaskControlTerminalSlot(source *TaskControlSource, index int, state taskControlLifecycle) bool {
	slot := &source.slots[index]
	request := TaskCancelKind(preemptLoad(&slot.request))
	if request != TaskCancelNone && !validTaskCancelKind(request) {
		return false
	}
	switch state {
	case taskControlFree:
		return taskControlReusableSlot(slot)
	case taskControlActive, taskControlClosing:
		generation := preemptLoad(&slot.generation)
		if _, ok := MakeOperationID(OperationSourceControl, uint32(index)+1, generation); !ok ||
			slot.task == nil || slot.task.taskControlLeases == 0 {
			return false
		}
		inflight := preemptLoad(&slot.inflight)
		if state == taskControlActive {
			return inflight&taskControlProducerClosed == 0
		}
		return inflight&taskControlProducerClosed != 0
	case taskControlQuiesced:
		generation := preemptLoad(&slot.generation)
		_, ok := MakeOperationID(OperationSourceControl, uint32(index)+1, generation)
		return ok && request == TaskCancelNone && taskControlProducersQuiesced(slot) && slot.task == nil
	default:
		return false
	}
}

func taskControlTerminalLeaseCountsValid(source *TaskControlSource) bool {
	for index := range source.slots {
		slot := &source.slots[index]
		state := taskControlLifecycle(preemptLoad(&slot.state))
		if state != taskControlActive && state != taskControlClosing {
			continue
		}
		needed := uint8(1)
		for prior := 0; prior < index; prior++ {
			other := &source.slots[prior]
			otherState := taskControlLifecycle(preemptLoad(&other.state))
			if (otherState == taskControlActive || otherState == taskControlClosing) && other.task == slot.task {
				if needed == ^uint8(0) {
					return false
				}
				needed++
			}
		}
		if slot.task == nil || slot.task.taskControlLeases < needed {
			return false
		}
	}
	return true
}

// taskControlSourceCanBeginTerminalClose permits live task endpoints while
// requiring every non-free slot to be a structurally valid exact generation.
// The last-G executor path uses this predicate instead of pretending the
// control source is empty before its adapter has had a chance to strong-join
// those endpoints.
func taskControlSourceCanBeginTerminalClose(source *TaskControlSource, p *P) bool {
	if !validTaskControlOwner(source, p) {
		return false
	}
	for index := range source.slots {
		state := taskControlLifecycle(preemptLoad(&source.slots[index].state))
		if !validTaskControlTerminalSlot(source, index, state) {
			return false
		}
	}
	return taskControlTerminalLeaseCountsValid(source)
}

// beginTaskControlSourceTerminalClose seals every explicitly exported task
// endpoint as one owner-side transaction. Calls admitted before the seal keep
// their inflight lease and may still publish into Closing; no G lease or slot
// generation is released until the adapter's later strong-join confirmation.
func beginTaskControlSourceTerminalClose(source *TaskControlSource, p *P) bool {
	if !taskControlSourceCanBeginTerminalClose(source, p) {
		return false
	}
	for index := range source.slots {
		slot := &source.slots[index]
		switch state := taskControlLifecycle(preemptLoad(&slot.state)); state {
		case taskControlFree:
		case taskControlActive:
			if !preemptCompareAndSwap(&slot.state, uint32(state), uint32(taskControlClosing)) ||
				!taskControlSealProducers(slot) {
				return false
			}
		case taskControlClosing:
			if !taskControlSealProducers(slot) {
				return false
			}
		case taskControlQuiesced:
			if !preemptCompareAndSwap(&slot.state, uint32(state), uint32(taskControlFree)) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (source *TaskControlSource) publishTerminalPass(p *P, terminal *G) (delivered, discarded uint32, ok bool) {
	if terminal == nil {
		return 0, 0, false
	}
	return source.publishPass(p, terminal)
}

func taskControlSourceCanFinishTerminalClose(source *TaskControlSource, p *P) bool {
	if !validTaskControlOwner(source, p) || preemptLoad(&source.pending) != 0 {
		return false
	}
	for index := range source.slots {
		slot := &source.slots[index]
		state := taskControlLifecycle(preemptLoad(&slot.state))
		switch state {
		case taskControlFree:
			if !taskControlReusableSlot(slot) {
				return false
			}
		case taskControlClosing:
			if !validTaskControlTerminalSlot(source, index, state) || !taskControlProducersQuiesced(slot) ||
				preemptLoad(&slot.request) != uint32(TaskCancelNone) {
				return false
			}
		case taskControlQuiesced:
			if !validTaskControlTerminalSlot(source, index, state) {
				return false
			}
		default:
			return false
		}
	}
	return taskControlTerminalLeaseCountsValid(source)
}

// finishTaskControlSourceTerminalClose consumes the external strong-join
// assertion. Its preflight is mutation-free, so an early Confirm call cannot
// release only a prefix of G leases. Once every admitted Post is quiescent and
// the final terminal publish pass has emptied every mailbox, all endpoints are
// quiesced and retired before the source can be unbound.
func finishTaskControlSourceTerminalClose(source *TaskControlSource, p *P) bool {
	if !taskControlSourceCanFinishTerminalClose(source, p) {
		return false
	}
	for index := range source.slots {
		slot := &source.slots[index]
		if taskControlLifecycle(preemptLoad(&slot.state)) != taskControlClosing {
			continue
		}
		slot.task.taskControlLeases--
		slot.task = nil
		preemptStore(&slot.state, uint32(taskControlQuiesced))
	}
	for index := range source.slots {
		slot := &source.slots[index]
		if taskControlLifecycle(preemptLoad(&slot.state)) == taskControlQuiesced &&
			!preemptCompareAndSwap(&slot.state, uint32(taskControlQuiesced), uint32(taskControlFree)) {
			return false
		}
	}
	return taskControlSourceEmpty(source, p)
}

func taskControlSourceEmpty(source *TaskControlSource, p *P) bool {
	if source == nil || source.owner != p || preemptLoad(&source.pending) != 0 {
		return false
	}
	for index := range source.slots {
		if !taskControlReusableSlot(&source.slots[index]) {
			return false
		}
	}
	return true
}

func BindTaskControlSource(source *TaskControlSource, p *P) bool {
	if p == nil || !taskControlSourceEmpty(source, nil) {
		return false
	}
	source.owner = p
	return true
}

func UnbindTaskControlSource(source *TaskControlSource, p *P) bool {
	if !taskControlSourceEmpty(source, p) {
		return false
	}
	source.owner = nil
	return true
}

func (source *TaskControlSource) CanRelease() bool {
	return taskControlSourceEmpty(source, nil)
}

type TaskControlExecutorPostResult struct {
	Control  TaskControlPostResult
	Executor ExecutorRequestResult
}

// PostTaskControlAndRequest preserves the universal producer order: durable
// fact first, advisory executor request second. A coalesced control request is
// already represented by an earlier fact/request and needs no second wake.
func PostTaskControlAndRequest(source *TaskControlSource, id OperationID, kind TaskCancelKind, registry *ExecutorRegistry, executor ExecutorHandle) TaskControlExecutorPostResult {
	result := TaskControlExecutorPostResult{Control: source.Post(id, kind), Executor: ExecutorRequestInvalid}
	if result.Control == TaskControlPosted {
		result.Executor = registry.Request(executor)
	}
	return result
}
