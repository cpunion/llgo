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
	"github.com/goplus/llgo/runtime/internal/coro"
	"github.com/goplus/llgo/runtime/internal/corofleet"
)

func coroNativeReplacementDrainRouteV1(
	owner *coroNativeMOwnerV1,
	domain *coroNativeFleetDomainV1,
) (more, ok bool) {
	if owner == nil || domain == nil || owner.handle != domain.handle {
		return false, false
	}
	p := domain.pOwnerV1()
	if p == nil {
		return false, false
	}
	if domain.adopted {
		if _, canceled := coroNativeFleetV1State.fleet.CancelPNeutralRunnableRequest(
			owner.handle,
			p,
		); !canceled {
			return false, false
		}
		moved, pending, status := coroNativeFleetV1State.fleet.TryDrainPNeutralRunnables(
			owner.handle,
			p,
			coro.RunnableTransferMailboxCapacity,
		)
		switch status {
		case coro.RunnableTransferDrainComplete:
			return moved != 0 || pending, true
		case coro.RunnableTransferDrainContended,
			coro.RunnableTransferDrainOwnerUnstable:
			return true, true
		default:
			return false, false
		}
	}
	if domain.ownerEpoch != owner.ownerEpoch {
		return false, false
	}
	if _, canceled := coroNativeFleetCancelOwnerRunnableDemandV1(
		owner.handle,
		owner.ownerEpoch,
	); !canceled {
		return false, false
	}
	moved, pending, status := coroNativeFleetTryDrainOwnerEpochV1(
		owner.handle,
		owner.ownerEpoch,
		coro.RunnableTransferMailboxCapacity,
	)
	switch status {
	case coro.RunnableTransferDrainComplete:
		return moved != 0 || pending, true
	case coro.RunnableTransferDrainContended,
		coro.RunnableTransferDrainOwnerUnstable:
		return true, true
	default:
		return false, false
	}
}

func coroNativeReplacementRequestRunnableV1(
	owner *coroNativeMOwnerV1,
	domain *coroNativeFleetDomainV1,
) bool {
	if owner == nil || domain == nil || owner.handle != domain.handle {
		return false
	}
	p := domain.pOwnerV1()
	if p == nil {
		return false
	}
	if domain.adopted {
		return coroNativeFleetV1State.fleet.RequestPNeutralRunnable(owner.handle, p)
	}
	return domain.ownerEpoch == owner.ownerEpoch &&
		coroNativeFleetRequestOwnerRunnableV1(owner.handle, owner.ownerEpoch)
}

func coroNativeReplacementRunSliceV1(
	owner *coroNativeMOwnerV1,
	domain *coroNativeFleetDomainV1,
	now int64,
) coroRunResultV1 {
	if owner == nil || domain == nil || owner.handle != domain.handle || now < 0 {
		return coroRunResultV1{}
	}
	driver := domain.driverOwnerV1()
	_, _, statusOK := coro.OSThreadSuspendHandoffStatus(driver)
	if !statusOK {
		return coroRunResultV1{}
	}
	budget := coroNativeFleetRunBudgetV1
	if domain.adopted {
		return coroRunSlice(
			domain.pOwnerV1(),
			&coroProgramGV1State,
			driver,
			budget,
		)
	}
	return coroNativeFleetRunOwnerEpochV1(
		owner.handle,
		owner.ownerEpoch,
		now,
		budget,
	)
}

