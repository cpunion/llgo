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
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

func TestResumeCleanupPlanHasNoDynamicGoPayload(t *testing.T) {
	var check func(string, reflect.Type)
	check = func(path string, typ reflect.Type) {
		switch typ.Kind() {
		case reflect.Array:
			check(path, typ.Elem())
		case reflect.Struct:
			for index := 0; index < typ.NumField(); index++ {
				field := typ.Field(index)
				check(path+"."+field.Name, field.Type)
			}
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
			reflect.Slice, reflect.String:
			t.Fatalf("%s has dynamic Go payload type %s", path, typ)
		}
	}
	for _, typ := range []reflect.Type{
		reflect.TypeOf(ResumeCleanupPlan{}),
		reflect.TypeOf(ResumeCleanupBinding{}),
		reflect.TypeOf(ResumeCleanupStep{}),
	} {
		check(typ.Name(), typ)
	}
}

func TestResumeCleanupPlanHasBoundedTargetLayout(t *testing.T) {
	wantSize, wantAlign := uintptr(72), uintptr(4)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		wantSize, wantAlign = 96, 8
	}
	if got := unsafe.Sizeof(ResumeCleanupPlan{}); got != wantSize {
		t.Fatalf("ResumeCleanupPlan size = %d, want %d", got, wantSize)
	}
	if got := unsafe.Alignof(ResumeCleanupPlan{}); got != wantAlign {
		t.Fatalf("ResumeCleanupPlan alignment = %d, want %d", got, wantAlign)
	}
}

func TestTypedResumeCleanupIsBoundedAndPNeutral(t *testing.T) {
	var (
		packet ResumePacket
		plan   ResumeCleanupPlan
		token  byte
	)
	fixture := newChannelClaimCoreFixtureBeforeResume(
		t,
		"typed-resume-cleanup",
		[]uint32{1, 2},
		true,
		0,
		func(fixture *channelClaimCoreFixture) {
			binding := ResumeCleanupBinding{
				Kind:         ResumeCleanupChannelSelect,
				Context:      unsafe.Pointer(&token),
				Entries:      unsafe.Pointer(&fixture.ids[0]),
				Claim:        fixture.claim,
				Count:        uint32(len(fixture.ids)),
				RuntimeCount: uint32(len(fixture.ids)),
				Stride:       unsafe.Sizeof(OperationID{}),
			}
			malformed := binding
			malformed.Count = 1
			if BindWaitSetResumeCleanup(&fixture.wait, &packet, &plan, malformed) ||
				packet != (ResumePacket{}) || plan != (ResumeCleanupPlan{}) ||
				fixture.wait.resume != nil || fixture.wait.resumeKind != resumeBindingNone {
				t.Fatal("cleanup binding accepted a truncated physical range")
			}
			if !BindWaitSetResumeCleanup(&fixture.wait, &packet, &plan, binding) {
				t.Fatal("bind typed resume cleanup")
			}
		},
	)
	externallyCommitChannelCandidate(t, fixture, 1)
	requestChannelClaimCoreFixture(t, fixture)

	materialized := uint32(0)
	for reduction := 0; reduction < 10000; reduction++ {
		step, ok := NextExecutorRunStep(fixture.driver)
		if !ok {
			t.Fatalf("select cleanup reduction %d", reduction)
		}
		switch step.Kind {
		case ExecutorRunStepSource:
			if step.Poll.Complete {
				if materialized != uint32(len(fixture.ids)) {
					t.Fatalf("typed cleanup runtime reductions = %d, want %d", materialized, len(fixture.ids))
				}
				goto complete
			}
		case ExecutorRunStepMaterialize:
			if step.Cleanup.Kind != ResumeCleanupChannelSelect ||
				step.Cleanup.Context != unsafe.Pointer(&token) ||
				step.Cleanup.Index != materialized ||
				step.Cleanup.WinnerCase != 2 ||
				step.Cleanup.Outcome != ParkOutcomeCompleted {
				t.Fatalf("typed cleanup step %d = %+v", materialized, step.Cleanup)
			}
			small := ResumeSmallInvalid
			if step.Cleanup.Index == 1 {
				small = 3
			}
			if !CommitResumeCleanupStep(step.Cleanup, small) {
				t.Fatalf("commit typed cleanup step %d", materialized)
			}
			materialized++
		default:
			t.Fatalf("unexpected typed cleanup runner step %d", step.Kind)
		}
	}
	t.Fatal("typed resume cleanup did not complete")

complete:
	if !EnterExecutorRunCompatibility(fixture.driver) ||
		packet.state != resumePacketMaterialized ||
		packet.source != (OperationID{}) ||
		packet.outcome != ParkOutcomeCompleted ||
		packet.caseID != 2 ||
		packet.result != ResumeResultChannel ||
		packet.small != 3 ||
		plan != (ResumeCleanupPlan{}) ||
		fixture.wait != (WaitSetRecord{}) ||
		fixture.ids[0] != (OperationID{}) ||
		fixture.ids[1] != (OperationID{}) ||
		!channelOperationSourceEmpty(fixture.source, fixture.p) {
		t.Fatalf("typed cleanup retained owner state: packet=%+v plan=%+v wait=%+v ids=%+v empty=%t",
			packet, plan, fixture.wait, fixture.ids,
			channelOperationSourceEmpty(fixture.source, fixture.p))
	}

	target := new(P)
	var mailbox RunnableTransferMailbox
	if !BindRunnableTransferMailbox(&mailbox, target) {
		t.Fatal("bind typed cleanup transfer mailbox")
	}
	transfer, published := PublishPNeutralRunnable(&mailbox, fixture.p, fixture.task.g)
	if !published || !transfer.Valid() || !ImportPNeutralRunnable(&mailbox, target, transfer) {
		t.Fatalf("transfer typed cleanup packet = (%+v, %t)", transfer, published)
	}
	if g, ok := NextRunnable(target); !ok || g != fixture.task.g {
		t.Fatal("dequeue transferred typed cleanup task")
	}
	action := beginWaitTestResume(t, target, fixture.task)
	outcome, caseID, cancel, result, small, taken := TakeResumePacket(
		fixture.task.g,
		fixture.ticket,
		&packet,
		nil,
	)
	if !taken || outcome != ParkOutcomeCompleted || caseID != 2 ||
		cancel != TaskCancelNone || result != ResumeResultChannel || small != 3 {
		t.Fatalf("take typed cleanup packet = outcome:%d case:%d cancel:%d result:%d small:%d taken:%t",
			outcome, caseID, cancel, result, small, taken)
	}
	finishWaitTestTask(t, target, fixture.task, action)
	closeTestExecutorDriver(t, fixture.driver)
	if !fixture.source.CanRelease() || !fixture.registry.CanRelease() {
		t.Fatal("typed cleanup retained source/registry")
	}
	runtime.KeepAlive(fixture.task.frame.memory)
}

