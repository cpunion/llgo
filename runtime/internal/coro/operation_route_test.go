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

import (
	"runtime"
	"sync"
	"testing"
	"unsafe"
)

type routeManualFixture struct {
	p        *P
	driver   *ExecutorDriver
	registry *ExecutorRegistry
	manual   *ManualOperationSource
	handle   ExecutorHandle
	route    RouteID
}

type routeWorkerFixture struct {
	p        *P
	driver   *ExecutorDriver
	registry *ExecutorRegistry
	worker   *WorkerOperationSource
	handle   ExecutorHandle
	route    RouteID
}

func bindRouteManualFixture(t *testing.T, route RouteID) *routeManualFixture {
	t.Helper()
	fixture := &routeManualFixture{
		p:        new(P),
		driver:   new(ExecutorDriver),
		registry: new(ExecutorRegistry),
		manual:   new(ManualOperationSource),
		route:    route,
	}
	fixture.handle = registerTestExecutor(t, fixture.registry)
	if !BindExecutorSourceCatalogAtRoute(fixture.driver, fixture.p, fixture.registry, fixture.handle, route,
		ExecutorSourceCatalog{Manual: fixture.manual}) {
		t.Fatal("bind route-aware manual driver")
	}
	return fixture
}

func bindRouteWorkerFixture(
	t *testing.T,
	registry *ExecutorRegistry,
	route RouteID,
) *routeWorkerFixture {
	t.Helper()
	fixture := &routeWorkerFixture{
		p:        new(P),
		driver:   new(ExecutorDriver),
		registry: registry,
		worker:   new(WorkerOperationSource),
		route:    route,
	}
	fixture.handle = registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalogAtRoute(fixture.driver, fixture.p, registry, fixture.handle, route,
		ExecutorSourceCatalog{Worker: fixture.worker}) {
		t.Fatal("bind route-aware worker driver")
	}
	return fixture
}

func closeOperationRouteFixture(t *testing.T, routes *OperationRouteRegistry, route RouteID) {
	t.Helper()
	if !routes.BeginClose(route) || !routes.ConfirmQuiesced(route) || !routes.Retire(route) {
		t.Fatalf("close route %d", route)
	}
}

func settleRouteManualFixture(t *testing.T, fixture *routeManualFixture, state *ParkState, ticket ParkTicket, ids []OperationID) {
	t.Helper()
	if fixture.manual.Pending() {
		published, lost, ok := fixture.manual.PublishPass(fixture.p)
		if !ok || published != 1 || lost != 0 {
			t.Fatalf("publish routed manual = (%d, %d, %t)", published, lost, ok)
		}
		resolution, duplicates, ok := fixture.manual.ResolveAffectedPublishedEpoch(fixture.p)
		if !ok || resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}) || duplicates != 0 {
			t.Fatalf("resolve routed manual = (%+v, %d, %t)", resolution, duplicates, ok)
		}
	} else {
		if !RequestParkCancel(state, ticket, ParkCancelOperation) {
			t.Fatal("cancel unposted routed manual")
		}
		resolution, ok := ResolveParkSnapshot(state, ticket)
		if !ok || resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 1}) {
			t.Fatalf("resolve routed manual cancellation = (%+v, %t)", resolution, ok)
		}
	}
	if applied, detached, ok := fixture.manual.ApplyAndDetach(fixture.p); !ok || applied != 1 || detached != 1 {
		t.Fatalf("apply routed manual = (%d, %d, %t)", applied, detached, ok)
	}
	outcome, _, lease, consumed := ConsumeParkSet(state, ticket)
	if !consumed || outcome != ParkOutcomeCompleted && outcome != ParkOutcomeCanceled {
		t.Fatalf("consume routed manual = (%d, %+v, %t)", outcome, lease, consumed)
	}
	finishManualOperations(t, fixture.manual, fixture.p, ids, lease)
	if _, _, ok := PollExecutor(fixture.driver); !ok {
		t.Fatal("acknowledge routed executor request")
	}
	closeTestExecutorDriver(t, fixture.driver)
}

