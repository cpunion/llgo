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
	"reflect"
	"sort"
	"testing"
	"unsafe"
)

const wantParkCommitRequestSize = 24 + unsafe.Sizeof(uintptr(0))

var (
	_ [wantParkCommitRequestSize - unsafe.Sizeof(ParkCommitRequest{})]byte
	_ [unsafe.Sizeof(ParkCommitRequest{}) - wantParkCommitRequestSize]byte
)

type commitSelectCandidateSpec struct {
	caseID    uint32
	mode      OperationCommitMode
	canCommit bool
}

// commitSelectFakeSource is deliberately a test-only adapter around the
// reusable production resolver. It uses the same exact-ID, synchronous static
// dispatch contract as a future channel/poll source without adding such a
// source or any dynamic dispatch to the runtime core.
type commitSelectFakeSource struct {
	g         G
	state     *ParkState
	wait      WaitSetRecord
	ticket    ParkTicket
	specs     []commitSelectCandidateSpec
	records   []OperationRecord
	ids       []OperationID
	attempts  []uint32
	canCommit []bool
}

func newCommitSelectFakeSource(
	t *testing.T,
	seed uint32,
	specs []commitSelectCandidateSpec,
	attachOrder []int,
	hasDefault bool,
	defaultCase uint32,
) *commitSelectFakeSource {
	t.Helper()
	count := len(specs)
	source := &commitSelectFakeSource{
		specs:     append([]commitSelectCandidateSpec(nil), specs...),
		records:   make([]OperationRecord, count),
		ids:       make([]OperationID, count),
		attempts:  make([]uint32, count),
		canCommit: make([]bool, count),
	}
	for index := range specs {
		source.canCommit[index] = specs[index].canCommit
	}
	if !InitG(&source.g) {
		t.Fatal("initialize commit-capable fake G")
	}
	source.state = &source.g.park
	ticket, ok := BeginParkSet(source.state, uint32(count), seed)
	if !ok {
		t.Fatal("begin commit-capable park-set")
	}
	source.ticket = ticket
	if !PrepareWaitSetRecord(&source.wait, &source.g, ticket) {
		t.Fatal("prepare commit-capable wait-set record")
	}
	if hasDefault && !SetParkDefault(source.state, ticket, defaultCase) {
		t.Fatal("set commit-capable default")
	}
	if len(attachOrder) != count {
		t.Fatalf("attach order length = %d, want %d", len(attachOrder), count)
	}
	seen := make([]bool, count)
	for _, index := range attachOrder {
		if index < 0 || index >= count || seen[index] {
			t.Fatalf("invalid attach index %d", index)
		}
		seen[index] = true
		id, idOK := MakeOperationID(OperationSourceHost, uint32(index+1), 1)
		if !idOK || !InitOperation(&source.records[index], id) {
			t.Fatalf("initialize commit-capable candidate %d", index)
		}
		if specs[index].mode != OperationCommitIrreversibleCompletion &&
			!DeclareOperationCommitMode(&source.records[index], specs[index].mode) {
			t.Fatalf("declare candidate %d mode %d", index, specs[index].mode)
		}
		if !AttachParkWaitOperation(source.state, ticket, &source.wait, &source.records[index], specs[index].caseID) {
			t.Fatalf("attach commit-capable candidate %d", index)
		}
		source.ids[index] = id
	}
	if !SealParkSet(source.state, ticket) || !CommitParkSet(source.state, ticket) {
		t.Fatal("commit commit-capable park-set")
	}
	return source
}

func (source *commitSelectFakeSource) publish(t *testing.T, index int) {
	t.Helper()
	var result OperationCompletionResult
	switch source.specs[index].mode {
	case OperationCommitIrreversibleCompletion:
		result = PublishOperationCompletion(&source.records[index], source.ids[index])
	case OperationCommitReadyThenTryCommit:
		result = PublishReadyThenTryCommitCandidate(&source.records[index], source.ids[index])
	case OperationCommitReservable:
		result = PublishReservableCandidate(&source.records[index], source.ids[index])
	default:
		t.Fatalf("publish invalid candidate mode %d", source.specs[index].mode)
	}
	if result != OperationCompletionPublished {
		t.Fatalf("publish candidate %d = %d", index, result)
	}
}

func (source *commitSelectFakeSource) tryCommit(request ParkCommitRequest) (ParkCommitAttempt, bool) {
	if !currentParkCommitRequest(request) {
		return ParkCommitAttempt{}, false
	}
	index := int(request.id.Slot()) - 1
	if index < 0 || index >= len(source.records) || request.record != &source.records[index] ||
		request.id != source.ids[index] || source.specs[index].mode != OperationCommitReadyThenTryCommit {
		return ParkCommitAttempt{}, false
	}
	source.attempts[index]++
	if source.canCommit[index] {
		return request.Succeeded(), true
	}
	return request.Failed(), true
}

func (source *commitSelectFakeSource) resolve(t *testing.T) (CompletionResolution, ParkResolveStatus) {
	t.Helper()
	var attempt ParkCommitAttempt
	for step := 0; step <= len(source.records)+1; step++ {
		resolution, request, status := ResolveParkSnapshotStep(source.state, source.ticket, attempt)
		switch status {
		case ParkResolvePending, ParkResolveResolved, ParkResolveInvalid:
			return resolution, status
		case ParkResolveNeedsCommit:
			var ok bool
			attempt, ok = source.tryCommit(request)
			if !ok {
				t.Fatal("fake source rejected current commit request")
			}
		default:
			t.Fatalf("unknown park resolution status %d", status)
		}
	}
	t.Fatal("commit-capable resolver did not terminate bounded handshake")
	return CompletionResolution{}, ParkResolveInvalid
}

