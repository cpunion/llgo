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

func TestExecutorSourceSetScansCompleteStaticCatalog(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	timers := new(TimerRegistrationTable)
	sources := new(ExecutorSourceSet)
	if !bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Waits: waits, Timers: timers}) || !validExecutorSourceSet(sources, p) {
		t.Fatal("bind source set")
	}

	waitToken, waitTicket, wait := registerTestWait(t, waits, p)
	timerToken, timerTicket, timer := prepareTestTimer(t, timers, p, 100)
	if posted := waits.Post(wait); posted != WaitRegistrationPosted || !sources.pending(p) {
		t.Fatalf("post aggregate wait = %d, pending=%t", posted, sources.pending(p))
	}

	scan, ok := sources.publishPass(p, 90, true)
	if !ok || scan.completed != 1 || scan.waits != 1 || scan.timers != 0 || scan.promoted != 0 ||
		!scan.hasDeadline || scan.deadline != 100 || sources.pending(p) {
		t.Fatalf("first aggregate scan = %+v, ok=%t, pending=%t", scan, ok, sources.pending(p))
	}
	consumeRegisteredOutcome(t, waitToken, waitTicket, WaitOutcomeCompleted)
	if result := waits.BeginClose(wait); result != WaitRegistrationCloseStarted {
		t.Fatalf("begin completed aggregate wait close = %d", result)
	}
	if result, ok := waits.ConfirmQuiesced(wait); !ok || result != WaitCancelCompletionWon || !waits.Retire(wait) {
		t.Fatalf("retire completed aggregate wait = (%d, %t)", result, ok)
	}

	scan, ok = sources.publishPass(p, 100, true)
	if !ok || scan.completed != 1 || scan.waits != 0 || scan.timers != 1 || scan.promoted != 0 ||
		scan.hasDeadline || scan.deadline != 0 {
		t.Fatalf("second aggregate scan = %+v, ok=%t", scan, ok)
	}
	consumeTimerOutcome(t, timerToken, timerTicket, WaitOutcomeCompleted)
	if !timers.RetireCompletedTimer(timer, timerToken, timerTicket) || !sources.empty(p) {
		t.Fatal("retire aggregate timer")
	}
	if closeScan, ok := sources.drainForClose(p); !ok || closeScan.completed != 0 {
		t.Fatalf("final aggregate scan = %+v, ok=%t", closeScan, ok)
	}
	if !unbindExecutorSourceSet(sources, p) || *sources != (ExecutorSourceSet{}) ||
		!waits.CanRelease() || !timers.CanRelease() {
		t.Fatal("unbind source set")
	}
}

func TestExecutorSourceSetDefersPromotionUntilPublishedEpochResolution(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	sources := new(ExecutorSourceSet)
	if !bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Waits: waits}) {
		t.Fatal("bind source set")
	}

	task := newYieldingTestG(t, "published-epoch")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue published-epoch task")
	}
	g, ok := NextRunnable(p)
	if !ok || g != task.g {
		t.Fatalf("dequeue published-epoch task = (%p, %t)", g, ok)
	}
	action := beginWaitTestResume(t, p, task)
	token, ticket, wait := registerTestWait(t, waits, p)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("prepare published-epoch park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit published-epoch park = (%+v, %t)", action, ok)
	}
	if posted := waits.Post(wait); posted != WaitRegistrationPosted {
		t.Fatalf("post published-epoch wait = %d", posted)
	}

	scan, ok := sources.publishPass(p, 0, false)
	if !ok || scan.completed != 1 || scan.promoted != 0 {
		t.Fatalf("published-epoch publish = (%+v, %t)", scan, ok)
	}
	if !task.g.waiting || task.g.state != GWaiting || p.readyHead != nil {
		t.Fatal("publish pass promoted a G before epoch resolution")
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 0 {
		t.Fatalf("published-epoch resolve = (%d, visits=%d, %t)", promoted, visits, ok)
	}
	if task.g.waiting || task.g.state != GRunnable || p.readyHead != task.g {
		t.Fatal("published-epoch resolve did not promote the completed G")
	}

	retireCompletedRegistration(t, waits, wait)
	if !unbindExecutorSourceSet(sources, p) {
		t.Fatal("unbind source set")
	}
	g, ok = NextRunnable(p)
	if !ok || g != task.g {
		t.Fatalf("dequeue promoted published-epoch task = (%p, %t)", g, ok)
	}
	finishWaitTestTask(t, p, task, beginWaitTestResume(t, p, task))
}

