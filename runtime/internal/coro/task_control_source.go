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

type taskControlSlot struct {
	// Producer-visible prefix. A host keeps only OperationID and reaches these
	// aligned atomic words through a stable target-owned source.
	producerSourceSlot
	request uint32

	// Owner-only suffix. Producers never read or retain the G pointer.
	task *G
}

// TaskControlSource is the cross-thread ingress for cooperative task abort and
// shutdown. Post only merges a durable monotonic request. The owner P later
// drains it through the common owner-side cancellation mutation core in a
// published epoch; producer threads never run Go cleanup, touch a ParkState,
// or resume a frame.
//
// The source has a stable address from Bind through Unbind. A target shim must
// Post before it requests the common executor doorbell. Closing seals producer
// admission; ConfirmQuiesced additionally requires every admitted Post to have
// returned and a final owner drain to have consumed any late request.
type TaskControlSource struct {
	routedProducerSource
	slots [TaskControlSourceCapacity]taskControlSlot
}

func taskControlSlotFor(source *TaskControlSource, id OperationID) (*taskControlSlot, bool) {
	if source == nil || !source.route.Valid() || !id.Valid() || id.Source() != OperationSourceControl ||
		id.Route() != source.route || id.LocalSlot() == 0 || id.LocalSlot() > TaskControlSourceCapacity {
		return nil, false
	}
	return &source.slots[id.LocalSlot()-1], true
}

func taskControlReusableSlot(slot *taskControlSlot) bool {
	if slot == nil || !producerSourceSlotReusable(&slot.producerSourceSlot) ||
		preemptLoad(&slot.request) != uint32(TaskCancelNone) || slot.task != nil {
		return false
	}
	return true
}

func validTaskControlOwner(source *TaskControlSource, p *P) bool {
	return source != nil && validRoutedProducerSource(&source.routedProducerSource, p)
}

// registeredTaskControlDelivery proves that one exact endpoint still pins its
// owner-only G lease to source.owner. The final state-specific ownership check
// is O(1); unlike the public arbitrary-G cancellation APIs, it never audits an
// unrelated ready or legacy-wait queue tail.
func registeredTaskControlDelivery(source *TaskControlSource, p *P, slot *taskControlSlot, id OperationID) (*G, bool) {
	exact, ok := taskControlSlotFor(source, id)
	if !ok || !validTaskControlOwner(source, p) || exact != slot ||
		preemptLoad(&slot.generation) != id.Generation {
		return nil, false
	}
	state := producerSourceLifecycle(preemptLoad(&slot.state))
	if state != producerSourceActive && state != producerSourceClosing {
		return nil, false
	}
	task := slot.task
	if task == nil || task.taskControlLeases == 0 || !pOwnsRegisteredTaskCancellation(p, task) {
		return nil, false
	}
	return task, true
}

func requestRegisteredTaskCancellation(source *TaskControlSource, p *P, slot *taskControlSlot, id OperationID, kind TaskCancelKind) bool {
	task, ok := registeredTaskControlDelivery(source, p, slot, id)
	return ok && requestTaskCancellationOwned(p, task, kind, taskCancellationProofRegistered)
}

