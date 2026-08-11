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
	"testing"
	"unsafe"
)

func attachChannelOperationPageForTest(
	source *ChannelOperationSource,
	p *P,
	page *ChannelOperationPage,
) bool {
	if AttachChannelOperationPage(source, p, page, nil) {
		return true
	}
	return AttachChannelOperationPage(source, p, page, new(OperationPageDirectoryBlock))
}

type channelClaimCoreFixture struct {
	p        *P
	driver   *ExecutorDriver
	registry *ExecutorRegistry
	source   *ChannelOperationSource
	handle   ExecutorHandle
	task     *yieldingTestG
	wait     WaitSetRecord
	ticket   ParkTicket
	ids      []OperationID
	claim    *SelectClaim
}

func newChannelClaimCoreFixture(t *testing.T, name string, caseIDs []uint32, withClaim bool, defaultCase uint32) *channelClaimCoreFixture {
	return newChannelClaimCoreFixtureBeforeResume(t, name, caseIDs, withClaim, defaultCase, nil)
}

func newChannelClaimCoreFixtureBeforeResume(
	t *testing.T,
	name string,
	caseIDs []uint32,
	withClaim bool,
	defaultCase uint32,
	beforeResume func(*channelClaimCoreFixture),
) *channelClaimCoreFixture {
	t.Helper()
	return newChannelClaimCoreFixtureWithSourceBeforeResume(
		t, name, caseIDs, withClaim, defaultCase, new(ChannelOperationSource), beforeResume,
	)
}

func newChannelClaimCoreFixtureWithSourceBeforeResume(
	t *testing.T,
	name string,
	caseIDs []uint32,
	withClaim bool,
	defaultCase uint32,
	source *ChannelOperationSource,
	beforeResume func(*channelClaimCoreFixture),
) *channelClaimCoreFixture {
	return newChannelClaimCoreFixtureWithSourceHooks(
		t, name, caseIDs, withClaim, defaultCase, source, nil, beforeResume,
	)
}

func newChannelClaimCoreFixtureWithSourceHooks(
	t *testing.T,
	name string,
	caseIDs []uint32,
	withClaim bool,
	defaultCase uint32,
	source *ChannelOperationSource,
	afterBind func(*channelClaimCoreFixture),
	beforeResume func(*channelClaimCoreFixture),
) *channelClaimCoreFixture {
	t.Helper()
	fixture := &channelClaimCoreFixture{
		p:        new(P),
		driver:   new(ExecutorDriver),
		registry: new(ExecutorRegistry),
		source:   source,
	}
	fixture.handle = registerTestExecutor(t, fixture.registry)
	if !BindExecutorSourceCatalog(fixture.driver, fixture.p, fixture.registry, fixture.handle, ExecutorSourceCatalog{
		Channel: fixture.source,
	}) {
		t.Fatal("bind channel claim-core executor")
	}
	if afterBind != nil {
		afterBind(fixture)
	}
	fixture.task = newYieldingTestG(t, name)
	if !Enqueue(fixture.p, fixture.task.g) {
		t.Fatal("enqueue channel claim-core task")
	}
	if g, ok := NextRunnable(fixture.p); !ok || g != fixture.task.g {
		t.Fatal("dequeue channel claim-core task")
	}
	action := beginWaitTestResume(t, fixture.p, fixture.task)
	var ok bool
	if defaultCase == 0 {
		fixture.ticket, ok = BeginParkSet(&fixture.task.g.park, uint32(len(caseIDs)), 71)
	} else {
		fixture.ticket, ok = BeginParkSetWithDefault(&fixture.task.g.park, uint32(len(caseIDs)), 71, defaultCase)
	}
	if !ok || !PrepareWaitSetRecord(&fixture.wait, fixture.task.g, fixture.ticket) {
		t.Fatal("prepare channel claim-core wait-set")
	}
	if withClaim {
		fixture.claim = new(SelectClaim)
	}
	fixture.ids = make([]OperationID, len(caseIDs))
	for index, caseID := range caseIDs {
		id, reserved := fixture.source.ReserveAndAttachWait(
			fixture.p, &fixture.task.g.park, fixture.ticket, &fixture.wait, caseID, fixture.claim,
		)
		if !reserved {
			t.Fatalf("reserve channel candidate %d", index)
		}
		fixture.ids[index] = id
	}
	if !SealParkSet(&fixture.task.g.park, fixture.ticket) {
		t.Fatal("seal channel claim-core wait-set")
	}
	fixture.task.frame.header.SuspendReason = uint16(SuspendPark)
	fixture.task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(fixture.task.g, fixture.task.handle, fixture.task.frame.header, fixture.ticket, &fixture.wait) {
		t.Fatal("prepare channel claim-core park")
	}
	if fixture.claim != nil {
		for index, id := range fixture.ids {
			if !fixture.source.ExposeExternalCommit(
				fixture.p, fixture.task.g, id, fixture.ticket, &fixture.wait, fixture.claim,
			) {
				t.Fatalf("expose channel candidate %d", index)
			}
		}
	}
	if beforeResume != nil {
		beforeResume(fixture)
	}
	if parked, resumed := Resumed(fixture.p, fixture.task.g, action); !resumed || parked.Kind != ActionPark {
		t.Fatalf("commit channel claim-core park = (%+v, %t)", parked, resumed)
	}
	return fixture
}

