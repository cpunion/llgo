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

func TestDirectChannelCompletionInboxConcurrentReuse(t *testing.T) {
	const (
		producers  = 8
		iterations = 500
	)
	p := new(P)
	driver := &ExecutorDriver{
		magic: executorDriverMagic,
		state: executorDriverActive,
		p:     p,
		route: RouteID(1),
	}
	p.executor = driver
	stub := unsafe.Pointer(&driver.directChannelStub)
	preemptStorePointer(&driver.directChannelHead, stub)
	driver.directChannelTail = stub

	nodes := make([]DirectChannelCompletion, producers)
	ack := make([]chan struct{}, producers)
	index := make(map[*DirectChannelCompletion]int, producers)
	for producer := range nodes {
		node := &nodes[producer]
		node.owner = driver
		node.route = driver.route
		preemptStore(&node.state, uint32(directChannelCompletionMatched))
		ack[producer] = make(chan struct{}, 1)
		index[node] = producer
	}

	errors := make(chan int, producers)
	var group sync.WaitGroup
	group.Add(producers)
	for producer := range nodes {
		go func(producer int) {
			defer group.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				if !PublishExecutorDirectChannelCompletion(driver, &nodes[producer]) {
					errors <- producer
					return
				}
				<-ack[producer]
			}
		}(producer)
	}

	seen := make([]int, producers)
	for completed := 0; completed < producers*iterations; {
		node, ok := takeExecutorDirectChannelCompletion(driver)
		if !ok {
			t.Fatal("take concurrent direct channel completion")
		}
		if node == nil {
			select {
			case producer := <-errors:
				t.Fatalf("producer %d could not publish direct channel completion", producer)
			default:
			}
			runtime.Gosched()
			continue
		}
		producer, known := index[node]
		if !known {
			t.Fatalf("take unknown direct channel completion %p", node)
		}
		seen[producer]++
		completed++
		preemptStore(&node.state, uint32(directChannelCompletionMatched))
		ack[producer] <- struct{}{}
	}
	group.Wait()
	select {
	case producer := <-errors:
		t.Fatalf("producer %d could not publish direct channel completion", producer)
	default:
	}
	for producer, count := range seen {
		if count != iterations {
			t.Fatalf("producer %d completions = %d, want %d", producer, count, iterations)
		}
	}
	if !executorDirectChannelInboxIdle(driver) || executorDirectChannelCompletionPending(driver) {
		t.Fatal("direct channel completion inbox retained a node")
	}
}

func TestDirectChannelCompletionPublishesToSleepingOwner(t *testing.T) {
	p := new(P)
	driver := &ExecutorDriver{
		magic: executorDriverMagic,
		state: executorDriverSleeping,
		p:     p,
		route: RouteID(1),
	}
	p.executor = driver
	stub := unsafe.Pointer(&driver.directChannelStub)
	preemptStorePointer(&driver.directChannelHead, stub)
	driver.directChannelTail = stub

	completion := &DirectChannelCompletion{
		owner: driver,
		route: driver.route,
		state: uint32(directChannelCompletionMatched),
	}
	if !PublishExecutorDirectChannelCompletion(driver, completion) {
		t.Fatal("publish direct channel completion to sleeping owner")
	}
	got, ok := takeExecutorDirectChannelCompletion(driver)
	if !ok || got != completion {
		t.Fatalf("take sleeping-owner completion = (%p, %t), want (%p, true)", got, ok, completion)
	}
}

