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

// ExecutorFleetCapacity is the first allocation-free target profile. Route
// identities are never reused, so this is also the maximum number of executor
// bindings over one fleet's lifetime, not merely its simultaneously-live
// count.
const ExecutorFleetCapacity = OperationRouteRegistryCapacity

// Keep the first profile representable by both fixed registries.
var _ [ExecutorRequestCapacity - ExecutorFleetCapacity]struct{}

// ExecutorFleetHandle is the target-owner identity of one exact route and
// executor generation. It is deliberately three uint32 words: target glue may
// retain it, but producers normally retain only the smaller OperationID issued
// by a source on this route.
type ExecutorFleetHandle struct {
	Route    uint32
	Executor ExecutorHandle
}

var (
	_ [12 - unsafe.Sizeof(ExecutorFleetHandle{})]byte
	_ [unsafe.Sizeof(ExecutorFleetHandle{}) - 12]byte
	_ [4 - unsafe.Alignof(ExecutorFleetHandle{})]byte
	_ [unsafe.Alignof(ExecutorFleetHandle{}) - 4]byte
)

func (handle ExecutorFleetHandle) Valid() bool {
	route := RouteID(handle.Route)
	return handle.Route == uint32(route) && route.Valid() &&
		handle.Executor.Slot != 0 && handle.Executor.Slot <= ExecutorRequestCapacity &&
		handle.Executor.Generation != 0
}

func (handle ExecutorFleetHandle) RouteID() (RouteID, bool) {
	if !handle.Valid() {
		return 0, false
	}
	return RouteID(handle.Route), true
}

type executorFleetSlotState uint32

const (
	executorFleetSlotUnused executorFleetSlotState = iota
	executorFleetSlotBinding
	executorFleetSlotActive
	executorFleetSlotRouteClosing
	executorFleetSlotRouteRetired
	executorFleetSlotExecutorClosing
	executorFleetSlotRetired
	executorFleetSlotPoisoned
)

type executorFleetSlot struct {
	// state is the only producer-observed fleet word. The remaining suffix is
	// immutable while Active and is cleared only after the route admission
	// strong join.
	state uint32

	handle  ExecutorFleetHandle
	p       *P
	driver  *ExecutorDriver
	mailbox RunnableTransferMailbox
}

const executorFleetMagic uint32 = 0x45584631 // "EXF1"

// ExecutorFleet is the allocation-free target owner for multiple independent
// P/driver/source islands. Fleet-owned domains use the embedded
// ExecutorRegistry; an adopted program-main domain retains its existing
// registry. One monotonically allocated OperationRouteRegistry maps every
// producer OperationID to the matching source catalog and exact request gate.
//
// The fleet and all P/driver/catalog storage must remain at stable addresses
// from first BindExecutorFleet through AllRetired. Owner lifecycle methods are
// serialized. Producer-concurrent methods are the routed Post methods and
// PublishPNeutralRunnableAndRequest; none retains or accepts a P/driver/source
// pointer in its durable result.
type ExecutorFleet struct {
	magic     uint32
	executors ExecutorRegistry
	routes    OperationRouteRegistry
	slots     [ExecutorFleetCapacity]executorFleetSlot
}

func pristineExecutorFleet(fleet *ExecutorFleet) bool {
	if fleet == nil || fleet.magic != 0 || fleet.routes.next != 0 || !fleet.executors.CanRelease() {
		return false
	}
	for index := range fleet.routes.slots {
		if fleet.routes.slots[index] != (operationRouteSlot{}) || fleet.slots[index] != (executorFleetSlot{}) {
			return false
		}
	}
	return true
}

func executorFleetReady(fleet *ExecutorFleet) bool {
	return fleet != nil && (fleet.magic == executorFleetMagic || pristineExecutorFleet(fleet))
}

func nextExecutorFleetRoute(fleet *ExecutorFleet) (RouteID, *executorFleetSlot, bool) {
	if !executorFleetReady(fleet) || fleet.routes.next >= ExecutorFleetCapacity {
		return 0, nil, false
	}
	index := fleet.routes.next
	routeSlot := &fleet.routes.slots[index]
	fleetSlot := &fleet.slots[index]
	if *routeSlot != (operationRouteSlot{}) || *fleetSlot != (executorFleetSlot{}) {
		return 0, nil, false
	}
	route := RouteID(index + 1)
	return route, fleetSlot, route.Valid()
}

