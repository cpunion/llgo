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
)

func addCompletionResolution(total *CompletionResolution, one CompletionResolution) {
	total.WaitSets += one.WaitSets
	total.Completed += one.Completed
	total.Canceled += one.Canceled
	total.Defaulted += one.Defaulted
	total.Winners += one.Winners
	total.Losers += one.Losers
}

func TestPublishedEpochResolutionHighCardinalityHasExactLinearSteps(t *testing.T) {
	const candidateCount = 2048

	p := new(P)
	task := newYieldingTestG(t, "bounded-resolution-high-cardinality")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue bounded high-cardinality task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue bounded high-cardinality task")
	}
	action := beginWaitTestResume(t, p, task)
	cases := make([]uint32, candidateCount)
	for index := range cases {
		cases[index] = uint32(index + 1)
	}
	operations := sealSchedulerParkV2(t, task.g, 103, cases...)
	commitSchedulerParkV2(t, p, task, action, operations)

	// A source set without Channel keeps the original rank-only path. No call
	// can consume two candidate links.
	var cursor publishedEpochResolveCursor
	initialSteps := 0
	for {
		step, ok := resolvePublishedEpochStep(nil, p, &cursor)
		if !ok || step.applyVisits != 0 || step.promoted != 0 {
			t.Fatalf("initial bounded step %d = (%+v, %t)", initialSteps, step, ok)
		}
		initialSteps++
		if step.complete {
			break
		}
	}
	if initialSteps != candidateCount+1 || task.g.park.phase != parkParked ||
		p.affectedWaitHead != nil || p.affectedWaitTail != nil {
		t.Fatalf("initial bounded pass = %d steps, phase=%d affected=(%p,%p)",
			initialSteps, task.g.park.phase, p.affectedWaitHead, p.affectedWaitTail)
	}

	publishSchedulerParkV2(t, p, operations, candidateCount/2)
	winnerVisits := 0
	for link := task.g.park.head; link != nil; link = link.next {
		winnerVisits++
		if link.operation == &operations.records[candidateCount/2] {
			break
		}
	}
	cursor = publishedEpochResolveCursor{}
	steps := 0
	resolution := CompletionResolution{}
	for {
		step, ok := resolvePublishedEpochStep(nil, p, &cursor)
		if !ok || step.applyVisits != 0 || step.promoted != 0 {
			t.Fatalf("terminal bounded step %d = (%+v, %t)", steps, step, ok)
		}
		addCompletionResolution(&resolution, step.resolution)
		steps++
		if step.complete {
			break
		}
	}
	// Seal sorted the list, so the unique resolver stops its scan at the first
	// published rank instead of duplicating the old all-candidate winner search.
	wantSteps := winnerVisits + candidateCount + 4 // prefix scan + settle + finalize/apply/finish/promote
	wantResolution := CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1, Losers: candidateCount - 1}
	if steps != wantSteps || task.g.park.seed != uint32(winnerVisits) || resolution != wantResolution || task.g.park.phase != parkDetaching ||
		task.g.park.attached != candidateCount || p.affectedWaitHead != &operations.wait ||
		p.affectedWaitTail != &operations.wait {
		t.Fatalf("terminal bounded pass = steps:%d/%d resolution:%+v phase:%d attached:%d affected=(%p,%p)",
			steps, wantSteps, resolution, task.g.park.phase, task.g.park.attached,
			p.affectedWaitHead, p.affectedWaitTail)
	}

	for index := range operations.records {
		detachSchedulerParkV2(t, task.g, operations, index)
	}
	if promoted, ok := PollReady(p); !ok || promoted != 1 || HasWaiting(p) {
		t.Fatalf("promote bounded high-cardinality task = (%d, %t), waiting=%t", promoted, ok, HasWaiting(p))
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue bounded high-cardinality result")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(task.g, operations.ticket)
	if !ok || outcome != ParkOutcomeCompleted || caseID != cases[candidateCount/2] ||
		taskCancel != TaskCancelNone || !lease.Valid() {
		t.Fatalf("take bounded high-cardinality result = (%d, %d, %+v, %d, %t)",
			outcome, caseID, lease, taskCancel, ok)
	}
	finishSchedulerParkV2Operations(t, operations, lease)
	finishWaitTestTask(t, p, task, action)
	runtime.KeepAlive(task.frame.memory)
}