func settleRouteWorkerFixture(
	t *testing.T,
	fixture *routeWorkerFixture,
	state *ParkState,
	ticket ParkTicket,
	id OperationID,
	want ScalarResultPayloadV1,
) {
	t.Helper()
	if published, lost, ok := fixture.worker.PublishPass(fixture.p); !ok || published != 1 || lost != 0 {
		t.Fatalf("publish routed worker = (%d, %d, %t)", published, lost, ok)
	}
	wantResolution := CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}
	if resolution, duplicates, ok := fixture.worker.ResolveAffectedPublishedEpoch(fixture.p); !ok ||
		resolution != wantResolution || duplicates != 0 {
		t.Fatalf("resolve routed worker = (%+v, %d, %t), want %+v", resolution, duplicates, ok, wantResolution)
	}
	if applied, detached, ok := fixture.worker.ApplyAndDetach(fixture.p); !ok || applied != 1 || detached != 1 ||
		!ParkReady(state, ticket) {
		t.Fatalf("apply routed worker = (%d, %d, %t), ready=%t", applied, detached, ok, ParkReady(state, ticket))
	}
	outcome, caseID, lease, consumed := ConsumeParkSet(state, ticket)
	leaseID, leaseOK := lease.ID()
	if !consumed || outcome != ParkOutcomeCompleted || caseID != 1 || !leaseOK || leaseID != id {
		t.Fatalf("consume routed worker = (%d, %d, %+v, %t)", outcome, caseID, lease, consumed)
	}
	finishWorkerOperations(t, fixture.worker, fixture.p, []OperationID{id}, lease, want)
	if _, _, ok := PollExecutor(fixture.driver); !ok {
		t.Fatal("acknowledge routed worker executor request")
	}
	closeTestExecutorDriver(t, fixture.driver)
}

func TestOperationIDRouteCodecIsFrozenTwoWordPOD(t *testing.T) {
	if unsafe.Sizeof(OperationID{}) != 8 || unsafe.Alignof(OperationID{}) != 4 ||
		unsafe.Offsetof(OperationID{}.Generation) != 4 {
		t.Fatalf("OperationID layout = size %d align %d generation %d", unsafe.Sizeof(OperationID{}), unsafe.Alignof(OperationID{}), unsafe.Offsetof(OperationID{}.Generation))
	}
	if OperationRouteEncodingCapacity != 511 || operationLocalMask != 32767 {
		t.Fatalf("route codec bounds = (%d, %d)", OperationRouteEncodingCapacity, operationLocalMask)
	}
	id, ok := MakeOperationIDAtRoute(OperationSourceIRQ, RouteID(OperationRouteEncodingCapacity), operationLocalMask, 17)
	wantWord0 := uint32(OperationSourceIRQ)<<24 | uint32(OperationRouteEncodingCapacity)<<15 | operationLocalMask
	if !ok || !id.Valid() || id.SourceSlot != wantWord0 || id.Source() != OperationSourceIRQ ||
		id.Route() != RouteID(OperationRouteEncodingCapacity) || id.LocalSlot() != operationLocalMask ||
		id.Slot() != operationLocalMask || id.Generation != 17 {
		t.Fatalf("route codec ID = (%+v, %t), word0 want %#x", id, ok, wantWord0)
	}
	for _, invalid := range []struct {
		source     OperationSource
		route      RouteID
		local      uint32
		generation uint32
	}{
		{OperationSourceInvalid, 1, 1, 1},
		{OperationSourceManual, 0, 1, 1},
		{OperationSourceManual, RouteID(OperationRouteEncodingCapacity + 1), 1, 1},
		{OperationSourceManual, 1, 0, 1},
		{OperationSourceManual, 1, operationLocalMask + 1, 1},
		{OperationSourceManual, 1, 1, 0},
	} {
		if got, made := MakeOperationIDAtRoute(invalid.source, invalid.route, invalid.local, invalid.generation); made || got != (OperationID{}) {
			t.Fatalf("accepted invalid route ID %+v: %+v", invalid, got)
		}
	}
	next, ok := NextOperationIDAtRoute(id, id.Source(), id.Route(), id.LocalSlot())
	if !ok || next.Route() != id.Route() || next.LocalSlot() != id.LocalSlot() || next.Generation != id.Generation+1 {
		t.Fatalf("route-aware next = (%+v, %t)", next, ok)
	}
	if got, ok := NextOperationID(id, id.Source(), id.LocalSlot()); ok || got != (OperationID{}) {
		t.Fatal("route-1 compatibility helper changed a non-route-1 identity")
	}
}

