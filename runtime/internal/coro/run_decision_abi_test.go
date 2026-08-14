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

func TestTakeRunDecisionWordsReturnsZeroTicketTaskCancellationExactlyOnce(t *testing.T) {
	for _, test := range []struct {
		name string
		kind TaskCancelKind
	}{
		{name: "abort", kind: TaskCancelAbort},
		{name: "shutdown", kind: TaskCancelShutdown},
	} {
		t.Run(test.name, func(t *testing.T) {
			kind := test.kind
			p := new(P)
			task := newYieldingTestG(t, "run-decision-words-task-cancel")
			if !Enqueue(p, task.g) || !RequestTaskCancellation(p, task.g, kind) {
				t.Fatalf("enqueue/request task cancellation %d", kind)
			}
			if g, ok := NextRunnable(p); !ok || g != task.g {
				t.Fatal("dequeue task cancellation decision")
			}
			_ = beginWaitTestResume(t, p, task)
			outcome, caseID, taskKind, sourceSlot, generation, ok := TakeRunDecisionWords(task.g, 0, 0)
			if !ok || outcome != 0 || caseID != 0 || taskKind != uint32(kind) || sourceSlot != 0 || generation != 0 ||
				task.g.park.taskCancelPhase != taskCancelCleanup {
				t.Fatalf("task cancellation %d words = (%d,%d,%d,%d,%d,%t), phase=%d",
					kind, outcome, caseID, taskKind, sourceSlot, generation, ok, task.g.park.taskCancelPhase)
			}
			if outcome, caseID, taskKind, sourceSlot, generation, ok = TakeRunDecisionWords(task.g, 0, 0); ok ||
				outcome != 0 || caseID != 0 || taskKind != 0 || sourceSlot != 0 || generation != 0 {
				t.Fatalf("task cancellation %d replay = (%d,%d,%d,%d,%d,%t)",
					kind, outcome, caseID, taskKind, sourceSlot, generation, ok)
			}
		})
	}
}

func TestTakeRunDecisionWordsAcceptsZeroTicketNormalResume(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "run-decision-words-normal")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue normal scalar decision task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue normal scalar decision task")
	}
	action := beginWaitTestResumeWithoutGate(t, p, task)
	outcome, caseID, taskKind, sourceSlot, generation, ok := TakeRunDecisionWords(task.g, 0, 0)
	if !ok || outcome != uint32(ParkOutcomePending) || caseID != 0 || taskKind != uint32(TaskCancelNone) ||
		sourceSlot != 0 || generation != 0 {
		t.Fatalf("normal scalar decision = (%d,%d,%d,%d,%d,%t)", outcome, caseID, taskKind, sourceSlot, generation, ok)
	}
	if outcome, caseID, taskKind, sourceSlot, generation, ok = TakeRunDecisionWords(task.g, 0, 0); ok ||
		outcome != 0 || caseID != 0 || taskKind != 0 || sourceSlot != 0 || generation != 0 {
		t.Fatalf("duplicate normal scalar decision = (%d,%d,%d,%d,%d,%t)", outcome, caseID, taskKind, sourceSlot, generation, ok)
	}
	finishWaitTestTask(t, p, task, action)
}

func TestTakeRunDecisionWordsPreservesExactTicketAndScalarizesLease(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "run-decision-words")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue scalar decision task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue scalar decision task")
	}
	action := beginWaitTestResume(t, p, task)
	operations := sealSchedulerParkV2(t, task.g, 29, 71)
	publishSchedulerParkV2(t, p, operations, 0)
	commitSchedulerParkV2(t, p, task, action, operations)
	if count, ok := PollReady(p); !ok || count != 0 {
		t.Fatalf("resolve scalar decision park = (%d, %t)", count, ok)
	}
	detachSchedulerParkV2(t, p, task.g, operations, 0)
	if count, ok := PollReady(p); !ok || count != 1 {
		t.Fatalf("promote scalar decision park = (%d, %t)", count, ok)
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue promoted scalar decision task")
	}
	action = beginWaitTestResume(t, p, task)

	wrongGeneration := operations.ticket.generation + 1
	if outcome, caseID, taskKind, sourceSlot, generation, ok := TakeRunDecisionWords(
		task.g, operations.ticket.epoch, wrongGeneration,
	); ok || outcome != 0 || caseID != 0 || taskKind != 0 || sourceSlot != 0 || generation != 0 ||
		p.runDecision == (RunDecision{}) || p.runDecisionTaken {
		t.Fatalf("stale scalar take = (%d,%d,%d,%d,%d,%t), retained=%t taken=%t",
			outcome, caseID, taskKind, sourceSlot, generation, ok,
			p.runDecision != (RunDecision{}), p.runDecisionTaken)
	}
	outcome, caseID, taskKind, sourceSlot, generation, ok := TakeRunDecisionWords(
		task.g, operations.ticket.epoch, operations.ticket.generation,
	)
	if !ok || outcome != uint32(ParkOutcomeCompleted) || caseID != operations.cases[0] ||
		taskKind != uint32(TaskCancelNone) || sourceSlot != operations.ids[0].SourceSlot ||
		generation != operations.ids[0].Generation {
		t.Fatalf("scalar decision = (%d,%d,%d,%d,%d,%t)", outcome, caseID, taskKind, sourceSlot, generation, ok)
	}
	if outcome, caseID, taskKind, sourceSlot, generation, ok = TakeRunDecisionWords(
		task.g, operations.ticket.epoch, operations.ticket.generation,
	); ok || outcome != 0 || caseID != 0 || taskKind != 0 || sourceSlot != 0 || generation != 0 {
		t.Fatalf("duplicate scalar take = (%d,%d,%d,%d,%d,%t)", outcome, caseID, taskKind, sourceSlot, generation, ok)
	}

	winnerLease := OperationResultLease{id: operations.ids[0], ticket: operations.ticket}
	finishSchedulerParkV2Operations(t, operations, winnerLease)
	finishWaitTestTask(t, p, task, action)
}

func TestTakeRunDecisionWordsRejectsNonzeroEpochWithZeroGeneration(t *testing.T) {
	if outcome, caseID, taskKind, sourceSlot, generation, ok := TakeRunDecisionWords(new(G), 1, 0); ok ||
		outcome != 0 || caseID != 0 || taskKind != 0 || sourceSlot != 0 || generation != 0 {
		t.Fatalf("invalid scalar ticket = (%d,%d,%d,%d,%d,%t)", outcome, caseID, taskKind, sourceSlot, generation, ok)
	}
}
