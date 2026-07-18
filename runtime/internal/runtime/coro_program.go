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

package runtime

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/coroalloc"
)

type coroProgramLifecycleV1 uint8

const (
	coroProgramUnusedV1 coroProgramLifecycleV1 = iota
	coroProgramBegunV1
	coroProgramRunningV1
	coroProgramMainReturnRequestedV1
	coroProgramStoppingV1
	coroProgramCompleteV1
	coroProgramFailedV1
)

type coroProgramDriveStatusV1 uint8

const (
	coroProgramDriveInvalidV1 coroProgramDriveStatusV1 = iota
	coroProgramDriveCompleteV1
	coroProgramDriveSuspendedV1
	coroProgramDrivePanicV1
	coroProgramDriveIgnoredV1
	// coroProgramDriveAgainV1 is internal to the scheduler-owner pump. It is
	// never returned through a public ABI. A synchronous native retained wait
	// uses it to resume without recursively stacking one Drive frame per wake.
	coroProgramDriveAgainV1
)

// coroProgramDriveStatusV2 is the exact uint32 status returned by the
// host-facing slice ABI. Values are frozen independently of the private V1
// status enum so a target can inspect them without a Go type or pointer.
type coroProgramDriveStatusV2 uint32

const (
	coroProgramDriveInvalidV2 coroProgramDriveStatusV2 = iota
	coroProgramDriveCompleteV2
	coroProgramDriveSuspendedV2
	coroProgramDriveYieldedV2
	coroProgramDrivePanicV2
	coroProgramDriveIgnoredV2
	coroProgramDriveRepostV2
	// AgainFresh is private. It is returned only after continuation settlement
	// has advanced the admission phase and before that same owner runs another
	// slice.
	coroProgramDriveAgainFreshV2
)

const (
	coroProgramRunMoreV2 uint32 = 1 << iota
	coroProgramRunBlockedV2
	coroProgramRunHasDeadlineV2
	coroProgramRunRequestInlineV2
	coroProgramRunRequestQueuedV2
)

// coroProgramRunResultV2 is a padding-free 32-byte POD result. Deadline is an
// absolute monotonic int64 split into two uint32 words so wasm32 hosts do not
// need an i64/BigInt calling convention. The caller owns this object; the
// runtime clears it before admission and never retains its address.
type coroProgramRunResultV2 struct {
	Flags              uint32
	Used               uint32
	ExecutorSlot       uint32
	ExecutorGeneration uint32
	Epoch              uint32
	DeadlineLo         uint32
	DeadlineHi         uint32
	Reserved           uint32
}

type coroProgramDriveOutcomeV2 struct {
	status   coroProgramDriveStatusV2
	result   coroProgramRunResultV2
	ranSlice bool
}

type coroProgramContinuationV1 uint8

const (
	coroProgramContinuationNoneV1 coroProgramContinuationV1 = iota
	coroProgramContinuationExecutorWakeV1
	coroProgramContinuationTerminalJoinV1
	coroProgramContinuationCommandJoinV1
	coroProgramContinuationHostRunV2
)

type coroProgramDriverModeV2 uint8

const (
	coroProgramDriverModeUnusedV2 coroProgramDriverModeV2 = iota
	coroProgramDriverModeLegacyV1
	coroProgramDriverModeSliceV2
)

// The coroutine program globals form the allocation-free, single-start state used by
// the process entry coroutine. Keeping G and P in static storage avoids a
// pthread, TLS, or event-library dependency for scheduler state. LLVM frames
// use the explicitly bootstrapped, statically selected coroalloc backend:
// native GC builds use BDWGC uncollectable ranges, nogc/wasm profiles use C
// malloc/free, and bare-metal builds use tinygogc.
//
// The entry path is intentionally single-use. No failure path resets this
// object: exported ABI failures terminate the process, and successful startup
// transitions from unused to complete or permanently failed.
// Keep phase-0 fields as separate globals. Besides making ownership explicit,
// this avoids a synthetic nil-dereference helper on field access through the
// address of one aggregate global; the process-entry ABI must remain a plain,
// non-suspending call island.
var (
	coroProgramLifecycleV1State          coroProgramLifecycleV1
	coroProgramManifestV1State           *coro.ProgramManifestV1
	coroProgramFactoryV1State            unsafe.Pointer
	coroProgramGV1State                  coroG
	coroProgramPV1State                  coroP
	coroProgramContinuationV1State       coroProgramContinuationV1
	coroProgramContinuationEpochV1       uint32
	coroProgramContinuationDeadlineV2    int64
	coroProgramContinuationHasDeadlineV2 bool
	coroProgramDriveAdmissionV1State     coro.DriveAdmission
	coroProgramDriverModeV2State         coroProgramDriverModeV2
)

func coroProgramSelectDriverModeV2(mode coroProgramDriverModeV2) bool {
	if mode != coroProgramDriverModeLegacyV1 && mode != coroProgramDriverModeSliceV2 {
		return false
	}
	if coroProgramDriverModeV2State == coroProgramDriverModeUnusedV2 {
		if !coroProgramDriveAdmissionV1State.PublishMode(uint32(mode)) {
			return false
		}
		coroProgramDriverModeV2State = mode
	}
	return coroProgramDriverModeV2State == mode
}

