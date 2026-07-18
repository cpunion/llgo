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

func workerPayloadForTest(t *testing.T, flags ScalarResultFlags, values ...uint64) ScalarResultPayloadV1 {
	t.Helper()
	var scalars [3]uint64
	copy(scalars[:], values)
	payload, ok := MakeScalarResultPayloadV1(
		ScalarResultKindWords,
		flags,
		uint8(len(values)),
		scalars[0],
		scalars[1],
		scalars[2],
	)
	if !ok {
		t.Fatal("make worker payload")
	}
	return payload
}

func reserveWorkerWaitSet(
	t *testing.T,
	source *WorkerOperationSource,
	p *P,
	seed uint32,
	cases []uint32,
) (*ParkState, ParkTicket, []OperationID) {
	t.Helper()
	state := new(ParkState)
	ticket, ok := BeginParkSet(state, uint32(len(cases)), seed)
	if !ok {
		t.Fatal("begin worker wait-set")
	}
	ids := make([]OperationID, len(cases))
	for index, caseID := range cases {
		id, reserved := source.ReserveAndAttach(p, state, ticket, caseID)
		if !reserved {
			t.Fatalf("reserve worker operation %d", index)
		}
		ids[index] = id
	}
	if !SealParkSet(state, ticket) || !CommitParkSet(state, ticket) {
		t.Fatal("commit worker wait-set")
	}
	return state, ticket, ids
}

func finishWorkerOperations(
	t *testing.T,
	source *WorkerOperationSource,
	p *P,
	ids []OperationID,
	lease OperationResultLease,
	want ScalarResultPayloadV1,
) {
	t.Helper()
	for _, id := range ids {
		if !source.ConfirmQuiesced(p, id) {
			t.Fatalf("confirm worker operation quiesced: %+v", id)
		}
	}
	if lease.Valid() {
		var got ScalarResultPayloadV1
		if !source.TakeResult(p, lease, &got) || got != want {
			t.Fatalf("take worker result = %+v, want %+v", got, want)
		}
		if source.TakeResult(p, lease, &got) || source.DiscardResult(p, lease) {
			t.Fatal("worker winner lease reused")
		}
	}
	for _, id := range ids {
		if !source.Recycle(p, id) {
			t.Fatalf("recycle worker operation: %+v", id)
		}
	}
}