func (source *commitSelectFakeSource) finish(
	t *testing.T,
) (outcome ParkOutcome, caseID uint32, lease OperationResultLease) {
	t.Helper()
	for index := range source.records {
		disposition, ok := OperationDispositionOf(&source.records[index], source.ids[index])
		if !ok || !AcknowledgeOperationResolution(&source.records[index], source.ids[index], disposition) {
			t.Fatalf("acknowledge candidate %d", index)
		}
		if !DetachParkWaitOperation(source.state, source.ticket, &source.records[index], source.ids[index]) {
			t.Fatalf("detach candidate %d", index)
		}
	}
	if !ParkReady(source.state, source.ticket) {
		t.Fatal("commit-capable park did not cross detach barrier")
	}
	for index := range source.records {
		if !ConfirmOperationQuiesced(&source.records[index], source.ids[index]) {
			t.Fatalf("quiesce candidate %d", index)
		}
	}
	outcome, caseID, lease, ok := ConsumeParkSet(source.state, source.ticket)
	if !ok {
		t.Fatal("consume commit-capable park")
	}
	if !ReleasePreparedWaitSetRecord(&source.wait) {
		t.Fatal("release commit-capable wait-set record")
	}
	winnerID, hasWinner := lease.ID()
	if outcome == ParkOutcomeCompleted {
		if !hasWinner {
			t.Fatal("completed park has no result lease")
		}
	} else if hasWinner || lease != (OperationResultLease{}) {
		t.Fatalf("non-completed park returned result lease %+v", lease)
	}
	for index := range source.records {
		if hasWinner && source.ids[index] == winnerID {
			if OperationCanRecycle(&source.records[index], source.ids[index]) ||
				!TakeOperationResult(&source.records[index], lease) {
				t.Fatalf("release candidate %d winner result", index)
			}
		}
		if !OperationCanRecycle(&source.records[index], source.ids[index]) ||
			!RecycleOperation(&source.records[index], source.ids[index]) {
			t.Fatalf("recycle candidate %d", index)
		}
	}
	return outcome, caseID, lease
}

func firstCommitSelectRankOrder(seed uint32, specs []commitSelectCandidateSpec) []int {
	effectiveSeed := seed ^ uint32(0x9e3779b9)
	order := make([]int, len(specs))
	for index := range order {
		order[index] = index
	}
	sort.Slice(order, func(left, right int) bool {
		return parkCaseRank(effectiveSeed, specs[order[left]].caseID) <
			parkCaseRank(effectiveSeed, specs[order[right]].caseID)
	})
	return order
}

func assertCommitCandidate(
	t *testing.T,
	source *commitSelectFakeSource,
	index int,
	mode OperationCommitMode,
	state OperationCommitState,
	published bool,
) {
	t.Helper()
	gotMode, modeOK := OperationCommitModeOf(&source.records[index], source.ids[index])
	gotState, stateOK := OperationCommitStateOf(&source.records[index], source.ids[index])
	if !modeOK || !stateOK || gotMode != mode || gotState != state ||
		operationCandidateIsPublished(&source.records[index]) != published {
		t.Fatalf("candidate %d = mode(%d,%t) state(%d,%t) published=%t; want mode=%d state=%d published=%t",
			index, gotMode, modeOK, gotState, stateOK, operationCandidateIsPublished(&source.records[index]), mode, state, published)
	}
}

func TestCommitCapableSelectTryFailureContinuesBySeedRankIndependentOfSourceOrder(t *testing.T) {
	const seed = uint32(0x2468ace0)
	specs := []commitSelectCandidateSpec{{caseID: 11}, {caseID: 22}, {caseID: 33}}
	rankOrder := firstCommitSelectRankOrder(seed, specs)
	failed, winner, reservation := rankOrder[0], rankOrder[1], rankOrder[2]
	specs[failed].mode = OperationCommitReadyThenTryCommit
	specs[winner].mode = OperationCommitReadyThenTryCommit
	specs[winner].canCommit = true
	specs[reservation].mode = OperationCommitReservable

	tests := []struct {
		name         string
		attachOrder  []int
		publishOrder []int
	}{
		{name: "forward", attachOrder: []int{0, 1, 2}, publishOrder: []int{0, 1, 2}},
		{name: "reverse", attachOrder: []int{2, 1, 0}, publishOrder: []int{2, 1, 0}},
	}
	var selected [2]uint32
	for testIndex, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := newCommitSelectFakeSource(t, seed, specs, test.attachOrder, false, 0)
			for _, index := range test.publishOrder {
				source.publish(t, index)
			}
			resolution, status := source.resolve(t)
			if status != ParkResolveResolved || resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1, Losers: 2}) {
				t.Fatalf("resolution = (%+v, %d)", resolution, status)
			}
			if source.attempts[failed] != 1 || source.attempts[winner] != 1 || source.attempts[reservation] != 0 {
				t.Fatalf("try-commit attempts = %v", source.attempts)
			}
			assertCommitCandidate(t, source, failed, OperationCommitReadyThenTryCommit, OperationCommitIdle, false)
			assertCommitCandidate(t, source, winner, OperationCommitReadyThenTryCommit, OperationCommitCommitted, true)
			assertCommitCandidate(t, source, reservation, OperationCommitReservable, OperationCommitRolledBack, true)
			winnerCase, winnerID, ok := ParkWinner(source.state, source.ticket)
			if !ok || winnerCase != specs[winner].caseID || winnerID != source.ids[winner] {
				t.Fatalf("winner = (%d, %+v, %t)", winnerCase, winnerID, ok)
			}
			selected[testIndex] = winnerCase
			outcome, caseID, lease := source.finish(t)
			if outcome != ParkOutcomeCompleted || caseID != winnerCase || !lease.Valid() {
				t.Fatalf("consume = (%d, %d, %+v)", outcome, caseID, lease)
			}
		})
	}
	if selected[0] != selected[1] || selected[0] != specs[winner].caseID {
		t.Fatalf("source order changed winner: %v", selected)
	}
}

