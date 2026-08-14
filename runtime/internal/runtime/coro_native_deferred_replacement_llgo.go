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

// coroNativeMActivateDeferredReplacementV1 is the request-side half of a
// demand-free native syscall handoff. It resolves only stable directory state;
// neither the caller's stack boundary nor an LLVM coroutine handle is
// published. The exact execution-domain generation remains authoritative in
// parent.handoff and replacement.baton.
func coroNativeMActivateDeferredReplacementV1(
	domain *coroNativeFleetDomainV1,
) bool {
	if domain == nil || domain.driverOwnerV1() == nil ||
		domain.handle.Route == 0 ||
		domain.handle.Route > coroNativeFleetDomainCapacityV1 {
		return false
	}
	parentSlot := coroNativeAtomicLoadV1(
		&coroNativeMDirectoryV1State.active[domain.handle.Route-1],
	)
	parent, ownerOK := coroNativeMOwnerForSlotV1(parentSlot)
	if !ownerOK || parent == nil || parentSlot == 0 ||
		parent.handle != domain.handle {
		return false
	}
	for {
		slot, phase, valid := parent.deferred.Observe()
		if !valid {
			return false
		}
		switch phase {
		case coro.DeferredExecutorHandoffIdle,
			coro.DeferredExecutorHandoffStarting,
			coro.DeferredExecutorHandoffQueued,
			coro.DeferredExecutorHandoffStarted:
			return true
		case coro.DeferredExecutorHandoffArmed:
			active, resolved, activeSlot, _, activeOK :=
				coroNativeMActiveOwnerV1(domain.driverOwnerV1())
			if !activeOK || active != parent || resolved != domain ||
				activeSlot != parentSlot {
				return false
			}
			startedSlot, won := parent.deferred.BeginStart()
			if !won {
				continue
			}
			// BeginStart reloads the one-word gate and is authoritative. A delayed
			// accepted request may safely coalesce into a later armed call on the
			// same stable parent even when that call obtained a different slot.
			slot = startedSlot
			if slot > coroNativeMDirectoryCapacityV1 {
				_ = parent.deferred.RetryStart(startedSlot)
				return false
			}
			replacement, replacementOK := coroNativeMOwnerForSlotV1(slot)
			released, releasedOK := parent.handoff.Released()
			if !replacementOK || replacement == nil ||
				coroNativeMOwnerLifecycleLoadV1(replacement) !=
					coroNativeMOwnerReplacementPublishedV1 ||
				replacement.parentSlot != parentSlot ||
				replacement.predecessorSlot != 0 ||
				replacement.lineageRootSlot != slot ||
				coroNativeAtomicLoadV1(&replacement.lineageSlot) != slot ||
				replacement.handle != domain.handle ||
				replacement.thread != nil || replacement.self != nil ||
				replacement.token != 0 || replacement.resume.Detached() ||
				!replacement.handoff.Idle() || !replacement.deferred.Idle() ||
				!releasedOK || released != replacement.baton ||
				replacement.ownerEpoch != released.OwnerEpoch {
				_ = parent.deferred.RetryStart(slot)
				return false
			}
			queued, started := coroNativeMRequestPhysicalOwnerV1(replacement, slot)
			if !started {
				if !parent.deferred.RetryStart(slot) {
					coroRuntimeAbort("native deferred replacement retry publication failed")
				}
				return false
			}
			if !parent.deferred.PublishStart(slot, queued) {
				// A physical owner now owns the exact thread/token/slot record. No
				// rollback is safe after this point; fail closed instead of losing
				// the strong-return obligation.
				coroRuntimeAbort("native deferred replacement start publication failed")
				return false
			}
			return true
		default:
			return false
		}
	}
}
