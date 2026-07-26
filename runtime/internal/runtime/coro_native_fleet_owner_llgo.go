//go:build llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test

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
	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/pthread"
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/coroclock"
	"github.com/goplus/llgo/runtime/internal/corofleet"
)

const (
	coroNativeFleetPeerIndexV1 uint32 = 1
	coroNativeFleetRunBudgetV1 uint32 = 64
)

type coroNativeFleetPhysicalLifecycleV1 uint8

const (
	coroNativeFleetPhysicalUnusedV1 coroNativeFleetPhysicalLifecycleV1 = iota
	coroNativeFleetPhysicalActiveV1
	coroNativeFleetPhysicalStoppingV1
	coroNativeFleetPhysicalRetiredV1
	coroNativeFleetPhysicalFailedV1
)

// coroNativeFleetPhysicalOwnerV1 contains only the one process-lifetime peer
// M and its atomic stop word. TargetIngress is reused here as a one-writer,
// one-reader seal: no callback enters it, so Quiesced is the acquire-visible
// stop observation and Retire leaves a permanent non-reusable tombstone.
type coroNativeFleetPhysicalOwnerV1 struct {
	stop      coro.TargetIngress
	thread    pthread.Thread
	handle    coro.ExecutorFleetHandle
	shutdown  bool
	lifecycle coroNativeFleetPhysicalLifecycleV1
}

var coroNativeFleetPhysicalOwnerV1State coroNativeFleetPhysicalOwnerV1

func coroNativeFleetPhysicalOwnerStartV1() bool {
	state := &coroNativeFleetPhysicalOwnerV1State
	handle, ok := coroNativeFleetHandleV1(coroNativeFleetPeerIndexV1)
	if !ok || state.lifecycle != coroNativeFleetPhysicalUnusedV1 || state.thread != nil ||
		state.handle != (coro.ExecutorFleetHandle{}) || state.shutdown || !state.stop.CanReleaseResources() ||
		!state.stop.Start() {
		return false
	}
	state.handle = handle
	state.lifecycle = coroNativeFleetPhysicalActiveV1
	if corofleet.CreatePeer(&state.thread) == 0 && state.thread != nil {
		return true
	}
	// pthread_create leaves the result slot unspecified on failure. The whole
	// fleet startup is single-use, so retire the stop word and make failure a
	// permanent state instead of trying to recycle an ambiguous native handle.
	state.thread = nil
	state.lifecycle = coroNativeFleetPhysicalFailedV1
	sealed := state.stop.Seal()
	quiesced := sealed && state.stop.Quiesced()
	retired := quiesced && state.stop.Retire()
	if !retired {
		coroRuntimeAbort("native coroutine fleet peer start rollback failed")
	}
	return false
}

func coroNativeFleetPhysicalOwnerStopV1() bool {
	state := &coroNativeFleetPhysicalOwnerV1State
	if state.lifecycle != coroNativeFleetPhysicalActiveV1 || state.thread == nil ||
		!state.handle.Valid() || !state.stop.Seal() {
		return false
	}
	state.lifecycle = coroNativeFleetPhysicalStoppingV1
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		state.handle,
		coroNativeFleetDomainActiveV1,
	)
	if !ok || !domain.doorbell.Ring() {
		state.lifecycle = coroNativeFleetPhysicalFailedV1
		return false
	}
	var result c.Pointer
	joined := pthread.Join(state.thread, &result) == 0
	state.thread = nil
	if !joined || result != nil || !state.stop.Quiesced() || !state.stop.Retire() {
		state.lifecycle = coroNativeFleetPhysicalFailedV1
		return false
	}
	state.lifecycle = coroNativeFleetPhysicalRetiredV1
	return true
}

func coroNativeFleetPhysicalOwnerStoppingV1(handle coro.ExecutorFleetHandle) bool {
	state := &coroNativeFleetPhysicalOwnerV1State
	return state.handle == handle && state.stop.Quiesced()
}

func coroNativeFleetPhysicalOwnerClockV1() (int64, bool) {
	return coroclock.MonotonicNano()
}

func coroNativeFleetPhysicalOwnerFailV1(message string) bool {
	coroRuntimeAbort(message)
	return false
}