func coroProgramFailV1() coroProgramDriveStatusV1 {
	_ = coroProgramDriveAdmissionV1State.RevokeEpoch()
	coroProgramLifecycleV1State = coroProgramFailedV1
	return coroProgramDriveInvalidV1
}

func coroProgramPublishContinuationV1(kind coroProgramContinuationV1) (uint32, bool) {
	if kind == coroProgramContinuationNoneV1 || coroProgramContinuationV1State != coroProgramContinuationNoneV1 ||
		coroProgramContinuationDeadlineV2 != 0 || coroProgramContinuationHasDeadlineV2 ||
		coroProgramContinuationEpochV1 == ^uint32(0) {
		return 0, false
	}
	coroProgramContinuationEpochV1++
	if coroProgramContinuationEpochV1 == 0 {
		return 0, false
	}
	coroProgramContinuationV1State = kind
	if !coroProgramDriveAdmissionV1State.PublishEpoch(coroProgramContinuationEpochV1) {
		coroProgramContinuationV1State = coroProgramContinuationNoneV1
		return 0, false
	}
	return coroProgramContinuationEpochV1, true
}

func coroProgramClearContinuationV1(kind coroProgramContinuationV1) bool {
	if kind == coroProgramContinuationNoneV1 || coroProgramContinuationV1State != kind ||
		coroProgramContinuationEpochV1 == 0 ||
		!coroProgramDriveAdmissionV1State.ClearEpoch(coroProgramContinuationEpochV1) {
		return false
	}
	coroProgramContinuationV1State = coroProgramContinuationNoneV1
	coroProgramContinuationDeadlineV2 = 0
	coroProgramContinuationHasDeadlineV2 = false
	// Clearing E1 and publishing E2 while retaining the same gate phase lets a
	// delayed E1 callback CAS become the owner (or Pending) of E2. Centralize the
	// transition here so every settled continuation invalidates its observed
	// phase before any caller can publish later work. The first publication does
	// not need an advance; only a completed publication crosses this boundary.
	return coroProgramDriveAdmissionV1State.AdvancePhase()
}

func coroProgramSetOutcomeContinuationV2(outcome *coroProgramDriveOutcomeV2, flags uint32) bool {
	if outcome == nil || !coroProgramExecutorBoundV1State ||
		coroProgramExecutorHandleV1State.Slot == 0 || coroProgramExecutorHandleV1State.Generation == 0 ||
		coroProgramContinuationV1State == coroProgramContinuationNoneV1 || coroProgramContinuationEpochV1 == 0 {
		return false
	}
	outcome.result.Flags |= flags
	outcome.result.ExecutorSlot = coroProgramExecutorHandleV1State.Slot
	outcome.result.ExecutorGeneration = coroProgramExecutorHandleV1State.Generation
	outcome.result.Epoch = coroProgramContinuationEpochV1
	return true
}

func coroProgramSetOutcomeDeadlineV2(outcome *coroProgramDriveOutcomeV2, deadline int64, hasDeadline bool) {
	if outcome == nil || !hasDeadline {
		return
	}
	word := uint64(deadline)
	outcome.result.Flags |= coroProgramRunHasDeadlineV2
	outcome.result.DeadlineLo = uint32(word)
	outcome.result.DeadlineHi = uint32(word >> 32)
}

func coroProgramOutcomeFromV1(status coroProgramDriveStatusV1) coroProgramDriveOutcomeV2 {
	switch status {
	case coroProgramDriveCompleteV1:
		return coroProgramDriveOutcomeV2{status: coroProgramDriveCompleteV2}
	case coroProgramDriveSuspendedV1:
		return coroProgramDriveOutcomeV2{status: coroProgramDriveSuspendedV2}
	case coroProgramDrivePanicV1:
		return coroProgramDriveOutcomeV2{status: coroProgramDrivePanicV2}
	case coroProgramDriveIgnoredV1:
		return coroProgramDriveOutcomeV2{status: coroProgramDriveIgnoredV2}
	case coroProgramDriveAgainV1:
		return coroProgramDriveOutcomeV2{status: coroProgramDriveAgainFreshV2}
	default:
		return coroProgramDriveOutcomeV2{status: coroProgramDriveInvalidV2}
	}
}

func coroProgramMergeOutcomeV2(previous, next coroProgramDriveOutcomeV2) coroProgramDriveOutcomeV2 {
	if previous.ranSlice {
		next.ranSlice = true
		if ^next.result.Used < previous.result.Used {
			return coroProgramDriveOutcomeV2{status: coroProgramDriveInvalidV2}
		}
		next.result.Used += previous.result.Used
	}
	return next
}