func executorRegistryCanRegister(registry *ExecutorRegistry) bool {
	if registry == nil {
		return false
	}
	for index := range registry.slots {
		slot := &registry.slots[index]
		if preemptLoad(&slot.state) == uint32(executorFree) &&
			preemptLoad(&slot.generation) != ^uint32(0) &&
			executorFreeSlotReusable(preemptLoad(&slot.generation), preemptLoad(&slot.inflight), preemptLoad(&slot.gate)) {
			return true
		}
	}
	return false
}

func executorFleetDriverCandidate(driver *ExecutorDriver, p *P) bool {
	return driver != nil && *driver == (ExecutorDriver{}) && p != nil && p.executor == nil &&
		preemptLoad(&p.executorMode) == executorModeUnbound && preemptLoad(&p.schedule) == scheduleIdle &&
		idleExecutorScheduler(p) && p.readyHead == nil && p.readyTail == nil &&
		emptySchedulerWaitQueues(p) && p.channelSource == nil
}

func executorFleetCatalogCandidate(catalog ExecutorSourceCatalog, p *P, route RouteID) bool {
	if p == nil || !route.Valid() {
		return false
	}
	if catalog.Timers != nil && (!timerRegistrationTableEmpty(catalog.Timers, nil) ||
		catalog.Timers.route != 0 && catalog.Timers.route != route) {
		return false
	}
	if catalog.Poll != nil && (!pollOperationSourceEmpty(catalog.Poll, nil) ||
		catalog.Poll.route != 0 && catalog.Poll.route != route) {
		return false
	}
	if catalog.Manual != nil && (!manualOperationSourceEmpty(catalog.Manual, nil) ||
		catalog.Manual.route != 0 && catalog.Manual.route != route) {
		return false
	}
	if catalog.Worker != nil && (!workerOperationSourceEmpty(catalog.Worker, nil) ||
		catalog.Worker.route != 0 && catalog.Worker.route != route) {
		return false
	}
	if catalog.Channel != nil && (!channelOperationSourceEmpty(catalog.Channel, nil) ||
		catalog.Channel.route != 0 && catalog.Channel.route != route) {
		return false
	}
	if catalog.Control != nil && (!taskControlSourceEmpty(catalog.Control, nil) ||
		catalog.Control.route != 0 && catalog.Control.route != route) {
		return false
	}
	// OperationRouteRegistry publishes pointer-free V2 producer sources and
	// retains owner-driven Timer identity. Waits is mandatory driver
	// infrastructure but does not by itself provide a routed source catalog.
	return catalog.Timers != nil || catalog.Poll != nil || catalog.Manual != nil ||
		catalog.Worker != nil || catalog.Channel != nil || catalog.Control != nil
}

func executorFleetSlotFor(fleet *ExecutorFleet, handle ExecutorFleetHandle) (*executorFleetSlot, RouteID, bool) {
	if fleet == nil || fleet.magic != executorFleetMagic || !handle.Valid() || handle.Route > ExecutorFleetCapacity {
		return nil, 0, false
	}
	route := RouteID(handle.Route)
	slot := &fleet.slots[handle.Route-1]
	return slot, route, slot.handle == handle
}

func executorFleetRouteOwner(fleet *ExecutorFleet, handle ExecutorFleetHandle) (*executorFleetSlot, RouteID, bool) {
	slot, route, ok := executorFleetSlotFor(fleet, handle)
	routeSlot, routeOK := operationRouteSlotFor(&fleet.routes, route)
	return slot, route, ok && preemptLoad(&slot.state) == uint32(executorFleetSlotActive) &&
		slot.p != nil && slot.driver != nil && validExecutorDriver(slot.driver) && slot.driver.p == slot.p &&
		slot.driver.registry != nil && slot.driver.handle == handle.Executor && slot.driver.route == route && routeOK &&
		preemptLoad(&routeSlot.state) == uint32(operationRouteActive) &&
		RouteID(preemptLoad(&routeSlot.route)) == route && routeSlot.executorRegistry == slot.driver.registry &&
		routeSlot.executor == handle.Executor &&
		(routeSlot.timers != nil || routeSlot.poll != nil || routeSlot.manual != nil || routeSlot.worker != nil ||
			routeSlot.channel != nil || routeSlot.control != nil)
}

