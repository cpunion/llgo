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

func TestCommandShutdownDrainConsumesCompletedChannelBeforeExecutorClose(t *testing.T) {
	fixture := newChannelClaimCoreFixture(t, "channel-command-drain", []uint32{51}, true, 0)
	var transaction ChannelExternalCommit
	if result := BeginChannelExternalCommit(
		&transaction,
		fixture.source,
		fixture.ids[0],
		fixture.claim,
	); result != ChannelExternalCommitBeginPrepared || !transaction.BeginEffect() || !transaction.Commit() {
		t.Fatalf("commit command-drain channel endpoint = result:%d transaction:%+v", result, transaction)
	}
	requestChannelClaimCoreFixture(t, fixture)
	if progress := pollChannelClaimCoreComplete(t, fixture); progress.Promoted != 1 ||
		fixture.p.readyHead != fixture.task.g || channelOperationSourceEmpty(fixture.source, fixture.p) {
		t.Fatalf("publish command-drain completion = promoted:%d ready:%p sourceEmpty:%t",
			progress.Promoted, fixture.p.readyHead, channelOperationSourceEmpty(fixture.source, fixture.p))
	}
	main := &G{magic: gMagic, state: GDead}
	if needed, ok := RequestCommandShutdownDrain(fixture.p, main); !ok || !needed ||
		fixture.task.g.park.taskCancelKind != TaskCancelShutdown ||
		fixture.task.g.park.taskCancelPhase != taskCancelRequested {
		t.Fatalf("request command source drain = needed:%t ok:%t cancel:(%d,%d)",
			needed, ok, fixture.task.g.park.taskCancelKind, fixture.task.g.park.taskCancelPhase)
	}
	if needed, ok := RequestCommandShutdownDrain(fixture.p, main); !ok || !needed {
		t.Fatalf("repeat command source drain = needed:%t ok:%t", needed, ok)
	}
	if BeginExecutorClose(fixture.driver) {
		t.Fatal("executor closed before channel resume cleanup")
	}
	if g, ok := NextRunnable(fixture.p); !ok || g != fixture.task.g {
		t.Fatal("dequeue command-drain channel task")
	}
	action := beginWaitTestResume(t, fixture.p, fixture.task)
	outcome, caseID, lease, cancel, taken := TakeRunDecision(fixture.task.g, fixture.ticket)
	if !taken || outcome != ParkOutcomeCanceled || caseID != 0 || !lease.Valid() || cancel != TaskCancelShutdown {
		t.Fatalf("take command-drain decision = (%d,%d,%+v,%d,%t)", outcome, caseID, lease, cancel, taken)
	}
	if !FinishSingleChannelPark(
		fixture.task.g,
		fixture.source,
		fixture.ids[0],
		fixture.claim,
		lease,
		true,
	) {
		t.Fatal("finish command-drain channel cleanup")
	}
	fixture.task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	fixture.task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(fixture.task.g, fixture.task.handle, fixture.task.frame.header) {
		t.Fatal("prepare command-drain task completion")
	}
	action, ok := Resumed(fixture.p, fixture.task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume command-drain completion = (%+v,%t)", action, ok)
	}
	action, ok = Checked(fixture.p, fixture.task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check command-drain destroy = (%+v,%t)", action, ok)
	}
	releaseTestFrame(t, fixture.task.g, fixture.task.frame)
	receipt, ok := DestroyedBounded(fixture.p, fixture.task.g, action)
	if !ok || receipt.Kind != ActionCommitDestroy || receipt.Handle != nil {
		t.Fatalf("publish command-drain destroy receipt = (%+v,%t)", receipt, ok)
	}
	closeAction, ok := CommitDestroyedReceiptCompatibility(fixture.p, fixture.task.g, receipt)
	if !ok || closeAction.Kind != ActionTerminalExecutorClose || closeAction.Handle != nil {
		t.Fatalf("begin command-drain terminal close = (%+v,%t)", closeAction, ok)
	}
	closedG, complete, ok := ConfirmTerminalExecutorClose(fixture.driver)
	if !ok || closedG != fixture.task.g || complete.Kind != ActionComplete || complete.Handle != nil {
		t.Fatalf("confirm command-drain terminal close = (%p,%+v,%t)", closedG, complete, ok)
	}
	if !AcknowledgeTaskCancellation(fixture.task.g, TaskCancelShutdown) {
		t.Fatal("acknowledge command-drain cancellation")
	}
	if !fixture.source.CanRelease() || !fixture.waits.CanRelease() || !fixture.registry.CanRelease() {
		t.Fatal("command-drain channel cleanup retained stable source state")
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
