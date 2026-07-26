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
	"unsafe"
)

func prepareManualResumePacket(
	t *testing.T,
	p *P,
	driver *ExecutorDriver,
	manual *ManualOperationSource,
	task *yieldingTestG,
) (Action, ParkTicket, OperationID, *WaitSetRecord, *ResumePacket) {
	t.Helper()
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue packet task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue packet task")
	}
	action := beginWaitTestResume(t, p, task)
	wait, packet := new(WaitSetRecord), new(ResumePacket)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	ticket, operation, ok := PrepareSingleManualPark(
		task.g,
		task.handle,
		task.frame.header,
		manual,
		wait,
		17,
		101,
	)
	if !ok || !BindSingleWaitSetResumePacket(wait, packet, operation) {
		t.Fatal("prepare packet-backed manual park")
	}
	if parked, resumed := Resumed(p, task.g, action); !resumed || parked.Kind != ActionPark {
		t.Fatalf("commit packet-backed manual park = (%+v, %t)", parked, resumed)
	}
	if driver.p != p {
		t.Fatal("packet fixture changed driver owner")
	}
	return action, ticket, operation, wait, packet
}

func TestResumePacketLayoutIsPointerFreeAndCrossTargetStable(t *testing.T) {
	if unsafe.Sizeof(ResumePacket{}) != 52 || unsafe.Alignof(ResumePacket{}) != 4 ||
		unsafe.Offsetof(ResumePacket{}.source) != 8 ||
		unsafe.Offsetof(ResumePacket{}.scalar) != 16 ||
		unsafe.Offsetof(ResumePacket{}.caseID) != 44 {
		t.Fatalf("resume packet layout = size:%d align:%d source:%d scalar:%d case:%d",
			unsafe.Sizeof(ResumePacket{}), unsafe.Alignof(ResumePacket{}),
			unsafe.Offsetof(ResumePacket{}.source), unsafe.Offsetof(ResumePacket{}.scalar),
			unsafe.Offsetof(ResumePacket{}.caseID))
	}
}

func TestZeroSourceResumePacketMaterializesDefault(t *testing.T) {
	p, targetP := new(P), new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
	task := newYieldingTestG(t, "zero-source-resume-packet")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue zero-source packet task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue zero-source packet task")
	}
	action := beginWaitTestResume(t, p, task)
	ticket, begun := BeginParkSetWithDefault(&task.g.park, 0, 113, 31)
	wait, packet := new(WaitSetRecord), new(ResumePacket)
	if !begun || !PrepareWaitSetRecord(wait, task.g, ticket) || !SealParkSet(&task.g.park, ticket) {
		t.Fatal("prepare zero-source logical park")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(task.g, task.handle, task.frame.header, ticket, wait) ||
		!BindSingleWaitSetResumePacket(wait, packet, OperationID{}) {
		t.Fatal("bind zero-source resume packet")
	}
	if parked, resumed := Resumed(p, task.g, action); !resumed || parked.Kind != ActionPark {
		t.Fatalf("commit zero-source packet park = (%+v, %t)", parked, resumed)
	}
	if drained, promoted, ok := PollExecutor(driver); !ok || drained != 0 || promoted != 1 ||
		!validMaterializedResumePacket(packet) || packet.source != (OperationID{}) ||
		packet.outcome != ParkOutcomeDefault || packet.caseID != 31 || packet.result != ResumeResultNone {
		t.Fatalf("materialize zero-source packet = drained:%d promoted:%d ok:%t packet:%+v",
			drained, promoted, ok, *packet)
	}
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, targetP) {
		t.Fatal("bind zero-source packet transfer mailbox")
	}
	transfer, published := PublishPNeutralRunnable(&mailbox, p, task.g)
	if !published || !ImportPNeutralRunnable(&mailbox, targetP, transfer) {
		t.Fatal("transfer zero-source packet task")
	}
	if g, ok := NextRunnable(targetP); !ok || g != task.g {
		t.Fatal("dequeue zero-source packet resume")
	}
	action = beginWaitTestResume(t, targetP, task)
	outcome, caseID, cancel, result, poll, taken := TakeResumePacket(task.g, ticket, packet, nil)
	if !taken || outcome != ParkOutcomeDefault || caseID != 31 || cancel != TaskCancelNone ||
		result != ResumeResultNone || poll != PollOperationResultInvalid {
		t.Fatalf("take zero-source packet = outcome:%d case:%d cancel:%d result:%d poll:%d taken:%t",
			outcome, caseID, cancel, result, poll, taken)
	}
	finishWaitTestTask(t, targetP, task, action)
	closeTestExecutorDriver(t, driver)
	runtime.KeepAlive(task.frame.memory)
}

