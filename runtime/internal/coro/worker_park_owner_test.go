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
	waits := new(WaitRegistrationTable)
	workers := new(WorkerOperationSource)
	sources := new(ExecutorSourceSet)
	if !bindExecutorSourceSet(sources, p, ExecutorSourceCatalog{Waits: waits, Worker: workers}) {
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
	if scan, ok := sources.publishPass(p, 0, false); !ok || scan.worker != 1 {
		t.Fatalf("publish worker owner result = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 1 {
		t.Fatalf("resolve worker owner result = (%d, %d, %t)", promoted, visits, ok)
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
	finishWaitTestTask(t, p, task, action)
	if !unbindExecutorSourceSet(sources, p) || !workers.CanRelease() || !waits.CanRelease() {
		t.Fatal("release worker owner catalog")
	}
}