func TestExecutorSourceSetRejectsStandaloneAffectedOperationBeforeResolution(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	manual := new(ManualOperationSource)
	sources := new(ExecutorSourceSet)
	if !bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Waits: waits, Manual: manual}) {
		t.Fatal("bind standalone-rejection source set")
	}
	state, ticket, ids := reserveManualWaitSet(t, manual, p, 83, []uint32{9})
	if result := manual.Post(ids[0]); result != ManualOperationPosted {
		t.Fatalf("post standalone operation = %d", result)
	}
	if scan, ok := sources.publishPass(p, 0, false); !ok || scan.manual != 1 || scan.completed != 1 {
		t.Fatalf("publish standalone operation = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); ok || promoted != 0 || visits != 0 {
		t.Fatalf("source set accepted standalone affected operation = (%d, %d, %t)", promoted, visits, ok)
	}
	if state.phase != parkParked || state.outcome != ParkOutcomePending || manual.affectedHead == 0 {
		t.Fatal("source set consumed standalone logical resolution before rejecting it")
	}

	if resolution, duplicates, ok := manual.ResolveAffectedPublishedEpoch(p); !ok || duplicates != 0 ||
		resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1}) {
		t.Fatalf("standalone recovery resolve = (%+v, %d, %t)", resolution, duplicates, ok)
	}
	if applied, detached, ok := manual.ApplyAndDetach(p); !ok || applied != 1 || detached != 1 {
		t.Fatalf("standalone recovery apply = (%d, %d, %t)", applied, detached, ok)
	}
	outcome, _, lease, consumed := ConsumeParkSet(state, ticket)
	if !consumed || outcome != ParkOutcomeCompleted || !lease.Valid() {
		t.Fatalf("consume standalone recovery = (%d, %+v, %t)", outcome, lease, consumed)
	}
	finishManualOperations(t, manual, p, ids, lease)
	if !unbindExecutorSourceSet(sources, p) || !manual.CanRelease() || !waits.CanRelease() {
		t.Fatal("release standalone-rejection source set")
	}
}