func TestReadyThenTryCommitAllFailuresStayParkedUntilSourceRepublishes(t *testing.T) {
	specs := []commitSelectCandidateSpec{
		{caseID: 41, mode: OperationCommitReadyThenTryCommit},
		{caseID: 42, mode: OperationCommitReadyThenTryCommit},
	}
	source := newCommitSelectFakeSource(t, 17, specs, []int{0, 1}, false, 0)
	source.publish(t, 0)
	source.publish(t, 1)
	resolution, status := source.resolve(t)
	if status != ParkResolvePending || resolution != (CompletionResolution{WaitSets: 1}) || source.state.phase != parkParked ||
		source.attempts[0] != 1 || source.attempts[1] != 1 {
		t.Fatalf("all-failed snapshot = (%+v, %d), phase=%d attempts=%v", resolution, status, source.state.phase, source.attempts)
	}
	for index := range specs {
		assertCommitCandidate(t, source, index, OperationCommitReadyThenTryCommit, OperationCommitIdle, false)
	}
	resolution, status = source.resolve(t)
	if status != ParkResolvePending || resolution != (CompletionResolution{WaitSets: 1}) ||
		source.attempts[0] != 1 || source.attempts[1] != 1 {
		t.Fatalf("unrepublished retry = (%+v, %d), attempts=%v", resolution, status, source.attempts)
	}
	source.canCommit[1] = true
	source.publish(t, 1)
	resolution, status = source.resolve(t)
	if status != ParkResolveResolved || resolution.Completed != 1 || source.attempts[1] != 2 {
		t.Fatalf("republished retry = (%+v, %d), attempts=%v", resolution, status, source.attempts)
	}
	if outcome, caseID, _ := source.finish(t); outcome != ParkOutcomeCompleted || caseID != specs[1].caseID {
		t.Fatalf("republished consume = (%d, %d)", outcome, caseID)
	}
}

func TestReadyThenTryCommitOldAttemptCannotConsumeRepublishedHint(t *testing.T) {
	specs := []commitSelectCandidateSpec{{caseID: 45, mode: OperationCommitReadyThenTryCommit}}
	source := newCommitSelectFakeSource(t, 19, specs, []int{0}, false, 0)
	source.publish(t, 0)
	_, firstRequest, status := ResolveParkSnapshotStep(source.state, source.ticket, ParkCommitAttempt{})
	if status != ParkResolveNeedsCommit || !currentParkCommitRequest(firstRequest) {
		t.Fatalf("first ready request = (%+v, %d)", firstRequest, status)
	}
	oldAttempt, ok := source.tryCommit(firstRequest)
	if !ok {
		t.Fatal("try first ready hint")
	}
	resolution, _, status := ResolveParkSnapshotStep(source.state, source.ticket, oldAttempt)
	if status != ParkResolvePending || resolution != (CompletionResolution{WaitSets: 1}) {
		t.Fatalf("consume first failed hint = (%+v, %d)", resolution, status)
	}
	firstReadyTicket := firstRequest.readyTicket
	source.publish(t, 0)
	beforeState, beforeRecord := *source.state, source.records[0]
	if currentParkCommitRequest(firstRequest) {
		t.Fatal("old request passed pre-effect gate after republish")
	}
	if got, _, gotStatus := ResolveParkSnapshotStep(source.state, source.ticket, oldAttempt); gotStatus != ParkResolveInvalid ||
		got != (CompletionResolution{}) || *source.state != beforeState || source.records[0] != beforeRecord {
		t.Fatalf("old attempt consumed republished hint: (%+v, %d)", got, gotStatus)
	}
	_, secondRequest, status := ResolveParkSnapshotStep(source.state, source.ticket, ParkCommitAttempt{})
	if status != ParkResolveNeedsCommit || secondRequest.readyTicket == firstReadyTicket || !currentParkCommitRequest(secondRequest) {
		t.Fatalf("republished request = (%+v, %d), first token=%+v", secondRequest, status, firstReadyTicket)
	}
	source.canCommit[0] = true
	newAttempt, ok := source.tryCommit(secondRequest)
	if !ok {
		t.Fatal("try republished ready hint")
	}
	resolution, _, status = ResolveParkSnapshotStep(source.state, source.ticket, newAttempt)
	if status != ParkResolveResolved || resolution.Completed != 1 {
		t.Fatalf("resolve republished hint = (%+v, %d)", resolution, status)
	}
	source.finish(t)
}

func TestReadyThenTryCommitReadinessGenerationExhaustionFailsClosed(t *testing.T) {
	specs := []commitSelectCandidateSpec{{caseID: 47, mode: OperationCommitReadyThenTryCommit}}
	source := newCommitSelectFakeSource(t, 21, specs, []int{0}, false, 0)
	source.records[0].resultTicket = ParkTicket{epoch: ^uint32(0), generation: ^uint32(0)}
	if !validParkState(source.state) {
		t.Fatal("synthetic exhausted readiness generation is not a valid pending record")
	}
	beforeState, beforeRecord := *source.state, source.records[0]
	if result := PublishReadyThenTryCommitCandidate(&source.records[0], source.ids[0]); result != OperationCompletionInvalid ||
		*source.state != beforeState || source.records[0] != beforeRecord {
		t.Fatalf("exhausted readiness publish = %d", result)
	}
	if !RequestParkCancel(source.state, source.ticket, ParkCancelOperation) {
		t.Fatal("cancel exhausted readiness fixture")
	}
	if resolution, status := source.resolve(t); status != ParkResolveResolved || resolution.Canceled != 1 {
		t.Fatalf("resolve exhausted readiness fixture = (%+v, %d)", resolution, status)
	}
	source.finish(t)
}

func TestReadyThenTryCommitLargeSnapshotVisitsEachCandidateOnce(t *testing.T) {
	const candidateCount = 4096
	specs := make([]commitSelectCandidateSpec, candidateCount)
	attachOrder := make([]int, candidateCount)
	for index := range specs {
		specs[index] = commitSelectCandidateSpec{
			caseID: uint32(index + 1),
			mode:   OperationCommitReadyThenTryCommit,
		}
		attachOrder[index] = candidateCount - 1 - index
	}
	source := newCommitSelectFakeSource(t, 0x5a17, specs, attachOrder, false, 0)

	links := uint32(0)
	var previous *ParkLink
	for link := source.state.head; link != nil; link = link.next {
		links++
		if previous != nil && previous.rank >= link.rank {
			t.Fatalf("sealed rank order at link %d = %d then %d", links, previous.rank, link.rank)
		}
		previous = link
	}
	if links != candidateCount {
		t.Fatalf("sealed links = %d, want %d", links, candidateCount)
	}

	for index := candidateCount - 1; index >= 0; index-- {
		source.publish(t, index)
	}
	resolution, status := source.resolve(t)
	if status != ParkResolvePending || resolution != (CompletionResolution{WaitSets: 1}) ||
		source.state.seed != candidateCount || source.state.winnerRecord != nil || source.state.winnerID != (OperationID{}) {
		t.Fatalf("large snapshot = (%+v, %d), visits=%d marker=(%p, %+v)",
			resolution, status, source.state.seed, source.state.winnerRecord, source.state.winnerID)
	}
	for index, attempts := range source.attempts {
		if attempts != 1 {
			t.Fatalf("candidate %d attempts = %d, want 1", index, attempts)
		}
	}

	if !RequestParkCancel(source.state, source.ticket, ParkCancelOperation) {
		t.Fatal("cancel large pending snapshot")
	}
	if resolution, status = source.resolve(t); status != ParkResolveResolved || resolution.Canceled != 1 {
		t.Fatalf("resolve large cleanup = (%+v, %d)", resolution, status)
	}
	source.finish(t)
}