func retireAllocatedFleetRoute(routes *OperationRouteRegistry, route RouteID) bool {
	return routes.BeginClose(route) && routes.ConfirmQuiesced(route) && routes.Retire(route)
}

func retireAllocatedFleetExecutor(registry *ExecutorRegistry, handle ExecutorHandle) bool {
	return registry.BeginClose(handle) && registry.ConfirmQuiesced(handle) && registry.Retire(handle)
}

func resetEmptyFleetMailbox(mailbox *RunnableTransferMailbox, owner *P) bool {
	if mailbox == nil || owner == nil || mailbox.magic != runnableTransferMailboxMagic || mailbox.owner != owner ||
		!tryRunnableTransferGate(mailbox) {
		return false
	}
	valid := validRunnableTransferHeaderLocked(mailbox) && mailbox.count == 0
	if valid {
		for index := range mailbox.slots {
			slot := &mailbox.slots[index]
			if slot.state != runnableTransferSlotEmpty || slot.source != nil || slot.g != nil {
				valid = false
				break
			}
		}
	}
	if !valid {
		releaseRunnableTransferGate(mailbox)
		return false
	}
	// The enclosing route admission has already been strongly joined (or this
	// mailbox has not yet been published by Bind), so no caller can be paused
	// before this mailbox gate and later dereference the cleared owner.
	*mailbox = RunnableTransferMailbox{}
	return true
}

func poisonExecutorFleetSlot(slot *executorFleetSlot) {
	if slot != nil {
		preemptStore(&slot.state, uint32(executorFleetSlotPoisoned))
	}
}

// BindExecutorFleet transactionally consumes the next monotonic route,
// registers one exact executor generation, binds the P/driver/source catalog,
// and publishes the route. The complete source catalog is preflighted before
// route allocation; the underlying source-set bind additionally rolls back
// each earlier empty binding if a later bind unexpectedly fails.
func BindExecutorFleet(
	fleet *ExecutorFleet,
	driver *ExecutorDriver,
	p *P,
	catalog ExecutorSourceCatalog,
) (ExecutorFleetHandle, bool) {
	route, slot, ok := nextExecutorFleetRoute(fleet)
	if !ok || !executorRegistryCanRegister(&fleet.executors) ||
		!executorFleetDriverCandidate(driver, p) || !executorFleetCatalogCandidate(catalog, p, route) {
		return ExecutorFleetHandle{}, false
	}
	if fleet.magic == 0 {
		fleet.magic = executorFleetMagic
	}
	allocated, ok := fleet.routes.Allocate()
	if !ok || allocated != route {
		return ExecutorFleetHandle{}, false
	}
	slot.handle.Route = uint32(route)
	slot.p = p
	slot.driver = driver
	preemptStore(&slot.state, uint32(executorFleetSlotBinding))

	executor, ok := fleet.executors.Register()
	if !ok {
		if !retireAllocatedFleetRoute(&fleet.routes, route) {
			poisonExecutorFleetSlot(slot)
			return ExecutorFleetHandle{}, false
		}
		slot.p, slot.driver = nil, nil
		preemptStore(&slot.state, uint32(executorFleetSlotRetired))
		return ExecutorFleetHandle{}, false
	}
	slot.handle.Executor = executor
	if !BindExecutorSourceCatalogAtRoute(driver, p, &fleet.executors, executor, route, catalog) {
		if !retireAllocatedFleetRoute(&fleet.routes, route) || !retireAllocatedFleetExecutor(&fleet.executors, executor) {
			poisonExecutorFleetSlot(slot)
			return ExecutorFleetHandle{}, false
		}
		slot.p, slot.driver = nil, nil
		preemptStore(&slot.state, uint32(executorFleetSlotRetired))
		return ExecutorFleetHandle{}, false
	}
	if !BindRunnableTransferMailbox(&slot.mailbox, p) {
		if !retireAllocatedFleetRoute(&fleet.routes, route) || !BeginExecutorClose(driver) || !ConfirmExecutorClose(driver) {
			poisonExecutorFleetSlot(slot)
			return ExecutorFleetHandle{}, false
		}
		slot.p, slot.driver = nil, nil
		preemptStore(&slot.state, uint32(executorFleetSlotRetired))
		return ExecutorFleetHandle{}, false
	}
	if !fleet.routes.Bind(route, driver) {
		mailboxOK := resetEmptyFleetMailbox(&slot.mailbox, p)
		if !retireAllocatedFleetRoute(&fleet.routes, route) || !BeginExecutorClose(driver) ||
			!ConfirmExecutorClose(driver) || !mailboxOK {
			poisonExecutorFleetSlot(slot)
			return ExecutorFleetHandle{}, false
		}
		slot.p, slot.driver = nil, nil
		preemptStore(&slot.state, uint32(executorFleetSlotRetired))
		return ExecutorFleetHandle{}, false
	}
	preemptStore(&slot.state, uint32(executorFleetSlotActive))
	return slot.handle, true
}

