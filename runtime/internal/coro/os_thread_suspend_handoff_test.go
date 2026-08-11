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
)

func commitLockedRunnerYield(
	t *testing.T,
	driver *ExecutorDriver,
	task *yieldingTestG,
) Action {
	t.Helper()
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, ok := Checked(driver.p, task.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check locked yield resume")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	if !EnterOSThreadLock(task.g) {
		t.Fatal("enter locked yield owner")
	}
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare locked yield")
	}
	next, ok := Resumed(driver.p, task.g, resume)
	if !ok || next.Kind != ActionYield ||
		!CommitExecutorRunAction(driver, task.g, next) {
		t.Fatalf("commit locked yield = (%+v, %t)", next, ok)
	}
	return next
}

func TestOSThreadYieldHandoffRunsOnePeerAndRestoresOwnerFIFO(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	owner := newYieldingTestG(t, "locked-yield-owner")
	first := newYieldingTestG(t, "locked-yield-first")
	second := newYieldingTestG(t, "locked-yield-second")
	if !Enqueue(p, owner.g) || !Enqueue(p, first.g) || !Enqueue(p, second.g) {
		t.Fatal("enqueue locked-yield fixture")
	}
	kind := commitLockedRunnerYield(t, driver, owner)
	required, ok := PrepareOSThreadSuspendHandoff(driver, owner.g, kind.Kind)
	if !ok || !required {
		t.Fatalf("prepare locked-yield handoff = (%t, %t)", required, ok)
	}
	if p.readyHead != first.g || first.g.nextReady != second.g ||
		second.g.nextReady != owner.g || p.readyTail != owner.g {
		t.Fatal("locked-yield detach reordered ready FIFO")
	}
	if detached, returnable, valid := OSThreadSuspendHandoffStatus(driver); !valid ||
		!detached || returnable {
		t.Fatalf("initial locked-yield status = (%t, %t, %t)",
			detached, returnable, valid)
	}

	step := runnerNextPhysicalAction(t, driver, first, ActionCheckResume)
	if detached, returnable, valid := OSThreadSuspendHandoffStatus(driver); !valid ||
		!detached || returnable {
		t.Fatalf("issued-peer locked-yield status = (%t, %t, %t)",
			detached, returnable, valid)
	}
	runnerYieldAction(t, driver, step, first)
	if required, prepared := PrepareOSThreadSuspendHandoff(
		driver, first.g, ActionYield,
	); !prepared || required {
		t.Fatalf("detached unlocked peer handoff = (%t, %t)", required, prepared)
	}
	if detached, returnable, valid := OSThreadSuspendHandoffStatus(driver); !valid ||
		!detached || !returnable {
		t.Fatalf("serviced locked-yield status = (%t, %t, %t)",
			detached, returnable, valid)
	}
	if !RestoreOSThreadSuspendHandoff(driver, owner.g) {
		t.Fatal("restore locked-yield owner")
	}
	_ = runnerNextPhysicalAction(t, driver, owner, ActionCheckResume)
	if p.readyHead != second.g || second.g.nextReady != first.g ||
		p.readyTail != first.g {
		t.Fatal("restored locked-yield owner reordered peers")
	}
	runtime.KeepAlive(owner.frame.memory)
	runtime.KeepAlive(first.frame.memory)
	runtime.KeepAlive(second.frame.memory)
}

func TestOSThreadSuspendHandoffUnlockedActionIsNoop(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "unlocked-yield")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue unlocked-yield fixture")
	}
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	runnerYieldAction(t, driver, step, task)
	if required, prepared := PrepareOSThreadSuspendHandoff(
		driver, task.g, ActionYield,
	); !prepared || required {
		t.Fatalf("unlocked-yield handoff = (%t, %t)", required, prepared)
	}
	if detached, returnable, valid := OSThreadSuspendHandoffStatus(driver); !valid ||
		detached || returnable {
		t.Fatalf("unlocked-yield status = (%t, %t, %t)",
			detached, returnable, valid)
	}
	runtime.KeepAlive(task.frame.memory)
}