// RegisterTaskControl allocates an external handle for an already owner-P
// managed task. It is intentionally explicit and owner-only.
func RegisterTaskControl(source *TaskControlSource, p *P, task *G) (OperationID, bool) {
	if !validTaskControlOwner(source, p) || !pOwnsTaskCancellation(p, task) || task.taskControlLeases == ^uint8(0) {
		return OperationID{}, false
	}
	for index := range source.slots {
		slot := &source.slots[index]
		if !taskControlReusableSlot(slot) || preemptLoad(&slot.generation) == ^uint32(0) {
			continue
		}
		generation, begun := beginProducerSourceSlot(&slot.producerSourceSlot)
		if !begun {
			return OperationID{}, false
		}
		id, ok := MakeOperationIDAtRoute(OperationSourceControl, source.route, uint32(index)+1, generation)
		if !ok {
			return OperationID{}, false
		}
		preemptStore(&slot.request, uint32(TaskCancelNone))
		slot.task = task
		task.taskControlLeases++
		if !activateProducerSourceSlot(&slot.producerSourceSlot, generation) {
			return OperationID{}, false
		}
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
	switch acquireProducerSourceGeneration(&slot.producerSourceSlot, id.Generation) {
	case producerSourceAcquireClosed:
		return TaskControlPostClosed
	case producerSourceAcquireStale:
		return TaskControlPostStale
	case producerSourceAcquired:
	default:
		return TaskControlPostInvalid
	}
	if preemptLoad(&slot.state) != uint32(producerSourceActive) {
		producerAdmissionRelease(&slot.inflight)
		return TaskControlPostClosed
	}
	for {
		old := TaskCancelKind(preemptLoad(&slot.request))
		if old != TaskCancelNone && !validTaskCancelKind(old) {
			producerAdmissionRelease(&slot.inflight)
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
		producerAdmissionRelease(&slot.inflight)
		if old == TaskCancelNone || merged > old {
			return TaskControlPosted
		}
		return TaskControlCoalesced
	}
}

func (source *TaskControlSource) Pending() bool {
	return source != nil && routedProducerPending(&source.routedProducerSource)
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

func (source *TaskControlSource) beginPublishPass(p *P) bool {
	return source != nil && beginRoutedProducerPass(&source.routedProducerSource, p)
}

// publishSlot claims at most one merged request from one real endpoint. A
// producer which posts after this cursor position leaves pending set and is
// serviced by the next epoch instead of extending this one indefinitely.
func (source *TaskControlSource) publishSlot(p *P, terminal *G, index uint32) (delivered, discarded uint32, ok bool) {
	if !validTaskControlOwner(source, p) || index >= uint32(len(source.slots)) {
		return 0, 0, false
	}
	slot := &source.slots[index]
	kind := TaskCancelKind(preemptLoad(&slot.request))
	if kind == TaskCancelNone {
		return 0, 0, true
	}
	if !validTaskCancelKind(kind) {
		return 0, 0, false
	}
	if !preemptCompareAndSwap(&slot.request, uint32(kind), uint32(TaskCancelNone)) {
		// A producer upgraded or same-value-CASed the monotonic mailbox after
		// our load. Do not spin inside this catalog entry: its request remains
		// sticky and producer pending/request publication (plus epoch B) makes
		// it visible to a later pass.
		return 0, 0, true
	}

	// Drain at most one merged fact per slot and pass. A producer that
	// publishes after the take leaves pending set for the next pass, so a hot
	// control endpoint cannot starve timer, I/O, or IRQ sources.
	state := producerSourceLifecycle(preemptLoad(&slot.state))
	switch state {
	case producerSourceActive, producerSourceClosing:
		generation := preemptLoad(&slot.generation)
		id, valid := MakeOperationIDAtRoute(OperationSourceControl, source.route, index+1, generation)
		if !valid || slot.task == nil {
			return 0, 0, false
		}
		// Once the final LLVM root has been destroyed there is no user or
		// cleanup continuation into which an admitted-late task stop can be
		// delivered. The endpoint remains pinned until the adapter joins it.
		if terminal != nil && slot.task == terminal {
			return 0, 1, true
		}
		if requestRegisteredTaskCancellation(source, p, slot, id, kind) {
			return 1, 0, true
		}
		if slot.task.state == GCanceling || slot.task.state == GPanicking || slot.task.state == GDead {
			return 0, 1, true
		}
		// Legacy waits and a future owner migration may reject delivery without
		// making the request invalid. Restore the exact durable fact and report
		// no progress; a later external state transition must make it applicable.
		if !taskControlRestoreRequest(source, slot, kind) {
			return 0, 0, false
		}
		return 0, 0, false
	default:
		return 0, 0, false
	}
}

func (source *TaskControlSource) publishPass(p *P, terminal *G) (delivered, discarded uint32, ok bool) {
	if !source.beginPublishPass(p) {
		return 0, 0, false
	}
	for index := range source.slots {
		oneDelivered, oneDiscarded, slotOK := source.publishSlot(p, terminal, uint32(index))
		delivered += oneDelivered
		discarded += oneDiscarded
		if !slotOK {
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
		slot.task != nil && beginProducerSourceClose(&slot.producerSourceSlot) == producerSourceCloseStarted
}

func ConfirmTaskControlQuiesced(source *TaskControlSource, p *P, id OperationID) bool {
	slot, ok := taskControlSlotFor(source, id)
	if !ok || !validTaskControlOwner(source, p) || preemptLoad(&slot.generation) != id.Generation ||
		preemptLoad(&slot.state) != uint32(producerSourceClosing) || !producerSourceSlotQuiesced(&slot.producerSourceSlot) ||
		preemptLoad(&slot.request) != uint32(TaskCancelNone) || slot.task == nil || slot.task.taskControlLeases == 0 {
		return false
	}
	slot.task.taskControlLeases--
	slot.task = nil
	return markProducerSourceQuiesced(&slot.producerSourceSlot)
}

func RetireTaskControl(source *TaskControlSource, p *P, id OperationID) bool {
	slot, ok := taskControlSlotFor(source, id)
	return ok && validTaskControlOwner(source, p) && preemptLoad(&slot.generation) == id.Generation &&
		producerSourceSlotQuiesced(&slot.producerSourceSlot) && preemptLoad(&slot.request) == uint32(TaskCancelNone) &&
		slot.task == nil && recycleProducerSourceSlot(&slot.producerSourceSlot)
}

func validTaskControlTerminalSlot(source *TaskControlSource, index int, state producerSourceLifecycle) bool {
	slot := &source.slots[index]
	request := TaskCancelKind(preemptLoad(&slot.request))
	if request != TaskCancelNone && !validTaskCancelKind(request) {
		return false
	}
	switch state {
	case producerSourceFree:
		return taskControlReusableSlot(slot)
	case producerSourceActive, producerSourceClosing:
		generation := preemptLoad(&slot.generation)
		if _, ok := MakeOperationIDAtRoute(OperationSourceControl, source.route, uint32(index)+1, generation); !ok ||
			slot.task == nil || slot.task.taskControlLeases == 0 {
			return false
		}
		inflight := preemptLoad(&slot.inflight)
		if state == producerSourceActive {
			return inflight&producerAdmissionClosed == 0
		}
		return inflight&producerAdmissionClosed != 0
	case producerSourceQuiesced:
		generation := preemptLoad(&slot.generation)
		_, ok := MakeOperationIDAtRoute(OperationSourceControl, source.route, uint32(index)+1, generation)
		return ok && request == TaskCancelNone && producerSourceSlotQuiesced(&slot.producerSourceSlot) && slot.task == nil
	default:
		return false
	}
}

func taskControlTerminalLeaseCountsValid(source *TaskControlSource) bool {
	for index := range source.slots {
		slot := &source.slots[index]
		state := producerSourceLifecycle(preemptLoad(&slot.state))
		if state != producerSourceActive && state != producerSourceClosing {
			continue
		}
		needed := uint8(1)
		for prior := 0; prior < index; prior++ {
			other := &source.slots[prior]
			otherState := producerSourceLifecycle(preemptLoad(&other.state))
			if (otherState == producerSourceActive || otherState == producerSourceClosing) && other.task == slot.task {
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
		state := producerSourceLifecycle(preemptLoad(&source.slots[index].state))
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
		switch state := producerSourceLifecycle(preemptLoad(&slot.state)); state {
		case producerSourceFree:
		case producerSourceActive:
			if beginProducerSourceClose(&slot.producerSourceSlot) != producerSourceCloseStarted {
				return false
			}
		case producerSourceClosing:
			if !producerAdmissionSeal(&slot.inflight) {
				return false
			}
		case producerSourceQuiesced:
			if !recycleProducerSourceSlot(&slot.producerSourceSlot) {
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
	if !validTaskControlOwner(source, p) || routedProducerPending(&source.routedProducerSource) {
		return false
	}
	for index := range source.slots {
		slot := &source.slots[index]
		state := producerSourceLifecycle(preemptLoad(&slot.state))
		switch state {
		case producerSourceFree:
			if !taskControlReusableSlot(slot) {
				return false
			}
		case producerSourceClosing:
			if !validTaskControlTerminalSlot(source, index, state) ||
				!producerSourceSlotQuiesced(&slot.producerSourceSlot) ||
				preemptLoad(&slot.request) != uint32(TaskCancelNone) {
				return false
			}
		case producerSourceQuiesced:
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
		if producerSourceLifecycle(preemptLoad(&slot.state)) != producerSourceClosing {
			continue
		}
		slot.task.taskControlLeases--
		slot.task = nil
		if !markProducerSourceQuiesced(&slot.producerSourceSlot) {
			return false
		}
	}
	for index := range source.slots {
		slot := &source.slots[index]
		if producerSourceLifecycle(preemptLoad(&slot.state)) == producerSourceQuiesced &&
			!recycleProducerSourceSlot(&slot.producerSourceSlot) {
			return false
		}
	}
	return taskControlSourceEmpty(source, p)
}

func taskControlSourceEmpty(source *TaskControlSource, p *P) bool {
	if source == nil || !routedProducerHeaderEmpty(&source.routedProducerSource, p) {
		return false
	}
	for index := range source.slots {
		if !taskControlReusableSlot(&source.slots[index]) {
			return false
		}
	}
	return true
}

func BindTaskControlSourceAtRoute(source *TaskControlSource, p *P, route RouteID) bool {
	if !taskControlSourceEmpty(source, nil) {
		return false
	}
	return bindRoutedProducerSource(&source.routedProducerSource, p, route)
}

// BindTaskControlSource is the explicit route-1 compatibility binding.
func BindTaskControlSource(source *TaskControlSource, p *P) bool {
	return BindTaskControlSourceAtRoute(source, p, RouteID(1))
}

func UnbindTaskControlSource(source *TaskControlSource, p *P) bool {
	if !taskControlSourceEmpty(source, p) {
		return false
	}
	return unbindRoutedProducerSource(&source.routedProducerSource, p)
}

func (source *TaskControlSource) CanRelease() bool {
	return taskControlSourceEmpty(source, nil)
}

func (source *TaskControlSource) Route() (RouteID, bool) {
	if source == nil {
		return 0, false
	}
	return routedProducerRoute(&source.routedProducerSource)
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
