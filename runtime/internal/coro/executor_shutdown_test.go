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
