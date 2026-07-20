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
	"sync"
	"testing"
	"unsafe"
)

type executorFleetManualFixture struct {
	p      *P
	driver *ExecutorDriver
	waits  *WaitRegistrationTable
	manual *ManualOperationSource
	handle ExecutorFleetHandle
}

func bindExecutorFleetManualFixture(t *testing.T, fleet *ExecutorFleet) *executorFleetManualFixture {
	t.Helper()
	fixture := &executorFleetManualFixture{
		p:      new(P),
		driver: new(ExecutorDriver),
		waits:  new(WaitRegistrationTable),
		manual: new(ManualOperationSource),
	}
	var ok bool
	fixture.handle, ok = BindExecutorFleet(fleet, fixture.driver, fixture.p, ExecutorSourceCatalog{
		Waits: fixture.waits, Manual: fixture.manual,
	})
	if !ok {
		t.Fatal("bind executor fleet manual fixture")
	}
	return fixture
}

func closeExecutorFleetFixture(t *testing.T, fleet *ExecutorFleet, fixture *executorFleetManualFixture) {
	t.Helper()
	if !BeginExecutorFleetClose(fleet, fixture.handle) ||
		!ConfirmExecutorFleetRouteClose(fleet, fixture.handle) ||
		!BeginExecutorFleetDriverClose(fleet, fixture.handle) ||
		!ConfirmExecutorFleetClose(fleet, fixture.handle) {
		t.Fatalf("close executor fleet route %d", fixture.handle.Route)
	}
	if *fixture.driver != (ExecutorDriver{}) || !fixture.waits.CanRelease() || !fixture.manual.CanRelease() ||
		fixture.p.executor != nil || preemptLoad(&fixture.p.executorMode) != executorModeUnbound {
		t.Fatalf("fleet close retained route %d resources", fixture.handle.Route)
	}
}

func settleExecutorFleetManual(
	t *testing.T,
	fixture *executorFleetManualFixture,
	state *ParkState,
	ticket ParkTicket,
	ids []OperationID,
) {
	t.Helper()
	published, lost, ok := fixture.manual.PublishPass(fixture.p)
	if !ok || published != 1 || lost != 0 {
		t.Fatalf("publish fleet manual = (%d, %d, %t)", published, lost, ok)
	}
	want := CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}
	if resolution, duplicates, ok := fixture.manual.ResolveAffectedPublishedEpoch(fixture.p); !ok || resolution != want || duplicates != 0 {
		t.Fatalf("resolve fleet manual = (%+v, %d, %t), want %+v", resolution, duplicates, ok, want)
	}
	if applied, detached, ok := fixture.manual.ApplyAndDetach(fixture.p); !ok || applied != 1 || detached != 1 {
		t.Fatalf("apply fleet manual = (%d, %d, %t)", applied, detached, ok)
	}
	outcome, _, lease, consumed := ConsumeParkSet(state, ticket)
	if !consumed || outcome != ParkOutcomeCompleted {
		t.Fatalf("consume fleet manual = (%d, %+v, %t)", outcome, lease, consumed)
	}
	finishManualOperations(t, fixture.manual, fixture.p, ids, lease)
	if _, _, ok := PollExecutor(fixture.driver); !ok {
		t.Fatal("acknowledge fleet executor request")
	}
}

func TestExecutorFleetHandleIsThreeWordPOD(t *testing.T) {
	if unsafe.Sizeof(ExecutorFleetHandle{}) != 12 || unsafe.Alignof(ExecutorFleetHandle{}) != 4 ||
		unsafe.Offsetof(ExecutorFleetHandle{}.Executor) != 4 {
		t.Fatalf("fleet handle layout = size %d align %d executor %d",
			unsafe.Sizeof(ExecutorFleetHandle{}), unsafe.Alignof(ExecutorFleetHandle{}),
			unsafe.Offsetof(ExecutorFleetHandle{}.Executor))
	}
	if !(ExecutorFleetHandle{Route: 1, Executor: ExecutorHandle{Slot: 1, Generation: 1}}).Valid() ||
		(ExecutorFleetHandle{Route: 0, Executor: ExecutorHandle{Slot: 1, Generation: 1}}).Valid() ||
		(ExecutorFleetHandle{Route: 1, Executor: ExecutorHandle{Slot: 0, Generation: 1}}).Valid() {
		t.Fatal("fleet handle validity accepted a malformed identity")
	}
}