func coroProgramBeginOwnedV1(manifest, expectedFactory unsafe.Pointer) (unsafe.Pointer, bool) {
	if coroProgramLifecycleV1State != coroProgramUnusedV1 {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return nil, false
	}
	if !coroalloc.Ready() {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return nil, false
	}
	if manifest == nil {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return nil, false
	}
	programManifest := (*coro.ProgramManifestV1)(manifest)
	_, v2Code := coro.ValidateRunnableProgramV2(programManifest, expectedFactory)
	_, v1Code := coro.ValidateRunnableDirectProgramV1(programManifest, expectedFactory)
	if v2Code != coro.ProgramValidationOKV2 && v1Code != coro.ProgramValidationOKV1 {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return nil, false
	}
	if !coroInitG(&coroProgramGV1State) || !coroProgramBindExecutorV1() {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return nil, false
	}
	coroProgramManifestV1State = (*coro.ProgramManifestV1)(manifest)
	coroProgramFactoryV1State = expectedFactory
	coroProgramLifecycleV1State = coroProgramBegunV1
	return unsafe.Pointer(&coroProgramGV1State), true
}

func coroProgramBeginV1(manifest, expectedFactory unsafe.Pointer) (unsafe.Pointer, bool) {
	if !coroProgramDriveAdmissionV1State.Acquire() {
		return nil, false
	}
	g, ok := coroProgramBeginOwnedV1(manifest, expectedFactory)
	_, pending, released := coroProgramDriveAdmissionV1State.Finish()
	if !released || pending {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return nil, false
	}
	return g, ok
}

func coroProgramFinishPanicV1(g *coroG, action coro.Action) coroProgramDriveStatusV1 {
	if g == nil || action.Kind != coro.ActionPanicComplete || action.Handle != nil {
		return coroProgramFailV1()
	}
	if _, published := coro.LoadPanicRecord(g); !published {
		return coroProgramFailV1()
	}
	_ = coroProgramDriveAdmissionV1State.RevokeEpoch()
	coroProgramLifecycleV1State = coroProgramFailedV1
	return coroProgramDrivePanicV1
}

func coroProgramFinishCommandV1() coroProgramDriveStatusV1 {
	if coroProgramLifecycleV1State != coroProgramMainReturnRequestedV1 ||
		coroProgramExecutorBoundV1State ||
		!coro.BeginCommandShutdown(&coroProgramPV1State, &coroProgramGV1State) {
		return coroProgramFailV1()
	}
	coroProgramLifecycleV1State = coroProgramStoppingV1
	if !coroCancelReady(&coroProgramPV1State) ||
		!coro.FinishCommandShutdown(&coroProgramPV1State, &coroProgramGV1State) ||
		!coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
		return coroProgramFailV1()
	}
	coroProgramLifecycleV1State = coroProgramCompleteV1
	return coroProgramDriveCompleteV1
}

func coroProgramConfirmTerminalJoinV1() coroProgramDriveStatusV1 {
	g, action, ok := coro.ConfirmTerminalExecutorClose(&coroProgramExecutorDriverV1State)
	if !ok || g == nil || !coroProgramExecutorRetiredV1() ||
		!coroProgramClearContinuationV1(coroProgramContinuationTerminalJoinV1) {
		return coroProgramFailV1()
	}
	switch action.Kind {
	case coro.ActionComplete:
		if action.Handle != nil ||
			g != &coroProgramGV1State && coroProgramLifecycleV1State != coroProgramMainReturnRequestedV1 ||
			!coroReleaseCompletedTask(g) {
			return coroProgramFailV1()
		}
		return coroProgramFinishMainV1()
	case coro.ActionPanicComplete:
		if g != &coroProgramGV1State {
			return coroProgramFailV1()
		}
		return coroProgramFinishPanicV1(g, action)
	default:
		return coroProgramFailV1()
	}
}

func coroProgramConfirmCommandJoinV1() coroProgramDriveStatusV1 {
	if !coro.ConfirmExecutorClose(&coroProgramExecutorDriverV1State) ||
		!coroProgramExecutorRetiredV1() ||
		!coroProgramClearContinuationV1(coroProgramContinuationCommandJoinV1) {
		return coroProgramFailV1()
	}
	return coroProgramFinishCommandV1()
}

func coroProgramBeginCommandCloseV1() bool {
	for {
		if !coroProgramPollExecutorV1(&coroProgramExecutorDriverV1State) {
			return false
		}
		if coro.BeginExecutorClose(&coroProgramExecutorDriverV1State) {
			return true
		}
		// A producer may win after PollExecutor's acknowledgement and before
		// BeginExecutorClose's exact gate CAS. Service that durable request and
		// retry; any other close failure is a scheduler invariant violation.
		if !coroProgramExecutorRegistryV1State.ObserveRequested(coroProgramExecutorHandleV1State) {
			return false
		}
	}
}

func coroProgramBeginTargetCloseV1(kind coroProgramContinuationV1) coroProgramDriveStatusV1 {
	if !coroProgramExecutorBoundV1State {
		return coroProgramFailV1()
	}
	epoch, ok := coroProgramPublishContinuationV1(kind)
	if !ok {
		return coroProgramFailV1()
	}
	switch coroTargetBeginExecutorCloseV1(coroProgramExecutorHandleV1State, epoch) {
	case coroTargetDispatchPendingV1:
		return coroProgramDriveSuspendedV1
	case coroTargetDispatchCompleteV1:
		switch kind {
		case coroProgramContinuationTerminalJoinV1:
			return coroProgramConfirmTerminalJoinV1()
		case coroProgramContinuationCommandJoinV1:
			return coroProgramConfirmCommandJoinV1()
		}
	}
	return coroProgramFailV1()
}