func driveBoundedCommitSelect(
	t *testing.T,
	source *commitSelectFakeSource,
	cursor *parkResolutionCursor,
) (CompletionResolution, ParkResolveStatus, int) {
	t.Helper()
	for step := 1; step <= 3*len(source.records)+8; step++ {
		var attempt ParkCommitAttempt
		if cursor.phase == parkResolutionCommit {
			request, ok := parkResolutionCommitRequest(source.state, source.ticket, cursor)
			if !ok {
				t.Fatal("load bounded commit request")
			}
			attempt, ok = source.tryCommit(request)
			if !ok {
				t.Fatal("dispatch bounded commit request")
			}
		}
		beforeSeed := source.state.seed
		before := append([]OperationRecord(nil), source.records...)
		resolution, request, status := resolveParkSnapshotBoundedStep(source.state, source.ticket, cursor, attempt)
		if source.state.seed < beforeSeed || source.state.seed-beforeSeed > 1 {
			t.Fatalf("bounded step %d candidate visits = %d -> %d", step, beforeSeed, source.state.seed)
		}
		changed := 0
		for index := range before {
			if before[index] != source.records[index] {
				changed++
			}
		}
		if changed > 1 {
			t.Fatalf("bounded step %d changed %d candidate records", step, changed)
		}
		switch status {
		case parkResolveProgress:
			if request != (ParkCommitRequest{}) {
				t.Fatalf("bounded progress step %d returned request %+v", step, request)
			}
		case ParkResolveNeedsCommit:
			if !currentParkCommitRequest(request) {
				t.Fatalf("bounded step %d returned stale request %+v", step, request)
			}
		case ParkResolvePending, ParkResolveResolved:
			return resolution, status, step
		default:
			t.Fatalf("bounded step %d failed with status %d", step, status)
		}
	}
	t.Fatal("bounded commit-capable resolution did not terminate")
	return CompletionResolution{}, ParkResolveInvalid, 0
}

func TestBoundedCommitResolverFreezesSnapshotAndSettlesOneCandidatePerStep(t *testing.T) {
	const seed = uint32(0x6a31)
	specs := []commitSelectCandidateSpec{{caseID: 141}, {caseID: 142}, {caseID: 143}, {caseID: 144}}
	ranks := firstCommitSelectRankOrder(seed, specs)
	failed, winner, irreversible, deferred := ranks[0], ranks[1], ranks[2], ranks[3]
	specs[failed].mode = OperationCommitReadyThenTryCommit
	specs[winner].mode = OperationCommitReservable
	specs[irreversible].mode = OperationCommitIrreversibleCompletion
	specs[deferred].mode = OperationCommitReadyThenTryCommit
	source := newCommitSelectFakeSource(t, seed, specs, []int{3, 1, 0, 2}, false, 0)
	source.publish(t, failed)
	source.publish(t, winner)
	source.publish(t, irreversible)

	var cursor parkResolutionCursor
	if !beginParkSnapshotResolution(source.state, source.ticket, &cursor, true) || !source.state.resolving {
		t.Fatal("begin bounded frozen snapshot")
	}
	beforeState, beforeDeferred := *source.state, source.records[deferred]
	if result := PublishReadyThenTryCommitCandidate(&source.records[deferred], source.ids[deferred]); result != OperationCompletionDeferred || *source.state != beforeState || source.records[deferred] != beforeDeferred {
		t.Fatalf("publication entered frozen snapshot: result=%d", result)
	}
	if RequestParkCancel(source.state, source.ticket, ParkCancelTaskAbort) || *source.state != beforeState ||
		applyTaskCancellationToPark(&source.g, TaskCancelAbort) || *source.state != beforeState {
		t.Fatal("cancellation entered frozen snapshot")
	}
	if result := RequestPhysicalOperationCancel(&source.records[deferred], source.ids[deferred]); result != OperationCancelInvalid || source.records[deferred] != beforeDeferred {
		t.Fatalf("physical cancellation entered frozen snapshot: %d", result)
	}

	resolution, status, steps := driveBoundedCommitSelect(t, source, &cursor)
	if status != ParkResolveResolved || resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1, Losers: 3}) ||
		steps != len(source.records)+4 || source.state.resolving || source.attempts[failed] != 1 {
		t.Fatalf("bounded mixed resolution = (%+v, %d), steps=%d resolving=%t attempts=%v",
			resolution, status, steps, source.state.resolving, source.attempts)
	}
	assertCommitCandidate(t, source, failed, OperationCommitReadyThenTryCommit, OperationCommitIdle, false)
	assertCommitCandidate(t, source, winner, OperationCommitReservable, OperationCommitCommitted, true)
	assertCommitCandidate(t, source, irreversible, OperationCommitIrreversibleCompletion, OperationCommitCommitted, true)
	assertCommitCandidate(t, source, deferred, OperationCommitReadyThenTryCommit, OperationCommitIdle, false)
	if outcome, caseID, _ := source.finish(t); outcome != ParkOutcomeCompleted || caseID != specs[winner].caseID {
		t.Fatalf("bounded mixed consume = (%d, %d)", outcome, caseID)
	}
}

