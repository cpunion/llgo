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
	"unsafe"
)

type yieldingTestG struct {
	name    string
	g       *G
	frame   *testFrame
	handle  unsafe.Pointer
	resumes int
}

func newYieldingTestG(t *testing.T, name string) *yieldingTestG {
	t.Helper()
	g := new(G)
	if !InitG(g) {
		t.Fatalf("initialize G %s", name)
	}
	handle := unsafe.Pointer(new(byte))
	frame := newTestFrame(t, g, handle, nil)
	if !AdoptRoot(g, handle) {
		t.Fatalf("adopt root for G %s", name)
	}
	return &yieldingTestG{name: name, g: g, frame: frame, handle: handle}
}

// TestSinglePRoundRobinTwoGYield models the exact adapter action protocol with
// two independent stackless frame chains. Each task yields twice. Requeueing
// at the tail must let the other runnable task execute before the yielding
// task's retained LLVM handle is resumed again.
func TestSinglePRoundRobinTwoGYield(t *testing.T) {
	p := new(P)
	a := newYieldingTestG(t, "a")
	b := newYieldingTestG(t, "b")
	tasks := map[*G]*yieldingTestG{a.g: a, b.g: b}
	if !Enqueue(p, a.g) || !Enqueue(p, b.g) {
		t.Fatal("enqueue initial runnable Gs")
	}

	var events []string
	for {
		g, ok := NextRunnable(p)
		if !ok {
			t.Fatal("dequeue rejected without an active scheduler operation")
		}
		if g == nil {
			break
		}
		task := tasks[g]
		if task == nil {
			t.Fatalf("dequeued unknown G %p", g)
		}
		action, ok := BeginRunG(p, g)
		if !ok {
			t.Fatalf("begin run for G %s", task.name)
		}

	runSlice:
		for {
			switch action.Kind {
			case ActionCheckResume:
				action, ok = checkedTestAction(p, g, action, false)
			case ActionResume:
				task.resumes++
				if task.resumes <= 2 {
					events = append(events, fmt.Sprintf("%s:yield:%d", task.name, task.resumes))
					task.frame.header.SuspendReason = uint16(SuspendYield)
					task.frame.header.Lifecycle = uint16(FrameSuspended)
					if !PrepareYield(g, task.handle, task.frame.header) {
						t.Fatalf("prepare yield %d for G %s", task.resumes, task.name)
					}
				} else {
					events = append(events, task.name+":complete")
					task.frame.header.SuspendReason = uint16(SuspendFrameComplete)
					task.frame.header.Lifecycle = uint16(FrameFinalSuspended)
					if !PrepareComplete(g, task.handle, task.frame.header) {
						t.Fatalf("prepare completion for G %s", task.name)
					}
				}
				action, ok = Resumed(p, g, action)
			case ActionCheckDestroy:
				action, ok = Checked(p, g, action, true)
			case ActionDestroy:
				releaseTestFrame(t, g, task.frame)
				action, ok = Destroyed(p, g, action)
			case ActionYield:
				if action.Handle != nil || p.current != nil || g.state != GRunnable || !g.queued ||
					g.active == nil || g.active.handle != task.handle || g.active.state != FrameSuspended {
					t.Fatalf("yielded G %s retained invalid state: action=%+v current=%p state=%d queued=%t active=%p", task.name, action, p.current, g.state, g.queued, g.active)
				}
				break runSlice
			case ActionComplete:
				if g.state != GDead || p.current != nil || g.queued || g.active != nil || g.frames != nil {
					t.Fatalf("completed G %s retained scheduler state", task.name)
				}
				break runSlice
			default:
				t.Fatalf("unexpected action %d for G %s", action.Kind, task.name)
			}
			if !ok {
				t.Fatalf("action protocol failed for G %s at action %d", task.name, action.Kind)
			}
		}
	}

	want := []string{
		"a:yield:1", "b:yield:1",
		"a:yield:2", "b:yield:2",
		"a:complete", "b:complete",
	}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("round-robin events = %v, want %v", events, want)
	}
	if !TerminalG(p, a.g) || !TerminalG(p, b.g) {
		t.Fatal("round-robin run did not consume both Gs")
	}
	runtime.KeepAlive(a.frame.memory)
	runtime.KeepAlive(b.frame.memory)
}

func TestPrepareYieldFailsClosed(t *testing.T) {
	task := newYieldingTestG(t, "yield-validation")
	if PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("yield accepted outside an active resume")
	}
	p := new(P)
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue yield-validation G")
	}
	if next, ok := NextRunnable(p); !ok || next != task.g {
		t.Fatal("dequeue yield-validation G")
	}
	action, ok := BeginRunG(p, task.g)
	if !ok {
		t.Fatal("begin yield-validation G")
	}
	action, ok = checkedTestAction(p, task.g, action, false)
	if !ok || action.Kind != ActionResume {
		t.Fatal("activate yield-validation G")
	}
	task.frame.header.SuspendReason = uint16(SuspendYield)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("valid active yield rejected")
	}
	if PrepareYield(task.g, task.handle, task.frame.header) {
		t.Fatal("duplicate yield transition accepted")
	}
	runtime.KeepAlive(task.frame.memory)
}