// AdoptExecutorFleet publishes an already-bound route-matching executor as
// the next fleet domain. This is the program-main migration boundary: existing
// static P/driver/source storage and its executor registry remain authoritative
// while the fleet adds only exact route ingress and a P-neutral transfer
// mailbox. Later fleet-owned domains may still use BindExecutorFleet and its
// private registry. No driver/source pointer is copied or rebound.
func AdoptExecutorFleet(
	fleet *ExecutorFleet,
	driver *ExecutorDriver,
	p *P,
) (ExecutorFleetHandle, bool) {
	route, slot, ok := nextExecutorFleetRoute(fleet)
	if !ok || !validExecutorDriverForP(driver, p) || driver.registry == nil ||
		driver.route != route || driver.sources.route != route ||
		driver.poll.phase != executorPollIdle || !emptyExecutorRunCursor(driver) ||
		!idleExecutorScheduler(p) || !activeExecutorHandle(driver.registry, driver.handle) {
		return ExecutorFleetHandle{}, false
	}
	if fleet.magic == 0 {
		fleet.magic = executorFleetMagic
	}
	allocated, ok := fleet.routes.Allocate()
	if !ok || allocated != route {
		return ExecutorFleetHandle{}, false
	}
	slot.handle = ExecutorFleetHandle{Route: uint32(route), Executor: driver.handle}
	slot.p = p
	slot.driver = driver
	preemptStore(&slot.state, uint32(executorFleetSlotBinding))
	if !BindRunnableTransferMailbox(&slot.mailbox, p) {
		if !retireAllocatedFleetRoute(&fleet.routes, route) {
			poisonExecutorFleetSlot(slot)
			return ExecutorFleetHandle{}, false
		}
		slot.p, slot.driver = nil, nil
		preemptStore(&slot.state, uint32(executorFleetSlotRetired))
		return ExecutorFleetHandle{}, false
	}
	if !fleet.routes.Bind(route, driver) {
		mailboxOK := resetEmptyFleetMailbox(&slot.mailbox, p)
		if !retireAllocatedFleetRoute(&fleet.routes, route) || !mailboxOK {
			poisonExecutorFleetSlot(slot)
			return ExecutorFleetHandle{}, false
		}
		slot.p, slot.driver = nil, nil
		preemptStore(&slot.state, uint32(executorFleetSlotRetired))
		return ExecutorFleetHandle{}, false
	}
	preemptStore(&slot.state, uint32(executorFleetSlotActive))
	return slot.handle, true
}

// PublishPNeutralRunnableAndRequest moves one P-neutral runnable into the
// exact destination route's bounded mailbox, then requests only that route's
// executor. The route producer lease spans both steps, so route close strongly
// joins the publish-to-request tail. The returned ID is local to handle.
func (fleet *ExecutorFleet) PublishPNeutralRunnableAndRequest(
	handle ExecutorFleetHandle,
	source *P,
	g *G,
) (RunnableTransferID, ExecutorRequestResult, bool) {
	if fleet == nil || fleet.magic != executorFleetMagic || !handle.Valid() || handle.Route > ExecutorFleetCapacity {
		return RunnableTransferID{}, ExecutorRequestInvalid, false
	}
	route := RouteID(handle.Route)
	routeSlot, ok := operationRouteSlotFor(&fleet.routes, route)
	if !ok || !operationRouteAcquireProducer(routeSlot) {
		return RunnableTransferID{}, ExecutorRequestClosed, false
	}
	slot := &fleet.slots[handle.Route-1]
	if preemptLoad(&slot.state) != uint32(executorFleetSlotActive) || slot.handle != handle {
		operationRouteReleaseProducer(routeSlot)
		return RunnableTransferID{}, ExecutorRequestStale, false
	}
	id, published := PublishPNeutralRunnable(&slot.mailbox, source, g)
	request := ExecutorRequestInvalid
	if published && slot.driver != nil && slot.driver.registry != nil {
		request = slot.driver.registry.Request(handle.Executor)
	}
	operationRouteReleaseProducer(routeSlot)
	return id, request, published
}