func TestOSThreadSuspendHandoffUnlockedGateIsConstantTime(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "unlocked-fast-handoff")
	peers := []*yieldingTestG{
		newYieldingTestG(t, "unlocked-fast-first"),
		newYieldingTestG(t, "unlocked-fast-middle"),
		newYieldingTestG(t, "unlocked-fast-last"),
	}
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue unlocked fast-handoff task")
	}
	for _, peer := range peers {
		if !Enqueue(p, peer.g) {
			t.Fatalf("enqueue unlocked fast-handoff peer %s", peer.name)
		}
	}
	step := runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	runnerYieldAction(t, driver, step, task)

	// Preserve every owner-maintained endpoint while corrupting only unrelated
	// payloads. Full diagnostics must find both defects; the ordinary unlocked
	// handoff gate must inspect only its exact completed task and O(1) headers.
	peerState := peers[1].g.state
	peers[1].g.state = GDead
	var firstWait, lastWait WaitSetRecord
	firstWait.activeNext = &lastWait
	lastWait.activePrev = &firstWait
	p.parkWaitHead, p.parkWaitTail = &firstWait, &lastWait
	if validReadyQueue(p) || validSchedulerWaitQueues(p) {
		t.Fatal("full diagnostics accepted corrupt distant handoff payloads")
	}
	if required, prepared := PrepareOSThreadSuspendHandoff(
		driver, task.g, ActionYield,
	); !prepared || required {
		t.Fatalf("unlocked fast handoff = (%t, %t)", required, prepared)
	}
	peers[1].g.state = peerState
	p.parkWaitHead, p.parkWaitTail = nil, nil

	// Local endpoint corruption remains visible without walking either queue.
	p.readyTail.nextReady = p.readyTail
	if required, prepared := PrepareOSThreadSuspendHandoff(
		driver, task.g, ActionYield,
	); prepared || required {
		t.Fatalf("unlocked handoff accepted corrupt ready tail = (%t, %t)", required, prepared)
	}
	p.readyTail.nextReady = nil
	p.parkWaitHead = &firstWait
	if required, prepared := PrepareOSThreadSuspendHandoff(
		driver, task.g, ActionYield,
	); prepared || required {
		t.Fatalf("unlocked handoff accepted mismatched wait endpoints = (%t, %t)", required, prepared)
	}
	p.parkWaitHead = nil
	if required, prepared := PrepareOSThreadSuspendHandoff(
		driver, task.g, ActionYield,
	); !prepared || required {
		t.Fatalf("restored unlocked handoff = (%t, %t)", required, prepared)
	}

	runtime.KeepAlive(task.frame.memory)
	for _, peer := range peers {
		runtime.KeepAlive(peer.frame.memory)
	}
}

func TestOSThreadYieldWithoutPeerStaysAttached(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	owner := newYieldingTestG(t, "locked-yield-no-peer")
	if !Enqueue(p, owner.g) {
		t.Fatal("enqueue locked-yield no-peer owner")
	}
	kind := commitLockedRunnerYield(t, driver, owner)
	required, ok := PrepareOSThreadSuspendHandoff(driver, owner.g, kind.Kind)
	if !ok || required {
		t.Fatalf("no-peer locked yield handoff = (%t, %t)", required, ok)
	}
	if detached, returnable, valid := OSThreadSuspendHandoffStatus(driver); !valid ||
		detached || returnable {
		t.Fatalf("no-peer locked-yield status = (%t, %t, %t)",
			detached, returnable, valid)
	}
	_ = runnerNextPhysicalAction(t, driver, owner, ActionCheckResume)
	runtime.KeepAlive(owner.frame.memory)
}

