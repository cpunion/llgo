//go:build llgo && llgo_coro && llgo_coro_native_pipe && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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

	"github.com/goplus/llgo/runtime/internal/clite/tls"
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/corofleet"
	"github.com/goplus/llgo/runtime/internal/coroworker"
)

// coroNativeForeignBoundaryV1 is native-stack storage for one synchronous
// foreign call. It centralizes the only detach/replacement/strong-join
// lifecycle used by both a LockOSThread call and a compiler-selected same-M
// call, with or without managed reentry. Each nested call owns a distinct
// value; no P-local resume slot or function-address registry is involved.
type coroNativeForeignBoundaryV1 struct {
	resume coro.ExecutorResumeHandoff

	driver      *coro.ExecutorDriver
	task        *coro.G
	parent      *coroNativeMOwnerV1
	domain      *coroNativeFleetDomainV1
	replacement *coroNativeMOwnerV1

	parentSlot         uint32
	replacementSlot    uint32
	ownerEpoch         uint32
	baton              coro.ExecutionDomainHandoffHandle
	replacementQueued  bool
	replacementSkipped bool
	callbackAcquired   bool
	active             bool
}

var (
	coroNativeForeignBoundaryTLSV1      tls.StaticHandle[*coroNativeForeignBoundaryV1]
	coroNativeForeignBoundaryTLSReadyV1 bool
)

// coroNativeForeignBoundaryTLSStartV1 makes the process-global pthread key an
// explicit part of native-fleet startup. Runtime-only archives and other
// section-garbage-collected library links are not required to retain or invoke
// an otherwise anonymous package initializer merely because managed C
// callbacks need TLS later.
//
// Native-fleet startup is serialized and one-shot. Keep this operation
// idempotent so a later startup precondition may fail without turning the
// already-created program-lifetime key into a retry hazard.
func coroNativeForeignBoundaryTLSStartV1() bool {
	if coroNativeForeignBoundaryTLSReadyV1 {
		return true
	}
	coroNativeForeignBoundaryTLSV1 =
		tls.AllocStatic[*coroNativeForeignBoundaryV1]()
	coroNativeForeignBoundaryTLSReadyV1 = true
	return true
}

func (boundary *coroNativeForeignBoundaryV1) startReplacementV1(
	releaseManaged bool,
) bool {
	if boundary == nil || !boundary.active || boundary.driver == nil ||
		boundary.parent == nil || boundary.domain == nil ||
		boundary.replacement != nil || boundary.replacementSlot != 0 ||
		boundary.baton.Valid() || boundary.replacementQueued ||
		boundary.replacementSkipped {
		return false
	}
	baton, begun := boundary.parent.handoff.Begin(boundary.ownerEpoch)
	if !begun {
		return false
	}
	slot, replacement, allocated := coroNativeMAllocateReplacementV1(
		boundary.parentSlot,
		boundary.domain.handle,
		baton,
	)
	if !allocated {
		rolledBack := boundary.parent.handoff.RequestReturn(baton) ==
			coro.ExecutionDomainHandoffReturnUnclaimed &&
			boundary.parent.handoff.Complete(baton)
		_ = rolledBack
		return false
	}
	if releaseManaged && !coroTargetReleaseManagedExecutionV1(boundary.driver) {
		// Release may have already dropped the quota before a required waiter
		// doorbell failed. Its boolean result therefore cannot authorize restoring
		// the detached resume or releasing the handoff as though the lease were
		// still held. Fail closed instead of creating two physical owners for one P.
		coroRuntimeAbort("native direct foreign execution quota release failed")
		return false
	}
	queued, started := coroNativeMRequestPhysicalOwnerV1(replacement, slot)
	if !started {
		replacement.thread = nil
		replacement.token = 0
		rollback := boundary.parent.handoff.RequestReturn(baton) ==
			coro.ExecutionDomainHandoffReturnUnclaimed &&
			boundary.parent.handoff.Complete(baton) &&
			coroNativeMReleaseUnstartedReplacementV1(slot)
		if releaseManaged {
			rollback = coroTargetReenterManagedExecutionV1(boundary.driver) &&
				rollback
		}
		_ = rollback
		return false
	}
	boundary.replacement = replacement
	boundary.replacementSlot = slot
	boundary.baton = baton
	boundary.replacementQueued = queued
	return true
}

