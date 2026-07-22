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

func TestExecutorSourceSetRejectsStandaloneAffectedOperationBeforeResolution(t *testing.T) {
	p := new(P)
	manual := new(ManualOperationSource)
	sources := new(ExecutorSourceSet)
	if !bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Manual: manual}) {
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
	if !unbindExecutorSourceSet(sources, p) || !manual.CanRelease() {
		t.Fatal("release standalone-rejection source set")
	}
}

func TestExecutorSourceSetRetryBudgetAndExternalFactHaveDistinctScheduling(t *testing.T) {
	p := new(P)
	manual := new(ManualOperationSource)
	sources := new(ExecutorSourceSet)
	if !bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Manual: manual}) {
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
	batch, _, _, resolved := resolveAffectedWaitSets(p, sources)
	if !resolved || batch != &wait || task.g.park.phase != parkDetaching {
		t.Fatal("resolve deferred scheduler batch")
	}
	// Model ApplyOne returning RetryBudget: no link is detached, and promotion
	// must put this exact WaitSetRecord back on owner work.
	if promoted, ok := promoteResolvedWaitSets(p, batch); !ok || promoted != 0 ||
		p.affectedWaitHead != &wait || p.affectedWaitTail != &wait || !sources.pending(p) {
		t.Fatalf("budget retry requeue = (%d, %t), pending=%t", promoted, ok, sources.pending(p))
	}

	retry, _, _, resolved := resolveAffectedWaitSets(p, sources)
	if !resolved || retry != &wait {
		t.Fatal("pop exact budget retry batch")
	}
	// The same retained operation may instead be waiting for physical backend
	// acknowledgement. It must leave owner work until its source publishes that
	// fact; otherwise More would cause an event-free busy loop.
	retry.work = waitSetWorkAwaitingExternal
	if promoted, ok := promoteResolvedWaitSets(p, retry); !ok || promoted != 0 ||
		p.affectedWaitHead != nil || p.affectedWaitTail != nil || sources.pending(p) {
		t.Fatalf("external-fact wait = (%d, %t), pending=%t", promoted, ok, sources.pending(p))
	}
	if !MarkWaitSetAffected(p, &wait) || !sources.pending(p) {
		t.Fatal("external acknowledgement did not republish exact wait-set")
	}
	retry, _, _, resolved = resolveAffectedWaitSets(p, sources)
	if !resolved || retry != &wait {
		t.Fatal("pop external acknowledgement retry batch")
	}
	if visits, applied := sources.applyResolvedWaitSetBatch(p, retry); !applied || visits != 1 {
		t.Fatalf("apply exact acknowledged retry = (%d, %t)", visits, applied)
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

func TestExecutorSourceSetDirtyApplyBeatsAwaitExternal(t *testing.T) {
	wait := WaitSetRecord{work: waitSetWorkResolving}
	// Model an owner-side source calling MarkWaitSetAffected from inside
	// ApplyOne and then returning AwaitExternalFact. The mark changes Resolving
	// to ResolvingDirty; the post-call observation must preserve that fact as a
	// runnable retry instead of overwriting it with AwaitingExternal.
	wait.work = waitSetWorkResolvingDirty
	retry, await, ok := finishWaitSetApplyProgress(&wait, false, true)
	if !ok || !retry || await || wait.work != waitSetWorkResolvingDirty {
		t.Fatalf("dirty apply/await classification = (%t, %t, %t), work=%d", retry, await, ok, wait.work)
	}
}

func TestExecutorSourceSetBindRollsBackEarlierSources(t *testing.T) {
	p := new(P)
	other := new(P)
	timers := new(TimerRegistrationTable)
	if !bindTimerRegistrationTable(timers, other) {
		t.Fatal("bind conflicting timer source")
	}

	sources := new(ExecutorSourceSet)
	if bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Timers: timers}) || *sources != (ExecutorSourceSet{}) ||
		timers.owner != other {
		t.Fatal("failed source-set bind did not roll back transaction")
	}
	if !unbindTimerRegistrationTable(timers, other) || !timers.CanRelease() {
		t.Fatal("release conflicting timer source")
	}
}

func TestExecutorSourceSetBindRollsBackTimerBeforeOwnedManualSource(t *testing.T) {
	p := new(P)
	other := new(P)
	timers := new(TimerRegistrationTable)
	manual := new(ManualOperationSource)
	if !BindManualOperationSource(manual, other) {
		t.Fatal("bind conflicting manual source")
	}

	sources := new(ExecutorSourceSet)
	if bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Timers: timers, Manual: manual}) ||
		*sources != (ExecutorSourceSet{}) || !timers.CanRelease() || manual.owner != other {
		t.Fatal("failed manual-source bind did not roll back earlier source bindings")
	}
	if !UnbindManualOperationSource(manual, other) || !manual.CanRelease() {
		t.Fatal("release conflicting manual source")
	}
}

func TestExecutorSourceSetBindRollsBackOperationSourcesBeforeOwnedControlSource(t *testing.T) {
	p := new(P)
	other := new(P)
	timers := new(TimerRegistrationTable)
	manual := new(ManualOperationSource)
	control := new(TaskControlSource)
	if !BindTaskControlSource(control, other) {
		t.Fatal("bind conflicting control source")
	}

	sources := new(ExecutorSourceSet)
	catalog := ExecutorSourceCatalog{Timers: timers, Manual: manual, Control: control}
	if bindExecutorSourceSet(sources, p, catalog) || *sources != (ExecutorSourceSet{}) ||
		!timers.CanRelease() || !manual.CanRelease() || control.owner != other {
		t.Fatal("failed control-source bind did not roll back earlier source bindings")
	}
	if !UnbindTaskControlSource(control, other) || !control.CanRelease() {
		t.Fatal("release conflicting control source")
	}
}
