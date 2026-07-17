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
	waits    *WaitRegistrationTable
	manual   *ManualOperationSource
	handle   ExecutorHandle
	route    RouteID
}

func bindRouteManualFixture(t *testing.T, route RouteID) *routeManualFixture {
	t.Helper()
	fixture := &routeManualFixture{
		p:        new(P),
		driver:   new(ExecutorDriver),
		registry: new(ExecutorRegistry),
		waits:    new(WaitRegistrationTable),
		manual:   new(ManualOperationSource),
		route:    route,
	}
	fixture.handle = registerTestExecutor(t, fixture.registry)
	if !BindExecutorSourceCatalogAtRoute(fixture.driver, fixture.p, fixture.registry, fixture.handle, route,
		ExecutorSourceCatalog{Waits: fixture.waits, Manual: fixture.manual}) {
		t.Fatal("bind route-aware manual driver")
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

func TestOperationRouteControlUsesBoundExecutor(t *testing.T) {
	routes := new(OperationRouteRegistry)
	route, ok := routes.Allocate()
	if !ok {
		t.Fatal("allocate control route")
	}
	p := new(P)
	driver := new(ExecutorDriver)
	executors := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	control := new(TaskControlSource)
	executor := registerTestExecutor(t, executors)
	if !BindExecutorSourceCatalogAtRoute(driver, p, executors, executor, route,
		ExecutorSourceCatalog{Waits: waits, Control: control}) || !routes.Bind(route, driver) {
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
	waits := new(WaitRegistrationTable)
	executor := registerTestExecutor(t, executors)
	if BindExecutorSourceCatalogAtRoute(driver, p, executors, executor, 1,
		ExecutorSourceCatalog{Waits: waits, Manual: manual}) || *driver != (ExecutorDriver{}) || !waits.CanRelease() {
		t.Fatal("changed a source's persistent route identity")
	}
	if !BindExecutorSourceCatalogAtRoute(driver, p, executors, executor, 2,
		ExecutorSourceCatalog{Waits: waits, Manual: manual}) {
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