func TestOperationRoutesKeepTwoPLocalIdentityDisjoint(t *testing.T) {
	routes := new(OperationRouteRegistry)
	route1, ok1 := routes.Allocate()
	route2, ok2 := routes.Allocate()
	if !ok1 || !ok2 || route1 != 1 || route2 != 2 {
		t.Fatalf("allocate two routes = (%d, %t), (%d, %t)", route1, ok1, route2, ok2)
	}
	first := bindRouteManualFixture(t, route1)
	second := bindRouteManualFixture(t, route2)
	if routes.Bind(route2, first.driver) {
		t.Fatal("bound route to a driver/source catalog from another route")
	}
	if !routes.Bind(route1, first.driver) || !routes.Bind(route2, second.driver) {
		t.Fatal("bind exact route catalogs")
	}
	state1, ticket1, ids1 := reserveManualWaitSet(t, first.manual, first.p, 101, []uint32{1})
	state2, ticket2, ids2 := reserveManualWaitSet(t, second.manual, second.p, 102, []uint32{1})
	id1, id2 := ids1[0], ids2[0]
	if id1.Source() != OperationSourceManual || id2.Source() != OperationSourceManual ||
		id1.LocalSlot() != 1 || id2.LocalSlot() != 1 || id1.Generation != 1 || id2.Generation != 1 ||
		id1.Route() != route1 || id2.Route() != route2 || id1 == id2 {
		t.Fatalf("two-P local identities alias: %+v %+v", id1, id2)
	}
	if result := second.manual.Post(id1); result != ManualOperationPostInvalid || second.manual.Pending() {
		t.Fatalf("wrong-route direct source post = %d, pending=%t", result, second.manual.Pending())
	}
	posted := routes.PostManualAndRequest(id1)
	if posted.Route != OperationRoutePosted || posted.Executor != ExecutorRequestPublished ||
		!first.manual.Pending() || second.manual.Pending() ||
		!first.registry.ObserveRequested(first.handle) || second.registry.ObserveRequested(second.handle) {
		t.Fatalf("route-1 post crossed owners: %+v", posted)
	}
	if duplicate := routes.PostManualAndRequest(id1); duplicate.Route != OperationRoutePostCoalesced || duplicate.Executor != ExecutorRequestInvalid {
		t.Fatalf("duplicate routed post = %+v", duplicate)
	}
	stale := id1
	stale.Generation++
	if result := routes.PostManualAndRequest(stale); result.Route != OperationRoutePostSourceStale {
		t.Fatalf("stale routed generation = %+v", result)
	}
	if posted = routes.PostManualAndRequest(id2); posted.Route != OperationRoutePosted || posted.Executor != ExecutorRequestPublished ||
		!second.manual.Pending() || !second.registry.ObserveRequested(second.handle) {
		t.Fatalf("route-2 post = %+v", posted)
	}
	unknown, made := MakeOperationIDAtRoute(OperationSourceManual, 3, 1, 1)
	if !made || routes.PostManualAndRequest(unknown).Route != OperationRoutePostStale {
		t.Fatal("unallocated route did not fail stale")
	}

	closeOperationRouteFixture(t, routes, route1)
	closeOperationRouteFixture(t, routes, route2)
	if late := routes.PostManualAndRequest(id1); late.Route != OperationRoutePostClosed || late.Executor != ExecutorRequestInvalid {
		t.Fatalf("retired-route late post = %+v", late)
	}
	if !routes.AllRetired() {
		t.Fatal("route registry retained a live binding after all routes retired")
	}
	settleRouteManualFixture(t, first, state1, ticket1, ids1)
	settleRouteManualFixture(t, second, state2, ticket2, ids2)
}