// PublishInitialReadyHeadAndRequest opportunistically moves only the exact
// source ready-head when it is a never-run initial coroutine. It is called by
// a source-P owner after a physical resume has completely committed, never by
// a callback or from inside managed code. Ineligibility and mailbox contention
// are ordinary local-execution fallback and leave the source queue untouched.
func (fleet *ExecutorFleet) PublishInitialReadyHeadAndRequest(
	handle ExecutorFleetHandle,
	source *P,
) (RunnableTransferID, ExecutorRequestResult, bool) {
	if source == nil || !stableRunnableTransferP(source) || !validReadyQueue(source) ||
		source.readyHead == nil || !initialPNeutralRunnable(source.readyHead, true) {
		return RunnableTransferID{}, ExecutorRequestInvalid, false
	}
	return fleet.PublishPNeutralRunnableAndRequest(handle, source, source.readyHead)
}

// ImportPNeutralRunnable imports one exact FIFO transfer on its destination P.
// It is owner-serialized and accepted while Active or RouteClosing, allowing a
// sealed route to drain before its pointer suffix is retired.
func (fleet *ExecutorFleet) ImportPNeutralRunnable(handle ExecutorFleetHandle, owner *P, id RunnableTransferID) bool {
	slot, _, ok := executorFleetSlotFor(fleet, handle)
	if !ok || slot.p != owner {
		return false
	}
	state := executorFleetSlotState(preemptLoad(&slot.state))
	return (state == executorFleetSlotActive || state == executorFleetSlotRouteClosing) &&
		ImportPNeutralRunnable(&slot.mailbox, owner, id)
}

// DrainPNeutralRunnables imports at most one mailbox capacity on an exact
// destination route. See ImportPNeutralRunnable for the accepted close phase.
func (fleet *ExecutorFleet) DrainPNeutralRunnables(
	handle ExecutorFleetHandle,
	owner *P,
	budget uint32,
) (uint32, bool, bool) {
	moved, more, status := fleet.TryDrainPNeutralRunnables(handle, owner, budget)
	return moved, more, status == RunnableTransferDrainComplete
}

// TryDrainPNeutralRunnables preserves the mailbox Try-gate contention result
// for a physical scheduler owner. Route or ownership failures remain Invalid.
func (fleet *ExecutorFleet) TryDrainPNeutralRunnables(
	handle ExecutorFleetHandle,
	owner *P,
	budget uint32,
) (uint32, bool, RunnableTransferDrainStatus) {
	slot, _, ok := executorFleetSlotFor(fleet, handle)
	if !ok || slot.p != owner {
		return 0, false, RunnableTransferDrainInvalid
	}
	state := executorFleetSlotState(preemptLoad(&slot.state))
	if state != executorFleetSlotActive && state != executorFleetSlotRouteClosing {
		return 0, false, RunnableTransferDrainInvalid
	}
	return TryDrainPNeutralRunnables(&slot.mailbox, owner, budget)
}

func (fleet *ExecutorFleet) PostManualAndRequest(id OperationID) OperationRouteIngressResult {
	if fleet == nil || fleet.magic != executorFleetMagic {
		return OperationRouteIngressResult{Route: OperationRoutePostInvalid, Executor: ExecutorRequestInvalid}
	}
	return fleet.routes.PostManualAndRequest(id)
}

func (fleet *ExecutorFleet) PostWorkerAndRequest(id OperationID, payload ScalarResultPayloadV1) OperationRouteIngressResult {
	if fleet == nil || fleet.magic != executorFleetMagic {
		return OperationRouteIngressResult{Route: OperationRoutePostInvalid, Executor: ExecutorRequestInvalid}
	}
	return fleet.routes.PostWorkerAndRequest(id, payload)
}