func TestTypedResumeCleanupWorkerTimerIsCompositeAndPNeutral(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	workers := new(WorkerOperationSource)
	timers := new(TimerRegistrationTable)
	executor := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, executor, ExecutorSourceCatalog{
		Timers: timers,
		Worker: workers,
	}) {
		t.Fatal("bind composite worker/timer catalog")
	}
	task := newYieldingTestG(t, "typed-worker-timer-cleanup")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue composite worker/timer task")
	}
	if g, ok := NextRunnableAt(p, 0); !ok || g != task.g {
		t.Fatal("dequeue composite worker/timer task")
	}
	action := beginWaitTestResume(t, p, task)
	var (
		wait       WaitSetRecord
		packet     ResumePacket
		plan       ResumeCleanupPlan
		operations [2]OperationID
		token      byte
	)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	ticket, worker, _, timer, retainedExecutor, ok :=
		PrepareCurrentExecutorWorkerTimerPark(
			driver,
			task.g,
			task.handle,
			task.frame.header,
			&wait,
			1,
			2,
			97,
			20,
		)
	operations = [2]OperationID{worker, timer}
	if !ok || retainedExecutor != executor ||
		!BindWaitSetResumeCleanup(
			&wait,
			&packet,
			&plan,
			ResumeCleanupBinding{
				Kind:         ResumeCleanupHostOperationDeadline,
				Context:      unsafe.Pointer(&token),
				Entries:      unsafe.Pointer(&operations[0]),
				Count:        uint32(len(operations)),
				RuntimeCount: 1,
				Stride:       unsafe.Sizeof(OperationID{}),
			},
		) ||
		!CommitCurrentExecutorWorkerSubmission(driver, task.g, worker) {
		t.Fatal("prepare composite worker/timer park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit composite worker/timer park = (%+v, %t)", action, ok)
	}

	postedWorker := false
	runtimeSteps := 0
	for reduction := 0; reduction < 10000; reduction++ {
		step, stepOK := NextExecutorRunStepAt(driver, 20)
		if !stepOK {
			t.Fatalf("composite worker/timer reduction %d: poll=%+v plan=%+v park=%+v",
				reduction, driver.poll, plan, task.g.park)
		}
		switch step.Kind {
		case ExecutorRunStepSource:
			if requested, exact := ExecutorWorkerPhysicalCancelRequested(driver, worker); exact && requested &&
				!postedWorker {
				payload := workerPayloadForTest(t, 31, 3100, 0, 125)
				if workers.Post(worker, payload) != WorkerOperationPosted {
					t.Fatal("post composite worker cancellation acknowledgement")
				}
				postedWorker = true
			}
			if step.Poll.Complete && packet.state == resumePacketMaterialized {
				goto complete
			}
		case ExecutorRunStepMaterialize:
			if step.Cleanup.Kind != ResumeCleanupHostOperationDeadline ||
				step.Cleanup.Context != unsafe.Pointer(&token) ||
				step.Cleanup.Index != 0 ||
				step.Cleanup.WinnerCase != 2 ||
				step.Cleanup.Outcome != ParkOutcomeCompleted ||
				!CommitResumeCleanupStep(step.Cleanup, ResumeSmallInvalid) {
				t.Fatalf("composite worker/timer materialization = %+v", step.Cleanup)
			}
			runtimeSteps++
		default:
			t.Fatalf("unexpected composite worker/timer step %d", step.Kind)
		}
	}
	t.Fatal("composite worker/timer cleanup did not complete")