func TestWorkerOperationSourcePayloadResolveLifecycleAndReuse(t *testing.T) {
	p := new(P)
	source := new(WorkerOperationSource)
	if !BindWorkerOperationSourceAtRoute(source, p, RouteID(7)) {
		t.Fatal("bind worker source")
	}
	state, ticket, ids := reserveWorkerWaitSet(t, source, p, 41, []uint32{10, 20, 30})
	payloads := []ScalarResultPayloadV1{
		workerPayloadForTest(t, 1, 100, 101),
		workerPayloadForTest(t, 2, 200, 201, 202),
	}
	if result := source.Post(ids[0], payloads[0]); result != WorkerOperationPosted {
		t.Fatalf("post first worker operation = %d", result)
	}
	if result := source.Post(ids[1], payloads[1]); result != WorkerOperationPosted {
		t.Fatalf("post second worker operation = %d", result)
	}
	if result := source.Post(ids[0], payloads[1]); result != WorkerOperationPostDuplicate {
		t.Fatalf("duplicate worker operation = %d", result)
	}
	if !source.Pending() {
		t.Fatal("worker source lost pending hint")
	}
	if published, lost, ok := source.PublishPass(p); !ok || published != 2 || lost != 0 || source.Pending() {
		t.Fatalf("publish worker pass = (%d, %d, %t), pending=%t", published, lost, ok, source.Pending())
	}
	wantResolution := CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1, Losers: 2}
	if resolution, duplicates, ok := source.ResolveAffectedPublishedEpoch(p); !ok ||
		resolution != wantResolution || duplicates != 1 {
		t.Fatalf("resolve worker epoch = (%+v, %d, %t), want %+v", resolution, duplicates, ok, wantResolution)
	}
	if applied, detached, ok := source.ApplyAndDetach(p); !ok || applied != 3 || detached != 3 ||
		!ParkReady(state, ticket) {
		t.Fatalf("apply worker set = (%d, %d, %t), ready=%t", applied, detached, ok, ParkReady(state, ticket))
	}
	winnerCase, winnerID, winnerOK := ParkWinner(state, ticket)
	if !winnerOK {
		t.Fatal("missing worker winner")
	}
	outcome, caseID, lease, consumed := ConsumeParkSet(state, ticket)
	leaseID, leaseOK := lease.ID()
	if !consumed || outcome != ParkOutcomeCompleted || caseID != winnerCase || !leaseOK || leaseID != winnerID {
		t.Fatalf("consume worker winner = (%d, %d, %+v, %t)", outcome, caseID, lease, consumed)
	}
	wantPayload := payloads[0]
	if winnerID == ids[1] {
		wantPayload = payloads[1]
	} else if winnerID != ids[0] {
		t.Fatalf("unexpected worker winner: %+v", winnerID)
	}
	finishWorkerOperations(t, source, p, ids, lease, wantPayload)

	// Reuse advances the physical generation, and an old producer ID cannot
	// write into the newly active operation.
	nextState, nextTicket, nextIDs := reserveWorkerWaitSet(t, source, p, 42, []uint32{40})
	if nextIDs[0].Source() != OperationSourceWorker || nextIDs[0].Route() != RouteID(7) ||
		nextIDs[0].Slot() != ids[0].Slot() || nextIDs[0].Generation == ids[0].Generation {
		t.Fatalf("worker generation did not advance: old=%+v next=%+v", ids[0], nextIDs[0])
	}
	if result := source.Post(ids[0], payloads[0]); result != WorkerOperationPostStale || source.Pending() {
		t.Fatalf("stale worker post = %d, pending=%t", result, source.Pending())
	}
	if result := source.Post(nextIDs[0], ScalarResultPayloadV1{}); result != WorkerOperationPostInvalid {
		t.Fatalf("invalid worker payload = %d", result)
	}
	if !RequestParkCancel(nextState, nextTicket, ParkCancelOperation) {
		t.Fatal("cancel reused worker wait-set")
	}
	if resolution, ok := ResolveParkSnapshot(nextState, nextTicket); !ok ||
		resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 1}) {
		t.Fatalf("resolve reused worker cancellation = (%+v, %t)", resolution, ok)
	}
	if applied, detached, ok := source.ApplyAndDetach(p); !ok || applied != 1 || detached != 1 {
		t.Fatalf("apply reused worker cancellation = (%d, %d, %t)", applied, detached, ok)
	}
	if outcome, _, lease, consumed := ConsumeParkSet(nextState, nextTicket); !consumed ||
		outcome != ParkOutcomeCanceled || lease.Valid() {
		t.Fatalf("consume reused worker cancellation = (%d, %+v, %t)", outcome, lease, consumed)
	}
	finishWorkerOperations(t, source, p, nextIDs, OperationResultLease{}, ScalarResultPayloadV1{})
	if !UnbindWorkerOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release worker source")
	}
}

func TestWorkerOperationSourceConcurrentProducerCoalescing(t *testing.T) {
	p := new(P)
	source := new(WorkerOperationSource)
	if !BindWorkerOperationSource(source, p) {
		t.Fatal("bind concurrent worker source")
	}
	state, ticket, ids := reserveWorkerWaitSet(t, source, p, 51, []uint32{1})
	id := ids[0]

	const producers = 32
	type post struct {
		result  WorkerOperationPostResult
		payload ScalarResultPayloadV1
	}
	posts := make(chan post, producers)
	var group sync.WaitGroup
	group.Add(producers)
	for index := 0; index < producers; index++ {
		payload := workerPayloadForTest(t, ScalarResultFlags(index), uint64(index), uint64(index+1000))
		go func() {
			defer group.Done()
			posts <- post{result: source.Post(id, payload), payload: payload}
		}()
	}
	group.Wait()
	close(posts)
	posted, duplicate := 0, 0
	var winnerPayload ScalarResultPayloadV1
	for one := range posts {
		switch one.result {
		case WorkerOperationPosted:
			posted++
			winnerPayload = one.payload
		case WorkerOperationPostDuplicate:
			duplicate++
		default:
			t.Fatalf("concurrent worker post = %d", one.result)
		}
	}
	if posted != 1 || duplicate != producers-1 {
		t.Fatalf("concurrent worker posts = (posted=%d duplicate=%d)", posted, duplicate)
	}
	if published, lost, ok := source.PublishPass(p); !ok || published != 1 || lost != 0 {
		t.Fatalf("publish concurrent worker result = (%d, %d, %t)", published, lost, ok)
	}
	if resolution, duplicates, ok := source.ResolveAffectedPublishedEpoch(p); !ok || duplicates != 0 ||
		resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}) {
		t.Fatalf("resolve concurrent worker result = (%+v, %d, %t)", resolution, duplicates, ok)
	}
	if applied, detached, ok := source.ApplyAndDetach(p); !ok || applied != 1 || detached != 1 ||
		!ParkReady(state, ticket) {
		t.Fatalf("apply concurrent worker result = (%d, %d, %t)", applied, detached, ok)
	}
	outcome, _, lease, consumed := ConsumeParkSet(state, ticket)
	if !consumed || outcome != ParkOutcomeCompleted {
		t.Fatalf("consume concurrent worker result = (%d, %+v, %t)", outcome, lease, consumed)
	}
	finishWorkerOperations(t, source, p, ids, lease, winnerPayload)
	if !UnbindWorkerOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release concurrent worker source")
	}
}

