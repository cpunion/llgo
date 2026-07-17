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
	"sync"
	"testing"
	"unsafe"
)

func closeTaskControlFixture(t *testing.T, source *TaskControlSource, p *P, id OperationID) {
	t.Helper()
	if !BeginCloseTaskControl(source, p, id) {
		t.Fatal("begin close task control")
	}
	if delivered, discarded, ok := source.PublishPass(p); !ok || delivered != 0 || discarded != 0 {
		t.Fatalf("final task control drain = (%d, %d, %t)", delivered, discarded, ok)
	}
	if !ConfirmTaskControlQuiesced(source, p, id) || !RetireTaskControl(source, p, id) {
		t.Fatal("quiesce and retire task control")
	}
}

func TestTaskControlSourceDeliversStrongestRequestOnOwner(t *testing.T) {
	if unsafe.Sizeof(OperationID{}) != 8 {
		t.Fatalf("task control producer ID size = %d", unsafe.Sizeof(OperationID{}))
	}
	p, g := newReadyTaskCancelFixture(t)
	var source TaskControlSource
	if !BindTaskControlSource(&source, p) {
		t.Fatal("bind task control source")
	}
	id, ok := RegisterTaskControl(&source, p, g)
	if !ok || id.Source() != OperationSourceControl || id.Slot() != 1 || id.Generation != 1 {
		t.Fatalf("register task control = (%+v, %t)", id, ok)
	}
	posts := []struct {
		kind TaskCancelKind
		want TaskControlPostResult
	}{
		{TaskCancelAbort, TaskControlPosted},
		{TaskCancelAbort, TaskControlCoalesced},
		{TaskCancelShutdown, TaskControlPosted},
		{TaskCancelAbort, TaskControlCoalesced},
	}
	for _, post := range posts {
		if got := source.Post(id, post.kind); got != post.want {
			t.Fatalf("post %d = %d, want %d", post.kind, got, post.want)
		}
	}
	if !source.Pending() {
		t.Fatal("merged control request not pending")
	}
	if delivered, discarded, ok := source.PublishPass(p); !ok || delivered != 1 || discarded != 0 {
		t.Fatalf("publish strongest task control = (%d, %d, %t)", delivered, discarded, ok)
	}
	if source.Pending() {
		t.Fatal("control source remained pending after drain")
	}
	if kind, ok := TaskCancellationOf(p, g); !ok || kind != TaskCancelShutdown {
		t.Fatalf("delivered task cancellation = (%d, %t)", kind, ok)
	}
	closeTaskControlFixture(t, &source, p, id)
	if !UnbindTaskControlSource(&source, p) || !source.CanRelease() {
		t.Fatal("release task control source")
	}
	if kind, ok := ClaimTaskCancellation(p, g); !ok || kind != TaskCancelShutdown {
		t.Fatalf("claim delivered shutdown = (%d, %t)", kind, ok)
	}
	finishTaskCancelFixture(t, p, g, TaskCancelShutdown)
}

func TestTaskControlLeaseUsesExistingGAlignmentPadding(t *testing.T) {
	stateEnd := unsafe.Offsetof(G{}.state) + unsafe.Sizeof(GState(0))
	leaseOffset := unsafe.Offsetof(G{}.taskControlLeases)
	leaseEnd := leaseOffset + unsafe.Sizeof(G{}.taskControlLeases)
	pointerAlign := unsafe.Alignof(uintptr(0))
	align := func(offset uintptr) uintptr { return (offset + pointerAlign - 1) &^ (pointerAlign - 1) }
	rootOffset := unsafe.Offsetof(G{}.root)
	if leaseOffset != stateEnd || align(stateEnd) != rootOffset || align(leaseEnd) != rootOffset {
		t.Fatalf("task control lease changed G pointer layout: stateEnd=%d lease=%d..%d root=%d align=%d",
			stateEnd, leaseOffset, leaseEnd, rootOffset, pointerAlign)
	}
}

