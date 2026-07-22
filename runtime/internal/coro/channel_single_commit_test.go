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

import "testing"

func TestChannelExternalCommitSingleAbortCopyAndCommit(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-single-commit", []uint32{91}, true, 0)
	slot, ok := channelOperationSlotFor(fixture.source, fixture.ids[0])
	if !ok {
		t.Fatal("find single commit slot")
	}

	var transaction ChannelExternalCommit
	if result := BeginChannelExternalCommit(&transaction, fixture.source, fixture.ids[0], fixture.claim); result != ChannelExternalCommitBeginPrepared || transaction.self != &transaction ||
		selectClaimLoad(fixture.claim) != selectClaimAcquiring || preemptLoad(&slot.inflight) != 1 {
		t.Fatalf("begin single commit = result:%d transaction:%+v claim:%d inflight:%#x",
			result, transaction, selectClaimLoad(fixture.claim), preemptLoad(&slot.inflight))
	}
	copied := transaction
	if copied.Abort() || copied.BeginEffect() || copied.Commit() ||
		selectClaimLoad(fixture.claim) != selectClaimAcquiring || preemptLoad(&slot.inflight) != 1 ||
		transaction.self != &transaction {
		t.Fatalf("copied single transaction mutated original: copied=%+v original=%+v", copied, transaction)
	}
	if !transaction.Abort() || transaction != (ChannelExternalCommit{}) ||
		selectClaimLoad(fixture.claim) != selectClaimOpen || preemptLoad(&slot.inflight) != 0 {
		t.Fatalf("abort single transaction = transaction:%+v claim:%d inflight:%#x",
			transaction, selectClaimLoad(fixture.claim), preemptLoad(&slot.inflight))
	}

	if result := BeginChannelExternalCommit(&transaction, fixture.source, fixture.ids[0], fixture.claim); result != ChannelExternalCommitBeginPrepared || !transaction.BeginEffect() ||
		selectClaimLoad(fixture.claim) != selectClaimCommitting {
		t.Fatalf("begin single effect = result:%d transaction:%+v claim:%d",
			result, transaction, selectClaimLoad(fixture.claim))
	}
	effectCopy := transaction
	if effectCopy.Abort() || effectCopy.BeginEffect() || effectCopy.Commit() || transaction.Abort() ||
		preemptLoad(&slot.physical) != uint32(channelPhysicalIdle) || preemptLoad(&slot.inflight) != 1 {
		t.Fatalf("copied Effect transaction crossed identity: copied=%+v original=%+v", effectCopy, transaction)
	}
	if !transaction.Commit() || transaction != (ChannelExternalCommit{}) ||
		selectClaimLoad(fixture.claim) != selectClaimClaimed || preemptLoad(&slot.inflight) != 0 ||
		preemptLoad(&slot.physical) != uint32(channelPhysicalCommitted) ||
		preemptLoad(&slot.mailbox) != uint32(channelMailboxForced) {
		t.Fatalf("commit single transaction = transaction:%+v claim:%d inflight:%#x physical:%d mailbox:%d",
			transaction, selectClaimLoad(fixture.claim), preemptLoad(&slot.inflight),
			preemptLoad(&slot.physical), preemptLoad(&slot.mailbox))
	}

	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 91 || !decision.lease.Valid() {
		t.Fatalf("single committed decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelExternalCommitSingleFailureIsAtomic(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-single-failure", []uint32{92}, true, 0)
	slot, ok := channelOperationSlotFor(fixture.source, fixture.ids[0])
	if !ok {
		t.Fatal("find single failure slot")
	}
	assertReleased := func(label string, transaction ChannelExternalCommit) {
		t.Helper()
		if transaction != (ChannelExternalCommit{}) || preemptLoad(&slot.inflight) != 0 ||
			selectClaimLoad(fixture.claim) != selectClaimOpen {
			t.Fatalf("%s retained state: transaction=%+v inflight=%#x claim=%d",
				label, transaction, preemptLoad(&slot.inflight), selectClaimLoad(fixture.claim))
		}
	}

	stale := fixture.ids[0]
	stale.Generation++
	var transaction ChannelExternalCommit
	if result := BeginChannelExternalCommit(&transaction, fixture.source, stale, fixture.claim); result != ChannelExternalCommitBeginAdmissionFailed {
		t.Fatalf("stale single admission = %d", result)
	}
	assertReleased("stale admission", transaction)

	wrongClaim := new(SelectClaim)
	if result := BeginChannelExternalCommit(&transaction, fixture.source, fixture.ids[0], wrongClaim); result != ChannelExternalCommitBeginClaimMismatch || selectClaimLoad(wrongClaim) != selectClaimOpen {
		t.Fatalf("mismatched single claim = result:%d transaction:%+v wrong:%d",
			result, transaction, selectClaimLoad(wrongClaim))
	}
	assertReleased("claim mismatch", transaction)

	if state := selectClaimOwnerAcquire(fixture.claim); state != selectClaimOpen {
		t.Fatalf("hold owner claim = %d", state)
	}
	if result := BeginChannelExternalCommit(&transaction, fixture.source, fixture.ids[0], fixture.claim); result != ChannelExternalCommitBeginClaimContended || transaction != (ChannelExternalCommit{}) ||
		preemptLoad(&slot.inflight) != 0 || selectClaimLoad(fixture.claim) != selectClaimAcquiring {
		t.Fatalf("single claim contention = result:%d transaction:%+v inflight:%#x claim:%d",
			result, transaction, preemptLoad(&slot.inflight), selectClaimLoad(fixture.claim))
	}
	if !selectClaimOwnerReleasePending(fixture.claim) {
		t.Fatal("release owner claim")
	}
	preemptStore(&fixture.claim.state, selectClaimClaimed)
	if result := BeginChannelExternalCommit(
		&transaction, fixture.source, fixture.ids[0], fixture.claim,
	); result != ChannelExternalCommitBeginClaimResolved || transaction != (ChannelExternalCommit{}) ||
		preemptLoad(&slot.inflight) != 0 || selectClaimLoad(fixture.claim) != selectClaimClaimed {
		t.Fatalf("single terminal claim = result:%d transaction:%+v inflight:%#x claim:%d",
			result, transaction, preemptLoad(&slot.inflight), selectClaimLoad(fixture.claim))
	}
	preemptStore(&fixture.claim.state, selectClaimOpen)

	slot.record.phase = operationDetached
	if result := BeginChannelExternalCommit(&transaction, fixture.source, fixture.ids[0], fixture.claim); result != ChannelExternalCommitBeginInvariantFailure {
		t.Fatalf("invalid record single begin = %d", result)
	}
	assertReleased("invalid record", transaction)
	slot.record.phase = operationActive

	if result := fixture.source.PostReady(fixture.ids[0]); result != ChannelOperationPosted {
		t.Fatalf("post cleanup readiness = %d", result)
	}
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || !decision.lease.Valid() {
		t.Fatalf("single failure cleanup decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}

func TestChannelExternalCommitPairPublicWrapper(t *testing.T) {
	a := newChannelClaimCoreFixture(t, "channel-public-pair-a", []uint32{93}, true, 0)
	b := newChannelClaimCoreFixture(t, "channel-public-pair-b", []uint32{94}, true, 0)
	var pair ChannelExternalCommitPair
	if result := BeginChannelExternalCommitPair(
		&pair,
		a.source, a.ids[0], a.claim,
		b.source, b.ids[0], b.claim,
	); result != ChannelExternalCommitPairBeginPrepared {
		t.Fatalf("begin public pair = %d", result)
	}
	copied := pair
	if copied.Abort() || copied.BeginEffect() || copied.Commit() ||
		selectClaimLoad(a.claim) != selectClaimAcquiring || selectClaimLoad(b.claim) != selectClaimAcquiring {
		t.Fatalf("copied public pair mutated claims: copied=%+v pair=%+v", copied, pair)
	}
	if !pair.BeginEffect() || pair.Abort() || !pair.Commit() || pair != (ChannelExternalCommitPair{}) ||
		selectClaimLoad(a.claim) != selectClaimClaimed || selectClaimLoad(b.claim) != selectClaimClaimed {
		t.Fatalf("commit public pair = pair:%+v claims:(%d,%d)",
			pair, selectClaimLoad(a.claim), selectClaimLoad(b.claim))
	}
	for _, fixture := range []*channelClaimCoreFixture{a, b} {
		requestChannelClaimCoreFixture(t, fixture)
		pollChannelClaimCoreComplete(t, fixture)
		decision := takeChannelClaimCoreDecision(t, fixture)
		if decision.outcome != ParkOutcomeCompleted || !decision.lease.Valid() {
			t.Fatalf("public pair decision = %+v", decision)
		}
		releaseChannelClaimCoreFixture(t, fixture, decision)
	}
}

func TestChannelExternalCommitCanWinAfterExposureBeforeResumed(t *testing.T) {
	committed := false
	fixture := newChannelClaimCoreFixtureBeforeResume(
		t,
		"channel-pre-resume-commit",
		[]uint32{95},
		true,
		0,
		func(fixture *channelClaimCoreFixture) {
			var transaction ChannelExternalCommit
			if result := BeginChannelExternalCommit(
				&transaction, fixture.source, fixture.ids[0], fixture.claim,
			); result != ChannelExternalCommitBeginPrepared ||
				!transaction.BeginEffect() || !transaction.Commit() {
				t.Fatalf("commit exposed endpoint before Resumed = result:%d transaction:%+v",
					result, transaction)
			}
			committed = true
		},
	)
	if !committed || selectClaimLoad(fixture.claim) != selectClaimClaimed {
		t.Fatal("pre-Resumed commit was not durably published")
	}
	requestChannelClaimCoreFixture(t, fixture)
	pollChannelClaimCoreComplete(t, fixture)
	decision := takeChannelClaimCoreDecision(t, fixture)
	if decision.outcome != ParkOutcomeCompleted || decision.caseID != 95 || !decision.lease.Valid() {
		t.Fatalf("pre-Resumed committed decision = %+v", decision)
	}
	releaseChannelClaimCoreFixture(t, fixture, decision)
}
