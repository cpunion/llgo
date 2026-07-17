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

// OperationRouteEncodingCapacity is fixed by the 9-bit OperationID route
// field. OperationRouteRegistryCapacity is the first small/static runtime
// profile. A generated native profile may raise the latter as far as the
// former without changing the two-word producer ABI.
const (
	OperationRouteEncodingCapacity = 1<<operationRouteBits - 1
	OperationRouteRegistryCapacity = 8
)

type operationRouteLifecycle uint32

const (
	operationRouteUnused operationRouteLifecycle = iota
	operationRouteAllocated
	operationRouteActive
	operationRouteClosing
	operationRouteQuiesced
	operationRouteRetired
)

// operationRouteSlot is target-owned stable storage. Its atomic prefix is the
// only state consulted before a producer lease is acquired. The immutable
// pointer suffix is published before Active and is cleared only after the
// route producer admission has been sealed and strongly joined.
type operationRouteSlot struct {
	state    uint32
	route    uint32
	inflight uint32

	executorRegistry *ExecutorRegistry
	executor         ExecutorHandle
	manual           *ManualOperationSource
	control          *TaskControlSource
}

// OperationRouteRegistry maps the route bits carried by an OperationID to one
// immutable executor/source catalog. Allocate, Bind, BeginClose,
// ConfirmQuiesced, and Retire are serialized by the target owner. Post is
// producer-concurrent.
//
// A callback retains only OperationID's two uint32 words. Target glue reaches
// this registry through target-global/static storage; it does not retain a Go
// pointer, P, G, source pointer, executor driver, or coroutine handle.
//
// Routes are monotonically allocated and never reused. Retire clears the
// pointer suffix but leaves a permanent Retired tombstone, so an old two-word
// callback can never address a later executor even after source generations
// coincide.
type OperationRouteRegistry struct {
	next  uint32
	slots [OperationRouteRegistryCapacity]operationRouteSlot
}

func operationRouteSlotFor(registry *OperationRouteRegistry, route RouteID) (*operationRouteSlot, bool) {
	if registry == nil || !route.Valid() || uint32(route) > OperationRouteRegistryCapacity {
		return nil, false
	}
	slot := &registry.slots[uint32(route)-1]
	return slot, preemptLoad(&slot.route) == uint32(route)
}

func operationRouteAcquireProducer(slot *operationRouteSlot) bool {
	return slot != nil && producerAdmissionAcquire(&slot.inflight)
}

func operationRouteReleaseProducer(slot *operationRouteSlot) {
	producerAdmissionRelease(&slot.inflight)
}

func operationRouteSealProducers(slot *operationRouteSlot) bool {
	return slot != nil && producerAdmissionSeal(&slot.inflight)
}

func operationRouteProducersQuiesced(slot *operationRouteSlot) bool {
	return slot != nil && producerAdmissionQuiesced(&slot.inflight)
}

func validOperationRouteBinding(slot *operationRouteSlot, route RouteID) bool {
	if slot == nil || !route.Valid() {
		return false
	}
	unbound := slot.executorRegistry == nil && slot.executor == (ExecutorHandle{}) && slot.manual == nil && slot.control == nil
	if unbound {
		return true
	}
	gateSlot, executorOK := executorSlot(slot.executorRegistry, slot.executor)
	if !executorOK || preemptLoad(&gateSlot.generation) != slot.executor.Generation ||
		preemptLoad(&gateSlot.state) != uint32(executorActive) {
		return false
	}
	gate := preemptLoad(&gateSlot.gate)
	if gate&^executorGateMask != 0 || gate&executorGateClosed != 0 ||
		preemptLoad(&gateSlot.inflight)&producerAdmissionClosed != 0 {
		return false
	}
	return (slot.manual != nil || slot.control != nil) &&
		(slot.manual == nil || slot.manual.route == route) &&
		(slot.control == nil || slot.control.route == route)
}

// Allocate reserves the next profile route. Exhaustion fails closed. Neither
// an unbound allocation nor a retired route is ever reconsidered by a later
// call; Abort is represented by closing and retiring the tombstone.
func (registry *OperationRouteRegistry) Allocate() (RouteID, bool) {
	if registry == nil || registry.next >= OperationRouteRegistryCapacity || registry.next >= OperationRouteEncodingCapacity {
		return 0, false
	}
	index := registry.next
	slot := &registry.slots[index]
	if preemptLoad(&slot.state) != uint32(operationRouteUnused) || preemptLoad(&slot.route) != 0 ||
		preemptLoad(&slot.inflight) != 0 || slot.executorRegistry != nil || slot.executor != (ExecutorHandle{}) ||
		slot.manual != nil || slot.control != nil {
		return 0, false
	}
	route := RouteID(index + 1)
	if !route.Valid() {
		return 0, false
	}
	registry.next++
	preemptStore(&slot.route, uint32(route))
	preemptStore(&slot.inflight, producerAdmissionClosed)
	preemptStore(&slot.state, uint32(operationRouteAllocated))
	return route, true
}

