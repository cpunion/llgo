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
	"fmt"
	"runtime"
	"testing"
)

func unlockSuspendedTestTask(t *testing.T, driver *ExecutorDriver, task *yieldingTestG) {
	t.Helper()
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, _, _, ok := BeginIssuedExecutorResumeRuntimeContext(driver, task.g)
	if !ok || step.G != task.g {
		t.Fatal("begin restored owner")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	if !CurrentOSThreadLocked(task.g) || !ExitOSThreadLock(task.g) {
		t.Fatal("unlock restored owner")
	}
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare restored owner yield")
	}
	next, committed, ok := ResumedExecutorRun(driver, driver.p, task.g, resume)
	if !ok || !committed || next.Kind != ActionYield {
		t.Fatal("commit restored owner yield")
	}
}

func TestOSThreadSuspensionsAllowIndependentLocksOnSameP(t *testing.T) {
	for _, count := range []int{2, 16} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			p := new(P)
			driver, _, _ := bindTestExecutorDriver(t, p)
			tasks := make([]*yieldingTestG, count)
			records := make([]OSThreadSuspension, count)
			for i := range tasks {
				tasks[i] = newYieldingTestG(t, fmt.Sprintf("locked-%d", i))
				if !Enqueue(p, tasks[i].g) {
					t.Fatal("enqueue")
				}
			}
			for i, task := range tasks {
				next := commitLockedRunnerYield(t, driver, task)
				required, ok := PrepareOSThreadSuspendHandoff(driver, task.g, next.Kind)
				if !ok || !required || !DetachOSThreadSuspension(driver, task.g, &records[i], uint32(i+1)) {
					t.Fatalf("detach %d", i)
				}
				if p.osThreadLockOwner != nil || !validReadyQueue(p) || !validSchedulerWaitQueues(p) {
					t.Fatal("invalid detached owner queues")
				}
				if got := nextOSThreadRunnable(p); (i+1 < count && got != tasks[i+1].g) || (i+1 == count && got != nil) {
					t.Fatalf("compensation selected locked task at %d", i)
				}
			}
			// All tasks remain on the original FIFO. Restore the oldest, even though
			// the newest cold record is at the list head; a stack-only fix is invalid.
			for i, task := range tasks {
				record, owner, ok := ReadyOSThreadSuspension(driver)
				if !ok || record != &records[i] || owner != uint32(i+1) {
					t.Fatalf("ready %d = (%p,%d,%t)", i, record, owner, ok)
				}
				copied := *record
				if RestoreOSThreadSuspension(driver, &copied, owner) || RestoreOSThreadSuspension(driver, record, owner+1) {
					t.Fatal("accepted substituted identity")
				}
				if !RestoreOSThreadSuspension(driver, record, owner) || *record != (OSThreadSuspension{}) {
					t.Fatal("restore exact identity")
				}
				unlockSuspendedTestTask(t, driver, task)
			}
			if p.osThreadSuspensions != nil || p.osThreadLockOwner != nil || !validReadyQueue(p) {
				t.Fatal("retained completed affinity")
			}
			for _, task := range tasks {
				runtime.KeepAlive(task.frame.memory)
			}
		})
	}
}

func TestOSThreadSuspensionAbortPreservesUnservicedOwner(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	owner := newYieldingTestG(t, "abort-owner")
	peer := newYieldingTestG(t, "abort-peer")
	if !Enqueue(p, owner.g) || !Enqueue(p, peer.g) {
		t.Fatal("enqueue")
	}
	action := commitLockedRunnerYield(t, driver, owner)
	if required, ok := PrepareOSThreadSuspendHandoff(driver, owner.g, action.Kind); !ok || !required {
		t.Fatal("prepare")
	}
	var record OSThreadSuspension
	if !DetachOSThreadSuspension(driver, owner.g, &record, 7) {
		t.Fatal("detach")
	}
	if ready, id, ok := ReadyOSThreadSuspension(driver); !ok || ready != nil || id != 0 {
		t.Fatal("yield returned before peer action")
	}
	before := record
	if AbortOSThreadSuspension(driver, &record, 8) || record != before {
		t.Fatal("foreign abort mutated record")
	}
	if !AbortOSThreadSuspension(driver, &record, 7) || p.osThreadLockOwner != owner.g || p.osThreadSuspensions != nil {
		t.Fatal("rollback")
	}
	runtime.KeepAlive(owner.frame.memory)
	runtime.KeepAlive(peer.frame.memory)
}

