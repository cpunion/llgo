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
