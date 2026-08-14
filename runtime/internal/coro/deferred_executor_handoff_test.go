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
	if !handoff.Idle() || !handoff.Arm() || handoff.Arm() {
		t.Fatal("arm deferred executor handoff")
	}
	if slot, phase, ok := handoff.Observe(); !ok || slot != 0 || phase != DeferredExecutorHandoffArmed {
		t.Fatalf("armed snapshot = (%d, %d, %t)", slot, phase, ok)
	}
	if !handoff.Withdraw() || handoff.Withdraw() || !handoff.Idle() {
		t.Fatal("withdraw deferred executor handoff")
	}
}

func TestDeferredExecutorHandoffStartOutcomes(t *testing.T) {
	for _, queued := range []bool{false, true} {
		var handoff DeferredExecutorHandoff
		if !handoff.Arm() {
			t.Fatal("arm deferred executor handoff")
		}
		const slot = uint32(23)
		if !handoff.BeginStart() || handoff.Withdraw() ||
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
	if !handoff.Arm() {
		t.Fatal("arm deferred executor handoff")
	}
	if !handoff.BeginStart() || !handoff.RetryStart() {
		t.Fatal("retry deferred executor start")
	}
	if !handoff.Withdraw() || !handoff.Idle() {
		t.Fatal("withdraw retried deferred executor handoff")
	}
}

func TestDeferredExecutorHandoffStartWithdrawRace(t *testing.T) {
	const iterations = 2_000
	for iteration := 0; iteration < iterations; iteration++ {
		var handoff DeferredExecutorHandoff
		if !handoff.Arm() {
			t.Fatal("arm deferred executor handoff")
		}
		var wait sync.WaitGroup
		wait.Add(2)
		started := make(chan bool, 1)
		withdrawn := make(chan bool, 1)
		go func() {
			defer wait.Done()
			ok := handoff.BeginStart()
			if ok && !handoff.PublishStart(31, true) {
				t.Errorf("publish winning start at iteration %d", iteration)
			}
			started <- ok
		}()
		go func() {
			defer wait.Done()
			withdrawn <- handoff.Withdraw()
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
	if handoff.BeginStart() {
		t.Fatal("started idle deferred executor handoff")
	}
	if !handoff.Arm() || !handoff.BeginStart() {
		t.Fatal("cannot begin deferred executor handoff")
	}
	if handoff.PublishStart(0, false) ||
		handoff.PublishStart(deferredExecutorHandoffSlotMask+1, false) {
		t.Fatal("accepted invalid deferred executor slot")
	}
}
