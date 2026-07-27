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
	detached, returnable, ok := coro.OSThreadSuspendHandoffStatus(driver)
	return detached && returnable, ok
}

// coroTargetHandleOSThreadSuspendV1 temporarily blocks the original M on
// corofleet's existing condition variable while one clean replacement owns
// the same P/driver/source island. The ordinary suspended LLVM frame is already
// rooted by P's ready/wait queues, so this path neither copies an active resume
// nor acquires another managed-execution permit.
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