func (boundary *coroNativeForeignBoundaryV1) beginV1(
	task *coro.G,
	mode coro.ExecutorResumeHandoffMode,
	lazyCompensation bool,
) bool {
	if boundary == nil || boundary.active || boundary.driver != nil ||
		boundary.task != nil || boundary.parent != nil ||
		boundary.domain != nil || boundary.replacement != nil ||
		boundary.parentSlot != 0 || boundary.replacementSlot != 0 ||
		boundary.ownerEpoch != 0 || boundary.baton.Valid() ||
		boundary.replacementQueued || boundary.replacementSkipped ||
		boundary.callbackAcquired {
		return false
	}
	driver, _, _, ownerOK := coro.CurrentExecutorDriver(task)
	parent, domain, parentSlot, ownerEpoch, physicalOK :=
		coroNativeMCurrentOwnerV1(driver)
	if !ownerOK || !physicalOK ||
		!coro.DetachExecutorResume(&boundary.resume, driver, task, mode) {
		return false
	}
	boundary.driver = driver
	boundary.task = task
	boundary.parent = parent
	boundary.domain = domain
	boundary.parentSlot = parentSlot
	boundary.ownerEpoch = ownerEpoch
	boundary.active = true
	if lazyCompensation {
		required, demandOK :=
			coro.ExecutorResumeHandoffCompensationRequired(&boundary.resume)
		if demandOK && !required {
			if !coroTargetReleaseManagedExecutionV1(boundary.driver) {
				// See startReplacementV1: a false result does not prove that the
				// quota release itself failed before publication.
				coroRuntimeAbort("native direct lazy execution quota release failed")
				return false
			}
			boundary.replacementSkipped = true
			return true
		}
		if !demandOK || !required {
			restored := coro.RestoreExecutorResume(&boundary.resume)
			boundary.driver = nil
			boundary.task = nil
			boundary.parent = nil
			boundary.domain = nil
			boundary.parentSlot = 0
			boundary.ownerEpoch = 0
			boundary.active = false
			_ = restored
			return false
		}
	}
	if boundary.startReplacementV1(true) {
		return true
	}
	restored := coro.RestoreExecutorResume(&boundary.resume)
	boundary.driver = nil
	boundary.task = nil
	boundary.parent = nil
	boundary.domain = nil
	boundary.parentSlot = 0
	boundary.ownerEpoch = 0
	boundary.active = false
	_ = restored
	return false
}

