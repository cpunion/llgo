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
