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

func TestExecutorShutdownDrainEmptyDomainIsCloseReady(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
	if needed, ok := RequestExecutorShutdownDrain(driver); !ok || needed {
		t.Fatalf("empty executor shutdown drain = (%t,%t)", needed, ok)
	}
	if !ExecutorShutdownDrained(driver) {
		t.Fatal("empty executor domain was not close-ready")
	}
}

func TestExecutorShutdownDrainRequestsEveryReadyTask(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
	first := newYieldingTestG(t, "executor-shutdown-first")
	second := newYieldingTestG(t, "executor-shutdown-second")
	if !Enqueue(p, first.g) || !Enqueue(p, second.g) {
		t.Fatal("enqueue executor shutdown tasks")
	}
	if needed, ok := RequestExecutorShutdownDrain(driver); !ok || !needed {
		t.Fatalf("executor shutdown drain = (%t,%t)", needed, ok)
	}
	for _, task := range []*yieldingTestG{first, second} {
		if kind, present := TaskCancellationOf(p, task.g); !present || kind != TaskCancelShutdown {
			t.Fatalf("task %s cancellation = (%d,%t)", task.name, kind, present)
		}
	}
	if ExecutorShutdownDrained(driver) {
		t.Fatal("executor became close-ready before canceled tasks drained")
	}
	late := newYieldingTestG(t, "executor-shutdown-late-spawn")
	if !Enqueue(p, late.g) {
		t.Fatal("enqueue task created during shutdown cleanup")
	}
	if needed, ok := RequestExecutorShutdownDrain(driver); !ok || !needed {
		t.Fatalf("repeat executor shutdown drain = (%t,%t)", needed, ok)
	}
	if kind, present := TaskCancellationOf(p, late.g); !present || kind != TaskCancelShutdown {
		t.Fatalf("late task cancellation = (%d,%t)", kind, present)
	}
}

func TestExecutorShutdownDrainDirectCompletionAfterCompatibility(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
	task := newYieldingTestG(t, "shutdown-direct-result")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue direct channel task")
	}
	if next, ok := NextRunnable(p); !ok || next != task.g {
		t.Fatal("select direct channel task")
	}
	action := beginWaitTestResume(t, p, task)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	storage := new(DirectChannelParkStorageV1)
	if owner, route := PrepareCurrentDirectChannelPark(task.g, task.handle, task.frame.header, storage); owner != driver || route != driver.route {
		t.Fatal("prepare direct channel park")
	}
	if parked, ok := Resumed(p, task.g, action); !ok || parked.Kind != ActionPark {
		t.Fatalf("activate direct channel park = (%+v, %t)", parked, ok)
	}
	if !EnterExecutorRunCompatibility(driver) {
		t.Fatal("enter shutdown compatibility before producer publication")
	}
	// A remote producer wins after the owner's compatibility check. Its
	// completion stays parked/in the inbox until the owner materializes it.
	if BeginDirectChannelCompletion(&storage.Completion) != DirectChannelCompletionBeginAcquired {
		t.Fatal("claim direct channel result")
	}
	owner, route, result := FinishDirectChannelCompletionFromCompilerTask(nil, &storage.Completion, 3, 0)
	if owner != driver || route != driver.route || result != DirectChannelCompletionFinishNeedsTarget ||
		!PublishExecutorDirectChannelCompletion(driver, &storage.Completion) {
		t.Fatal("publish remote completion after shutdown compatibility")
	}
	if needed, ok := RequestExecutorShutdownDrain(driver); !ok || !needed {
		t.Fatalf("shutdown with racing direct completion = (%t,%t)", needed, ok)
	}
	if kind, ok := TaskCancellationOf(p, task.g); !ok || kind != TaskCancelShutdown {
		t.Fatalf("parked task cancellation = (%d,%t)", kind, ok)
	}
	completion, ok := takeExecutorDirectChannelCompletion(driver)
	if !ok || completion != &storage.Completion || !CommitDirectChannelCompletion(completion, 3) {
		t.Fatal("materialize the retained direct result")
	}
	if !validReadyQueue(p) || dequeueOSThreadRunnable(p) != task.g {
		t.Fatal("select canceled direct channel task")
	}
	beginWaitTestResumeWithoutGate(t, p, task)
	driver.run.issued = ActionCheckResume
	outcome, kind, small, ok := TakeIssuedDirectChannelResume(task.g, storage)
	if !ok || outcome != ParkOutcomeCanceled || kind != TaskCancelShutdown || small != ResumeSmallInvalid {
		t.Fatalf("shutdown resume gate = (%d,%d,%d,%t)", outcome, kind, small, ok)
	}
	if !executorDirectChannelInboxIdle(driver) {
		t.Fatal("shutdown result left a duplicate completion in the inbox")
	}
}