func TestWorkerOperationSourceLateAdmittedLoserRequiresDrain(t *testing.T) {
	p := new(P)
	source := new(WorkerOperationSource)
	if !BindWorkerOperationSource(source, p) {
		t.Fatal("bind late worker source")
	}
	state, ticket, ids := reserveWorkerWaitSet(t, source, p, 61, []uint32{1})
	id := ids[0]
	slot, _ := workerOperationSlotFor(source, id)
	payload := workerPayloadForTest(t, 7, 77)
	if acquireProducerSourceGeneration(&slot.producerSourceSlot, id.Generation) != producerSourceAcquired {
		t.Fatal("admit worker producer")
	}
	if !RequestParkCancel(state, ticket, ParkCancelOperation) {
		t.Fatal("cancel worker operation")
	}
	if resolution, ok := ResolveParkSnapshot(state, ticket); !ok ||
		resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 1}) {
		t.Fatalf("resolve worker cancellation = (%+v, %t)", resolution, ok)
	}
	if applied, detached, ok := source.ApplyAndDetach(p); !ok || applied != 1 || detached != 1 {
		t.Fatalf("apply worker cancellation = (%d, %d, %t)", applied, detached, ok)
	}
	if source.ConfirmQuiesced(p, id) {
		t.Fatal("worker source quiesced with admitted producer")
	}
	if result := source.Post(id, payload); result != WorkerOperationPostClosed {
		t.Fatalf("new producer entered closed worker source = %d", result)
	}
	if !preemptCompareAndSwap(&slot.mailbox, uint32(workerOperationMailboxEmpty), uint32(workerOperationMailboxPosting)) {
		t.Fatal("publish late worker mailbox")
	}
	slot.payload = payload
	preemptStore(&slot.mailbox, uint32(workerOperationMailboxPosted))
	preemptStore(&source.pending, 1)
	if !producerAdmissionReleaseChecked(&slot.inflight) {
		t.Fatal("release late worker producer")
	}
	if source.ConfirmQuiesced(p, id) {
		t.Fatal("worker source quiesced before mailbox drain")
	}
	if published, lost, ok := source.PublishPass(p); !ok || published != 0 || lost != 1 {
		t.Fatalf("drain late worker loser = (%d, %d, %t)", published, lost, ok)
	}
	if slot.result != (ScalarResultCell{}) || !source.ConfirmQuiesced(p, id) {
		t.Fatal("confirm worker source after strong join and final drain")
	}
	if outcome, _, lease, consumed := ConsumeParkSet(state, ticket); !consumed ||
		outcome != ParkOutcomeCanceled || lease.Valid() {
		t.Fatalf("consume late worker cancellation = (%d, %+v, %t)", outcome, lease, consumed)
	}
	if !source.Recycle(p, id) || !UnbindWorkerOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("recycle late worker loser")
	}
}

