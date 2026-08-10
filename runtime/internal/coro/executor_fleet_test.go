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
	manual *ManualOperationSource
	handle ExecutorFleetHandle
}

func bindExecutorFleetManualFixture(t *testing.T, fleet *ExecutorFleet) *executorFleetManualFixture {
	t.Helper()
	fixture := &executorFleetManualFixture{
		p:      new(P),
		driver: new(ExecutorDriver),
		manual: new(ManualOperationSource),
	}
	var ok bool
	fixture.handle, ok = BindExecutorFleet(fleet, fixture.driver, fixture.p, ExecutorSourceCatalog{
		Manual: fixture.manual,
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
	if *fixture.driver != (ExecutorDriver{}) || !fixture.manual.CanRelease() ||
		fixture.p.executor != nil || preemptLoad(&fixture.p.executorMode) != executorModeUnbound {
		t.Fatalf("fleet close retained route %d resources", fixture.handle.Route)
	}
}

func finishExecutorFleetTask(
	t *testing.T,
	fixture *executorFleetManualFixture,
	task *yieldingTestG,
	action Action,
) {
	t.Helper()
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatalf("prepare fleet task completion for %s", task.name)
	}
	action, ok := Resumed(fixture.p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume fleet task completion for %s = (%+v,%t)", task.name, action, ok)
	}
	action, ok = Checked(fixture.p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check fleet task destroy for %s = (%+v,%t)", task.name, action, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	receipt, ok := DestroyedBounded(fixture.p, task.g, action)
	if !ok || receipt.Kind != ActionCommitDestroy {
		t.Fatalf("publish fleet task destroy for %s = (%+v,%t)", task.name, receipt, ok)
	}
	complete, ok := CommitExecutorRunDomainDestroy(fixture.driver, task.g, receipt)
	if !ok || complete.Kind != ActionComplete {
		t.Fatalf("commit fleet task destroy for %s = (%+v,%t)", task.name, complete, ok)
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

func enqueueMaterializedFleetRunnable(
	t *testing.T,
	p *P,
	task *yieldingTestG,
	preferred RouteID,
) {
	t.Helper()
	task.g.active.state = FrameSuspended
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	task.g.park = ParkState{
		ticket:     ParkTicket{generation: 1},
		phase:      parkMaterialized,
		seed:       uint32(preferred),
		outcome:    ParkOutcomeCompleted,
		winnerCase: 1,
	}
	if !validParkState(&task.g.park) || !Enqueue(p, task.g) {
		t.Fatal("enqueue materialized fleet runnable")
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

func TestExecutorFleetRequestsCommittedChannelEndpointByExactRoute(t *testing.T) {
	fleet := new(ExecutorFleet)
	type fixture struct {
		p       *P
		driver  *ExecutorDriver
		channel *ChannelOperationSource
		handle  ExecutorFleetHandle
	}
	bind := func() *fixture {
		current := &fixture{
			p: new(P), driver: new(ExecutorDriver),
			channel: new(ChannelOperationSource),
		}
		var ok bool
		current.handle, ok = BindExecutorFleet(fleet, current.driver, current.p, ExecutorSourceCatalog{
			Channel: current.channel,
		})
		if !ok {
			t.Fatal("bind fleet channel route")
		}
		return current
	}
	first, second := bind(), bind()
	firstID, firstOK := MakeOperationIDAtRoute(OperationSourceChannel, RouteID(first.handle.Route), 1, 1)
	secondID, secondOK := MakeOperationIDAtRoute(OperationSourceChannel, RouteID(second.handle.Route), 1, 1)
	if !firstOK || !secondOK || firstID.Route() == secondID.Route() {
		t.Fatalf("make distinct channel route IDs = (%+v,%t) (%+v,%t)", firstID, firstOK, secondID, secondOK)
	}
	if result := fleet.RequestChannelExecutor(firstID); result != ExecutorRequestPublished ||
		!fleet.executors.ObserveRequested(first.handle.Executor) ||
		fleet.executors.ObserveRequested(second.handle.Executor) {
		t.Fatalf("request first channel route = %d", result)
	}
	if _, _, ok := PollExecutor(first.driver); !ok {
		t.Fatal("acknowledge first channel request")
	}
	if result := fleet.RequestChannelExecutor(secondID); result != ExecutorRequestPublished ||
		!fleet.executors.ObserveRequested(second.handle.Executor) {
		t.Fatalf("request second channel route = %d", result)
	}
	if _, _, ok := PollExecutor(second.driver); !ok {
		t.Fatal("acknowledge second channel request")
	}
	manualID, made := MakeOperationIDAtRoute(OperationSourceManual, firstID.Route(), 1, 1)
	if !made || fleet.RequestChannelExecutor(manualID) != ExecutorRequestInvalid {
		t.Fatal("channel request accepted another source kind")
	}
	for _, current := range []*fixture{first, second} {
		if !BeginExecutorFleetClose(fleet, current.handle) ||
			!ConfirmExecutorFleetRouteClose(fleet, current.handle) ||
			!BeginExecutorFleetDriverClose(fleet, current.handle) ||
			!ConfirmExecutorFleetClose(fleet, current.handle) {
			t.Fatalf("close fleet channel route %d", current.handle.Route)
		}
		if !current.channel.CanRelease() {
			t.Fatalf("fleet channel route %d retained source storage", current.handle.Route)
		}
	}
	if fleet.RequestChannelExecutor(firstID) != ExecutorRequestClosed || !fleet.AllRetired() {
		t.Fatal("retired fleet channel route accepted a wake")
	}
}

func TestExecutorFleetAdoptsExistingProgramDomainBeforeOwnedPeer(t *testing.T) {
	fleet := new(ExecutorFleet)
	programP := new(P)
	programDriver := new(ExecutorDriver)
	programRegistry := new(ExecutorRegistry)
	programManual := new(ManualOperationSource)
	programExecutor := registerTestExecutor(t, programRegistry)
	if !BindExecutorSourceCatalogAtRoute(
		programDriver,
		programP,
		programRegistry,
		programExecutor,
		1,
		ExecutorSourceCatalog{Manual: programManual},
	) {
		t.Fatal("bind existing program executor")
	}
	queued := newYieldingTestG(t, "adopted-program-ready")
	if !Enqueue(programP, queued.g) {
		t.Fatal("enqueue existing program root before fleet adoption")
	}
	programHandle, adopted := AdoptExecutorFleet(fleet, programDriver, programP)
	if !adopted || programHandle.Route != 1 || programHandle.Executor != programExecutor ||
		programDriver.registry != programRegistry {
		t.Fatalf("adopt existing program domain = (%+v, %t)", programHandle, adopted)
	}
	peer := bindExecutorFleetManualFixture(t, fleet)
	if peer.handle.Route != 2 || peer.driver.registry != &fleet.executors ||
		peer.driver.registry == programDriver.registry {
		t.Fatalf("owned peer after adopted program = %+v", peer.handle)
	}

	state, ticket, ids := reserveManualWaitSet(t, programManual, programP, 721, []uint32{1})
	posted := fleet.PostManualAndRequest(ids[0])
	if posted.Route != OperationRoutePosted || posted.Executor != ExecutorRequestPublished ||
		!programRegistry.ObserveRequested(programExecutor) || fleet.executors.ObserveRequested(peer.handle.Executor) {
		t.Fatalf("adopted program route post = %+v", posted)
	}
	programFixture := &executorFleetManualFixture{
		p: programP, driver: programDriver, manual: programManual, handle: programHandle,
	}
	settleExecutorFleetManual(t, programFixture, state, ticket, ids)
	sink := new(P)
	mailbox := new(RunnableTransferMailbox)
	if !BindRunnableTransferMailbox(mailbox, sink) {
		t.Fatal("bind adopted program cleanup mailbox")
	}
	transfer, published := PublishPNeutralRunnable(mailbox, programP, queued.g)
	if !published || !transfer.Valid() {
		t.Fatalf("publish adopted program ready task = (%+v, %t)", transfer, published)
	}
	if moved, more, drainOK := DrainPNeutralRunnables(mailbox, sink, 1); !drainOK || moved != 1 || more {
		t.Fatalf("drain adopted program ready task = (%d, %t, %t)", moved, more, drainOK)
	}
	if runnable, nextOK := NextRunnable(sink); !nextOK || runnable != queued.g {
		t.Fatalf("adopted program cleanup task = (%p, %t)", runnable, nextOK)
	}
	action := beginWaitTestResume(t, sink, queued)
	finishWaitTestTask(t, sink, queued, action)
	if !TerminalG(sink, queued.g) {
		t.Fatal("adopted program ready task retained scheduler state")
	}
	closeExecutorFleetFixture(t, fleet, programFixture)
	closeExecutorFleetFixture(t, fleet, peer)
	if !programRegistry.CanRelease() || !fleet.AllRetired() {
		t.Fatal("adopted program or owned peer retained executor resources")
	}
}

func TestExecutorFleetAdoptedProgramCanFinishAuthoritativeExternalClose(t *testing.T) {
	fleet := new(ExecutorFleet)
	p := new(P)
	driver, registry, manual, executor := bindTestExecutorDriverWithManual(t, p)
	handle, adopted := AdoptExecutorFleet(fleet, driver, p)
	if !adopted || handle.Executor != executor || handle.Route != 1 {
		t.Fatalf("adopt external-close program = (%+v,%t)", handle, adopted)
	}
	if !BeginExecutorClose(driver) {
		t.Fatal("begin authoritative adopted driver close")
	}
	if !BeginExecutorFleetClose(fleet, handle) {
		t.Fatal("begin adopted fleet route close")
	}
	if !ConfirmExecutorFleetRouteClose(fleet, handle) {
		t.Fatal("confirm adopted fleet route close")
	}
	if !BeginExecutorFleetExternalDriverClose(fleet, handle) {
		t.Fatal("record authoritative adopted driver close")
	}
	if ConfirmExecutorFleetExternalClose(fleet, handle) {
		t.Fatal("fleet confirmed adopted close before authoritative driver")
	}
	if !ConfirmExecutorClose(driver) || !ConfirmExecutorFleetExternalClose(fleet, handle) ||
		!fleet.AllRetired() || !registry.CanRelease() || !manual.CanRelease() {
		t.Fatal("finish adopted authoritative external close")
	}
}

func TestExecutorFleetBindsTimerAndRoutesPollSources(t *testing.T) {
	t.Run("timer-owner-source", func(t *testing.T) {
		fleet := new(ExecutorFleet)
		p, driver := new(P), new(ExecutorDriver)
		timers := new(TimerRegistrationTable)
		handle, ok := BindExecutorFleet(fleet, driver, p, ExecutorSourceCatalog{
			Timers: timers,
		})
		if !ok || handle.Route != 1 {
			t.Fatalf("bind timer fleet route = (%+v, %t)", handle, ok)
		}
		if route, routeOK := timers.Route(); !routeOK || route != RouteID(handle.Route) {
			t.Fatalf("timer fleet source route = (%d, %t), want %d", route, routeOK, handle.Route)
		}
		route := RouteID(handle.Route)
		if first := fleet.RequestTimerExecutor(route); first != ExecutorRequestPublished {
			t.Fatalf("first timer generation request = %d", first)
		}
		if second := fleet.RequestTimerExecutor(route); second != ExecutorRequestCoalesced {
			t.Fatalf("coalesced timer generation request = %d", second)
		}
		if timersDone, promoted, pollOK := PollExecutorAt(driver, 0); !pollOK || timersDone != 0 || promoted != 0 {
			t.Fatalf("service timer generation request = (%d, %d, %t)", timersDone, promoted, pollOK)
		}
		if !BeginExecutorFleetClose(fleet, handle) || !ConfirmExecutorFleetRouteClose(fleet, handle) {
			t.Fatal("strong-close timer request route")
		}
		if late := fleet.RequestTimerExecutor(route); late != ExecutorRequestClosed {
			t.Fatalf("closed timer route request = %d", late)
		}
		if !BeginExecutorFleetDriverClose(fleet, handle) || !ConfirmExecutorFleetClose(fleet, handle) ||
			!fleet.AllRetired() || !timers.CanRelease() {
			t.Fatal("timer fleet route retained source or executor state")
		}
	})

	t.Run("poll-producer-route", func(t *testing.T) {
		fleet := new(ExecutorFleet)
		p, driver := new(P), new(ExecutorDriver)
		poll := new(PollOperationSource)
		handle, ok := BindExecutorFleet(fleet, driver, p, ExecutorSourceCatalog{
			Poll: poll,
		})
		if !ok || handle.Route != 1 {
			t.Fatalf("bind poll fleet route = (%+v, %t)", handle, ok)
		}

		task := newYieldingTestG(t, "fleet-routed-poll")
		peer := newYieldingTestG(t, "fleet-routed-poll-peer")
		if !Enqueue(p, task.g) || !Enqueue(p, peer.g) {
			t.Fatal("enqueue routed poll tasks")
		}
		if runnable, nextOK := NextRunnableAt(p, 0); !nextOK || runnable != task.g {
			t.Fatalf("dequeue routed poll task = (%p, %t)", runnable, nextOK)
		}
		action := beginWaitTestResume(t, p, task)
		ticket, ok := BeginParkSet(&task.g.park, 1, 811)
		wait := new(WaitSetRecord)
		if !ok || !PrepareWaitSetRecord(wait, task.g, ticket) {
			t.Fatal("begin routed poll park set")
		}
		pollHandle, id, ok := poll.ReserveAndAttachPollOperationV2(
			p, &task.g.park, ticket, wait, 17, 42, PollInterestRead, 0,
		)
		if !ok || id.Route() != RouteID(handle.Route) || !SealParkSet(&task.g.park, ticket) {
			t.Fatalf("reserve routed poll operation = (%+v, %+v, %t)", pollHandle, id, ok)
		}
		task.frame.header.SuspendReason = uint16(SuspendPark)
		task.frame.header.Lifecycle = uint16(FrameSuspended)
		if !PrepareParkSet(task.g, task.handle, task.frame.header, ticket, wait) {
			t.Fatal("prepare routed poll scheduler park")
		}
		parked, resumed := Resumed(p, task.g, action)
		if !resumed || parked.Kind != ActionPark {
			t.Fatalf("commit routed poll park = (%+v, %t)", parked, resumed)
		}
		posted := fleet.PostPollAndRequest(id, PollOperationReady)
		duplicate := fleet.PostPollAndRequest(id, PollOperationClosing)
		if posted.Route != OperationRoutePosted || posted.Executor != ExecutorRequestPublished ||
			duplicate.Route != OperationRoutePostCoalesced || duplicate.Executor != ExecutorRequestInvalid {
			t.Fatalf("routed poll posts = first %+v duplicate %+v", posted, duplicate)
		}
		if !BeginExecutorFleetClose(fleet, handle) || !ConfirmExecutorFleetRouteClose(fleet, handle) {
			t.Fatal("strong-close routed poll producer ingress")
		}
		if late := fleet.PostPollAndRequest(id, PollOperationReady); late.Route != OperationRoutePostClosed {
			t.Fatalf("closed poll route accepted late result: %+v", late)
		}
		if timersDone, promoted, pollOK := PollExecutorAt(driver, 0); !pollOK || timersDone != 0 || promoted != 1 {
			t.Fatalf("service routed poll = (%d, %d, %t)", timersDone, promoted, pollOK)
		}
		if runnable, nextOK := NextRunnableAt(p, 0); !nextOK || runnable != peer.g || !Enqueue(p, peer.g) {
			t.Fatalf("rotate routed poll peer = (%p, %t)", runnable, nextOK)
		}
		if runnable, nextOK := NextRunnableAt(p, 0); !nextOK || runnable != task.g {
			t.Fatalf("dequeue completed routed poll task = (%p, %t)", runnable, nextOK)
		}
		action = beginWaitTestResume(t, p, task)
		outcome, caseID, lease, taskCancel, consumed := TakeRunDecision(task.g, ticket)
		result, taken := poll.TakePollOperationV2Result(p, pollHandle, lease)
		if !consumed || outcome != ParkOutcomeCompleted || caseID != 17 || taskCancel != TaskCancelNone ||
			!taken || result != PollOperationReady || !poll.RecyclePollOperationV2(p, pollHandle) {
			t.Fatalf("consume routed poll = outcome:%d case:%d result:%d taken:%t consumed:%t cancel:%d lease:%+v",
				outcome, caseID, result, taken, consumed, taskCancel, lease)
		}
		finishWaitTestTask(t, p, task, action)
		var sink P
		var mailbox RunnableTransferMailbox
		if !BindRunnableTransferMailbox(&mailbox, &sink) {
			t.Fatal("bind routed poll cleanup mailbox")
		}
		if id, published := PublishPNeutralRunnable(&mailbox, p, peer.g); !published || !id.Valid() {
			t.Fatalf("publish routed poll peer cleanup = (%+v, %t)", id, published)
		}
		if moved, more, drainOK := DrainPNeutralRunnables(&mailbox, &sink, 1); !drainOK || moved != 1 || more {
			t.Fatalf("drain routed poll peer cleanup = (%d, %t, %t)", moved, more, drainOK)
		}
		if !BeginExecutorFleetDriverClose(fleet, handle) || !ConfirmExecutorFleetClose(fleet, handle) ||
			!fleet.AllRetired() || !poll.CanRelease() {
			t.Fatal("poll fleet route retained source or executor state")
		}
		if runnable, nextOK := NextRunnable(&sink); !nextOK || runnable != peer.g {
			t.Fatalf("dequeue routed poll cleanup peer = (%p, %t)", runnable, nextOK)
		}
		peerAction := beginWaitTestResume(t, &sink, peer)
		finishWaitTestTask(t, &sink, peer, peerAction)
		if !TerminalG(&sink, peer.g) {
			t.Fatal("routed poll cleanup peer retained scheduler state")
		}
	})
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

func TestExecutorFleetDemandDistributesSurplusWithoutBouncingLastRunnable(t *testing.T) {
	fleet := new(ExecutorFleet)
	source := bindExecutorFleetManualFixture(t, fleet)
	first := bindExecutorFleetManualFixture(t, fleet)
	second := bindExecutorFleetManualFixture(t, fleet)
	tasks := [3]*yieldingTestG{
		newYieldingTestG(t, "fleet-demand-first"),
		newYieldingTestG(t, "fleet-demand-second"),
		newYieldingTestG(t, "fleet-demand-local"),
	}
	for _, task := range tasks {
		scratch := new(P)
		yieldRunnableForTransfer(t, scratch, task)
		runnable, ok := NextRunnable(scratch)
		if !ok || runnable != task.g {
			t.Fatal("detach yielded global distribution candidate")
		}
		if !Enqueue(source.p, task.g) {
			t.Fatal("enqueue global distribution source")
		}
	}
	if !fleet.RequestPNeutralRunnable(first.handle, first.p) ||
		!fleet.RequestPNeutralRunnable(second.handle, second.p) {
		t.Fatal("publish two exact runnable demands")
	}
	firstDistribution, ok := fleet.DistributePNeutralRunnable(source.handle, source.p)
	if !ok || !firstDistribution.Valid() || firstDistribution.Target != first.handle ||
		firstDistribution.Count != 1 ||
		source.p.readyHead != tasks[1].g {
		t.Fatalf("first demand distribution = %+v, ok=%t head=%p",
			firstDistribution, ok, source.p.readyHead)
	}
	secondDistribution, ok := fleet.DistributePNeutralRunnable(source.handle, source.p)
	if !ok || !secondDistribution.Valid() || secondDistribution.Target != second.handle ||
		secondDistribution.Count != 1 ||
		source.p.readyHead != tasks[2].g || source.p.readyTail != tasks[2].g {
		t.Fatalf("second demand distribution = %+v, ok=%t source=(%p,%p)",
			secondDistribution, ok, source.p.readyHead, source.p.readyTail)
	}
	if !fleet.ImportPNeutralRunnable(
		first.handle,
		first.p,
		firstDistribution.Transfer,
	) || !fleet.ImportPNeutralRunnable(
		second.handle,
		second.p,
		secondDistribution.Transfer,
	) {
		t.Fatal("import demanded runnable transfers")
	}
	for index, fixture := range [3]*executorFleetManualFixture{first, second, source} {
		if index < 2 {
			if _, _, pollOK := PollExecutor(fixture.driver); !pollOK {
				t.Fatalf("acknowledge demanded executor %d", index)
			}
		}
		runnable, nextOK := NextRunnable(fixture.p)
		if !nextOK || runnable != tasks[index].g {
			t.Fatalf("dequeue distributed runnable %d = (%p,%t)", index, runnable, nextOK)
		}
		action := beginWaitTestResume(t, fixture.p, tasks[index])
		finishExecutorFleetTask(t, fixture, tasks[index], action)
	}
	closeExecutorFleetFixture(t, fleet, source)
	closeExecutorFleetFixture(t, fleet, first)
	closeExecutorFleetFixture(t, fleet, second)
	if !fleet.AllRetired() {
		t.Fatal("demand distribution retained fleet resources")
	}
}

func TestExecutorFleetDemandSharesSingleInitialButNotSingleYielded(t *testing.T) {
	t.Run("initial", func(t *testing.T) {
		fleet := new(ExecutorFleet)
		source := bindExecutorFleetManualFixture(t, fleet)
		target := bindExecutorFleetManualFixture(t, fleet)
		task := newYieldingTestG(t, "fleet-single-initial")
		if !Enqueue(source.p, task.g) {
			t.Fatal("prepare single initial demand")
		}
		distribution, ok := fleet.DistributePNeutralRunnable(source.handle, source.p)
		if !ok || !distribution.Valid() || distribution.Target != target.handle ||
			distribution.Count != 1 ||
			source.p.readyHead != nil || source.p.readyTail != nil {
			t.Fatalf("single initial distribution = %+v/%t source=(%p,%p)",
				distribution, ok, source.p.readyHead, source.p.readyTail)
		}
	})
	t.Run("yielded", func(t *testing.T) {
		fleet := new(ExecutorFleet)
		source := bindExecutorFleetManualFixture(t, fleet)
		target := bindExecutorFleetManualFixture(t, fleet)
		task := newYieldingTestG(t, "fleet-single-yielded")
		yieldRunnableForTransfer(t, source.p, task)
		if !fleet.RequestPNeutralRunnable(target.handle, target.p) {
			t.Fatal("prepare single yielded demand")
		}
		distribution, ok := fleet.DistributePNeutralRunnable(source.handle, source.p)
		if !ok || distribution != (RunnableDistribution{}) ||
			source.p.readyHead != task.g || source.p.readyTail != task.g || !task.g.queued {
			t.Fatalf("single yielded bounced = %+v/%t source=(%p,%p) queued=%t",
				distribution, ok, source.p.readyHead, source.p.readyTail, task.g.queued)
		}
		if inflight, cancelOK := fleet.CancelPNeutralRunnableRequest(target.handle, target.p); !cancelOK || inflight {
			t.Fatalf("cancel yielded demand = (%t,%t)", inflight, cancelOK)
		}
	})
}

func TestExecutorFleetPreferredMaterializedRunnableRequiresExactDemand(t *testing.T) {
	fleet := new(ExecutorFleet)
	source := bindExecutorFleetManualFixture(t, fleet)
	target := bindExecutorFleetManualFixture(t, fleet)
	task := newYieldingTestG(t, "fleet-preferred-materialized")
	enqueueMaterializedFleetRunnable(t, source.p, task, RouteID(target.handle.Route))

	if distribution, ok := fleet.DistributeMaterializedRunnableToPreferredRoute(
		source.handle,
		source.p,
	); !ok || distribution != (RunnableDistribution{}) || source.p.readyHead != task.g {
		t.Fatalf("preferred runnable moved without demand = %+v/%t head=%p",
			distribution, ok, source.p.readyHead)
	}
	if !fleet.RequestPNeutralRunnable(target.handle, target.p) {
		t.Fatal("request preferred materialized runnable")
	}
	distribution, ok := fleet.DistributeMaterializedRunnableToPreferredRoute(
		source.handle,
		source.p,
	)
	if !ok || !distribution.Valid() || distribution.Target != target.handle ||
		distribution.Count != 1 || source.p.readyHead != nil || source.p.readyTail != nil ||
		preemptLoad(&fleet.slots[target.handle.Route-1].runnableDemand) != uint32(runnableDemandIdle) {
		t.Fatalf("preferred materialized distribution = %+v/%t source=(%p,%p) demand=%d",
			distribution, ok, source.p.readyHead, source.p.readyTail,
			preemptLoad(&fleet.slots[target.handle.Route-1].runnableDemand))
	}
	if !fleet.ImportPNeutralRunnable(target.handle, target.p, distribution.Transfer) ||
		target.p.readyHead != task.g || target.p.readyTail != task.g {
		t.Fatalf("import preferred materialized runnable = target=(%p,%p)",
			target.p.readyHead, target.p.readyTail)
	}
	if preferred, valid := MaterializedRunnablePreferredRoute(task.g); !valid ||
		preferred != RouteID(target.handle.Route) {
		t.Fatalf("import lost preferred materialized route = (%d,%t)", preferred, valid)
	}
}

func TestExecutorFleetPreferredMaterializedRunnableSettlesSourceReadyDebt(t *testing.T) {
	fleet := new(ExecutorFleet)
	source := bindExecutorFleetManualFixture(t, fleet)
	target := bindExecutorFleetManualFixture(t, fleet)
	task := newYieldingTestG(t, "fleet-preferred-ready-debt")
	enqueueMaterializedFleetRunnable(t, source.p, task, RouteID(target.handle.Route))
	// This is the exact stable cursor produced by a completed source epoch
	// which promoted the materialized continuation before returning to the
	// target distribution hook.
	source.driver.run.readyDebt = true
	if !validExecutorDriver(source.driver) {
		t.Fatal("prepare source ready debt")
	}
	if !fleet.RequestPNeutralRunnable(target.handle, target.p) {
		t.Fatal("request preferred ready-debt route")
	}
	distribution, ok := fleet.DistributeMaterializedRunnableToPreferredRoute(
		source.handle,
		source.p,
	)
	if !ok || !distribution.Valid() || validExecutorDriver(source.driver) {
		t.Fatalf("transfer did not expose stale source debt = %+v/%t valid=%t",
			distribution, ok, validExecutorDriver(source.driver))
	}
	if !CommitExecutorRunSourceDistribution(source.driver, true) ||
		source.driver.run.readyDebt || !validExecutorDriver(source.driver) {
		t.Fatalf("settle source ready debt = cursor:%+v valid:%t",
			source.driver.run, validExecutorDriver(source.driver))
	}
	if !fleet.ImportPNeutralRunnable(target.handle, target.p, distribution.Transfer) ||
		target.p.readyHead != task.g {
		t.Fatal("import preferred ready-debt runnable")
	}
}

func TestExecutorFleetPreferredMaterializedRunnableNeverScansAnotherDemand(t *testing.T) {
	fleet := new(ExecutorFleet)
	source := bindExecutorFleetManualFixture(t, fleet)
	preferred := bindExecutorFleetManualFixture(t, fleet)
	other := bindExecutorFleetManualFixture(t, fleet)
	task := newYieldingTestG(t, "fleet-exact-preferred-materialized")
	enqueueMaterializedFleetRunnable(t, source.p, task, RouteID(preferred.handle.Route))
	if !fleet.RequestPNeutralRunnable(other.handle, other.p) {
		t.Fatal("request non-preferred materialized route")
	}
	if distribution, ok := fleet.DistributeMaterializedRunnableToPreferredRoute(
		source.handle,
		source.p,
	); !ok || distribution != (RunnableDistribution{}) || source.p.readyHead != task.g {
		t.Fatalf("preferred runnable scanned another demand = %+v/%t head=%p",
			distribution, ok, source.p.readyHead)
	}
	if inflight, ok := fleet.CancelPNeutralRunnableRequest(other.handle, other.p); !ok || inflight {
		t.Fatalf("cancel non-preferred demand = (%t,%t)", inflight, ok)
	}
}

func TestExecutorFleetDemandDistributesBoundedHalfBatch(t *testing.T) {
	fleet := new(ExecutorFleet)
	source := bindExecutorFleetManualFixture(t, fleet)
	target := bindExecutorFleetManualFixture(t, fleet)
	tasks := make([]*yieldingTestG, 8)
	for index := range tasks {
		tasks[index] = newYieldingTestG(t, "fleet-demand-batch")
		scratch := new(P)
		yieldRunnableForTransfer(t, scratch, tasks[index])
		runnable, ok := NextRunnable(scratch)
		if !ok || runnable != tasks[index].g {
			t.Fatalf("detach fleet batch task %d", index)
		}
		if !Enqueue(source.p, tasks[index].g) {
			t.Fatalf("enqueue fleet batch task %d", index)
		}
	}
	if !fleet.RequestPNeutralRunnable(target.handle, target.p) {
		t.Fatal("request fleet batch")
	}
	distribution, ok := fleet.DistributePNeutralRunnable(source.handle, source.p)
	if !ok || !distribution.Valid() || distribution.Target != target.handle ||
		distribution.Count != 4 || source.p.readyCount != 4 ||
		source.p.readyHead != tasks[4].g || source.p.readyTail != tasks[7].g {
		t.Fatalf("fleet batch distribution = %+v/%t source:%d (%p,%p)",
			distribution, ok, source.p.readyCount, source.p.readyHead, source.p.readyTail)
	}
	moved, more, status := fleet.TryDrainPNeutralRunnables(
		target.handle,
		target.p,
		RunnableTransferMailboxCapacity,
	)
	if status != RunnableTransferDrainComplete || more || moved != distribution.Count ||
		target.p.readyCount != distribution.Count ||
		target.p.readyHead != tasks[0].g || target.p.readyTail != tasks[3].g {
		t.Fatalf("fleet batch import = (%d,%t,%d), target:%d (%p,%p)",
			moved, more, status, target.p.readyCount, target.p.readyHead, target.p.readyTail)
	}
}

func TestExecutorFleetBatchPreparationDoesNotAllocate(t *testing.T) {
	fleet := new(ExecutorFleet)
	source := bindExecutorFleetManualFixture(t, fleet)
	for index := 0; index < int(RunnableTransferMailboxCapacity); index++ {
		task := newYieldingTestG(t, "fleet-batch-allocation")
		scratch := new(P)
		yieldRunnableForTransfer(t, scratch, task)
		runnable, ok := NextRunnable(scratch)
		if !ok || runnable != task.g || !Enqueue(source.p, task.g) {
			t.Fatalf("prepare allocation task %d", index)
		}
	}
	failed := false
	allocations := testing.AllocsPerRun(100, func() {
		distribution, ok := fleet.DistributePNeutralRunnable(source.handle, source.p)
		if !ok || distribution != (RunnableDistribution{}) {
			failed = true
		}
	})
	if failed || allocations != 0 || source.p.readyCount != RunnableTransferMailboxCapacity {
		t.Fatalf("batch preparation = failed:%t allocations:%f count:%d",
			failed, allocations, source.p.readyCount)
	}
}

func TestExecutorFleetConcurrentSourcesClaimOneRunnableDemand(t *testing.T) {
	fleet := new(ExecutorFleet)
	first := bindExecutorFleetManualFixture(t, fleet)
	second := bindExecutorFleetManualFixture(t, fleet)
	target := bindExecutorFleetManualFixture(t, fleet)
	for index, source := range [2]*executorFleetManualFixture{first, second} {
		yielded := newYieldingTestG(t, "fleet-concurrent-demand-yielded")
		yieldRunnableForTransfer(t, source.p, yielded)
		initial := newYieldingTestG(t, "fleet-concurrent-demand-initial")
		if !Enqueue(source.p, initial.g) {
			t.Fatalf("enqueue concurrent source %d surplus", index)
		}
	}
	if !fleet.RequestPNeutralRunnable(target.handle, target.p) {
		t.Fatal("publish concurrent runnable demand")
	}
	type result struct {
		distribution RunnableDistribution
		ok           bool
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var group sync.WaitGroup
	for _, source := range [2]*executorFleetManualFixture{first, second} {
		group.Add(1)
		go func(source *executorFleetManualFixture) {
			defer group.Done()
			<-start
			distribution, ok := fleet.DistributePNeutralRunnable(source.handle, source.p)
			results <- result{distribution: distribution, ok: ok}
		}(source)
	}
	close(start)
	group.Wait()
	close(results)
	published := 0
	for result := range results {
		if !result.ok {
			t.Fatal("concurrent demand distribution broke a fleet invariant")
		}
		if result.distribution.Valid() {
			published++
			if result.distribution.Target != target.handle {
				t.Fatalf("concurrent demand selected wrong target %+v", result.distribution)
			}
		} else if result.distribution != (RunnableDistribution{}) {
			t.Fatalf("concurrent demand returned partial result %+v", result.distribution)
		}
	}
	if published != 1 || preemptLoad(&fleet.slots[target.handle.Route-1].runnableDemand) !=
		uint32(runnableDemandIdle) {
		t.Fatalf("concurrent demand publications/state = %d/%d", published,
			preemptLoad(&fleet.slots[target.handle.Route-1].runnableDemand))
	}
}

func TestExecutorFleetCloseCancelsDemandAndJoinsClaim(t *testing.T) {
	t.Run("requested", func(t *testing.T) {
		fleet := new(ExecutorFleet)
		target := bindExecutorFleetManualFixture(t, fleet)
		if !fleet.RequestPNeutralRunnable(target.handle, target.p) ||
			!BeginExecutorFleetClose(fleet, target.handle) ||
			preemptLoad(&fleet.slots[target.handle.Route-1].runnableDemand) != uint32(runnableDemandIdle) ||
			!ConfirmExecutorFleetRouteClose(fleet, target.handle) ||
			!BeginExecutorFleetDriverClose(fleet, target.handle) ||
			!ConfirmExecutorFleetClose(fleet, target.handle) || !fleet.AllRetired() {
			t.Fatal("requested demand survived fleet close")
		}
	})
	t.Run("claimed", func(t *testing.T) {
		fleet := new(ExecutorFleet)
		target := bindExecutorFleetManualFixture(t, fleet)
		slot := &fleet.slots[target.handle.Route-1]
		if !fleet.RequestPNeutralRunnable(target.handle, target.p) ||
			!preemptCompareAndSwap(
				&slot.runnableDemand,
				uint32(runnableDemandRequested),
				uint32(runnableDemandClaimed),
			) ||
			!BeginExecutorFleetClose(fleet, target.handle) {
			t.Fatal("prepare claimed demand close")
		}
		if ConfirmExecutorFleetRouteClose(fleet, target.handle) {
			t.Fatal("fleet close crossed an in-flight demand claim")
		}
		if !preemptCompareAndSwap(
			&slot.runnableDemand,
			uint32(runnableDemandClaimed),
			uint32(runnableDemandIdle),
		) || !ConfirmExecutorFleetRouteClose(fleet, target.handle) ||
			!BeginExecutorFleetDriverClose(fleet, target.handle) ||
			!ConfirmExecutorFleetClose(fleet, target.handle) || !fleet.AllRetired() {
			t.Fatal("fleet did not close after demand claimant quiesced")
		}
	})
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
		Manual: first.manual,
	}); ok || handle != (ExecutorFleetHandle{}) || fleet.routes.next != nextBefore {
		t.Fatalf("duplicate fleet bind mutated route allocation = (%+v, %t), next=%d", handle, ok, fleet.routes.next)
	}

	foreignRegistry := new(ExecutorRegistry)
	foreignP := new(P)
	foreignDriver := new(ExecutorDriver)
	foreignManual := new(ManualOperationSource)
	foreignExecutor := registerTestExecutor(t, foreignRegistry)
	if !BindExecutorSourceCatalogAtRoute(foreignDriver, foreignP, foreignRegistry, foreignExecutor, 1,
		ExecutorSourceCatalog{Manual: foreignManual}) {
		t.Fatal("bind foreign-registry driver")
	}
	if handle, ok := BindExecutorFleet(fleet, foreignDriver, foreignP, ExecutorSourceCatalog{
		Manual: foreignManual,
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
		Manual: wrongRouteSource,
	}); ok || handle != (ExecutorFleetHandle{}) || fleet.routes.next != nextBefore || !wrongRouteSource.CanRelease() {
		t.Fatal("wrong-route catalog entered fleet bind transaction")
	}
	if handle, ok := BindExecutorFleet(fleet, new(ExecutorDriver), new(P), ExecutorSourceCatalog{}); ok || handle != (ExecutorFleetHandle{}) || fleet.routes.next != nextBefore {
		t.Fatal("wait-only catalog consumed a fleet route")
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
		Manual: new(ManualOperationSource),
	}); ok || handle != (ExecutorFleetHandle{}) || fleet.routes.next != ExecutorFleetCapacity {
		t.Fatalf("fleet lifetime capacity exhaustion = (%+v, %t), next=%d", handle, ok, fleet.routes.next)
	}
	late := fleet.PostManualAndRequest(OperationID{})
	if !fleet.AllRetired() || late.Route != OperationRoutePostInvalid {
		t.Fatal("exhausted fleet retained resources or accepted invalid ingress")
	}
}