func TestExecutorFleetRoutesTwoPCompletionsToExactExecutor(t *testing.T) {
	fleet := new(ExecutorFleet)
	if !fleet.AllRetired() {
		t.Fatal("zero fleet is not resource-empty")
	}
	first := bindExecutorFleetManualFixture(t, fleet)
	second := bindExecutorFleetManualFixture(t, fleet)
	if first.p == second.p || first.driver == second.driver || first.handle.Route != 1 || second.handle.Route != 2 ||
		first.handle.Executor == second.handle.Executor {
		t.Fatalf("two-P fleet identities = %+v %+v", first.handle, second.handle)
	}

	state1, ticket1, ids1 := reserveManualWaitSet(t, first.manual, first.p, 701, []uint32{1})
	state2, ticket2, ids2 := reserveManualWaitSet(t, second.manual, second.p, 702, []uint32{1})
	id1, id2 := ids1[0], ids2[0]
	if id1.Route() != 1 || id2.Route() != 2 || id1.LocalSlot() != id2.LocalSlot() ||
		id1.Generation != id2.Generation || id1 == id2 {
		t.Fatalf("two-P operation identities alias: %+v %+v", id1, id2)
	}
	if result := second.manual.Post(id1); result != ManualOperationPostInvalid || second.manual.Pending() {
		t.Fatalf("wrong route reached second source = %d", result)
	}
	posted1 := fleet.PostManualAndRequest(id1)
	if posted1.Route != OperationRoutePosted || posted1.Executor != ExecutorRequestPublished ||
		!fleet.executors.ObserveRequested(first.handle.Executor) ||
		fleet.executors.ObserveRequested(second.handle.Executor) || !first.manual.Pending() || second.manual.Pending() {
		t.Fatalf("first route crossed fleet owners: %+v", posted1)
	}
	if duplicate := fleet.PostManualAndRequest(id1); duplicate.Route != OperationRoutePostCoalesced ||
		duplicate.Executor != ExecutorRequestInvalid {
		t.Fatalf("duplicate first route = %+v", duplicate)
	}
	stale := id1
	stale.Generation++
	if result := fleet.PostManualAndRequest(stale); result.Route != OperationRoutePostSourceStale ||
		result.Executor != ExecutorRequestInvalid {
		t.Fatalf("stale first route = %+v", result)
	}
	posted2 := fleet.PostManualAndRequest(id2)
	if posted2.Route != OperationRoutePosted || posted2.Executor != ExecutorRequestPublished ||
		!fleet.executors.ObserveRequested(second.handle.Executor) || !second.manual.Pending() {
		t.Fatalf("second route completion = %+v", posted2)
	}

	settleExecutorFleetManual(t, first, state1, ticket1, ids1)
	settleExecutorFleetManual(t, second, state2, ticket2, ids2)
	closeExecutorFleetFixture(t, fleet, first)
	closeExecutorFleetFixture(t, fleet, second)
	if late := fleet.PostManualAndRequest(id1); late.Route != OperationRoutePostClosed ||
		late.Executor != ExecutorRequestInvalid {
		t.Fatalf("retired route accepted late completion: %+v", late)
	}
	if !fleet.AllRetired() {
		t.Fatal("two-P fleet retained resources")
	}
}

