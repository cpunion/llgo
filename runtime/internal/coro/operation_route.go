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
	timers           *TimerRegistrationTable
	poll             *PollOperationSource
	manual           *ManualOperationSource
	worker           *WorkerOperationSource
	channel          *ChannelOperationSource
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
	unbound := slot.executorRegistry == nil && slot.executor == (ExecutorHandle{}) && slot.timers == nil &&
		slot.poll == nil && slot.manual == nil && slot.worker == nil && slot.channel == nil && slot.control == nil
	if unbound {
		return true
	}
	gateSlot, executorOK := executorSlot(slot.executorRegistry, slot.executor)
	if !executorOK || preemptLoad(&gateSlot.generation) != slot.executor.Generation {
		return false
	}
	gate := preemptLoad(&gateSlot.gate)
	inflight := preemptLoad(&gateSlot.inflight)
	switch executorLifecycle(preemptLoad(&gateSlot.state)) {
	case executorActive:
		if gate&^executorGateMask != 0 || gate&executorGateClosed != 0 ||
			inflight&producerAdmissionClosed != 0 {
			return false
		}
	case executorClosing:
		// An adopted program driver may have sealed its authoritative request
		// gate before the fleet coordinator withdraws the additional route
		// ingress. Route retirement needs only immutable binding identity and its
		// own producer strong join; later target/program close joins and retires
		// the executor producer domain itself.
		if gate != executorGateClosed || inflight&producerAdmissionClosed == 0 {
			return false
		}
	default:
		return false
	}
	return (slot.timers != nil || slot.poll != nil || slot.manual != nil || slot.worker != nil ||
		slot.channel != nil || slot.control != nil) &&
		(slot.timers == nil || slot.timers.route == route) &&
		(slot.poll == nil || slot.poll.route == route) &&
		(slot.manual == nil || slot.manual.route == route) &&
		(slot.worker == nil || slot.worker.route == route) &&
		(slot.channel == nil || slot.channel.route == route) &&
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
		slot.timers != nil || slot.poll != nil || slot.manual != nil || slot.worker != nil ||
		slot.channel != nil || slot.control != nil {
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

// Bind publishes one already-bound driver's routed source catalog at its exact
// route. Timer is retained for exact catalog identity but has no producer
// callback: its owner discovers expiry from the monotonic clock. Poll V2 is a
// producer source and is dispatched by PostPollAndRequest below. Legacy Wait
// handles remain outside this registry and keep their existing callback ABI.
func (registry *OperationRouteRegistry) Bind(route RouteID, driver *ExecutorDriver) bool {
	slot, ok := operationRouteSlotFor(registry, route)
	if !ok || preemptLoad(&slot.state) != uint32(operationRouteAllocated) ||
		!operationRouteProducersQuiesced(slot) || !validExecutorDriver(driver) || driver.route != route ||
		driver.sources.route != route || driver.registry == nil || !activeExecutorHandle(driver.registry, driver.handle) ||
		(driver.sources.timers == nil && driver.sources.poll == nil && driver.sources.manual == nil &&
			driver.sources.worker == nil && driver.sources.channel == nil && driver.sources.control == nil) ||
		driver.sources.timers != nil && driver.sources.timers.route != route ||
		driver.sources.poll != nil && driver.sources.poll.route != route ||
		driver.sources.manual != nil && driver.sources.manual.route != route ||
		driver.sources.worker != nil && driver.sources.worker.route != route ||
		driver.sources.channel != nil && driver.sources.channel.route != route ||
		driver.sources.control != nil && driver.sources.control.route != route {
		return false
	}
	slot.executorRegistry = driver.registry
	slot.executor = driver.handle
	slot.timers = driver.sources.timers
	slot.poll = driver.sources.poll
	slot.manual = driver.sources.manual
	slot.worker = driver.sources.worker
	slot.channel = driver.sources.channel
	slot.control = driver.sources.control
	if !producerAdmissionReopen(&slot.inflight) {
		slot.executorRegistry = nil
		slot.executor = ExecutorHandle{}
		slot.timers = nil
		slot.poll = nil
		slot.manual = nil
		slot.worker = nil
		slot.channel = nil
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
// inside ManualOperationSource.Post, WorkerOperationSource.Post, or
// TaskControlSource.Post until then.
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
	slot.timers = nil
	slot.poll = nil
	slot.manual = nil
	slot.worker = nil
	slot.channel = nil
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
				slot.executor != (ExecutorHandle{}) || slot.timers != nil || slot.poll != nil ||
				slot.manual != nil || slot.worker != nil || slot.channel != nil || slot.control != nil {
				return false
			}
			continue
		}
		if preemptLoad(&slot.route) != 0 || preemptLoad(&slot.state) != uint32(operationRouteUnused) ||
			preemptLoad(&slot.inflight) != 0 || slot.executorRegistry != nil ||
			slot.executor != (ExecutorHandle{}) || slot.timers != nil || slot.poll != nil ||
			slot.manual != nil || slot.worker != nil || slot.channel != nil || slot.control != nil {
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

func mapWorkerOperationRouteResult(result WorkerOperationPostResult) OperationRoutePostResult {
	switch result {
	case WorkerOperationPosted:
		return OperationRoutePosted
	case WorkerOperationPostDuplicate:
		return OperationRoutePostCoalesced
	case WorkerOperationPostClosed:
		return OperationRoutePostSourceClosed
	case WorkerOperationPostStale:
		return OperationRoutePostSourceStale
	default:
		return OperationRoutePostInvalid
	}
}

func mapPollOperationRouteResult(result PollOperationPostResult) OperationRoutePostResult {
	switch result {
	case PollOperationPosted:
		return OperationRoutePosted
	case PollOperationPostDuplicate:
		return OperationRoutePostCoalesced
	case PollOperationPostClosed:
		return OperationRoutePostSourceClosed
	case PollOperationPostStale:
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
// ExecutorRequestIdleWake. Channel is intentionally absent: its hchan shim must
// hold both endpoint admissions, publish physical/result and exact source
// mailboxes, publish both claims Claimed, release both lifetime admissions,
// and only then request their executors. A generic one-ID route call cannot
// preserve that rendezvous order.
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

// PostWorkerAndRequest routes one pointer-free worker completion to the exact
// WorkerOperationSource and executor encoded by id.Route. The route admission
// remains held across both the source's exact-generation scalar mailbox Post
// and ExecutorRegistry.Request. WorkerOperationSource.Post retains its own
// producer admission, so route retirement and source-slot retirement remain
// separate strong-join boundaries.
func (registry *OperationRouteRegistry) PostWorkerAndRequest(
	id OperationID,
	payload ScalarResultPayloadV1,
) OperationRouteIngressResult {
	result := OperationRouteIngressResult{Route: OperationRoutePostInvalid, Executor: ExecutorRequestInvalid}
	if !id.Valid() || id.Source() != OperationSourceWorker || !payload.Valid() {
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
	if slot.worker == nil {
		result.Route = OperationRoutePostInvalid
	} else {
		result.Route = mapWorkerOperationRouteResult(slot.worker.Post(id, payload))
	}
	if result.Route == OperationRoutePosted && slot.executorRegistry != nil {
		result.Executor = slot.executorRegistry.Request(slot.executor)
	}
	operationRouteReleaseProducer(slot)
	return result
}

// PostPollAndRequest routes one pointer-free readiness result to the exact
// PollOperationSource and executor encoded by id.Route. The route producer
// lease covers both durable source publication and ExecutorRegistry.Request,
// matching the Manual/Worker ordering and making route close a strong join of
// the complete callback tail.
func (registry *OperationRouteRegistry) PostPollAndRequest(
	id OperationID,
	result PollOperationResult,
) OperationRouteIngressResult {
	post := OperationRouteIngressResult{Route: OperationRoutePostInvalid, Executor: ExecutorRequestInvalid}
	if !id.Valid() || id.Source() != OperationSourcePoll ||
		(result != PollOperationReady && result != PollOperationClosing) {
		return post
	}
	slot, ok := operationRouteSlotFor(registry, id.Route())
	if !ok {
		post.Route = OperationRoutePostStale
		return post
	}
	if !operationRouteAcquireProducer(slot) {
		state := operationRouteLifecycle(preemptLoad(&slot.state))
		if state == operationRouteClosing || state == operationRouteQuiesced || state == operationRouteRetired {
			post.Route = OperationRoutePostClosed
		} else {
			post.Route = OperationRoutePostStale
		}
		return post
	}
	if preemptLoad(&slot.state) != uint32(operationRouteActive) ||
		preemptLoad(&slot.route) != uint32(id.Route()) {
		operationRouteReleaseProducer(slot)
		post.Route = OperationRoutePostClosed
		return post
	}
	if slot.poll == nil {
		post.Route = OperationRoutePostInvalid
	} else {
		post.Route = mapPollOperationRouteResult(slot.poll.PostPollOperationV2(id, result))
	}
	if post.Route == OperationRoutePosted && slot.executorRegistry != nil {
		post.Executor = slot.executorRegistry.Request(slot.executor)
	}
	operationRouteReleaseProducer(slot)
	return post
}

func (registry *OperationRouteRegistry) PostTaskControlAndRequest(id OperationID, kind TaskCancelKind) OperationRouteIngressResult {
	return registry.PostAndRequest(id, kind)
}

// RequestTimerExecutor routes the wake half of an atomically published
// controlled-timer generation change. The control word is the durable fact;
// this method only protects the route lookup through ExecutorRegistry.Request.
// The owner later observes the mismatch while scanning its timer catalog and
// performs ordinary ParkSet cancellation.
func (registry *OperationRouteRegistry) RequestTimerExecutor(route RouteID) ExecutorRequestResult {
	if !route.Valid() {
		return ExecutorRequestInvalid
	}
	slot, ok := operationRouteSlotFor(registry, route)
	if !ok {
		return ExecutorRequestStale
	}
	if !operationRouteAcquireProducer(slot) {
		state := operationRouteLifecycle(preemptLoad(&slot.state))
		if state == operationRouteClosing || state == operationRouteQuiesced || state == operationRouteRetired {
			return ExecutorRequestClosed
		}
		return ExecutorRequestStale
	}
	result := ExecutorRequestInvalid
	if preemptLoad(&slot.state) == uint32(operationRouteActive) &&
		preemptLoad(&slot.route) == uint32(route) && slot.timers != nil &&
		slot.executorRegistry != nil {
		result = slot.executorRegistry.Request(slot.executor)
	}
	operationRouteReleaseProducer(slot)
	return result
}

// RequestChannelExecutor requests the exact executor after the typed hchan
// adapter has durably committed one Channel endpoint. Unlike PostAndRequest,
// this method does not publish a source fact: the adapter's external commit
// transaction already did so while holding the hchan lock and endpoint
// admission. The route lease protects source/executor pointer lookup through
// the request tail and makes close a strong join of that routing operation.
func (registry *OperationRouteRegistry) RequestChannelExecutor(id OperationID) ExecutorRequestResult {
	if !id.Valid() || id.Source() != OperationSourceChannel {
		return ExecutorRequestInvalid
	}
	slot, ok := operationRouteSlotFor(registry, id.Route())
	if !ok {
		return ExecutorRequestStale
	}
	if !operationRouteAcquireProducer(slot) {
		state := operationRouteLifecycle(preemptLoad(&slot.state))
		if state == operationRouteClosing || state == operationRouteQuiesced || state == operationRouteRetired {
			return ExecutorRequestClosed
		}
		return ExecutorRequestStale
	}
	result := ExecutorRequestInvalid
	if preemptLoad(&slot.state) == uint32(operationRouteActive) &&
		preemptLoad(&slot.route) == uint32(id.Route()) && slot.channel != nil &&
		slot.executorRegistry != nil {
		result = slot.executorRegistry.Request(slot.executor)
	}
	operationRouteReleaseProducer(slot)
	return result
}