func TestWorkerOperationSourceDeferredPublicationStaysSticky(t *testing.T) {
	p := new(P)
	source := new(WorkerOperationSource)
	if !BindWorkerOperationSource(source, p) {
		t.Fatal("bind deferred worker source")
	}
	state, ticket, ids := reserveWorkerWaitSet(t, source, p, 67, []uint32{3})
	id := ids[0]
	payload := workerPayloadForTest(t, 8, 808)
	state.resolving = true
	if source.Post(id, payload) != WorkerOperationPosted {
		t.Fatal("post deferred worker result")
	}
	if published, lost, ok := source.PublishPass(p); !ok || published != 0 || lost != 0 || !source.Pending() {
		t.Fatalf("defer worker publish = (%d, %d, %t), pending=%t", published, lost, ok, source.Pending())
	}
	slot, _ := workerOperationSlotFor(source, id)
	if preemptLoad(&slot.mailbox) != uint32(workerOperationMailboxPosted) || slot.result != (ScalarResultCell{}) ||
		operationCandidateIsPublished(&slot.record) {
		t.Fatal("deferred worker publication did not restore its sticky mailbox")
	}
	state.resolving = false
	if published, lost, ok := source.PublishPass(p); !ok || published != 1 || lost != 0 || source.Pending() {
		t.Fatalf("retry deferred worker publish = (%d, %d, %t), pending=%t", published, lost, ok, source.Pending())
	}
	if resolution, duplicates, ok := source.ResolveAffectedPublishedEpoch(p); !ok || duplicates != 0 ||
		resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}) {
		t.Fatalf("resolve deferred worker result = (%+v, %d, %t)", resolution, duplicates, ok)
	}
	if applied, detached, ok := source.ApplyAndDetach(p); !ok || applied != 1 || detached != 1 {
		t.Fatalf("apply deferred worker result = (%d, %d, %t)", applied, detached, ok)
	}
	outcome, _, lease, consumed := ConsumeParkSet(state, ticket)
	if !consumed || outcome != ParkOutcomeCompleted {
		t.Fatalf("consume deferred worker result = (%d, %+v, %t)", outcome, lease, consumed)
	}
	finishWorkerOperations(t, source, p, ids, lease, payload)
	if !UnbindWorkerOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release deferred worker source")
	}
}

