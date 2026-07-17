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

type channelClaimCoreFixture struct {
	p        *P
	driver   *ExecutorDriver
	registry *ExecutorRegistry
	waits    *WaitRegistrationTable
	source   *ChannelOperationSource
	handle   ExecutorHandle
	task     *yieldingTestG
	wait     WaitSetRecord
	ticket   ParkTicket
	ids      []OperationID
	claim    *SelectClaim
}

func newChannelClaimCoreFixture(t *testing.T, name string, caseIDs []uint32, withClaim bool, defaultCase uint32) *channelClaimCoreFixture {
	t.Helper()
	fixture := &channelClaimCoreFixture{
		p:        new(P),
		driver:   new(ExecutorDriver),
		registry: new(ExecutorRegistry),
		waits:    new(WaitRegistrationTable),
		source:   new(ChannelOperationSource),
	}
	fixture.handle = registerTestExecutor(t, fixture.registry)
	if !BindExecutorSourceCatalog(fixture.driver, fixture.p, fixture.registry, fixture.handle, ExecutorSourceCatalog{
		Waits: fixture.waits, Channel: fixture.source,
	}) {
		t.Fatal("bind channel claim-core executor")
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
	if parked, resumed := Resumed(fixture.p, fixture.task.g, action); !resumed || parked.Kind != ActionPark {
		t.Fatalf("commit channel claim-core park = (%+v, %t)", parked, resumed)
	}
	return fixture
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
	if !fixture.source.CanRelease() || !fixture.waits.CanRelease() || !fixture.registry.CanRelease() {
		t.Fatal("channel claim-core cleanup retained stable state")
	}
	runtime.KeepAlive(fixture.task.frame.memory)
}

func externallyCommitChannelCandidate(t *testing.T, fixture *channelClaimCoreFixture, index int) {
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
	if result := admission.publishExternallyCommitted(); result != ChannelOperationPosted {
		t.Fatalf("publish externally committed channel candidate = %d", result)
	}
	if !publishExternalSelectClaim(fixture.claim) {
		t.Fatal("publish externally committed select claim")
	}
	if !admission.releaseCommitted() {
		t.Fatal("release externally committed channel admission")
	}
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
	); failedResult != channelExternalCommitPairBeginClaimContended || failed != (channelExternalCommitPair{}) ||
		selectClaimLoad(a.claim) != selectClaimOpen || selectClaimLoad(b.claim) != selectClaimClaimed ||
		preemptLoad(&slotA.inflight) != 0 || preemptLoad(&slotB.inflight) != 0 {
		t.Fatalf("pair claim contention = (%+v,%d), claims=(%d,%d) inflight=(%#x,%#x)",
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
	if id, ok := source.ReserveAndAttachWait(p, nil, ParkTicket{}, nil, 1, nil); ok || id != (OperationID{}) {
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
		fixture.p.affectedWaitTail != &fixture.wait || operationCandidateState(&slot.record) != OperationCommitReady ||
		slot.record.resultState != operationResultEmpty || selectClaimLoad(fixture.claim) != selectClaimAcquiring {
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
		fixture.p.affectedWaitTail != &fixture.wait || operationCandidateState(&slot.record) != OperationCommitReady ||
		slot.record.resultState != operationResultEmpty || selectClaimLoad(fixture.claim) != selectClaimCommitting {
		t.Fatalf("Committing discovery retained resolver: progress=%+v resolve=%+v park=%+v record=%+v claim=%d",
			progress, fixture.driver.poll.resolve, fixture.task.g.park, slot.record, selectClaimLoad(fixture.claim))
	}
	if admission.publishExternallyCommitted() != ChannelOperationPosted ||
		!publishExternalSelectClaim(fixture.claim) || !admission.releaseCommitted() {
		t.Fatal("complete external effect after Committing discovery yield")
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
	if result := PublishReadyThenTryCommitCandidate(&slot.record, id); result != OperationCompletionPublished ||
		!finishChannelMailboxDrain(fixture.source, slot, channelMailboxReady) ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxForced) || !fixture.source.Pending() {
		t.Fatal("Ready drain cleared a sticky forced handoff")
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