func TestBoundedCommitResolverDefaultWaitsForEveryFailedHint(t *testing.T) {
	specs := []commitSelectCandidateSpec{
		{caseID: 151, mode: OperationCommitReadyThenTryCommit},
		{caseID: 152, mode: OperationCommitReadyThenTryCommit},
		{caseID: 153, mode: OperationCommitReadyThenTryCommit},
	}
	source := newCommitSelectFakeSource(t, 0x6b41, specs, []int{2, 0, 1}, true, 159)
	for index := range specs {
		source.publish(t, index)
	}
	var cursor parkResolutionCursor
	if !beginParkSnapshotResolution(source.state, source.ticket, &cursor, true) {
		t.Fatal("begin bounded default snapshot")
	}
	resolution, status, steps := driveBoundedCommitSelect(t, source, &cursor)
	wantSteps := 3*len(specs) + 2 // scan+TryCommit, decision, settle, finalize
	if status != ParkResolveResolved || resolution != (CompletionResolution{WaitSets: 1, Defaulted: 1, Losers: 3}) ||
		steps != wantSteps || source.state.seed != uint32(len(specs)) {
		t.Fatalf("bounded default = (%+v, %d), steps=%d/%d visits=%d attempts=%v",
			resolution, status, steps, wantSteps, source.state.seed, source.attempts)
	}
	for index, attempts := range source.attempts {
		if attempts != 1 {
			t.Fatalf("bounded default candidate %d attempts = %d", index, attempts)
		}
	}
	if outcome, caseID, lease := source.finish(t); outcome != ParkOutcomeDefault || caseID != 159 || lease.Valid() {
		t.Fatalf("bounded default consume = (%d, %d, %+v)", outcome, caseID, lease)
	}
}

func TestBoundedCommitResolverStrongCancelSkipsReadyDispatch(t *testing.T) {
	specs := []commitSelectCandidateSpec{
		{caseID: 161, mode: OperationCommitReadyThenTryCommit, canCommit: true},
		{caseID: 162, mode: OperationCommitReservable},
	}
	source := newCommitSelectFakeSource(t, 0x6c51, specs, []int{1, 0}, false, 0)
	source.publish(t, 0)
	source.publish(t, 1)
	if !RequestParkCancel(source.state, source.ticket, ParkCancelTaskAbort) {
		t.Fatal("request bounded strong cancellation")
	}
	var cursor parkResolutionCursor
	if !beginParkSnapshotResolution(source.state, source.ticket, &cursor, true) || cursor.phase != parkResolutionDecision {
		t.Fatal("begin bounded strong-cancel snapshot")
	}
	resolution, status, steps := driveBoundedCommitSelect(t, source, &cursor)
	if status != ParkResolveResolved || resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 2}) ||
		steps != len(specs)+2 || source.state.seed != 0 || source.attempts[0] != 0 {
		t.Fatalf("bounded strong cancel = (%+v, %d), steps=%d visits=%d attempts=%v",
			resolution, status, steps, source.state.seed, source.attempts)
	}
	assertCommitCandidate(t, source, 0, OperationCommitReadyThenTryCommit, OperationCommitRolledBack, true)
	assertCommitCandidate(t, source, 1, OperationCommitReservable, OperationCommitRolledBack, true)
	if outcome, caseID, lease := source.finish(t); outcome != ParkOutcomeCanceled || caseID != 0 || lease.Valid() {
		t.Fatalf("bounded strong-cancel consume = (%d, %d, %+v)", outcome, caseID, lease)
	}
}

func TestBoundedCommitResolverFastBeginRejectsBadFirstLinkWithoutPoison(t *testing.T) {
	specs := []commitSelectCandidateSpec{{caseID: 171}}
	source := newCommitSelectFakeSource(t, 0x6d61, specs, []int{0}, false, 0)
	source.state.seed = 37
	link := source.state.head
	record := link.operation
	link.operation = nil
	var cursor parkResolutionCursor
	if beginParkSnapshotResolution(source.state, source.ticket, &cursor, false) || source.state.resolving ||
		source.state.seed != 37 || cursor != (parkResolutionCursor{}) {
		t.Fatalf("failed fast begin left transient state: resolving=%t seed=%d cursor=%+v",
			source.state.resolving, source.state.seed, cursor)
	}
	link.operation = record
	if !validParkState(source.state) || !RequestParkCancel(source.state, source.ticket, ParkCancelOperation) {
		t.Fatal("restore fast-begin rollback fixture")
	}
	if resolution, status := source.resolve(t); status != ParkResolveResolved || resolution.Canceled != 1 {
		t.Fatalf("resolve fast-begin rollback fixture = (%+v, %d)", resolution, status)
	}
	source.finish(t)
}

func TestResolveParkSnapshotCompatibilityDoesNotPoisonReadyHandshake(t *testing.T) {
	specs := []commitSelectCandidateSpec{{caseID: 49, mode: OperationCommitReadyThenTryCommit}}
	source := newCommitSelectFakeSource(t, 22, specs, []int{0}, false, 0)
	source.publish(t, 0)
	beforeState, beforeRecord := *source.state, source.records[0]
	if resolution, ok := ResolveParkSnapshot(source.state, source.ticket); ok || resolution != (CompletionResolution{}) {
		t.Fatalf("compatibility resolution = (%+v, %t)", resolution, ok)
	}
	if *source.state != beforeState || source.records[0] != beforeRecord ||
		source.state.winnerRecord != nil || source.state.winnerID != (OperationID{}) {
		t.Fatal("compatibility wrapper retained transient commit cursor")
	}
	if resolution, ok := new(ExecutorSourceSet).resolveCommitCapablePark(source.state, source.ticket); ok ||
		resolution != (CompletionResolution{}) || *source.state != beforeState || source.records[0] != beforeRecord {
		t.Fatalf("unsupported static dispatcher poisoned snapshot = (%+v, %t)", resolution, ok)
	}

	source.canCommit[0] = true
	resolution, status := source.resolve(t)
	if status != ParkResolveResolved || resolution.Completed != 1 || source.attempts[0] != 1 {
		t.Fatalf("production handshake after compatibility call = (%+v, %d), attempts=%v", resolution, status, source.attempts)
	}
	source.finish(t)
}