func TestManualResumePacketRetiresOldRouteBeforePTransfer(t *testing.T) {
	sourceP, targetP := new(P), new(P)
	driver, registry, manual, handle := bindTestExecutorDriverWithManual(t, sourceP)
	task := newYieldingTestG(t, "manual-resume-packet-transfer")
	_, ticket, operation, wait, packet := prepareManualResumePacket(t, sourceP, driver, manual, task)
	if posted := manual.Post(operation); posted != ManualOperationPosted ||
		registry.Request(handle) != ExecutorRequestPublished {
		t.Fatal("publish packet-backed manual result")
	}
	if drained, promoted, ok := PollExecutor(driver); !ok || drained != 1 || promoted != 1 {
		t.Fatalf("materialize packet-backed result = drained:%d promoted:%d ok:%t", drained, promoted, ok)
	}
	if *wait != (WaitSetRecord{}) || !validMaterializedResumePacket(packet) ||
		packet.source != (OperationID{}) || packet.outcome != ParkOutcomeCompleted ||
		packet.caseID != 17 || packet.result != ResumeResultNone ||
		task.g.park.phase != parkMaterialized || sourceP.readyHead != task.g ||
		!manualOperationSourceEmpty(manual, sourceP) {
		t.Fatalf("materialized packet retained old route: wait=%+v packet=%+v phase=%d ready=%p release=%t",
			*wait, *packet, task.g.park.phase, sourceP.readyHead, manualOperationSourceEmpty(manual, sourceP))
	}

	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, targetP) {
		t.Fatal("bind packet transfer mailbox")
	}
	transfer, published := PublishPNeutralRunnable(&mailbox, sourceP, task.g)
	if !published || !transfer.Valid() || !ImportPNeutralRunnable(&mailbox, targetP, transfer) ||
		sourceP.readyHead != nil || sourceP.readyTail != nil ||
		targetP.readyHead != task.g || targetP.readyTail != task.g {
		t.Fatalf("transfer materialized packet = id:%+v published:%t source=(%p,%p) target=(%p,%p)",
			transfer, published, sourceP.readyHead, sourceP.readyTail, targetP.readyHead, targetP.readyTail)
	}
	if g, ok := NextRunnable(targetP); !ok || g != task.g {
		t.Fatal("dequeue transferred packet task")
	}
	action := beginWaitTestResume(t, targetP, task)
	outcome, caseID, cancel, result, poll, taken := TakeResumePacket(task.g, ticket, packet, nil)
	current, executor, route, currentOK := CurrentExecutorDriver(task.g)
	if !taken || outcome != ParkOutcomeCompleted || caseID != 17 || cancel != TaskCancelNone ||
		result != ResumeResultNone || poll != PollOperationResultInvalid ||
		*packet != (ResumePacket{}) || task.g.park.phase != parkDelivered ||
		current != nil || executor != (ExecutorHandle{}) || route != 0 || currentOK {
		t.Fatalf("take transferred packet = outcome:%d case:%d cancel:%d result:%d poll:%d taken:%t packet:%+v phase:%d",
			outcome, caseID, cancel, result, poll, taken, *packet, task.g.park.phase)
	}
	finishWaitTestTask(t, targetP, task, action)
	closeTestExecutorDriver(t, driver)
	runtime.KeepAlive(task.frame.memory)
}