func TestDirectChannelCanceledWaiterAfterOwnerTakesCompletion(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	completion := &DirectChannelCompletion{
		owner: driver,
		route: driver.route,
		state: uint32(directChannelCompletionBound),
	}
	if !requestDirectChannelCancellation(completion) {
		t.Fatal("publish canceled channel completion")
	}
	if got := BeginDirectChannelCompletion(completion); got != DirectChannelCompletionBeginCanceled {
		t.Fatalf("published canceled waiter = %d", got)
	}
	node, ok := takeExecutorDirectChannelCompletion(driver)
	if !ok || node != completion {
		t.Fatal("take canceled channel completion before unlinking typed waiter")
	}
	// The owner next takes the hchan lock to remove the typed waiter. A
	// producer already holding that lock can still dequeue it in this gap.
	if got := BeginDirectChannelCompletion(completion); got != DirectChannelCompletionBeginCanceled {
		t.Fatalf("taken canceled waiter = %d, want canceled", got)
	}
	preemptStore(&completion.small, 3)
	if got := BeginDirectChannelCompletion(completion); got != DirectChannelCompletionBeginInvalid {
		t.Fatalf("taken matched waiter = %d, want invalid", got)
	}
	preemptStore(&completion.small, uint32(ResumeSmallInvalid))
	preemptStore(&completion.state, uint32(directChannelCompletionMaterialized))
	if got := BeginDirectChannelCompletion(completion); got != DirectChannelCompletionBeginInvalid {
		t.Fatalf("already materialized waiter = %d, want invalid", got)
	}
	closeTestExecutorDriver(t, driver)
}

func TestDirectChannelCompletionValidationAcrossProducerPublication(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
	task := newYieldingTestG(t, "direct-producer-snapshot")
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
		t.Fatal("activate direct channel park")
	}
	completion := &storage.Completion
	observed := directChannelCompletionState(preemptLoad(&completion.state))
	// Reproduce the native observation exactly: the owner reads Bound, then
	// the remote producer publishes its result before the owner reads small.
	if BeginDirectChannelCompletion(completion) != DirectChannelCompletionBeginAcquired {
		t.Fatal("claim the producer effect")
	}
	if owner, route, ok := FinishDirectChannelCompletion(completion, 4, 0); !ok || owner != driver || route != driver.route {
		t.Fatal("publish the producer result between owner reads")
	}
	small := preemptLoad(&completion.small)
	if !validDirectChannelCompletionSnapshot(completion, observed, small) {
		t.Fatalf("legal producer transition rejected: observed=%d small=%d current=%d", observed, small, preemptLoad(&completion.state))
	}
	for _, state := range []directChannelCompletionState{
		directChannelCompletionUnused, directChannelCompletionBound,
		directChannelCompletionCanceled, directChannelCompletionMaterialized,
	} {
		invalid := &DirectChannelCompletion{state: uint32(state), small: 4}
		if validDirectChannelCompletionSnapshot(invalid, state, 4) {
			t.Fatalf("invalid nonzero result accepted in state %d", state)
		}
	}
	if validDirectChannelCompletionSnapshot(completion, directChannelCompletionMatched, 256) {
		t.Fatal("out-of-range result accepted")
	}
}