complete:
	if !postedWorker || runtimeSteps != 1 ||
		operations != ([2]OperationID{}) ||
		plan != (ResumeCleanupPlan{}) ||
		packet.state != resumePacketMaterialized ||
		packet.outcome != ParkOutcomeCompleted ||
		packet.caseID != 2 ||
		packet.result != ResumeResultNone ||
		packet.small != ResumeSmallInvalid {
		t.Fatalf("composite worker/timer materialization retained state: posted=%t runtime=%d operations=%+v plan=%+v packet=%+v",
			postedWorker, runtimeSteps, operations, plan, packet)
	}
	if !EnterExecutorRunCompatibility(driver) {
		t.Fatal("leave composite worker/timer unified runner")
	}
	if g, ok := NextRunnableAt(p, 20); !ok || g != task.g {
		t.Fatal("dequeue materialized composite worker/timer task")
	}
	action = beginWaitTestResume(t, p, task)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	outcome, caseID, cancel, result, small, taken := TakeResumePacket(
		task.g,
		ticket,
		&packet,
		nil,
	)
	if !taken || outcome != ParkOutcomeCompleted || caseID != 2 ||
		cancel != TaskCancelNone || result != ResumeResultNone ||
		small != ResumeSmallInvalid || packet != (ResumePacket{}) ||
		!workerOperationSourceEmpty(workers, p) || !timerRegistrationTableEmpty(timers, p) {
		t.Fatalf("take composite worker/timer packet = (%d, %d, %d, %d, %d, %t), packet=%+v release=(%t,%t)",
			outcome, caseID, cancel, result, small, taken, packet,
			workerOperationSourceEmpty(workers, p), timerRegistrationTableEmpty(timers, p))
	}
}