func (boundary *coroNativeForeignBoundaryV1) reclaimReplacementV1() bool {
	if boundary == nil || !boundary.active || boundary.driver == nil ||
		boundary.parent == nil || boundary.domain == nil ||
		boundary.replacement == nil || boundary.replacementSlot == 0 ||
		!boundary.baton.Valid() || boundary.replacementSkipped {
		return false
	}
	if boundary.replacementQueued {
		released, stillQueued := boundary.parent.handoff.Released()
		if stillQueued && released != boundary.baton {
			coroRuntimeAbort("native direct queued replacement handoff mismatch")
		}
		if stillQueued {
			switch corofleet.CancelReuseOwner(
				boundary.replacement.thread,
				boundary.replacement.token,
				boundary.replacementSlot,
			) {
			case 0:
				boundary.replacement.thread = nil
				boundary.replacement.token = 0
				withdrawn := boundary.parent.handoff.RequestReturn(boundary.baton) ==
					coro.ExecutionDomainHandoffReturnUnclaimed &&
					boundary.parent.handoff.Complete(boundary.baton) &&
					coroNativeMReleaseUnstartedReplacementV1(boundary.replacementSlot)
				if !withdrawn {
					coroRuntimeAbort("native direct queued replacement withdrawal failed")
				}
				boundary.replacement = nil
				boundary.replacementSlot = 0
				boundary.baton = coro.ExecutionDomainHandoffHandle{}
				boundary.replacementQueued = false
				return true
			case 1:
				// Dispatch won between Released and cancellation. The handoff race
				// below decides whether it claimed or only acknowledges return.
			default:
				// Claim and an immediate retirement may consume both the Released
				// phase and the original C token between our snapshot and cancel.
				// Only a still-live exact Released publication makes the failed
				// cancellation an invariant error.
				current, releasedNow := boundary.parent.handoff.Released()
				if releasedNow && current == boundary.baton {
					coroRuntimeAbort("native direct replacement cancel state invalid")
				}
			}
		}
		// Once Claim has consumed Released, the C record may already have
		// retired into a clean successor and its original token is no longer a
		// cancel capability. The handoff generation is then the sole authority.
		boundary.replacementQueued = false
	}
	returnResult := boundary.parent.handoff.RequestReturn(boundary.baton)
	if returnResult == coro.ExecutionDomainHandoffReturnUnclaimed {
		if !coroNativeMWaitAndRecycleOSThreadSuspendV1(
			boundary.replacementSlot,
			boundary.replacement,
			boundary.parent,
		) {
			coroRuntimeAbort("native direct revoked replacement recycle failed")
		}
		if !boundary.parent.handoff.Complete(boundary.baton) {
			coroRuntimeAbort("native direct revoked handoff completion failed")
		}
		boundary.replacement = nil
		boundary.replacementSlot = 0
		boundary.baton = coro.ExecutionDomainHandoffHandle{}
		boundary.replacementQueued = false
		return true
	}
	returnRequested := returnResult == coro.ExecutionDomainHandoffReturnClaimed
	alreadyReturned := false
	if returnResult == coro.ExecutionDomainHandoffReturnInvalid {
		returnRequested = boundary.parent.handoff.ReturnRequested(boundary.baton)
		alreadyReturned = boundary.parent.handoff.Returned(boundary.baton)
	}
	request := coro.ExecutorRequestInvalid
	if returnRequested {
		// The return baton is the durable fact. The executor request is its
		// preemption transport: a replacement may currently be inside an
		// unbounded managed resume and cannot observe a doorbell until it first
		// reaches the compiler safepoint gate.
		request = coroNativeFleetV1State.fleet.RequestExecutor(boundary.domain.handle)
	}
	ringOK := returnRequested &&
		coro.ExecutorRequestAccepted(request) &&
		boundary.domain.doorbell.Ring()
	if !returnRequested && !alreadyReturned {
		coroRuntimeAbort("native direct replacement return state invalid")
	}
	if returnRequested && !coro.ExecutorRequestAccepted(request) {
		coroRuntimeAbort("native direct replacement preemption request failed")
	}
	if returnRequested && !ringOK {
		coroRuntimeAbort("native direct replacement doorbell failed")
	}
	for returnRequested && ringOK && !boundary.parent.handoff.Returned(boundary.baton) {
		if corofleet.Yield() != 0 {
			ringOK = false
		}
	}
	returnedSlot, returnedOwner, returned := coroNativeMReplacementLineageOwnerV1(
		boundary.replacementSlot,
		boundary.replacement,
		boundary.parent,
		boundary.baton,
	)
	returned = returned && returnedOwner.thread != nil &&
		coroNativeAtomicLoadV1(
			&coroNativeMDirectoryV1State.active[boundary.domain.handle.Route-1],
		) == boundary.parentSlot
	if returnRequested && !ringOK {
		coroRuntimeAbort("native direct replacement return wait failed")
	}
	if !returned {
		coroRuntimeAbort("native direct replacement lineage return failed")
	}
	if !coroNativeMRecycleReplacementV1(returnedSlot) {
		coroRuntimeAbort("native direct claimed replacement recycle failed")
	}
	if !boundary.parent.handoff.Complete(boundary.baton) {
		coroRuntimeAbort("native direct claimed handoff completion failed")
	}
	boundary.replacement = nil
	boundary.replacementSlot = 0
	boundary.baton = coro.ExecutionDomainHandoffHandle{}
	boundary.replacementQueued = false
	return true
}

func (boundary *coroNativeForeignBoundaryV1) restartReplacementV1() bool {
	return boundary != nil && boundary.active &&
		boundary.startReplacementV1(false)
}