func coroProgramFinishMainV1() coroProgramDriveStatusV1 {
	switch coroProgramLifecycleV1State {
	case coroProgramRunningV1:
		// A startup table without the explicit normal-main marker is valid only
		// when terminal close has already retired the executor and the whole P.
		if !coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
			return coroProgramFailV1()
		}
		coroProgramLifecycleV1State = coroProgramCompleteV1
		return coroProgramDriveCompleteV1
	case coroProgramMainReturnRequestedV1:
		if coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
			coroProgramLifecycleV1State = coroProgramCompleteV1
			return coroProgramDriveCompleteV1
		}
		if needed, ok := coro.RequestCommandShutdownDrain(
			&coroProgramPV1State,
			&coroProgramGV1State,
		); !ok {
			return coroProgramFailV1()
		} else if needed {
			// Event-source registrations must be consumed by their compiler
			// resume gates before the target ingress is strongly joined. Re-enter
			// the bounded runner; every CheckResume dispatch receives the sticky
			// shutdown token before it can execute user code.
			return coroProgramDriveAgainV1
		}
		if !coroProgramExecutorBoundV1State || !coroProgramBeginCommandCloseV1() {
			return coroProgramFailV1()
		}
		return coroProgramBeginTargetCloseV1(coroProgramContinuationCommandJoinV1)
	default:
		return coroProgramFailV1()
	}
}

func coroProgramBeginExecutorWaitV1(deadline int64, hasDeadline bool) coroProgramDriveStatusV1 {
	if !coroProgramExecutorBoundV1State {
		return coroProgramFailV1()
	}
	epoch, ok := coroProgramPublishContinuationV1(coroProgramContinuationExecutorWakeV1)
	if !ok {
		return coroProgramFailV1()
	}
	coroProgramContinuationDeadlineV2 = deadline
	coroProgramContinuationHasDeadlineV2 = hasDeadline
	switch coroTargetBeginExecutorWaitV1(coroProgramExecutorHandleV1State, epoch, deadline, hasDeadline) {
	case coroTargetDispatchPendingV1:
		return coroProgramDriveSuspendedV1
	case coroTargetDispatchCompleteV1:
		if !coroProgramWakeExecutorV1(&coroProgramExecutorDriverV1State) ||
			!coroProgramClearContinuationV1(coroProgramContinuationExecutorWakeV1) {
			return coroProgramFailV1()
		}
		return coroProgramDriveAgainV1
	default:
		return coroProgramFailV1()
	}
}

func coroProgramHandleRunResultV1(result coroRunResultV1) coroProgramDriveStatusV1 {
	switch result.stop {
	case coroRunMainDoneV1:
		if result.g != &coroProgramGV1State || result.action != (coro.Action{}) {
			return coroProgramFailV1()
		}
		return coroProgramFinishMainV1()
	case coroRunExecutorSleepV1:
		if result.g != nil || result.action != (coro.Action{}) {
			return coroProgramFailV1()
		}
		return coroProgramBeginExecutorWaitV1(result.deadline, result.hasDeadline)
	case coroRunTerminalExecutorCloseV1:
		driver, valid := coro.TerminalExecutorCloseDriver(
			&coroProgramPV1State,
			result.g,
			result.action,
		)
		if !valid || driver != &coroProgramExecutorDriverV1State {
			return coroProgramFailV1()
		}
		return coroProgramBeginTargetCloseV1(coroProgramContinuationTerminalJoinV1)
	case coroRunPanicCompleteV1:
		return coroProgramFinishPanicV1(result.g, result.action)
	default:
		return coroProgramFailV1()
	}
}

func coroProgramDriveStepV1() coroProgramDriveStatusV1 {
	if coroProgramContinuationV1State != coroProgramContinuationNoneV1 {
		return coroProgramFailV1()
	}
	return coroProgramHandleRunResultV1(coroRun(
		&coroProgramPV1State,
		&coroProgramGV1State,
		&coroProgramExecutorDriverV1State,
	))
}

func coroProgramFailOutcomeV2() coroProgramDriveOutcomeV2 {
	_ = coroProgramFailV1()
	return coroProgramDriveOutcomeV2{status: coroProgramDriveInvalidV2}
}