func TestOutstandingReadyCommitRequestFreezesSnapshot(t *testing.T) {
	const seed = uint32(0x317)
	specs := []commitSelectCandidateSpec{
		{caseID: 51, mode: OperationCommitReadyThenTryCommit},
		{caseID: 52, mode: OperationCommitReadyThenTryCommit},
	}
	rankOrder := firstCommitSelectRankOrder(seed, specs)
	low, high := rankOrder[0], rankOrder[1]
	source := newCommitSelectFakeSource(t, seed, specs, []int{1, 0}, false, 0)
	source.publish(t, low)
	source.publish(t, high)

	_, lowRequest, status := ResolveParkSnapshotStep(source.state, source.ticket, ParkCommitAttempt{})
	if status != ParkResolveNeedsCommit || lowRequest.record != &source.records[low] {
		t.Fatalf("first outstanding request = (%+v, %d), want candidate %d", lowRequest, status, low)
	}
	lowAttempt, ok := source.tryCommit(lowRequest)
	if !ok {
		t.Fatal("try first ready candidate")
	}
	_, highRequest, status := ResolveParkSnapshotStep(source.state, source.ticket, lowAttempt)
	if status != ParkResolveNeedsCommit || highRequest.record != &source.records[high] || !currentParkCommitRequest(highRequest) {
		t.Fatalf("second outstanding request = (%+v, %d), want candidate %d", highRequest, status, high)
	}

	beforeState := *source.state
	beforeLow, beforeHigh := source.records[low], source.records[high]
	if resolution, _, duplicateStatus := ResolveParkSnapshotStep(source.state, source.ticket, ParkCommitAttempt{}); duplicateStatus != ParkResolveInvalid ||
		resolution != (CompletionResolution{}) || *source.state != beforeState {
		t.Fatalf("reentrant snapshot = (%+v, %d)", resolution, duplicateStatus)
	}
	if RequestParkCancel(source.state, source.ticket, ParkCancelOperation) || *source.state != beforeState {
		t.Fatal("outstanding request accepted logical cancellation")
	}
	if applyTaskCancellationToPark(&source.g, TaskCancelAbort) || *source.state != beforeState {
		t.Fatal("outstanding request accepted task cancellation")
	}
	if result := PublishReadyThenTryCommitCandidate(&source.records[low], source.ids[low]); result != OperationCompletionDeferred ||
		source.records[low] != beforeLow || source.records[high] != beforeHigh || *source.state != beforeState {
		t.Fatalf("publication during outstanding request = %d", result)
	}

	highAttempt, ok := source.tryCommit(highRequest)
	if !ok {
		t.Fatal("try second ready candidate")
	}
	resolution, _, status := ResolveParkSnapshotStep(source.state, source.ticket, highAttempt)
	if status != ParkResolvePending || resolution != (CompletionResolution{WaitSets: 1}) ||
		source.state.seed != uint32(len(specs)) || source.state.winnerRecord != nil || source.state.winnerID != (OperationID{}) {
		t.Fatalf("failed frozen snapshot = (%+v, %d), visits=%d marker=(%p, %+v)",
			resolution, status, source.state.seed, source.state.winnerRecord, source.state.winnerID)
	}

	source.canCommit[low] = true
	source.publish(t, low)
	resolution, status = source.resolve(t)
	if status != ParkResolveResolved || resolution.Completed != 1 || source.attempts[low] != 2 || source.attempts[high] != 1 {
		t.Fatalf("resolution after frozen snapshot = (%+v, %d), attempts=%v", resolution, status, source.attempts)
	}
	if outcome, caseID, _ := source.finish(t); outcome != ParkOutcomeCompleted || caseID != specs[low].caseID {
		t.Fatalf("consume after frozen snapshot = (%d, %d)", outcome, caseID)
	}
}

func TestParkCommitRequestRejectsWrongTicketIDStaleAndDuplicateWithoutMutation(t *testing.T) {
	specs := []commitSelectCandidateSpec{{caseID: 51, mode: OperationCommitReadyThenTryCommit, canCommit: true}}
	source := newCommitSelectFakeSource(t, 23, specs, []int{0}, false, 0)
	source.publish(t, 0)
	resolution, request, status := ResolveParkSnapshotStep(source.state, source.ticket, ParkCommitAttempt{})
	requestTicket, ticketOK := request.Ticket()
	requestID, idOK := request.ID()
	if status != ParkResolveNeedsCommit || resolution != (CompletionResolution{WaitSets: 1}) ||
		!ticketOK || requestTicket != source.ticket || !idOK || requestID != source.ids[0] || !currentParkCommitRequest(request) {
		t.Fatalf("initial request = (%+v, %+v, %d)", resolution, request, status)
	}

	beforeState, beforeRecord := *source.state, source.records[0]
	wrongTicket := request
	wrongTicket.ticket.generation++
	if currentParkCommitRequest(wrongTicket) {
		t.Fatal("wrong-ticket request passed source pre-effect gate")
	}
	badAttempt := ParkCommitAttempt{request: wrongTicket, result: ParkCommitAttemptSucceeded}
	if got, _, gotStatus := ResolveParkSnapshotStep(source.state, source.ticket, badAttempt); gotStatus != ParkResolveInvalid ||
		got != (CompletionResolution{}) || *source.state != beforeState || source.records[0] != beforeRecord {
		t.Fatalf("wrong-ticket attempt mutated state: (%+v, %d)", got, gotStatus)
	}
	wrongID := request
	wrongID.id.Generation++
	if currentParkCommitRequest(wrongID) {
		t.Fatal("wrong-ID request passed source pre-effect gate")
	}
	badAttempt = ParkCommitAttempt{request: wrongID, result: ParkCommitAttemptSucceeded}
	if got, _, gotStatus := ResolveParkSnapshotStep(source.state, source.ticket, badAttempt); gotStatus != ParkResolveInvalid ||
		got != (CompletionResolution{}) || *source.state != beforeState || source.records[0] != beforeRecord {
		t.Fatalf("wrong-ID attempt mutated state: (%+v, %d)", got, gotStatus)
	}

	attempt, ok := source.tryCommit(request)
	if !ok || source.attempts[0] != 1 {
		t.Fatal("exact request did not reach fake source")
	}
	resolution, _, status = ResolveParkSnapshotStep(source.state, source.ticket, attempt)
	if status != ParkResolveResolved || resolution.Completed != 1 {
		t.Fatalf("exact request resolution = (%+v, %d)", resolution, status)
	}
	if _, ok := source.tryCommit(request); ok || source.attempts[0] != 1 {
		t.Fatal("stale request reached source after terminal resolution")
	}
	if got, _, gotStatus := ResolveParkSnapshotStep(source.state, source.ticket, attempt); gotStatus != ParkResolveInvalid || got != (CompletionResolution{}) {
		t.Fatalf("duplicate attempt = (%+v, %d)", got, gotStatus)
	}
	source.finish(t)
}

