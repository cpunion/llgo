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

func TestWorkerParkOwnerPrepareCompleteAndFinish(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	workers := new(WorkerOperationSource)
	executor := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, executor, ExecutorSourceCatalog{Waits: waits, Worker: workers}) {
		t.Fatal("bind worker owner catalog")
	}
	task := newYieldingTestG(t, "worker-owner")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue worker owner task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue worker owner task")
	}
	action := beginWaitTestResume(t, p, task)
	var wait WaitSetRecord
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	ticket, id, ok := PrepareSingleWorkerPark(
		task.g, task.handle, task.frame.header, workers, &wait, 31, 79,
	)
	if !ok || !CommitWorkerSubmission(task.g, workers, id) {
		t.Fatal("prepare worker owner park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit worker owner park = (%+v, %t)", action, ok)
	}
	payload := workerPayloadForTest(t, 11, 111, 222, 0)
	if workers.Post(id, payload) != WorkerOperationPosted {
		t.Fatal("post worker owner result")
	}
	var complete ExecutorPollProgress
	for entries := 0; entries < 1000; entries++ {
		progress, ok := PollExecutorSlice(driver, 1)
		if !ok || progress.Used != 1 {
			t.Fatalf("bounded worker owner poll %d = (%+v, %t)", entries, progress, ok)
		}
		if progress.Complete {
			complete = progress
			break
		}
	}
	if !complete.Complete || complete.Worker != 1 || complete.WorkerLost != 0 ||
		complete.Completed != 1 || complete.Promoted != 1 || complete.ApplyVisits != 1 {
		t.Fatalf("complete bounded worker owner poll = %+v", complete)
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue completed worker owner")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskCancel, ok := TakeRunDecision(task.g, ticket)
	if !ok || outcome != ParkOutcomeCompleted || caseID != 31 || !lease.Valid() || taskCancel != TaskCancelNone {
		t.Fatalf("take worker owner decision = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskCancel, ok)
	}
	var got ScalarResultPayloadV1
	if !FinishSingleWorkerPark(task.g, workers, id, lease, false, &got) || got != payload {
		t.Fatalf("finish worker owner result = %+v, want %+v", got, payload)
	}
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare worker owner completion")
	}
	action, ok = Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume worker owner completion = (%+v, %t)", action, ok)
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check worker owner destroy = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	closeAction, ok := Destroyed(p, task.g, action)
	if !ok || closeAction.Kind != ActionTerminalExecutorClose || closeAction.Handle != nil {
		t.Fatalf("begin worker owner terminal close = (%+v, %t)", closeAction, ok)
	}
	closed, terminal, ok := ConfirmTerminalExecutorClose(driver)
	if !ok || closed != task.g || terminal.Kind != ActionComplete || terminal.Handle != nil {
		t.Fatalf("confirm worker owner terminal close = (%p, %+v, %t)", closed, terminal, ok)
	}
	if !workers.CanRelease() || !waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("release worker owner catalog")
	}
}
