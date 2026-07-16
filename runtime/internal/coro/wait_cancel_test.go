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

func TestCancelWaitClassificationAndGenerationReuse(t *testing.T) {
	if publishWaitCancellation(nil, 1) != WaitCancelInvalid || publishWaitCancellation(new(WaitToken), 0) != WaitCancelInvalid ||
		publishWaitCancellation(new(WaitToken), WaitTicket(waitMaxGen+1)) != WaitCancelInvalid {
		t.Fatal("invalid cancellation input accepted")
	}
	token := new(WaitToken)
	first, ok := ArmWait(token)
	if !ok {
		t.Fatal("arm first generation")
	}
	if result := publishWaitCancellation(token, first); result != WaitCancelWon {
		t.Fatalf("first cancellation = %d, want won", result)
	}
	if CompleteWait(token, first) || publishWaitCancellation(token, first) != WaitCancelAlreadyCanceled {
		t.Fatal("canceled generation accepted a completion or duplicate cancellation")
	}
	if ticket, ok := ArmWait(token); ok || ticket != 0 {
		t.Fatal("unclaimed canceled generation rearmed")
	}
	if !claimWait(token, first) {
		t.Fatal("claim canceled generation")
	}
	if outcome, ok := consumeWait(token, first); !ok || outcome != WaitOutcomeCanceled {
		t.Fatalf("consume canceled generation = (%d, %t)", outcome, ok)
	}
	if outcome, ok := WaitOutcomeOf(token, first); !ok || outcome != WaitOutcomeCanceled ||
		publishWaitCancellation(token, first) != WaitCancelAlreadyCanceled {
		t.Fatal("consumption forgot the canceled winner")
	}
	second, ok := ArmWait(token)
	if !ok || second == first {
		t.Fatalf("rearm generation = (%d, %t), first=%d", second, ok, first)
	}
	if CompleteWait(token, first) || publishWaitCancellation(token, first) != WaitCancelInvalid {
		t.Fatal("stale first generation affected reuse")
	}
	if !CompleteWait(token, second) || publishWaitCancellation(token, second) != WaitCancelCompletionWon || !claimWait(token, second) {
		t.Fatal("completion winner was not classified")
	}
	if outcome, ok := consumeWait(token, second); !ok || outcome != WaitOutcomeCompleted {
		t.Fatalf("consume completed generation = (%d, %t)", outcome, ok)
	}
	if outcome, ok := WaitOutcomeOf(token, second); !ok || outcome != WaitOutcomeCompleted ||
		publishWaitCancellation(token, second) != WaitCancelCompletionWon {
		t.Fatal("consumption forgot the completion winner")
	}
}

func TestCancelWaitAfterClaim(t *testing.T) {
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok || !claimWait(token, ticket) {
		t.Fatal("arm and claim wait")
	}
	if result := publishWaitCancellation(token, ticket); result != WaitCancelWon {
		t.Fatalf("claimed cancellation = %d, want won", result)
	}
	if !validClaimedWait(token, ticket) || CompleteWait(token, ticket) ||
		publishWaitCancellation(token, ticket) != WaitCancelAlreadyCanceled {
		t.Fatal("claimed canceled state was not terminal")
	}
	if outcome, ok := consumeWait(token, ticket); !ok || outcome != WaitOutcomeCanceled {
		t.Fatalf("consume claimed cancellation = (%d, %t)", outcome, ok)
	}
}

func TestCompleteAndCancelWaitRaceHasOneOutcome(t *testing.T) {
	const iterations = 1000
	for iteration := 0; iteration < iterations; iteration++ {
		token := new(WaitToken)
		ticket, ok := ArmWait(token)
		if !ok {
			t.Fatalf("iteration %d: arm token", iteration)
		}
		start := make(chan struct{})
		completed := make(chan bool, 1)
		canceled := make(chan WaitCancelResult, 1)
		go func() {
			<-start
			completed <- CompleteWait(token, ticket)
		}()
		go func() {
			<-start
			canceled <- publishWaitCancellation(token, ticket)
		}()
		close(start)
		completionWon, cancelResult := <-completed, <-canceled
		var want WaitOutcome
		switch {
		case completionWon && cancelResult == WaitCancelCompletionWon:
			want = WaitOutcomeCompleted
		case !completionWon && cancelResult == WaitCancelWon:
			want = WaitOutcomeCanceled
		default:
			t.Fatalf("iteration %d: completion=%t cancellation=%d", iteration, completionWon, cancelResult)
		}
		if !claimWait(token, ticket) {
			t.Fatalf("iteration %d: claim terminal outcome", iteration)
		}
		if outcome, ok := consumeWait(token, ticket); !ok || outcome != want {
			t.Fatalf("iteration %d: consume = (%d, %t), want %d", iteration, outcome, ok, want)
		}
	}
}

