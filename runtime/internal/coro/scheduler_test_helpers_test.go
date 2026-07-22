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

func beginWaitTestResumeWithoutGate(t *testing.T, p *P, task *yieldingTestG) Action {
	t.Helper()
	action, ok := BeginRunG(p, task.g)
	if !ok || action.Kind != ActionCheckResume {
		t.Fatalf("begin G %s = (%+v, %t)", task.name, action, ok)
	}
	action, ok = Checked(p, task.g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatalf("activate G %s = (%+v, %t)", task.name, action, ok)
	}
	task.frame.header.SuspendReason = uint16(SuspendNone)
	task.frame.header.Lifecycle = uint16(FrameActive)
	return action
}

func beginWaitTestResume(t *testing.T, p *P, task *yieldingTestG) Action {
	t.Helper()
	action := beginWaitTestResumeWithoutGate(t, p, task)
	if p.runDecision == (RunDecision{}) {
		takeNormalResumeGateForTest(t, task.g)
	}
	return action
}

func finishWaitTestTask(t *testing.T, p *P, task *yieldingTestG, action Action) {
	t.Helper()
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatalf("prepare completion for G %s", task.name)
	}
	action, ok := Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume completion for G %s = (%+v, %t)", task.name, action, ok)
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check destroy for G %s = (%+v, %t)", task.name, action, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	action, ok = Destroyed(p, task.g, action)
	if !ok || action.Kind != ActionComplete {
		t.Fatalf("destroy G %s = (%+v, %t)", task.name, action, ok)
	}
}

func prepareWaitTestRootDestroy(t *testing.T, p *P, task *yieldingTestG, action Action) Action {
	t.Helper()
	task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
	task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
	if !PrepareComplete(task.g, task.handle, task.frame.header) {
		t.Fatal("prepare root completion")
	}
	action, ok := Resumed(p, task.g, action)
	if !ok || action.Kind != ActionCheckDestroy {
		t.Fatalf("resume root completion = (%+v, %t)", action, ok)
	}
	action, ok = Checked(p, task.g, action, true)
	if !ok || action.Kind != ActionDestroy {
		t.Fatalf("check root destroy = (%+v, %t)", action, ok)
	}
	releaseTestFrame(t, task.g, task.frame)
	return action
}
