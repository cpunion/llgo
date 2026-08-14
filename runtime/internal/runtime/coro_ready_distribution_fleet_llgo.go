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
	domain, _, ok := coroNativeFleetExecutionDomainV1(driver)
	return domain, ok && domain.pOwnerV1() == p
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

// coroTargetBeginRunSliceV1 freezes policy which is safe to observe at a
// bounded scheduler-stack entry. GOMAXPROCS changes take effect on the next
// slice (at most 64 reductions for compatibility runners); the execution quota
// itself remains the exact concurrency gate immediately, so caching only the
// placement policy cannot exceed a shrunken limit. A negative LockOSThread
// handoff observation is exact until this slice itself creates a detached
// handoff, and that transition returns from the slice immediately.
func coroTargetBeginRunSliceV1(
	source *coro.P,
	driver *coro.ExecutorDriver,
) (coroRunTargetCapabilityV1, bool) {
	osThreadPossible, osThreadOK := coro.OSThreadSuspendHandoffPossible(driver)
	if source == nil || driver == nil || !osThreadOK {
		return coroRunTargetCapabilityV1{}, false
	}
	target := coroRunTargetCapabilityV1{physicalReturn: osThreadPossible}
	state := &coroNativeFleetV1State
	if coroNativeFleetPhysicalOwnerV1State.stop.Quiesced() {
		return target, true
	}
	if state.lifecycle != coroNativeFleetActiveV1 {
		return coroRunTargetCapabilityV1{}, false
	}
	if _, ok := coroTargetReadyDistributionDomainV1(source, driver); !ok {
		return coroRunTargetCapabilityV1{}, false
	}
	policy := &coroNativeFleetPhysicalOwnerV1State.policyEpoch
	for {
		epoch := coroNativeAtomicLoadV1(policy)
		distributionEnabled, distributionOK := coroTargetReadyDistributionEnabledV1(state)
		if !distributionOK || epoch == 0 {
			return coroRunTargetCapabilityV1{}, false
		}
		if coroNativeAtomicLoadV1(policy) != epoch {
			continue
		}
		target.readyDistribution = distributionEnabled
		target.policyEpoch = epoch
		break
	}
	replacementPossible, replacementOK := coroTargetReplacementReturnPossibleV1(driver)
	if !replacementOK {
		return coroRunTargetCapabilityV1{}, false
	}
	target.physicalReturn = target.physicalReturn || replacementPossible
	return target, true
}

// The stop word is live because program-main return may publish it while a
// distribution-capable peer is inside this very slice. A serial slice skips
// this post-action hook entirely: shutdown also publishes the executor request
// which makes its next bounded source reduction observe stop, while a physical
// return candidate has the separate exact hook. Ready placement is the bounded
// slice capability above; revalidating the complete quota lifetime and limit
// after every action adds no safety because TryAcquire remains authoritative.
func coroTargetReadyDistributionV1(target coroRunTargetCapabilityV1) (distribute, stop, ok bool) {
	stop = coroNativeFleetPhysicalOwnerV1State.stop.Quiesced()
	return target.readyDistribution && !stop, stop, true
}

// A successful quota resize executes an exact compiler yield before its Go
// caller can continue. That rare action is the synchronization point for this
// policy epoch; ordinary channel/park/complete actions do not read it. An
// unrelated explicit yield with an unchanged epoch keeps the current slice.
func coroTargetRefreshRunSliceV1(target coroRunTargetCapabilityV1) (distribute, restart, ok bool) {
	stop := coroNativeFleetPhysicalOwnerV1State.stop.Quiesced()
	epoch := coroKeyedAtomicLoadUint32(&coroNativeFleetPhysicalOwnerV1State.policyEpoch)
	enabled, valid := coroTargetReadyDistributionEnabledV1(&coroNativeFleetV1State)
	if !valid || target.policyEpoch == 0 || epoch == 0 {
		return false, false, false
	}
	return target.readyDistribution && enabled && !stop,
		stop || epoch != target.policyEpoch || enabled != target.readyDistribution,
		true
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
	if !coroNativeFleetFinishExecutorRequestV1(target, distribution.Request) {
		return coroTargetReadyDistributionFailV1("native ready distribution request tail failed")
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

func coroTargetReplacementReturnPossibleV1(
	driver *coro.ExecutorDriver,
) (bool, bool) {
	owner, _, _, _, ok := coroNativeMActiveOwnerV1(driver)
	if !ok {
		return false, false
	}
	if !owner.baton.Valid() {
		return false, true
	}
	parent, parentOK := coroNativeMOwnerForSlotV1(owner.parentSlot)
	return parentOK && parent.handle == owner.handle, parentOK
}

// coroTargetStopForPhysicalReturnV1 is the exact stable-reduction gate for a
// compensation M. An ordinary detached Yield/Park uses the P-local handoff;
// an active foreign-call replacement uses its parent M's atomic return baton.
// Both stop only after a complete reducer commit, so no physical action or
// source transaction is split. Ordinary owners skip this hook through the
// slice-entry negative capability above.
func coroTargetStopForPhysicalReturnV1(
	driver *coro.ExecutorDriver,
) (bool, bool) {
	possible, ok := coro.OSThreadSuspendHandoffPossible(driver)
	if !ok {
		return false, false
	}
	if possible {
		detached, returnable, statusOK := coro.OSThreadSuspendHandoffStatus(driver)
		return detached && returnable, statusOK
	}
	owner, _, _, _, ownerOK := coroNativeMCurrentOwnerV1(driver)
	if !ownerOK || !owner.baton.Valid() {
		return false, ownerOK
	}
	parent, parentOK := coroNativeMOwnerForSlotV1(owner.parentSlot)
	if !parentOK || parent.handle != owner.handle {
		return false, false
	}
	if !parent.handoff.ReturnRequested(owner.baton) {
		return false, true
	}
	return coro.ExecutorResumeHandoffReturnable(driver), true
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