func coroNativeReplacementCommitDestroyV1(
	owner *coroNativeMOwnerV1,
	domain *coroNativeFleetDomainV1,
	result coroRunResultV1,
) bool {
	if owner == nil || domain == nil || result.stop != coroRunDestroyCommitV1 ||
		result.g == nil {
		return coroNativeFleetPhysicalOwnerFailV1(
			"native replacement destroy receipt invalid",
		)
	}
	if !domain.adopted {
		completed, committed := coroNativeFleetCommitOwnerDestroyV1(
			owner.handle,
			owner.ownerEpoch,
			result.g,
			result.action,
		)
		if !committed {
			return coroNativeFleetPhysicalOwnerFailV1(
				"native replacement peer destroy receipt commit failed",
			)
		}
		if completed.Kind == coro.ActionPanicComplete {
			return coroNativeFleetPhysicalOwnerFailV1(
				"native replacement peer task panic",
			)
		}
		return true
	}
	if result.g == &coroProgramGV1State {
		return coroNativeFleetPhysicalOwnerFailV1(
			"native replacement attempted to destroy detached program task",
		)
	}
	// The detached locked G is rooted privately by parent.resume and therefore
	// is intentionally absent from P's ready/wait headers. A replacement child
	// can consequently be the last scheduler-visible G without being the last
	// task in the command executor. Keep the long-lived program domain bound;
	// terminal close belongs only to the restored program owner.
	next, committed := coro.CommitExecutorRunDomainDestroy(
		domain.driverOwnerV1(),
		result.g,
		result.action,
	)
	if !committed {
		return coroNativeFleetPhysicalOwnerFailV1(
			"native replacement program-domain destroy receipt commit failed",
		)
	}
	switch next.Kind {
	case coro.ActionComplete:
		retireOwner := coro.ActionRetiresPhysicalOwner(next)
		if !coroReleaseCompletedTask(result.g) ||
			retireOwner && !coroTargetRetirePhysicalOwnerV1(
				domain.pOwnerV1(),
				domain.driverOwnerV1(),
			) {
			return coroNativeFleetPhysicalOwnerFailV1(
				"native replacement completed task release failed",
			)
		}
		return true
	case coro.ActionPanicComplete:
		return coroNativeFleetPhysicalOwnerFailV1(
			"native replacement program task panic",
		)
	default:
		return coroNativeFleetPhysicalOwnerFailV1(
			"native replacement destroy receipt result invalid",
		)
	}
}

func coroNativeReplacementTryReturnV1(
	slot uint32,
	owner, parent *coroNativeMOwnerV1,
	domain *coroNativeFleetDomainV1,
) (returned, ok bool) {
	driver := domain.driverOwnerV1()
	detached, returnable, statusOK := coro.OSThreadSuspendHandoffStatus(driver)
	if !statusOK {
		return false, false
	}
	if detached && returnable &&
		!parent.handoff.ReturnRequested(owner.baton) &&
		!parent.handoff.RequestClaimedReturn(owner.baton) {
		return false, false
	}
	if !coroNativeMReplacementReturnRequestedV1(owner, parent) {
		return false, true
	}
	if coroNativeAtomicLoadV1(&domain.borrowedWait) != 0 {
		return false, false
	}
	more, drained := coroNativeReplacementDrainRouteV1(owner, domain)
	if !drained {
		return false, false
	}
	if more {
		return false, true
	}
	if detached {
		_, returnable, statusOK = coro.OSThreadSuspendHandoffStatus(driver)
		if !statusOK {
			return false, false
		}
	} else {
		returnable = coro.ExecutorResumeHandoffReturnable(driver)
	}
	if !returnable {
		return false, true
	}
	if !coroNativeMFinishReplacementReturnV1(slot, owner, parent) {
		return false, false
	}
	return true, true
}

func coroNativeReplacementWaitV1(
	slot uint32,
	owner, parent *coroNativeMOwnerV1,
	domain *coroNativeFleetDomainV1,
) bool {
	wait, shouldWait, prepared := coroNativeReplacementPrepareWaitV1(owner.handle, slot)
	if !prepared {
		return false
	}
	if !shouldWait {
		return true
	}
	for {
		if coroNativeMReplacementReturnRequestedV1(owner, parent) {
			return coroNativeReplacementDisarmWaitV1(domain, slot)
		}
		now, clockOK := coroNativeFleetPhysicalOwnerClockV1()
		if !clockOK {
			_ = coroNativeReplacementDisarmWaitV1(domain, slot)
			return false
		}
		switch coroNativeReplacementWaitPassAtV1(wait, now) {
		case coroNativeReplacementWaitRetryV1:
			continue
		case coroNativeReplacementWaitWakeV1:
			return coroNativeReplacementDisarmWaitV1(domain, slot)
		case coroNativeReplacementWaitDeadlineV1:
			if !coroNativeReplacementDisarmWaitV1(domain, slot) ||
				!coro.RequestExecutorSourceService(domain.driverOwnerV1()) {
				return false
			}
			return true
		default:
			_ = coroNativeReplacementDisarmWaitV1(domain, slot)
			return false
		}
	}
}