func (fleet *ExecutorFleet) PostPollAndRequest(id OperationID, result PollOperationResult) OperationRouteIngressResult {
	if fleet == nil || fleet.magic != executorFleetMagic {
		return OperationRouteIngressResult{Route: OperationRoutePostInvalid, Executor: ExecutorRequestInvalid}
	}
	return fleet.routes.PostPollAndRequest(id, result)
}

func (fleet *ExecutorFleet) PostTaskControlAndRequest(id OperationID, kind TaskCancelKind) OperationRouteIngressResult {
	if fleet == nil || fleet.magic != executorFleetMagic {
		return OperationRouteIngressResult{Route: OperationRoutePostInvalid, Executor: ExecutorRequestInvalid}
	}
	return fleet.routes.PostTaskControlAndRequest(id, kind)
}

// RequestChannelExecutor routes the wake half of an already committed typed
// channel rendezvous. The Channel source fact is published by the hchan
// transaction before this call; this method only resolves and requests the
// executor encoded by the endpoint's OperationID route.
func (fleet *ExecutorFleet) RequestChannelExecutor(id OperationID) ExecutorRequestResult {
	if fleet == nil || fleet.magic != executorFleetMagic {
		return ExecutorRequestInvalid
	}
	return fleet.routes.RequestChannelExecutor(id)
}

// BeginExecutorFleetClose first seals the route ingress. Source and executor
// shutdown is intentionally unavailable until ConfirmExecutorFleetRouteClose
// has strongly joined every admitted completion/request/transfer producer.
func BeginExecutorFleetClose(fleet *ExecutorFleet, handle ExecutorFleetHandle) bool {
	slot, route, ok := executorFleetRouteOwner(fleet, handle)
	if !ok || !fleet.routes.BeginClose(route) {
		return false
	}
	preemptStore(&slot.state, uint32(executorFleetSlotRouteClosing))
	return true
}

// BeginExecutorFleetExternalDriverClose records that an adopted executor has
// already entered its command or terminal close state through the authoritative
// program driver. The route must still be strongly retired first; only the
// executor-gate transition itself is external. Fleet-owned domains continue to
// use BeginExecutorFleetDriverClose and cannot bypass its normal Begin call.
func BeginExecutorFleetExternalDriverClose(fleet *ExecutorFleet, handle ExecutorFleetHandle) bool {
	slot, _, ok := executorFleetSlotFor(fleet, handle)
	if !ok || preemptLoad(&slot.state) != uint32(executorFleetSlotRouteRetired) ||
		slot.driver == nil || slot.p == nil || !validExecutorDriver(slot.driver) ||
		slot.driver.p != slot.p ||
		(slot.driver.state != executorDriverClosing && slot.driver.state != executorDriverTerminalClosing) {
		return false
	}
	preemptStore(&slot.state, uint32(executorFleetSlotExecutorClosing))
	return true
}

func emptyExecutorFleetMailbox(mailbox *RunnableTransferMailbox, owner *P) bool {
	if mailbox == nil || owner == nil || mailbox.magic != runnableTransferMailboxMagic || mailbox.owner != owner ||
		!tryRunnableTransferGate(mailbox) {
		return false
	}
	empty := validRunnableTransferHeaderLocked(mailbox) && mailbox.count == 0
	if empty {
		for index := range mailbox.slots {
			slot := &mailbox.slots[index]
			if slot.state != runnableTransferSlotEmpty || slot.source != nil || slot.g != nil {
				empty = false
				break
			}
		}
	}
	releaseRunnableTransferGate(mailbox)
	return empty
}

// ConfirmExecutorFleetRouteClose is the route-ingress strong-join boundary.
// It refuses to retire the route pointer suffix while a transferred runnable
// still owns a mailbox slot. No source or driver lifecycle is mutated here.
func ConfirmExecutorFleetRouteClose(fleet *ExecutorFleet, handle ExecutorFleetHandle) bool {
	slot, route, ok := executorFleetSlotFor(fleet, handle)
	if !ok || preemptLoad(&slot.state) != uint32(executorFleetSlotRouteClosing) {
		return false
	}
	routeSlot, routeOK := operationRouteSlotFor(&fleet.routes, route)
	if !routeOK || !operationRouteProducersQuiesced(routeSlot) || !emptyExecutorFleetMailbox(&slot.mailbox, slot.p) ||
		!fleet.routes.ConfirmQuiesced(route) || !fleet.routes.Retire(route) {
		return false
	}
	preemptStore(&slot.state, uint32(executorFleetSlotRouteRetired))
	return true
}