func TestOSThreadSuspensionRejectsIssuedAndForeignDriver(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	foreign, _, _ := bindTestExecutorDriver(t, new(P))
	owner := newYieldingTestG(t, "issued-owner")
	peer := newYieldingTestG(t, "issued-peer")
	if !Enqueue(p, owner.g) || !Enqueue(p, peer.g) {
		t.Fatal("enqueue")
	}
	action := commitLockedRunnerYield(t, driver, owner)
	if required, ok := PrepareOSThreadSuspendHandoff(driver, owner.g, action.Kind); !ok || !required {
		t.Fatal("prepare")
	}
	var record OSThreadSuspension
	if DetachOSThreadSuspension(foreign, owner.g, &record, 1) || DetachOSThreadSuspension(driver, owner.g, &record, 0) || record != (OSThreadSuspension{}) {
		t.Fatal("accepted foreign driver or zero owner")
	}
	if !DetachOSThreadSuspension(driver, owner.g, &record, 1) {
		t.Fatal("detach")
	}
	_ = runnerNextPhysicalAction(t, driver, peer, ActionCheckResume)
	if candidate, id, ok := ReadyOSThreadSuspension(driver); ok || candidate != nil || id != 0 {
		t.Fatal("returned across an issued physical action")
	}
	runtime.KeepAlive(owner.frame.memory)
	runtime.KeepAlive(peer.frame.memory)
}

