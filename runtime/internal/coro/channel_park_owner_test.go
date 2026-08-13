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
	"testing"
	"unsafe"
)

func TestDirectChannelParkStateCertificates(t *testing.T) {
	if !validReusableDirectChannelParkState(new(ParkState)) {
		t.Fatal("zero idle park was not reusable")
	}
	ticket := ParkTicket{generation: 1}
	delivered := ParkState{ticket: ticket, phase: parkDelivered}
	if !validReusableDirectChannelParkState(&delivered) {
		t.Fatal("clean delivered park was not reusable")
	}
	delivered.expected = 1
	if validReusableDirectChannelParkState(&delivered) {
		t.Fatal("delivered park with retained expected count was reusable")
	}

	id, idOK := MakeOperationID(OperationSourceChannel, 1, 1)
	if !idOK {
		t.Fatal("make direct certificate operation ID")
	}
	g := new(G)
	wait := WaitSetRecord{
		g: g, ticket: ticket, state: waitSetRecordPreparing,
	}
	record := OperationRecord{id: id, phase: operationActive}
	setOperationCandidate(&record, OperationCommitReadyThenTryCommit, OperationCommitIdle, false)
	g.park = ParkState{
		ticket: ticket, phase: parkParked, expected: 1, attached: 1,
	}
	record.link = ParkLink{
		park: &g.park, wait: &wait, operation: &record,
		ticket: ticket, caseID: 1,
	}
	g.park.head = &record.link
	if !validPreparedDirectChannelParkState(&g.park, &wait, id, 1) {
		t.Fatal("exact prepared direct park certificate was rejected")
	}
	record.link.previous = &record.link
	if validPreparedDirectChannelParkState(&g.park, &wait, id, 1) {
		t.Fatal("cyclic prepared direct park certificate was accepted")
	}
	record.link.previous = nil
	setOperationCandidate(&record, OperationCommitReadyThenTryCommit, OperationCommitCommitted, true)
	if validPreparedDirectChannelParkState(&g.park, &wait, id, 1) {
		t.Fatal("pre-committed prepared direct park certificate was accepted")
	}
}

func TestCurrentExecutorChannelOwnerResolvesExactRouteAcrossTwoP(t *testing.T) {
	registry := new(ExecutorRegistry)
	type fixture struct {
		p      *P
		driver *ExecutorDriver
		source *ChannelOperationSource
		handle ExecutorHandle
		route  RouteID
		task   *yieldingTestG
		action Action
	}
	fixtures := []*fixture{
		{p: new(P), driver: new(ExecutorDriver), source: new(ChannelOperationSource), route: 3, task: newYieldingTestG(t, "channel-route-3")},
		{p: new(P), driver: new(ExecutorDriver), source: new(ChannelOperationSource), route: 7, task: newYieldingTestG(t, "channel-route-7")},
	}
	for _, current := range fixtures {
		current.handle = registerTestExecutor(t, registry)
		if !BindExecutorSourceCatalogAtRoute(
			current.driver,
			current.p,
			registry,
			current.handle,
			current.route,
			ExecutorSourceCatalog{Channel: current.source},
		) || !Enqueue(current.p, current.task.g) {
			t.Fatalf("bind/enqueue channel route %d", current.route)
		}
		if next, ok := NextRunnable(current.p); !ok || next != current.task.g {
			t.Fatalf("dequeue channel route %d", current.route)
		}
		current.action = beginWaitTestResume(t, current.p, current.task)
		current.task.frame.header.SuspendReason = uint16(SuspendPark)
		current.task.frame.header.Lifecycle = uint16(FrameSuspended)
		driver, handle, route, ok := CurrentExecutorChannelDriver(current.task.g)
		p, park, source, ownerOK := CurrentExecutorChannelParkOwner(driver, current.task.g)
		if !ok || !ownerOK || driver != current.driver || handle != current.handle || route != current.route ||
			p != current.p || park != &current.task.g.park || source != current.source {
			t.Fatalf("resolve channel route %d = driver:%p handle:%+v route:%d p:%p park:%p source:%p ok:(%t,%t)",
				current.route, driver, handle, route, p, park, source, ok, ownerOK)
		}
		directRoute, directP, directSource, reservation, directCurrent, reserved :=
			CurrentExecutorChannelDirectReservation(current.task.g)
		if !directCurrent || !reserved || directRoute != current.route ||
			directP != current.p || directSource != current.source ||
			!validDirectReservationHeader(directSource, directP, reservation) {
			t.Fatalf("resolve direct channel route %d = route:%d p:%p source:%p current:%t reserved:%t",
				current.route, directRoute, directP, directSource, directCurrent, reserved)
		}
	}
	if fixtures[0].source == fixtures[1].source || fixtures[0].handle == fixtures[1].handle {
		t.Fatal("channel route fixtures alias physical ownership")
	}
	for _, current := range fixtures {
		current.task.frame.header.SuspendReason = uint16(SuspendNone)
		current.task.frame.header.Lifecycle = uint16(FrameActive)
		yieldRunningDriverTask(t, current.p, current.task, current.action)
		closeTestExecutorDriver(t, current.driver)
		finishReadyDriverTasks(t, current.p, map[*G]*yieldingTestG{current.task.g: current.task})
		if !current.source.CanRelease() {
			t.Fatalf("channel route %d retained source storage", current.route)
		}
	}
	if !registry.CanRelease() {
		t.Fatal("channel route owner test retained executor registry")
	}
}

