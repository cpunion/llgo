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
	"sync"
	"testing"
)

func TestDeferredExecutorHandoffWithdraw(t *testing.T) {
	var handoff DeferredExecutorHandoff
	if !handoff.Idle() || !handoff.Arm(17) || handoff.Arm(18) {
		t.Fatal("arm deferred executor handoff")
	}
	if slot, phase, ok := handoff.Observe(); !ok || slot != 17 || phase != DeferredExecutorHandoffArmed {
		t.Fatalf("armed snapshot = (%d, %d, %t)", slot, phase, ok)
	}
	if handoff.Withdraw(18) || !handoff.Withdraw(17) || !handoff.Idle() {
		t.Fatal("withdraw deferred executor handoff")
	}
}

func TestDeferredExecutorHandoffStartOutcomes(t *testing.T) {
	for _, queued := range []bool{false, true} {
		var handoff DeferredExecutorHandoff
		if !handoff.Arm(23) {
			t.Fatal("arm deferred executor handoff")
		}
		slot, begun := handoff.BeginStart()
		if !begun || slot != 23 || handoff.Withdraw(23) ||
			!handoff.PublishStart(slot, queued) {
			t.Fatalf("publish deferred start queued=%t", queued)
		}
		want := DeferredExecutorHandoffStarted
		if queued {
			want = DeferredExecutorHandoffQueued
		}
		if gotSlot, phase, ok := handoff.Observe(); !ok || gotSlot != slot || phase != want {
			t.Fatalf("started snapshot queued=%t = (%d, %d, %t)", queued, gotSlot, phase, ok)
		}
		if !handoff.Complete(slot) || !handoff.Idle() || handoff.Complete(slot) {
			t.Fatalf("complete deferred start queued=%t", queued)
		}
	}
}

func TestDeferredExecutorHandoffRetry(t *testing.T) {
	var handoff DeferredExecutorHandoff
	if !handoff.Arm(29) {
		t.Fatal("arm deferred executor handoff")
	}
	slot, begun := handoff.BeginStart()
	if !begun || slot != 29 || !handoff.RetryStart(slot) {
		t.Fatal("retry deferred executor start")
	}
	if !handoff.Withdraw(slot) || !handoff.Idle() {
		t.Fatal("withdraw retried deferred executor handoff")
	}
}

func TestDeferredExecutorHandoffStartWithdrawRace(t *testing.T) {
	const iterations = 2_000
	for iteration := 0; iteration < iterations; iteration++ {
		var handoff DeferredExecutorHandoff
		if !handoff.Arm(31) {
			t.Fatal("arm deferred executor handoff")
		}
		var wait sync.WaitGroup
		wait.Add(2)
		started := make(chan bool, 1)
		withdrawn := make(chan bool, 1)
		go func() {
			defer wait.Done()
			slot, ok := handoff.BeginStart()
			if ok && !handoff.PublishStart(slot, true) {
				t.Errorf("publish winning start at iteration %d", iteration)
			}
			started <- ok
		}()
		go func() {
			defer wait.Done()
			withdrawn <- handoff.Withdraw(31)
		}()
		wait.Wait()
		startWon, withdrawWon := <-started, <-withdrawn
		if startWon == withdrawWon {
			t.Fatalf("race winners at iteration %d = start:%t withdraw:%t", iteration, startWon, withdrawWon)
		}
		if startWon {
			if !handoff.Complete(31) {
				t.Fatalf("complete race winner at iteration %d", iteration)
			}
		}
		if !handoff.Idle() {
			t.Fatalf("race did not return idle at iteration %d", iteration)
		}
	}
}

func TestDeferredExecutorHandoffRejectsInvalidSlots(t *testing.T) {
	var handoff DeferredExecutorHandoff
	if handoff.Arm(0) || handoff.Arm(deferredExecutorHandoffSlotMask+1) {
		t.Fatal("accepted invalid deferred executor slot")
	}
	if _, begun := handoff.BeginStart(); begun {
		t.Fatal("started idle deferred executor handoff")
	}
}
