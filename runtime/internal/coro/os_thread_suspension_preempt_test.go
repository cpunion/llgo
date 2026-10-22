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

// The sole unlocked peer may compute forever while a detached yielding owner
// waits. It must cross a bounded service checkpoint even with no source wait,
// external request, or other physically selectable runnable.
func TestOSThreadSuspensionPreemptsSoleComputePeer(t *testing.T) {
	for _, compiler := range []bool{false, true} {
		name := "defensive"
		if compiler {
			name = "compiler"
		}
		t.Run(name, func(t *testing.T) {
			p := new(P)
			driver, _, _ := bindTestExecutorDriver(t, p)
			owner := newYieldingTestG(t, "cold-compute-owner")
			peer := newYieldingTestG(t, "cold-compute-peer")
			if !Enqueue(p, owner.g) || !Enqueue(p, peer.g) {
				t.Fatal("enqueue")
			}
			action := commitLockedRunnerYield(t, driver, owner)
			if required, ok := PrepareOSThreadSuspendHandoff(driver, owner.g, action.Kind); !ok || !required {
				t.Fatal("prepare locked handoff")
			}
			var record OSThreadSuspension
			if !DetachOSThreadSuspension(driver, owner.g, &record, 71) {
				t.Fatal("detach cold owner")
			}
			step := runnerNextPhysicalAction(t, driver, peer, ActionCheckResume)
			resume, ok := Checked(p, peer.g, step.Action, false)
			if !ok || resume.Kind != ActionResume {
				t.Fatal("begin compute peer")
			}
			takeNormalRunnerDecision(t, peer.g)
			peer.frame.header.SuspendReason = uint16(SuspendNone)
			peer.frame.header.Lifecycle = uint16(FrameActive)
			if HasWaiting(p) || runnableForOSThreadOwner(p) || registryRequestPendingForColdTest(driver) {
				t.Fatal("fixture has another service cause")
			}
			if compiler {
				consumeServicePreemptTestBudget(t, p, peer.g, true)
			} else {
				yielded := false
				for count := uint32(0); count < servicePreemptSafepointBudget/preemptCheckpointStride; count++ {
					if PollPreempt(peer.g) {
						yielded = true
						break
					}
				}
				if !yielded {
					t.Fatal("cold locked owner lost bounded compute-peer preemption")
				}
			}
			peer.frame.header.SuspendReason = uint16(SuspendYield)
			peer.frame.header.Lifecycle = uint16(FrameSuspended)
			if !PrepareYield(peer.g, peer.handle, peer.frame.header) {
				t.Fatal("prepare peer yield")
			}
			next, resumed := Resumed(p, peer.g, resume)
			if !resumed || next.Kind != ActionYield || !CommitExecutorRunAction(driver, peer.g, next) {
				t.Fatal("commit compute-peer yield")
			}
			ready, identity, valid := ReadyOSThreadSuspension(driver)
			if !valid || ready != &record || identity != 71 || !RestoreOSThreadSuspension(driver, ready, identity) {
				t.Fatal("peer service did not restore exact cold owner")
			}
			runtime.KeepAlive(owner.frame.memory)
			runtime.KeepAlive(peer.frame.memory)
		})
	}
}

func registryRequestPendingForColdTest(driver *ExecutorDriver) bool {
	return driver == nil || driver.requestGate == nil || preemptLoad(driver.requestGate)&executorGateRequested != 0
}

func TestOSThreadSuspensionPreemptsAttachedOwnerForReadyPeer(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	owner := newYieldingTestG(t, "attached-compute")
	peer := newYieldingTestG(t, "attached-peer")
	if !Enqueue(p, owner.g) {
		t.Fatal("enqueue owner")
	}
	step := runnerNextPhysicalAction(t, driver, owner, ActionCheckResume)
	_, ok := Checked(p, owner.g, step.Action, false)
	if !ok {
		t.Fatal("begin owner")
	}
	takeNormalRunnerDecision(t, owner.g)
	owner.frame.header.SuspendReason = uint16(SuspendNone)
	owner.frame.header.Lifecycle = uint16(FrameActive)
	if !EnterOSThreadLock(owner.g) || !Enqueue(p, peer.g) {
		t.Fatal("lock/enqueue")
	}
	if HasWaiting(p) || runnableForOSThreadOwner(p) || registryRequestPendingForColdTest(driver) {
		t.Fatal("unexpected service cause")
	}
	consumeServicePreemptTestBudget(t, p, owner.g, true)
	runtime.KeepAlive(owner.frame.memory)
	runtime.KeepAlive(peer.frame.memory)
}

func TestOSThreadSuspensionPreemptsForReadyColdOwner(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	owner := newYieldingTestG(t, "serviced-cold-owner")
	peer := newYieldingTestG(t, "serviced-cold-peer")
	if !Enqueue(p, owner.g) || !Enqueue(p, peer.g) {
		t.Fatal("enqueue")
	}
	action := commitLockedRunnerYield(t, driver, owner)
	if required, ok := PrepareOSThreadSuspendHandoff(driver, owner.g, action.Kind); !ok || !required {
		t.Fatal("prepare")
	}
	var record OSThreadSuspension
	if !DetachOSThreadSuspension(driver, owner.g, &record, 72) {
		t.Fatal("detach")
	}
	step := runnerNextPhysicalAction(t, driver, peer, ActionCheckResume)
	resume, ok := Checked(p, peer.g, step.Action, false)
	if !ok {
		t.Fatal("begin first peer action")
	}
	takeNormalRunnerDecision(t, peer.g)
	peer.frame.header.SuspendReason = uint16(SuspendNone)
	peer.frame.header.Lifecycle = uint16(FrameActive)
	yieldRunningDriverTask(t, p, peer, resume)
	if !CommitExecutorRunAction(driver, peer.g, Action{Kind: ActionYield}) {
		t.Fatal("commit peer service")
	}
	if ready, _, ok := ReadyOSThreadSuspension(driver); !ok || ready != &record {
		t.Fatal("cold owner not ready")
	}
	step = runnerNextPhysicalAction(t, driver, peer, ActionCheckResume)
	_, ok = Checked(p, peer.g, step.Action, false)
	if !ok {
		t.Fatal("begin sole compute peer")
	}
	takeNormalRunnerDecision(t, peer.g)
	peer.frame.header.SuspendReason = uint16(SuspendNone)
	peer.frame.header.Lifecycle = uint16(FrameActive)
	if HasWaiting(p) || runnableForOSThreadOwner(p) || registryRequestPendingForColdTest(driver) {
		t.Fatal("unexpected service cause")
	}
	consumeServicePreemptTestBudget(t, p, peer.g, true)
	runtime.KeepAlive(owner.frame.memory)
	runtime.KeepAlive(peer.frame.memory)
}