func TestTaskCancelAfterResumePacketMigrationNeverRevisitsOldSource(t *testing.T) {
	sourceP, targetP := new(P), new(P)
	driver, registry, manual, handle := bindTestExecutorDriverWithManual(t, sourceP)
	task := newYieldingTestG(t, "resume-packet-late-cancel")
	_, ticket, operation, _, packet := prepareManualResumePacket(t, sourceP, driver, manual, task)
	if manual.Post(operation) != ManualOperationPosted ||
		registry.Request(handle) != ExecutorRequestPublished {
		t.Fatal("publish late-cancel packet result")
	}
	if _, promoted, ok := PollExecutor(driver); !ok || promoted != 1 ||
		!manualOperationSourceEmpty(manual, sourceP) {
		t.Fatalf("materialize late-cancel packet = promoted:%d ok:%t empty:%t",
			promoted, ok, manualOperationSourceEmpty(manual, sourceP))
	}
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, targetP) {
		t.Fatal("bind late-cancel transfer mailbox")
	}
	transfer, ok := PublishPNeutralRunnable(&mailbox, sourceP, task.g)
	if !ok || !ImportPNeutralRunnable(&mailbox, targetP, transfer) ||
		!RequestTaskCancellation(targetP, task.g, TaskCancelAbort) {
		t.Fatal("transfer and cancel materialized packet")
	}
	if g, ready := NextRunnable(targetP); !ready || g != task.g {
		t.Fatal("dequeue late-cancel packet task")
	}
	action := beginWaitTestResume(t, targetP, task)
	outcome, caseID, cancel, result, poll, taken := TakeResumePacket(task.g, ticket, packet, nil)
	if !taken || outcome != ParkOutcomeCanceled || caseID != 0 || cancel != TaskCancelAbort ||
		result != ResumeResultNone || poll != PollOperationResultInvalid ||
		*packet != (ResumePacket{}) || task.g.park.taskCancelPhase != taskCancelCleanup {
		t.Fatalf("take late-cancel packet = outcome:%d case:%d cancel:%d result:%d poll:%d taken:%t phase:%d",
			outcome, caseID, cancel, result, poll, taken, task.g.park.taskCancelPhase)
	}
	finishWaitTestTask(t, targetP, task, action)
	if !AcknowledgeTaskCancellation(task.g, TaskCancelAbort) || !TerminalG(targetP, task.g) {
		t.Fatal("late-cancel packet cleanup did not terminate")
	}
	closeTestExecutorDriver(t, driver)
	runtime.KeepAlive(task.frame.memory)
}

func TestWorkerResumePacketCopiesScalarBeforePTransfer(t *testing.T) {
	sourceP, targetP := new(P), new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	workers := new(WorkerOperationSource)
	executor := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, sourceP, registry, executor, ExecutorSourceCatalog{Worker: workers}) {
		t.Fatal("bind packet worker catalog")
	}
	task := newYieldingTestG(t, "worker-resume-packet-transfer")
	if !Enqueue(sourceP, task.g) {
		t.Fatal("enqueue packet worker task")
	}
	if g, ok := NextRunnable(sourceP); !ok || g != task.g {
		t.Fatal("dequeue packet worker task")
	}
	action := beginWaitTestResume(t, sourceP, task)
	wait, packet := new(WaitSetRecord), new(ResumePacket)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	ticket, operation, ok := PrepareCurrentExecutorWorkerPark(
		driver,
		task.g,
		task.handle,
		task.frame.header,
		wait,
		23,
		107,
	)
	if !ok || !BindSingleWaitSetResumePacket(wait, packet, operation) ||
		!CommitCurrentExecutorWorkerSubmission(driver, task.g, operation) {
		t.Fatal("prepare packet worker park")
	}
	if parked, resumed := Resumed(sourceP, task.g, action); !resumed || parked.Kind != ActionPark {
		t.Fatalf("commit packet worker park = (%+v, %t)", parked, resumed)
	}
	payload := workerPayloadForTest(t, 29, 2901, 2902, 125)
	if workers.Post(operation, payload) != WorkerOperationPosted ||
		registry.Request(executor) != ExecutorRequestPublished {
		t.Fatal("publish packet worker result")
	}
	if drained, promoted, polled := PollExecutor(driver); !polled || drained != 1 || promoted != 1 ||
		!workerOperationSourceEmpty(workers, sourceP) ||
		packet.result != ResumeResultScalar || packet.scalar != payload {
		t.Fatalf("materialize packet worker = drained:%d promoted:%d polled:%t empty:%t packet:%+v",
			drained, promoted, polled, workerOperationSourceEmpty(workers, sourceP), *packet)
	}
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, targetP) {
		t.Fatal("bind packet worker transfer mailbox")
	}
	transfer, published := PublishPNeutralRunnable(&mailbox, sourceP, task.g)
	if !published || !ImportPNeutralRunnable(&mailbox, targetP, transfer) {
		t.Fatal("transfer packet worker task")
	}
	if g, ready := NextRunnable(targetP); !ready || g != task.g {
		t.Fatal("dequeue transferred packet worker")
	}
	action = beginWaitTestResume(t, targetP, task)
	var got ScalarResultPayloadV1
	outcome, caseID, cancel, result, poll, taken := TakeResumePacket(task.g, ticket, packet, &got)
	if !taken || outcome != ParkOutcomeCompleted || caseID != 23 || cancel != TaskCancelNone ||
		result != ResumeResultScalar || poll != PollOperationResultInvalid || got != payload {
		t.Fatalf("take packet worker = outcome:%d case:%d cancel:%d result:%d poll:%d taken:%t payload:%+v",
			outcome, caseID, cancel, result, poll, taken, got)
	}
	finishWaitTestTask(t, targetP, task, action)
	closeTestExecutorDriver(t, driver)
	runtime.KeepAlive(task.frame.memory)
}