func TestTaskControlSourceGenerationRejectsABA(t *testing.T) {
	p, g := newReadyTaskCancelFixture(t)
	var source TaskControlSource
	if !BindTaskControlSource(&source, p) {
		t.Fatal("bind task control source")
	}
	first, ok := RegisterTaskControl(&source, p, g)
	if !ok {
		t.Fatal("register first task control generation")
	}
	closeTaskControlFixture(t, &source, p, first)
	if got := source.Post(first, TaskCancelAbort); got != TaskControlPostClosed {
		t.Fatalf("post retired generation = %d", got)
	}
	second, ok := RegisterTaskControl(&source, p, g)
	if !ok || second.Slot() != first.Slot() || second.Generation != first.Generation+1 {
		t.Fatalf("register second task control generation = (%+v, %t), first %+v", second, ok, first)
	}
	if got := source.Post(first, TaskCancelShutdown); got != TaskControlPostStale {
		t.Fatalf("post stale reused generation = %d", got)
	}
	if source.Pending() {
		t.Fatal("stale post published pending work")
	}
	closeTaskControlFixture(t, &source, p, second)
	if !UnbindTaskControlSource(&source, p) {
		t.Fatal("unbind task control source")
	}
}

func TestTaskControlSourceFinalDrainDeliversAdmittedLatePost(t *testing.T) {
	p, g := newReadyTaskCancelFixture(t)
	var source TaskControlSource
	if !BindTaskControlSource(&source, p) {
		t.Fatal("bind task control source")
	}
	id, ok := RegisterTaskControl(&source, p, g)
	if !ok {
		t.Fatal("register task control")
	}
	slot, valid := taskControlSlotFor(&source, id)
	if !valid || !taskControlAcquireProducer(slot) {
		t.Fatal("admit producer before endpoint close")
	}
	if !BeginCloseTaskControl(&source, p, id) {
		t.Fatal("seal task control producers")
	}
	// Model a producer paused after validating Active but before publishing.
	preemptStore(&slot.request, uint32(TaskCancelAbort))
	preemptStore(&source.pending, 1)
	taskControlReleaseProducer(slot)
	if ConfirmTaskControlQuiesced(&source, p, id) {
		t.Fatal("confirmed endpoint before final late-fact drain")
	}
	if delivered, discarded, ok := source.PublishPass(p); !ok || delivered != 1 || discarded != 0 {
		t.Fatalf("deliver admitted late endpoint fact = (%d, %d, %t)", delivered, discarded, ok)
	}
	if kind, pending := TaskCancellationOf(p, g); !pending || kind != TaskCancelAbort {
		t.Fatalf("admitted late cancellation = (%d, %t)", kind, pending)
	}
	if !ConfirmTaskControlQuiesced(&source, p, id) || !RetireTaskControl(&source, p, id) ||
		!UnbindTaskControlSource(&source, p) {
		t.Fatal("retire late-post endpoint")
	}
	if kind, ok := ClaimTaskCancellation(p, g); !ok || kind != TaskCancelAbort {
		t.Fatalf("claim admitted late abort = (%d, %t)", kind, ok)
	}
	finishTaskCancelFixture(t, p, g, TaskCancelAbort)
}

func TestTaskControlSourceRejectedDeliveryRestoresDurableFact(t *testing.T) {
	p, g := newReadyTaskCancelFixture(t)
	var source TaskControlSource
	if !BindTaskControlSource(&source, p) {
		t.Fatal("bind task control source")
	}
	id, ok := RegisterTaskControl(&source, p, g)
	if !ok || dequeue(p) != g {
		t.Fatal("register control before legacy wait")
	}
	// Task cancellation intentionally has no legacy-wait bridge. Until every
	// production park is V2, a failed owner delivery must retain the exact
	// request instead of turning this migration boundary into silent loss.
	attachWaitingTaskCancelFixture(p, g)
	if result := source.Post(id, TaskCancelAbort); result != TaskControlPosted {
		t.Fatalf("post legacy-wait task cancellation = %d", result)
	}
	if delivered, discarded, published := source.PublishPass(p); published || delivered != 0 || discarded != 0 {
		t.Fatalf("legacy-wait publish unexpectedly succeeded = (%d, %d, %t)", delivered, discarded, published)
	}
	slot, valid := taskControlSlotFor(&source, id)
	if !valid || TaskCancelKind(preemptLoad(&slot.request)) != TaskCancelAbort || !source.Pending() ||
		g.park.taskCancelKind != TaskCancelNone || g.park.taskCancelPhase != taskCancelIdle {
		t.Fatal("rejected owner delivery did not restore the durable request")
	}

	detachWaitingTaskCancelFixture(p, g)
	g.state = GRunnable
	if !Enqueue(p, g) {
		t.Fatal("restore runnable owner after legacy wait")
	}
	if delivered, discarded, published := source.PublishPass(p); !published || delivered != 1 || discarded != 0 || source.Pending() {
		t.Fatalf("retry restored task cancellation = (%d, %d, %t), pending=%t", delivered, discarded, published, source.Pending())
	}
	closeTaskControlFixture(t, &source, p, id)
	if !UnbindTaskControlSource(&source, p) {
		t.Fatal("unbind restored control source")
	}
	if kind, claimed := ClaimTaskCancellation(p, g); !claimed || kind != TaskCancelAbort {
		t.Fatalf("claim restored cancellation = (%d, %t)", kind, claimed)
	}
	finishTaskCancelFixture(t, p, g, TaskCancelAbort)
}