// Bind publishes one already-bound driver's Manual/Control catalog at its
// exact route. Legacy Wait/Timer V1 handles are deliberately absent: their
// platform ABI and registration tables remain unchanged in this slice.
func (registry *OperationRouteRegistry) Bind(route RouteID, driver *ExecutorDriver) bool {
	slot, ok := operationRouteSlotFor(registry, route)
	if !ok || preemptLoad(&slot.state) != uint32(operationRouteAllocated) ||
		!operationRouteProducersQuiesced(slot) || !validExecutorDriver(driver) || driver.route != route ||
		driver.sources.route != route || driver.registry == nil || !activeExecutorHandle(driver.registry, driver.handle) ||
		(driver.sources.manual == nil && driver.sources.control == nil) ||
		driver.sources.manual != nil && driver.sources.manual.route != route ||
		driver.sources.control != nil && driver.sources.control.route != route {
		return false
	}
	slot.executorRegistry = driver.registry
	slot.executor = driver.handle
	slot.manual = driver.sources.manual
	slot.control = driver.sources.control
	if !producerAdmissionReopen(&slot.inflight) {
		slot.executorRegistry = nil
		slot.executor = ExecutorHandle{}
		slot.manual = nil
		slot.control = nil
		return false
	}
	preemptStore(&slot.state, uint32(operationRouteActive))
	return true
}

// BeginClose withdraws producer admission. It accepts an allocated-but-unbound
// route so failed setup can still leave the required permanent tombstone.
func (registry *OperationRouteRegistry) BeginClose(route RouteID) bool {
	slot, ok := operationRouteSlotFor(registry, route)
	if !ok {
		return false
	}
	for {
		switch state := operationRouteLifecycle(preemptLoad(&slot.state)); state {
		case operationRouteAllocated:
			if !operationRouteProducersQuiesced(slot) {
				return false
			}
			if !preemptCompareAndSwap(&slot.state, uint32(state), uint32(operationRouteClosing)) {
				continue
			}
			return true
		case operationRouteActive:
			if !preemptCompareAndSwap(&slot.state, uint32(state), uint32(operationRouteClosing)) {
				continue
			}
			return operationRouteSealProducers(slot)
		default:
			return false
		}
	}
}

// ConfirmQuiesced is the route-ingress strong-join boundary. Source shutdown
// may begin only after this succeeds: an admitted route callback may still be
// inside ManualOperationSource.Post or TaskControlSource.Post until then.
func (registry *OperationRouteRegistry) ConfirmQuiesced(route RouteID) bool {
	slot, ok := operationRouteSlotFor(registry, route)
	return ok && preemptLoad(&slot.state) == uint32(operationRouteClosing) &&
		operationRouteProducersQuiesced(slot) &&
		preemptCompareAndSwap(&slot.state, uint32(operationRouteClosing), uint32(operationRouteQuiesced))
}

// Retire clears all Go pointers after the strong join and leaves the route ID
// plus Retired state forever. It does not close source slots or the executor;
// their existing owner protocols run after ingress withdrawal.
func (registry *OperationRouteRegistry) Retire(route RouteID) bool {
	slot, ok := operationRouteSlotFor(registry, route)
	if !ok || preemptLoad(&slot.state) != uint32(operationRouteQuiesced) ||
		!operationRouteProducersQuiesced(slot) || !validOperationRouteBinding(slot, route) {
		return false
	}
	slot.executorRegistry = nil
	slot.executor = ExecutorHandle{}
	slot.manual = nil
	slot.control = nil
	preemptStore(&slot.state, uint32(operationRouteRetired))
	return true
}

// AllRetired reports that every allocated route has completed its strong join,
// cleared its live pointer suffix, and reached Retired. It is a diagnostic and
// shutdown invariant only: it never authorizes releasing, zeroing, or reusing
// the registry storage. Retired route tombstones remain target-global for the
// process lifetime.
func (registry *OperationRouteRegistry) AllRetired() bool {
	if registry == nil || registry.next > OperationRouteRegistryCapacity {
		return false
	}
	for index := range registry.slots {
		slot := &registry.slots[index]
		if uint32(index) < registry.next {
			if preemptLoad(&slot.route) != uint32(index+1) ||
				preemptLoad(&slot.state) != uint32(operationRouteRetired) ||
				!operationRouteProducersQuiesced(slot) || slot.executorRegistry != nil ||
				slot.executor != (ExecutorHandle{}) || slot.manual != nil || slot.control != nil {
				return false
			}
			continue
		}
		if preemptLoad(&slot.route) != 0 || preemptLoad(&slot.state) != uint32(operationRouteUnused) ||
			preemptLoad(&slot.inflight) != 0 || slot.executorRegistry != nil ||
			slot.executor != (ExecutorHandle{}) || slot.manual != nil || slot.control != nil {
			return false
		}
	}
	return true
}