func TestCurrentChannelParkPreparationCapacityFailureIsZeroEffect(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	source := new(ChannelOperationSource)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{Channel: source}) {
		t.Fatal("bind capacity-gate channel executor")
	}
	task := newYieldingTestG(t, "channel-capacity-gate")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue capacity-gate channel task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue capacity-gate channel task")
	}
	action := beginWaitTestResume(t, p, task)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)

	capacity := ChannelOperationConfiguredCapacity(source)
	for index := uint32(0); index < capacity; index++ {
		slot, ok := channelOperationSlotAt(source, index)
		if !ok {
			t.Fatalf("lookup capacity-gate slot %d", index)
		}
		preemptStore(&slot.externalLease, ^uint32(0)-1)
	}
	defer func() {
		for index := uint32(0); index < capacity; index++ {
			slot, _ := channelOperationSlotAt(source, index)
			preemptStore(&slot.externalLease, 0)
		}
	}()

	var wait WaitSetRecord
	var claim SelectClaim
	var packet ResumePacket
	var plan ResumeCleanupPlan
	var entry OperationID
	var context byte
	beforePark, beforePending := task.g.park, task.g.pending
	prepared := PrepareCurrentChannelParkCleanup(
		task.g,
		task.handle,
		task.frame.header,
		&wait,
		&claim,
		&packet,
		&plan,
		unsafe.Pointer(&context),
		&entry,
		ChannelDirectReservation{},
		false,
		1,
		91,
	)
	if prepared.State != CurrentChannelParkPreparationNeedsCapacity ||
		prepared.Owner != p || prepared.Source != source || prepared.Route != driver.route ||
		prepared.Ticket != (ParkTicket{}) || prepared.Operation != (OperationID{}) {
		t.Fatalf("capacity-gate result = %+v", prepared)
	}
	if task.g.park != beforePark || task.g.pending != beforePending || task.g.active.parkWait != nil ||
		wait != (WaitSetRecord{}) || claim != (SelectClaim{}) || packet != (ResumePacket{}) ||
		plan != (ResumeCleanupPlan{}) || entry != (OperationID{}) {
		t.Fatalf("capacity-gate preparation mutated state: park=%+v pending=%+v frameWait=%p wait=%+v claim=%+v packet=%+v plan=%+v entry=%+v",
			task.g.park, task.g.pending, task.g.active.parkWait, wait, claim, packet, plan, entry)
	}

	for index := uint32(0); index < capacity; index++ {
		slot, _ := channelOperationSlotAt(source, index)
		preemptStore(&slot.externalLease, 0)
	}
	reservation, reserved := source.PreflightDirectReservation(p)
	if !reserved {
		t.Fatal("preflight stale direct reservation")
	}
	beforeScan, beforeCursor := source.scanLimit, source.reserveCursor
	preemptStore(&reservation.slot.state, uint32(producerSourceInitializing))
	prepared = PrepareCurrentChannelParkCleanup(
		task.g,
		task.handle,
		task.frame.header,
		&wait,
		&claim,
		&packet,
		&plan,
		unsafe.Pointer(&context),
		&entry,
		reservation,
		true,
		1,
		91,
	)
	if prepared != (CurrentChannelParkPreparation{}) || task.g.park != beforePark ||
		task.g.pending != beforePending || task.g.active.parkWait != nil ||
		wait != (WaitSetRecord{}) || claim != (SelectClaim{}) ||
		packet != (ResumePacket{}) || plan != (ResumeCleanupPlan{}) ||
		entry != (OperationID{}) || source.scanLimit != beforeScan ||
		source.reserveCursor != beforeCursor {
		t.Fatalf("stale-reservation preparation was not zero effect: result=%+v park=%+v pending=%+v frameWait=%p wait=%+v claim=%+v packet=%+v plan=%+v entry=%+v scan=%d cursor=%d",
			prepared, task.g.park, task.g.pending, task.g.active.parkWait, wait, claim,
			packet, plan, entry, source.scanLimit, source.reserveCursor)
	}
	preemptStore(&reservation.slot.state, uint32(producerSourceFree))
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !source.CanRelease() || !registry.CanRelease() {
		t.Fatal("capacity-gate cleanup retained source/registry")
	}
}