func TestOperationRouteWorkerSharedRegistryKeepsPayloadAndExecutorExact(t *testing.T) {
	routes := new(OperationRouteRegistry)
	route1, ok1 := routes.Allocate()
	route2, ok2 := routes.Allocate()
	if !ok1 || !ok2 || route1 != 1 || route2 != 2 {
		t.Fatalf("allocate worker routes = (%d, %t), (%d, %t)", route1, ok1, route2, ok2)
	}
	executors := new(ExecutorRegistry)
	first := bindRouteWorkerFixture(t, executors, route1)
	second := bindRouteWorkerFixture(t, executors, route2)
	if !routes.Bind(route1, first.driver) || !routes.Bind(route2, second.driver) {
		t.Fatal("bind routed worker catalogs")
	}
	state1, ticket1, ids1 := reserveWorkerWaitSet(t, first.worker, first.p, 151, []uint32{1})
	state2, ticket2, ids2 := reserveWorkerWaitSet(t, second.worker, second.p, 152, []uint32{1})
	id1, id2 := ids1[0], ids2[0]
	if id1.Source() != OperationSourceWorker || id2.Source() != OperationSourceWorker ||
		id1.LocalSlot() != 1 || id2.LocalSlot() != 1 || id1.Generation != 1 || id2.Generation != 1 ||
		id1.Route() != route1 || id2.Route() != route2 || id1 == id2 ||
		!first.worker.MarkSubmitted(first.p, id1) || !second.worker.MarkSubmitted(second.p, id2) {
		t.Fatalf("two routed worker identities alias or failed submission: %+v %+v", id1, id2)
	}
	payload1 := workerPayloadForTest(t, 1, 0x1111222233334444, 0x5555666677778888)
	payload2 := workerPayloadForTest(t, 2, 0x9999aaaabbbbcccc, 0xddddeeeeffff0000, 0x1020304050607080)
	if invalid := routes.PostWorkerAndRequest(id1, ScalarResultPayloadV1{}); invalid != (OperationRouteIngressResult{
		Route: OperationRoutePostInvalid, Executor: ExecutorRequestInvalid,
	}) || first.worker.Pending() {
		t.Fatalf("invalid routed worker payload = %+v, pending=%t", invalid, first.worker.Pending())
	}
	wrongSource, made := MakeOperationIDAtRoute(OperationSourceManual, route1, 1, 1)
	if !made || routes.PostWorkerAndRequest(wrongSource, payload1).Route != OperationRoutePostInvalid {
		t.Fatal("routed worker accepted a non-worker ID")
	}
	unknown, made := MakeOperationIDAtRoute(OperationSourceWorker, 3, 1, 1)
	if !made || routes.PostWorkerAndRequest(unknown, payload1).Route != OperationRoutePostStale {
		t.Fatal("routed worker accepted an unallocated route")
	}

	posted1 := routes.PostWorkerAndRequest(id1, payload1)
	if posted1.Route != OperationRoutePosted || posted1.Executor != ExecutorRequestPublished ||
		!first.worker.Pending() || second.worker.Pending() ||
		!executors.ObserveRequested(first.handle) || executors.ObserveRequested(second.handle) {
		t.Fatalf("route-1 worker post crossed source or executor: %+v", posted1)
	}
	if duplicate := routes.PostWorkerAndRequest(id1, payload2); duplicate.Route != OperationRoutePostCoalesced ||
		duplicate.Executor != ExecutorRequestInvalid {
		t.Fatalf("duplicate routed worker post = %+v", duplicate)
	}
	stale := id1
	stale.Generation++
	if result := routes.PostWorkerAndRequest(stale, payload2); result.Route != OperationRoutePostSourceStale ||
		result.Executor != ExecutorRequestInvalid {
		t.Fatalf("stale routed worker generation = %+v", result)
	}
	if first.worker.BeginClose(first.p, id1) != WorkerOperationCloseStarted {
		t.Fatal("begin routed worker source close")
	}
	if sourceClosed := routes.PostWorkerAndRequest(id1, payload2); sourceClosed.Route != OperationRoutePostSourceClosed || sourceClosed.Executor != ExecutorRequestInvalid {
		t.Fatalf("closed routed worker source = %+v", sourceClosed)
	}
	posted2 := routes.PostWorkerAndRequest(id2, payload2)
	if posted2.Route != OperationRoutePosted || posted2.Executor != ExecutorRequestPublished ||
		!second.worker.Pending() || !executors.ObserveRequested(second.handle) {
		t.Fatalf("route-2 worker post = %+v", posted2)
	}

	closeOperationRouteFixture(t, routes, route1)
	closeOperationRouteFixture(t, routes, route2)
	if late := routes.PostWorkerAndRequest(id1, payload1); late.Route != OperationRoutePostClosed ||
		late.Executor != ExecutorRequestInvalid {
		t.Fatalf("retired worker route late post = %+v", late)
	}
	if !routes.AllRetired() {
		t.Fatal("worker route registry retained a live binding")
	}
	settleRouteWorkerFixture(t, first, state1, ticket1, id1, payload1)
	settleRouteWorkerFixture(t, second, state2, ticket2, id2, payload2)
}