func coroProgramBeginHostRunV2(outcome coroProgramDriveOutcomeV2) coroProgramDriveOutcomeV2 {
	if coroProgramContinuationV1State != coroProgramContinuationNoneV1 ||
		!coroProgramExecutorBoundV1State {
		return coroProgramFailOutcomeV2()
	}
	epoch, ok := coroProgramPublishContinuationV1(coroProgramContinuationHostRunV2)
	if !ok {
		return coroProgramFailOutcomeV2()
	}
	outcome.status = coroProgramDriveYieldedV2
	outcome.result.Flags = coroProgramRunMoreV2
	if !coroProgramSetOutcomeContinuationV2(&outcome, 0) {
		return coroProgramFailOutcomeV2()
	}
	switch coroTargetBeginExecutorRunV2(coroProgramExecutorHandleV1State, epoch) {
	case coroTargetRunRequestInlineV2:
		outcome.result.Flags |= coroProgramRunRequestInlineV2
	case coroTargetRunRequestQueuedV2:
		outcome.result.Flags |= coroProgramRunRequestQueuedV2
	default:
		return coroProgramFailOutcomeV2()
	}
	return outcome
}

// coroProgramDriveStepV2 executes at most one certified RunSlice. Used counts
// only source/dispatch/resume/destroy transitions certified by that RunSlice;
// compatibility bookkeeping after its boundary is deliberately not charged.
// Any
// compatibility transition that still needs scheduler work is converted into
// HostRun rather than hiding a second slice in this ABI entry.
func coroProgramDriveStepV2(budget uint32) coroProgramDriveOutcomeV2 {
	if budget == 0 ||
		coroProgramContinuationV1State != coroProgramContinuationNoneV1 {
		return coroProgramFailOutcomeV2()
	}
	result := coroFinishRunSliceCompatibility(
		&coroProgramPV1State,
		&coroProgramGV1State,
		&coroProgramExecutorDriverV1State,
		coroRunSlice(
			&coroProgramPV1State,
			&coroProgramGV1State,
			&coroProgramExecutorDriverV1State,
			budget,
		),
	)
	outcome := coroProgramDriveOutcomeV2{
		result:   coroProgramRunResultV2{Used: result.used},
		ranSlice: true,
	}
	switch result.stop {
	case coroRunSliceBudgetV1, coroRunAgainV1:
		return coroProgramBeginHostRunV2(outcome)
	case coroRunInvalidV1:
		return coroProgramFailOutcomeV2()
	}
	mapped := coroProgramOutcomeFromV1(coroProgramHandleRunResultV1(result))
	mapped.result.Used = outcome.result.Used
	mapped.ranSlice = true
	switch mapped.status {
	case coroProgramDriveAgainFreshV2:
		return coroProgramBeginHostRunV2(mapped)
	case coroProgramDriveSuspendedV2:
		mapped.result.Flags |= coroProgramRunBlockedV2
		if !coroProgramSetOutcomeContinuationV2(&mapped, 0) {
			return coroProgramFailOutcomeV2()
		}
		if result.stop == coroRunExecutorSleepV1 {
			coroProgramSetOutcomeDeadlineV2(&mapped, result.deadline, result.hasDeadline)
		}
	}
	return mapped
}

func coroProgramRunOwnedV1(gPointer, handle unsafe.Pointer) coroProgramDriveStatusV1 {
	if coroProgramLifecycleV1State != coroProgramBegunV1 || coroProgramManifestV1State == nil || coroProgramFactoryV1State == nil ||
		gPointer != unsafe.Pointer(&coroProgramGV1State) || handle == nil ||
		!coroProgramExecutorBoundV1State || coroProgramContinuationV1State != coroProgramContinuationNoneV1 {
		return coroProgramFailV1()
	}
	if !coroAdoptRoot(&coroProgramGV1State, handle) || !coroEnqueue(&coroProgramPV1State, &coroProgramGV1State) {
		return coroProgramFailV1()
	}
	if !coroTargetExecutorStartV1(coroProgramExecutorHandleV1State) {
		return coroProgramFailV1()
	}
	coroProgramLifecycleV1State = coroProgramRunningV1
	return coroProgramDriveStepV1()
}

func coroProgramContinueOwnedV1(epoch uint32) coroProgramDriveStatusV1 {
	if epoch == 0 || epoch != coroProgramContinuationEpochV1 ||
		coroProgramContinuationV1State == coroProgramContinuationNoneV1 ||
		coroProgramLifecycleV1State == coroProgramCompleteV1 ||
		coroProgramLifecycleV1State == coroProgramFailedV1 {
		return coroProgramFailV1()
	}
	kind := coroProgramContinuationV1State
	var targetResult coroTargetDispatchResultV1
	switch kind {
	case coroProgramContinuationExecutorWakeV1:
		targetResult = coroTargetPollExecutorWakeV1(coroProgramExecutorHandleV1State, epoch)
	case coroProgramContinuationTerminalJoinV1, coroProgramContinuationCommandJoinV1:
		targetResult = coroTargetPollExecutorCloseV1(coroProgramExecutorHandleV1State, epoch)
	default:
		return coroProgramFailV1()
	}
	switch targetResult {
	case coroTargetDispatchPendingV1:
		return coroProgramDriveSuspendedV1
	case coroTargetDispatchCompleteV1:
	default:
		return coroProgramFailV1()
	}
	switch kind {
	case coroProgramContinuationExecutorWakeV1:
		if !coroProgramWakeExecutorV1(&coroProgramExecutorDriverV1State) ||
			!coroProgramClearContinuationV1(kind) {
			return coroProgramFailV1()
		}
		// coroProgramClearContinuationV1 already invalidated the old admission
		// phase while retaining ownership; later work may now publish a new epoch.
		return coroProgramDriveAgainV1
	case coroProgramContinuationTerminalJoinV1:
		return coroProgramConfirmTerminalJoinV1()
	case coroProgramContinuationCommandJoinV1:
		return coroProgramConfirmCommandJoinV1()
	default:
		return coroProgramFailV1()
	}
}

