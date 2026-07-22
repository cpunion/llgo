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
	"testing"
	"unsafe"
)

// checkedTestAction models the compiler's normal zero-ticket resume prologue.
// A non-zero decision remains available to tests that exercise exact park or
// task-cancellation delivery explicitly.
func checkedTestAction(p *P, g *G, action Action, done bool) (Action, bool) {
	action, ok := Checked(p, g, action, done)
	if !ok || action.Kind != ActionResume || p.runDecision != (RunDecision{}) {
		return action, ok
	}
	_, _, _, _, ok = TakeRunDecision(g, ParkTicket{})
	return action, ok
}

func takeNormalResumeGateForTest(t *testing.T, g *G) {
	t.Helper()
	outcome, caseID, lease, task, ok := TakeRunDecision(g, ParkTicket{})
	if !ok || outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) || task != TaskCancelNone {
		t.Fatalf("take normal resume gate = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, task, ok)
	}
}

type uncheckedResumeGateFixture struct {
	p      *P
	task   *yieldingTestG
	frame  *Frame
	action Action
}

func newUncheckedResumeGateFixture(t *testing.T, name string) uncheckedResumeGateFixture {
	t.Helper()
	p := new(P)
	task := newYieldingTestG(t, name)
	if !Enqueue(p, task.g) {
		t.Fatalf("enqueue unchecked-resume G %s", name)
	}
	if next, ok := NextRunnable(p); !ok || next != task.g {
		t.Fatalf("dequeue unchecked-resume G %s", name)
	}
	action, ok := BeginRunG(p, task.g)
	if !ok || action.Kind != ActionCheckResume {
		t.Fatalf("begin unchecked-resume G %s = (%+v, %t)", name, action, ok)
	}
	action, ok = Checked(p, task.g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatalf("check unchecked-resume G %s = (%+v, %t)", name, action, ok)
	}
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	if p.runDecision != (RunDecision{}) || p.runDecisionTaken || resumeGateTaken(task.g) {
		t.Fatalf("unchecked-resume G %s unexpectedly passed its gate", name)
	}
	return uncheckedResumeGateFixture{p: p, task: task, frame: FrameFromStorage(task.frame.storage), action: action}
}

func assertResumeGateStillUnchecked(t *testing.T, fixture uncheckedResumeGateFixture) {
	t.Helper()
	if fixture.p.current != fixture.task.g || !fixture.p.inResume || fixture.p.action != fixture.action ||
		fixture.task.g.runP != fixture.p || fixture.task.g.state != GRunning ||
		fixture.p.runDecision != (RunDecision{}) || fixture.p.runDecisionTaken || resumeGateTaken(fixture.task.g) {
		t.Fatalf("unchecked resume gate mutated: current=%p inResume=%t action=%+v state=%d decision=%+v taken=%t",
			fixture.p.current, fixture.p.inResume, fixture.p.action, fixture.task.g.state,
			fixture.p.runDecision, fixture.p.runDecisionTaken)
	}
}