func TestExecutorSourceSetDeferredBatchRemainsPendingForExactRetry(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	manual := new(ManualOperationSource)
	sources := new(ExecutorSourceSet)
	if !bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Waits: waits, Manual: manual}) {
		t.Fatal("bind deferred-batch source set")
	}
	task := newYieldingTestG(t, "source-set-deferred")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue deferred-batch task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue deferred-batch task")
	}
	action := beginWaitTestResume(t, p, task)
	ticket, ok := BeginParkSet(&task.g.park, 1, 89)
	var wait WaitSetRecord
	if !ok || !PrepareWaitSetRecord(&wait, task.g, ticket) {
		t.Fatal("prepare deferred wait-set")
	}
	id, attached := manual.ReserveAndAttachWait(p, &task.g.park, ticket, &wait, 19)
	if !attached || !SealParkSet(&task.g.park, ticket) {
		t.Fatal("attach deferred manual operation")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(task.g, task.handle, task.frame.header, ticket, &wait) {
		t.Fatal("prepare deferred scheduler park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit deferred scheduler park = (%+v, %t)", action, ok)
	}
	if result := manual.Post(id); result != ManualOperationPosted {
		t.Fatalf("post deferred operation = %d", result)
	}
	if scan, ok := sources.publishPass(p, 0, false); !ok || scan.manual != 1 {
		t.Fatalf("publish deferred operation = (%+v, %t)", scan, ok)
	}
	if standalone, valid := manual.standaloneAffected(p); !valid || standalone {
		t.Fatalf("scheduler operation entered standalone chain = (%t, %t)", standalone, valid)
	}
	if _, _, resolved := manual.ResolveAffectedPublishedEpoch(p); !resolved {
		t.Fatal("resolve empty manual source-local phase")
	}
	batch, _, _, resolved := resolveAffectedWaitSets(p)
	if !resolved || batch != &wait || task.g.park.phase != parkDetaching {
		t.Fatal("resolve deferred scheduler batch")
	}
	// Model a source-specific ApplyOne returning Deferred: no link is detached,
	// and promotion must put this exact WaitSetRecord back on owner work.
	if promoted, ok := promoteResolvedWaitSets(p, batch); !ok || promoted != 0 ||
		p.affectedWaitHead != &wait || p.affectedWaitTail != &wait || !sources.pending(p) {
		t.Fatalf("deferred batch requeue = (%d, %t), pending=%t", promoted, ok, sources.pending(p))
	}

	retry, _, _, resolved := resolveAffectedWaitSets(p)
	if !resolved || retry != &wait {
		t.Fatal("pop exact deferred retry batch")
	}
	if visits, applied := sources.applyResolvedWaitSetBatch(p, retry); !applied || visits != 1 {
		t.Fatalf("apply exact deferred retry = (%d, %t)", visits, applied)
	}
	if promoted, ok := promoteResolvedWaitSets(p, retry); !ok || promoted != 1 || sources.pending(p) {
		t.Fatalf("promote exact deferred retry = (%d, %t), pending=%t", promoted, ok, sources.pending(p))
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue deferred-retry task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskCancel, decisionOK := TakeRunDecision(task.g, ticket)
	if !decisionOK || outcome != ParkOutcomeCompleted || caseID != 19 || taskCancel != TaskCancelNone || !lease.Valid() {
		t.Fatalf("take deferred-retry decision = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, decisionOK)
	}
	if !manual.ConfirmQuiesced(p, id) || !manual.TakeResult(p, lease) || !manual.Recycle(p, id) {
		t.Fatal("release deferred-retry operation")
	}
	if !unbindExecutorSourceSet(sources, p) {
		t.Fatal("unbind deferred-batch source set")
	}
	finishWaitTestTask(t, p, task, action)
}

func TestExecutorSourceSetBindRollsBackEarlierSources(t *testing.T) {
	p := new(P)
	other := new(P)
	waits := new(WaitRegistrationTable)
	timers := new(TimerRegistrationTable)
	if !bindTimerRegistrationTable(timers, other) {
		t.Fatal("bind conflicting timer source")
	}

	sources := new(ExecutorSourceSet)
	if bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Waits: waits, Timers: timers}) || *sources != (ExecutorSourceSet{}) ||
		!waits.CanRelease() || waits.owner != nil || timers.owner != other {
		t.Fatal("failed source-set bind did not roll back transaction")
	}
	if !unbindTimerRegistrationTable(timers, other) || !timers.CanRelease() {
		t.Fatal("release conflicting timer source")
	}
}

func TestExecutorSourceSetBindRollsBackWaitAndTimerBeforeOwnedManualSource(t *testing.T) {
	p := new(P)
	other := new(P)
	waits := new(WaitRegistrationTable)
	timers := new(TimerRegistrationTable)
	manual := new(ManualOperationSource)
	if !BindManualOperationSource(manual, other) {
		t.Fatal("bind conflicting manual source")
	}

	sources := new(ExecutorSourceSet)
	if bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Waits: waits, Timers: timers, Manual: manual}) ||
		*sources != (ExecutorSourceSet{}) || !waits.CanRelease() || !timers.CanRelease() || manual.owner != other {
		t.Fatal("failed manual-source bind did not roll back earlier source bindings")
	}
	if !UnbindManualOperationSource(manual, other) || !manual.CanRelease() {
		t.Fatal("release conflicting manual source")
	}
}

func TestExecutorSourceSetBindRollsBackOperationSourcesBeforeOwnedControlSource(t *testing.T) {
	p := new(P)
	other := new(P)
	waits := new(WaitRegistrationTable)
	timers := new(TimerRegistrationTable)
	manual := new(ManualOperationSource)
	control := new(TaskControlSource)
	if !BindTaskControlSource(control, other) {
		t.Fatal("bind conflicting control source")
	}

	sources := new(ExecutorSourceSet)
	catalog := ExecutorSourceCatalog{Waits: waits, Timers: timers, Manual: manual, Control: control}
	if bindExecutorSourceSet(sources, p, catalog) || *sources != (ExecutorSourceSet{}) ||
		!waits.CanRelease() || !timers.CanRelease() || !manual.CanRelease() || control.owner != other {
		t.Fatal("failed control-source bind did not roll back earlier source bindings")
	}
	if !UnbindTaskControlSource(control, other) || !control.CanRelease() {
		t.Fatal("release conflicting control source")
	}
}
