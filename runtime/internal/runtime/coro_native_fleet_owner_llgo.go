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

// coroNativeFleetPhysicalOwnerV1 is one process-lifetime peer M. Production
// starts the complete bounded topology before any managed resume, and each
// slot retains its exact route tombstone after join.
type coroNativeFleetPhysicalOwnerV1 struct {
	thread    pthread.Thread
	handle    coro.ExecutorFleetHandle
	shutdown  bool
	lifecycle coroNativeFleetPhysicalLifecycleV1
}

// coroNativeFleetPhysicalOwnersV1 owns the complete bounded peer set and one
// atomic broadcast stop word. TargetIngress has no callback entrants here:
// Seal is the release publication observed by every peer through Quiesced, and
// Retire leaves a permanent non-reusable process tombstone after all joins.
type coroNativeFleetPhysicalOwnersV1 struct {
	stop      coro.TargetIngress
	peers     [coroNativeFleetDomainCapacityV1 - 1]coroNativeFleetPhysicalOwnerV1
	count     uint32
	lifecycle coroNativeFleetPhysicalLifecycleV1
}

var coroNativeFleetPhysicalOwnerV1State coroNativeFleetPhysicalOwnersV1

func coroNativeFleetPhysicalOwnerForHandleV1(
	handle coro.ExecutorFleetHandle,
) (*coroNativeFleetPhysicalOwnerV1, bool) {
	state := &coroNativeFleetPhysicalOwnerV1State
	if !handle.Valid() || handle.Route < 2 || handle.Route > coroNativeFleetV1State.domainCount ||
		handle.Route-2 >= state.count {
		return nil, false
	}
	peer := &state.peers[handle.Route-2]
	return peer, peer.handle == handle
}

func coroNativeFleetPhysicalOwnersStartV1() bool {
	state := &coroNativeFleetPhysicalOwnerV1State
	count := coroNativeFleetV1State.domainCount
	if coroNativeFleetV1State.lifecycle != coroNativeFleetActiveV1 ||
		count == 0 || count > coroNativeFleetDomainCapacityV1 ||
		state.lifecycle != coroNativeFleetPhysicalUnusedV1 || state.count != 0 ||
		!state.stop.CanReleaseResources() || !state.stop.Start() {
		return false
	}
	state.count = count - 1
	state.lifecycle = coroNativeFleetPhysicalActiveV1
	created := uint32(0)
	for index := uint32(0); index < state.count; index++ {
		peer := &state.peers[index]
		handle, ok := coroNativeFleetHandleV1(index + 1)
		if !ok || peer.lifecycle != coroNativeFleetPhysicalUnusedV1 ||
			peer.thread != nil || peer.handle != (coro.ExecutorFleetHandle{}) || peer.shutdown {
			break
		}
		peer.handle = handle
		peer.lifecycle = coroNativeFleetPhysicalActiveV1
		if corofleet.CreatePeer(&peer.thread, handle.Route) != 0 || peer.thread == nil {
			// pthread_create leaves the result slot unspecified on failure.
			peer.thread = nil
			peer.lifecycle = coroNativeFleetPhysicalFailedV1
			break
		}
		created++
	}
	if created == state.count {
		return true
	}
	sealed := state.stop.Seal()
	rang := sealed
	for index := uint32(0); index < created; index++ {
		domain, ok := coroNativeFleetDomainForHandleV1(
			&coroNativeFleetV1State,
			state.peers[index].handle,
			coroNativeFleetDomainActiveV1,
		)
		ringOK := ok && domain.doorbell.Ring()
		rang = rang && ringOK
	}
	joined := rang
	for index := uint32(0); index < created; index++ {
		peer := &state.peers[index]
		var result c.Pointer
		ok := pthread.Join(peer.thread, &result) == 0 && result == nil
		peer.thread = nil
		peer.lifecycle = coroNativeFleetPhysicalFailedV1
		joined = joined && ok
	}
	retired := joined && state.stop.Quiesced() && state.stop.Retire()
	state.lifecycle = coroNativeFleetPhysicalFailedV1
	if !retired {
		coroRuntimeAbort("native coroutine fleet peer start rollback failed")
	}
	return false
}

func coroNativeFleetPhysicalOwnersStopV1() bool {
	state := &coroNativeFleetPhysicalOwnerV1State
	if state.lifecycle != coroNativeFleetPhysicalActiveV1 ||
		state.count+1 != coroNativeFleetV1State.domainCount || !state.stop.Seal() {
		return false
	}
	state.lifecycle = coroNativeFleetPhysicalStoppingV1
	for index := uint32(0); index < state.count; index++ {
		peer := &state.peers[index]
		domain, ok := coroNativeFleetDomainForHandleV1(
			&coroNativeFleetV1State,
			peer.handle,
			coroNativeFleetDomainActiveV1,
		)
		if !ok || peer.lifecycle != coroNativeFleetPhysicalActiveV1 ||
			peer.thread == nil || !domain.doorbell.Ring() {
			state.lifecycle = coroNativeFleetPhysicalFailedV1
			return false
		}
	}
	for index := uint32(0); index < state.count; index++ {
		peer := &state.peers[index]
		var result c.Pointer
		if pthread.Join(peer.thread, &result) != 0 || result != nil {
			state.lifecycle = coroNativeFleetPhysicalFailedV1
			return false
		}
		peer.thread = nil
		peer.lifecycle = coroNativeFleetPhysicalRetiredV1
	}
	if !state.stop.Quiesced() || !state.stop.Retire() {
		state.lifecycle = coroNativeFleetPhysicalFailedV1
		return false
	}
	state.lifecycle = coroNativeFleetPhysicalRetiredV1
	return true
}

