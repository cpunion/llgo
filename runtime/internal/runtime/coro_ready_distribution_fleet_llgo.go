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

import "github.com/goplus/llgo/runtime/internal/coro"

func coroTargetReadyDistributionFailV1(message string) bool {
	coroRuntimeAbort(message)
	return false
}

func coroTargetReadyDistributionDomainV1(
	p *coro.P,
	driver *coro.ExecutorDriver,
) (*coroNativeFleetDomainV1, bool) {
	if p == nil || driver == nil {
		return nil, false
	}
	state := &coroNativeFleetV1State
	for index := uint32(0); index < state.domainCount; index++ {
		domain := &state.domains[index]
		if domain.lifecycle == coroNativeFleetDomainActiveV1 &&
			domain.pOwnerV1() == p && domain.driverOwnerV1() == driver {
			return domain, true
		}
	}
	return nil, false
}

// coroTargetReadyDistributionEnabledV1 keeps runnable ownership local when
// managed Go execution is serial. The native fleet intentionally keeps every
// physical route alive so it can continue to service timer, poll, channel and
// cancellation sources, but moving a runnable between those routes cannot add
// execution capacity while GOMAXPROCS is one. Apart from the mailbox and
// doorbell traffic, such a move also loses the warm owner-local scheduler
// state and makes the next route contend for the sole execution lease.
//
// Limit is a live observation rather than startup policy: increasing
// GOMAXPROCS immediately re-enables surplus sharing, while shrinking it stops
// new transfers without disturbing work that was already published.
func coroTargetReadyDistributionEnabledV1(state *coroNativeFleetStateV1) (bool, bool) {
	if state == nil {
		return false, false
	}
	limit, ok := state.execution.Limit()
	return limit > 1, ok
}

// coroTargetAfterStableRunActionV1 is owner-to-owner work distribution, not a
// producer callback. The active domain prefix and every P/driver identity are
// frozen before any peer M starts, and the program coordinator joins all peer
// Ms before route close; the fleet's route producer lease therefore needs no
// additional target-ingress lease around this short publish/request/ring tail.
func coroTargetAfterStableRunActionV1(source *coro.P, driver *coro.ExecutorDriver) bool {
	state := &coroNativeFleetV1State
	if source == nil || driver == nil {
		return coroTargetReadyDistributionFailV1("native ready distribution lacks source owner")
	}
	if coroNativeFleetPhysicalOwnerV1State.stop.Quiesced() {
		// Fleet shutdown is a one-way ownership barrier. A peer action which
		// committed concurrently with program-main return is still valid, but
		// it must not publish another transfer after the stop boundary.
		return true
	}
	if state.lifecycle != coroNativeFleetActiveV1 {
		return coroTargetReadyDistributionFailV1("native ready distribution fleet is not active")
	}
	distributionEnabled, limitOK := coroTargetReadyDistributionEnabledV1(state)
	if !limitOK {
		return coroTargetReadyDistributionFailV1("native ready distribution execution quota is not active")
	}
	if !distributionEnabled {
		return true
	}
	sourceDomain, ok := coroTargetReadyDistributionDomainV1(source, driver)
	if !ok {
		return coroTargetReadyDistributionFailV1("native ready distribution source route mismatch")
	}
	distribution, distributed := state.fleet.DistributePNeutralRunnable(
		sourceDomain.handle,
		source,
	)
	if !distributed {
		return coroTargetReadyDistributionFailV1("native ready distribution core rejected owner")
	}
	if !distribution.Valid() {
		return true
	}
	return coroTargetPublishReadyDistributionV1(state, distribution)
}

func coroTargetPublishReadyDistributionV1(
	state *coroNativeFleetStateV1,
	distribution coro.RunnableDistribution,
) bool {
	if state == nil || !distribution.Valid() {
		return false
	}
	target, valid := coroNativeFleetDomainForHandleV1(
		state,
		distribution.Target,
		coroNativeFleetDomainActiveV1,
	)
	if !valid {
		return coroTargetReadyDistributionFailV1("native ready distribution target route mismatch")
	}
	if coroNativeFleetRequestNeedsRingV1(target, distribution.Request) && !target.doorbell.Ring() {
		return coroTargetReadyDistributionFailV1("native ready distribution doorbell failed")
	}
	return true
}