func TestOSThreadParkHandoffReturnsAfterSourcePromotion(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	owner := newYieldingTestG(t, "locked-park-owner")
	if !Enqueue(p, owner.g) {
		t.Fatal("enqueue locked-park owner")
	}
	step := runnerNextPhysicalAction(t, driver, owner, ActionCheckResume)
	resume, ok := Checked(p, owner.g, step.Action, false)
	if !ok || resume.Kind != ActionResume {
		t.Fatal("check locked-park resume")
	}
	takeNormalRunnerDecision(t, owner.g)
	owner.frame.header.SuspendReason = uint16(SuspendNone)
	owner.frame.header.Lifecycle = uint16(FrameActive)
	if !EnterOSThreadLock(owner.g) {
		t.Fatal("enter locked-park owner")
	}
	ticket, begun := BeginParkSetWithDefault(&owner.g.park, 0, 91, 701)
	var wait WaitSetRecord
	if !begun || !PrepareWaitSetRecord(&wait, owner.g, ticket) ||
		!SealParkSet(&owner.g.park, ticket) {
		t.Fatal("prepare locked-park default wait")
	}
	owner.frame.header.SuspendReason = uint16(SuspendPark)
	owner.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(owner.g, owner.handle, owner.frame.header, ticket, &wait) {
		t.Fatal("commit locked-park wait")
	}
	parked, resumed := Resumed(p, owner.g, resume)
	if !resumed || parked.Kind != ActionPark ||
		!CommitExecutorRunAction(driver, owner.g, parked) {
		t.Fatalf("commit locked park = (%+v, %t)", parked, resumed)
	}
	required, prepared := PrepareOSThreadSuspendHandoff(
		driver, owner.g, parked.Kind,
	)
	if !prepared || !required {
		t.Fatalf("prepare locked-park handoff = (%t, %t)", required, prepared)
	}

	for reduction := 0; reduction < 64; reduction++ {
		detached, returnable, valid := OSThreadSuspendHandoffStatus(driver)
		if !valid || !detached {
			t.Fatalf("locked-park status %d = (%t, %t, %t)",
				reduction, detached, returnable, valid)
		}
		if returnable {
			break
		}
		next, nextOK := NextExecutorRunStep(driver)
		if !nextOK || next.Kind != ExecutorRunStepSource {
			t.Fatalf("locked-park source reduction %d = (%+v, %t)",
				reduction, next, nextOK)
		}
	}
	if detached, returnable, valid := OSThreadSuspendHandoffStatus(driver); !valid ||
		!detached || !returnable || !owner.g.queued || wait != (WaitSetRecord{}) {
		t.Fatalf("promoted locked-park status = (%t, %t, %t), queued=%t wait=%+v",
			detached, returnable, valid, owner.g.queued, wait)
	}
	if !RestoreOSThreadSuspendHandoff(driver, owner.g) {
		t.Fatal("restore locked-park owner")
	}
	_ = runnerNextPhysicalAction(t, driver, owner, ActionCheckResume)
	runtime.KeepAlive(owner.frame.memory)
}

func TestOSThreadSuspendHandoffAbortBeforeClaim(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	owner := newYieldingTestG(t, "locked-yield-abort-owner")
	peer := newYieldingTestG(t, "locked-yield-abort-peer")
	if !Enqueue(p, owner.g) || !Enqueue(p, peer.g) {
		t.Fatal("enqueue locked-yield abort fixture")
	}
	kind := commitLockedRunnerYield(t, driver, owner)
	if required, ok := PrepareOSThreadSuspendHandoff(driver, owner.g, kind.Kind); !ok || !required {
		t.Fatal("prepare locked-yield abort")
	}
	if !AbortOSThreadSuspendHandoff(driver, owner.g) {
		t.Fatal("abort locked-yield before claim")
	}
	_ = runnerNextPhysicalAction(t, driver, owner, ActionCheckResume)
	runtime.KeepAlive(owner.frame.memory)
	runtime.KeepAlive(peer.frame.memory)
}