func coroProgramContinueOwnedV2(handle coro.ExecutorHandle, epoch uint32) coroProgramDriveOutcomeV2 {
	if handle.Slot == 0 || handle.Generation == 0 || handle != coroProgramExecutorHandleV1State {
		return coroProgramDriveOutcomeV2{status: coroProgramDriveIgnoredV2}
	}
	if epoch == 0 || epoch != coroProgramContinuationEpochV1 ||
		coroProgramContinuationV1State == coroProgramContinuationNoneV1 ||
		coroProgramLifecycleV1State == coroProgramCompleteV1 ||
		coroProgramLifecycleV1State == coroProgramFailedV1 {
		return coroProgramFailOutcomeV2()
	}
	if coroProgramContinuationV1State == coroProgramContinuationHostRunV2 {
		if !coroTargetConsumeExecutorRunV2(handle, epoch) ||
			!coroProgramClearContinuationV1(coroProgramContinuationHostRunV2) {
			return coroProgramFailOutcomeV2()
		}
		return coroProgramDriveOutcomeV2{status: coroProgramDriveAgainFreshV2}
	}
	outcome := coroProgramOutcomeFromV1(coroProgramContinueOwnedV1(epoch))
	if outcome.status == coroProgramDriveSuspendedV2 {
		outcome.result.Flags |= coroProgramRunBlockedV2
		if !coroProgramSetOutcomeContinuationV2(&outcome, 0) {
			return coroProgramFailOutcomeV2()
		}
		if coroProgramContinuationV1State == coroProgramContinuationExecutorWakeV1 {
			coroProgramSetOutcomeDeadlineV2(
				&outcome,
				coroProgramContinuationDeadlineV2,
				coroProgramContinuationHasDeadlineV2,
			)
		}
	}
	return outcome
}

// coroProgramFinishDriveAdmissionV1 closes one scheduler-owner episode. A
// callback that raced target Begin or another continuation can only publish the
// atomic Pending bit; this loop claims it before releasing ownership and resumes
// exclusively from the still-published POD epoch.
func coroProgramFinishDriveAdmissionV1(status coroProgramDriveStatusV1) coroProgramDriveStatusV1 {
	for {
		if status == coroProgramDriveAgainV1 {
			// Every Again source has settled a continuation through
			// coroProgramClearContinuationV1, which already advanced the phase
			// while preserving this owner.
			return status
		}
		epoch, pending, ok := coroProgramDriveAdmissionV1State.Finish()
		if !ok {
			coroProgramLifecycleV1State = coroProgramFailedV1
			return coroProgramDriveInvalidV1
		}
		if !pending {
			return status
		}
		if epoch == 0 {
			// The prior owner revoked this epoch while a stale callback was
			// publishing Pending. Ownership is still held; retry release.
			continue
		}
		status = coroProgramContinueOwnedV1(epoch)
	}
}

// coroProgramFinishDriveAdmissionV2 closes one V2 owner episode. A callback
// deferred while BeginRun is still publishing a HostRun request has already
// received Repost from the public ABI. Claim its Pending hint without consuming
// HostRun: the exact epoch remains durable and the target must post the same
// tuple in a later host turn. Other pending continuations retain the V1
// early-completion behavior, but settlement may only return AgainFresh and
// cannot publish a later epoch here.
func coroProgramFinishDriveAdmissionV2(outcome coroProgramDriveOutcomeV2) coroProgramDriveOutcomeV2 {
	for {
		if outcome.status == coroProgramDriveAgainFreshV2 {
			// Every AgainFresh source has settled a continuation through
			// coroProgramClearContinuationV1, which already advanced the phase
			// while preserving this owner.
			return outcome
		}
		epoch, pending, ok := coroProgramDriveAdmissionV1State.Finish()
		if !ok {
			return coroProgramFailOutcomeV2()
		}
		if !pending {
			return outcome
		}
		if epoch == 0 {
			continue
		}
		if outcome.status == coroProgramDriveYieldedV2 &&
			coroProgramContinuationV1State == coroProgramContinuationHostRunV2 {
			continue
		}
		next := coroProgramContinueOwnedV2(coroProgramExecutorHandleV1State, epoch)
		outcome = coroProgramMergeOutcomeV2(outcome, next)
	}
}