func TestExecutorFleetCloseStrongJoinsRouteIngressBeforeDriver(t *testing.T) {
	fleet := new(ExecutorFleet)
	fixture := bindExecutorFleetManualFixture(t, fleet)
	route := RouteID(fixture.handle.Route)
	routeSlot, ok := operationRouteSlotFor(&fleet.routes, route)
	if !ok || !operationRouteAcquireProducer(routeSlot) {
		t.Fatal("hold fleet route producer tail")
	}
	if !BeginExecutorFleetClose(fleet, fixture.handle) {
		t.Fatal("begin fleet route close")
	}
	if ConfirmExecutorFleetRouteClose(fleet, fixture.handle) ||
		BeginExecutorFleetDriverClose(fleet, fixture.handle) {
		t.Fatal("fleet retired source/driver before route ingress strong join")
	}
	operationRouteReleaseProducer(routeSlot)
	if !ConfirmExecutorFleetRouteClose(fleet, fixture.handle) ||
		!BeginExecutorFleetDriverClose(fleet, fixture.handle) ||
		!ConfirmExecutorFleetClose(fleet, fixture.handle) || !fleet.AllRetired() {
		t.Fatal("finish fleet close after route ingress join")
	}
}

func TestExecutorFleetTransferUsesRouteAdmissionAndRequiresEmptyMailbox(t *testing.T) {
	fleet := new(ExecutorFleet)
	first := bindExecutorFleetManualFixture(t, fleet)
	second := bindExecutorFleetManualFixture(t, fleet)
	source := new(P)
	task := newYieldingTestG(t, "fleet-transfer")
	if !Enqueue(source, task.g) {
		t.Fatal("enqueue fleet transfer source")
	}
	id, request, ok := fleet.PublishPNeutralRunnableAndRequest(first.handle, source, task.g)
	if !ok || !id.Valid() || request != ExecutorRequestPublished || source.readyHead != nil ||
		!fleet.executors.ObserveRequested(first.handle.Executor) ||
		fleet.executors.ObserveRequested(second.handle.Executor) {
		t.Fatalf("publish fleet transfer = (%+v, %d, %t)", id, request, ok)
	}
	if fleet.ImportPNeutralRunnable(second.handle, second.p, id) || second.p.readyHead != nil {
		t.Fatal("wrong fleet route imported transfer")
	}
	staleSource := new(P)
	staleTask := newYieldingTestG(t, "fleet-transfer-stale")
	if !Enqueue(staleSource, staleTask.g) {
		t.Fatal("enqueue stale fleet transfer source")
	}
	stale := first.handle
	stale.Executor.Generation++
	if staleID, staleRequest, staleOK := fleet.PublishPNeutralRunnableAndRequest(stale, staleSource, staleTask.g); staleOK || staleID != (RunnableTransferID{}) || staleRequest != ExecutorRequestStale ||
		staleSource.readyHead != staleTask.g || !staleTask.g.queued {
		t.Fatalf("stale fleet transfer mutated source = (%+v, %d, %t)", staleID, staleRequest, staleOK)
	}
	if !BeginExecutorFleetClose(fleet, first.handle) {
		t.Fatal("seal fleet transfer route")
	}
	if ConfirmExecutorFleetRouteClose(fleet, first.handle) {
		t.Fatal("retired fleet route with a durable transferred runnable")
	}
	if !fleet.ImportPNeutralRunnable(first.handle, first.p, id) || first.p.readyHead != task.g ||
		fleet.ImportPNeutralRunnable(first.handle, first.p, id) {
		t.Fatal("drain exact fleet transfer during route close")
	}
	if !ConfirmExecutorFleetRouteClose(fleet, first.handle) {
		t.Fatal("retire drained fleet transfer route")
	}

	// This test intentionally stops at RouteRetired: the imported runnable is
	// now ordinary owner-P work, so the driver correctly refuses shutdown until
	// that task terminates or is migrated by a later scheduler policy.
	if BeginExecutorFleetDriverClose(fleet, first.handle) {
		t.Fatal("closed driver while imported runnable remained queued")
	}
}

