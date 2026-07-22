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

// TestUnifiedTimerPollChannelWaitSet proves the cross-source invariant which
// the source-specific tests cannot establish independently: one frame-local
// WaitSetRecord may bind Timer, Poll, and Channel candidates, publish all three
// facts in one complete catalog snapshot, select exactly one ranked winner,
// detach every loser before promotion, enforce quiescence before recycle, and
// then reuse the same logical and physical wait-set storage for a
// task-cancellation race.
func TestUnifiedTimerPollChannelWaitSet(t *testing.T) {
	p := new(P)
	sources := new(ExecutorSourceSet)
	timers := new(TimerRegistrationTable)
	poll := new(PollOperationSource)
	channel := new(ChannelOperationSource)
	if !bindExecutorSourceSetAtRoute(sources, p, RouteID(7), ExecutorSourceCatalog{
		Timers: timers, Poll: poll, Channel: channel,
	}) {
		t.Fatal("bind unified timer/poll/channel source set")
	}

	park := beginTimerV2TestPark(t, p, "unified-multi-event", 3, 313)
	claim := new(SelectClaim)
	cases := [3]uint32{71, 72, 73}
	for left := 0; left < len(cases); left++ {
		for right := left + 1; right < len(cases); right++ {
			if parkCaseRank(park.task.g.park.seed, cases[right]) < parkCaseRank(park.task.g.park.seed, cases[left]) {
				cases[left], cases[right] = cases[right], cases[left]
			}
		}
	}
	timerCase, pollCase, channelCase := cases[0], cases[1], cases[2]
	timerHandle, timerOK := timers.ReserveAndAttachTimerV2(
		p, &park.task.g.park, park.ticket, park.wait, timerCase, 0,
	)
	pollHandle, pollID := reservePollV2Test(t, p, poll, park, pollCase, 41)
	channelID, channelOK := channel.ReserveAndAttachWait(
		p, &park.task.g.park, park.ticket, park.wait, channelCase, claim,
	)
	if !timerOK || !channelOK || !SealParkSet(&park.task.g.park, park.ticket) {
		t.Fatal("attach and seal unified wait-set")
	}
	park.task.frame.header.SuspendReason = uint16(SuspendPark)
	park.task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(park.task.g, park.task.handle, park.task.frame.header, park.ticket, park.wait) ||
		!channel.ExposeExternalCommit(p, park.task.g, channelID, park.ticket, park.wait, claim) {
		t.Fatal("prepare unified wait-set")
	}
	if parked, resumed := Resumed(p, park.task.g, park.action); !resumed || parked.Kind != ActionPark {
		t.Fatalf("commit unified wait-set = (%+v, %t)", parked, resumed)
	}
	if poll.PostPollOperationV2(pollID, PollOperationReady) != PollOperationPosted ||
		channel.PostReady(channelID) != ChannelOperationPosted {
		t.Fatal("publish unified poll/channel facts")
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.timers != 1 || scan.poll != 1 ||
		scan.channel != 1 || scan.completed != 3 {
		t.Fatalf("publish unified source snapshot = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 3 {
		t.Fatalf("resolve unified source snapshot = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, selectedCase, lease, taskCancel := resumeTimerV2TestPark(t, p, park)
	if outcome != ParkOutcomeCompleted || selectedCase != timerCase || taskCancel != TaskCancelNone ||
		!timers.TakeTimerV2Result(p, timerHandle, lease) || !timers.RecycleTimerV2(p, timerHandle) ||
		!poll.RecyclePollOperationV2(p, pollHandle) || !channel.ConfirmQuiesced(p, channelID) ||
		!channel.ResetSelectClaim(p, claim) || !channel.Recycle(p, channelID) {
		t.Fatalf("finish unified winner and losers = (%d, %d, %+v, %d)", outcome, selectedCase, lease, taskCancel)
	}
	if *park.wait != (WaitSetRecord{}) || selectClaimLoad(claim) != selectClaimOpen {
		t.Fatal("promoted unified wait-set storage did not become reusable")
	}

	// Reuse the exact ParkState, WaitSetRecord, SelectClaim, and physical source
	// slots. All facts become visible beside a strong task cancellation; the
	// cancellation suppresses every continuation while source Apply still
	// retires all three physical generations before promotion.
	secondTicket, begun := BeginParkSet(&park.task.g.park, 3, 317)
	if !begun || !PrepareWaitSetRecord(park.wait, park.task.g, secondTicket) {
		t.Fatal("rearm unified wait-set storage")
	}
	second := &timerV2TestPark{task: park.task, ticket: secondTicket, wait: park.wait, action: action}
	secondTimer, secondTimerOK := timers.ReserveAndAttachTimerV2(
		p, &second.task.g.park, second.ticket, second.wait, timerCase, 0,
	)
	secondPoll, secondPollID := reservePollV2Test(t, p, poll, second, pollCase, 42)
	secondChannelID, secondChannelOK := channel.ReserveAndAttachWait(
		p, &second.task.g.park, second.ticket, second.wait, channelCase, claim,
	)
	if !secondTimerOK || !secondChannelOK || secondTimer.Slot != timerHandle.Slot ||
		secondTimer.Generation <= timerHandle.Generation || secondPoll.Slot != pollHandle.Slot ||
		secondPoll.Generation <= pollHandle.Generation || secondChannelID.LocalSlot() != channelID.LocalSlot() ||
		secondChannelID.Generation <= channelID.Generation || !SealParkSet(&second.task.g.park, second.ticket) {
		t.Fatal("rearm unified source generations")
	}
	second.task.frame.header.SuspendReason = uint16(SuspendPark)
	second.task.frame.header.Lifecycle = uint16(FrameSuspended)
	if !PrepareParkSet(second.task.g, second.task.handle, second.task.frame.header, second.ticket, second.wait) ||
		!channel.ExposeExternalCommit(p, second.task.g, secondChannelID, second.ticket, second.wait, claim) {
		t.Fatal("prepare repeated unified wait-set")
	}
	if parked, resumed := Resumed(p, second.task.g, second.action); !resumed || parked.Kind != ActionPark {
		t.Fatalf("commit repeated unified wait-set = (%+v, %t)", parked, resumed)
	}
	if poll.PostPollOperationV2(secondPollID, PollOperationReady) != PollOperationPosted ||
		channel.PostReady(secondChannelID) != ChannelOperationPosted ||
		!RequestTaskCancellation(p, second.task.g, TaskCancelAbort) {
		t.Fatal("publish repeated facts and task cancellation")
	}
	if scan, ok := sources.publishPass(p, 0, true); !ok || scan.timers != 1 || scan.poll != 1 ||
		scan.channel != 1 || scan.completed != 3 {
		t.Fatalf("publish canceled unified snapshot = (%+v, %t)", scan, ok)
	}
	if promoted, visits, ok := sources.resolvePublishedEpoch(p); !ok || promoted != 1 || visits != 3 {
		t.Fatalf("resolve canceled unified snapshot = (%d, %d, %t)", promoted, visits, ok)
	}
	action, outcome, selectedCase, lease, taskCancel = resumeTimerV2TestPark(t, p, second)
	if outcome != ParkOutcomeCanceled || selectedCase != 0 || lease.Valid() || taskCancel != TaskCancelAbort ||
		!timers.RecycleTimerV2(p, secondTimer) || !poll.RecyclePollOperationV2(p, secondPoll) ||
		!channel.ConfirmQuiesced(p, secondChannelID) || !channel.ResetSelectClaim(p, claim) ||
		!channel.Recycle(p, secondChannelID) {
		t.Fatalf("finish canceled unified wait-set = (%d, %d, %+v, %d)", outcome, selectedCase, lease, taskCancel)
	}
	if !unbindExecutorSourceSet(sources, p) || !timers.CanRelease() ||
		!poll.CanRelease() || !channel.CanRelease() {
		t.Fatal("unbind unified wait-set source catalog")
	}
	finishWaitTestTask(t, p, second.task, action)
	if !AcknowledgeTaskCancellation(second.task.g, TaskCancelAbort) || !TerminalG(p, second.task.g) {
		t.Fatal("release unified wait-set task cancellation")
	}
	runtime.KeepAlive(second.task.frame.memory)
}