func (boundary *coroNativeForeignBoundaryV1) finishV1() bool {
	if boundary == nil || boundary.callbackAcquired {
		return false
	}
	if !boundary.replacementSkipped && !boundary.reclaimReplacementV1() {
		coroRuntimeAbort("native direct foreign replacement reclaim failed")
	}
	boundary.replacementSkipped = false
	if !coroTargetReenterManagedExecutionV1(boundary.driver) {
		coroRuntimeAbort("native direct foreign execution quota reentry failed")
	}
	if !coro.RestoreExecutorResume(&boundary.resume) {
		coroRuntimeAbort("native direct foreign resume restore failed")
	}
	boundary.driver = nil
	boundary.task = nil
	boundary.parent = nil
	boundary.domain = nil
	boundary.parentSlot = 0
	boundary.ownerEpoch = 0
	boundary.active = false
	return true
}

func coroNativeForeignBoundarySetTLSV1(
	boundary *coroNativeForeignBoundaryV1,
) (previous *coroNativeForeignBoundaryV1, ok bool) {
	if boundary == nil || !boundary.active ||
		!coroNativeForeignBoundaryTLSReadyV1 {
		return nil, false
	}
	previous = coroNativeForeignBoundaryTLSV1.Get()
	coroNativeForeignBoundaryTLSV1.Set(boundary)
	return previous, coroNativeForeignBoundaryTLSV1.Get() == boundary
}

func coroNativeForeignBoundaryRestoreTLSV1(
	boundary, previous *coroNativeForeignBoundaryV1,
) bool {
	if boundary == nil || !coroNativeForeignBoundaryTLSReadyV1 ||
		coroNativeForeignBoundaryTLSV1.Get() != boundary {
		return false
	}
	if previous == nil {
		coroNativeForeignBoundaryTLSV1.Clear()
	} else {
		coroNativeForeignBoundaryTLSV1.Set(previous)
	}
	return coroNativeForeignBoundaryTLSV1.Get() == previous
}

// __llgo_coro_foreign_reentry_acquire_v1 reclaims the replacement M before a
// compiler-generated raw C callback constructs its exact typed coroutine ramp.
// parentOut is a compiler-only handle identity used to initialize HeaderV1;
// neither value identifies the callback target.
//
//export __llgo_coro_foreign_reentry_acquire_v1
func __llgo_coro_foreign_reentry_acquire_v1(parentOut *unsafe.Pointer) unsafe.Pointer {
	if !coroNativeForeignBoundaryTLSReadyV1 {
		coroRuntimeAbort("synchronous foreign callback TLS is unavailable")
	}
	boundary := coroNativeForeignBoundaryTLSV1.Get()
	if parentOut == nil || boundary == nil || !boundary.active ||
		boundary.callbackAcquired || !boundary.reclaimReplacementV1() {
		coroRuntimeAbort("invalid synchronous foreign callback acquire")
	}
	task, parent, ok := coro.ExecutorResumeHandoffContext(&boundary.resume)
	if !ok || task != boundary.task || parent == nil {
		coroRuntimeAbort("synchronous foreign callback lost parent context")
	}
	boundary.callbackAcquired = true
	*parentOut = parent
	return unsafe.Pointer(task)
}

func coroNativeForeignReentryRunV1(
	boundary *coroNativeForeignBoundaryV1,
	child unsafe.Pointer,
) coro.CompletionSnapshot {
	if boundary == nil || !boundary.active || !boundary.callbackAcquired ||
		boundary.replacement != nil || boundary.replacementSlot != 0 ||
		boundary.baton.Valid() || boundary.replacementQueued ||
		boundary.replacementSkipped || child == nil {
		coroRuntimeAbort("invalid synchronous foreign callback child")
	}
	var record coro.ForeignReentryRecord
	if !coro.BeginForeignReentry(&record, &boundary.resume, child) {
		coroRuntimeAbort("cannot begin synchronous foreign callback child")
	}
	for {
		now, clockOK := coroNativeFleetPhysicalOwnerClockV1()
		if !clockOK {
			coroRuntimeAbort("synchronous foreign callback monotonic clock failed")
		}
		result := coroRunSliceAtV1(
			boundary.domain.pOwnerV1(),
			boundary.driver,
			now,
			coroNativeFleetRunBudgetV1,
		)
		switch result.stop {
		case coroRunSliceBudgetV1, coroRunAgainV1:
			continue
		case coroRunExecutionWaitV1:
			if !coroTargetWaitManagedExecutionV1(boundary.driver) {
				coroRuntimeAbort("synchronous foreign callback execution wait failed")
			}
		case coroRunOSThreadSuspendV1:
			if result.g != boundary.task ||
				!coroTargetHandleOSThreadSuspendV1(
					boundary.domain.pOwnerV1(),
					boundary.driver,
					result.g,
					result.action,
				) {
				coroRuntimeAbort("synchronous foreign callback suspension handoff failed")
			}
		case coroRunForeignReentryCompleteV1:
			if result.g != boundary.task ||
				result.action != (coro.Action{Kind: coro.ActionForeignReentryComplete}) {
				coroRuntimeAbort("synchronous foreign callback completion receipt invalid")
			}
			snapshot, consumed := coro.ConsumeForeignReentryCompletion(&record)
			if !consumed || !boundary.restartReplacementV1() {
				coroRuntimeAbort("synchronous foreign callback completion reconciliation failed")
			}
			boundary.callbackAcquired = false
			return snapshot
		default:
			coroRuntimeAbort("synchronous foreign callback runner stopped unexpectedly")
		}
	}
}