func TestExecutorFleetConcurrentCompletionAndCloseFailClosed(t *testing.T) {
	fleet := new(ExecutorFleet)
	fixture := bindExecutorFleetManualFixture(t, fleet)
	state, ticket, ids := reserveManualWaitSet(t, fixture.manual, fixture.p, 703, []uint32{1})
	id := ids[0]

	const producers = 32
	start := make(chan struct{})
	results := make(chan OperationRoutePostResult, producers)
	var group sync.WaitGroup
	group.Add(producers)
	for index := 0; index < producers; index++ {
		go func() {
			defer group.Done()
			<-start
			results <- fleet.PostManualAndRequest(id).Route
		}()
	}
	close(start)
	if !BeginExecutorFleetClose(fleet, fixture.handle) {
		t.Fatal("begin concurrent fleet close")
	}
	group.Wait()
	close(results)
	posted := false
	for result := range results {
		switch result {
		case OperationRoutePosted:
			posted = true
		case OperationRoutePostCoalesced, OperationRoutePostClosed:
		default:
			t.Fatalf("concurrent fleet completion = %d", result)
		}
	}
	if !ConfirmExecutorFleetRouteClose(fleet, fixture.handle) {
		t.Fatal("strong-join concurrent fleet route")
	}
	if posted {
		settleExecutorFleetManual(t, fixture, state, ticket, ids)
	} else {
		if !RequestParkCancel(state, ticket, ParkCancelOperation) {
			t.Fatal("cancel never-posted fleet operation")
		}
		if resolution, ok := ResolveParkSnapshot(state, ticket); !ok ||
			resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 1}) {
			t.Fatalf("resolve never-posted fleet operation = (%+v, %t)", resolution, ok)
		}
		if applied, detached, ok := fixture.manual.ApplyAndDetach(fixture.p); !ok || applied != 1 || detached != 1 {
			t.Fatalf("detach never-posted fleet operation = (%d, %d, %t)", applied, detached, ok)
		}
		outcome, _, lease, consumed := ConsumeParkSet(state, ticket)
		if !consumed || outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) {
			t.Fatalf("consume never-posted fleet operation = (%d, %+v, %t)", outcome, lease, consumed)
		}
		finishManualOperations(t, fixture.manual, fixture.p, ids, lease)
	}
	if !BeginExecutorFleetDriverClose(fleet, fixture.handle) ||
		!ConfirmExecutorFleetClose(fleet, fixture.handle) || !fleet.AllRetired() {
		t.Fatal("finish concurrent fleet close")
	}
}

