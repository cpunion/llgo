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

type coroProgramContinuationV1 uint8

const (
	coroProgramContinuationNoneV1 coroProgramContinuationV1 = iota
	coroProgramContinuationExecutorWakeV1
	coroProgramContinuationTerminalJoinV1
	coroProgramContinuationCommandJoinV1
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
	coroProgramLifecycleV1State      coroProgramLifecycleV1
	coroProgramManifestV1State       *coro.ProgramManifestV1
	coroProgramFactoryV1State        unsafe.Pointer
	coroProgramGV1State              coroG
	coroProgramPV1State              coroP
	coroProgramContinuationV1State   coroProgramContinuationV1
	coroProgramContinuationEpochV1   uint32
	coroProgramDriveAdmissionV1State coro.DriveAdmission
)

func coroProgramFailV1() coroProgramDriveStatusV1 {
	_ = coroProgramDriveAdmissionV1State.RevokeEpoch()
	coroProgramLifecycleV1State = coroProgramFailedV1
	return coroProgramDriveInvalidV1
}

func coroProgramPublishContinuationV1(kind coroProgramContinuationV1) (uint32, bool) {
	if kind == coroProgramContinuationNoneV1 || coroProgramContinuationV1State != coroProgramContinuationNoneV1 ||
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
	return true
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
	if !ok || g != &coroProgramGV1State || !coroProgramExecutorRetiredV1() ||
		!coroProgramClearContinuationV1(coroProgramContinuationTerminalJoinV1) {
		return coroProgramFailV1()
	}
	switch action.Kind {
	case coro.ActionComplete:
		if action.Handle != nil || !coroReleaseCompletedTask(g) {
			return coroProgramFailV1()
		}
		return coroProgramFinishMainV1()
	case coro.ActionPanicComplete:
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

func coroProgramDriveStepV1() coroProgramDriveStatusV1 {
	if coroProgramContinuationV1State != coroProgramContinuationNoneV1 {
		return coroProgramFailV1()
	}
	result := coroRun(
		&coroProgramPV1State,
		&coroProgramGV1State,
		&coroProgramExecutorDriverV1State,
	)
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

func coroProgramDriveV1() coroProgramDriveStatusV1 {
	for {
		status := coroProgramDriveStepV1()
		if status != coroProgramDriveAgainV1 {
			return status
		}
	}
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
	return coroProgramDriveV1()
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
		return coroProgramDriveV1()
	case coroProgramContinuationTerminalJoinV1:
		return coroProgramConfirmTerminalJoinV1()
	case coroProgramContinuationCommandJoinV1:
		return coroProgramConfirmCommandJoinV1()
	default:
		return coroProgramFailV1()
	}
}

// coroProgramFinishDriveAdmissionV1 closes one scheduler-owner episode. A
// callback that raced target Begin or another continuation can only publish the
// atomic Pending bit; this loop claims it before releasing ownership and resumes
// exclusively from the still-published POD epoch.
func coroProgramFinishDriveAdmissionV1(status coroProgramDriveStatusV1) coroProgramDriveStatusV1 {
	for {
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

func coroProgramRunV1(gPointer, handle unsafe.Pointer) coroProgramDriveStatusV1 {
	if !coroProgramDriveAdmissionV1State.Acquire() {
		return coroProgramDriveInvalidV1
	}
	return coroProgramFinishDriveAdmissionV1(coroProgramRunOwnedV1(gPointer, handle))
}

func coroProgramContinueV1(epoch uint32) coroProgramDriveStatusV1 {
	switch coroProgramDriveAdmissionV1State.Enter(epoch) {
	case coro.DriveAdmissionAcquired:
		return coroProgramFinishDriveAdmissionV1(coroProgramContinueOwnedV1(epoch))
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

//export __llgo_coro_program_main_return_v1
func __llgo_coro_program_main_return_v1(g unsafe.Pointer) {
	if !coroProgramMainReturnV1(g) {
		coroRuntimeAbort("invalid coroutine command main return")
	}
}
