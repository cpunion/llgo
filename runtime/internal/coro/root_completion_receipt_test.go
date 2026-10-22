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

func prepareRootCompletionReceiptTest(t *testing.T, compiler bool) (*P, *ExecutorDriver, *yieldingTestG, Action) {
	t.Helper()
	p := new(P)
	driver, _, _ := bindTestExecutorDriver(t, p)
	task := newYieldingTestG(t, "root-receipt")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue")
	}
	_ = runnerNextPhysicalAction(t, driver, task, ActionCheckResume)
	resume, _, _, ok := BeginIssuedExecutorResumeRuntimeContext(driver, task.g)
	if !ok {
		t.Fatal("resume")
	}
	takeNormalRunnerDecision(t, task.g)
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if compiler {
		ok = PrepareCompleteStatusCompiler(task.g, task.handle, task.frame.header, CompletionReturn)
	} else {
		ok = PrepareComplete(task.g, task.handle, task.frame.header)
	}
	if !ok {
		t.Fatal("prepare completion")
	}
	return p, driver, task, resume
}
func TestCompilerRootCompletionCarriesIssuedReceipt(t *testing.T) {
	p, driver, task, resume := prepareRootCompletionReceiptTest(t, true)
	if task.g.pending.kind != pendingCompilerRootComplete {
		t.Fatalf("compiler return did not publish root receipt, got%d", task.g.pending.kind)
	}
	next, committed, ok := ResumedExecutorRun(driver, p, task.g, resume)
	if !ok || committed || next.Kind != ActionCheckDestroy || next.Handle != task.handle || task.g.pending != (pendingTransition{}) || task.g.active != nil || task.g.destroyTarget != FrameFromStorage(task.frame.storage) || !task.g.destroyRoot || p.inResume || p.runDecisionTaken || task.g.state != GDispatching {
		t.Fatalf("root return did not close exact issued episode: %+v %t %t", next, committed, ok)
	}
	if !PrepareIssuedExecutorDestroyAfterResume(driver, task.g, next) {
		t.Fatal("root receipt cannot enter existing done/destroy gate")
	}
}
func TestCompilerRootCompletionCompatibilityKeepsGenericValidation(t *testing.T) {
	p, _, task, resume := prepareRootCompletionReceiptTest(t, false)
	if task.g.pending.kind != pendingComplete {
		t.Fatal("generic completion acquired private receipt")
	}
	if next, ok := Resumed(p, task.g, resume); !ok || next.Kind != ActionCheckDestroy {
		t.Fatalf("generic completion changed: %+v %t", next, ok)
	}
	p, _, task, resume = prepareRootCompletionReceiptTest(t, true)
	if next, ok := Resumed(p, task.g, resume); !ok || next.Kind != ActionCheckDestroy {
		t.Fatalf("generic consumer rejected compiler receipt: %+v %t", next, ok)
	}
}
func TestCompilerRootCompletionRejectsChangedIssuedOwner(t *testing.T) {
	for _, kind := range []string{"wrong-P", "wrong-G", "wrong-action", "wrong-issue", "inline-depth", "not-running", "no-resume", "active-frame", "pending-target"} {
		t.Run(kind, func(t *testing.T) {
			p, driver, task, resume := prepareRootCompletionReceiptTest(t, true)
			switch kind {
			case "wrong-P":
				driver.p = new(P)
			case "wrong-G":
				p.current = new(G)
			case "wrong-action":
				resume.Handle = unsafe.Pointer(new(byte))
			case "wrong-issue":
				driver.run.issued = ActionCheckDestroy
			case "inline-depth":
				p.inlineAwaitDepth = 1
			case "not-running":
				task.g.state = GWaiting
			case "no-resume":
				p.inResume = false
			case "active-frame":
				task.g.active = nil
			case "pending-target":
				task.g.pending.target = FrameFromStorage(task.frame.storage)
			}
			beforeP, beforeG, beforeFrame := *p, *task.g, *FrameFromStorage(task.frame.storage)
			next, committed, ok := ResumedExecutorRun(driver, p, task.g, resume)
			if ok || committed || next != (Action{}) || *p != beforeP || *task.g != beforeG || *FrameFromStorage(task.frame.storage) != beforeFrame {
				t.Fatalf("changed issued root accepted or mutated: %+v %t %t", next, committed, ok)
			}
		})
	}
}