// __llgo_coro_foreign_reentry_run_v1 drives the exact callback child through
// the ordinary timer/channel/I/O/cancellation scheduler and returns its
// parent-owned completion snapshot. A non-return status is left explicit for
// the generated boundary adapter; it may not be silently converted into a C
// return.
//
//export __llgo_coro_foreign_reentry_run_v1
func __llgo_coro_foreign_reentry_run_v1(
	child unsafe.Pointer,
	typeOut, dataOut *unsafe.Pointer,
) uint32 {
	if typeOut == nil || dataOut == nil ||
		!coroNativeForeignBoundaryTLSReadyV1 {
		coroRuntimeAbort("invalid synchronous foreign callback result output")
	}
	boundary := coroNativeForeignBoundaryTLSV1.Get()
	snapshot := coroNativeForeignReentryRunV1(boundary, child)
	*typeOut = snapshot.TypeWord
	*dataOut = snapshot.DataWord
	return uint32(snapshot.Status)
}

// __llgo_coro_foreign_reentry_failure_v1 is the fail-closed MVP outcome
// boundary. Return is reconstructed by the generated adapter; panic, Goexit
// and cancellation require an explicit cross-C transport before they may be
// made observable as ordinary Go control flow. They must never silently return
// through the C ABI in the meantime.
//
//export __llgo_coro_foreign_reentry_failure_v1
func __llgo_coro_foreign_reentry_failure_v1(
	status uint32,
	typeWord, dataWord unsafe.Pointer,
) {
	_, _, _ = status, typeWord, dataWord
	coroRuntimeAbort("synchronous foreign callback completed without returning")
}

// __llgo_coro_same_m_foreign_call_v1 invokes one compiler-generated typed
// thunk on the current native M. The thunk owns C ABI argument/result layout
// in its typed frame-local record; runtime sees only its address and one opaque
// record word. A clean replacement owns the released P whenever execution is
// inside C. The same boundary serves caller-thread declarations with no
// callback and declarations with exact compiler-generated callback adapters.
//
//export __llgo_coro_same_m_foreign_call_v1
func __llgo_coro_same_m_foreign_call_v1(
	g unsafe.Pointer,
	thunk, record uintptr,
) {
	task := (*coro.G)(g)
	if thunk == 0 || record == 0 {
		coroRuntimeAbort("invalid same-M foreign call")
	}
	var boundary coroNativeForeignBoundaryV1
	if !boundary.beginV1(task, coro.ExecutorResumeHandoffSameMForeign, false) {
		coroRuntimeAbort("same-M foreign call cannot detach active resume")
	}
	previous, installed := coroNativeForeignBoundarySetTLSV1(&boundary)
	if !installed {
		coroRuntimeAbort("same-M foreign call cannot publish callback context")
	}
	args := [coroworker.MaxArgs]uintptr{record}
	var result coroworker.Result
	// Managed reentry cannot be abandoned by a signal longjmp. The zero trace
	// target deliberately disables the worker fault landing pad for this exact
	// boundary; reentry faults retain the process signal disposition.
	callOK := coroworker.Call(thunk, 0, 1, &args, &result)
	if !coroNativeForeignBoundaryRestoreTLSV1(&boundary, previous) {
		coroRuntimeAbort("same-M foreign call cannot restore callback context")
	}
	if !boundary.finishV1() {
		coroRuntimeAbort("same-M foreign call cannot restore active resume")
	}
	if !callOK {
		coroRuntimeAbort("same-M foreign call thunk failed")
	}
}