func coroNativeFleetPhysicalOwnerStoppingV1(handle coro.ExecutorFleetHandle) bool {
	state := &coroNativeFleetPhysicalOwnerV1State
	peer, ok := coroNativeFleetPhysicalOwnerForHandleV1(handle)
	// The atomic stop word is the only coordinator-to-peer shutdown fact.
	// Aggregate lifecycle is coordinator-owned diagnostic state and must not
	// become a second, non-atomic publication protocol.
	return ok && peer.lifecycle == coroNativeFleetPhysicalActiveV1 && state.stop.Quiesced()
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
	peer, ownerOK := coroNativeFleetPhysicalOwnerForHandleV1(handle)
	if !ownerOK || !coroNativeFleetPhysicalOwnerStoppingV1(handle) {
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
	peer.shutdown = true
	return true, true
}

func coroNativeFleetPhysicalOwnerShutdownReadyV1(handle coro.ExecutorFleetHandle) bool {
	peer, ownerOK := coroNativeFleetPhysicalOwnerForHandleV1(handle)
	domain, ok := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		handle,
		coroNativeFleetDomainActiveV1,
	)
	if !ownerOK || !ok || !peer.shutdown || domain.driverOwnerV1() == nil ||
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
	if _, ok := coroNativeFleetCancelOwnerRunnableDemandV1(handle, *epoch); !ok {
		return coroNativeFleetPhysicalOwnerFailV1("native fleet peer demand cancel failed")
	}
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
	case coroRunExecutionWaitV1:
		domain, ok := coroNativeFleetDomainForHandleV1(
			&coroNativeFleetV1State,
			handle,
			coroNativeFleetDomainActiveV1,
		)
		if !ok || !coroTargetWaitManagedExecutionV1(domain.driverOwnerV1()) {
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer execution wait failed")
		}
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
		peer, ownerOK := coroNativeFleetPhysicalOwnerForHandleV1(handle)
		if !ownerOK {
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer owner identity lost")
		}
		if coroNativeFleetPhysicalOwnerStoppingV1(handle) && !peer.shutdown {
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
		if !coroNativeFleetRequestOwnerRunnableV1(handle, *epoch) {
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer demand publication failed")
		}
		moved, more, finalDrainStatus = coroNativeFleetTryDrainOwnerEpochV1(
			handle,
			*epoch,
			coro.RunnableTransferMailboxCapacity,
		)
		switch finalDrainStatus {
		case coro.RunnableTransferDrainContended:
			return true
		case coro.RunnableTransferDrainComplete:
		case coro.RunnableTransferDrainOwnerUnstable:
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer demand recheck owner unstable")
		case coro.RunnableTransferDrainCorrupt:
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer demand recheck corrupt")
		case coro.RunnableTransferDrainInvalid:
			return coroNativeFleetPhysicalOwnerFailV1("native fleet peer demand recheck failed")
		}
		if moved != 0 || more {
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
	peer, ok := coroNativeFleetPhysicalOwnerForHandleV1(handle)
	if !ok || peer.lifecycle != coroNativeFleetPhysicalActiveV1 {
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

// __llgo_coro_native_fleet_owner_v2 is called only by corofleet's fixed C
// pthread routine. route is the scalar fixed-topology route passed as the
// pthread start argument; it is never a Go pointer, G, P, function address, or
// coroutine handle. The compiler retains this exact body and its static
// closure as a raw scheduler-stack island, so coroHandleResume/Destroy never
// acquire a managed coroutine twin. One means coordinated stop; every
// invariant failure is process-fatal because no managed G can safely receive a
// scheduler panic.
//
//export __llgo_coro_native_fleet_owner_v2
func __llgo_coro_native_fleet_owner_v2(route uint32) uint32 {
	state := &coroNativeFleetPhysicalOwnerV1State
	if route < 2 || route > coroNativeFleetV1State.domainCount ||
		route-2 >= state.count {
		coroRuntimeAbort("native coroutine fleet physical owner route invalid")
		return 0
	}
	peer := &state.peers[route-2]
	// handle is published before pthread_create and remains immutable until
	// join. Do not gate entry on the aggregate lifecycle: a program which
	// returns immediately may request Stopping before the new pthread reaches
	// this fixed ABI.
	if peer.handle.Route != route || !peer.handle.Valid() ||
		!coroNativeFleetRunPhysicalOwnerV1(peer.handle) {
		coroRuntimeAbort("native coroutine fleet physical owner failed")
		return 0
	}
	return 1
}