func TestExecutorFleetBindPreflightAndLifetimeCapacity(t *testing.T) {
	fleet := new(ExecutorFleet)
	first := bindExecutorFleetManualFixture(t, fleet)
	nextBefore := fleet.routes.next
	if handle, ok := BindExecutorFleet(fleet, first.driver, first.p, ExecutorSourceCatalog{
		Waits: first.waits, Manual: first.manual,
	}); ok || handle != (ExecutorFleetHandle{}) || fleet.routes.next != nextBefore {
		t.Fatalf("duplicate fleet bind mutated route allocation = (%+v, %t), next=%d", handle, ok, fleet.routes.next)
	}

	foreignRegistry := new(ExecutorRegistry)
	foreignP := new(P)
	foreignDriver := new(ExecutorDriver)
	foreignWaits := new(WaitRegistrationTable)
	foreignManual := new(ManualOperationSource)
	foreignExecutor := registerTestExecutor(t, foreignRegistry)
	if !BindExecutorSourceCatalogAtRoute(foreignDriver, foreignP, foreignRegistry, foreignExecutor, 1,
		ExecutorSourceCatalog{Waits: foreignWaits, Manual: foreignManual}) {
		t.Fatal("bind foreign-registry driver")
	}
	if handle, ok := BindExecutorFleet(fleet, foreignDriver, foreignP, ExecutorSourceCatalog{
		Waits: foreignWaits, Manual: foreignManual,
	}); ok || handle != (ExecutorFleetHandle{}) || fleet.routes.next != nextBefore {
		t.Fatal("fleet adopted a driver from a foreign executor registry")
	}
	closeTestExecutorDriver(t, foreignDriver)

	wrongRouteP := new(P)
	wrongRouteSource := new(ManualOperationSource)
	if !BindManualOperationSourceAtRoute(wrongRouteSource, wrongRouteP, 7) ||
		!UnbindManualOperationSource(wrongRouteSource, wrongRouteP) {
		t.Fatal("establish wrong-route source identity")
	}
	if handle, ok := BindExecutorFleet(fleet, new(ExecutorDriver), new(P), ExecutorSourceCatalog{
		Waits: new(WaitRegistrationTable), Manual: wrongRouteSource,
	}); ok || handle != (ExecutorFleetHandle{}) || fleet.routes.next != nextBefore || !wrongRouteSource.CanRelease() {
		t.Fatal("wrong-route catalog entered fleet bind transaction")
	}
	if handle, ok := BindExecutorFleet(fleet, new(ExecutorDriver), new(P), ExecutorSourceCatalog{
		Waits: new(WaitRegistrationTable),
	}); ok || handle != (ExecutorFleetHandle{}) || fleet.routes.next != nextBefore {
		t.Fatal("wait-only catalog consumed a fleet route")
	}

	// Timer/Poll source identities are route-aware, but the current route
	// registry has no producer dispatch entry for either. Fleet Bind must reject
	// them before it consumes a route or executor generation, even when a Manual
	// source would otherwise make the catalog route-bindable.
	{
		p, driver := new(P), new(ExecutorDriver)
		waits, manual, timers := new(WaitRegistrationTable), new(ManualOperationSource), new(TimerRegistrationTable)
		beforeExecutors := fleet.executors.slots
		if handle, ok := BindExecutorFleet(fleet, driver, p, ExecutorSourceCatalog{
			Waits: waits, Timers: timers, Manual: manual,
		}); ok || handle != (ExecutorFleetHandle{}) || fleet.routes.next != nextBefore ||
			fleet.executors.slots != beforeExecutors || *driver != (ExecutorDriver{}) ||
			p.executor != nil || !waits.CanRelease() || !manual.CanRelease() || !timers.CanRelease() {
			t.Fatal("timer catalog entered fleet bind transaction")
		}
	}
	{
		p, driver := new(P), new(ExecutorDriver)
		waits, manual, poll := new(WaitRegistrationTable), new(ManualOperationSource), new(PollOperationSource)
		beforeExecutors := fleet.executors.slots
		if handle, ok := BindExecutorFleet(fleet, driver, p, ExecutorSourceCatalog{
			Waits: waits, Poll: poll, Manual: manual,
		}); ok || handle != (ExecutorFleetHandle{}) || fleet.routes.next != nextBefore ||
			fleet.executors.slots != beforeExecutors || *driver != (ExecutorDriver{}) ||
			p.executor != nil || !waits.CanRelease() || !manual.CanRelease() || !poll.CanRelease() {
			t.Fatal("poll catalog entered fleet bind transaction")
		}
	}

	oldHandle := first.handle
	closeExecutorFleetFixture(t, fleet, first)
	var previousExecutor = oldHandle.Executor
	for route := uint32(2); route <= ExecutorFleetCapacity; route++ {
		fixture := bindExecutorFleetManualFixture(t, fleet)
		if fixture.handle.Route != route || fixture.handle.Executor.Slot != previousExecutor.Slot ||
			fixture.handle.Executor.Generation <= previousExecutor.Generation {
			t.Fatalf("fleet route/executor generation %d = %+v after %+v", route, fixture.handle, previousExecutor)
		}
		if result := fleet.executors.Request(previousExecutor); result != ExecutorRequestStale &&
			result != ExecutorRequestClosed {
			t.Fatalf("old executor generation %d request = %d", route, result)
		}
		previousExecutor = fixture.handle.Executor
		closeExecutorFleetFixture(t, fleet, fixture)
	}
	if handle, ok := BindExecutorFleet(fleet, new(ExecutorDriver), new(P), ExecutorSourceCatalog{
		Waits: new(WaitRegistrationTable), Manual: new(ManualOperationSource),
	}); ok || handle != (ExecutorFleetHandle{}) || fleet.routes.next != ExecutorFleetCapacity {
		t.Fatalf("fleet lifetime capacity exhaustion = (%+v, %t), next=%d", handle, ok, fleet.routes.next)
	}
	late := fleet.PostManualAndRequest(OperationID{})
	if !fleet.AllRetired() || late.Route != OperationRoutePostInvalid {
		t.Fatal("exhausted fleet retained resources or accepted invalid ingress")
	}
}