func TestDirectChannelParkPreservesCleanupCancellation(t *testing.T) {
	for _, phase := range []string{"ordinary", "shielded", "pending-shield", "claimed-cleanup"} {
		for _, issued := range []bool{false, true} {
			for _, completion := range []string{"inline", "routed", "before-suspend"} {
				name := phase + "/" + completion + "/compatibility"
				if issued {
					name = phase + "/" + completion + "/issued"
				}
				t.Run(name, func(t *testing.T) {
					p := new(P)
					driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
					task := newYieldingTestG(t, "compact-cleanup")
					if !Enqueue(p, task.g) {
						t.Fatal("enqueue compact channel task")
					}
					if next, valid := NextRunnable(p); !valid || next != task.g {
						t.Fatal("select compact channel task")
					}
					action := beginWaitTestResume(t, p, task)
					var receipt unsafe.Pointer
					switch phase {
					case "shielded", "pending-shield":
						var valid bool
						receipt, valid = BeginCleanupShield(task.g, task.handle)
						if !valid || receipt != task.handle {
							t.Fatal("begin compact channel cleanup scope")
						}
					case "claimed-cleanup":
						if !RequestTaskCancellation(p, task.g, TaskCancelAbort) {
							t.Fatal("request compact channel task stop")
						}
						if kind, valid := ClaimTaskCancellation(p, task.g); !valid || kind != TaskCancelAbort {
							t.Fatal("claim compact channel task stop")
						}
					}
					if phase == "pending-shield" && !RequestTaskCancellation(p, task.g, TaskCancelAbort) {
						t.Fatal("defer task stop before compact preparation")
					}
					if issued {
						driver.run.issued = ActionCheckResume
					}
					task.frame.header.SuspendReason = uint16(SuspendPark)
					task.frame.header.Lifecycle = uint16(FrameSuspended)
					storage := new(DirectChannelParkStorageV1)
					owner, route := PrepareCurrentDirectChannelPark(task.g, task.handle, task.frame.header, storage)
					if owner != driver || route != driver.route {
						t.Fatal("prepare compact channel cleanup park")
					}
					if completion == "before-suspend" {
						if BeginDirectChannelCompletion(&storage.Completion) != DirectChannelCompletionBeginAcquired {
							t.Fatal("claim early physical result")
						}
						if _, _, result := FinishDirectChannelCompletionFromCompilerTask(task.g, &storage.Completion, 3, 0); result != DirectChannelCompletionFinishOwnerPublished {
							t.Fatalf("publish result before suspend: %d", result)
						}
					}
					if parked, valid := Resumed(p, task.g, action); !valid || parked.Kind != ActionPark {
						t.Fatalf("activate compact cleanup wait: %+v valid=%t", parked, valid)
					}
					// This test drives the physical reducer directly, without a run
					// slice. Close its simulated issued interval before selecting peers.
					driver.run.issued = ActionInvalid
					if phase == "pending-shield" || phase == "claimed-cleanup" {
						if !RequestTaskCancellation(p, task.g, TaskCancelShutdown) {
							t.Fatal("request stop while compact cleanup is parked")
						}
						if completion != "before-suspend" && executorDirectChannelCompletionPending(driver) {
							t.Fatal("deferred task stop canceled compact cleanup wait")
						}
					}
					if completion == "inline" {
						producer := newYieldingTestG(t, "compact-producer")
						if !Enqueue(p, producer.g) {
							t.Fatal("enqueue compact producer")
						}
						if next, valid := NextRunnable(p); !valid || next != producer.g {
							t.Fatal("select compact producer")
						}
						producerAction := beginWaitTestResume(t, p, producer)
						if BeginDirectChannelCompletion(&storage.Completion) != DirectChannelCompletionBeginAcquired {
							t.Fatal("claim owner-local physical result")
						}
						if _, _, result := FinishDirectChannelCompletionFromCompilerTask(producer.g, &storage.Completion, 3, 0); result != DirectChannelCompletionFinishInline {
							t.Fatalf("materialize owner-local physical result: %d", result)
						}
						finishWaitTestTask(t, p, producer, producerAction)
					} else {
						if completion == "routed" {
							if BeginDirectChannelCompletion(&storage.Completion) != DirectChannelCompletionBeginAcquired {
								t.Fatal("claim routed physical result")
							}
							owner, route, result := FinishDirectChannelCompletionFromCompilerTask(nil, &storage.Completion, 3, 0)
							if result != DirectChannelCompletionFinishNeedsTarget || owner != driver || route != driver.route ||
								!PublishExecutorDirectChannelCompletion(driver, &storage.Completion) {
								t.Fatal("publish routed physical result")
							}
						}
						node, valid := takeExecutorDirectChannelCompletion(driver)
						if !valid || node != &storage.Completion || !CommitDirectChannelCompletion(node, 3) {
							t.Fatal("materialize routed compact cleanup result")
						}
					}
					// Materialization already performed the source reduction and
					// published ready debt. Select that ready task directly, as the
					// bounded runner does, rather than invoking legacy PollExecutor.
					if !validReadyQueue(p) || dequeueOSThreadRunnable(p) != task.g {
						t.Fatal("resume compact cleanup task")
					}
					beginWaitTestResumeWithoutGate(t, p, task)
					driver.run.issued = ActionCheckResume
					outcome, stop, small, valid := TakeIssuedDirectChannelResume(task.g, storage)
					if !valid || outcome != ParkOutcomeCompleted || stop != TaskCancelNone || small != 3 {
						t.Fatalf("compact cleanup result: outcome=%d stop=%d small=%d valid=%t", outcome, stop, small, valid)
					}
					if receipt != nil {
						want := TaskCancelNone
						if phase == "pending-shield" {
							want = TaskCancelShutdown
						}
						if kind, valid := EndCleanupShield(task.g, task.handle, receipt); !valid || kind != want {
							t.Fatalf("end compact cleanup scope: kind=%d valid=%t want=%d", kind, valid, want)
						}
					} else if phase == "claimed-cleanup" &&
						(task.g.park.taskCancelKind != TaskCancelAbort || task.g.park.taskCancelPhase != taskCancelCleanup) {
						t.Fatal("compact park erased the previously claimed cleanup cause")
					}
					runtime.KeepAlive(task.frame)
				})
			}
		}
	}
}