// coroNativeFleetPhysicalOwnerBeginShutdownV1 is intentionally repeatable.
// Cancellation cleanup may legally spawn another task; rescanning at each
// stable reduction boundary gives that child the same sticky Shutdown before
// the domain is allowed to report drained.
func coroNativeFleetPhysicalOwnerBeginShutdownV1(handle coro.ExecutorFleetHandle, epoch uint32) (bool, bool) {
	state := &coroNativeFleetPhysicalOwnerV1State
	if state.handle != handle || !coroNativeFleetPhysicalOwnerStoppingV1(handle) {
		return false, false
	}
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !ok || domain.ownerEpoch != epoch {
		return false, false
	}
	driver := domain.driverOwnerV1()
	if driver == nil || !coro.EnterExecutorRunCompatibility(driver) {
		// A bounded source transaction may still own its cursor. The caller must
		// finish that transaction and retry at the next stable reduction boundary.
		return false, true
	}
	if _, requested := coro.RequestExecutorShutdownDrain(driver); !requested {
		return false, false
	}
	state.shutdown = true
	return true, true
}

func coroNativeFleetPhysicalOwnerShutdownReadyV1(handle coro.ExecutorFleetHandle) bool {
	state := &coroNativeFleetPhysicalOwnerV1State
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !ok || state.handle != handle || !state.shutdown || domain.driverOwnerV1() == nil ||
		!coro.EnterExecutorRunCompatibility(domain.driverOwnerV1()) {
		return false
	}
	return coro.ExecutorShutdownDrained(domain.driverOwnerV1())
}

// coroNativeFleetRunPhysicalOwnerPassV1 owns exactly one outer scheduler
// iteration. Keep this as a separate physical call frame: LLVM currently
// materializes aggregate result slots at their lexical call site, and an
// aggregate-return call directly inside an unbounded loop would otherwise
// lower to a dynamic alloca on every backedge. Returning between passes makes
// those bounded scratch slots reclaimable without increasing the pthread stack.
func coroNativeFleetRunPhysicalOwnerPassV1(
	handle coro.ExecutorFleetHandle,
	epoch *uint32,
	done *bool,
) bool {
	if epoch == nil || *epoch == 0 || done == nil {
		return coroNativeFleetPhysicalOwnerFailV1("native fleet peer epoch invalid")
	}
	*done = false
	_, _, drainStatus := coroNativeFleetTryDrainOwnerEpochV1(
		handle,
		*epoch,
		coro.RunnableTransferMailboxCapacity,
	)
	switch drainStatus {
	case coro.RunnableTransferDrainOwnerUnstable:
		// A bounded slice may end after Dispatch and before its indivisible
		// physical Action. Resume the reducer immediately; if this was not a
		// valid pending action, its exact driver validation fails closed.
	case coro.RunnableTransferDrainContended:
		return true
	case coro.RunnableTransferDrainComplete:
	case coro.RunnableTransferDrainCorrupt:
		return coroNativeFleetPhysicalOwnerFailV1("native fleet peer mailbox corrupt")
	case coro.RunnableTransferDrainInvalid:
		return coroNativeFleetPhysicalOwnerFailV1("native fleet peer mailbox drain failed")
	}
	if coroNativeFleetPhysicalOwnerStoppingV1(handle) {
		if _, retry := coroNativeFleetPhysicalOwnerBeginShutdownV1(handle, *epoch); !retry {
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer shutdown publication failed")
		}
	}
	now, clockOK := coroNativeFleetPhysicalOwnerClockV1()
	if !clockOK {
		return coroNativeFleetPhysicalOwnerFailV1("native fleet peer monotonic clock failed")
	}
	result := coroNativeFleetRunOwnerEpochV1(handle, *epoch, now, coroNativeFleetRunBudgetV1)
	switch result.stop {
	case coroRunSliceBudgetV1, coroRunAgainV1:
		return true
	case coroRunDestroyCommitV1:
		completed, committed := coroNativeFleetCommitOwnerDestroyV1(
			handle,
			*epoch,
			result.g,
			result.action,
		)
		if !committed || completed.Kind == coro.ActionPanicComplete {
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer destroy commit failed")
		}
		return true
	case coroRunIdleV1:
		// Mailbox publication precedes the advisory executor request. The run
		// slice may acknowledge that request after its entry-time mailbox drain
		// and then report idle. Recheck the mailbox before ArmIdle so the
		// request-to-sleep window is closed by either this import, the driver's
		// final request scan, or a later IdleWake doorbell.
		moved, more, finalDrainStatus := coroNativeFleetTryDrainOwnerEpochV1(
			handle,
			*epoch,
			coro.RunnableTransferMailboxCapacity,
		)
		switch finalDrainStatus {
		case coro.RunnableTransferDrainContended:
			return true
		case coro.RunnableTransferDrainComplete:
		case coro.RunnableTransferDrainOwnerUnstable:
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer final mailbox owner unstable")
		case coro.RunnableTransferDrainCorrupt:
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer final mailbox corrupt")
		case coro.RunnableTransferDrainInvalid:
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer final mailbox drain failed")
		}
		if moved != 0 || more {
			return true
		}
		if coroNativeFleetPhysicalOwnerStoppingV1(handle) &&
			!coroNativeFleetPhysicalOwnerV1State.shutdown {
			// A previous budget boundary may have landed inside a source
			// transaction. Idle proves that transaction is now complete; retry
			// the one-time shutdown publication before arming another wait.
			return true
		}
		if coroNativeFleetPhysicalOwnerStoppingV1(handle) &&
			coroNativeFleetPhysicalOwnerShutdownReadyV1(handle) {
			if !coroNativeFleetFinishOwnerEpochV1(handle, *epoch) {
				return coroNativeFleetPhysicalOwnerFailV1("native fleet peer finish epoch failed")
			}
			*done = true
			return true
		}
		freshNow, freshClockOK := coroNativeFleetPhysicalOwnerClockV1()
		if !freshClockOK {
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer wait clock failed")
		}
		plan, prepared := coroNativeFleetPrepareOwnerWaitAtV1(
			handle,
			*epoch,
			now,
			freshNow,
		)
		if !prepared {
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer wait prepare failed")
		}
		if !plan.Armed {
			return true
		}
		wait, armed := coroNativeFleetArmOwnerWaitV1(handle, plan)
		if !armed {
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer reactor arm failed")
		}
		for {
			waitNow, waitClockOK := coroNativeFleetPhysicalOwnerClockV1()
			if !waitClockOK {
				return coroNativeFleetPhysicalOwnerFailV1("native fleet peer reactor clock failed")
			}
			switch coroNativeFleetWaitOwnerPassAtV1(wait, waitNow) {
			case coroNativeFleetWaitPassRetryV1:
				continue
			case coroNativeFleetWaitPassWakeV1:
				wakeNow, wakeClockOK := coroNativeFleetPhysicalOwnerClockV1()
				if !wakeClockOK {
					return coroNativeFleetPhysicalOwnerFailV1("native fleet peer wake clock failed")
				}
				next, wakeOK := coroNativeFleetWakeOwnerAtV1(handle, wakeNow)
				if !wakeOK {
					return coroNativeFleetPhysicalOwnerFailV1("native fleet peer wake transition failed")
				}
				*epoch = next
				return true
			default:
				return coroNativeFleetPhysicalOwnerFailV1("native fleet peer reactor wait failed")
			}
		}
	case coroRunPanicCompleteV1:
		return coroNativeFleetPhysicalOwnerFailV1("native fleet peer task panic")
	default:
		return coroNativeFleetPhysicalOwnerFailV1("native fleet peer run slice failed")
	}
}