func TestSingleChannelParkOwnerTransactionAndFinish(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	source := new(ChannelOperationSource)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{
		Channel: source,
	}) {
		t.Fatal("bind single-channel owner executor")
	}
	task := newYieldingTestG(t, "single-channel-owner")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue single-channel owner task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue single-channel owner task")
	}
	action := beginWaitTestResume(t, p, task)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	var wait WaitSetRecord
	var claim SelectClaim
	reservation, reserved := source.PreflightDirectReservation(p)
	if !reserved {
		t.Fatal("preflight single-channel owner reservation")
	}
	if !validDirectReservationHeader(source, p, reservation) {
		t.Fatal("direct reservation lost its authenticated owner/slot identity")
	}
	malformed := reservation
	malformed.index = malformed.capacity
	if validDirectReservationHeader(source, p, malformed) {
		t.Fatal("direct reservation accepted an out-of-catalog slot identity")
	}
	ticket, id, ok := prepareSingleChannelParkOrdinary(
		task.g,
		task.handle,
		task.frame.header,
		source,
		&wait,
		&claim,
		&reservation,
		41,
		73,
	)
	if !ok || !validParkTicket(ticket) || !id.Valid() || selectClaimLoad(&claim) != selectClaimOpen {
		t.Fatalf("prepare single-channel park = (%+v, %+v, %t), claim=%d", ticket, id, ok, selectClaimLoad(&claim))
	}
	var transaction ChannelExternalCommit
	if result := BeginChannelExternalCommit(&transaction, source, id, &claim); result != ChannelExternalCommitBeginPrepared ||
		!transaction.BeginEffect() || !transaction.Commit() {
		t.Fatalf("commit single-channel endpoint = result:%d transaction:%+v", result, transaction)
	}
	if parked, resumed := Resumed(p, task.g, action); !resumed || parked.Kind != ActionPark {
		t.Fatalf("commit single-channel physical park = (%+v, %t)", parked, resumed)
	}
	requested := registry.Request(handle)
	if requested != ExecutorRequestPublished && requested != ExecutorRequestCoalesced {
		t.Fatalf("request single-channel executor = %d", requested)
	}
	for step := 0; ; step++ {
		progress, polled := PollExecutorSlice(driver, 1)
		if !polled {
			t.Fatalf("poll single-channel owner at step %d", step)
		}
		if progress.Complete {
			break
		}
		if step == 10000 {
			t.Fatal("single-channel owner did not become runnable")
		}
	}
	if g, runnable := NextRunnable(p); !runnable || g != task.g {
		t.Fatal("dequeue completed single-channel owner task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, cancel, taken := TakeRunDecision(task.g, ticket)
	if !taken || outcome != ParkOutcomeCompleted || caseID != 41 || cancel != TaskCancelNone || !lease.Valid() {
		t.Fatalf("take single-channel owner decision = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, cancel, taken)
	}
	if !FinishSingleChannelPark(task.g, source, id, &claim, lease, false) ||
		selectClaimLoad(&claim) != selectClaimOpen {
		t.Fatal("finish single-channel owner transaction")
	}
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !source.CanRelease() || !registry.CanRelease() {
		t.Fatal("single-channel owner cleanup retained stable state")
	}
}

func TestCommandShutdownDrainConsumesCompletedChannelBeforeExecutorClose(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-command-drain", []uint32{51}, true, 0)
	var transaction ChannelExternalCommit
	if result := BeginChannelExternalCommit(
		&transaction,
		fixture.source,
		fixture.ids[0],
		fixture.claim,
	); result != ChannelExternalCommitBeginPrepared || !transaction.BeginEffect() || !transaction.Commit() {
		t.Fatalf("commit command-drain channel endpoint = result:%d transaction:%+v", result, transaction)
	}
	requestChannelClaimCoreFixture(t, fixture)
	if progress := pollChannelClaimCoreComplete(t, fixture); progress.Promoted != 1 ||
		fixture.p.readyHead != fixture.task.g || channelOperationSourceEmpty(fixture.source, fixture.p) {
		t.Fatalf("publish command-drain completion = promoted:%d ready:%p sourceEmpty:%t",
			progress.Promoted, fixture.p.readyHead, channelOperationSourceEmpty(fixture.source, fixture.p))
	}
	if needed, ok := RequestCommandShutdownDrain(fixture.p, nil); needed || ok {
		t.Fatalf("nil command main accepted = needed:%t ok:%t", needed, ok)
	}
	main := &G{magic: gMagic, state: GDead}
	if needed, ok := RequestCommandShutdownDrain(fixture.p, main); !ok || !needed ||
		fixture.task.g.park.taskCancelKind != TaskCancelShutdown ||
		fixture.task.g.park.taskCancelPhase != taskCancelRequested {
		t.Fatalf("request command source drain = needed:%t ok:%t cancel:(%d,%d)",
			needed, ok, fixture.task.g.park.taskCancelKind, fixture.task.g.park.taskCancelPhase)
	}
	if needed, ok := RequestCommandShutdownDrain(fixture.p, main); !ok || !needed {
		t.Fatalf("repeat command source drain = needed:%t ok:%t", needed, ok)
	}
	if BeginExecutorClose(fixture.driver) {
		t.Fatal("executor closed before channel resume cleanup")
	}
	if g, ok := NextRunnable(fixture.p); !ok || g != fixture.task.g {
		t.Fatal("dequeue command-drain channel task")
	}
	action := beginWaitTestResume(t, fixture.p, fixture.task)
	outcome, caseID, lease, cancel, taken := TakeRunDecision(fixture.task.g, fixture.ticket)
	if !taken || outcome != ParkOutcomeCanceled || caseID != 0 || !lease.Valid() || cancel != TaskCancelShutdown {
		t.Fatalf("take command-drain decision = (%d,%d,%+v,%d,%t)", outcome, caseID, lease, cancel, taken)
	}
	if !FinishSingleChannelPark(
		fixture.task.g,
		fixture.source,
		fixture.ids[0],
		fixture.claim,
		lease,
		true,
	) {
		t.Fatal("finish command-drain channel cleanup")
	}
	fixture.task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	fixture.task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(fixture.task.g, fixture.task.handle, fixture.task.frame.header) {
		t.Fatal("prepare command-drain task completion")
	}
	action, ok := Resumed(fixture.p, fixture.task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume command-drain completion = (%+v,%t)", action, ok)
	}
	action, ok = Checked(fixture.p, fixture.task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check command-drain destroy = (%+v,%t)", action, ok)
	}
	releaseTestFrame(t, fixture.task.g, fixture.task.frame)
	receipt, ok := DestroyedBounded(fixture.p, fixture.task.g, action)
	if !ok || receipt.Kind != ActionCommitDestroy || receipt.Handle != nil {
		t.Fatalf("publish command-drain destroy receipt = (%+v,%t)", receipt, ok)
	}
	closeAction, ok := CommitDestroyedReceiptCompatibility(fixture.p, fixture.task.g, receipt)
	if !ok || closeAction.Kind != ActionTerminalExecutorClose || closeAction.Handle != nil {
		t.Fatalf("begin command-drain terminal close = (%+v,%t)", closeAction, ok)
	}
	closedG, complete, ok := ConfirmTerminalExecutorClose(fixture.driver)
	if !ok || closedG != fixture.task.g || complete.Kind != ActionComplete || complete.Handle != nil {
		t.Fatalf("confirm command-drain terminal close = (%p,%+v,%t)", closedG, complete, ok)
	}
	if !AcknowledgeTaskCancellation(fixture.task.g, TaskCancelShutdown) {
		t.Fatal("acknowledge command-drain cancellation")
	}
	if !fixture.source.CanRelease() || !fixture.registry.CanRelease() {
		t.Fatal("command-drain channel cleanup retained stable source state")
	}
}

func TestEmptyChannelParkOwnerSupportsTaskCancellation(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "empty-channel-owner")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue empty-channel owner task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue empty-channel owner task")
	}
	action := beginWaitTestResume(t, p, task)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	var wait WaitSetRecord
	ticket, ok := PrepareEmptyChannelPark(task.g, task.handle, task.frame.header, &wait, 79)
	if !ok || !validParkTicket(ticket) {
		t.Fatalf("prepare empty-channel park = (%+v, %t)", ticket, ok)
	}
	if parked, resumed := Resumed(p, task.g, action); !resumed || parked.Kind != ActionPark {
		t.Fatalf("commit empty-channel physical park = (%+v, %t)", parked, resumed)
	}
	if !RequestTaskCancellation(p, task.g, TaskCancelAbort) {
		t.Fatal("request empty-channel task cancellation")
	}
	for step := 0; ; step++ {
		ready, polled := PollReady(p)
		if !polled {
			t.Fatalf("poll empty-channel cancellation at step %d", step)
		}
		if ready != 0 {
			break
		}
		if step == 10000 {
			t.Fatal("empty-channel cancellation did not become runnable")
		}
	}
	if g, runnable := NextRunnable(p); !runnable || g != task.g {
		t.Fatal("dequeue canceled empty-channel task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, cancel, taken := TakeRunDecision(task.g, ticket)
	if !taken || outcome != ParkOutcomeCanceled || caseID != 0 || lease.Valid() || cancel != TaskCancelAbort {
		t.Fatalf("take empty-channel cancellation = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, cancel, taken)
	}
	finishWaitTestTask(t, p, task, action)
}