func TestChannelOperationPagedCatalogSelectCompletesOneThousand(t *testing.T) {
	const count = 1000
	source := new(ChannelOperationSource)
	var pages [15]ChannelOperationPage
	if ChannelOperationConfiguredCapacity(source) != ChannelOperationPageCapacity ||
		!ConfigureChannelOperationPages(source, pages[:1]) ||
		ChannelOperationConfiguredCapacity(source) != 2*ChannelOperationPageCapacity ||
		!ConfigureChannelOperationPages(source, pages[:]) ||
		ChannelOperationConfiguredCapacity(source) != 16*ChannelOperationPageCapacity ||
		!ConfigureChannelOperationPages(source, pages[:]) {
		t.Fatal("configure paged channel catalog")
	}
	caseIDs := make([]uint32, count)
	for index := range caseIDs {
		caseIDs[index] = uint32(index + 1)
	}
	fixture := newChannelClaimCoreFixtureWithSourceBeforeResume(
		t, "channel-paged-thousand", caseIDs, true, 0, source, nil,
	)
	if fixture.ids[ChannelOperationPageCapacity].LocalSlot() != ChannelOperationPageCapacity+1 ||
		fixture.ids[count-1].LocalSlot() != count {
		t.Fatalf("paged channel IDs did not remain linear: page2=%+v last=%+v",
			fixture.ids[ChannelOperationPageCapacity], fixture.ids[count-1])
	}
	var replacement [15]ChannelOperationPage
	if ConfigureChannelOperationPages(source, pages[:]) ||
		ConfigureChannelOperationPages(source, replacement[:]) ||
		ConfigureChannelOperationPages(source, pages[:14]) {
		t.Fatal("bound paged channel catalog allowed reconfiguration")
	}
	for index, id := range fixture.ids {
		if result := source.PostReady(id); result != ChannelOperationPosted {
			t.Fatalf("post paged channel operation %d = %d", index, result)
		}
	}
	requestChannelClaimCoreFixture(t, fixture)
	progress := pollChannelClaimCoreComplete(t, fixture)
	if progress.Completed != count || progress.ApplyVisits != count || progress.Promoted != 1 {
		t.Fatalf("resolve paged channel operations = %+v", progress)
	}
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID == 0 ||
		!decision.lease.Valid() || decision.taskCancel != TaskCancelNone {
		t.Fatalf("take paged channel decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
	if !ConfigureChannelOperationPages(source, pages[:]) {
		t.Fatal("released paged channel catalog was not reusable")
	}
}

func TestChannelOperationConfiguredCapacityPreflightsSelectBeyondFourCases(t *testing.T) {
	source := new(ChannelOperationSource)
	var pages [15]ChannelOperationPage
	if !ConfigureChannelOperationPages(source, pages[:]) {
		t.Fatal("configure channel select capacity")
	}
	p := new(P)
	if !BindChannelOperationSource(source, p) {
		t.Fatal("bind configured channel source")
	}
	if !CanReserveChannelOperations(p, source, 5) ||
		!CanReserveChannelOperations(p, source, 1000) ||
		CanReserveChannelOperations(p, source, 1025) {
		t.Fatalf("configured channel select preflight: capacity=%d", ChannelOperationConfiguredCapacity(source))
	}
	dynamic := new(ChannelOperationPage)
	if !attachChannelOperationPageForTest(source, p, dynamic) ||
		ChannelOperationConfiguredCapacity(source) != 17*ChannelOperationPageCapacity ||
		!CanReserveChannelOperations(p, source, 1025) ||
		AttachChannelOperationPage(source, p, dynamic, nil) ||
		AttachChannelOperationPage(source, p, &pages[0], nil) {
		t.Fatalf("dynamic channel page publication: capacity=%d", ChannelOperationConfiguredCapacity(source))
	}
	if !UnbindChannelOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release configured channel source")
	}
	if !ConfigureChannelOperationPages(source, pages[:]) ||
		ConfigureChannelOperationPages(source, pages[:14]) {
		t.Fatal("dynamic channel catalog allowed static local-slot remapping")
	}
}

func TestChannelOperationDynamicCatalogCompletesBeyondStaticProfile(t *testing.T) {
	const count = 1025
	source := new(ChannelOperationSource)
	var pages [15]ChannelOperationPage
	if !ConfigureChannelOperationPages(source, pages[:]) {
		t.Fatal("configure static channel profile")
	}
	caseIDs := make([]uint32, count)
	for index := range caseIDs {
		caseIDs[index] = uint32(index + 1)
	}
	fixture := newChannelClaimCoreFixtureWithSourceHooks(
		t, "channel-dynamic-profile", caseIDs, true, 0, source,
		func(fixture *channelClaimCoreFixture) {
			if !attachChannelOperationPageForTest(source, fixture.p, new(ChannelOperationPage)) {
				t.Fatal("attach dynamic channel page")
			}
		},
		nil,
	)
	if first := fixture.ids[0]; first.LocalSlot() != count {
		t.Fatalf("first dynamic channel local slot = %d, want %d", first.LocalSlot(), count)
	}
	for index, id := range fixture.ids {
		if result := source.PostReady(id); result != ChannelOperationPosted {
			t.Fatalf("post dynamic channel operation %d = %d", index, result)
		}
	}
	requestChannelClaimCoreFixture(t, fixture)
	progress := pollChannelClaimCoreComplete(t, fixture)
	if progress.Completed != count || progress.ApplyVisits != count || progress.Promoted != 1 {
		t.Fatalf("resolve dynamic channel operations = %+v", progress)
	}
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID == 0 ||
		!decision.lease.Valid() || decision.taskCancel != TaskCancelNone {
		t.Fatalf("take dynamic channel decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelReadyIndexSkipsLargeEmptyActivePrefix(t *testing.T) {
	source := new(ChannelOperationSource)
	var pages [15]ChannelOperationPage
	if !ConfigureChannelOperationPages(source, pages[:]) {
		t.Fatal("configure indexed channel profile")
	}
	fixture := newChannelClaimCoreFixtureWithSourceHooks(
		t, "channel-ready-index", []uint32{1}, true, 0, source,
		func(fixture *channelClaimCoreFixture) {
			if !attachChannelOperationPageForTest(source, fixture.p, new(ChannelOperationPage)) {
				t.Fatal("attach indexed channel page")
			}
		},
		nil,
	)
	id := fixture.ids[0]
	if id.LocalSlot() != 1025 || source.PostReady(id) != ChannelOperationPosted {
		t.Fatalf("prepare high indexed channel operation %+v", id)
	}
	requestChannelClaimCoreFixture(t, fixture)
	const budget = 32
	progress, ok := PollExecutorSlice(fixture.driver, budget)
	if !ok || !progress.Complete || progress.Used >= budget ||
		progress.Completed != 1 || progress.ApplyVisits != 1 || progress.Promoted != 1 {
		t.Fatalf("indexed high-slot progress = (%+v, %t)", progress, ok)
	}
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 1 ||
		!decision.lease.Valid() || decision.taskCancel != TaskCancelNone {
		t.Fatalf("take indexed high-slot decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelOperationDynamicPagePublicationIsProducerSafe(t *testing.T) {
	if ChannelOperationMaximumCapacity != 511*ChannelOperationPageCapacity {
		t.Fatalf("channel maximum capacity = %d", ChannelOperationMaximumCapacity)
	}
	source := new(ChannelOperationSource)
	p := new(P)
	if !BindChannelOperationSource(source, p) {
		t.Fatal("bind dynamic channel source")
	}
	stop := make(chan struct{})
	failed := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			capacity := ChannelOperationConfiguredCapacity(source)
			slot, ok := channelOperationSlotAt(source, capacity-1)
			if capacity < ChannelOperationPageCapacity || !ok || slot == nil {
				select {
				case failed <- "producer observed a partial dynamic page":
				default:
				}
				return
			}
			runtime.Gosched()
		}
	}()
	for page := 0; page < 96; page++ {
		if !attachChannelOperationPageForTest(source, p, new(ChannelOperationPage)) {
			t.Fatalf("attach dynamic channel page %d", page)
		}
		runtime.Gosched()
	}
	close(stop)
	<-done
	select {
	case message := <-failed:
		t.Fatal(message)
	default:
	}
	if got := ChannelOperationConfiguredCapacity(source); got != 97*ChannelOperationPageCapacity {
		t.Fatalf("dynamic channel capacity = %d", got)
	}
	if !UnbindChannelOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release dynamically grown channel source")
	}
}

func requestChannelClaimCoreFixture(t *testing.T, fixture *channelClaimCoreFixture) {
	t.Helper()
	result := fixture.registry.Request(fixture.handle)
	if result != ExecutorRequestPublished && result != ExecutorRequestCoalesced {
		t.Fatalf("request channel claim-core executor = %d", result)
	}
}

func pollChannelClaimCoreComplete(t *testing.T, fixture *channelClaimCoreFixture) ExecutorPollProgress {
	t.Helper()
	for step := 0; step < 10000; step++ {
		progress, ok := PollExecutorSlice(fixture.driver, 1)
		if !ok {
			t.Fatalf("poll channel claim-core step %d", step)
		}
		if progress.Complete {
			return progress
		}
	}
	t.Fatal("channel claim-core poll did not complete")
	return ExecutorPollProgress{}
}

type channelClaimCoreDecision struct {
	action     Action
	outcome    ParkOutcome
	caseID     uint32
	lease      OperationResultLease
	taskCancel TaskCancelKind
}

func takeChannelClaimCoreDecision(t *testing.T, fixture *channelClaimCoreFixture) channelClaimCoreDecision {
	t.Helper()
	if g, ok := NextRunnable(fixture.p); !ok || g != fixture.task.g {
		t.Fatal("dequeue resolved channel claim-core task")
	}
	action := beginWaitTestResume(t, fixture.p, fixture.task)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(fixture.task.g, fixture.ticket)
	if !ok {
		t.Fatal("take channel claim-core run decision")
	}
	return channelClaimCoreDecision{action: action, outcome: outcome, caseID: caseID, lease: lease, taskCancel: taskCancel}
}

func releaseChannelClaimCoreFixture(t *testing.T, fixture *channelClaimCoreFixture, decision channelClaimCoreDecision) {
	t.Helper()
	for _, id := range fixture.ids {
		if !fixture.source.ConfirmQuiesced(fixture.p, id) {
			t.Fatalf("confirm channel operation quiesced: %+v", id)
		}
	}
	if fixture.claim != nil {
		if !fixture.source.ResetSelectClaim(fixture.p, fixture.claim) || selectClaimLoad(fixture.claim) != selectClaimOpen {
			t.Fatal("reset detached channel select claim")
		}
	}
	if decision.lease.Valid() && !fixture.source.TakeResult(fixture.p, decision.lease) {
		t.Fatal("take channel winner result")
	}
	for _, id := range fixture.ids {
		if !fixture.source.Recycle(fixture.p, id) {
			t.Fatalf("recycle channel operation: %+v", id)
		}
	}
	yieldRunningDriverTask(t, fixture.p, fixture.task, decision.action)
	closeTestExecutorDriver(t, fixture.driver)
	finishReadyDriverTasks(t, fixture.p, map[*G]*yieldingTestG{fixture.task.g: fixture.task})
	if !fixture.source.CanRelease() || !fixture.registry.CanRelease() {
		t.Fatal("channel claim-core cleanup retained stable state")
	}
	runtime.KeepAlive(fixture.task.frame.memory)
}

func externallyCommitChannelCandidate(t *testing.T, fixture *channelClaimCoreFixture, index int) {
	externallyCommitChannelCandidateAtRoute(t, fixture, index, 0)
}

func externallyCommitChannelCandidateAtRoute(
	t *testing.T,
	fixture *channelClaimCoreFixture,
	index int,
	route RouteID,
) {
	t.Helper()
	admission, acquired := fixture.source.acquireExternalCommit(fixture.ids[index])
	if acquired != channelExternalCommitAcquired {
		t.Fatalf("admit externally committed channel candidate = %d", acquired)
	}
	if fixture.claim == nil || !preemptCompareAndSwap(&fixture.claim.state, selectClaimOpen, selectClaimAcquiring) {
		_ = admission.releaseWithoutCommit()
		t.Fatal("acquire external channel select claim under admission")
	}
	if !beginExternalSelectClaimEffect(fixture.claim) {
		t.Fatal("begin externally committed channel effect")
	}
	if result := admission.publishExternallyCommittedAtRoute(route); result != ChannelOperationPosted {
		t.Fatalf("publish externally committed channel candidate = %d", result)
	}
	if !publishExternalSelectClaim(fixture.claim) {
		t.Fatal("publish externally committed select claim")
	}
	if !admission.releaseCommitted() {
		t.Fatal("release externally committed channel admission")
	}
}

func TestOwnerLocalChannelCompletionSkipsExternalSourceEpoch(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-owner-local-peer", []uint32{151}, true, 0)

	// Consume the mandatory initial parked-set visit. The peer is still waiting,
	// but its record is now owner-idle and therefore eligible for the exact local
	// completion queue.
	requestChannelClaimCoreFixture(t, fixture)
	initial := pollChannelClaimCoreComplete(t, fixture)
	if initial.Completed != 0 || initial.Promoted != 0 || fixture.wait.work != waitSetWorkIdle ||
		fixture.p.affectedWaitHead != nil || fixture.p.affectedWaitTail != nil {
		t.Fatalf("initial owner-local visit = %+v wait=%+v affected=(%p,%p)",
			initial, fixture.wait, fixture.p.affectedWaitHead, fixture.p.affectedWaitTail)
	}

	producer := newYieldingTestG(t, "channel-owner-local-producer")
	if !Enqueue(fixture.p, producer.g) {
		t.Fatal("enqueue owner-local producer")
	}
	if g, ok := NextRunnable(fixture.p); !ok || g != producer.g {
		t.Fatalf("dequeue owner-local producer = (%p,%t)", g, ok)
	}
	producerAction := beginWaitTestResume(t, fixture.p, producer)
	externallyCommitChannelCandidateAtRoute(t, fixture, 0, RouteID(1))

	local, ok := TryPublishOwnerLocalChannelCompletion(producer.g, fixture.source, fixture.ids[0])
	ready, readyOK := channelOperationReadyAt(fixture.source, fixture.ids[0].LocalSlot()-1)
	if !ok || !local || !readyOK || ready || fixture.source.Pending() ||
		fixture.driver.local.head != &fixture.wait || fixture.driver.local.tail != &fixture.wait ||
		fixture.wait.work != waitSetWorkQueued || fixture.p.affectedWaitHead != nil ||
		fixture.p.affectedWaitTail != nil || fixture.registry.ObserveRequested(fixture.handle) ||
		preemptWordState(loadGPreempt(producer.g)) != preemptRequested {
		t.Fatalf("owner-local publication = (%t,%t) ready=(%t,%t) pending=%t local=(%p,%p) wait=%+v request=%t preempt=%#x",
			local, ok, ready, readyOK, fixture.source.Pending(), fixture.driver.local.head,
			fixture.driver.local.tail, fixture.wait, fixture.registry.ObserveRequested(fixture.handle),
			loadGPreempt(producer.g))
	}
	yieldRunningDriverTask(t, fixture.p, producer, producerAction)

	var complete ExecutorPollProgress
	for reduction := 0; reduction < 64; reduction++ {
		step, advanced := NextExecutorRunStep(fixture.driver)
		if !advanced || step.Kind != ExecutorRunStepSource || step.Poll.Used != 1 ||
			!step.Poll.AtomicResolve || fixture.driver.poll != (executorPollTransaction{}) {
			t.Fatalf("owner-local reduction %d = (%+v,%t), poll=%+v", reduction, step, advanced, fixture.driver.poll)
		}
		complete = step.Poll
		if complete.Complete {
			if !CommitExecutorRunSourceDistribution(fixture.driver, false) {
				t.Fatal("commit owner-local source distribution")
			}
			break
		}
	}
	if !complete.Complete || complete.Promoted != 1 || !emptyOwnerLocalCompletion(&fixture.driver.local) ||
		fixture.task.g.park.phase != parkReady || fixture.source.Pending() ||
		fixture.registry.ObserveRequested(fixture.handle) {
		t.Fatalf("owner-local completion = %+v local=%+v park=%+v pending=%t request=%t",
			complete, fixture.driver.local, fixture.task.g.park, fixture.source.Pending(),
			fixture.registry.ObserveRequested(fixture.handle))
	}

	if !EnterExecutorRunCompatibility(fixture.driver) {
		t.Fatal("leave owner-local bounded runner")
	}
	if g, runnable := NextRunnable(fixture.p); !runnable || g != producer.g {
		t.Fatalf("dequeue yielded owner-local producer = (%p,%t)", g, runnable)
	}
	finishWaitTestTask(t, fixture.p, producer, beginWaitTestResume(t, fixture.p, producer))
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 151 || !decision.lease.Valid() {
		t.Fatalf("owner-local peer decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
	runtime.KeepAlive(producer.frame.memory)
}

func TestSelectClaimLayoutPairAcquisitionAndFrozenSourceID(t *testing.T) {
	if unsafe.Sizeof(SelectClaim{}) != 4 || unsafe.Alignof(SelectClaim{}) != 4 {
		t.Fatalf("SelectClaim layout = size:%d align:%d", unsafe.Sizeof(SelectClaim{}), unsafe.Alignof(SelectClaim{}))
	}
	if OperationSourceChannel != OperationSourceControl+1 {
		t.Fatalf("Channel source ID = %d, want appended after Control %d", OperationSourceChannel, OperationSourceControl)
	}
	var claims [2]SelectClaim
	if acquired, ok := tryAcquireExternalSelectClaims(&claims[0], &claims[1]); !ok || !acquired ||
		selectClaimLoad(&claims[0]) != selectClaimAcquiring || selectClaimLoad(&claims[1]) != selectClaimAcquiring {
		t.Fatal("pair claim acquisition did not reserve both selects")
	}
	if !beginExternalSelectClaimsEffect(&claims[0], &claims[1]) ||
		selectClaimLoad(&claims[0]) != selectClaimCommitting || selectClaimLoad(&claims[1]) != selectClaimCommitting {
		t.Fatal("pair effect permission did not commit both selects")
	}
	if !publishExternalSelectClaims(&claims[0], &claims[1]) ||
		selectClaimLoad(&claims[0]) != selectClaimClaimed || selectClaimLoad(&claims[1]) != selectClaimClaimed {
		t.Fatal("pair claim publication did not commit both selects")
	}

	first, second := &claims[0], &claims[1]
	if uintptr(unsafe.Pointer(first)) > uintptr(unsafe.Pointer(second)) {
		first, second = second, first
	}
	preemptStore(&first.state, selectClaimOpen)
	preemptStore(&second.state, selectClaimClaimed)
	if acquired, ok := tryAcquireExternalSelectClaims(first, second); !ok || acquired || selectClaimLoad(first) != selectClaimOpen {
		t.Fatal("failed pair acquisition did not roll back its first claim")
	}
	if acquired, ok := tryAcquireExternalSelectClaims(nil, second); ok || acquired {
		t.Fatal("invalid pair claim was accepted")
	}
}

func TestChannelExternalCommitPairOrderingFailuresAndCommit(t *testing.T) {
	a := newChannelClaimCoreFixture(t, "channel-pair-a", []uint32{81}, true, 0)
	b := newChannelClaimCoreFixture(t, "channel-pair-b", []uint32{82}, true, 0)
	slotA, _ := channelOperationSlotFor(a.source, a.ids[0])
	slotB, _ := channelOperationSlotFor(b.source, b.ids[0])

	var pair channelExternalCommitPair
	if result := beginChannelExternalCommitPair(
		&pair, a.source, a.ids[0], a.claim, a.source, a.ids[0], a.claim,
	); result != channelExternalCommitPairBeginInvalid || pair != (channelExternalCommitPair{}) {
		t.Fatalf("self endpoint pair = (%+v,%d)", pair, result)
	}
	if result := beginChannelExternalCommitPair(
		&pair, a.source, a.ids[0], b.claim, b.source, b.ids[0], a.claim,
	); result != channelExternalCommitPairBeginClaimMismatch || pair != (channelExternalCommitPair{}) ||
		preemptLoad(&slotA.inflight) != 0 || preemptLoad(&slotB.inflight) != 0 ||
		selectClaimLoad(a.claim) != selectClaimOpen || selectClaimLoad(b.claim) != selectClaimOpen {
		t.Fatalf("mismatched pair claim mapping = (%+v,%d), inflight=(%#x,%#x) claims=(%d,%d)",
			pair, result, preemptLoad(&slotA.inflight), preemptLoad(&slotB.inflight),
			selectClaimLoad(a.claim), selectClaimLoad(b.claim))
	}
	slotA.record.phase = operationDetached
	if result := beginChannelExternalCommitPair(
		&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim,
	); result != channelExternalCommitPairBeginInvariantFailure || pair != (channelExternalCommitPair{}) ||
		preemptLoad(&slotA.inflight) != 0 || preemptLoad(&slotB.inflight) != 0 ||
		selectClaimLoad(a.claim) != selectClaimOpen || selectClaimLoad(b.claim) != selectClaimOpen {
		t.Fatalf("post-claim record rejection = (%+v,%d), inflight=(%#x,%#x) claims=(%d,%d)",
			pair, result, preemptLoad(&slotA.inflight), preemptLoad(&slotB.inflight),
			selectClaimLoad(a.claim), selectClaimLoad(b.claim))
	}
	slotA.record.phase = operationActive

	result := beginChannelExternalCommitPair(&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim)
	if result != channelExternalCommitPairBeginPrepared {
		t.Fatalf("begin ordered channel pair = %d", result)
	}
	// This is only the C0 runtime address-identity proof. It is deliberately not
	// a C1 production wiring certificate: compiler tests must additionally prove
	// non-moving coroutine-frame placement, no heap allocation, and a
	// NoSuspend/NoPanic hchan critical section before real hchan may call it.
	firstSlot := pair.endpointA.slot
	if !pair.firstIsA {
		firstSlot = pair.endpointB.slot
	}
	copiedAbort := pair
	if copiedAbort.abort() || copiedAbort.beginEffect() || copiedAbort.commit() ||
		releaseChannelExternalCommitPairWithoutEffect(&copiedAbort) || pair.self != &pair ||
		selectClaimLoad(a.claim) != selectClaimAcquiring || selectClaimLoad(b.claim) != selectClaimAcquiring ||
		preemptLoad(&slotA.inflight) != 1 || preemptLoad(&slotB.inflight) != 1 {
		t.Fatalf("copied Prepared pair mutated original before abort: copied=%+v pair=%+v claims=(%d,%d) inflight=(%#x,%#x)",
			copiedAbort, pair, selectClaimLoad(a.claim), selectClaimLoad(b.claim),
			preemptLoad(&slotA.inflight), preemptLoad(&slotB.inflight))
	}
	if !pair.abort() || pair != (channelExternalCommitPair{}) ||
		selectClaimLoad(a.claim) != selectClaimOpen || selectClaimLoad(b.claim) != selectClaimOpen {
		t.Fatal("abort ordered channel pair")
	}
	var reversed channelExternalCommitPair
	result = beginChannelExternalCommitPair(&reversed, b.source, b.ids[0], b.claim, a.source, a.ids[0], a.claim)
	if result != channelExternalCommitPairBeginPrepared {
		t.Fatalf("begin reversed channel pair = %d", result)
	}
	reversedFirstSlot := reversed.endpointA.slot
	if !reversed.firstIsA {
		reversedFirstSlot = reversed.endpointB.slot
	}
	if reversedFirstSlot != firstSlot || !reversed.abort() {
		t.Fatal("reversing channel direction changed admission order")
	}

	firstSource, firstID, firstClaim := a.source, a.ids[0], a.claim
	secondSource, secondID, secondClaim := b.source, b.ids[0], b.claim
	if uintptr(unsafe.Pointer(slotA)) > uintptr(unsafe.Pointer(slotB)) {
		firstSource, secondSource = secondSource, firstSource
		firstID, secondID = secondID, firstID
		firstClaim, secondClaim = secondClaim, firstClaim
	}
	staleFirst := firstID
	staleFirst.Generation++
	var failed channelExternalCommitPair
	if failedResult := beginChannelExternalCommitPair(
		&failed, firstSource, staleFirst, firstClaim, secondSource, secondID, secondClaim,
	); failedResult != channelExternalCommitPairBeginFirstAdmissionFailed || failed != (channelExternalCommitPair{}) {
		t.Fatalf("first pair admission failure = (%+v,%d)", failed, failedResult)
	}
	staleSecond := secondID
	staleSecond.Generation++
	if failedResult := beginChannelExternalCommitPair(
		&failed, firstSource, firstID, firstClaim, secondSource, staleSecond, secondClaim,
	); failedResult != channelExternalCommitPairBeginSecondAdmissionFailed || failed != (channelExternalCommitPair{}) ||
		preemptLoad(&slotA.inflight) != 0 || preemptLoad(&slotB.inflight) != 0 {
		t.Fatalf("second pair admission failure = (%+v,%d), inflight=(%#x,%#x)",
			failed, failedResult, preemptLoad(&slotA.inflight), preemptLoad(&slotB.inflight))
	}
	preemptStore(&b.claim.state, selectClaimClaimed)
	if failedResult := beginChannelExternalCommitPair(
		&failed, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim,
	); failedResult != channelExternalCommitPairBeginClaimResolved || failed != (channelExternalCommitPair{}) ||
		selectClaimLoad(a.claim) != selectClaimOpen || selectClaimLoad(b.claim) != selectClaimClaimed ||
		preemptLoad(&slotA.inflight) != 0 || preemptLoad(&slotB.inflight) != 0 {
		t.Fatalf("pair terminal claim classification = (%+v,%d), claims=(%d,%d) inflight=(%#x,%#x)",
			failed, failedResult, selectClaimLoad(a.claim), selectClaimLoad(b.claim),
			preemptLoad(&slotA.inflight), preemptLoad(&slotB.inflight))
	}
	preemptStore(&b.claim.state, selectClaimOpen)

	result = beginChannelExternalCommitPair(&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim)
	copiedPrepared := pair
	if result != channelExternalCommitPairBeginPrepared || copiedPrepared.beginEffect() || copiedPrepared.commit() ||
		copiedPrepared.abort() || pair.self != &pair || selectClaimLoad(a.claim) != selectClaimAcquiring ||
		selectClaimLoad(b.claim) != selectClaimAcquiring || preemptLoad(&slotA.inflight) != 1 ||
		preemptLoad(&slotB.inflight) != 1 {
		t.Fatalf("copied Prepared pair crossed address identity: result=%d copied=%+v pair=%+v claims=(%d,%d) inflight=(%#x,%#x)",
			result, copiedPrepared, pair, selectClaimLoad(a.claim), selectClaimLoad(b.claim),
			preemptLoad(&slotA.inflight), preemptLoad(&slotB.inflight))
	}
	if !pair.beginEffect() ||
		selectClaimLoad(a.claim) != selectClaimCommitting || selectClaimLoad(b.claim) != selectClaimCommitting {
		t.Fatalf("begin external channel pair effect = result:%d pair:%+v claims:(%d,%d)",
			result, pair, selectClaimLoad(a.claim), selectClaimLoad(b.claim))
	}
	copiedEffect := pair
	if copiedEffect.beginEffect() || copiedEffect.abort() || copiedEffect.commit() ||
		releaseChannelExternalCommitPairWithoutEffect(&copiedEffect) || pair.self != &pair ||
		selectClaimLoad(a.claim) != selectClaimCommitting || selectClaimLoad(b.claim) != selectClaimCommitting ||
		preemptLoad(&slotA.inflight) != 1 || preemptLoad(&slotB.inflight) != 1 ||
		preemptLoad(&slotA.physical) != uint32(channelPhysicalIdle) ||
		preemptLoad(&slotB.physical) != uint32(channelPhysicalIdle) {
		t.Fatalf("copied Effect pair mutated original: copied=%+v pair=%+v claims=(%d,%d) inflight=(%#x,%#x)",
			copiedEffect, pair, selectClaimLoad(a.claim), selectClaimLoad(b.claim),
			preemptLoad(&slotA.inflight), preemptLoad(&slotB.inflight))
	}
	if pair.abort() || !pair.commit() ||
		pair != (channelExternalCommitPair{}) || selectClaimLoad(a.claim) != selectClaimClaimed ||
		selectClaimLoad(b.claim) != selectClaimClaimed || preemptLoad(&slotA.inflight) != 0 ||
		preemptLoad(&slotB.inflight) != 0 || preemptLoad(&slotA.mailbox) != uint32(channelMailboxForced) ||
		preemptLoad(&slotB.mailbox) != uint32(channelMailboxForced) {
		t.Fatalf("commit external channel pair = result:%d pair:%+v claims:(%d,%d) inflight:(%#x,%#x) mailboxes:(%d,%d)",
			result, pair, selectClaimLoad(a.claim), selectClaimLoad(b.claim), preemptLoad(&slotA.inflight),
			preemptLoad(&slotB.inflight), preemptLoad(&slotA.mailbox), preemptLoad(&slotB.mailbox))
	}
	requestChannelClaimCoreFixture(t, a)
	requestChannelClaimCoreFixture(t, b)
	pollChannelClaimCoreComplete(t, a)
	pollChannelClaimCoreComplete(t, b)
	decisionA := takeChannelClaimCoreDecision(t, a)
	decisionB := takeChannelClaimCoreDecision(t, b)
	if decisionA.outcome != ParkOutcomeCompleted || decisionA.caseID != 81 || !decisionA.lease.Valid() ||
		decisionB.outcome != ParkOutcomeCompleted || decisionB.caseID != 82 || !decisionB.lease.Valid() {
		t.Fatalf("external pair decisions = (%+v,%+v)", decisionA, decisionB)
	}
	releaseChannelClaimCoreFixture(t, a, decisionA)
	releaseChannelClaimCoreFixture(t, b, decisionB)
}

func TestChannelExternalCommitPairClaimsBeforeOwnerRecordValidation(t *testing.T) {
	a := newChannelClaimCoreFixture(t, "channel-pair-owner-record-a", []uint32{87}, true, 0)
	b := newChannelClaimCoreFixture(t, "channel-pair-owner-record-b", []uint32{88}, true, 0)
	slotA, _ := channelOperationSlotFor(a.source, a.ids[0])
	slotB, _ := channelOperationSlotFor(b.source, b.ids[0])
	if state := selectClaimOwnerAcquire(a.claim); state != selectClaimOpen {
		t.Fatalf("acquire owner claim before record validation = %d", state)
	}

	// A resolver holding the claim may mutate its owner-only record. External
	// begin must observe claim contention without reading that record first.
	slotA.record.phase = operationDetached
	var pair channelExternalCommitPair
	if result := beginChannelExternalCommitPair(
		&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim,
	); result != channelExternalCommitPairBeginClaimContended || pair != (channelExternalCommitPair{}) ||
		selectClaimLoad(a.claim) != selectClaimAcquiring || selectClaimLoad(b.claim) != selectClaimOpen ||
		preemptLoad(&slotA.inflight) != 0 || preemptLoad(&slotB.inflight) != 0 {
		t.Fatalf("external begin inspected owner record before claim: pair=%+v result=%d claims=(%d,%d) inflight=(%#x,%#x)",
			pair, result, selectClaimLoad(a.claim), selectClaimLoad(b.claim),
			preemptLoad(&slotA.inflight), preemptLoad(&slotB.inflight))
	}
	slotA.record.phase = operationActive

	// Keep mutating the same owner-only field while begin repeatedly contends.
	// The race detector proves that the contended path reads only stable slot
	// identity plus atomic claim/admission fields.
	started := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		slotA.record.phase = operationDetached
		close(started)
		for {
			select {
			case <-stop:
				slotA.record.phase = operationActive
				close(done)
				return
			default:
				slotA.record.phase = operationActive
				slotA.record.phase = operationDetached
			}
		}
	}()
	<-started
	for attempt := 0; attempt < 1024; attempt++ {
		var pair channelExternalCommitPair
		result := beginChannelExternalCommitPair(
			&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim,
		)
		if result != channelExternalCommitPairBeginClaimContended || pair != (channelExternalCommitPair{}) {
			close(stop)
			<-done
			t.Fatalf("owner-record race begin %d = (%+v,%d)", attempt, pair, result)
		}
	}
	close(stop)
	<-done
	if !selectClaimOwnerReleasePending(a.claim) || selectClaimLoad(b.claim) != selectClaimOpen ||
		preemptLoad(&slotA.inflight) != 0 || preemptLoad(&slotB.inflight) != 0 ||
		slotA.record.phase != operationActive {
		t.Fatal("release owner-record race fixture")
	}

	for _, fixture := range []*channelClaimCoreFixture{a, b} {
		if result := fixture.source.PostReady(fixture.ids[0]); result != ChannelOperationPosted {
			t.Fatalf("post owner-record cleanup readiness = %d", result)
		}
		requestChannelClaimCoreFixture(t, fixture)
		pollChannelClaimCoreComplete(t, fixture)
		decision := takeChannelClaimCoreDecision(t, fixture)
		if decision.outcome != ParkOutcomeCompleted || !decision.lease.Valid() {
			t.Fatalf("owner-record cleanup decision = %+v", decision)
		}
		releaseChannelClaimCoreFixture(t, fixture, decision)
	}
}

func TestChannelExternalCommitPairRejectsNonPendingEndpoint(t *testing.T) {
	a := newChannelClaimCoreFixture(t, "channel-pair-invalid-endpoint-a", []uint32{89}, true, 0)
	b := newChannelClaimCoreFixture(t, "channel-pair-invalid-endpoint-b", []uint32{90}, true, 0)
	slotA, _ := channelOperationSlotFor(a.source, a.ids[0])
	slotB, _ := channelOperationSlotFor(b.source, b.ids[0])
	record := &slotA.record

	tests := []struct {
		name    string
		mutate  func()
		restore func()
	}{
		{
			name: "terminal-disposition",
			mutate: func() {
				record.disposition = OperationDispositionLost
			},
			restore: func() {
				record.disposition = OperationDispositionPending
			},
		},
		{
			name: "resolution-applied",
			mutate: func() {
				record.resolutionApplied = true
			},
			restore: func() {
				record.resolutionApplied = false
			},
		},
		{
			name: "candidate-not-pending-valid",
			mutate: func() {
				setOperationCandidate(record, OperationCommitReadyThenTryCommit, OperationCommitRolledBack, true)
			},
			restore: func() {
				setOperationCandidate(record, OperationCommitReadyThenTryCommit, OperationCommitIdle, false)
			},
		},
		{
			name: "park-resolving",
			mutate: func() {
				record.link.park.resolving = true
			},
			restore: func() {
				record.link.park.resolving = false
			},
		},
		{
			name: "ticket-mismatch",
			mutate: func() {
				record.link.ticket.generation++
			},
			restore: func() {
				record.link.ticket.generation--
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.mutate()
			var pair channelExternalCommitPair
			result := beginChannelExternalCommitPair(
				&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim,
			)
			test.restore()
			if result != channelExternalCommitPairBeginInvariantFailure || pair != (channelExternalCommitPair{}) ||
				selectClaimLoad(a.claim) != selectClaimOpen || selectClaimLoad(b.claim) != selectClaimOpen ||
				preemptLoad(&slotA.inflight) != 0 || preemptLoad(&slotB.inflight) != 0 {
				t.Fatalf("invalid endpoint entered effect gate: result=%d pair=%+v claims=(%d,%d) inflight=(%#x,%#x)",
					result, pair, selectClaimLoad(a.claim), selectClaimLoad(b.claim),
					preemptLoad(&slotA.inflight), preemptLoad(&slotB.inflight))
			}
		})
	}

	for _, fixture := range []*channelClaimCoreFixture{a, b} {
		if result := fixture.source.PostReady(fixture.ids[0]); result != ChannelOperationPosted {
			t.Fatalf("post invalid-endpoint cleanup readiness = %d", result)
		}
		requestChannelClaimCoreFixture(t, fixture)
		pollChannelClaimCoreComplete(t, fixture)
		decision := takeChannelClaimCoreDecision(t, fixture)
		if decision.outcome != ParkOutcomeCompleted || !decision.lease.Valid() {
			t.Fatalf("invalid-endpoint cleanup decision = %+v", decision)
		}
		releaseChannelClaimCoreFixture(t, fixture, decision)
	}
}

func TestChannelExternalCommitPairCheckedReleaseRejectsConsumedAdmission(t *testing.T) {
	a := newChannelClaimCoreFixture(t, "channel-pair-checked-release-a", []uint32{93}, true, 0)
	b := newChannelClaimCoreFixture(t, "channel-pair-checked-release-b", []uint32{94}, true, 0)
	var pair channelExternalCommitPair
	if result := beginChannelExternalCommitPair(
		&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim,
	); result != channelExternalCommitPairBeginPrepared {
		t.Fatalf("prepare checked-release pair = (%d,%+v)", result, pair)
	}
	consumed := &pair.endpointA
	if !pair.firstIsA {
		consumed = &pair.endpointB
	}
	if !producerAdmissionReleaseChecked(&consumed.slot.inflight) {
		t.Fatal("consume admission before checked pair release")
	}
	slotA, slotB := pair.endpointA.slot, pair.endpointB.slot
	if pair.abort() || pair.self != &pair || pair.phase != channelExternalCommitPairBroken ||
		selectClaimLoad(a.claim) != selectClaimOpen || selectClaimLoad(b.claim) != selectClaimOpen ||
		preemptLoad(&slotA.inflight) != 0 || preemptLoad(&slotB.inflight) != 0 {
		t.Fatalf("pair accepted consumed admission: pair=%+v claims=(%d,%d) inflight=(%#x,%#x)",
			pair, selectClaimLoad(a.claim), selectClaimLoad(b.claim),
			preemptLoad(&slotA.inflight), preemptLoad(&slotB.inflight))
	}

	for _, fixture := range []*channelClaimCoreFixture{a, b} {
		if result := fixture.source.PostReady(fixture.ids[0]); result != ChannelOperationPosted {
			t.Fatalf("post checked-release cleanup readiness = %d", result)
		}
		requestChannelClaimCoreFixture(t, fixture)
		pollChannelClaimCoreComplete(t, fixture)
		decision := takeChannelClaimCoreDecision(t, fixture)
		if decision.outcome != ParkOutcomeCompleted || !decision.lease.Valid() {
			t.Fatalf("checked-release cleanup decision = %+v", decision)
		}
		releaseChannelClaimCoreFixture(t, fixture, decision)
	}
}

func TestChannelExternalAdmissionCopyCannotReleaseAnotherProducer(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-admission-linear-token", []uint32{97}, true, 0)
	admission, acquired := fixture.source.acquireExternalCommit(fixture.ids[0])
	if acquired != channelExternalCommitAcquired || admission.token == 0 || admission.token&1 == 0 {
		t.Fatalf("acquire linear external admission = (%+v,%d)", admission, acquired)
	}
	copied := admission
	if !admission.releaseWithoutCommit() {
		t.Fatal("release original linear external admission")
	}
	slot, _ := channelOperationSlotFor(fixture.source, fixture.ids[0])
	if !producerAdmissionAcquire(&slot.inflight) {
		t.Fatal("acquire unrelated producer beside copied admission")
	}
	if copied.releaseWithoutCommit() || !copied.broken || preemptLoad(&slot.inflight) != 1 {
		t.Fatalf("copied admission consumed unrelated producer: copied=%+v inflight=%#x", copied, preemptLoad(&slot.inflight))
	}
	if !producerAdmissionReleaseChecked(&slot.inflight) {
		t.Fatal("release unrelated producer after rejected copy")
	}
	if result := fixture.source.PostReady(fixture.ids[0]); result != ChannelOperationPosted {
		t.Fatalf("post linear-token cleanup readiness = %d", result)
	}
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || !decision.lease.Valid() {
		t.Fatalf("linear-token cleanup decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelExternalCommitPairDuplicateAfterEffectFailsClosed(t *testing.T) {
	a := newChannelClaimCoreFixture(t, "channel-pair-duplicate-a", []uint32{83}, true, 0)
	b := newChannelClaimCoreFixture(t, "channel-pair-duplicate-b", []uint32{84}, true, 0)
	var pair channelExternalCommitPair
	result := beginChannelExternalCommitPair(&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim)
	if result != channelExternalCommitPairBeginPrepared || !pair.beginEffect() {
		t.Fatalf("prepare duplicate-after-effect pair = (%d,%+v)", result, pair)
	}
	slotA, _ := channelOperationSlotFor(a.source, a.ids[0])
	preemptStore(&slotA.physical, uint32(channelPhysicalCommitted))
	preemptStore(&slotA.mailbox, uint32(channelMailboxForced))
	if pair.commit() || pair.self != &pair || pair.phase != channelExternalCommitPairBroken || pair.abort() ||
		preemptLoad(&slotA.inflight) != 1 || selectClaimLoad(a.claim) != selectClaimCommitting ||
		selectClaimLoad(b.claim) != selectClaimCommitting {
		t.Fatalf("duplicate effect was treated as idempotent: pair=%+v inflight=%#x claims=(%d,%d)",
			pair, preemptLoad(&slotA.inflight), selectClaimLoad(a.claim), selectClaimLoad(b.claim))
	}
	// This is an intentional fail-closed terminal fixture: the retained pair
	// leases demonstrate that no release/reclaim follows an ambiguous effect.
	runtime.KeepAlive(a.task.frame.memory)
	runtime.KeepAlive(b.task.frame.memory)
}

func TestChannelExternalCommitPairAbortFailureRetainsLease(t *testing.T) {
	a := newChannelClaimCoreFixture(t, "channel-pair-abort-broken-a", []uint32{85}, true, 0)
	b := newChannelClaimCoreFixture(t, "channel-pair-abort-broken-b", []uint32{86}, true, 0)
	var pair channelExternalCommitPair
	result := beginChannelExternalCommitPair(&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim)
	if result != channelExternalCommitPairBeginPrepared {
		t.Fatalf("prepare broken-abort pair = (%d,%+v)", result, pair)
	}
	preemptStore(&a.claim.state, selectClaimClaimed)
	slotA, _ := channelOperationSlotFor(a.source, a.ids[0])
	slotB, _ := channelOperationSlotFor(b.source, b.ids[0])
	if pair.abort() || pair.self != &pair || pair.phase != channelExternalCommitPairBroken ||
		preemptLoad(&slotA.inflight) != 1 || preemptLoad(&slotB.inflight) != 1 {
		t.Fatalf("failed claim rollback released lifetime: pair=%+v inflight=(%#x,%#x)",
			pair, preemptLoad(&slotA.inflight), preemptLoad(&slotB.inflight))
	}
	// Intentional fail-closed fixture, as above: a corrupted claim rollback can
	// leak a bounded slot but can never release then touch a reclaimed frame.
	runtime.KeepAlive(a.task.frame.memory)
	runtime.KeepAlive(b.task.frame.memory)
}

func TestChannelReservationAttachFailureLeavesReusableGeneration(t *testing.T) {
	p := new(P)
	source := new(ChannelOperationSource)
	if !BindChannelOperationSource(source, p) {
		t.Fatal("bind channel source for attach rollback")
	}
	var g G
	if !InitG(&g) {
		t.Fatal("initialize attach-rollback G")
	}
	ticket, begun := BeginParkSet(&g.park, 1, 1)
	var wait WaitSetRecord
	if !begun || !PrepareWaitSetRecord(&wait, &g, ticket) {
		t.Fatal("prepare attach-rollback wait")
	}
	wrongTicket := ticket
	wrongTicket.generation++
	if id, ok := source.ReserveAndAttachWait(p, &g.park, wrongTicket, &wait, 1, nil); ok || id != (OperationID{}) {
		t.Fatalf("invalid channel attach = (%+v, %t)", id, ok)
	}
	slot := &source.slots[0]
	id, idOK := MakeOperationID(OperationSourceChannel, 1, 1)
	if !idOK || preemptLoad(&slot.state) != uint32(producerSourceFree) ||
		preemptLoad(&slot.inflight) != producerAdmissionClosed || preemptLoad(&slot.generation) != 1 ||
		slot.record != (OperationRecord{id: id, phase: operationReusable}) || slot.claim != nil {
		t.Fatalf("failed channel attach leaked generation: state=%d inflight=%#x generation=%d record=%+v claim=%p",
			preemptLoad(&slot.state), preemptLoad(&slot.inflight), preemptLoad(&slot.generation), slot.record, slot.claim)
	}
	if !AbortParkSet(&g.park, ticket) {
		t.Fatal("abort attach-rollback park")
	}
	if outcome, _, lease, consumed := ConsumeParkSet(&g.park, ticket); !consumed ||
		outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) || !ReleasePreparedWaitSetRecord(&wait) {
		t.Fatalf("consume attach-rollback park = (%d,%+v,%t)", outcome, lease, consumed)
	}
	if !UnbindChannelOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release channel source after attach rollback")
	}
}

func TestChannelReadyTryCommitAndStrongJoinLifecycle(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-ready-commit", []uint32{101, 202}, true, 0)
	if result := fixture.source.PostReady(fixture.ids[1]); result != ChannelOperationPosted {
		t.Fatalf("post channel readiness = %d", result)
	}
	slot, _ := channelOperationSlotFor(fixture.source, fixture.ids[1])
	if !producerAdmissionAcquire(&slot.inflight) {
		t.Fatal("pin admitted channel producer")
	}
	requestChannelClaimCoreFixture(t, fixture)
	progress := pollChannelClaimCoreComplete(t, fixture)
	if !progress.More || selectClaimLoad(fixture.claim) != selectClaimClaimed ||
		preemptLoad(&slot.state) != uint32(producerSourceClosing) ||
		preemptLoad(&slot.inflight) != producerAdmissionClosed|1 || slot.claim != fixture.claim ||
		slot.record.phase != operationActive || fixture.task.g.park.phase != parkDetaching {
		t.Fatalf("channel apply/join boundary = progress:%+v claim:%d state:%d inflight:%#x slotClaim:%p phase:%d",
			progress, selectClaimLoad(fixture.claim), preemptLoad(&slot.state), preemptLoad(&slot.inflight), slot.claim, slot.record.phase)
	}
	if fixture.source.ConfirmQuiesced(fixture.p, fixture.ids[1]) {
		t.Fatal("channel operation quiesced before admitted producer returned")
	}
	producerAdmissionRelease(&slot.inflight)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	leaseID, leaseOK := decision.lease.ID()
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 202 || decision.taskCancel != TaskCancelNone ||
		!leaseOK || leaseID != fixture.ids[1] {
		t.Fatalf("channel ready decision = %+v leaseID=(%+v,%t)", decision, leaseID, leaseOK)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelApplyRejectsQuiescedNonClaimedFrame(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-apply-nonclaimed", []uint32{95, 96}, true, 0)
	id := fixture.ids[0]
	if result := fixture.source.PostReady(id); result != ChannelOperationPosted {
		t.Fatalf("post nonclaimed Apply readiness = %d", result)
	}
	requestChannelClaimCoreFixture(t, fixture)
	slot, _ := channelOperationSlotFor(fixture.source, id)
	reachedApply := false
	for step := 0; step < 10000; step++ {
		progress, ok := PollExecutorSlice(fixture.driver, 1)
		if !ok || progress.Complete {
			t.Fatalf("advance to nonclaimed Apply step %d = (%+v,%t)", step, progress, ok)
		}
		if fixture.driver.poll.resolve.phase == publishedEpochResolveApply &&
			fixture.driver.poll.resolve.link == &slot.record.link {
			reachedApply = true
			break
		}
	}
	if !reachedApply || selectClaimLoad(fixture.claim) != selectClaimClaimed ||
		preemptLoad(&slot.inflight) != 0 || slot.record.disposition != OperationDispositionWinner ||
		slot.record.resolutionApplied || slot.record.phase != operationActive {
		t.Fatalf("nonclaimed Apply precondition: reached=%t claim=%d inflight=%#x record=%+v",
			reachedApply, selectClaimLoad(fixture.claim), preemptLoad(&slot.inflight), slot.record)
	}
	preemptStore(&fixture.claim.state, selectClaimOpen)
	if result := fixture.source.ApplyOne(fixture.p, id, &slot.record); result != OperationApplyInvalid ||
		preemptLoad(&slot.state) != uint32(producerSourceClosing) ||
		preemptLoad(&slot.inflight) != producerAdmissionClosed || slot.claim != fixture.claim ||
		slot.record.resolutionApplied || slot.record.phase != operationActive {
		t.Fatalf("quiesced nonclaimed Apply = result:%d state:%d inflight:%#x claim:%p record:%+v",
			result, preemptLoad(&slot.state), preemptLoad(&slot.inflight), slot.claim, slot.record)
	}
	preemptStore(&fixture.claim.state, selectClaimClaimed)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 95 || !decision.lease.Valid() {
		t.Fatalf("nonclaimed Apply recovery decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelReadyTryCommitRetryBudgetYieldsEpochAndPreservesReady(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-retry-budget", []uint32{11, 22}, true, 0)
	if result := fixture.source.PostReady(fixture.ids[0]); result != ChannelOperationPosted {
		t.Fatalf("post retry-budget readiness = %d", result)
	}
	slot, _ := channelOperationSlotFor(fixture.source, fixture.ids[0])
	preemptStore(&slot.physical, uint32(channelPhysicalRetryBudget))
	requestChannelClaimCoreFixture(t, fixture)
	competitor := newYieldingTestG(t, "channel-retry-competitor")
	if !Enqueue(fixture.p, competitor.g) {
		t.Fatal("enqueue unrelated ready G beside channel retry")
	}
	var progress ExecutorPollProgress
	for step := 0; step < 10000; step++ {
		runStep, ok := NextExecutorRunStep(fixture.driver)
		if !ok || runStep.Kind != ExecutorRunStepSource || runStep.Poll.Used != 1 {
			t.Fatalf("bounded retry source step %d = (%+v,%t)", step, runStep, ok)
		}
		progress = runStep.Poll
		if progress.Complete {
			break
		}
		if step == 9999 {
			t.Fatal("bounded retry source epoch did not complete")
		}
	}
	if !progress.Complete || !progress.More || !fixture.driver.run.readyDebt ||
		fixture.driver.poll != (executorPollTransaction{}) || fixture.task.g.park.resolving ||
		fixture.p.affectedWaitHead != &fixture.wait || fixture.p.affectedWaitTail != &fixture.wait ||
		selectClaimLoad(fixture.claim) != selectClaimOpen || slot.record.resultState != operationResultEmpty ||
		!operationCandidateIsPublished(&slot.record) || operationCandidateState(&slot.record) != OperationCommitReady ||
		!validParkTicket(slot.record.resultTicket) {
		t.Fatalf("retry-budget cursor/result = progress:%+v poll:%+v claim:%d candidate:%d result:%d ticket:%+v",
			progress, fixture.driver.poll.resolve, selectClaimLoad(fixture.claim), operationCandidateState(&slot.record),
			slot.record.resultState, slot.record.resultTicket)
	}
	runStep, ok := NextExecutorRunStep(fixture.driver)
	if !ok || runStep.Kind != ExecutorRunStepDispatch || runStep.G != competitor.g ||
		!fixture.driver.run.readyDebt || fixture.p.current != competitor.g {
		t.Fatalf("channel retry ready-debt dispatch = (%+v,%t), cursor=%+v", runStep, ok, fixture.driver.run)
	}
	runStep, ok = NextExecutorRunStep(fixture.driver)
	if !ok || runStep.Kind != ExecutorRunStepAction || runStep.G != competitor.g {
		t.Fatalf("channel retry ready-debt action = (%+v,%t)", runStep, ok)
	}
	runnerYieldAction(t, fixture.driver, runStep, competitor)
	if !EnterExecutorRunCompatibility(fixture.driver) {
		t.Fatal("leave bounded runner after observed ready-debt dispatch")
	}
	if g, runnable := NextRunnable(fixture.p); !runnable || g != competitor.g {
		t.Fatalf("channel retry lost yielded competitor = (%p,%t)", g, runnable)
	}
	finishWaitTestTask(t, fixture.p, competitor, beginWaitTestResume(t, fixture.p, competitor))
	readyTicket := slot.record.resultTicket
	preemptStore(&slot.physical, uint32(channelPhysicalReady))
	pollChannelClaimCoreComplete(t, fixture)
	if slot.record.resultTicket != fixture.ticket || selectClaimLoad(fixture.claim) != selectClaimClaimed {
		t.Fatalf("retry completion changed wrong ticket: ready=%+v result=%+v park=%+v claim=%d",
			readyTicket, slot.record.resultTicket, fixture.ticket, selectClaimLoad(fixture.claim))
	}
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 11 || !decision.lease.Valid() {
		t.Fatalf("retry-budget decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
	runtime.KeepAlive(competitor.frame.memory)
}

func TestChannelFailedReadyReopensClaimForLaterPublication(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-ready-retry-generation", []uint32{23, 24}, true, 0)
	id := fixture.ids[0]
	if result := fixture.source.PostReady(id); result != ChannelOperationPosted {
		t.Fatalf("post expiring channel readiness = %d", result)
	}
	slot, _ := channelOperationSlotFor(fixture.source, id)
	preemptStore(&slot.physical, uint32(channelPhysicalIdle))
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	firstReadyTicket := slot.record.resultTicket
	if fixture.task.g.park.phase != parkParked || fixture.task.g.park.resolving ||
		selectClaimLoad(fixture.claim) != selectClaimOpen || operationCandidateIsPublished(&slot.record) ||
		operationCandidateState(&slot.record) != OperationCommitIdle || slot.record.resultState != operationResultEmpty ||
		!validParkTicket(firstReadyTicket) {
		t.Fatalf("failed Ready did not preserve a reusable wait: park=%+v claim=%d record=%+v",
			fixture.task.g.park, selectClaimLoad(fixture.claim), slot.record)
	}
	if result := fixture.source.PostReady(id); result != ChannelOperationPosted {
		t.Fatalf("repost channel readiness = %d", result)
	}
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	if slot.record.resultTicket != fixture.ticket || selectClaimLoad(fixture.claim) != selectClaimClaimed {
		t.Fatalf("later Ready did not commit exact park: first=%+v result=%+v park=%+v claim=%d",
			firstReadyTicket, slot.record.resultTicket, fixture.ticket, selectClaimLoad(fixture.claim))
	}
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 23 || !decision.lease.Valid() {
		t.Fatalf("reposted channel decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelDiscoveryAcquiringStopsWithoutParkMutation(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-discovery-contention", []uint32{31, 32}, true, 0)
	if result := fixture.source.PostReady(fixture.ids[0]); result != ChannelOperationPosted {
		t.Fatal("prepare channel discovery contention")
	}
	admission, acquired := fixture.source.acquireExternalCommit(fixture.ids[0])
	if acquired != channelExternalCommitAcquired ||
		!preemptCompareAndSwap(&fixture.claim.state, selectClaimOpen, selectClaimAcquiring) {
		_ = admission.releaseWithoutCommit()
		t.Fatal("acquire contended claim under external admission")
	}
	requestChannelClaimCoreFixture(t, fixture)
	progress, ok := PollExecutorSlice(fixture.driver, 1000)
	slot, _ := channelOperationSlotFor(fixture.source, fixture.ids[0])
	if !ok || !progress.Complete || !progress.More || fixture.task.g.park.resolving ||
		fixture.driver.poll != (executorPollTransaction{}) || fixture.p.affectedWaitHead != &fixture.wait ||
		fixture.p.affectedWaitTail != &fixture.wait || operationCandidateState(&slot.record) != OperationCommitIdle ||
		operationCandidateIsPublished(&slot.record) || slot.record.resultState != operationResultEmpty ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxReady) ||
		selectClaimLoad(fixture.claim) != selectClaimAcquiring {
		t.Fatalf("contended discovery mutated park/candidate: progress=%+v resolve=%+v park=%+v record=%+v",
			progress, fixture.driver.poll.resolve, fixture.task.g.park, slot.record)
	}
	if !preemptCompareAndSwap(&fixture.claim.state, selectClaimAcquiring, selectClaimOpen) ||
		!admission.releaseWithoutCommit() {
		t.Fatal("roll back external discovery claim")
	}
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 31 || !decision.lease.Valid() {
		t.Fatalf("post-contention channel decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelContendedOwnerCertificateRestoresDiscoveryAfterPeerRollback(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-discovery-cas-contention", []uint32{35}, true, 0)
	if result := fixture.source.PostReady(fixture.ids[0]); result != ChannelOperationPosted ||
		!fixture.source.beginPublishPass(fixture.p) {
		t.Fatalf("publish readiness before contended discovery certificate = %d", result)
	}
	slot, _ := channelOperationSlotFor(fixture.source, fixture.ids[0])
	if published, lost, ok := fixture.source.publishSlot(fixture.p, 0); !ok || published != 1 || lost != 0 {
		t.Fatalf("publish candidate before contended discovery certificate = (%d,%d,%t)", published, lost, ok)
	}
	beforePark, beforeRecord := fixture.task.g.park, slot.record
	var cursor publishedEpochResolveCursor
	var step publishedEpochResolveStep
	if !initializePublishedEpochResolution(&fixture.driver.sources, fixture.p, &cursor, &step) ||
		cursor.phase != publishedEpochResolveDiscover || cursor.link == nil || cursor.claim != nil ||
		fixture.p.affectedWaitHead != nil || fixture.p.affectedWaitTail != nil {
		t.Fatalf("initialize contended discovery cursor = cursor:%+v step:%+v affected:(%p,%p)",
			cursor, step, fixture.p.affectedWaitHead, fixture.p.affectedWaitTail)
	}
	if !resolvePublishedEpochDiscoverStep(&fixture.driver.sources, fixture.p, &cursor, &step) ||
		cursor.phase != publishedEpochResolveDiscover || cursor.link != nil || cursor.claim != fixture.claim ||
		selectClaimLoad(fixture.claim) != selectClaimOpen {
		t.Fatalf("discover contended claim domain = cursor:%+v step:%+v claim:%d",
			cursor, step, selectClaimLoad(fixture.claim))
	}

	// Model the exact single-CAS failure window: the peer won Open->Acquiring
	// and rolled back to Open before this owner restores the FIFO. Contended is
	// the reduction-local certificate; it is deliberately not a shared state.
	if !restorePublishedEpochDiscovery(fixture.p, &cursor, &step, true, selectClaimContended) ||
		!step.complete || !step.retryBudget || cursor != (publishedEpochResolveCursor{}) ||
		selectClaimLoad(fixture.claim) != selectClaimOpen || fixture.task.g.park != beforePark ||
		slot.record != beforeRecord || fixture.p.affectedWaitHead != &fixture.wait ||
		fixture.p.affectedWaitTail != &fixture.wait || fixture.wait.work != waitSetWorkQueued ||
		fixture.wait.workNext != nil {
		t.Fatalf("Contended certificate did not restore exact FIFO: cursor=%+v step=%+v park=%+v record=%+v affected=(%p,%p) work=%d",
			cursor, step, fixture.task.g.park, slot.record, fixture.p.affectedWaitHead,
			fixture.p.affectedWaitTail, fixture.wait.work)
	}

	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 35 || !decision.lease.Valid() {
		t.Fatalf("post-Contended channel decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelDiscoveryCommittingYieldsUntilForcedPublication(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-discovery-effect-permission", []uint32{33, 34}, true, 0)
	if result := fixture.source.PostReady(fixture.ids[0]); result != ChannelOperationPosted {
		t.Fatal("prepare channel Committing discovery contention")
	}
	admission, acquired := fixture.source.acquireExternalCommit(fixture.ids[0])
	if acquired != channelExternalCommitAcquired ||
		!preemptCompareAndSwap(&fixture.claim.state, selectClaimOpen, selectClaimAcquiring) {
		_ = admission.releaseWithoutCommit()
		t.Fatal("acquire external claim before Committing discovery")
	}
	if !beginExternalSelectClaimEffect(fixture.claim) {
		t.Fatal("acquire shared external effect permission")
	}
	requestChannelClaimCoreFixture(t, fixture)
	progress, ok := PollExecutorSlice(fixture.driver, 1000)
	slot, _ := channelOperationSlotFor(fixture.source, fixture.ids[0])
	if !ok || !progress.Complete || !progress.More || fixture.task.g.park.resolving ||
		fixture.driver.poll != (executorPollTransaction{}) || fixture.p.affectedWaitHead != &fixture.wait ||
		fixture.p.affectedWaitTail != &fixture.wait || operationCandidateState(&slot.record) != OperationCommitIdle ||
		operationCandidateIsPublished(&slot.record) || slot.record.resultState != operationResultEmpty ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxReady) ||
		selectClaimLoad(fixture.claim) != selectClaimCommitting {
		t.Fatalf("Committing discovery retained resolver: progress=%+v resolve=%+v park=%+v record=%+v claim=%d",
			progress, fixture.driver.poll.resolve, fixture.task.g.park, slot.record, selectClaimLoad(fixture.claim))
	}
	beforeRecord := slot.record
	if admission.publishExternallyCommitted() != ChannelOperationPosted || !fixture.source.beginPublishPass(fixture.p) {
		t.Fatal("complete external effect after Committing discovery yield")
	}
	if published, lost, publishOK := fixture.source.publishSlot(fixture.p, 0); !publishOK || published != 0 || lost != 0 ||
		slot.record != beforeRecord || preemptLoad(&slot.mailbox) != uint32(channelMailboxForced) ||
		selectClaimLoad(fixture.claim) != selectClaimCommitting || !fixture.source.Pending() {
		t.Fatalf("Committing forced drain touched owner record: (%d,%d,%t) record=%+v mailbox=%d claim=%d pending=%t",
			published, lost, publishOK, slot.record, preemptLoad(&slot.mailbox),
			selectClaimLoad(fixture.claim), fixture.source.Pending())
	}
	if !publishExternalSelectClaim(fixture.claim) || !admission.releaseCommitted() {
		t.Fatal("publish external claim after sticky Committing forced drain")
	}
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 33 || !decision.lease.Valid() {
		t.Fatalf("post-Committing forced decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelExternallyCommittedOvertakesPausedReadyDrain(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-forced-paused-drain", []uint32{41, 42}, true, 0)
	id := fixture.ids[0]
	slot, _ := channelOperationSlotFor(fixture.source, id)
	if result := fixture.source.PostReady(id); result != ChannelOperationPosted ||
		!preemptCompareAndSwap(&slot.mailbox, uint32(channelMailboxReady), uint32(channelMailboxDrainingReady)) {
		t.Fatal("pause ordinary channel Ready drain")
	}
	admission, acquired := fixture.source.acquireExternalCommit(id)
	if acquired != channelExternalCommitAcquired {
		t.Fatalf("admit forced publication behind Ready drain = %d", acquired)
	}
	if !preemptCompareAndSwap(&fixture.claim.state, selectClaimOpen, selectClaimAcquiring) {
		_ = admission.releaseWithoutCommit()
		t.Fatal("acquire forced select claim under admission")
	}
	if !beginExternalSelectClaimEffect(fixture.claim) {
		t.Fatal("begin forced effect behind Ready drain")
	}
	forcedResult := admission.publishExternallyCommitted()
	if forcedResult != ChannelOperationPosted ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxForcedBehindReadyDrain) ||
		preemptLoad(&slot.physical) != uint32(channelPhysicalCommitted) {
		t.Fatalf("forced publication behind Ready drain = result:%d mailbox:%d physical:%d",
			forcedResult, preemptLoad(&slot.mailbox), preemptLoad(&slot.physical))
	}
	beforeRecord := slot.record
	if published, lost, publishOK := fixture.source.publishDrainedSlot(fixture.p, slot, channelMailboxReady); !publishOK || published != 0 || lost != 0 || slot.record != beforeRecord ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxForced) || !fixture.source.Pending() ||
		selectClaimLoad(fixture.claim) != selectClaimCommitting {
		t.Fatalf("Committing Ready drain did not preserve sticky forced handoff: (%d,%d,%t) record=%+v mailbox=%d pending=%t",
			published, lost, publishOK, slot.record, preemptLoad(&slot.mailbox), fixture.source.Pending())
	}
	if !publishExternalSelectClaim(fixture.claim) || !admission.releaseCommitted() ||
		!fixture.source.beginPublishPass(fixture.p) {
		t.Fatal("publish forced claim/pass after paused drain")
	}
	if published, lost, ok := fixture.source.publishSlot(fixture.p, 0); !ok || published != 1 || lost != 0 ||
		!operationCandidateExternallyCommitted(&slot.record) || preemptLoad(&slot.mailbox) != uint32(channelMailboxEmpty) {
		t.Fatalf("drain sticky forced handoff = (%d,%d,%t), record=%+v mailbox=%d",
			published, lost, ok, slot.record, preemptLoad(&slot.mailbox))
	}
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 41 || !decision.lease.Valid() {
		t.Fatalf("paused-drain forced decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelReadyObservationReloadsForcedBeforeDrainCAS(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-ready-forced-cas-race", []uint32{43, 44}, true, 0)
	id := fixture.ids[0]
	slot, _ := channelOperationSlotFor(fixture.source, id)
	if result := fixture.source.PostReady(id); result != ChannelOperationPosted {
		t.Fatalf("post Ready before forced CAS race = %d", result)
	}
	observed := channelOperationMailbox(preemptLoad(&slot.mailbox))
	admission, acquired := fixture.source.acquireExternalCommit(id)
	if acquired != channelExternalCommitAcquired ||
		!preemptCompareAndSwap(&fixture.claim.state, selectClaimOpen, selectClaimAcquiring) {
		_ = admission.releaseWithoutCommit()
		t.Fatal("admit forced producer before owner Ready CAS")
	}
	if !beginExternalSelectClaimEffect(fixture.claim) ||
		admission.publishExternallyCommitted() != ChannelOperationPosted ||
		!publishExternalSelectClaim(fixture.claim) || !admission.releaseCommitted() ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxForced) {
		t.Fatal("publish Forced over owner's stale Ready observation")
	}
	drained, drainOK := beginChannelMailboxDrain(slot, observed)
	if !drainOK || drained != channelMailboxForced ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxDrainingForced) ||
		!restoreChannelMailboxDrain(fixture.source, slot, drained) {
		t.Fatalf("stale Ready CAS did not reload Forced: drained=%d ok=%t mailbox=%d",
			drained, drainOK, preemptLoad(&slot.mailbox))
	}
	if !fixture.source.beginPublishPass(fixture.p) {
		t.Fatal("begin forced CAS-race publication pass")
	}
	if published, lost, ok := fixture.source.publishSlot(fixture.p, 0); !ok || published != 1 || lost != 0 ||
		!operationCandidateExternallyCommitted(&slot.record) {
		t.Fatalf("publish reloaded Forced = (%d,%d,%t), record=%+v", published, lost, ok, slot.record)
	}
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 43 || !decision.lease.Valid() {
		t.Fatalf("Ready/Forced CAS-race decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelExternalCommitAdmissionSurvivesConcurrentCloseSeal(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-forced-close-race", []uint32{45, 46}, true, 0)
	id := fixture.ids[0]
	admission, acquired := fixture.source.acquireExternalCommit(id)
	if acquired != channelExternalCommitAcquired {
		t.Fatalf("acquire pre-effect channel admission = %d", acquired)
	}
	slot, _ := channelOperationSlotFor(fixture.source, id)
	if !preemptCompareAndSwap(&fixture.claim.state, selectClaimOpen, selectClaimAcquiring) ||
		!preemptCompareAndSwap(&fixture.claim.state, selectClaimAcquiring, selectClaimOpen) ||
		!admission.releaseWithoutCommit() || preemptLoad(&slot.inflight) != 0 ||
		preemptLoad(&slot.physical) != uint32(channelPhysicalIdle) ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxEmpty) || fixture.source.Pending() {
		t.Fatal("pre-effect external admission did not roll back without publication")
	}
	admission, acquired = fixture.source.acquireExternalCommit(id)
	if acquired != channelExternalCommitAcquired {
		t.Fatalf("reacquire pre-effect channel admission = %d", acquired)
	}
	if !preemptCompareAndSwap(&fixture.claim.state, selectClaimOpen, selectClaimAcquiring) {
		_ = admission.releaseWithoutCommit()
		t.Fatal("reacquire select claim under held admission")
	}
	// The executor may already have been requested by another source fact. Its
	// owner must remain unable to detach/reclaim this frame until the external
	// producer has published Claimed and released the lifetime admission.
	requestChannelClaimCoreFixture(t, fixture)
	if result := fixture.source.BeginClose(fixture.p, id); result != ChannelOperationCloseStarted {
		t.Fatalf("seal channel source behind admitted physical commit = %d", result)
	}
	if preemptLoad(&slot.inflight) != producerAdmissionClosed|1 ||
		!beginExternalSelectClaimEffect(fixture.claim) ||
		admission.publishExternallyCommitted() != ChannelOperationPosted ||
		preemptLoad(&slot.inflight) != producerAdmissionClosed|1 ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxForced) {
		t.Fatalf("held external publication lost across close: inflight=%#x mailbox=%d",
			preemptLoad(&slot.inflight), preemptLoad(&slot.mailbox))
	}
	if !publishExternalSelectClaim(fixture.claim) {
		t.Fatal("publish forced claim after concurrent close")
	}
	progress := pollChannelClaimCoreComplete(t, fixture)
	if !progress.More || preemptLoad(&slot.inflight) != producerAdmissionClosed|1 ||
		preemptLoad(&slot.state) != uint32(producerSourceClosing) || slot.record.phase != operationActive ||
		slot.claim != fixture.claim || fixture.task.g.park.phase != parkDetaching ||
		fixture.source.ConfirmQuiesced(fixture.p, id) || fixture.source.ResetSelectClaim(fixture.p, fixture.claim) {
		t.Fatalf("held admission failed to pin frame lifetime: progress=%+v inflight=%#x state=%d phase=%d claim=%p park=%d",
			progress, preemptLoad(&slot.inflight), preemptLoad(&slot.state), slot.record.phase,
			slot.claim, fixture.task.g.park.phase)
	}
	if g, ok := NextRunnable(fixture.p); !ok || g != nil {
		t.Fatalf("held external admission promoted task early = (%p,%t)", g, ok)
	}
	if admission.releaseWithoutCommit() || !admission.releaseCommitted() ||
		preemptLoad(&slot.inflight) != producerAdmissionClosed {
		t.Fatal("release committed external lifetime admission")
	}
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 45 || !decision.lease.Valid() {
		t.Fatalf("close-race forced decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelClaimlessSingleRejectsExternalCommitUntilC1Fence(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-single-local-only", []uint32{47}, false, 0)
	admission, result := fixture.source.acquireExternalCommit(fixture.ids[0])
	if result != channelExternalCommitAcquireUnsupported || admission != (channelExternalCommitAdmission{}) {
		t.Fatalf("claim-less C0 external admission = (%+v,%d)", admission, result)
	}
	if posted := fixture.source.PostReady(fixture.ids[0]); posted != ChannelOperationPosted {
		t.Fatalf("claim-less local Ready post = %d", posted)
	}
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 47 || !decision.lease.Valid() {
		t.Fatalf("claim-less local decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelCommitDomainDiscoveryRejectsMismatchedClaims(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-mismatched-claims", []uint32{48, 49}, true, 0)
	second, _ := channelOperationSlotFor(fixture.source, fixture.ids[1])
	second.claim = new(SelectClaim)
	if posted := fixture.source.PostReady(fixture.ids[0]); posted != ChannelOperationPosted {
		t.Fatalf("post mismatched-claim readiness = %d", posted)
	}
	requestChannelClaimCoreFixture(t, fixture)
	if progress, ok := PollExecutorSlice(fixture.driver, 1000); ok || progress != (ExecutorPollProgress{}) {
		t.Fatalf("mismatched channel claims were accepted = (%+v,%t)", progress, ok)
	}
	second.claim = fixture.claim
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 48 || !decision.lease.Valid() {
		t.Fatalf("repaired claim-domain decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelRouteSkeletonRejectsGenericEarlyDoorbell(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-route-skeleton", []uint32{50}, true, 0)
	routes := new(OperationRouteRegistry)
	route, allocated := routes.Allocate()
	if !allocated || route != fixture.driver.route || !routes.Bind(route, fixture.driver) {
		t.Fatalf("bind channel operation route = (%d,%t)", route, allocated)
	}
	result := routes.PostAndRequest(fixture.ids[0], TaskCancelNone)
	slot, _ := channelOperationSlotFor(fixture.source, fixture.ids[0])
	if result != (OperationRouteIngressResult{Route: OperationRoutePostInvalid, Executor: ExecutorRequestInvalid}) ||
		fixture.source.Pending() || preemptLoad(&slot.mailbox) != uint32(channelMailboxEmpty) ||
		fixture.registry.ObserveRequested(fixture.handle) {
		t.Fatalf("generic route crossed channel ordering = result:%+v pending:%t mailbox:%d requested:%t",
			result, fixture.source.Pending(), preemptLoad(&slot.mailbox), fixture.registry.ObserveRequested(fixture.handle))
	}
	stale := fixture.ids[0]
	stale.Generation++
	if posted := fixture.source.PostReady(stale); posted != ChannelOperationPostStale || fixture.source.Pending() {
		t.Fatalf("stale channel post = %d pending=%t", posted, fixture.source.Pending())
	}
	if posted := fixture.source.PostReady(fixture.ids[0]); posted != ChannelOperationPosted {
		t.Fatalf("post routed channel readiness = %d", posted)
	}
	if duplicate := fixture.source.PostReady(fixture.ids[0]); duplicate != ChannelOperationPostDuplicate {
		t.Fatalf("duplicate channel readiness = %d", duplicate)
	}
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	if !routes.BeginClose(route) || !routes.ConfirmQuiesced(route) || !routes.Retire(route) || !routes.AllRetired() {
		t.Fatal("retire channel route skeleton")
	}
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 50 || !decision.lease.Valid() {
		t.Fatalf("channel route-skeleton decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelExternallyCommittedBeatsDefaultAndOrdinaryCancel(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-forced-winner", []uint32{51, 52}, true, 999)
	externallyCommitChannelCandidate(t, fixture, 1)
	if !RequestWaitSetCancel(fixture.p, &fixture.wait, ParkCancelOperation) {
		t.Fatal("publish ordinary cancel beside forced channel result")
	}
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	leaseID, leaseOK := decision.lease.ID()
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 52 || !leaseOK || leaseID != fixture.ids[1] {
		t.Fatalf("forced/default/cancel decision = %+v leaseID=(%+v,%t)", decision, leaseID, leaseOK)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelExternallyCommittedStrongCancelDiscardsWithoutRollback(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-forced-strong-cancel", []uint32{61, 62}, true, 0)
	externallyCommitChannelCandidate(t, fixture, 0)
	if !RequestWaitSetCancel(fixture.p, &fixture.wait, ParkCancelTaskAbort) {
		t.Fatal("publish strong cancel beside forced channel result")
	}
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	slot, _ := channelOperationSlotFor(fixture.source, fixture.ids[0])
	if slot.record.disposition != OperationDispositionCanceled ||
		operationCandidateState(&slot.record) != OperationCommitCommitted ||
		slot.record.resultState != operationResultDiscarded || slot.record.resultTicket != (ParkTicket{}) ||
		preemptLoad(&slot.physical) != uint32(channelPhysicalCommitted) || slot.record.phase != operationDetached {
		t.Fatalf("forced strong-cancel ownership = disposition:%d candidate:%d result:%d ticket:%+v physical:%d phase:%d",
			slot.record.disposition, operationCandidateState(&slot.record), slot.record.resultState,
			slot.record.resultTicket, preemptLoad(&slot.physical), slot.record.phase)
	}
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCanceled || decision.caseID != 0 || decision.lease != (OperationResultLease{}) {
		t.Fatalf("forced strong-cancel decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelDeferredForcedRestoresAThenBWithoutFIFOCycle(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-forced-a-after", []uint32{71, 72}, true, 0)
	id := fixture.ids[0]
	if result := fixture.source.PostReady(id); result != ChannelOperationPosted {
		t.Fatalf("post A readiness = %d", result)
	}
	requestChannelClaimCoreFixture(t, fixture)
	for step := 0; step < 1000; step++ {
		progress, ok := PollExecutorSlice(fixture.driver, 1)
		if !ok || progress.Complete {
			t.Fatalf("advance to Channel A cursor step %d = (%+v,%t)", step, progress, ok)
		}
		if fixture.driver.poll.phase == executorPollEpochAPublish &&
			fixture.driver.poll.source == executorCatalogChannel && fixture.driver.poll.cursor == 1 {
			break
		}
		if step == 999 {
			t.Fatal("did not reach Channel A cursor after exact slot")
		}
	}
	// The ordinary Ready owner fact is in A, but this irreversible peer fact is
	// behind A's Channel cursor. Mailbox publication precedes Claimed and the
	// second request/doorbell exactly as required by the hchan shim contract.
	admission, acquired := fixture.source.acquireExternalCommit(id)
	if acquired != channelExternalCommitAcquired {
		t.Fatalf("admit exact forced fact behind Channel A cursor = %d", acquired)
	}
	if !preemptCompareAndSwap(&fixture.claim.state, selectClaimOpen, selectClaimAcquiring) {
		_ = admission.releaseWithoutCommit()
		t.Fatal("acquire exact forced claim behind Channel A cursor")
	}
	if !beginExternalSelectClaimEffect(fixture.claim) ||
		admission.publishExternallyCommitted() != ChannelOperationPosted ||
		!publishExternalSelectClaim(fixture.claim) || !admission.releaseCommitted() {
		t.Fatal("publish exact forced fact behind Channel A cursor")
	}
	requestChannelClaimCoreFixture(t, fixture)
	for step := 0; step < 1000 && fixture.driver.poll.phase != executorPollAcknowledge; step++ {
		progress, ok := PollExecutorSlice(fixture.driver, 1)
		if !ok || progress.Complete {
			t.Fatalf("advance deferred forced abort step %d = (%+v,%t)", step, progress, ok)
		}
	}
	slot, _ := channelOperationSlotFor(fixture.source, id)
	if fixture.driver.poll.phase != executorPollAcknowledge || fixture.p.affectedWaitHead != &fixture.wait ||
		fixture.p.affectedWaitTail != &fixture.wait || fixture.wait.workNext != nil ||
		fixture.wait.work != waitSetWorkQueued || fixture.task.g.park.resolving ||
		operationCandidateState(&slot.record) != OperationCommitReady || slot.record.resultState != operationResultEmpty {
		t.Fatalf("deferred forced A restore = poll:%+v affected:(%p,%p) work:%d next:%p parkResolving:%t candidate:%d result:%d",
			fixture.driver.poll, fixture.p.affectedWaitHead, fixture.p.affectedWaitTail, fixture.wait.work,
			fixture.wait.workNext, fixture.task.g.park.resolving, operationCandidateState(&slot.record), slot.record.resultState)
	}
	for link, visits := fixture.p.affectedWaitHead, 0; link != nil; link, visits = link.workNext, visits+1 {
		if visits != 0 {
			t.Fatal("deferred forced restore introduced an affected FIFO cycle")
		}
	}
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 71 || !decision.lease.Valid() {
		t.Fatalf("deferred forced B decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelExternalValidationRacesTaskCancellationAndPreservesEffectSemantics(t *testing.T) {
	a := newChannelClaimCoreFixture(t, "channel-cancel-race-a", []uint32{73}, true, 0)
	b := newChannelClaimCoreFixture(t, "channel-cancel-race-b", []uint32{74}, true, 0)
	var pair channelExternalCommitPair
	if result := beginChannelExternalCommitPair(
		&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim,
	); result != channelExternalCommitPairBeginPrepared {
		t.Fatalf("prepare cancellation-race pair = (%d,%+v)", result, pair)
	}

	start := make(chan struct{})
	validated := make(chan bool, 1)
	go func() {
		<-start
		valid := true
		for iteration := 0; iteration < 1<<15; iteration++ {
			if !validChannelExternalEndpointHeld(&pair.endpointA, a.claim) ||
				!validChannelExternalEndpointHeld(&pair.endpointB, b.claim) {
				valid = false
				break
			}
			runtime.Gosched()
		}
		validated <- valid
	}()
	close(start)
	for iteration := 0; iteration < 1<<15; iteration++ {
		if !RequestTaskCancellation(a.p, a.task.g, TaskCancelAbort) ||
			!RequestTaskCancellation(b.p, b.task.g, TaskCancelAbort) {
			t.Fatalf("publish task cancellation during external validation at %d", iteration)
		}
		runtime.Gosched()
	}
	if !<-validated || pair.phase != channelExternalCommitPairPrepared ||
		selectClaimLoad(a.claim) != selectClaimAcquiring || selectClaimLoad(b.claim) != selectClaimAcquiring {
		t.Fatalf("cancellation invalidated pre-effect endpoint identity: pair=%+v claims=(%d,%d)",
			pair, selectClaimLoad(a.claim), selectClaimLoad(b.claim))
	}
	if !pair.beginEffect() || !pair.commit() {
		t.Fatal("strong cancellation incorrectly prohibited the physical channel effect")
	}

	for _, fixture := range []*channelClaimCoreFixture{a, b} {
		requestChannelClaimCoreFixture(t, fixture)
		pollChannelClaimCoreComplete(t, fixture)
		slot, _ := channelOperationSlotFor(fixture.source, fixture.ids[0])
		if slot.record.disposition != OperationDispositionCanceled ||
			operationCandidateState(&slot.record) != OperationCommitCommitted ||
			slot.record.resultState != operationResultDiscarded ||
			preemptLoad(&slot.physical) != uint32(channelPhysicalCommitted) {
			t.Fatalf("strong cancellation lost committed physical ownership: record=%+v physical=%d",
				slot.record, preemptLoad(&slot.physical))
		}
		decision := takeChannelClaimCoreDecision(t, fixture)
		if decision.outcome != ParkOutcomeCanceled || decision.caseID != 0 ||
			decision.lease != (OperationResultLease{}) || decision.taskCancel != TaskCancelAbort {
			t.Fatalf("cancellation-race decision = %+v", decision)
		}
		releaseChannelClaimCoreFixture(t, fixture, decision)
	}
}

func TestChannelExternalValidationIgnoresUnrelatedActiveQueueMutation(t *testing.T) {
	a := newChannelClaimCoreFixture(t, "channel-neighbor-race-a", []uint32{141}, true, 0)
	b := newChannelClaimCoreFixture(t, "channel-neighbor-race-b", []uint32{142}, true, 0)
	var pair channelExternalCommitPair
	if result := beginChannelExternalCommitPair(
		&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim,
	); result != channelExternalCommitPairBeginPrepared {
		t.Fatalf("prepare active-neighbor race pair = (%d,%+v)", result, pair)
	}

	// Another G may join and leave the same P's active queue while this target
	// remains pinned by its admission and SelectClaim. These owner-only queue
	// fields are outside the external endpoint validation domain.
	neighbor := new(WaitSetRecord)
	mutated := make(chan struct{})
	mutationDone := make(chan struct{})
	go func() {
		close(mutated)
		for iteration := 0; iteration < 1<<15; iteration++ {
			neighbor.activeNext = &a.wait
			a.wait.activePrev = neighbor
			a.p.parkWaitHead = neighbor
			runtime.Gosched()
			a.p.parkWaitHead = &a.wait
			a.wait.activePrev = nil
			neighbor.activeNext = nil
		}
		close(mutationDone)
	}()
	<-mutated
	valid := true
	for iteration := 0; iteration < 1<<15; iteration++ {
		if !validChannelExternalEndpointHeld(&pair.endpointA, a.claim) ||
			!validChannelExternalEndpointHeld(&pair.endpointB, b.claim) {
			valid = false
			break
		}
		runtime.Gosched()
	}
	<-mutationDone
	if !valid || a.p.parkWaitHead != &a.wait || a.p.parkWaitTail != &a.wait ||
		a.wait.activePrev != nil || a.wait.activeNext != nil || neighbor.activeNext != nil ||
		!validChannelExternalEndpointHeld(&pair.endpointA, a.claim) ||
		!validChannelExternalEndpointHeld(&pair.endpointB, b.claim) || !pair.abort() {
		t.Fatalf("unrelated active-queue mutation invalidated held endpoint: valid=%t pair=%+v queue=(%p,%p) target=(%p,%p)",
			valid, pair, a.p.parkWaitHead, a.p.parkWaitTail, a.wait.activePrev, a.wait.activeNext)
	}

	for _, fixture := range []*channelClaimCoreFixture{a, b} {
		if result := fixture.source.PostReady(fixture.ids[0]); result != ChannelOperationPosted {
			t.Fatalf("post active-neighbor cleanup = %d", result)
		}
		requestChannelClaimCoreFixture(t, fixture)
		pollChannelClaimCoreComplete(t, fixture)
		decision := takeChannelClaimCoreDecision(t, fixture)
		if decision.outcome != ParkOutcomeCompleted || !decision.lease.Valid() {
			t.Fatalf("active-neighbor cleanup decision = %+v", decision)
		}
		releaseChannelClaimCoreFixture(t, fixture, decision)
	}
}

func TestChannelReadyPublisherAndExternalBeginShareClaimDomain(t *testing.T) {
	a := newChannelClaimCoreFixture(t, "channel-publish-claim-a", []uint32{75}, true, 0)
	b := newChannelClaimCoreFixture(t, "channel-publish-claim-b", []uint32{76}, true, 0)
	if result := a.source.PostReady(a.ids[0]); result != ChannelOperationPosted {
		t.Fatalf("post readiness before claim-domain race = %d", result)
	}
	var pair channelExternalCommitPair
	if result := beginChannelExternalCommitPair(
		&pair, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim,
	); result != channelExternalCommitPairBeginPrepared {
		t.Fatalf("prepare publisher-race pair = (%d,%+v)", result, pair)
	}
	slot, _ := channelOperationSlotFor(a.source, a.ids[0])
	beforeRecord := slot.record
	start := make(chan struct{})
	published := make(chan bool, 1)
	go func() {
		<-start
		valid := true
		for iteration := 0; iteration < 1<<12; iteration++ {
			completed, lost, ok := a.source.publishSlot(a.p, 0)
			if !ok || completed != 0 || lost != 0 {
				valid = false
				break
			}
			runtime.Gosched()
		}
		published <- valid
	}()
	close(start)
	for iteration := 0; iteration < 1<<12; iteration++ {
		if !validChannelExternalEndpointHeld(&pair.endpointA, a.claim) ||
			!validChannelExternalEndpointHeld(&pair.endpointB, b.claim) {
			t.Fatalf("publisher contention invalidated held endpoints at %d", iteration)
		}
		runtime.Gosched()
	}
	if !<-published || slot.record != beforeRecord ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxReady) ||
		selectClaimLoad(a.claim) != selectClaimAcquiring || !pair.abort() {
		t.Fatalf("claim-contended publisher touched record: record=%+v mailbox=%d claim=%d pair=%+v",
			slot.record, preemptLoad(&slot.mailbox), selectClaimLoad(a.claim), pair)
	}
	if !a.source.beginPublishPass(a.p) {
		t.Fatal("begin owner publication after external abort")
	}
	if completed, lost, ok := a.source.publishSlot(a.p, 0); !ok || completed != 1 || lost != 0 ||
		operationCandidateState(&slot.record) != OperationCommitReady ||
		selectClaimLoad(a.claim) != selectClaimOpen {
		t.Fatalf("owner publication after claim release = (%d,%d,%t), record=%+v claim=%d",
			completed, lost, ok, slot.record, selectClaimLoad(a.claim))
	}
	if result := b.source.PostReady(b.ids[0]); result != ChannelOperationPosted {
		t.Fatalf("post publisher-race peer cleanup = %d", result)
	}
	for _, fixture := range []*channelClaimCoreFixture{a, b} {
		requestChannelClaimCoreFixture(t, fixture)
		pollChannelClaimCoreComplete(t, fixture)
		decision := takeChannelClaimCoreDecision(t, fixture)
		if decision.outcome != ParkOutcomeCompleted || !decision.lease.Valid() {
			t.Fatalf("publisher-race cleanup decision = %+v", decision)
		}
		releaseChannelClaimCoreFixture(t, fixture, decision)
	}
}

func TestChannelPairRawReleaseRequiresPreparedPhase(t *testing.T) {
	a := newChannelClaimCoreFixture(t, "channel-release-phase-a", []uint32{77}, true, 0)
	b := newChannelClaimCoreFixture(t, "channel-release-phase-b", []uint32{78}, true, 0)
	var effect channelExternalCommitPair
	if result := beginChannelExternalCommitPair(
		&effect, a.source, a.ids[0], a.claim, b.source, b.ids[0], b.claim,
	); result != channelExternalCommitPairBeginPrepared || !effect.beginEffect() {
		t.Fatalf("enter Effect for raw-release gate = (%d,%+v)", result, effect)
	}
	beforeA, beforeB := effect.endpointA, effect.endpointB
	beforeInflightA := preemptLoad(&beforeA.slot.inflight)
	beforeInflightB := preemptLoad(&beforeB.slot.inflight)
	if releaseChannelExternalCommitPairWithoutEffect(&effect) || effect.phase != channelExternalCommitPairEffect ||
		effect.endpointA != beforeA || effect.endpointB != beforeB ||
		preemptLoad(&beforeA.slot.inflight) != beforeInflightA ||
		preemptLoad(&beforeB.slot.inflight) != beforeInflightB ||
		selectClaimLoad(a.claim) != selectClaimCommitting || selectClaimLoad(b.claim) != selectClaimCommitting {
		t.Fatalf("raw release crossed Effect boundary: pair=%+v inflight=(%#x,%#x) claims=(%d,%d)",
			effect, preemptLoad(&beforeA.slot.inflight), preemptLoad(&beforeB.slot.inflight),
			selectClaimLoad(a.claim), selectClaimLoad(b.claim))
	}
	if !effect.commit() {
		t.Fatal("commit pair after rejected raw Effect release")
	}
	for _, fixture := range []*channelClaimCoreFixture{a, b} {
		requestChannelClaimCoreFixture(t, fixture)
		pollChannelClaimCoreComplete(t, fixture)
		decision := takeChannelClaimCoreDecision(t, fixture)
		if decision.outcome != ParkOutcomeCompleted || !decision.lease.Valid() {
			t.Fatalf("Effect raw-release cleanup decision = %+v", decision)
		}
		releaseChannelClaimCoreFixture(t, fixture, decision)
	}

	c := newChannelClaimCoreFixture(t, "channel-release-broken-c", []uint32{79}, true, 0)
	d := newChannelClaimCoreFixture(t, "channel-release-broken-d", []uint32{80}, true, 0)
	var broken channelExternalCommitPair
	if result := beginChannelExternalCommitPair(
		&broken, c.source, c.ids[0], c.claim, d.source, d.ids[0], d.claim,
	); result != channelExternalCommitPairBeginPrepared {
		t.Fatalf("prepare Broken raw-release gate = (%d,%+v)", result, broken)
	}
	broken.phase = channelExternalCommitPairBroken
	beforeA, beforeB = broken.endpointA, broken.endpointB
	beforeInflightA = preemptLoad(&beforeA.slot.inflight)
	beforeInflightB = preemptLoad(&beforeB.slot.inflight)
	if releaseChannelExternalCommitPairWithoutEffect(&broken) || broken.phase != channelExternalCommitPairBroken ||
		broken.endpointA != beforeA || broken.endpointB != beforeB ||
		preemptLoad(&beforeA.slot.inflight) != beforeInflightA ||
		preemptLoad(&beforeB.slot.inflight) != beforeInflightB ||
		selectClaimLoad(c.claim) != selectClaimAcquiring || selectClaimLoad(d.claim) != selectClaimAcquiring {
		t.Fatalf("raw release crossed Broken boundary: pair=%+v inflight=(%#x,%#x) claims=(%d,%d)",
			broken, preemptLoad(&beforeA.slot.inflight), preemptLoad(&beforeB.slot.inflight),
			selectClaimLoad(c.claim), selectClaimLoad(d.claim))
	}
	broken.phase = channelExternalCommitPairPrepared
	if !broken.abort() {
		t.Fatal("clean up synthetic Broken raw-release fixture")
	}
	for _, fixture := range []*channelClaimCoreFixture{c, d} {
		if result := fixture.source.PostReady(fixture.ids[0]); result != ChannelOperationPosted {
			t.Fatalf("post Broken raw-release cleanup = %d", result)
		}
		requestChannelClaimCoreFixture(t, fixture)
		pollChannelClaimCoreComplete(t, fixture)
		decision := takeChannelClaimCoreDecision(t, fixture)
		if decision.outcome != ParkOutcomeCompleted || !decision.lease.Valid() {
			t.Fatalf("Broken raw-release cleanup decision = %+v", decision)
		}
		releaseChannelClaimCoreFixture(t, fixture, decision)
	}
}

func TestChannelCompatibilityResolversRejectForcedShapeWithoutMutation(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-legacy-forced-reject", []uint32{91}, true, 0)
	externallyCommitChannelCandidate(t, fixture, 0)
	if !fixture.source.beginPublishPass(fixture.p) {
		t.Fatal("begin forced compatibility publication pass")
	}
	slot, _ := channelOperationSlotFor(fixture.source, fixture.ids[0])
	if published, lost, ok := fixture.source.publishSlot(fixture.p, 0); !ok || published != 1 || lost != 0 ||
		!operationCandidateExternallyCommitted(&slot.record) {
		t.Fatalf("publish forced compatibility candidate = (%d,%d,%t), record=%+v",
			published, lost, ok, slot.record)
	}

	assertUnchanged := func(name string, state ParkState, record OperationRecord, wait WaitSetRecord,
		head, tail *WaitSetRecord) {
		t.Helper()
		if fixture.task.g.park != state || slot.record != record || fixture.wait != wait ||
			fixture.p.affectedWaitHead != head || fixture.p.affectedWaitTail != tail {
			t.Fatalf("%s mutated claim-aware state: park=%+v record=%+v wait=%+v affected=(%p,%p)",
				name, fixture.task.g.park, slot.record, fixture.wait,
				fixture.p.affectedWaitHead, fixture.p.affectedWaitTail)
		}
	}
	beforeState, beforeRecord, beforeWait := fixture.task.g.park, slot.record, fixture.wait
	beforeHead, beforeTail := fixture.p.affectedWaitHead, fixture.p.affectedWaitTail
	if resolution, request, status := ResolveParkSnapshotStep(
		&fixture.task.g.park, fixture.ticket, ParkCommitAttempt{},
	); status != ParkResolveInvalid || resolution != (CompletionResolution{}) || request != (ParkCommitRequest{}) {
		t.Fatalf("generic Step accepted forced Channel shape = (%+v,%+v,%d)", resolution, request, status)
	}
	assertUnchanged("ResolveParkSnapshotStep", beforeState, beforeRecord, beforeWait, beforeHead, beforeTail)
	if resolution, ok := ResolveParkSnapshot(&fixture.task.g.park, fixture.ticket); ok || resolution != (CompletionResolution{}) {
		t.Fatalf("generic Resolve accepted forced Channel shape = (%+v,%t)", resolution, ok)
	}
	assertUnchanged("ResolveParkSnapshot", beforeState, beforeRecord, beforeWait, beforeHead, beforeTail)
	if head, tail, resolution, ok := resolveAffectedWaitSets(fixture.p, &fixture.driver.sources); ok || head != nil || tail != nil || resolution != (CompletionResolution{}) {
		t.Fatalf("legacy affected resolver accepted forced Channel shape = (%p,%p,%+v,%t)",
			head, tail, resolution, ok)
	}
	assertUnchanged("resolveAffectedWaitSets", beforeState, beforeRecord, beforeWait, beforeHead, beforeTail)

	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 91 || !decision.lease.Valid() {
		t.Fatalf("claim-aware compatibility cleanup decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelReservationRejectsSplitSelectClaimDomainsBeforeVisibility(t *testing.T) {
	tests := []struct {
		name        string
		firstClaim  bool
		secondClaim bool
		distinct    bool
	}{
		{name: "distinct-claims", firstClaim: true, secondClaim: true, distinct: true},
		{name: "claim-then-nil", firstClaim: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := new(P)
			source := new(ChannelOperationSource)
			if !BindChannelOperationSource(source, p) {
				t.Fatal("bind split-claim source")
			}
			g := new(G)
			if !InitG(g) {
				t.Fatal("initialize split-claim G")
			}
			ticket, ok := BeginParkSet(&g.park, 2, 107)
			wait := new(WaitSetRecord)
			if !ok || !PrepareWaitSetRecord(wait, g, ticket) {
				t.Fatal("prepare split-claim wait")
			}
			var firstClaim, secondClaim *SelectClaim
			if test.firstClaim {
				firstClaim = new(SelectClaim)
			}
			if test.secondClaim {
				if test.distinct || firstClaim == nil {
					secondClaim = new(SelectClaim)
				} else {
					secondClaim = firstClaim
				}
			}
			firstID, attached := source.ReserveAndAttachWait(p, &g.park, ticket, wait, 1, firstClaim)
			if !attached || firstID.LocalSlot() != 1 {
				t.Fatalf("reserve first split-claim case = (%+v,%t)", firstID, attached)
			}
			firstSlot, _ := channelOperationSlotFor(source, firstID)
			beforeState, beforeFirst, beforeSecond := g.park, firstSlot.record, source.slots[1]
			secondID, secondAttached := source.ReserveAndAttachWait(p, &g.park, ticket, wait, 2, secondClaim)
			if secondAttached || secondID != (OperationID{}) || g.park != beforeState ||
				firstSlot.record != beforeFirst || source.slots[1] != beforeSecond ||
				preemptLoad(&source.slots[1].state) != uint32(producerSourceFree) ||
				preemptLoad(&source.slots[1].generation) != 0 ||
				selectClaimLoad(firstClaim) != selectClaimOpen || selectClaimLoad(secondClaim) != selectClaimOpen {
				t.Fatalf("split claim became producer-visible: id=%+v attached=%t park=%+v slot=%+v claims=(%d,%d)",
					secondID, secondAttached, g.park, source.slots[1],
					selectClaimLoad(firstClaim), selectClaimLoad(secondClaim))
			}
			if admission, result := source.acquireExternalCommit(secondID); result != channelExternalCommitAcquireInvalid || admission != (channelExternalCommitAdmission{}) ||
				preemptLoad(&source.slots[1].physical) != uint32(channelPhysicalIdle) {
				t.Fatalf("rejected second peer entered effect path = (%+v,%d)", admission, result)
			}

			if firstClaim != nil && !source.AbortSelectPreparation(p, &g.park, ticket, wait, firstClaim) {
				t.Fatal("terminalize first split claim")
			} else if firstClaim == nil && !AbortParkSet(&g.park, ticket) {
				t.Fatal("abort claim-less split preparation")
			}
			if source.ApplyOne(p, firstID, &firstSlot.record) != OperationApplyDetached ||
				!source.ConfirmQuiesced(p, firstID) {
				t.Fatal("apply first split-claim operation")
			}
			if firstClaim != nil && !source.ResetSelectClaim(p, firstClaim) {
				t.Fatal("reset first split claim")
			}
			if !source.Recycle(p, firstID) {
				t.Fatal("recycle first split-claim operation")
			}
			if outcome, _, lease, consumed := ConsumeParkSet(&g.park, ticket); !consumed || outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) ||
				!ReleasePreparedWaitSetRecord(wait) {
				t.Fatalf("consume split-claim abort = (%d,%+v,%t)", outcome, lease, consumed)
			}
			if !UnbindChannelOperationSource(source, p) || !source.CanRelease() {
				t.Fatal("release split-claim source")
			}
		})
	}
}

func TestChannelReservationRejectsMultiCandidateClaimlessDomainBeforeVisibility(t *testing.T) {
	p := new(P)
	source := new(ChannelOperationSource)
	if !BindChannelOperationSource(source, p) {
		t.Fatal("bind multi-candidate claim-less source")
	}
	g := new(G)
	if !InitG(g) {
		t.Fatal("initialize multi-candidate claim-less G")
	}
	ticket, ok := BeginParkSet(&g.park, 2, 108)
	wait := new(WaitSetRecord)
	if !ok || !PrepareWaitSetRecord(wait, g, ticket) {
		t.Fatal("prepare multi-candidate claim-less wait")
	}
	beforePark, beforeSlot := g.park, source.slots[0]
	if id, attached := source.ReserveAndAttachWait(p, &g.park, ticket, wait, 1, nil); attached ||
		id != (OperationID{}) || g.park != beforePark || source.slots[0] != beforeSlot ||
		preemptLoad(&source.slots[0].generation) != 0 ||
		preemptLoad(&source.slots[0].state) != uint32(producerSourceFree) {
		t.Fatalf("multi-candidate nil claim became producer-visible: id=%+v attached=%t park=%+v slot=%+v",
			id, attached, g.park, source.slots[0])
	}
	if !AbortParkSet(&g.park, ticket) {
		t.Fatal("abort rejected multi-candidate claim-less park")
	}
	if outcome, _, lease, consumed := ConsumeParkSet(&g.park, ticket); !consumed ||
		outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) ||
		!ReleasePreparedWaitSetRecord(wait) {
		t.Fatalf("consume rejected multi-candidate claim-less park = (%d,%+v,%t)", outcome, lease, consumed)
	}
	if !UnbindChannelOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release multi-candidate claim-less source")
	}
}

func TestChannelReservationRejectsClaimReuseAcrossWaitsBeforeVisibility(t *testing.T) {
	p := new(P)
	source := new(ChannelOperationSource)
	if !BindChannelOperationSource(source, p) {
		t.Fatal("bind cross-wait claim source")
	}
	claim := new(SelectClaim)
	firstG, secondG := new(G), new(G)
	if !InitG(firstG) || !InitG(secondG) {
		t.Fatal("initialize cross-wait claim Gs")
	}
	firstTicket, firstOK := BeginParkSet(&firstG.park, 1, 109)
	secondTicket, secondOK := BeginParkSet(&secondG.park, 1, 110)
	firstWait, secondWait := new(WaitSetRecord), new(WaitSetRecord)
	if !firstOK || !secondOK ||
		!PrepareWaitSetRecord(firstWait, firstG, firstTicket) ||
		!PrepareWaitSetRecord(secondWait, secondG, secondTicket) {
		t.Fatal("prepare cross-wait claim parks")
	}
	firstID, attached := source.ReserveAndAttachWait(p, &firstG.park, firstTicket, firstWait, 1, claim)
	if !attached {
		t.Fatal("reserve first cross-wait claim")
	}
	beforeSecond := source.slots[1]
	if secondID, secondAttached := source.ReserveAndAttachWait(
		p, &secondG.park, secondTicket, secondWait, 2, claim,
	); secondAttached || secondID != (OperationID{}) || source.slots[1] != beforeSecond ||
		preemptLoad(&source.slots[1].generation) != 0 || selectClaimLoad(claim) != selectClaimOpen {
		t.Fatalf("claim reused across waits = (%+v,%t), slot=%+v claim=%d",
			secondID, secondAttached, source.slots[1], selectClaimLoad(claim))
	}
	if !source.AbortSelectPreparation(p, &firstG.park, firstTicket, firstWait, claim) {
		t.Fatal("abort first cross-wait claim preparation")
	}
	firstSlot, _ := channelOperationSlotFor(source, firstID)
	if source.ApplyOne(p, firstID, &firstSlot.record) != OperationApplyDetached ||
		!source.ConfirmQuiesced(p, firstID) || !source.ResetSelectClaim(p, claim) ||
		!source.Recycle(p, firstID) {
		t.Fatal("release first cross-wait claim operation")
	}
	if outcome, _, lease, consumed := ConsumeParkSet(&firstG.park, firstTicket); !consumed ||
		outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) ||
		!ReleasePreparedWaitSetRecord(firstWait) {
		t.Fatalf("consume first cross-wait abort = (%d,%+v,%t)", outcome, lease, consumed)
	}
	if !AbortParkSet(&secondG.park, secondTicket) {
		t.Fatal("abort unattached second cross-wait preparation")
	}
	if outcome, _, lease, consumed := ConsumeParkSet(&secondG.park, secondTicket); !consumed ||
		outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) ||
		!ReleasePreparedWaitSetRecord(secondWait) {
		t.Fatalf("consume second cross-wait abort = (%d,%+v,%t)", outcome, lease, consumed)
	}
	if !UnbindChannelOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release cross-wait claim source")
	}
}

func TestChannelPreparationAbortLifecycleAndCanonicalClaimReset(t *testing.T) {
	t.Run("seal-reject-multi-slot", func(t *testing.T) {
		p := new(P)
		source := new(ChannelOperationSource)
		other := new(ChannelOperationSource)
		if !BindChannelOperationSource(source, p) || BindChannelOperationSource(other, p) ||
			p.channelSource != source || other.owner != nil {
			t.Fatalf("canonical Channel source binding = source:%p canonical:%p otherOwner:%p",
				source, p.channelSource, other.owner)
		}
		g := new(G)
		if !InitG(g) {
			t.Fatal("initialize Seal-abort G")
		}
		ticket, ok := BeginParkSet(&g.park, 2, 111)
		wait := new(WaitSetRecord)
		claim := new(SelectClaim)
		if !ok || !PrepareWaitSetRecord(wait, g, ticket) {
			t.Fatal("prepare Seal-abort wait")
		}
		ids := make([]OperationID, 2)
		for index := range ids {
			ids[index], ok = source.ReserveAndAttachWait(p, &g.park, ticket, wait, 7, claim)
			if !ok {
				t.Fatalf("reserve duplicate Seal-abort case %d", index)
			}
		}
		if SealParkSet(&g.park, ticket) || g.park.phase != parkPreparing ||
			!source.AbortSelectPreparation(p, &g.park, ticket, wait, claim) ||
			g.park.phase != parkDetaching || selectClaimLoad(claim) != selectClaimClaimed ||
			other.ResetSelectClaim(p, claim) {
			t.Fatalf("Seal-abort domain = park:%+v claim:%d otherResetOwner:%p",
				g.park, selectClaimLoad(claim), other.owner)
		}
		for index, id := range ids {
			slot, _ := channelOperationSlotFor(source, id)
			if source.ApplyOne(p, id, &slot.record) != OperationApplyDetached ||
				!source.ConfirmQuiesced(p, id) {
				t.Fatalf("apply Seal-abort case %d", index)
			}
			if index == 0 && source.ResetSelectClaim(p, claim) {
				t.Fatal("reset multi-slot claim before every registration detached")
			}
		}
		if !source.ResetSelectClaim(p, claim) || selectClaimLoad(claim) != selectClaimOpen {
			t.Fatal("reset multi-slot claim after complete detach")
		}
		for _, id := range ids {
			if !source.Recycle(p, id) {
				t.Fatalf("recycle Seal-abort case %+v", id)
			}
		}
		if outcome, _, lease, consumed := ConsumeParkSet(&g.park, ticket); !consumed ||
			outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) ||
			!ReleasePreparedWaitSetRecord(wait) {
			t.Fatalf("consume Seal-abort park = (%d,%+v,%t)", outcome, lease, consumed)
		}
		if !UnbindChannelOperationSource(source, p) || !source.CanRelease() || !other.CanRelease() ||
			p.channelSource != nil {
			t.Fatal("release canonical Seal-abort sources")
		}
	})

	t.Run("early-ready-before-park", func(t *testing.T) {
		p := new(P)
		source := new(ChannelOperationSource)
		if !BindChannelOperationSource(source, p) {
			t.Fatal("bind early-Ready abort source")
		}
		g := new(G)
		if !InitG(g) {
			t.Fatal("initialize early-Ready abort G")
		}
		ticket, ok := BeginParkSet(&g.park, 1, 113)
		wait := new(WaitSetRecord)
		claim := new(SelectClaim)
		if !ok || !PrepareWaitSetRecord(wait, g, ticket) {
			t.Fatal("prepare early-Ready abort wait")
		}
		id, attached := source.ReserveAndAttachWait(p, &g.park, ticket, wait, 9, claim)
		if !attached || !SealParkSet(&g.park, ticket) || source.PostReady(id) != ChannelOperationPosted ||
			!source.AbortSelectPreparation(p, &g.park, ticket, wait, claim) {
			t.Fatal("abort sealed preparation with early Ready")
		}
		slot, _ := channelOperationSlotFor(source, id)
		if g.state == GWaiting || wait.state != waitSetRecordPreparing ||
			selectClaimLoad(claim) != selectClaimClaimed || !source.beginPublishPass(p) {
			t.Fatal("early-Ready abort crossed physical park boundary")
		}
		if published, lost, publishOK := source.publishSlot(p, 0); !publishOK || published != 0 || lost != 1 ||
			preemptLoad(&slot.mailbox) != uint32(channelMailboxEmpty) ||
			operationCandidateIsPublished(&slot.record) {
			t.Fatalf("drain early Ready after preparation abort = (%d,%d,%t), record=%+v mailbox=%d",
				published, lost, publishOK, slot.record, preemptLoad(&slot.mailbox))
		}
		if source.ApplyOne(p, id, &slot.record) != OperationApplyDetached ||
			!source.ConfirmQuiesced(p, id) || !source.ResetSelectClaim(p, claim) ||
			!source.Recycle(p, id) {
			t.Fatal("release early-Ready aborted operation")
		}
		if outcome, _, lease, consumed := ConsumeParkSet(&g.park, ticket); !consumed ||
			outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) ||
			!ReleasePreparedWaitSetRecord(wait) {
			t.Fatalf("consume early-Ready abort = (%d,%+v,%t)", outcome, lease, consumed)
		}
		if !UnbindChannelOperationSource(source, p) || !source.CanRelease() {
			t.Fatal("release early-Ready abort source")
		}
	})
}

func TestChannelExternalLeaseExhaustionRetiresOnlyClaimBackedReservation(t *testing.T) {
	p := new(P)
	source := new(ChannelOperationSource)
	if !BindChannelOperationSource(source, p) {
		t.Fatal("bind external-lease exhaustion source")
	}
	type preparedOperation struct {
		g      *G
		wait   *WaitSetRecord
		claim  *SelectClaim
		ticket ParkTicket
		id     OperationID
	}
	prepare := func(withClaim bool, caseID uint32) preparedOperation {
		t.Helper()
		operation := preparedOperation{g: new(G), wait: new(WaitSetRecord)}
		if !InitG(operation.g) {
			t.Fatal("initialize external-lease preparation G")
		}
		var ok bool
		operation.ticket, ok = BeginParkSet(&operation.g.park, 1, caseID)
		if !ok || !PrepareWaitSetRecord(operation.wait, operation.g, operation.ticket) {
			t.Fatal("prepare external-lease wait")
		}
		if withClaim {
			operation.claim = new(SelectClaim)
		}
		operation.id, ok = source.ReserveAndAttachWait(
			p, &operation.g.park, operation.ticket, operation.wait, caseID, operation.claim,
		)
		if !ok {
			t.Fatal("reserve external-lease operation")
		}
		return operation
	}
	cleanup := func(operation preparedOperation) {
		t.Helper()
		if operation.claim != nil && !source.AbortSelectPreparation(
			p, &operation.g.park, operation.ticket, operation.wait, operation.claim,
		) {
			t.Fatal("terminalize external-lease claim")
		} else if operation.claim == nil && !AbortParkSet(&operation.g.park, operation.ticket) {
			t.Fatal("abort claim-less external-lease preparation")
		}
		slot, ok := channelOperationSlotFor(source, operation.id)
		if !ok || source.ApplyOne(p, operation.id, &slot.record) != OperationApplyDetached ||
			!source.ConfirmQuiesced(p, operation.id) {
			t.Fatal("apply external-lease aborted operation")
		}
		if operation.claim != nil && !source.ResetSelectClaim(p, operation.claim) {
			t.Fatal("reset external-lease claim")
		}
		if !source.Recycle(p, operation.id) {
			t.Fatal("recycle external-lease operation")
		}
		if outcome, _, lease, ok := ConsumeParkSet(&operation.g.park, operation.ticket); !ok || outcome != ParkOutcomeCanceled || lease != (OperationResultLease{}) ||
			!ReleasePreparedWaitSetRecord(operation.wait) {
			t.Fatalf("consume external-lease aborted park = (%d,%+v,%t)", outcome, lease, ok)
		}
	}

	first := prepare(true, 101)
	retired, _ := channelOperationSlotFor(source, first.id)
	if admission, acquired := source.acquireExternalCommit(first.id); acquired != channelExternalCommitAcquireUnsupported ||
		admission != (channelExternalCommitAdmission{}) {
		t.Fatalf("unexposed external lease was admitted = (%+v,%d)", admission, acquired)
	}
	// This test isolates lease-sequence exhaustion from the owner preparation
	// state machine. Full fixtures above publish Exposed only through
	// ExposeExternalCommit after PrepareParkSet.
	preemptStore(&retired.external, uint32(channelExternalExposed))
	nearExhaustion := ^uint32(0) - 3
	preemptStore(&retired.externalLease, nearExhaustion)
	admission, acquired := source.acquireExternalCommit(first.id)
	if acquired != channelExternalCommitAcquired || admission.token != nearExhaustion+1 ||
		preemptLoad(&retired.externalLease) != nearExhaustion+1 {
		t.Fatalf("acquire final external lease = (%+v,%d), lease=%#x", admission, acquired,
			preemptLoad(&retired.externalLease))
	}
	if !admission.releaseWithoutCommit() || preemptLoad(&retired.externalLease) != ^uint32(0)-1 {
		t.Fatalf("release final external lease = admission:%+v lease:%#x",
			admission, preemptLoad(&retired.externalLease))
	}
	cleanup(first)
	if !channelOperationReusableSlot(source, retired, 0) || channelOperationExternalReservable(retired) {
		t.Fatalf("exhausted slot lifecycle/external state = reusable:%t external:%t lease:%#x",
			channelOperationReusableSlot(source, retired, 0), channelOperationExternalReservable(retired),
			preemptLoad(&retired.externalLease))
	}

	claimBacked := prepare(true, 102)
	if claimBacked.id.LocalSlot() != 2 {
		t.Fatalf("claim-backed reservation reused exhausted slot: id=%+v", claimBacked.id)
	}
	cleanup(claimBacked)
	claimless := prepare(false, 103)
	if claimless.id.LocalSlot() != 1 {
		t.Fatalf("claim-less local reservation could not use lifecycle-empty retired slot: id=%+v", claimless.id)
	}
	cleanup(claimless)
	if preemptLoad(&retired.externalLease) != ^uint32(0)-1 ||
		!UnbindChannelOperationSource(source, p) || !source.CanRelease() {
		t.Fatalf("retired external lease blocked lifecycle release: lease=%#x owner=%p releasable=%t",
			preemptLoad(&retired.externalLease), source.owner, source.CanRelease())
	}
}

func TestForcedResolutionBeginsLocallyAndVisitsOneLinkPerReduction(t *testing.T) {
	const candidateCount = 1024
	var state ParkState
	ticket, ok := BeginParkSet(&state, candidateCount, 97)
	if !ok {
		t.Fatal("begin long forced park-set")
	}
	records := make([]OperationRecord, candidateCount)
	for index := range records {
		id, idOK := MakeOperationID(OperationSourceManual, uint32(index+1), 1)
		if !idOK || !InitOperation(&records[index], id) ||
			!DeclareOperationCommitMode(&records[index], OperationCommitReadyThenTryCommit) ||
			!AttachParkOperation(&state, ticket, &records[index], uint32(index+1)) {
			t.Fatalf("attach long forced candidate %d", index)
		}
	}
	if !SealParkSet(&state, ticket) || !CommitParkSet(&state, ticket) {
		t.Fatal("seal/commit long forced park-set")
	}
	forced := state.head.operation
	if result := PublishExternallyCommittedReadyThenCandidate(forced, forced.id); result != OperationCompletionPublished {
		t.Fatalf("publish long forced winner = %d", result)
	}
	tail := state.head
	for tail.next != nil {
		tail = tail.next
	}
	// A deliberately distant corruption proves begin does not hide a full list
	// audit. Each budget=1 settle reduction validates only its current link and
	// adjacency, and the corruption is observed only when its link is reached.
	tail.operation.phase = operationDetached
	var cursor parkResolutionCursor
	if !beginForcedParkSnapshotResolution(&state, ticket, &cursor, forced) {
		t.Fatal("forced begin scanned a distant candidate")
	}
	for step := 0; step < candidateCount-1; step++ {
		resolution, request, status := resolveParkSnapshotBoundedStep(
			&state, ticket, &cursor, ParkCommitAttempt{},
		)
		if resolution != (CompletionResolution{}) || request != (ParkCommitRequest{}) || status != parkResolveProgress {
			t.Fatalf("long forced reduction %d = (%+v,%+v,%d)", step, resolution, request, status)
		}
	}
	if resolution, request, status := resolveParkSnapshotBoundedStep(
		&state, ticket, &cursor, ParkCommitAttempt{},
	); resolution != (CompletionResolution{}) || request != (ParkCommitRequest{}) || status != ParkResolveInvalid {
		t.Fatalf("distant forced corruption = (%+v,%+v,%d)", resolution, request, status)
	}
}