func TestDirectChannelParkRejectsInterruptingOrInvalidTaskStop(t *testing.T) {
	for _, state := range []struct {
		kind  TaskCancelKind
		phase taskCancelPhase
	}{
		{TaskCancelAbort, taskCancelRequested},
		{TaskCancelShutdown, taskCancelRequested},
		{TaskCancelNone, taskCancelCleanup},
		{TaskCancelAbort, taskCancelIdle},
		{TaskCancelNone, taskCancelShieldedRequested},
		{TaskCancelKind(255), taskCancelShieldedRequested},
		{TaskCancelNone, taskCancelPhase(255)},
	} {
		for _, issued := range []bool{false, true} {
			p := new(P)
			driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
			task := newYieldingTestG(t, "reject-invalid-compact-stop")
			if !Enqueue(p, task.g) {
				t.Fatal("enqueue invalid compact stop test")
			}
			if next, valid := NextRunnable(p); !valid || next != task.g {
				t.Fatal("select invalid compact stop test")
			}
			beginWaitTestResume(t, p, task)
			if issued {
				driver.run.issued = ActionCheckResume
			}
			task.g.park.taskCancelKind, task.g.park.taskCancelPhase = state.kind, state.phase
			before := task.g.park
			task.frame.header.SuspendReason = uint16(SuspendPark)
			task.frame.header.Lifecycle = uint16(FrameSuspended)
			storage := new(DirectChannelParkStorageV1)
			if owner, route := PrepareCurrentDirectChannelPark(task.g, task.handle, task.frame.header, storage); owner != nil || route != 0 ||
				task.g.park != before || task.g.pending != (pendingTransition{}) || task.g.active.parkWait != nil ||
				*storage != (DirectChannelParkStorageV1{}) {
				t.Fatalf("compact prepare accepted or mutated rejected stop: kind=%d phase=%d issued=%t", state.kind, state.phase, issued)
			}
			runtime.KeepAlive(task.frame)
		}
	}
}