// coroTargetAfterSourceReductionV1 consumes the optional producer-locality
// hint only after epoch-B cleanup has promoted a source-neutral park. A target
// must have published exact runnable demand, so a producer which wakes a peer
// and then keeps computing never captures that peer behind itself.
func coroTargetAfterSourceReductionV1(
	source *coro.P,
	driver *coro.ExecutorDriver,
	progress coro.ExecutorPollProgress,
) (distributed, ok bool) {
	if !progress.Complete || progress.Promoted == 0 {
		return false, true
	}
	state := &coroNativeFleetV1State
	if source == nil || driver == nil {
		return false, coroTargetReadyDistributionFailV1("native source distribution lacks source owner")
	}
	if coroNativeFleetPhysicalOwnerV1State.stop.Quiesced() {
		return false, true
	}
	if state.lifecycle != coroNativeFleetActiveV1 {
		return false, coroTargetReadyDistributionFailV1("native source distribution fleet is not active")
	}
	distributionEnabled, limitOK := coroTargetReadyDistributionEnabledV1(state)
	if !limitOK {
		return false, coroTargetReadyDistributionFailV1("native source distribution execution quota is not active")
	}
	if !distributionEnabled {
		return false, true
	}
	sourceDomain, ok := coroTargetReadyDistributionDomainV1(source, driver)
	if !ok {
		return false, coroTargetReadyDistributionFailV1("native source distribution route mismatch")
	}
	distribution, accepted := state.fleet.DistributeMaterializedRunnableToPreferredRoute(
		sourceDomain.handle,
		source,
	)
	if !accepted {
		return false, coroTargetReadyDistributionFailV1("native source distribution core rejected owner")
	}
	if !distribution.Valid() {
		return false, true
	}
	if !coroTargetPublishReadyDistributionV1(state, distribution) {
		return false, false
	}
	return true, true
}

// coroTargetDrainProgramTransfersV1 imports the adopted route-1 mailbox while
// the program's existing DriveAdmission owns its P. A separate fleet owner
// epoch would duplicate that serialization and conflict with the host ABI.
// Contention or a bounded reducer's pending physical action is ordinary
// retryable work; corrupt identity is the only failure.
func coroTargetDrainProgramTransfersV1(p *coro.P, driver *coro.ExecutorDriver) (more, ok bool) {
	state := &coroNativeFleetV1State
	target := &coroNativeFleetTargetV1State
	if state.lifecycle != coroNativeFleetActiveV1 ||
		target.lifecycle != coroNativeFleetTargetActiveV1 || !target.program.Valid() {
		return false, false
	}
	domain, valid := coroNativeFleetDomainForHandleV1(
		state,
		target.program,
		coroNativeFleetDomainActiveV1,
	)
	if !valid || domain.pOwnerV1() != p || domain.driverOwnerV1() != driver {
		return false, false
	}
	moved, pending, status := state.fleet.TryDrainPNeutralRunnables(
		target.program,
		p,
		coro.RunnableTransferMailboxCapacity,
	)
	switch status {
	case coro.RunnableTransferDrainComplete:
		return moved != 0 || pending, true
	case coro.RunnableTransferDrainOwnerUnstable, coro.RunnableTransferDrainContended:
		return true, true
	default:
		return false, false
	}
}

func coroTargetRequestProgramRunnableV1(p *coro.P, driver *coro.ExecutorDriver) bool {
	domain, ok := coroTargetReadyDistributionDomainV1(p, driver)
	return ok && domain.adopted &&
		coroNativeFleetV1State.fleet.RequestPNeutralRunnable(domain.handle, p)
}

func coroTargetBeforeProgramRunSliceV1(p *coro.P, driver *coro.ExecutorDriver) bool {
	domain, valid := coroTargetReadyDistributionDomainV1(p, driver)
	if !valid || !domain.adopted {
		return false
	}
	if _, ok := coroNativeFleetV1State.fleet.CancelPNeutralRunnableRequest(
		domain.handle,
		p,
	); !ok {
		return false
	}
	_, ok := coroTargetDrainProgramTransfersV1(p, driver)
	return ok
}