func TestTaskControlSourceConcurrentPostsCoalesceWithoutLoss(t *testing.T) {
	p, g := newReadyTaskCancelFixture(t)
	var source TaskControlSource
	if !BindTaskControlSource(&source, p) {
		t.Fatal("bind task control source")
	}
	id, ok := RegisterTaskControl(&source, p, g)
	if !ok {
		t.Fatal("register task control")
	}
	const producers = 64
	results := make(chan TaskControlPostResult, producers)
	var group sync.WaitGroup
	for index := 0; index < producers; index++ {
		kind := TaskCancelAbort
		if index%7 == 0 {
			kind = TaskCancelShutdown
		}
		group.Add(1)
		go func() {
			defer group.Done()
			results <- source.Post(id, kind)
		}()
	}
	group.Wait()
	close(results)
	posted := 0
	for result := range results {
		if result != TaskControlPosted && result != TaskControlCoalesced {
			t.Fatalf("concurrent post result = %d", result)
		}
		if result == TaskControlPosted {
			posted++
		}
	}
	if posted == 0 {
		t.Fatal("no concurrent producer published a fact")
	}
	if delivered, discarded, ok := source.PublishPass(p); !ok || delivered != 1 || discarded != 0 {
		t.Fatalf("publish concurrent control facts = (%d, %d, %t)", delivered, discarded, ok)
	}
	if kind, ok := TaskCancellationOf(p, g); !ok || kind != TaskCancelShutdown {
		t.Fatalf("concurrent strongest cancellation = (%d, %t)", kind, ok)
	}
	closeTaskControlFixture(t, &source, p, id)
	if !UnbindTaskControlSource(&source, p) {
		t.Fatal("unbind task control source")
	}
	if kind, ok := ClaimTaskCancellation(p, g); !ok || kind != TaskCancelShutdown {
		t.Fatalf("claim concurrent shutdown = (%d, %t)", kind, ok)
	}
	finishTaskCancelFixture(t, p, g, TaskCancelShutdown)
}

func TestTaskControlSourceDiscardsPostAfterTaskTerminal(t *testing.T) {
	p, g := newReadyTaskCancelFixture(t)
	var source TaskControlSource
	if !BindTaskControlSource(&source, p) {
		t.Fatal("bind task control source")
	}
	id, ok := RegisterTaskControl(&source, p, g)
	if !ok {
		t.Fatal("register task control")
	}
	if dequeue(p) != g {
		t.Fatal("dequeue task before terminal transition")
	}
	preemptStore(preemptAddress(g), preemptDisabled)
	g.state = GDead
	if ReclaimableG(g) {
		t.Fatal("terminal G became reclaimable while control endpoint retained it")
	}
	if result := source.Post(id, TaskCancelShutdown); result != TaskControlPosted {
		t.Fatalf("post against not-yet-retired terminal endpoint = %d", result)
	}
	if delivered, discarded, ok := source.PublishPass(p); !ok || delivered != 0 || discarded != 1 {
		t.Fatalf("discard terminal task control = (%d, %d, %t)", delivered, discarded, ok)
	}
	closeTaskControlFixture(t, &source, p, id)
	if !UnbindTaskControlSource(&source, p) || !ReclaimableG(g) {
		t.Fatal("release terminal task control source")
	}
}