func TestCommitCapableDefaultRunsOnlyAfterEveryTryCommitFailureAndHasNoLease(t *testing.T) {
	t.Run("all-failures", func(t *testing.T) {
		specs := []commitSelectCandidateSpec{
			{caseID: 61, mode: OperationCommitReadyThenTryCommit},
			{caseID: 62, mode: OperationCommitReadyThenTryCommit},
		}
		source := newCommitSelectFakeSource(t, 29, specs, []int{1, 0}, true, 69)
		source.publish(t, 1)
		source.publish(t, 0)
		resolution, status := source.resolve(t)
		if status != ParkResolveResolved || resolution != (CompletionResolution{WaitSets: 1, Defaulted: 1, Losers: 2}) ||
			source.attempts[0] != 1 || source.attempts[1] != 1 {
			t.Fatalf("default resolution = (%+v, %d), attempts=%v", resolution, status, source.attempts)
		}
		if _, _, ok := ParkWinner(source.state, source.ticket); ok {
			t.Fatal("default exposed a physical winner")
		}
		outcome, caseID, lease := source.finish(t)
		if outcome != ParkOutcomeDefault || caseID != 69 || lease != (OperationResultLease{}) {
			t.Fatalf("default consume = (%d, %d, %+v)", outcome, caseID, lease)
		}
	})

	t.Run("successful-candidate-suppresses-default", func(t *testing.T) {
		specs := []commitSelectCandidateSpec{{caseID: 71, mode: OperationCommitReadyThenTryCommit, canCommit: true}}
		source := newCommitSelectFakeSource(t, 31, specs, []int{0}, true, 79)
		source.publish(t, 0)
		resolution, status := source.resolve(t)
		if status != ParkResolveResolved || resolution.Completed != 1 || resolution.Defaulted != 0 || source.attempts[0] != 1 {
			t.Fatalf("candidate/default resolution = (%+v, %d)", resolution, status)
		}
		outcome, caseID, lease := source.finish(t)
		if outcome != ParkOutcomeCompleted || caseID != 71 || !lease.Valid() {
			t.Fatalf("candidate/default consume = (%d, %d, %+v)", outcome, caseID, lease)
		}
	})

	t.Run("no-ready-candidate", func(t *testing.T) {
		specs := []commitSelectCandidateSpec{{caseID: 81, mode: OperationCommitReadyThenTryCommit, canCommit: true}}
		source := newCommitSelectFakeSource(t, 37, specs, []int{0}, true, 89)
		resolution, status := source.resolve(t)
		if status != ParkResolveResolved || resolution != (CompletionResolution{WaitSets: 1, Defaulted: 1, Losers: 1}) || source.attempts[0] != 0 {
			t.Fatalf("no-ready default = (%+v, %d), attempts=%v", resolution, status, source.attempts)
		}
		if outcome, caseID, lease := source.finish(t); outcome != ParkOutcomeDefault || caseID != 89 || lease.Valid() {
			t.Fatalf("no-ready default consume = (%d, %d, %+v)", outcome, caseID, lease)
		}
	})
}

func TestCommitCapableCancellationPriority(t *testing.T) {
	t.Run("ordinary-cancel-waits-for-successful-candidate", func(t *testing.T) {
		const seed = uint32(41)
		specs := []commitSelectCandidateSpec{{caseID: 91}, {caseID: 92}}
		ranks := firstCommitSelectRankOrder(seed, specs)
		failed, winner := ranks[0], ranks[1]
		specs[failed].mode = OperationCommitReadyThenTryCommit
		specs[winner].mode = OperationCommitReadyThenTryCommit
		specs[winner].canCommit = true
		source := newCommitSelectFakeSource(t, seed, specs, []int{1, 0}, false, 0)
		source.publish(t, 0)
		source.publish(t, 1)
		if !RequestParkCancel(source.state, source.ticket, ParkCancelOperation) {
			t.Fatal("request ordinary cancellation")
		}
		resolution, status := source.resolve(t)
		if status != ParkResolveResolved || resolution.Completed != 1 || resolution.Canceled != 0 ||
			source.attempts[failed] != 1 || source.attempts[winner] != 1 {
			t.Fatalf("ordinary-cancel success = (%+v, %d), attempts=%v", resolution, status, source.attempts)
		}
		if outcome, caseID, _ := source.finish(t); outcome != ParkOutcomeCompleted || caseID != specs[winner].caseID {
			t.Fatalf("ordinary-cancel consume = (%d, %d)", outcome, caseID)
		}
	})

	t.Run("ordinary-cancel-after-all-failures", func(t *testing.T) {
		specs := []commitSelectCandidateSpec{
			{caseID: 101, mode: OperationCommitReadyThenTryCommit},
			{caseID: 102, mode: OperationCommitReadyThenTryCommit},
		}
		source := newCommitSelectFakeSource(t, 43, specs, []int{0, 1}, false, 0)
		source.publish(t, 0)
		source.publish(t, 1)
		if !RequestParkCancel(source.state, source.ticket, ParkCancelOperation) {
			t.Fatal("request ordinary cancellation")
		}
		resolution, status := source.resolve(t)
		if status != ParkResolveResolved || resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 2}) ||
			source.attempts[0] != 1 || source.attempts[1] != 1 {
			t.Fatalf("ordinary-cancel failure = (%+v, %d), attempts=%v", resolution, status, source.attempts)
		}
		if outcome, caseID, lease := source.finish(t); outcome != ParkOutcomeCanceled || caseID != 0 || lease.Valid() {
			t.Fatalf("ordinary-cancel failure consume = (%d, %d, %+v)", outcome, caseID, lease)
		}
	})

	for _, kind := range []ParkCancelKind{ParkCancelTaskAbort, ParkCancelShutdown} {
		kind := kind
		t.Run("strong-cancel-"+map[ParkCancelKind]string{ParkCancelTaskAbort: "task", ParkCancelShutdown: "shutdown"}[kind], func(t *testing.T) {
			specs := []commitSelectCandidateSpec{
				{caseID: 111, mode: OperationCommitReadyThenTryCommit, canCommit: true},
				{caseID: 112, mode: OperationCommitReservable},
			}
			source := newCommitSelectFakeSource(t, 47, specs, []int{1, 0}, false, 0)
			source.publish(t, 0)
			source.publish(t, 1)
			if !RequestParkCancel(source.state, source.ticket, kind) {
				t.Fatalf("request strong cancellation %d", kind)
			}
			resolution, status := source.resolve(t)
			if status != ParkResolveResolved || resolution != (CompletionResolution{WaitSets: 1, Canceled: 1, Losers: 2}) ||
				source.attempts[0] != 0 {
				t.Fatalf("strong cancellation = (%+v, %d), attempts=%v", resolution, status, source.attempts)
			}
			assertCommitCandidate(t, source, 0, OperationCommitReadyThenTryCommit, OperationCommitRolledBack, true)
			assertCommitCandidate(t, source, 1, OperationCommitReservable, OperationCommitRolledBack, true)
			if outcome, caseID, lease := source.finish(t); outcome != ParkOutcomeCanceled || caseID != 0 || lease.Valid() {
				t.Fatalf("strong cancellation consume = (%d, %d, %+v)", outcome, caseID, lease)
			}
		})
	}
}