type OperationRoutePostResult uint8

const (
	OperationRoutePostInvalid OperationRoutePostResult = iota
	OperationRoutePosted
	OperationRoutePostCoalesced
	OperationRoutePostSourceClosed
	OperationRoutePostSourceStale
	OperationRoutePostClosed
	OperationRoutePostStale
)

type OperationRouteIngressResult struct {
	Route    OperationRoutePostResult
	Executor ExecutorRequestResult
}

func mapManualOperationRouteResult(result ManualOperationPostResult) OperationRoutePostResult {
	switch result {
	case ManualOperationPosted:
		return OperationRoutePosted
	case ManualOperationPostDuplicate:
		return OperationRoutePostCoalesced
	case ManualOperationPostClosed:
		return OperationRoutePostSourceClosed
	case ManualOperationPostStale:
		return OperationRoutePostSourceStale
	default:
		return OperationRoutePostInvalid
	}
}

func mapTaskControlRouteResult(result TaskControlPostResult) OperationRoutePostResult {
	switch result {
	case TaskControlPosted:
		return OperationRoutePosted
	case TaskControlCoalesced:
		return OperationRoutePostCoalesced
	case TaskControlPostClosed:
		return OperationRoutePostSourceClosed
	case TaskControlPostStale:
		return OperationRoutePostSourceStale
	default:
		return OperationRoutePostInvalid
	}
}

// PostAndRequest is the minimal fake target ingress. The caller supplies only
// the two-word ID plus a scalar control kind. Manual uses TaskCancelNone;
// Control requires Abort or Shutdown. The source switch is static and the
// durable source fact is always published before the correct executor gate is
// requested. A real target rings its retained doorbell only when Executor says
// ExecutorRequestIdleWake.
func (registry *OperationRouteRegistry) PostAndRequest(id OperationID, control TaskCancelKind) OperationRouteIngressResult {
	result := OperationRouteIngressResult{Route: OperationRoutePostInvalid, Executor: ExecutorRequestInvalid}
	if !id.Valid() || id.Source() != OperationSourceManual && id.Source() != OperationSourceControl ||
		id.Source() == OperationSourceManual && control != TaskCancelNone ||
		id.Source() == OperationSourceControl && !validTaskCancelKind(control) {
		return result
	}
	slot, ok := operationRouteSlotFor(registry, id.Route())
	if !ok {
		result.Route = OperationRoutePostStale
		return result
	}
	if !operationRouteAcquireProducer(slot) {
		state := operationRouteLifecycle(preemptLoad(&slot.state))
		if state == operationRouteClosing || state == operationRouteQuiesced || state == operationRouteRetired {
			result.Route = OperationRoutePostClosed
		} else {
			result.Route = OperationRoutePostStale
		}
		return result
	}
	if preemptLoad(&slot.state) != uint32(operationRouteActive) || preemptLoad(&slot.route) != uint32(id.Route()) {
		operationRouteReleaseProducer(slot)
		result.Route = OperationRoutePostClosed
		return result
	}
	switch id.Source() {
	case OperationSourceManual:
		if slot.manual == nil {
			result.Route = OperationRoutePostInvalid
		} else {
			result.Route = mapManualOperationRouteResult(slot.manual.Post(id))
		}
	case OperationSourceControl:
		if slot.control == nil {
			result.Route = OperationRoutePostInvalid
		} else {
			result.Route = mapTaskControlRouteResult(slot.control.Post(id, control))
		}
	}
	if result.Route == OperationRoutePosted && slot.executorRegistry != nil {
		result.Executor = slot.executorRegistry.Request(slot.executor)
	}
	operationRouteReleaseProducer(slot)
	return result
}

func (registry *OperationRouteRegistry) PostManualAndRequest(id OperationID) OperationRouteIngressResult {
	return registry.PostAndRequest(id, TaskCancelNone)
}

func (registry *OperationRouteRegistry) PostTaskControlAndRequest(id OperationID, kind TaskCancelKind) OperationRouteIngressResult {
	return registry.PostAndRequest(id, kind)
}