func TestExecutorShutdownDrainDoesNotRequeueAwaitingExternalWait(t *testing.T) {
	fixture := &pollParkOwnerFixture{
		p:        new(P),
		driver:   new(ExecutorDriver),
		registry: new(ExecutorRegistry),
		poll:     new(PollOperationSource),
		task:     newYieldingTestG(t, "shutdown-awaiting-poll"),
	}
	packet := new(ResumePacket)
	registered := registerTestExecutor(t, fixture.registry)
	if !BindExecutorSourceCatalogAtRoute(
		fixture.driver, fixture.p, fixture.registry, registered, RouteID(7),
		ExecutorSourceCatalog{Poll: fixture.poll},
	) {
		t.Fatal("bind poll owner")
	}
	if !Enqueue(fixture.p, fixture.task.g) {
		t.Fatal("enqueue poll task")
	}
	if next, ok := NextRunnableAt(fixture.p, 0); !ok || next != fixture.task.g {
		t.Fatal("dequeue poll task")
	}
	fixture.action = beginWaitTestResume(t, fixture.p, fixture.task)
	fixture.task.frame.header.SuspendReason = uint16(SuspendPark)
	fixture.task.frame.header.Lifecycle = uint16(FrameSuspended)
	var prepared bool
	fixture.ticket, fixture.handle, fixture.operation, fixture.executor, prepared = PrepareCurrentExecutorPollPark(
		fixture.driver, fixture.task.g, fixture.task.handle, fixture.task.frame.header,
		&fixture.wait, 1, 1, 42, PollInterestRead, 0,
	)
	if !prepared || !BindSingleWaitSetResumePacket(&fixture.wait, packet, fixture.operation) {
		t.Fatal("prepare/bind poll park")
	}
	if parked, ok := Resumed(fixture.p, fixture.task.g, fixture.action); !ok || parked.Kind != ActionPark {
		t.Fatalf("commit poll park = (%+v, %t)", parked, ok)
	}
	if _, promoted, ok := PollExecutorAt(fixture.driver, 0); !ok || promoted != 0 {
		t.Fatalf("initial idle poll = (%d, %t)", promoted, ok)
	}

	// Hold an admitted producer inflight so shutdown cancellation must
	// transition the wait to waitSetWorkAwaitingExternal instead of promoting it.
	slot, ok := pollOperationSlotAt(fixture.poll, fixture.operation.LocalSlot()-1)
	if !ok || !producerAdmissionAcquire(&slot.v2Producer.inflight) {
		t.Fatal("admit poll producer before shutdown")
	}
	if needed, ok := RequestExecutorShutdownDrain(fixture.driver); !ok || !needed {
		t.Fatalf("first shutdown drain = (%t, %t)", needed, ok)
	}
	if _, promoted, ok := PollExecutorAt(fixture.driver, 0); !ok || promoted != 0 ||
		fixture.wait.work != waitSetWorkAwaitingExternal {
		t.Fatalf("awaiting external after first shutdown = (promoted=%d, ok=%t, work=%d)", promoted, ok, fixture.wait.work)
	}
	if fixture.p.affectedWaitHead != nil {
		t.Fatal("affected wait queue must be empty once awaiting external")
	}

	// Repeating shutdown drain across peer run-slice boundaries must not
	// requeue an already-canceled wait that is still awaiting external completion,
	// otherwise it would livelock and starve sibling ready tasks (such as a
	// shutdown sentinel whose defer releases the blocked external operation).
	if needed, ok := RequestExecutorShutdownDrain(fixture.driver); !ok || !needed {
		t.Fatalf("repeat shutdown drain = (%t, %t)", needed, ok)
	}
	if fixture.wait.work != waitSetWorkAwaitingExternal || fixture.p.affectedWaitHead != nil {
		t.Fatalf("repeat shutdown drain requeued awaiting external wait: work=%d affected=%p", fixture.wait.work, fixture.p.affectedWaitHead)
	}

	// Complete the admitted producer and verify shutdown finishes cleanly.
	if !preemptCompareAndSwap(
		&slot.v2Mailbox,
		uint32(pollOperationMailboxEmpty),
		uint32(pollOperationMailboxPosting),
	) {
		t.Fatal("claim late admitted poll mailbox")
	}
	slot.v2Result = PollOperationReady
	preemptStore(&slot.v2Mailbox, uint32(pollOperationMailboxPosted))
	preemptStore(&fixture.poll.pending, 1)
	producerAdmissionRelease(&slot.v2Producer.inflight)

	if _, promoted, ok := PollExecutorAt(fixture.driver, 0); !ok || promoted != 1 {
		t.Fatalf("resolve shutdown completion = (%d, %t)", promoted, ok)
	}
	if next, ok := NextRunnableAt(fixture.p, 0); !ok || next != fixture.task.g {
		t.Fatalf("dequeue canceled task = (%p, %t)", next, ok)
	}
	action, ok := BeginRunG(fixture.p, fixture.task.g)
	if !ok || action.Kind != ActionCheckResume {
		t.Fatalf("begin canceled task = (%+v, %t)", action, ok)
	}
	fixture.action, ok = Checked(fixture.p, fixture.task.g, action, false)
	if !ok || fixture.action.Kind != ActionResume {
		t.Fatalf("activate canceled task = (%+v, %t)", fixture.action, ok)
	}
	outcome, _, cancel, _, _, taken := TakeResumePacket(fixture.task.g, fixture.ticket, packet, nil)
	if !taken || outcome != ParkOutcomeCanceled || cancel != TaskCancelShutdown {
		t.Fatalf("shutdown cleanup resume = (taken=%t, outcome=%d, cancel=%d)", taken, outcome, cancel)
	}
	fixture.task.frame.header.SuspendReason = uint16(SuspendNone)
	fixture.task.frame.header.Lifecycle = uint16(FrameActive)
	yieldRunningDriverTask(t, fixture.p, fixture.task, fixture.action)
	closeTestExecutorDriver(t, fixture.driver)
	finishReadyDriverTasks(t, fixture.p, map[*G]*yieldingTestG{fixture.task.g: fixture.task})
	if !AcknowledgeTaskCancellation(fixture.task.g, TaskCancelShutdown) {
		t.Fatal("acknowledge shutdown cancellation")
	}
	if !TerminalG(fixture.p, fixture.task.g) || !fixture.poll.CanRelease() || !fixture.registry.CanRelease() {
		t.Fatal("shutdown awaiting-external owner retained terminal state")
	}
}