func TestTaskControlSourceLastEndpointOwnsTerminalStorageLease(t *testing.T) {
	p, g := newReadyTaskCancelFixture(t)
	var source TaskControlSource
	if !BindTaskControlSource(&source, p) {
		t.Fatal("bind task control source")
	}
	first, firstOK := RegisterTaskControl(&source, p, g)
	second, secondOK := RegisterTaskControl(&source, p, g)
	if !firstOK || !secondOK || g.taskControlLeases != 2 {
		t.Fatalf("register two task control leases = (%t, %t), leases=%d", firstOK, secondOK, g.taskControlLeases)
	}
	if dequeue(p) != g {
		t.Fatal("dequeue multi-endpoint task")
	}
	preemptStore(preemptAddress(g), preemptDisabled)
	g.state = GDead
	if ReclaimableG(g) {
		t.Fatal("multi-endpoint terminal task became reclaimable")
	}
	closeTaskControlFixture(t, &source, p, first)
	if g.taskControlLeases != 1 || ReclaimableG(g) {
		t.Fatalf("first endpoint released terminal task: leases=%d reclaimable=%t", g.taskControlLeases, ReclaimableG(g))
	}
	closeTaskControlFixture(t, &source, p, second)
	if g.taskControlLeases != 0 || !ReclaimableG(g) || !UnbindTaskControlSource(&source, p) {
		t.Fatalf("last endpoint did not release terminal task: leases=%d reclaimable=%t", g.taskControlLeases, ReclaimableG(g))
	}
}

func TestPostTaskControlRequestsExecutorAfterDurableFact(t *testing.T) {
	p, g := newReadyTaskCancelFixture(t)
	var source TaskControlSource
	var registry ExecutorRegistry
	if !BindTaskControlSource(&source, p) {
		t.Fatal("bind task control source")
	}
	id, taskOK := RegisterTaskControl(&source, p, g)
	executor, executorOK := registry.Register()
	if !taskOK || !executorOK {
		t.Fatal("register control and executor handles")
	}
	result := PostTaskControlAndRequest(&source, id, TaskCancelAbort, &registry, executor)
	if result.Control != TaskControlPosted || result.Executor != ExecutorRequestPublished ||
		!source.Pending() || !registry.ObserveRequested(executor) {
		t.Fatalf("post control and request = (%d, %d)", result.Control, result.Executor)
	}
	if delivered, discarded, ok := source.PublishPass(p); !ok || delivered != 1 || discarded != 0 {
		t.Fatalf("publish requested control = (%d, %d, %t)", delivered, discarded, ok)
	}
	if cleared, ok := registry.Acknowledge(executor); !ok || !cleared {
		t.Fatalf("acknowledge executor request = (%t, %t)", cleared, ok)
	}
	closeTaskControlFixture(t, &source, p, id)
	if !UnbindTaskControlSource(&source, p) || !registry.BeginClose(executor) ||
		!registry.ConfirmQuiesced(executor) || !registry.Retire(executor) || !registry.CanRelease() {
		t.Fatal("release task control/executor ingress")
	}
	if kind, ok := ClaimTaskCancellation(p, g); !ok || kind != TaskCancelAbort {
		t.Fatalf("claim requested abort = (%d, %t)", kind, ok)
	}
	finishTaskCancelFixture(t, p, g, TaskCancelAbort)
}