// coroNativeFleetRunPhysicalOwnerV1 is the complete ordinary-domain M loop.
// It shares the common reducer and logical driver with every target profile;
// this layer owns only clock sampling, mailbox import, physical poll, and the
// explicit stop boundary. No recursion or dynamically selected callback is
// used, and every blocking wait is bounded so stop is observed promptly.
func coroNativeFleetRunPhysicalOwnerV1(handle coro.ExecutorFleetHandle) bool {
	if !handle.Valid() || handle.Route != coroNativeFleetPeerIndexV1+1 ||
		coroNativeFleetPhysicalOwnerV1State.handle != handle {
		return coroNativeFleetPhysicalOwnerFailV1("native fleet peer handle invalid")
	}
	epoch, ok := coroNativeFleetBeginOwnerEpochV1(handle)
	if !ok {
		return coroNativeFleetPhysicalOwnerFailV1("native fleet peer begin epoch failed")
	}
	done := false
	for {
		if !coroNativeFleetRunPhysicalOwnerPassV1(handle, &epoch, &done) {
			return false
		}
		if done {
			return true
		}
	}
}

// __llgo_coro_native_fleet_owner_v1 is called only by corofleet's fixed C
// pthread routine. The compiler retains this exact body and its static closure
// as a raw scheduler-stack island, so coroHandleResume/Destroy never acquire a
// managed coroutine twin. One means coordinated stop; every invariant failure
// is process-fatal because no managed G can safely receive a scheduler panic.
//
//export __llgo_coro_native_fleet_owner_v1
func __llgo_coro_native_fleet_owner_v1() uint32 {
	state := &coroNativeFleetPhysicalOwnerV1State
	// handle is published before pthread_create and remains immutable until
	// join. Do not gate entry on lifecycle: a program which returns immediately
	// may request Stopping before the new pthread reaches this fixed ABI.
	if !state.handle.Valid() || !coroNativeFleetRunPhysicalOwnerV1(state.handle) {
		coroRuntimeAbort("native coroutine fleet physical owner failed")
		return 0
	}
	return 1
}