func coroNativeMRunClaimedReplacementOwnerV1(
	slot uint32,
	owner, parent *coroNativeMOwnerV1,
	domain *coroNativeFleetDomainV1,
) bool {
	if slot == 0 || owner == nil || parent == nil || domain == nil ||
		owner.parentSlot == 0 || owner.handle != domain.handle ||
		!owner.baton.Valid() {
		return false
	}
	for {
		if returned, returnOK := coroNativeReplacementTryReturnV1(
			slot,
			owner,
			parent,
			domain,
		); !returnOK {
			return coroNativeFleetPhysicalOwnerFailV1(
				"native replacement return transition failed",
			)
		} else if returned {
			return true
		}
		if more, drainOK := coroNativeReplacementDrainRouteV1(owner, domain); !drainOK {
			return coroNativeFleetPhysicalOwnerFailV1(
				"native replacement route drain failed",
			)
		} else if more {
			continue
		}
		if !domain.adopted && coroNativeFleetPhysicalOwnerStoppingV1(owner.handle) {
			if _, retry := coroNativeFleetPhysicalOwnerBeginShutdownV1(
				owner.handle,
				owner.ownerEpoch,
			); !retry {
				return coroNativeFleetPhysicalOwnerFailV1(
					"native replacement shutdown publication failed",
				)
			}
		}
		now, clockOK := coroNativeFleetPhysicalOwnerClockV1()
		if !clockOK {
			return coroNativeFleetPhysicalOwnerFailV1(
				"native replacement monotonic clock failed",
			)
		}
		result := coroNativeReplacementRunSliceV1(owner, domain, now)
		switch result.stop {
		case coroRunSliceBudgetV1, coroRunAgainV1:
			continue
		case coroRunExecutionWaitV1:
			if !coroTargetWaitManagedExecutionV1(domain.driverOwnerV1()) {
				return coroNativeFleetPhysicalOwnerFailV1(
					"native replacement execution wait failed",
				)
			}
		case coroRunOSThreadSuspendV1:
			if !coroTargetHandleOSThreadSuspendV1(
				domain.pOwnerV1(),
				domain.driverOwnerV1(),
				result.g,
				result.action,
			) {
				return coroNativeFleetPhysicalOwnerFailV1(
					"native replacement locked suspension handoff failed",
				)
			}
		case coroRunDestroyCommitV1:
			if !coroNativeReplacementCommitDestroyV1(owner, domain, result) {
				return coroNativeFleetPhysicalOwnerFailV1(
					"native replacement destroy commit failed",
				)
			}
		case coroRunIdleV1:
			if returned, returnOK := coroNativeReplacementTryReturnV1(
				slot,
				owner,
				parent,
				domain,
			); !returnOK {
				return coroNativeFleetPhysicalOwnerFailV1(
					"native replacement idle return transition failed",
				)
			} else if returned {
				return true
			}
			if !coroNativeReplacementRequestRunnableV1(owner, domain) {
				return coroNativeFleetPhysicalOwnerFailV1(
					"native replacement runnable demand failed",
				)
			}
			if more, drainOK := coroNativeReplacementDrainRouteV1(owner, domain); !drainOK {
				return coroNativeFleetPhysicalOwnerFailV1(
					"native replacement demand recheck failed",
				)
			} else if more {
				continue
			}
			if !coroNativeReplacementWaitV1(slot, owner, parent, domain) {
				return coroNativeFleetPhysicalOwnerFailV1(
					"native replacement reactor wait failed",
				)
			}
		case coroRunMainDoneV1:
			if !domain.adopted || result.g != &coroProgramGV1State {
				return coroNativeFleetPhysicalOwnerFailV1(
					"native replacement observed invalid main completion",
				)
			}
			c.Exit(0)
			for {
			}
		case coroRunPanicCompleteV1:
			return coroNativeFleetPhysicalOwnerFailV1(
				"native replacement task panic",
			)
		default:
			return coroNativeFleetPhysicalOwnerFailV1(
				"native replacement run slice failed",
			)
		}
	}
}

// coroNativeMRunReplacementOwnerV1 borrows one existing route without
// creating a second scheduler or releasing the peer's logical owner epoch.
// Every pass uses the common reducer; this loop supplies only the physical
// monotonic clock, exact mailbox transport, quota wait, and raw poll wait.
func coroNativeMRunReplacementOwnerV1(slot uint32) bool {
	owner, parent, domain, claimed, ok := coroNativeMClaimReplacementV1(slot)
	if !ok {
		return coroNativeFleetPhysicalOwnerFailV1(
			"native replacement M claim failed",
		)
	}
	if !claimed {
		return coroNativeFleetPhysicalOwnerFailV1(
			"native replacement M claim was revoked before startup",
		)
	}
	if corofleet.OwnerReady(slot) != 0 {
		return coroNativeFleetPhysicalOwnerFailV1(
			"native replacement M startup acknowledgement failed",
		)
	}
	return coroNativeMRunClaimedReplacementOwnerV1(
		slot,
		owner,
		parent,
		domain,
	)
}