func TestExecutorDriverTerminalCloseJoinsActiveTaskControls(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	control := new(TaskControlSource)
	executor := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, executor, ExecutorSourceCatalog{Waits: waits, Control: control}) {
		t.Fatal("bind terminal control-source executor")
	}
	task := newYieldingTestG(t, "driver-terminal-control")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue terminal control task")
	}
	if next, ok := NextRunnable(p); !ok || next != task.g {
		t.Fatal("dequeue terminal control task")
	}
	action := beginWaitTestResume(t, p, task)
	before, beforeOK := RegisterTaskControl(control, p, task.g)
	late, lateOK := RegisterTaskControl(control, p, task.g)
	if !beforeOK || !lateOK || task.g.taskControlLeases != 2 {
		t.Fatalf("register terminal task controls = (%t, %t), leases=%d", beforeOK, lateOK, task.g.taskControlLeases)
	}
	posted := PostTaskControlAndRequest(control, before, TaskCancelAbort, registry, executor)
	if posted.Control != TaskControlPosted || posted.Executor != ExecutorRequestPublished {
		t.Fatalf("post immediately before terminal destroy = (%d, %d)", posted.Control, posted.Executor)
	}
	executorSlot, executorSlotOK := executorSlot(registry, executor)
	if !executorSlotOK || !executorAcquireProducer(executorSlot) {
		t.Fatal("pin terminal executor request tail")
	}

	// Pin one producer after admission and Active validation. It represents a
	// target call which entered before the terminal seal but does not publish
	// its durable fact or executor request tail until after the close action.
	lateSlot, valid := taskControlSlotFor(control, late)
	if !valid || !taskControlAcquireProducer(lateSlot) ||
		preemptLoad(&lateSlot.generation) != late.Generation ||
		preemptLoad(&lateSlot.state) != uint32(taskControlActive) {
		t.Fatal("admit late terminal control producer")
	}

	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare terminal control completion")
	}
	action, ok := Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatal("resume terminal control completion")
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatal("check terminal control destroy")
	}
	releaseTestFrame(t, task.g, task.frame)
	closeAction, committed := Destroyed(p, task.g, action)
	if !committed || closeAction.Kind != ActionTerminalExecutorClose || closeAction.Handle != nil ||
		driver.state != executorDriverTerminalClosing || task.g.root != nil ||
		task.g.taskControlLeases != 2 || task.g.park.taskCancelKind != TaskCancelNone ||
		task.g.park.taskCancelPhase != taskCancelIdle {
		t.Fatalf("begin terminal control close = (%+v, %t), state=%d leases=%d cancel=(%d,%d)",
			closeAction, committed, driver.state, task.g.taskControlLeases,
			task.g.park.taskCancelKind, task.g.park.taskCancelPhase)
	}
	if preemptLoad(&lateSlot.state) != uint32(taskControlClosing) ||
		preemptLoad(&lateSlot.inflight) != taskControlProducerClosed|1 {
		t.Fatalf("terminal seal did not retain admitted producer: state=%d inflight=%#x",
			preemptLoad(&lateSlot.state), preemptLoad(&lateSlot.inflight))
	}
	if result := control.Post(late, TaskCancelShutdown); result != TaskControlPostClosed {
		t.Fatalf("new post after terminal seal = %d", result)
	}
	if stale, staleOK := Destroyed(p, task.g, action); staleOK || stale != (Action{}) {
		t.Fatal("terminal control close allowed a second root destroy")
	}
	if completed, terminal, confirmed := ConfirmTerminalExecutorClose(driver); confirmed || completed != nil || terminal != (Action{}) ||
		task.g.taskControlLeases != 2 || lateSlot.task != task.g {
		t.Fatalf("terminal control close crossed producer join = (%p, %+v, %t), leases=%d task=%p",
			completed, terminal, confirmed, task.g.taskControlLeases, lateSlot.task)
	}

	// Complete the already-admitted producer after ActionTerminalExecutorClose.
	// Its executor request loses to the closed gate, but its durable fact remains
	// in the Closing endpoint for Confirm's mandatory final owner drain.
	if !preemptCompareAndSwap(&lateSlot.request, uint32(TaskCancelNone), uint32(TaskCancelShutdown)) {
		t.Fatal("publish admitted terminal-late request")
	}
	preemptStore(&control.pending, 1)
	taskControlReleaseProducer(lateSlot)
	if request := registry.Request(executor); request != ExecutorRequestClosed {
		t.Fatalf("terminal-late executor request = %d", request)
	}
	if completed, terminal, confirmed := ConfirmTerminalExecutorClose(driver); confirmed || completed != nil || terminal != (Action{}) ||
		task.g.taskControlLeases != 2 || lateSlot.task != task.g {
		t.Fatalf("terminal control close partially committed before executor join = (%p, %+v, %t), leases=%d task=%p",
			completed, terminal, confirmed, task.g.taskControlLeases, lateSlot.task)
	}
	executorReleaseProducer(executorSlot)
	completed, terminal, confirmed := ConfirmTerminalExecutorClose(driver)
	if !confirmed || completed != task.g || terminal.Kind != ActionComplete || terminal.Handle != nil ||
		!TerminalG(p, task.g) || task.g.taskControlLeases != 0 ||
		task.g.park.taskCancelKind != TaskCancelNone || task.g.park.taskCancelPhase != taskCancelIdle {
		t.Fatalf("confirm terminal control close = (%p, %+v, %t), terminalG=%t leases=%d cancel=(%d,%d)",
			completed, terminal, confirmed, TerminalG(p, task.g), task.g.taskControlLeases,
			task.g.park.taskCancelKind, task.g.park.taskCancelPhase)
	}
	if *driver != (ExecutorDriver{}) || !control.CanRelease() || !waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("terminal control close retained stable ingress storage")
	}
	if repeated, repeatedAction, repeatedOK := ConfirmTerminalExecutorClose(driver); repeatedOK || repeated != nil || repeatedAction != (Action{}) {
		t.Fatalf("terminal control close confirmed twice = (%p, %+v, %t)", repeated, repeatedAction, repeatedOK)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestExecutorDriverControlSourceCancelsFrameLocalParkInPublishedEpoch(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	waits := new(WaitRegistrationTable)
	control := new(TaskControlSource)
	executor := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, executor, ExecutorSourceCatalog{Waits: waits, Control: control}) {
		t.Fatal("bind control-source executor")
	}
	task := newYieldingTestG(t, "driver-control")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue control-source task")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue control-source task")
	}
	action := beginWaitTestResume(t, p, task)
	ticket, ok := BeginParkSet(&task.g.park, 0, 109)
	var wait WaitSetRecord
	if !ok || !PrepareWaitSetRecord(&wait, task.g, ticket) || !SealParkSet(&task.g.park, ticket) {
		t.Fatal("prepare zero-candidate control wait")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(task.g, task.handle, task.frame.header, ticket, &wait) {
		t.Fatal("prepare control-source park")
	}
	if action, ok = Resumed(p, task.g, action); !ok || action.Kind != ActionPark || !HasWaiting(p) {
		t.Fatalf("commit control-source park = (%+v, %t), waiting=%t", action, ok, HasWaiting(p))
	}
	controlID, ok := RegisterTaskControl(control, p, task.g)
	if !ok {
		t.Fatal("register parked task control endpoint")
	}
	post := PostTaskControlAndRequest(control, controlID, TaskCancelAbort, registry, executor)
	if post.Control != TaskControlPosted || post.Executor != ExecutorRequestPublished {
		t.Fatalf("post parked task cancellation = (%d, %d)", post.Control, post.Executor)
	}
	if drained, promoted, ok := PollExecutor(driver); !ok || drained != 1 || promoted != 1 ||
		HasWaiting(p) || wait != (WaitSetRecord{}) {
		t.Fatalf("poll control-source published epoch = (%d, %d, %t), waiting=%t wait=%+v",
			drained, promoted, ok, HasWaiting(p), wait)
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue control-canceled task")
	}
	action = beginWaitTestResume(t, p, task)
	outcome, caseID, lease, taskKind, ok := TakeRunDecision(task.g, ticket)
	if !ok || outcome != ParkOutcomeCanceled || caseID != 0 || lease != (OperationResultLease{}) || taskKind != TaskCancelAbort {
		t.Fatalf("take control-source cancellation = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, taskKind, ok)
	}
	closeTaskControlFixture(t, control, p, controlID)
	yieldRunningDriverTask(t, p, task, action)
	closeTestExecutorDriver(t, driver)
	if !control.CanRelease() || !waits.CanRelease() || !registry.CanRelease() {
		t.Fatal("control-source executor retained ingress state")
	}
	if g, ok := NextRunnable(p); !ok || g != task.g {
		t.Fatal("dequeue control cleanup task")
	}
	finishWaitTestTask(t, p, task, beginWaitTestResume(t, p, task))
	if !AcknowledgeTaskCancellation(task.g, TaskCancelAbort) || !TerminalG(p, task.g) {
		t.Fatal("control-canceled task did not reach acknowledged terminal state")
	}
	runtime.KeepAlive(task.frame.memory)
}
