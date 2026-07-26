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
				Kind:    ResumeCleanupChannelSelect,
				Context: unsafe.Pointer(&token),
				Entries: unsafe.Pointer(&fixture.ids[0]),
				Source:  fixture.source,
				Claim:   fixture.claim,
				Count:   uint32(len(fixture.ids)),
				Stride:  unsafe.Sizeof(OperationID{}),
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