func TestOSThreadSuspensionParkPromotesOnlyThroughSourceReduction(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "cold-park-owner")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue")
	}
	_ = runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, _, _, ok := BeginIssuedExecutorResumeRuntimeContext(driver, task.g)
	if !ok {
		t.Fatal("begin resume")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	if !EnterOSThreadLock(task.g) {
		t.Fatal("lock")
	}
	ticket, ok := BeginParkSetWithDefault(&task.g.park, 0, 91, 701)
	var wait WaitSetRecord
	if !ok || !PrepareWaitSetRecord(&wait, task.g, ticket) || !SealParkSet(&task.g.park, ticket) {
		t.Fatal("park recipe")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(task.g, task.handle, task.frame.header, ticket, &wait) {
		t.Fatal("prepare park")
	}
	action, ok := Resumed(p, task.g, resume)
	if !ok || action.Kind != ActionPark || !CommitExecutorRunAction(driver, task.g, action) {
		t.Fatal("commit park")
	}
	if required, ok := PrepareOSThreadSuspendHandoff(driver, task.g, action.Kind); !ok || !required {
		t.Fatal("prepare handoff")
	}
	var record OSThreadSuspension
	if !DetachOSThreadSuspension(driver, task.g, &record, 9) {
		t.Fatal("detach")
	}
	if ready, _, ok := ReadyOSThreadSuspension(driver); !ok || ready != nil {
		t.Fatal("waiting owner selected")
	}
	for i := 0; i < 64 && !task.g.queued; i++ {
		step, ok := NextExecutorRunStep(driver)
		if !ok || step.Kind != ExecutorRunStepSource {
			t.Fatalf("source reduction %d = %+v %t", i, step, ok)
		}
	}
	if ready, id, ok := ReadyOSThreadSuspension(driver); !ok || ready != &record || id != 9 || wait != (WaitSetRecord{}) {
		t.Fatal("source did not promote original owner")
	}
	if !RestoreOSThreadSuspension(driver, &record, 9) {
		t.Fatal("restore promoted owner")
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestOSThreadSuspensionCorruptCycleFailsClosed(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "cycle-owner")
	peer := newYieldingTestG(t, "cycle-peer")
	if !Enqueue(p, task.g) || !Enqueue(p, peer.g) {
		t.Fatal("enqueue")
	}
	action := commitLockedRunnerYield(t, driver, task)
	if required, ok := PrepareOSThreadSuspendHandoff(driver, task.g, action.Kind); !ok || !required {
		t.Fatal("prepare")
	}
	var record OSThreadSuspension
	if !DetachOSThreadSuspension(driver, task.g, &record, 1) {
		t.Fatal("detach")
	}
	record.next = &record
	if validReadyQueue(p) || suspendedOSThreadTask(p, task.g) != nil {
		t.Fatal("cycle accepted")
	}
	if _, _, ok := ReadyOSThreadSuspension(driver); ok {
		t.Fatal("cycle selected")
	}
	record.next = nil
	if !AbortOSThreadSuspension(driver, &record, 1) {
		t.Fatal("restore fixture")
	}
	runtime.KeepAlive(task.frame.memory)
	runtime.KeepAlive(peer.frame.memory)
}

func TestOSThreadSuspensionRoutedChannelPreservesReadyDebt(t *testing.T) {
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
	task := newYieldingTestG(t, "locked-routed-channel")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue")
	}
	_ = runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, _, _, ok := BeginIssuedExecutorResumeRuntimeContext(driver, task.g)
	if !ok {
		t.Fatal("begin locked channel resume")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	if !EnterOSThreadLock(task.g) {
		t.Fatal("lock")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	storage := new(DirectChannelParkStorageV1)
	if owner, route := PrepareCurrentDirectChannelPark(task.g, task.handle, task.frame.header, storage); owner != driver || route != driver.route {
		t.Fatal("prepare direct park")
	}
	action, committed, ok := ResumedExecutorRun(driver, p, task.g, resume)
	if !ok || !committed || action.Kind != ActionPark {
		t.Fatal("commit direct park")
	}
	if required, ok := PrepareOSThreadSuspendHandoff(driver, task.g, action.Kind); !ok || !required {
		t.Fatal("prepare handoff")
	}
	var record OSThreadSuspension
	if !DetachOSThreadSuspension(driver, task.g, &record, 1) {
		t.Fatal("detach owner")
	}
	if BeginDirectChannelCompletion(&storage.Completion) != DirectChannelCompletionBeginAcquired {
		t.Fatal("claim external effect")
	}
	owner, route, result := FinishDirectChannelCompletionFromCompilerTask(nil, &storage.Completion, 3, 0)
	if owner != driver || route != driver.route || result != DirectChannelCompletionFinishNeedsTarget || !PublishExecutorDirectChannelCompletion(driver, &storage.Completion) {
		t.Fatal("publish external channel completion")
	}
	node, ok := takeExecutorDirectChannelCompletion(driver)
	if !ok || node != &storage.Completion || !CommitDirectChannelCompletion(node, 3) {
		t.Fatal("materialize external channel result")
	}
	if !driver.run.readyDebt || !task.g.queued {
		t.Fatal("external materialization lost logical readiness")
	}
	if !validExecutorDriverHeader(driver) {
		t.Fatal("routed locked channel readiness invalidated driver")
	}
	if nextOSThreadRunnable(p) != nil {
		t.Fatal("logical readiness authorized a foreign M to run locked G")
	}
	if candidate, id, ok := ReadyOSThreadSuspension(driver); !ok || candidate != &record || id != 1 {
		t.Fatal("ready debt did not request original physical owner")
	}
	if !RestoreOSThreadSuspension(driver, &record, 1) || !driver.run.readyDebt {
		t.Fatal("restore lost physical action fairness debt")
	}
	_ = runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	runtime.KeepAlive(task.frame.memory)
}

func newSuspendedLockedChannelOwner(t *testing.T) (*P, *ExecutorDriver, *yieldingTestG, *OSThreadSuspension, *DirectChannelParkStorageV1) {
	t.Helper()
	p := new(P)
	driver, _, _, _ := bindTestExecutorDriverWithManual(t, p)
	task := newYieldingTestG(t, "locked-channel-owner")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue")
	}
	_ = runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, _, _, ok := BeginIssuedExecutorResumeRuntimeContext(driver, task.g)
	if !ok {
		t.Fatal("begin resume")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	if !EnterOSThreadLock(task.g) {
		t.Fatal("lock")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	storage := new(DirectChannelParkStorageV1)
	if owner, route := PrepareCurrentDirectChannelPark(task.g, task.handle, task.frame.header, storage); owner != driver || route != driver.route {
		t.Fatal("prepare direct park")
	}
	action, committed, ok := ResumedExecutorRun(driver, p, task.g, resume)
	if !ok || !committed || action.Kind != ActionPark {
		t.Fatal("commit park")
	}
	if required, ok := PrepareOSThreadSuspendHandoff(driver, task.g, action.Kind); !ok || !required {
		t.Fatal("prepare handoff")
	}
	record := new(OSThreadSuspension)
	if !DetachOSThreadSuspension(driver, task.g, record, 1) {
		t.Fatal("detach")
	}
	return p, driver, task, record, storage
}

func TestOSThreadSuspensionInlineChannelAllowsLockedPeerYield(t *testing.T) {
	p, driver, owner, record, storage := newSuspendedLockedChannelOwner(t)
	peer := newYieldingTestG(t, "independent-locked-producer")
	if !Enqueue(p, peer.g) {
		t.Fatal("enqueue peer")
	}
	_ = runnerNextPhysicalAction(t, driver, peer, ActionCheckResume)
	resume, _, _, ok := BeginIssuedExecutorResumeRuntimeContext(driver, peer.g)
	if !ok {
		t.Fatal("begin producer")
	}
	takeNormalRunnerDecision(t, peer.g)
	peer.frame.header.SuspendReason = uint16(SuspendNone)
	peer.frame.header.Lifecycle = uint16(FrameActive)
	if !EnterOSThreadLock(peer.g) {
		t.Fatal("lock producer")
	}
	if BeginDirectChannelCompletion(&storage.Completion) != DirectChannelCompletionBeginAcquired {
		t.Fatal("claim channel effect")
	}
	if _, _, result := FinishDirectChannelCompletionFromCompilerTask(peer.g, &storage.Completion, 3, 0); result != DirectChannelCompletionFinishInline {
		t.Fatal("materialize same-P result")
	}
	peer.frame.header.SuspendReason = uint16(SuspendYield)
	peer.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(peer.g, peer.handle, peer.frame.header) {
		t.Fatal("prepare producer yield")
	}
	action, committed, ok := ResumedExecutorRun(driver, p, peer.g, resume)
	if !ok || !committed || action.Kind != ActionYield || !driver.run.readyDebt {
		t.Fatal("commit producer action and ready debt")
	}
	if required, ok := PrepareOSThreadSuspendHandoff(driver, peer.g, action.Kind); !ok || !required {
		t.Fatal("prepare producer handoff")
	}
	// The pending physical handoff has no unlocked peer. Its action debt belongs
	// to the older locked M, so driver.Route and quota release must remain valid.
	if !validExecutorDriverHeader(driver) {
		t.Fatal("locked peer handoff invalidated ready debt before quota release")
	}
	var producerRecord OSThreadSuspension
	if !DetachOSThreadSuspension(driver, peer.g, &producerRecord, 2) {
		t.Fatal("detach producer")
	}
	if selected, id, ok := ReadyOSThreadSuspension(driver); !ok || selected != record || id != 1 {
		t.Fatal("handoff lost original receiver")
	}
	if !RestoreOSThreadSuspension(driver, record, 1) {
		t.Fatal("restore receiver")
	}
	runtime.KeepAlive(owner.frame.memory)
	runtime.KeepAlive(peer.frame.memory)
}

func TestOSThreadSuspensionAbortWaitingParkHasNoReadyDebt(t *testing.T) {
	p, driver, owner, record, _ := newSuspendedLockedChannelOwner(t)
	if !AbortOSThreadSuspension(driver, record, 1) {
		t.Fatal("abort before physical claimant")
	}
	if driver.run.readyDebt || !validExecutorDriverHeader(driver) || p.osThreadLockOwner != owner.g || owner.g.queued {
		t.Fatal("aborted waiting owner received an impossible runnable debt")
	}
	runtime.KeepAlive(owner.frame.memory)
}