func TestTypedResumeCleanupKeyedManualIsPNeutral(t *testing.T) {
	p := new(P)
	driver, registry, manual, executor := bindTestExecutorDriverWithManual(t, p)
	task := newYieldingTestG(t, "typed-keyed-manual-cleanup")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue keyed manual task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue keyed manual task")
	}
	action := beginWaitTestResume(t, p, task)
	var (
		wait   WaitSetRecord
		packet ResumePacket
		plan   ResumeCleanupPlan
		token  byte
	)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	ticket, operation, ok := PrepareSingleManualPark(
		task.g,
		task.handle,
		task.frame.header,
		manual,
		&wait,
		1,
		131,
	)
	if !ok ||
		!BindWaitSetResumeCleanup(
			&wait,
			&packet,
			&plan,
			ResumeCleanupBinding{
				Kind:         ResumeCleanupKeyedPark,
				Context:      unsafe.Pointer(&token),
				Entries:      unsafe.Pointer(&operation),
				Count:        1,
				RuntimeCount: 1,
				Stride:       unsafe.Sizeof(OperationID{}),
			},
		) {
		t.Fatal("prepare keyed manual cleanup")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark {
		t.Fatalf("commit keyed manual park = (%+v, %t)", action, ok)
	}
	if manual.Post(operation) != ManualOperationPosted ||
		registry.Request(executor) != ExecutorRequestPublished {
		t.Fatal("publish keyed manual completion")
	}

	runtimeSteps := 0
	for reduction := 0; reduction < 10000; reduction++ {
		step, stepOK := NextExecutorRunStep(driver)
		if !stepOK {
			t.Fatalf("keyed manual reduction %d: plan=%+v park=%+v",
				reduction, plan, task.g.park)
		}
		switch step.Kind {
		case ExecutorRunStepSource:
			if step.Poll.Complete && packet.state == resumePacketMaterialized {
				goto complete
			}
		case ExecutorRunStepMaterialize:
			if step.Cleanup.Kind != ResumeCleanupKeyedPark ||
				step.Cleanup.Context != unsafe.Pointer(&token) ||
				step.Cleanup.Index != 0 ||
				step.Cleanup.WinnerCase != 1 ||
				step.Cleanup.Outcome != ParkOutcomeCompleted ||
				!CommitResumeCleanupStep(step.Cleanup, ResumeSmallInvalid) {
				t.Fatalf("keyed manual materialization = %+v", step.Cleanup)
			}
			runtimeSteps++
		default:
			t.Fatalf("unexpected keyed manual step %d", step.Kind)
		}
	}
	t.Fatal("keyed manual cleanup did not complete")

complete:
	if runtimeSteps != 1 || operation != (OperationID{}) ||
		plan != (ResumeCleanupPlan{}) || packet.state != resumePacketMaterialized ||
		packet.outcome != ParkOutcomeCompleted || packet.caseID != 1 ||
		packet.result != ResumeResultNone || packet.small != ResumeSmallInvalid ||
		!manualOperationSourceEmpty(manual, p) {
		t.Fatalf("keyed manual materialization retained state: runtime=%d operation=%+v plan=%+v packet=%+v empty=%t",
			runtimeSteps, operation, plan, packet, manualOperationSourceEmpty(manual, p))
	}
	if !EnterExecutorRunCompatibility(driver) {
		t.Fatal("leave keyed manual unified runner")
	}
	if g, ready := NextRunnable(p); !ready || g != task.g {
		t.Fatal("dequeue keyed manual task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, cancel, result, small, taken := TakeResumePacket(
		task.g,
		ticket,
		&packet,
		nil,
	)
	if !taken || outcome != ParkOutcomeCompleted || caseID != 1 ||
		cancel != TaskCancelNone || result != ResumeResultNone ||
		small != ResumeSmallInvalid || packet != (ResumePacket{}) {
		t.Fatalf("take keyed manual packet = (%d, %d, %d, %d, %d, %t), packet=%+v",
			outcome, caseID, cancel, result, small, taken, packet)
	}
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare keyed manual task completion")
	}
	action, ok = Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume keyed manual completion = (%+v, %t)", action, ok)
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check keyed manual destroy = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	receipt, ok := DestroyedBounded(p, task.g, action)
	if !ok || receipt.Kind != ActionCommitDestroy {
		t.Fatalf("publish keyed manual destroy receipt = (%+v, %t)", receipt, ok)
	}
	completeAction, ok := CommitExecutorRunDomainDestroy(driver, task.g, receipt)
	if !ok || completeAction.Kind != ActionComplete {
		t.Fatalf("commit keyed manual domain destroy = (%+v, %t)", completeAction, ok)
	}
	closeTestExecutorDriver(t, driver)
	runtime.KeepAlive(task.frame.memory)
}