func TestOperationRouteWorkerStrongJoinCoversPostRequestTail(t *testing.T) {
	routes := new(OperationRouteRegistry)
	route, ok := routes.Allocate()
	if !ok {
		t.Fatal("allocate strong-join worker route")
	}
	executors := new(ExecutorRegistry)
	fixture := bindRouteWorkerFixture(t, executors, route)
	if !routes.Bind(route, fixture.driver) {
		t.Fatal("bind strong-join worker route")
	}
	state, ticket, ids := reserveWorkerWaitSet(t, fixture.worker, fixture.p, 153, []uint32{1})
	id := ids[0]
	payload := workerPayloadForTest(t, 3, 7, 8, 9)
	if !fixture.worker.MarkSubmitted(fixture.p, id) {
		t.Fatal("mark strong-join worker submitted")
	}
	slot, found := operationRouteSlotFor(routes, route)
	if !found {
		t.Fatal("find strong-join route slot")
	}
	posted := make(chan struct{})
	release := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		if !operationRouteAcquireProducer(slot) {
			close(posted)
			done <- false
			return
		}
		postOK := fixture.worker.Post(id, payload) == WorkerOperationPosted
		requestOK := executors.Request(fixture.handle) == ExecutorRequestPublished
		close(posted)
		<-release
		operationRouteReleaseProducer(slot)
		done <- postOK && requestOK
	}()
	<-posted
	if !routes.BeginClose(route) {
		t.Fatal("begin strong-join worker route close")
	}
	if routes.ConfirmQuiesced(route) {
		t.Fatal("worker route quiesced while Post -> Request tail remained admitted")
	}
	close(release)
	if !<-done || !routes.ConfirmQuiesced(route) || !routes.Retire(route) {
		t.Fatal("finish strong-join worker route")
	}
	settleRouteWorkerFixture(t, fixture, state, ticket, id, payload)
}

func TestOperationRouteControlUsesBoundExecutor(t *testing.T) {
	routes := new(OperationRouteRegistry)
	route, ok := routes.Allocate()
	if !ok {
		t.Fatal("allocate control route")
	}
	p := new(P)
	driver := new(ExecutorDriver)
	executors := new(ExecutorRegistry)
	control := new(TaskControlSource)
	executor := registerTestExecutor(t, executors)
	if !BindExecutorSourceCatalogAtRoute(driver, p, executors, executor, route,
		ExecutorSourceCatalog{Control: control}) || !routes.Bind(route, driver) {
		t.Fatal("bind routed control driver")
	}
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize routed control G")
	}
	g.state = GRunnable
	if !Enqueue(p, g) {
		t.Fatal("enqueue routed control G")
	}
	id, ok := RegisterTaskControl(control, p, g)
	if !ok || id.Route() != route || id.LocalSlot() != 1 || id.Generation != 1 {
		t.Fatalf("register routed task control = (%+v, %t)", id, ok)
	}
	posted := routes.PostTaskControlAndRequest(id, TaskCancelShutdown)
	if posted.Route != OperationRoutePosted || posted.Executor != ExecutorRequestPublished ||
		!executors.ObserveRequested(executor) || !control.Pending() {
		t.Fatalf("routed task control post = %+v", posted)
	}
	closeOperationRouteFixture(t, routes, route)
	if delivered, discarded, ok := control.PublishPass(p); !ok || delivered != 1 || discarded != 0 {
		t.Fatalf("publish routed control = (%d, %d, %t)", delivered, discarded, ok)
	}
	if kind, ok := TaskCancellationOf(p, g); !ok || kind != TaskCancelShutdown {
		t.Fatalf("routed cancellation = (%d, %t)", kind, ok)
	}
	closeTaskControlFixture(t, control, p, id)
	if _, _, ok := PollExecutor(driver); !ok {
		t.Fatal("acknowledge control executor request")
	}
	closeTestExecutorDriver(t, driver)
	if !routes.AllRetired() {
		t.Fatal("retired control route retained a live binding")
	}
}

func TestOperationRouteCloseStrongJoinsConcurrentPosts(t *testing.T) {
	routes := new(OperationRouteRegistry)
	route, ok := routes.Allocate()
	if !ok {
		t.Fatal("allocate concurrent route")
	}
	fixture := bindRouteManualFixture(t, route)
	if !routes.Bind(route, fixture.driver) {
		t.Fatal("bind concurrent route")
	}
	state, ticket, ids := reserveManualWaitSet(t, fixture.manual, fixture.p, 111, []uint32{1})
	id := ids[0]
	if posted := routes.PostManualAndRequest(id); posted.Route != OperationRoutePosted {
		t.Fatalf("initial concurrent route post = %+v", posted)
	}
	const producers = 32
	start := make(chan struct{})
	results := make(chan OperationRoutePostResult, producers)
	var wg sync.WaitGroup
	for index := 0; index < producers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- routes.PostManualAndRequest(id).Route
		}()
	}
	close(start)
	runtime.Gosched()
	if !routes.BeginClose(route) {
		t.Fatal("begin concurrent route close")
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result != OperationRoutePostCoalesced && result != OperationRoutePostClosed {
			t.Fatalf("concurrent post result = %d", result)
		}
	}
	if !routes.ConfirmQuiesced(route) || !routes.Retire(route) {
		t.Fatal("strong-join concurrent route")
	}
	if late := routes.PostManualAndRequest(id); late.Route != OperationRoutePostClosed {
		t.Fatalf("post-close route result = %+v", late)
	}
	settleRouteManualFixture(t, fixture, state, ticket, ids)
}