func coroNativeForeignWordCallV1(
	task *coro.G,
	mode coro.ExecutorResumeHandoffMode,
	lazyCompensation bool,
	function, traceTarget uintptr,
	argc uint32,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
	r1, r2, errno *uintptr,
) uint32 {
	if function == 0 || traceTarget == 0 || argc > coroworker.MaxArgs ||
		r1 == nil || r2 == nil || errno == nil ||
		r1 == r2 || r1 == errno || r2 == errno ||
		(mode != coro.ExecutorResumeHandoffLockedForeign &&
			mode != coro.ExecutorResumeHandoffSameMForeign) ||
		lazyCompensation && mode != coro.ExecutorResumeHandoffSameMForeign {
		coroRuntimeAbort("invalid native direct foreign call")
	}
	var boundary coroNativeForeignBoundaryV1
	if !boundary.beginV1(task, mode, lazyCompensation) {
		coroRuntimeAbort("native direct foreign call cannot detach active resume")
	}
	args := [coroworker.MaxArgs]uintptr{a0, a1, a2, a3, a4, a5, a6, a7, a8}
	var result coroworker.Result
	callOK := coroworker.Call(function, traceTarget, argc, &args, &result)
	if !boundary.finishV1() {
		coroRuntimeAbort("native direct foreign call cannot reacquire managed execution")
	}
	if !callOK {
		coroRuntimeAbort("native direct foreign call failed")
	}
	if result.Fault != coroworker.FaultNone {
		if !StoreCoroWorkerFaultPCs(task, result.FaultPC, result.FaultTarget) {
			coroRuntimeAbort("native direct foreign fault has no traceback identity")
		}
		switch result.Fault {
		case coroworker.FaultMemory:
			return coroWorkerResumeFaultMemoryV1
		case coroworker.FaultDivide:
			return coroWorkerResumeFaultDivideV1
		default:
			coroRuntimeAbort("native direct foreign call returned unknown fault")
		}
	}
	*r1, *r2, *errno = result.R1, result.R2, result.Errno
	return coroWorkerResumeSuccessV1
}

// __llgo_coro_native_syscall_call_v1 is the native entersyscall/exitsyscall
// boundary for compiler-certified llgo.syscall calls. The current M performs
// the syscall directly after releasing its execution domain. The detached
// scheduler boundary requests a cached clean M only when independently
// progressing route work already requires compensation; a quick return can
// still cancel that request before dispatch, while a blocking syscall lets a
// dispatch winner claim and service the route.
//
//export __llgo_coro_native_syscall_call_v1
func __llgo_coro_native_syscall_call_v1(
	g unsafe.Pointer,
	function, traceTarget uintptr,
	argc uint32,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
	r1, r2, errno *uintptr,
) uint32 {
	return coroNativeForeignWordCallV1(
		(*coro.G)(g),
		coro.ExecutorResumeHandoffSameMForeign,
		true,
		function, traceTarget, argc,
		a0, a1, a2, a3, a4, a5, a6, a7, a8,
		r1, r2, errno,
	)
}

// __llgo_coro_os_thread_foreign_call_v1 is the LockOSThread form of the same
// direct blocking boundary. The dynamic guard preserves the G-to-M contract;
// its compensation and exact return protocol are shared with native syscalls.
//
//export __llgo_coro_os_thread_foreign_call_v1
func __llgo_coro_os_thread_foreign_call_v1(
	g unsafe.Pointer,
	function, traceTarget uintptr,
	argc uint32,
	a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
	r1, r2, errno *uintptr,
) uint32 {
	task := (*coro.G)(g)
	if !coro.CurrentOSThreadLocked(task) {
		coroRuntimeAbort("invalid locked-thread foreign call")
	}
	return coroNativeForeignWordCallV1(
		task,
		coro.ExecutorResumeHandoffLockedForeign,
		false,
		function, traceTarget, argc,
		a0, a1, a2, a3, a4, a5, a6, a7, a8,
		r1, r2, errno,
	)
}