func TestWorkerOperationSourceSchedulerWaitRecordPath(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	source := new(WorkerOperationSource)
	sources := new(ExecutorSourceSet)
	if !bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Waits: waits, Worker: source}) {
		t.Fatal("bind scheduler worker source catalog")
	}
	park := beginTimerV2TestPark(t, p, "worker-source-wait-record", 1, 71)
	id, ok := source.ReserveAndAttachWait(p, &park.task.g.park, park.ticket, park.wait, 19)
	if !ok {
		t.Fatal("reserve scheduler worker operation")
	}
	commitTimerV2TestPark(t, p, park)
	payload := workerPayloadForTest(t, 9, 900, 901, 902)
	if source.Post(id, payload) != WorkerOperationPosted {
		t.Fatal("post scheduler worker result")
	}
	if scan, ok := sources.publishPass(p, 0, false); !ok || scan.worker != 1 ||
		scan.workerLost != 0 || scan.completed != 1 {
		t.Fatalf("publish scheduler worker result = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("resolve scheduler worker wait = (%d, visits=%d, %t)", promoted, visits, ok)
	}
	if g, ok := NextRunnable(p); !ok || g != park.task.g {
		t.Fatal("dequeue scheduler worker result")
	}
	action := beginWaitTestResume(t, p, park.task)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(park.task.g, park.ticket)
	if !ok || outcome != ParkOutcomeCompleted || caseID != 19 || taskCancel != TaskCancelNone {
		t.Fatalf("take scheduler worker decision = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, ok)
	}
	finishWorkerOperations(t, source, p, []OperationID{id}, lease, payload)
	finishWaitTestTask(t, p, park.task, action)
	if !unbindExecutorSourceSet(sources, p) || !source.CanRelease() || !waits.CanRelease() {
		t.Fatal("release scheduler worker source catalog")
	}
}

func TestWorkerOperationSourceSubmittedCancellationAwaitsPhysicalCompletion(t *testing.T) {
	p := new(P)
	source := new(WorkerOperationSource)
	if !BindWorkerOperationSource(source, p) {
		t.Fatal("bind cancelable scheduler worker source")
	}
	park := beginTimerV2TestPark(t, p, "worker-source-cancel-await", 1, 73)
	id, ok := source.ReserveAndAttachWait(p, &park.task.g.park, park.ticket, park.wait, 23)
	if !ok || !source.MarkSubmitted(p, id) {
		t.Fatal("reserve and submit scheduler worker operation")
	}
	commitTimerV2TestPark(t, p, park)
	if !RequestWaitSetCancel(p, park.wait, ParkCancelOperation) {
		t.Fatal("request submitted worker cancellation")
	}
	batch, tail, resolution, ok := resolveAffectedWaitSets(p, nil)
	if !ok || batch != park.wait || tail != park.wait ||
		resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 1}) {
		t.Fatalf("resolve submitted worker cancellation = (%p, %p, %+v, %t)", batch, tail, resolution, ok)
	}
	slot, _ := workerOperationSlotFor(source, id)
	if got := source.ApplyOne(p, id, &slot.record); got != OperationApplyAwaitExternalFact {
		t.Fatalf("apply submitted worker cancellation = %d, want await-external", got)
	}
	if retry, await, ok := finishWaitSetApplyProgress(park.wait, false, true); !ok || retry || !await {
		t.Fatalf("finish submitted worker await = (%t, %t, %t)", retry, await, ok)
	}
	if ParkReady(&park.task.g.park, park.ticket) {
		t.Fatal("submitted worker cancellation promoted before physical completion")
	}
	payload := workerPayloadForTest(t, 10, 1000, 0, 0)
	if source.Post(id, payload) != WorkerOperationPosted {
		t.Fatal("post physically completed canceled worker")
	}
	if published, lost, ok := source.PublishPass(p); !ok || published != 0 || lost != 1 {
		t.Fatalf("publish canceled worker completion = (%d, %d, %t)", published, lost, ok)
	}
	batch, tail, resolution, ok = resolveAffectedWaitSets(p, nil)
	if !ok || batch != park.wait || tail != park.wait || resolution != (CompletionResolution{}) {
		t.Fatalf("revisit physically complete cancellation = (%p, %p, %+v, %t)", batch, tail, resolution, ok)
	}
	if got := source.ApplyOne(p, id, &slot.record); got != OperationApplyDetached {
		t.Fatalf("detach physically complete cancellation = %d", got)
	}
	if promoted, ok := promoteResolvedWaitSets(p, batch); !ok || promoted != 1 {
		t.Fatalf("promote physically complete cancellation = (%d, %t)", promoted, ok)
	}
	if g, ok := NextRunnable(p); !ok || g != park.task.g {
		t.Fatal("dequeue physically complete cancellation")
	}
	action := beginWaitTestResume(t, p, park.task)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(park.task.g, park.ticket)
	if !ok || outcome != ParkOutcomeCanceled || caseID != 0 || lease.Valid() || taskCancel != TaskCancelNone {
		t.Fatalf("take delayed worker cancellation = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, ok)
	}
	if !source.ConfirmQuiesced(p, id) || !source.Recycle(p, id) {
		t.Fatal("release physically complete canceled worker")
	}
	finishWaitTestTask(t, p, park.task, action)
	if !UnbindWorkerOperationSource(source, p) || !source.CanRelease() {
		t.Fatal("release submitted cancellation worker source")
	}
}

func TestWorkerOperationSourceProducerPrefixIsAlignedPOD(t *testing.T) {
	if unsafe.Offsetof(workerOperationSlot{}.producerSourceSlot) != 0 ||
		unsafe.Offsetof(workerOperationSlot{}.state)%4 != 0 ||
		unsafe.Offsetof(workerOperationSlot{}.generation)%4 != 0 ||
		unsafe.Offsetof(workerOperationSlot{}.inflight)%4 != 0 ||
		unsafe.Offsetof(workerOperationSlot{}.mailbox)%4 != 0 ||
		unsafe.Offsetof(workerOperationSlot{}.payload)%4 != 0 ||
		unsafe.Offsetof(workerOperationSlot{}.record) < unsafe.Offsetof(workerOperationSlot{}.payload)+unsafe.Sizeof(ScalarResultPayloadV1{}) {
		t.Fatalf("worker producer prefix layout: state=%d generation=%d inflight=%d mailbox=%d payload=%d record=%d",
			unsafe.Offsetof(workerOperationSlot{}.state), unsafe.Offsetof(workerOperationSlot{}.generation),
			unsafe.Offsetof(workerOperationSlot{}.inflight), unsafe.Offsetof(workerOperationSlot{}.mailbox),
			unsafe.Offsetof(workerOperationSlot{}.payload), unsafe.Offsetof(workerOperationSlot{}.record))
	}
}
