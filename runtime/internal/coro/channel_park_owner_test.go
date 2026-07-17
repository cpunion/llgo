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

func TestSingleChannelParkOwnerTransactionAndFinish(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	source := new(ChannelOperationSource)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{
		Waits: waits, Channel: source,
	}) {
		t.Fatal("bind single-channel owner executor")
	}
	task := newYieldingTestG(t, "single-channel-owner")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue single-channel owner task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue single-channel owner task")
	}
	action := beginWaitTestResume(t, p, task)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	var wait WaitSetRecord
	var claim SelectClaim
	ticket, id, ok := PrepareSingleChannelPark(
		task.g,
		task.handle,
		task.frame.header,
		source,
		&wait,
		&claim,
		41,
		73,
	)
	if !ok || !validParkTicket(ticket) || !id.Valid() || selectClaimLoad(&claim) != selectClaimOpen {
		t.Fatalf("prepare single-channel park = (%+v, %+v, %t), claim=%d", ticket, id, ok, selectClaimLoad(&claim))
	}
	var transaction ChannelExternalCommit
	if result := BeginChannelExternalCommit(&transaction, source, id, &claim); result != ChannelExternalCommitBeginPrepared ||
		!transaction.BeginEffect() || !transaction.Commit() {
		t.Fatalf("commit single-channel endpoint = result:%d transaction:%+v", result, transaction)
	}
	if parked, resumed := Resumed(p, task.g, action); !resumed || parked.Kind != ActionPark {
		t.Fatalf("commit single-channel physical park = (%+v, %t)", parked, resumed)
	}
	requested := registry.Request(handle)
	if requested != ExecutorRequestPublished && requested != ExecutorRequestCoalesced {
		t.Fatalf("request single-channel executor = %d", requested)
	}
	for step := 0; ; step++ {
		progress, polled := PollExecutorSlice(driver, 1)
		if !polled {
			t.Fatalf("poll single-channel owner at step %d", step)
		}
		if progress.Complete {
			break
		}
		if step == 10000 {
			t.Fatal("single-channel owner did not become runnable")
		}
	}
	if g, runnable := NextRunnable(p); !runnable || g != task.g {
		t.Fatal("dequeue completed single-channel owner task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, cancel, taken := TakeRunDecision(task.g, ticket)
	if !taken || outcome != ParkOutcomeCompleted || caseID != 41 || cancel != TaskCancelNone || !lease.Valid() {
		t.Fatalf("take single-channel owner decision = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, cancel, taken)
	}
	if !FinishSingleChannelPark(task.g, source, id, &claim, lease, false) ||
		selectClaimLoad(&claim) != selectClaimOpen {
		t.Fatal("finish single-channel owner transaction")
	}
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	finishReadyDriverTasks(t, p, map[*G]*yieldingTestG{task.g: task})
	if !source.CanRelease() || !waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("single-channel owner cleanup retained stable state")
	}
}

func TestEmptyChannelParkOwnerSupportsTaskCancellation(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "empty-channel-owner")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue empty-channel owner task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue empty-channel owner task")
	}
	action := beginWaitTestResume(t, p, task)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	var wait WaitSetRecord
	ticket, ok := PrepareEmptyChannelPark(task.g, task.handle, task.frame.header, &wait, 79)
	if !ok || !validParkTicket(ticket) {
		t.Fatalf("prepare empty-channel park = (%+v, %t)", ticket, ok)
	}
	if parked, resumed := Resumed(p, task.g, action); !resumed || parked.Kind != ActionPark {
		t.Fatalf("commit empty-channel physical park = (%+v, %t)", parked, resumed)
	}
	if !RequestTaskCancellation(p, task.g, TaskCancelAbort) {
		t.Fatal("request empty-channel task cancellation")
	}
	for step := 0; ; step++ {
		ready, polled := PollReady(p)
		if !polled {
			t.Fatalf("poll empty-channel cancellation at step %d", step)
		}
		if ready != 0 {
			break
		}
		if step == 10000 {
			t.Fatal("empty-channel cancellation did not become runnable")
		}
	}
	if g, runnable := NextRunnable(p); !runnable || g != task.g {
		t.Fatal("dequeue canceled empty-channel task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, cancel, taken := TakeRunDecision(task.g, ticket)
	if !taken || outcome != ParkOutcomeCanceled || caseID != 0 || lease.Valid() || cancel != TaskCancelAbort {
		t.Fatalf("take empty-channel cancellation = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, cancel, taken)
	}
	finishWaitTestTask(t, p, task, action)
}