func TestDirectChannelParkReversibleAndRollback(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
	defer closeTestExecutorDriver(t, driver)
	task := newYieldingTestG(t, "reversible-compact-park")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue reversible park task")
	}
	if next, valid := NextRunnable(p); !valid || next != task.g {
		t.Fatal("select reversible park task")
	}
	action := beginWaitTestResume(t, p, task)
	driver.run.issued = ActionCheckResume
	beforePark := task.g.park
	beforePending := task.g.pending
	beforeReason := task.frame.header.SuspendReason
	beforeLifecycle := task.frame.header.Lifecycle

	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	storage := new(DirectChannelParkStorageV1)
	owner, route, undo := PrepareCurrentDirectChannelParkReversible(
		task.g, task.handle, task.frame.header, storage,
	)
	if owner != driver || route != driver.route || !undo.valid || task.g.active.parkWait == nil {
		t.Fatalf("PrepareCurrentDirectChannelParkReversible = (%p, %d, %+v)", owner, route, undo)
	}
	if !RollbackUnpublishedDirectChannelPark(task.g, task.handle, task.frame.header, storage, undo) {
		t.Fatal("RollbackUnpublishedDirectChannelPark failed")
	}
	if task.g.park != beforePark || task.g.pending != beforePending || task.g.active.parkWait != nil ||
		*storage != (DirectChannelParkStorageV1{}) ||
		task.frame.header.SuspendReason != beforeReason ||
		task.frame.header.Lifecycle != beforeLifecycle {
		t.Fatalf("rollback left dirty state: park=%+v pending=%+v wait=%p reason=%d life=%d",
			task.g.park, task.g.pending, task.g.active.parkWait,
			task.frame.header.SuspendReason, task.frame.header.Lifecycle)
	}
	// A second rollback on the already-cleared storage must fail closed.
	if RollbackUnpublishedDirectChannelPark(task.g, task.handle, task.frame.header, storage, undo) {
		t.Fatal("duplicate rollback must fail closed")
	}
	driver.run.issued = ActionInvalid
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare completion after rollback")
	}
	next, ok := Resumed(p, task.g, action)
	if !ok || next.Kind != ActionCheckDestroy {
		t.Fatalf("resume after rollback = (%+v, %t)", next, ok)
	}
	next, ok = Checked(p, task.g, next, true)
	if !ok || next.Kind != ActionDestroy {
		t.Fatalf("checked destroy after rollback = (%+v, %t)", next, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	receipt, ok := DestroyedBounded(p, task.g, next)
	if !ok || receipt.Kind != ActionCommitDestroy {
		t.Fatalf("destroyed bounded after rollback = (%+v, %t)", receipt, ok)
	}
	if complete, ok := CommitExecutorRunDomainDestroy(driver, task.g, receipt); !ok || complete.Kind != ActionComplete {
		t.Fatalf("domain destroy after rollback = (%+v, %t)", complete, ok)
	}
	runtime.KeepAlive(task.frame)
}

func TestPublishExecutorDirectChannelCompletionBatch(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
	defer closeTestExecutorDriver(t, driver)

	var nodes [3]DirectChannelCompletion
	for i := range nodes {
		nodes[i].owner = driver
		nodes[i].route = driver.route
		preemptStore(&nodes[i].state, uint32(directChannelCompletionMatched))
		if !PrepareDirectChannelCompletionForBatch(&nodes[i]) {
			t.Fatalf("PrepareDirectChannelCompletionForBatch[%d] failed", i)
		}
	}
	LinkDirectChannelCompletionBatch(&nodes[0], &nodes[1])
	LinkDirectChannelCompletionBatch(&nodes[1], &nodes[2])
	if !PublishExecutorDirectChannelCompletionBatch(driver, &nodes[0], &nodes[2]) {
		t.Fatal("PublishExecutorDirectChannelCompletionBatch failed")
	}
	for i := range nodes {
		got, ok := takeExecutorDirectChannelCompletion(driver)
		if !ok || got != &nodes[i] {
			t.Fatalf("take[%d] = (%p, %t), want (%p, true)", i, got, ok, &nodes[i])
		}
	}
	if !executorDirectChannelInboxIdle(driver) {
		t.Fatal("batch direct completion did not leave inbox idle")
	}
	if PublishExecutorDirectChannelCompletionBatch(nil, &nodes[0], &nodes[2]) ||
		PublishExecutorDirectChannelCompletionBatch(driver, nil, &nodes[2]) {
		t.Fatal("invalid batch publish arguments must fail closed")
	}
}

func TestDirectChannelSelectCompletionMultiCaseAndRace(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)

	// 1. State-machine arbitration: competing losing cases observe Canceled once
	//    another case acquires Bound->Effect, while Materialized/Unused stay Invalid.
	probe := &DirectChannelCompletion{
		owner: driver,
		route: driver.route,
		state: uint32(directChannelCompletionBound),
	}
	if got := BeginDirectChannelSelectCompletion(probe); got != DirectChannelCompletionBeginAcquired {
		t.Fatalf("first select case acquire = %d, want acquired", got)
	}
	if got := BeginDirectChannelSelectCompletion(probe); got != DirectChannelCompletionBeginCanceled {
		t.Fatalf("competing select case during Effect = %d, want canceled", got)
	}
	preemptStore(&probe.state, uint32(directChannelCompletionMatched))
	if got := BeginDirectChannelSelectCompletion(probe); got != DirectChannelCompletionBeginCanceled {
		t.Fatalf("competing select case during Matched = %d, want canceled", got)
	}
	preemptStore(&probe.state, uint32(directChannelCompletionTaken))
	preemptStore(&probe.small, packDirectChannelResult(2, 3))
	if got := BeginDirectChannelSelectCompletion(probe); got != DirectChannelCompletionBeginCanceled {
		t.Fatalf("competing select case during Taken = %d, want canceled", got)
	}
	preemptStore(&probe.state, uint32(directChannelCompletionMaterialized))
	if got := BeginDirectChannelSelectCompletion(probe); got != DirectChannelCompletionBeginInvalid {
		t.Fatalf("select case after Materialized = %d, want invalid", got)
	}

	// 2. Full park -> select caseID=3 completion -> materialize -> P-neutral
	//    migration -> late task cancellation -> TakeDirectChannelSelectResume.
	task := newYieldingTestG(t, "direct-select-case3")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue direct-select task")
	}
	if next, ok := NextRunnable(p); !ok || next != task.g {
		t.Fatalf("select direct-select task = (%p, %t)", next, ok)
	}
	action := beginWaitTestResume(t, p, task)
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)

	storage := new(DirectChannelParkStorageV1)
	if owner, route := PrepareCurrentDirectChannelPark(task.g, task.handle, task.frame.header, storage); owner != driver || route != driver.route {
		t.Fatalf("PrepareCurrentDirectChannelPark = (%p, %d)", owner, route)
	}
	if parked, ok := Resumed(p, task.g, action); !ok || parked.Kind != ActionPark {
		t.Fatalf("park direct-select task = (%+v, %t)", parked, ok)
	}
	if BeginDirectChannelSelectCompletion(&storage.Completion) != DirectChannelCompletionBeginAcquired {
		t.Fatal("acquire direct-select case 3")
	}
	// Multi-case select must never inline-promote; same-P producer publishes to inbox.
	if _, _, result := FinishDirectChannelSelectCompletionFromCompilerTask(
		nil, &storage.Completion, 4, 3, driver.route,
	); result != DirectChannelCompletionFinishNeedsTarget {
		t.Fatalf("FinishDirectChannelSelectCompletionFromCompilerTask(nil) = %d, want NeedsTarget", result)
	}
	if !PublishExecutorDirectChannelCompletion(driver, &storage.Completion) {
		t.Fatal("publish direct-select completion")
	}
	node, ok := takeExecutorDirectChannelCompletion(driver)
	if !ok || node != &storage.Completion {
		t.Fatalf("take direct-select completion = (%p, %t)", node, ok)
	}
	ctx, small, caseID, matched, snapOK := DirectChannelCompletionSelectSnapshot(node)
	if !snapOK || ctx != unsafe.Pointer(storage) || small != 4 || caseID != 3 || !matched {
		t.Fatalf("DirectChannelCompletionSelectSnapshot = (%p, %d, %d, %t, %t)", ctx, small, caseID, matched, snapOK)
	}
	if !CommitDirectChannelSelectCompletion(node, small, caseID) {
		t.Fatal("CommitDirectChannelSelectCompletion failed")
	}
	if task.g.park.winnerCase != 3 || task.g.park.outcome != ParkOutcomeCompleted || !task.g.park.directChannel {
		t.Fatalf("materialized select park = %+v", task.g.park)
	}
	if !RequestTaskCancellation(p, task.g, TaskCancelAbort) {
		t.Fatal("request late task cancellation on materialized direct select")
	}
	if !validReadyQueue(p) || dequeueOSThreadRunnable(p) != task.g {
		t.Fatal("dequeue materialized direct-select task")
	}
	check, ok := BeginRunG(p, task.g)
	if !ok || check.Kind != ActionCheckResume {
		t.Fatalf("BeginRunG direct-select = (%+v, %t)", check, ok)
	}
	resume, ok := Checked(p, task.g, check, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatalf("Checked direct-select = (%+v, %t)", resume, ok)
	}
	outcome, gotCase, cancelKind, gotSmall, taken := TakeDirectChannelSelectResume(task.g, storage)
	if !taken || outcome != ParkOutcomeCanceled || gotCase != 0 ||
		cancelKind != TaskCancelAbort || gotSmall != ResumeSmallInvalid {
		t.Fatalf("TakeDirectChannelSelectResume = (%d, %d, %d, %d, %t)",
			outcome, gotCase, cancelKind, gotSmall, taken)
	}
	runtime.KeepAlive(task.frame)
}