// BeginExecutorFleetDriverClose seals the exact executor request gate only
// after route ingress is retired. The driver itself enforces that every source
// and scheduler queue is empty.
func BeginExecutorFleetDriverClose(fleet *ExecutorFleet, handle ExecutorFleetHandle) bool {
	slot, _, ok := executorFleetSlotFor(fleet, handle)
	if !ok || preemptLoad(&slot.state) != uint32(executorFleetSlotRouteRetired) ||
		slot.driver == nil || !BeginExecutorClose(slot.driver) {
		return false
	}
	preemptStore(&slot.state, uint32(executorFleetSlotExecutorClosing))
	return true
}

// ConfirmExecutorFleetClose consumes the target adapter's proof that all
// retained doorbell/backend calls for this executor have returned. It retires
// the executor generation, unbinds every source and P, and finally clears the
// empty fleet-owned transfer mailbox. The route/executor handle remains as a
// permanent ABA tombstone.
func ConfirmExecutorFleetClose(fleet *ExecutorFleet, handle ExecutorFleetHandle) bool {
	slot, _, ok := executorFleetSlotFor(fleet, handle)
	if !ok || preemptLoad(&slot.state) != uint32(executorFleetSlotExecutorClosing) ||
		slot.driver == nil || slot.p == nil || !emptyExecutorFleetMailbox(&slot.mailbox, slot.p) {
		return false
	}
	p, driver := slot.p, slot.driver
	if !ConfirmExecutorClose(driver) || !resetEmptyFleetMailbox(&slot.mailbox, p) {
		poisonExecutorFleetSlot(slot)
		return false
	}
	slot.p = nil
	slot.driver = nil
	preemptStore(&slot.state, uint32(executorFleetSlotRetired))
	return true
}

// ConfirmExecutorFleetExternalClose consumes the authoritative program
// driver's already-completed ConfirmExecutorClose or
// ConfirmTerminalExecutorClose. The driver and source catalog must be fully
// zero/unbound before this call; the fleet then releases only its empty
// transfer mailbox and pointer suffix, preserving the same route tombstone as
// an ordinary fleet-owned close.
func ConfirmExecutorFleetExternalClose(fleet *ExecutorFleet, handle ExecutorFleetHandle) bool {
	slot, _, ok := executorFleetSlotFor(fleet, handle)
	if !ok || preemptLoad(&slot.state) != uint32(executorFleetSlotExecutorClosing) ||
		slot.driver == nil || slot.p == nil || *slot.driver != (ExecutorDriver{}) ||
		slot.p.executor != nil || preemptLoad(&slot.p.executorMode) != executorModeUnbound ||
		!emptyExecutorFleetMailbox(&slot.mailbox, slot.p) {
		return false
	}
	p := slot.p
	if !resetEmptyFleetMailbox(&slot.mailbox, p) {
		poisonExecutorFleetSlot(slot)
		return false
	}
	slot.p = nil
	slot.driver = nil
	preemptStore(&slot.state, uint32(executorFleetSlotRetired))
	return true
}

// AllRetired reports that every consumed route is a pointer-free tombstone,
// every executor generation is retired, and no P/source/mailbox root remains.
// The fleet registry storage itself remains target-global because stale
// callbacks may still arrive and must observe Closed/Stale rather than freed
// memory.
func (fleet *ExecutorFleet) AllRetired() bool {
	if fleet == nil {
		return false
	}
	if fleet.magic == 0 {
		return pristineExecutorFleet(fleet)
	}
	if fleet.magic != executorFleetMagic || !fleet.routes.AllRetired() || !fleet.executors.CanRelease() {
		return false
	}
	for index := range fleet.slots {
		slot := &fleet.slots[index]
		if uint32(index) < fleet.routes.next {
			if preemptLoad(&slot.state) != uint32(executorFleetSlotRetired) || !slot.handle.Valid() ||
				slot.handle.Route != uint32(index+1) || slot.p != nil || slot.driver != nil ||
				slot.mailbox != (RunnableTransferMailbox{}) {
				return false
			}
			continue
		}
		if *slot != (executorFleetSlot{}) {
			return false
		}
	}
	return true
}
