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

func TestExecutorResumeHandoffRequestAfterReplacementAdmission(t *testing.T) {
	p := new(P)
	driver, registry, executor := bindTestExecutorDriver(t, p)
	parent := newYieldingTestG(t, "request-parent")
	peer := newYieldingTestG(t, "request-replacement")
	fixture := beginExecutorResumeHandoffFixture(t, p, driver, parent)
	if pending, ok := ExecutorResumeHandoffRequestPending(&fixture.handoff); !ok || pending {
		t.Fatalf("initial request = (%t, %t)", pending, ok)
	}
	if !Enqueue(p, peer.g) {
		t.Fatal("enqueue replacement")
	}
	if required, ok := ExecutorResumeHandoffCompensationRequired(&fixture.handoff); !ok || !required {
		t.Fatalf("pre-admission demand = (%t, %t)", required, ok)
	}
	step := runnerNextPhysicalAction(t, driver, peer, ActionCheckResume)
	if _, ok := ExecutorResumeHandoffCompensationRequired(&fixture.handoff); ok {
		t.Fatal("mutable demand inspection accepted an issued replacement action")
	}
	beforeP, beforeRun := *p, driver.run
	if pending, ok := ExecutorResumeHandoffRequestPending(&fixture.handoff); !ok || pending {
		t.Fatalf("active replacement request = (%t, %t)", pending, ok)
	}
	if result := registry.Request(executor); result != ExecutorRequestPublished {
		t.Fatalf("request before Arm = %d", result)
	}
	if pending, ok := ExecutorResumeHandoffRequestPending(&fixture.handoff); !ok || !pending {
		t.Fatalf("sticky pre-Arm request = (%t, %t)", pending, ok)
	}
	if *p != beforeP || driver.run != beforeRun {
		t.Fatal("request observation changed replacement scheduler state")
	}
	runnerYieldAction(t, driver, step, peer)
	fixture.restore(t)
	if _, ok := ExecutorResumeHandoffRequestPending(&fixture.handoff); ok {
		t.Fatal("request observation accepted a consumed handoff")
	}
	runtime.KeepAlive(parent.frame.memory)
	runtime.KeepAlive(peer.frame.memory)
}

func TestExecutorResumeHandoffRequestDoesNotReadReplacementState(t *testing.T) {
	p := new(P)
	driver, registry, executor := bindTestExecutorDriver(t, p)
	parent := newYieldingTestG(t, "concurrent-request-parent")
	fixture := beginExecutorResumeHandoffFixture(t, p, driver, parent)
	if result := registry.Request(executor); result != ExecutorRequestPublished {
		t.Fatalf("publish request = %d", result)
	}
	// Model fields owned exclusively by the admitted replacement. Race builds
	// must also reject any future attempt to read these in the parent observer.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 10000; i++ {
			p.inResume = i&1 != 0
			p.current = parent.g
			p.action.Kind = ActionResume
			p.externalWaitCount = uint32(i)
			driver.run.sourceMore = i&1 != 0
			driver.run.issued = ActionCheckResume
			if i&63 == 0 {
				runtime.Gosched()
			}
		}
	}()
	for i := 0; i < 10000; i++ {
		if pending, ok := ExecutorResumeHandoffRequestPending(&fixture.handoff); !ok || !pending {
			t.Errorf("concurrent request = (%t, %t)", pending, ok)
			break
		}
	}
	<-done
	p.inResume, p.current, p.action, p.externalWaitCount = false, nil, Action{}, 0
	driver.run.sourceMore, driver.run.issued = false, ActionInvalid
	fixture.restore(t)
	runtime.KeepAlive(parent.frame.memory)
}

func TestExecutorResumeHandoffRequestPreservesAtomicSchedule(t *testing.T) {
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	parent := newYieldingTestG(t, "schedule-request-parent")
	fixture := beginExecutorResumeHandoffFixture(t, p, driver, parent)
	preemptStore(&p.schedule, scheduleRequested)
	if pending, ok := ExecutorResumeHandoffRequestPending(&fixture.handoff); !ok || !pending {
		t.Fatalf("schedule request = (%t, %t)", pending, ok)
	}
	if preemptLoad(&p.schedule) != scheduleRequested {
		t.Fatal("request observation acknowledged schedule")
	}
	preemptStore(&p.schedule, scheduleIdle)
	fixture.restore(t)
	runtime.KeepAlive(parent.frame.memory)
}