func TestClaimCompleteAndCancelWaitRaceConverges(t *testing.T) {
	const iterations = 1000
	for iteration := 0; iteration < iterations; iteration++ {
		token := new(WaitToken)
		ticket, ok := ArmWait(token)
		if !ok {
			t.Fatalf("iteration %d: arm token", iteration)
		}
		start := make(chan struct{})
		claimed := make(chan bool, 1)
		completed := make(chan bool, 1)
		canceled := make(chan WaitCancelResult, 1)
		go func() {
			<-start
			claimed <- claimWait(token, ticket)
		}()
		go func() {
			<-start
			completed <- CompleteWait(token, ticket)
		}()
		go func() {
			<-start
			canceled <- publishWaitCancellation(token, ticket)
		}()
		close(start)
		claimOK, completionWon, cancelResult := <-claimed, <-completed, <-canceled
		if !claimOK {
			t.Fatalf("iteration %d: exact waiter did not claim", iteration)
		}
		var want WaitOutcome
		switch {
		case completionWon && cancelResult == WaitCancelCompletionWon:
			want = WaitOutcomeCompleted
		case !completionWon && cancelResult == WaitCancelWon:
			want = WaitOutcomeCanceled
		default:
			t.Fatalf("iteration %d: completion=%t cancellation=%d", iteration, completionWon, cancelResult)
		}
		if outcome, ok := consumeWait(token, ticket); !ok || outcome != want {
			t.Fatalf("iteration %d: consume = (%d, %t), want %d", iteration, outcome, ok, want)
		}
	}
}

func TestWaitOutcomeSurvivesConcurrentConsumption(t *testing.T) {
	const iterations = 500
	for iteration := 0; iteration < iterations; iteration++ {
		for _, completed := range []bool{false, true} {
			token := new(WaitToken)
			ticket, ok := ArmWait(token)
			if !ok || !claimWait(token, ticket) {
				t.Fatalf("iteration %d: arm and claim", iteration)
			}
			start := make(chan struct{})
			published := make(chan bool, 1)
			consumed := make(chan WaitOutcome, 1)
			go func() {
				<-start
				if completed {
					published <- CompleteWait(token, ticket)
					return
				}
				published <- publishWaitCancellation(token, ticket) == WaitCancelWon
			}()
			go func() {
				<-start
				for poll := 0; poll < 100000; poll++ {
					if outcome, ok := consumeWait(token, ticket); ok {
						consumed <- outcome
						return
					}
					runtime.Gosched()
				}
				consumed <- WaitOutcomeInvalid
			}()
			close(start)
			if !<-published {
				t.Fatalf("iteration %d: publish completed=%t", iteration, completed)
			}
			outcome := <-consumed
			want := WaitOutcomeCanceled
			cancelResult := WaitCancelAlreadyCanceled
			if completed {
				want = WaitOutcomeCompleted
				cancelResult = WaitCancelCompletionWon
			}
			if outcome != want || publishWaitCancellation(token, ticket) != cancelResult {
				t.Fatalf("iteration %d: completed=%t outcome=%d", iteration, completed, outcome)
			}
			if stable, ok := WaitOutcomeOf(token, ticket); !ok || stable != want {
				t.Fatalf("iteration %d: stable outcome = (%d, %t), want %d", iteration, stable, ok, want)
			}
		}
	}
}

func TestCancelWaitPublishesMetadataAcrossThreads(t *testing.T) {
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok || !claimWait(token, ticket) {
		t.Fatal("arm and claim publication token")
	}
	type cancelRecord struct {
		code    uint64
		inverse uint64
	}
	const code = uint64(0x1234567890abcdef)
	record := new(cancelRecord)
	release := make(chan struct{})
	defer close(release)
	go func() {
		record.code = code
		record.inverse = ^code
		if publishWaitCancellation(token, ticket) != WaitCancelWon {
			panic("cancel publication rejected")
		}
		<-release
	}()
	observed := false
	for poll := 0; poll < 100000; poll++ {
		outcome, ok := consumeWait(token, ticket)
		if ok {
			if outcome != WaitOutcomeCanceled {
				t.Fatalf("published outcome = %d", outcome)
			}
			observed = true
			break
		}
		runtime.Gosched()
	}
	if !observed {
		t.Fatal("cancellation metadata was not published")
	}
	if record.code != code || record.inverse != ^code {
		t.Fatalf("published cancellation metadata = (%#x, %#x)", record.code, record.inverse)
	}
}

func TestSinglePParkCancellationBeforePrepareResumesOnce(t *testing.T) {
	p := new(P)
	task := newYieldingTestG(t, "early-cancel")
	if !Enqueue(p, task.g) {
		t.Fatal("enqueue canceled task")
	}
	g, ok := NextRunnable(p)
	if !ok || g != task.g {
		t.Fatal("dequeue canceled task")
	}
	action := beginWaitTestResume(t, p, task)
	token := new(WaitToken)
	ticket, ok := ArmWait(token)
	if !ok || publishWaitCancellation(token, ticket) != WaitCancelWon {
		t.Fatal("arm and cancel before park")
	}
	task.frame.header.SuspendReason = uint16(SuspendPark)
	task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PreparePark(task.g, task.handle, task.frame.header, token, ticket) {
		t.Fatal("prepare pre-canceled park")
	}
	action, ok = Resumed(p, task.g, action)
	if !ok || action.Kind != ActionPark || !HasWaiting(p) {
		t.Fatalf("commit pre-canceled park = (%+v, %t), waiting=%t", action, ok, HasWaiting(p))
	}
	if count, ok := PollReady(p); !ok || count != 1 || HasWaiting(p) {
		t.Fatalf("promote canceled waiter = (%d, %t), waiting=%t", count, ok, HasWaiting(p))
	}
	g, ok = NextRunnable(p)
	if !ok || g != task.g {
		t.Fatal("canceled waiter not runnable")
	}
	if next, ok := NextRunnable(p); !ok || next != nil {
		t.Fatalf("canceled waiter promoted more than once: (%p, %t)", next, ok)
	}
	finishWaitTestTask(t, p, task, beginWaitTestResume(t, p, task))
	runtime.KeepAlive(task.frame.memory)
}