// coroTargetPrepareOSThreadSuspendV1 is called after one complete physical
// Action has committed but before P-neutral ready distribution. Keeping the
// peer local closes the exact Yield service obligation before any target state
// is published.
func coroTargetPrepareOSThreadSuspendV1(
	p *coro.P,
	driver *coro.ExecutorDriver,
	task *coro.G,
	action coro.Action,
) (bool, bool) {
	switch action.Kind {
	case coro.ActionYield, coro.ActionPark:
		if p == nil || driver == nil || task == nil ||
			action.Handle != nil || action.Flags != 0 {
			return false, false
		}
		if !coro.OSThreadSuspendHandoffCandidate(task) {
			return false, true
		}
		return coro.PrepareOSThreadSuspendHandoff(
			driver, task, action.Kind,
		)
	default:
		return false, true
	}
}

func coroNativeAbortOSThreadSuspendV1(
	parent *coroNativeMOwnerV1,
	baton coro.ExecutionDomainHandoffHandle,
	driver *coro.ExecutorDriver,
	task *coro.G,
) bool {
	return parent != nil &&
		parent.handoff.RequestReturn(baton) ==
			coro.ExecutionDomainHandoffReturnUnclaimed &&
		parent.handoff.Complete(baton) &&
		coro.AbortOSThreadSuspendHandoff(driver, task)
}

// coroTargetStopForOSThreadReturnV1 is the exact stable-reduction gate for a
// compensation M. A detached Yield stops after its first complete peer Action;
// a detached Park stops as soon as source service promotes the locked owner.
// Keeping this observation inside the common runner lets source transactions
// retain their normal batch budget without crossing the return boundary.
func coroTargetStopForOSThreadReturnV1(
	driver *coro.ExecutorDriver,
) (bool, bool) {
	possible, ok := coro.OSThreadSuspendHandoffPossible(driver)
	if !ok || !possible {
		return false, ok
	}
	detached, returnable, ok := coro.OSThreadSuspendHandoffStatus(driver)
	return detached && returnable, ok
}

// coroTargetHandleOSThreadSuspendV1 temporarily blocks the original M on
// corofleet's existing condition variable while one clean replacement owns
// the same P/driver/source island. The ordinary suspended LLVM frame is already
// rooted by P's ready/wait queues, so this path neither copies an active resume
// nor acquires another managed-execution P lease.
func coroTargetHandleOSThreadSuspendV1(
	p *coro.P,
	driver *coro.ExecutorDriver,
	task *coro.G,
	action coro.Action,
) bool {
	if p == nil || driver == nil || task == nil ||
		action.Handle != nil || action.Flags != 0 ||
		(action.Kind != coro.ActionYield &&
			action.Kind != coro.ActionPark) {
		return false
	}
	detached, _, statusOK := coro.OSThreadSuspendHandoffStatus(driver)
	parent, domain, parentSlot, ownerEpoch, physicalOK :=
		coroNativeMCurrentOwnerV1(driver)
	if !statusOK || !detached || !physicalOK ||
		domain == nil || domain.pOwnerV1() != p ||
		domain.driverOwnerV1() != driver {
		return false
	}
	baton, begun := parent.handoff.Begin(ownerEpoch)
	if !begun {
		return false
	}
	childSlot, child, allocated := coroNativeMAllocateReplacementV1(
		parentSlot,
		domain.handle,
		baton,
	)
	if !allocated {
		_ = coroNativeAbortOSThreadSuspendV1(
			parent, baton, driver, task,
		)
		return false
	}
	if !coroNativeMStartPhysicalOwnerV1(child, childSlot) {
		child.thread = nil
		child.token = 0
		aborted := coroNativeAbortOSThreadSuspendV1(
			parent, baton, driver, task,
		)
		released := coroNativeMReleaseUnstartedReplacementV1(childSlot)
		_ = aborted && released
		return false
	}
	if !coroNativeMWaitAndRecycleOSThreadSuspendV1(
		childSlot, child, parent,
	) ||
		!parent.handoff.Complete(baton) ||
		!coro.RestoreOSThreadSuspendHandoff(driver, task) {
		return false
	}
	return true
}