func TestOperationRouteAllocationNeverReusesRetiredTombstone(t *testing.T) {
	routes := new(OperationRouteRegistry)
	for index := uint32(0); index < OperationRouteRegistryCapacity; index++ {
		route, ok := routes.Allocate()
		if !ok || route != RouteID(index+1) {
			t.Fatalf("allocate route %d = (%d, %t)", index, route, ok)
		}
		closeOperationRouteFixture(t, routes, route)
	}
	if route, ok := routes.Allocate(); ok || route != 0 {
		t.Fatalf("profile route exhaustion = (%d, %t)", route, ok)
	}
	for index := range routes.slots {
		if preemptLoad(&routes.slots[index].state) != uint32(operationRouteRetired) ||
			preemptLoad(&routes.slots[index].route) != uint32(index+1) {
			t.Fatalf("route %d tombstone was reused", index+1)
		}
	}
	if !routes.AllRetired() {
		t.Fatal("exhausted route registry retained a live binding")
	}
}

func TestRouteAwareSourceBindingRejectsIdentityChange(t *testing.T) {
	p := new(P)
	manual := new(ManualOperationSource)
	if !BindManualOperationSourceAtRoute(manual, p, 2) {
		t.Fatal("establish manual route identity")
	}
	state, ticket, ids := reserveManualWaitSet(t, manual, p, 121, []uint32{1})
	if !RequestParkCancel(state, ticket, ParkCancelOperation) {
		t.Fatal("cancel used route-aware source")
	}
	resolution, ok := ResolveParkSnapshot(state, ticket)
	if !ok || resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 1}) {
		t.Fatalf("resolve used route-aware source = (%+v, %t)", resolution, ok)
	}
	if applied, detached, ok := manual.ApplyAndDetach(p); !ok || applied != 1 || detached != 1 {
		t.Fatalf("apply used route-aware source = (%d, %d, %t)", applied, detached, ok)
	}
	if outcome, _, lease, consumed := ConsumeParkSet(state, ticket); !consumed || outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) {
		t.Fatalf("consume used route-aware source = (%d, %+v, %t)", outcome, lease, consumed)
	}
	finishManualOperations(t, manual, p, ids, OperationResultLease{})
	if !UnbindManualOperationSource(manual, p) {
		t.Fatal("unbind used route-aware source")
	}
	driver := new(ExecutorDriver)
	executors := new(ExecutorRegistry)
	executor := registerTestExecutor(t, executors)
	if BindExecutorSourceCatalogAtRoute(driver, p, executors, executor, 1,
		ExecutorSourceCatalog{Manual: manual}) || *driver != (ExecutorDriver{}) {
		t.Fatal("changed a source's persistent route identity")
	}
	if !BindExecutorSourceCatalogAtRoute(driver, p, executors, executor, 2,
		ExecutorSourceCatalog{Manual: manual}) {
		t.Fatal("rebind source at its established route")
	}
	closeTestExecutorDriver(t, driver)

	controlP, g := newReadyTaskCancelFixture(t)
	control := new(TaskControlSource)
	if !BindTaskControlSourceAtRoute(control, controlP, 4) {
		t.Fatal("bind route-aware control source")
	}
	controlID, ok := RegisterTaskControl(control, controlP, g)
	if !ok || controlID.Route() != 4 {
		t.Fatalf("use route-aware control source = (%+v, %t)", controlID, ok)
	}
	closeTaskControlFixture(t, control, controlP, controlID)
	if !UnbindTaskControlSource(control, controlP) {
		t.Fatal("unbind used route-aware control source")
	}
	if BindTaskControlSourceAtRoute(control, controlP, 5) {
		t.Fatal("changed a used control source's route identity")
	}
	if !BindTaskControlSourceAtRoute(control, controlP, 4) || !UnbindTaskControlSource(control, controlP) {
		t.Fatal("rebind used control source at established route")
	}
}