func TestReservableSelectFreezesLogicalCommitAndRollbackBeforePhysicalAckDetach(t *testing.T) {
	const seed = uint32(53)
	specs := []commitSelectCandidateSpec{
		{caseID: 121, mode: OperationCommitReservable},
		{caseID: 122, mode: OperationCommitReservable},
		{caseID: 123, mode: OperationCommitReservable},
	}
	ranks := firstCommitSelectRankOrder(seed, specs)
	winner := ranks[0]
	source := newCommitSelectFakeSource(t, seed, specs, []int{2, 0, 1}, false, 0)
	source.publish(t, 2)
	source.publish(t, 1)
	source.publish(t, 0)
	resolution, status := source.resolve(t)
	if status != ParkResolveResolved || resolution != (CompletionResolution{WaitSets: 1, Completed: 1, Winners: 1, Losers: 2}) {
		t.Fatalf("reservation resolution = (%+v, %d)", resolution, status)
	}
	for index := range specs {
		wantState := OperationCommitRolledBack
		wantDisposition := OperationDispositionLost
		if index == winner {
			wantState = OperationCommitCommitted
			wantDisposition = OperationDispositionWinner
		}
		assertCommitCandidate(t, source, index, OperationCommitReservable, wantState, true)
		disposition, ok := OperationDispositionOf(&source.records[index], source.ids[index])
		if !ok || disposition != wantDisposition {
			t.Fatalf("reservation candidate %d disposition = (%d, %t)", index, disposition, ok)
		}
		// The resolver has frozen the logical action, but only the source may
		// acknowledge its physical commit/rollback and detach the ParkLink.
		if DetachParkOperation(source.state, source.ticket, &source.records[index], source.ids[index]) {
			t.Fatalf("reservation candidate %d detached before physical acknowledgement", index)
		}
	}
	outcome, caseID, lease := source.finish(t)
	if outcome != ParkOutcomeCompleted || caseID != specs[winner].caseID || !lease.Valid() {
		t.Fatalf("reservation consume = (%d, %d, %+v)", outcome, caseID, lease)
	}
}

func TestCommitCapableSelectCoreLayoutAndPendingStepAreAllocationFree(t *testing.T) {
	if unsafe.Offsetof(OperationRecord{}.candidate) != 11 || unsafe.Offsetof(OperationRecord{}.resultTicket) != 16 ||
		unsafe.Offsetof(OperationRecord{}.link) != 24 {
		t.Fatalf("OperationRecord compact offsets = candidate %d resultTicket %d link %d",
			unsafe.Offsetof(OperationRecord{}.candidate), unsafe.Offsetof(OperationRecord{}.resultTicket), unsafe.Offsetof(OperationRecord{}.link))
	}
	if unsafe.Offsetof(ParkState{}.hasDefault) != 9 || unsafe.Offsetof(ParkState{}.resolving) != 10 ||
		unsafe.Offsetof(ParkState{}.expected) != 12 {
		t.Fatalf("ParkState padding reuse offsets = default %d resolving %d expected %d",
			unsafe.Offsetof(ParkState{}.hasDefault), unsafe.Offsetof(ParkState{}.resolving), unsafe.Offsetof(ParkState{}.expected))
	}
	for _, value := range []any{
		OperationRecord{}, ParkLink{}, ParkState{}, ParkCommitRequest{}, ParkCommitAttempt{}, ExecutorSourceSet{},
		parkResolutionCursor{}, publishedEpochResolveCursor{},
	} {
		typeOf := reflect.TypeOf(value)
		for fieldIndex := 0; fieldIndex < typeOf.NumField(); fieldIndex++ {
			kind := typeOf.Field(fieldIndex).Type.Kind()
			switch kind {
			case reflect.Func, reflect.Interface, reflect.Map, reflect.Slice:
				t.Fatalf("%s.%s introduces dynamic candidate dispatch/storage (%s)",
					typeOf.Name(), typeOf.Field(fieldIndex).Name, kind)
			}
		}
	}

	specs := []commitSelectCandidateSpec{{caseID: 131, mode: OperationCommitReadyThenTryCommit}}
	source := newCommitSelectFakeSource(t, 59, specs, []int{0}, false, 0)
	failed := false
	allocations := testing.AllocsPerRun(1000, func() {
		resolution, request, status := ResolveParkSnapshotStep(source.state, source.ticket, ParkCommitAttempt{})
		if status != ParkResolvePending || resolution != (CompletionResolution{WaitSets: 1}) || request != (ParkCommitRequest{}) {
			failed = true
		}
	})
	if failed || allocations != 0 {
		t.Fatalf("pending resolver = failed %t allocations %.2f", failed, allocations)
	}
	if !RequestParkCancel(source.state, source.ticket, ParkCancelOperation) {
		t.Fatal("cancel allocation fixture")
	}
	if resolution, status := source.resolve(t); status != ParkResolveResolved || resolution.Canceled != 1 {
		t.Fatalf("resolve allocation fixture = (%+v, %d)", resolution, status)
	}
	source.finish(t)
}