func TestDirectChannelSelectUrgentPendingFairness(t *testing.T) {
	p := new(P)
	driver := new(ExecutorDriver)
	registry := new(ExecutorRegistry)
	timers := new(TimerRegistrationTable)
	manual := new(ManualOperationSource)
	handle := registerTestExecutor(t, registry)
	if !BindExecutorSourceCatalog(driver, p, registry, handle, ExecutorSourceCatalog{
		Timers: timers,
		Manual: manual,
	}) {
		t.Fatal("bind fairness test executor driver")
	}

	task := newYieldingTestG(t, "urgent-fairness")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue fairness task")
	}
	driver.run.readyDebt = true
	driver.run.actionsSinceSource = executorRunUrgentSourceQuantum

	// Without urgent pending sources, readyDebt still precedes fallback poll below 64.
	if !executorRunReadyDebtBlocksSource(driver) {
		t.Fatal("readyDebt should still dispatch when no urgent source is pending")
	}
	// Mark manual source pending (models a woken sync.Mutex waiter).
	manual.scanLimit = 1
	preemptStore(&manual.pending, 1)
	if executorRunReadyDebtBlocksSource(driver) {
		t.Fatal("readyDebt must yield to urgent manual/poll pending source once urgent quantum is reached")
	}
	// Clear urgent pending and hit 64 with no active timers or affected waits:
	// pure channel workloads keep dispatching readyDebt without empty scans.
	preemptStore(&manual.pending, 0)
	manual.scanLimit = 0
	driver.run.actionsSinceSource = executorRunSourceQuantum
	if !executorRunReadyDebtBlocksSource(driver) {
		t.Fatal("readyDebt should stay fast at quantum when no timer or pending source exists")
	}
	// With an active timer (or affectedWaitHead), hitting 64 yields so timer
	// expirations and cancellations cannot starve under direct-channel traffic.
	timers.activeCount = 1
	if executorRunReadyDebtBlocksSource(driver) {
		t.Fatal("readyDebt must yield at executorRunSourceQuantum when timers are active")
	}
	timers.activeCount = 0
	var wait WaitSetRecord
	p.affectedWaitHead = &wait
	if executorRunReadyDebtBlocksSource(driver) {
		t.Fatal("readyDebt must yield at executorRunSourceQuantum when affectedWaitHead is non-nil")
	}
	p.affectedWaitHead = nil
	runtime.KeepAlive(task.frame)
}