func TestSchedulerOnlyReadyCommitFailsClosedAndRestoresAffectedSnapshot(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "bounded-resolution-no-ready-dispatch")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue scheduler-only Ready task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue scheduler-only Ready task")
	}
	action := beginWaitTestResume(t, p, task)
	ticket, ok := BeginParkSet(&task.g.park, 1, 105)
	var wait WaitSetRecord
	if !ok || !PrepareWaitSetRecord(&wait, task.g, ticket) {
		t.Fatal("prepare scheduler-only Ready wait-set")
	}
	id, idOK := MakeOperationID(OperationSourceHost, 1, 1)
	var record OperationRecord
	if !idOK || !InitOperation(&record, id) ||
		!DeclareOperationCommitMode(&record, OperationCommitReadyThenTryCommit) ||
		!AttachParkWaitOperation(&task.g.park, ticket, &wait, &record, 7) ||
		!SealParkSet(&task.g.park, ticket) {
		t.Fatal("attach scheduler-only Ready candidate")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(task.g, task.handle, task.frame.header, ticket, &wait) {
		t.Fatal("prepare scheduler-only Ready park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit scheduler-only Ready park = (%+v, %t)", action, ok)
	}
	if PublishReadyThenTryCommitCandidate(&record, id) != OperationCompletionPublished ||
		!MarkWaitSetAffected(p, &wait) {
		t.Fatal("publish scheduler-only Ready hint")
	}
	beforeState, beforeRecord := task.g.park, record
	if promoted, polled := PollReady(p); polled || promoted != 0 || task.g.park != beforeState || record != beforeRecord ||
		wait.work != waitSetWorkQueued || p.affectedWaitHead != &wait || p.affectedWaitTail != &wait {
		t.Fatalf("scheduler-only Ready fail-close = promoted:%d polled:%t state:%+v record:%+v work:%d affected=(%p,%p)",
			promoted, polled, task.g.park, record, wait.work, p.affectedWaitHead, p.affectedWaitTail)
	}

	if !RequestWaitSetCancel(p, &wait, ParkCancelTaskAbort) {
		t.Fatal("cancel scheduler-only Ready fixture")
	}
	if promoted, polled := PollReady(p); !polled || promoted != 0 || task.g.park.phase != parkDetaching ||
		p.affectedWaitHead != &wait || p.affectedWaitTail != &wait {
		t.Fatalf("resolve scheduler-only Ready cleanup = (%d, %t), phase=%d affected=(%p,%p)",
			promoted, polled, task.g.park.phase, p.affectedWaitHead, p.affectedWaitTail)
	}
	disposition, dispositionOK := OperationDispositionOf(&record, id)
	if !dispositionOK || disposition != OperationDispositionCanceled {
		t.Fatal("read scheduler-only Ready cleanup disposition")
	}
	discardUnselectedTestResult(t, &record, id)
	if !AcknowledgeOperationResolution(&record, id, disposition) ||
		!DetachParkWaitOperation(&task.g.park, ticket, &record, id) {
		t.Fatal("detach scheduler-only Ready cleanup")
	}
	if promoted, polled := PollReady(p); !polled || promoted != 1 {
		t.Fatalf("promote scheduler-only Ready cleanup = (%d, %t)", promoted, polled)
	}
	if g, runnable := NextRunnable(p); !runnable || g != task.g {
		t.Fatal("dequeue scheduler-only Ready cleanup")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskCancel, decisionOK := TakeRunDecision(task.g, ticket)
	if !decisionOK || outcome != ParkOutcomeCanceled || caseID != 0 || lease.Valid() || taskCancel != TaskCancelNone {
		t.Fatalf("take scheduler-only Ready cleanup = (%d, %d, %+v, %d, %t)",
			outcome, caseID, lease, taskCancel, decisionOK)
	}
	if !ConfirmOperationQuiesced(&record, id) || !OperationCanRecycle(&record, id) || !RecycleOperation(&record, id) {
		t.Fatal("recycle scheduler-only Ready cleanup")
	}
	finishWaitTestTask(t, p, task, action)
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorBudgetOneBoundsCandidateWorkAndPreservesABFairness(t *testing.T) {
	p := new(P)
	driver, registry, _, manual, handle := bindTestExecutorDriverWithManual(t, p)
	task := newYieldingTestG(t, "bounded-resolution-driver")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue bounded driver task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue bounded driver task")
	}
	action := beginWaitTestResume(t, p, task)
	ticket, ok := BeginParkSet(&task.g.park, ManualOperationSourceCapacity, 107)
	var wait WaitSetRecord
	if !ok || !PrepareWaitSetRecord(&wait, task.g, ticket) {
		t.Fatal("prepare bounded driver wait-set")
	}
	ids := make([]OperationID, ManualOperationSourceCapacity)
	for index := range ids {
		var attached bool
		ids[index], attached = manual.ReserveAndAttachWait(p, &task.g.park, ticket, &wait, uint32(index+1))
		if !attached {
			t.Fatalf("attach bounded driver candidate %d", index)
		}
	}
	if !SealParkSet(&task.g.park, ticket) {
		t.Fatal("seal bounded driver wait-set")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(task.g, task.handle, task.frame.header, ticket, &wait) {
		t.Fatal("prepare bounded driver park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit bounded driver park = (%+v, %t)", action, ok)
	}
	if posted := manual.Post(ids[0]); posted != ManualOperationPosted ||
		registry.Request(handle) != ExecutorRequestPublished {
		t.Fatal("publish bounded driver winner")
	}

	latePosted := false
	ackReached := false
	steps := 0
	previousApply := uint32(0)
	var complete ExecutorPollProgress
	for steps < 10000 {
		progress, advanced := PollExecutorSlice(driver, 1)
		if !advanced || progress.Used != 1 || progress.AtomicResolve || progress.Overshot ||
			progress.ApplyVisits < previousApply || progress.ApplyVisits-previousApply > 1 {
			t.Fatalf("budget-one candidate step %d = (%+v, %t), prior apply=%d", steps, progress, advanced, previousApply)
		}
		previousApply = progress.ApplyVisits
		steps++
		if !latePosted && driver.poll.phase == executorPollEpochAResolve &&
			driver.poll.resolve.phase != publishedEpochResolveIdle {
			if posted := manual.Post(ids[1]); posted != ManualOperationPosted ||
				registry.Request(handle) != ExecutorRequestCoalesced {
				t.Fatalf("publish completion behind A snapshot = %d", posted)
			}
			latePosted = true
		}
		if driver.poll.phase == executorPollAcknowledge {
			ackReached = true
			lateSlot, _ := manualOperationSlotFor(manual, ids[1])
			if manualOperationMailbox(preemptLoad(&lateSlot.mailbox)) != manualOperationMailboxPosted {
				t.Fatal("A resolution consumed producer fact published behind its catalog cursor")
			}
		}
		if progress.Complete {
			complete = progress
			break
		}
	}
	if !latePosted || !ackReached || !complete.Complete || complete.Epochs != 2 ||
		complete.ApplyVisits != ManualOperationSourceCapacity || complete.Manual != 1 || complete.ManualLost != 1 ||
		complete.Promoted != 1 || !complete.More || complete.Blocked || driver.poll != (executorPollTransaction{}) {
		t.Fatalf("bounded A/ack/B result = late:%t ack:%t steps:%d progress:%+v poll:%+v",
			latePosted, ackReached, steps, complete, driver.poll)
	}

	for index, id := range ids {
		slot, _ := manualOperationSlotFor(manual, id)
		want := OperationDispositionLost
		if index == 0 {
			want = OperationDispositionWinner
		}
		if slot.record.phase != operationDetached || slot.record.disposition != want || !slot.record.resolutionApplied {
			t.Fatalf("bounded candidate %d = phase:%d disposition:%d applied:%t", index,
				slot.record.phase, slot.record.disposition, slot.record.resolutionApplied)
		}
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue bounded driver result")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(task.g, ticket)
	if !ok || outcome != ParkOutcomeCompleted || caseID != 1 || taskCancel != TaskCancelNone || !lease.Valid() {
		t.Fatalf("take bounded driver result = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, ok)
	}
	for _, id := range ids {
		if !manual.ConfirmQuiesced(p, id) {
			t.Fatalf("quiesce bounded candidate %+v", id)
		}
	}
	if !manual.TakeResult(p, lease) {
		t.Fatal("take bounded winner result")
	}
	for _, id := range ids {
		if !manual.Recycle(p, id) {
			t.Fatalf("recycle bounded candidate %+v", id)
		}
	}
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	runtime.KeepAlive(task.frame.memory)
}

func TestPublishedEpochAwaitExternalStaysOffWorkQueueUntilNewFact(t *testing.T) {
	p := new(P)
	driver, registry, _, manual, handle := bindTestExecutorDriverWithManual(t, p)
	task := newYieldingTestG(t, "bounded-resolution-external")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue external-wait task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue external-wait task")
	}
	action := beginWaitTestResume(t, p, task)
	ticket, ok := BeginParkSet(&task.g.park, 1, 109)
	var wait WaitSetRecord
	if !ok || !PrepareWaitSetRecord(&wait, task.g, ticket) {
		t.Fatal("prepare external-wait set")
	}
	id, attached := manual.ReserveAndAttachWait(p, &task.g.park, ticket, &wait, 17)
	if !attached || !SealParkSet(&task.g.park, ticket) {
		t.Fatal("attach external-wait candidate")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(task.g, task.handle, task.frame.header, ticket, &wait) {
		t.Fatal("prepare external-wait park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit external-wait park = (%+v, %t)", action, ok)
	}
	slot, _ := manualOperationSlotFor(manual, id)
	if PublishOperationCompletion(&slot.record, id) != OperationCompletionPublished {
		t.Fatal("publish owner-side external-wait completion")
	}
	batch, _, resolution, resolved := resolveAffectedWaitSets(p, &driver.sources)
	if !resolved || batch != &wait || resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}) ||
		wait.work != waitSetWorkResolving || task.g.park.phase != parkDetaching {
		t.Fatalf("prepare external acknowledgement gap = batch:%p resolution:%+v resolved:%t work:%d phase:%d",
			batch, resolution, resolved, wait.work, task.g.park.phase)
	}

	cursor := publishedEpochResolveCursor{
		wait:      &wait,
		batchTail: &wait,
		phase:     publishedEpochResolveFinish,
		waitAwait: true,
	}
	step, advanced := resolvePublishedEpochStep(&driver.sources, p, &cursor)
	if !advanced || step.complete || step.retryBudget || !step.awaitExternal ||
		wait.work != waitSetWorkAwaitingExternal || cursor.phase != publishedEpochResolvePromote {
		t.Fatalf("finish external-wait action = (%+v, %t), work=%d cursor=%+v", step, advanced, wait.work, cursor)
	}
	step, advanced = resolvePublishedEpochStep(&driver.sources, p, &cursor)
	if !advanced || !step.complete || p.affectedWaitHead != nil || p.affectedWaitTail != nil || driver.sources.pending(p) {
		t.Fatalf("park external-wait action = (%+v, %t), affected=(%p,%p) pending=%t",
			step, advanced, p.affectedWaitHead, p.affectedWaitTail, driver.sources.pending(p))
	}

	budget, budgetOK := MinExecutorPollBudget(driver)
	if !budgetOK {
		t.Fatal("external-wait base budget")
	}
	progress, polled := PollExecutorSlice(driver, budget)
	if !polled || !progress.Complete || progress.More || !progress.Blocked || progress.ApplyVisits != 0 ||
		wait.work != waitSetWorkAwaitingExternal {
		t.Fatalf("event-free external poll = (%+v, %t), work=%d", progress, polled, wait.work)
	}
	if !MarkWaitSetAffected(p, &wait) || registry.Request(handle) != ExecutorRequestPublished {
		t.Fatal("republish external acknowledgement")
	}
	if _, promoted, ok := PollExecutor(driver); !ok || promoted != 1 || wait != (WaitSetRecord{}) {
		t.Fatalf("apply external acknowledgement = promoted:%d ok:%t wait:%+v", promoted, ok, wait)
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue externally acknowledged task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(task.g, ticket)
	if !ok || outcome != ParkOutcomeCompleted || caseID != 17 || taskCancel != TaskCancelNone || !lease.Valid() {
		t.Fatalf("take external-wait result = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, ok)
	}
	if !manual.ConfirmQuiesced(p, id) || !manual.TakeResult(p, lease) || !manual.Recycle(p, id) {
		t.Fatal("release externally acknowledged operation")
	}
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	runtime.KeepAlive(task.frame.memory)
}