// coroProgramFinishFreshDriveV2 runs at most one RunSlice for a public V2
// entry. A continuation callback starts with ranSlice=false and may therefore
// settle, advance the admission phase while retaining ownership, and run one
// slice. If an early physical callback settled after a slice already ran, the
// phase-advanced owner only publishes HostRun.
func coroProgramFinishFreshDriveV2(outcome coroProgramDriveOutcomeV2, budget uint32) coroProgramDriveOutcomeV2 {
	for {
		outcome = coroProgramFinishDriveAdmissionV2(outcome)
		if outcome.status != coroProgramDriveAgainFreshV2 {
			return outcome
		}
		if outcome.ranSlice {
			outcome = coroProgramBeginHostRunV2(outcome)
		} else {
			outcome = coroProgramDriveStepV2(budget)
		}
	}
}

// coroProgramFinishFreshDriveV1 is the legacy whole-program pump with an
// explicit phase boundary between continuation settlement and later scheduler
// work. In particular, E1 is cleared and its admission phase advanced before
// the retained owner may publish E2.
func coroProgramFinishFreshDriveV1(status coroProgramDriveStatusV1) coroProgramDriveStatusV1 {
	for {
		status = coroProgramFinishDriveAdmissionV1(status)
		if status != coroProgramDriveAgainV1 {
			return status
		}
		status = coroProgramDriveStepV1()
	}
}

func coroProgramRunV1(gPointer, handle unsafe.Pointer) coroProgramDriveStatusV1 {
	if !coroProgramDriveAdmissionV1State.Acquire() {
		return coroProgramDriveInvalidV1
	}
	if !coroProgramSelectDriverModeV2(coroProgramDriverModeLegacyV1) {
		return coroProgramFinishDriveAdmissionV1(coroProgramFailV1())
	}
	return coroProgramFinishFreshDriveV1(coroProgramRunOwnedV1(gPointer, handle))
}

func coroProgramContinueV1(epoch uint32) coroProgramDriveStatusV1 {
	switch coroProgramDriveAdmissionV1State.EnterMode(
		uint32(coroProgramDriverModeLegacyV1),
		epoch,
	) {
	case coro.DriveAdmissionAcquired:
		if !coroProgramSelectDriverModeV2(coroProgramDriverModeLegacyV1) {
			return coroProgramFinishDriveAdmissionV1(coroProgramFailV1())
		}
		return coroProgramFinishFreshDriveV1(coroProgramContinueOwnedV1(epoch))
	case coro.DriveAdmissionDeferred:
		return coroProgramDriveSuspendedV1
	case coro.DriveAdmissionStale:
		// A delayed or duplicate host callback carries only its old POD epoch.
		// Reject it without reading or poisoning scheduler-owned lifecycle state.
		return coroProgramDriveIgnoredV1
	default:
		return coroProgramDriveInvalidV1
	}
}

func coroProgramWriteOutcomeV2(out *coroProgramRunResultV2, outcome coroProgramDriveOutcomeV2) uint32 {
	if out == nil {
		return uint32(coroProgramDriveInvalidV2)
	}
	if outcome.status == coroProgramDriveAgainFreshV2 || outcome.status > coroProgramDriveAgainFreshV2 {
		outcome.status = coroProgramDriveInvalidV2
	}
	if outcome.status == coroProgramDriveInvalidV2 || outcome.status == coroProgramDriveIgnoredV2 {
		*out = coroProgramRunResultV2{}
	} else {
		*out = outcome.result
	}
	return uint32(outcome.status)
}

func coroProgramRunSliceV2(
	gPointer, handle unsafe.Pointer,
	budget uint32,
	out *coroProgramRunResultV2,
) uint32 {
	if out == nil {
		return uint32(coroProgramDriveInvalidV2)
	}
	*out = coroProgramRunResultV2{}
	if budget == 0 ||
		!coroProgramDriveAdmissionV1State.Acquire() {
		return uint32(coroProgramDriveInvalidV2)
	}
	if !coroProgramSelectDriverModeV2(coroProgramDriverModeSliceV2) {
		return coroProgramWriteOutcomeV2(
			out,
			coroProgramFinishDriveAdmissionV2(coroProgramFailOutcomeV2()),
		)
	}
	if coroProgramLifecycleV1State != coroProgramBegunV1 ||
		coroProgramManifestV1State == nil || coroProgramFactoryV1State == nil ||
		gPointer != unsafe.Pointer(&coroProgramGV1State) || handle == nil ||
		!coroProgramExecutorBoundV1State ||
		coroProgramContinuationV1State != coroProgramContinuationNoneV1 ||
		!coroAdoptRoot(&coroProgramGV1State, handle) ||
		!coroEnqueue(&coroProgramPV1State, &coroProgramGV1State) ||
		!coroTargetExecutorStartV1(coroProgramExecutorHandleV1State) {
		return coroProgramWriteOutcomeV2(
			out,
			coroProgramFinishDriveAdmissionV2(coroProgramFailOutcomeV2()),
		)
	}
	coroProgramLifecycleV1State = coroProgramRunningV1
	outcome := coroProgramDriveStepV2(budget)
	return coroProgramWriteOutcomeV2(out, coroProgramFinishFreshDriveV2(outcome, budget))
}

