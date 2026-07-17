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

func TestExecutorSourceSetScansCompleteStaticCatalog(t *testing.T) {
	p := new(P)
	waits := new(WaitRegistrationTable)
	timers := new(TimerRegistrationTable)
	sources := new(ExecutorSourceSet)
	if !bindExecutorSourceSet(sources, p, waits, timers) || !validExecutorSourceSet(sources, p) {
		t.Fatal("bind source set")
	}

	waitToken, waitTicket, wait := registerTestWait(t, waits, p)
	timerToken, timerTicket, timer := prepareTestTimer(t, timers, p, 100)
	if posted := waits.Post(wait); posted != WaitRegistrationPosted || !sources.pending(p) {
		t.Fatalf("post aggregate wait = %d, pending=%t", posted, sources.pending(p))
	}

	scan, ok := sources.drain(p, 90, true)
	if !ok || scan.completed != 1 || scan.waits != 1 || scan.timers != 0 || scan.promoted != 0 ||
		!scan.hasDeadline || scan.deadline != 100 || sources.pending(p) {
		t.Fatalf("first aggregate scan = %+v, ok=%t, pending=%t", scan, ok, sources.pending(p))
	}
	consumeRegisteredOutcome(t, waitToken, waitTicket, WaitOutcomeCompleted)
	if result := waits.BeginClose(wait); result != WaitRegistrationCloseStarted {
		t.Fatalf("begin completed aggregate wait close = %d", result)
	}
	if result, ok := waits.ConfirmQuiesced(wait); !ok || result != WaitCancelCompletionWon || !waits.Retire(wait) {
		t.Fatalf("retire completed aggregate wait = (%d, %t)", result, ok)
	}

	scan, ok = sources.drain(p, 100, true)
	if !ok || scan.completed != 1 || scan.waits != 0 || scan.timers != 1 || scan.promoted != 0 ||
		scan.hasDeadline || scan.deadline != 0 {
		t.Fatalf("second aggregate scan = %+v, ok=%t", scan, ok)
	}
	consumeTimerOutcome(t, timerToken, timerTicket, WaitOutcomeCompleted)
	if !timers.RetireCompletedTimer(timer, timerToken, timerTicket) || !sources.empty(p) {
		t.Fatal("retire aggregate timer")
	}
	if closeScan, ok := sources.drainForClose(p); !ok || closeScan.completed != 0 {
		t.Fatalf("final aggregate scan = %+v, ok=%t", closeScan, ok)
	}
	if !unbindExecutorSourceSet(sources, p) || *sources != (ExecutorSourceSet{}) ||
		!waits.CanRelease() || !timers.CanRelease() {
		t.Fatal("unbind source set")
	}
}

func TestExecutorSourceSetBindRollsBackEarlierSources(t *testing.T) {
	p := new(P)
	other := new(P)
	waits := new(WaitRegistrationTable)
	timers := new(TimerRegistrationTable)
	if !bindTimerRegistrationTable(timers, other) {
		t.Fatal("bind conflicting timer source")
	}

	sources := new(ExecutorSourceSet)
	if bindExecutorSourceSet(sources, p, waits, timers) || *sources != (ExecutorSourceSet{}) ||
		!waits.CanRelease() || waits.owner != nil || timers.owner != other {
		t.Fatal("failed source-set bind did not roll back transaction")
	}
	if !unbindTimerRegistrationTable(timers, other) || !timers.CanRelease() {
		t.Fatal("release conflicting timer source")
	}
}