func TestResumeGateRejectsCompilerHooksBeforeAnySideEffect(t *testing.T) {
	t.Run("await", func(t *testing.T) {
		fixture := newUncheckedResumeGateFixture(t, "gate-await")
		childHandle := unsafe.Pointer(new(byte))
		child := newTestFrame(t, fixture.task.g, childHandle, fixture.task.handle)
		fixture.task.frame.header.SuspendReason = uint16(SuspendCall)
		fixture.task.frame.header.Lifecycle = uint16(FrameSuspended)
		beforePending := fixture.task.g.pending
		childFrame := FrameFromStorage(child.storage)
		beforeParentState := fixture.frame.state
		beforeChildState := childFrame.state
		if PrepareAwait(fixture.task.g, fixture.task.handle, childHandle) {
			t.Fatal("await accepted before resume gate take")
		}
		if fixture.task.g.pending != beforePending || childFrame.parent != nil ||
			fixture.frame.state != beforeParentState || childFrame.state != beforeChildState {
			t.Fatal("rejected await mutated frame-chain state")
		}
		assertResumeGateStillUnchecked(t, fixture)
	})

	t.Run("complete", func(t *testing.T) {
		fixture := newUncheckedResumeGateFixture(t, "gate-complete")
		fixture.task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
		fixture.task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
		beforePending := fixture.task.g.pending
		beforeState := fixture.frame.state
		if PrepareComplete(fixture.task.g, fixture.task.handle, fixture.task.frame.header) {
			t.Fatal("completion accepted before resume gate take")
		}
		if fixture.task.g.pending != beforePending || fixture.frame.state != beforeState {
			t.Fatal("rejected completion mutated transition state")
		}
		assertResumeGateStillUnchecked(t, fixture)
	})

	t.Run("yield", func(t *testing.T) {
		fixture := newUncheckedResumeGateFixture(t, "gate-yield")
		fixture.task.frame.header.SuspendReason = uint16(SuspendYield)
		fixture.task.frame.header.Lifecycle = uint16(FrameSuspended)
		beforePending := fixture.task.g.pending
		beforeState := fixture.frame.state
		if PrepareYield(fixture.task.g, fixture.task.handle, fixture.task.frame.header) {
			t.Fatal("yield accepted before resume gate take")
		}
		if fixture.task.g.pending != beforePending || fixture.frame.state != beforeState {
			t.Fatal("rejected yield mutated transition state")
		}
		assertResumeGateStillUnchecked(t, fixture)
	})

	t.Run("park-set", func(t *testing.T) {
		fixture := newUncheckedResumeGateFixture(t, "gate-park-set")
		ticket, ok := BeginParkSet(&fixture.task.g.park, 0, 73)
		if !ok {
			t.Fatal("begin unchecked-resume park set")
		}
		wait := new(WaitSetRecord)
		if !PrepareWaitSetRecord(wait, fixture.task.g, ticket) || !SealParkSet(&fixture.task.g.park, ticket) {
			t.Fatal("prepare unchecked-resume park-set record")
		}
		fixture.task.frame.header.SuspendReason = uint16(SuspendPark)
		fixture.task.frame.header.Lifecycle = uint16(FrameSuspended)
		beforePark := fixture.task.g.park
		beforeWait := *wait
		beforePending := fixture.task.g.pending
		beforeFrameWait := fixture.frame.parkWait
		if PrepareParkSet(fixture.task.g, fixture.task.handle, fixture.task.frame.header, ticket, wait) {
			t.Fatal("park set accepted before resume gate take")
		}
		if fixture.task.g.park != beforePark || fixture.task.g.park.phase != parkSealed ||
			*wait != beforeWait || fixture.task.g.pending != beforePending ||
			fixture.frame.parkWait != beforeFrameWait {
			t.Fatal("rejected park set committed or mutated wait ownership")
		}
		assertResumeGateStillUnchecked(t, fixture)
	})

	t.Run("spawn", func(t *testing.T) {
		fixture := newUncheckedResumeGateFixture(t, "gate-spawn")
		child := new(G)
		beforeChild := *child
		beforeReadyHead, beforeReadyTail := fixture.p.readyHead, fixture.p.readyTail
		if p, ok := runningSpawnContext(fixture.task.g); ok || p != nil || CanBeginSpawn(fixture.task.g) {
			t.Fatal("spawn context accepted before resume gate take")
		}
		if BeginSpawn(fixture.task.g, child, unsafe.Pointer(child), TaskStorageSize()) {
			t.Fatal("spawn begin accepted before resume gate take")
		}
		if *child != beforeChild || fixture.task.g.spawnChild != nil ||
			fixture.p.readyHead != beforeReadyHead || fixture.p.readyTail != beforeReadyTail {
			t.Fatal("rejected spawn published child or queue ownership")
		}
		assertResumeGateStillUnchecked(t, fixture)
	})

	t.Run("explicit-status", func(t *testing.T) {
		fixture := newUncheckedResumeGateFixture(t, "gate-explicit-status")
		fixture.task.frame.header.SuspendReason = uint16(SuspendPanic)
		fixture.task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
		beforeRecord := fixture.task.g.panicRecord
		beforePending := fixture.task.g.pending
		typeWord, dataWord := new(byte), new(byte)
		if PrepareExplicitStatus(fixture.task.g, fixture.task.handle, fixture.task.frame.header,
			ExplicitStatusPanic, unsafe.Pointer(typeWord), unsafe.Pointer(dataWord)) {
			t.Fatal("explicit status accepted before resume gate take")
		}
		if fixture.task.g.panicRecord != beforeRecord || !emptyPanicRecord(&fixture.task.g.panicRecord) ||
			fixture.task.g.pending != beforePending {
			t.Fatal("rejected explicit status poisoned or published panic state")
		}
		assertResumeGateStillUnchecked(t, fixture)
	})

	t.Run("preempt", func(t *testing.T) {
		fixture := newUncheckedResumeGateFixture(t, "gate-preempt")
		if !RequestPreempt(fixture.task.g) {
			t.Fatal("request unchecked-resume preemption")
		}
		beforePreempt := preemptLoad(preemptAddress(fixture.task.g))
		beforeSchedule := preemptLoad(&fixture.p.schedule)
		beforeBudget := fixture.p.servicePreemptBudget
		if PollPreempt(fixture.task.g) {
			t.Fatal("preemption poll accepted before resume gate take")
		}
		if preemptLoad(preemptAddress(fixture.task.g)) != beforePreempt || beforePreempt != preemptRequested ||
			preemptLoad(&fixture.p.schedule) != beforeSchedule || fixture.p.servicePreemptBudget != beforeBudget {
			t.Fatal("rejected preemption poll consumed a request or service budget")
		}
		assertResumeGateStillUnchecked(t, fixture)
	})

	t.Run("resumed", func(t *testing.T) {
		fixture := newUncheckedResumeGateFixture(t, "gate-resumed")
		fixture.task.frame.header.SuspendReason = uint16(SuspendYield)
		fixture.task.frame.header.Lifecycle = uint16(FrameSuspended)
		fixture.task.g.pending = pendingTransition{kind: pendingYield, from: fixture.frame}
		beforePending := fixture.task.g.pending
		beforeFrameState := fixture.frame.state
		if action, ok := Resumed(fixture.p, fixture.task.g, fixture.action); ok || action != (Action{}) {
			t.Fatalf("resume return accepted before gate take = (%+v, %t)", action, ok)
		}
		if fixture.task.g.pending != beforePending || fixture.frame.state != beforeFrameState ||
			fixture.task.g.state != GRunning || !fixture.p.inResume {
			t.Fatal("rejected resume return committed its pending transition")
		}
		assertResumeGateStillUnchecked(t, fixture)
	})
}

func TestResumeGateZeroDecisionIsExactAndExactlyOnce(t *testing.T) {
	fixture := newUncheckedResumeGateFixture(t, "gate-zero-decision")
	wrong := ParkTicket{epoch: 1, generation: 1}
	if outcome, caseID, lease, task, ok := TakeRunDecision(fixture.task.g, wrong); ok ||
		outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) || task != TaskCancelNone {
		t.Fatalf("zero decision accepted nonzero ticket = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, task, ok)
	}
	assertResumeGateStillUnchecked(t, fixture)
	takeNormalResumeGateForTest(t, fixture.task.g)
	if !resumeGateTaken(fixture.task.g) {
		t.Fatal("exact zero-ticket take did not open resume gate")
	}
	if outcome, caseID, lease, task, ok := TakeRunDecision(fixture.task.g, ParkTicket{}); ok ||
		outcome != ParkOutcomePending || caseID != 0 || lease != (OperationResultLease{}) || task != TaskCancelNone {
		t.Fatalf("zero decision replay = (%d, %d, %+v, %d, %t)", outcome, caseID, lease, task, ok)
	}
	if !resumeGateTaken(fixture.task.g) {
		t.Fatal("replayed zero-ticket take corrupted the consumed gate")
	}
}