func coroProgramContinueSliceV2(
	executorSlot, executorGeneration, epoch, budget uint32,
	out *coroProgramRunResultV2,
) uint32 {
	if out == nil {
		return uint32(coroProgramDriveInvalidV2)
	}
	*out = coroProgramRunResultV2{}
	if executorSlot == 0 || executorGeneration == 0 || epoch == 0 ||
		budget == 0 {
		return uint32(coroProgramDriveInvalidV2)
	}
	handle := coro.ExecutorHandle{Slot: executorSlot, Generation: executorGeneration}
	switch coroProgramDriveAdmissionV1State.EnterExecutorMode(
		executorSlot,
		executorGeneration,
		uint32(coroProgramDriverModeSliceV2),
		epoch,
	) {
	case coro.DriveAdmissionAcquired:
		var outcome coroProgramDriveOutcomeV2
		if !coroProgramSelectDriverModeV2(coroProgramDriverModeSliceV2) {
			outcome = coroProgramFailOutcomeV2()
		} else {
			outcome = coroProgramContinueOwnedV2(handle, epoch)
		}
		return coroProgramWriteOutcomeV2(
			out,
			coroProgramFinishFreshDriveV2(outcome, budget),
		)
	case coro.DriveAdmissionDeferred:
		outcome := coroProgramDriveOutcomeV2{
			status: coroProgramDriveRepostV2,
			result: coroProgramRunResultV2{
				Flags:              coroProgramRunMoreV2 | coroProgramRunRequestQueuedV2,
				ExecutorSlot:       executorSlot,
				ExecutorGeneration: executorGeneration,
				Epoch:              epoch,
			},
		}
		return coroProgramWriteOutcomeV2(out, outcome)
	case coro.DriveAdmissionStale:
		return coroProgramWriteOutcomeV2(
			out,
			coroProgramDriveOutcomeV2{status: coroProgramDriveIgnoredV2},
		)
	default:
		return uint32(coroProgramDriveInvalidV2)
	}
}

func coroProgramMainReturnV1(gPointer unsafe.Pointer) bool {
	if coroProgramLifecycleV1State != coroProgramRunningV1 ||
		gPointer != unsafe.Pointer(&coroProgramGV1State) ||
		!coro.CommandMainReturnPoint(&coroProgramPV1State, &coroProgramGV1State) {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return false
	}
	coroProgramLifecycleV1State = coroProgramMainReturnRequestedV1
	return true
}

//export __llgo_coro_program_begin_v1
func __llgo_coro_program_begin_v1(manifest, expectedFactory unsafe.Pointer) unsafe.Pointer {
	g, ok := coroProgramBeginV1(manifest, expectedFactory)
	if !ok {
		coroRuntimeAbort("invalid coroutine program bootstrap")
		return nil
	}
	return g
}

//export __llgo_coro_program_run_v1
func __llgo_coro_program_run_v1(g, handle unsafe.Pointer) {
	switch coroProgramRunV1(g, handle) {
	case coroProgramDriveCompleteV1, coroProgramDriveSuspendedV1:
		return
	case coroProgramDrivePanicV1:
		coroRuntimeAbort("coroutine program terminated by panic")
	default:
		coroRuntimeAbort("invalid coroutine program execution")
	}
}

// __llgo_coro_program_run_slice_v2 runs at most one bounded scheduler slice.
// Its result is caller-owned POD storage and is never retained across the host
// boundary.
//
//export __llgo_coro_program_run_slice_v2
func __llgo_coro_program_run_slice_v2(
	g, handle unsafe.Pointer,
	budget uint32,
	out *coroProgramRunResultV2,
) uint32 {
	return coroProgramRunSliceV2(g, handle, budget, out)
}

// __llgo_coro_program_continue_v1 is a clean target re-entry after a retained
// wait or asynchronous strong join. The epoch is POD target state; all managed
// continuation ownership remains in static scheduler objects.
//
//export __llgo_coro_program_continue_v1
func __llgo_coro_program_continue_v1(epoch uint32) {
	switch coroProgramContinueV1(epoch) {
	case coroProgramDriveCompleteV1, coroProgramDriveSuspendedV1, coroProgramDriveIgnoredV1:
		return
	case coroProgramDrivePanicV1:
		coroRuntimeAbort("coroutine program terminated by panic")
	default:
		coroRuntimeAbort("invalid coroutine program continuation")
	}
}

// __llgo_coro_program_continue_slice_v2 is the versioned host re-entry for an
// exact executor tuple and continuation epoch. Deferred callers receive
// Repost; they never recurse or spin inside the scheduler.
//
//export __llgo_coro_program_continue_slice_v2
func __llgo_coro_program_continue_slice_v2(
	executorSlot, executorGeneration, epoch, budget uint32,
	out *coroProgramRunResultV2,
) uint32 {
	return coroProgramContinueSliceV2(
		executorSlot,
		executorGeneration,
		epoch,
		budget,
		out,
	)
}

//export __llgo_coro_program_main_return_v1
func __llgo_coro_program_main_return_v1(g unsafe.Pointer) {
	if !coroProgramMainReturnV1(g) {
		coroRuntimeAbort("invalid coroutine command main return")
	}
}
