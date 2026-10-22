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
	"github.com/xgo-dev/llgo/runtime/internal/coro"
	"github.com/xgo-dev/llgo/runtime/internal/corofleet"
)

// An ordinary locked suspension has no native activation to retain: the LLVM
// continuation is already on its original P. One raw coordinator retains the
// exact returned pthreads which own such continuations and dispatches them in
// core ready order. Only clean compensation Ms enter the shared standby pool.
// This object is cold storage allocated once on the first locked suspension.
type coroNativeOSThreadRotationV1 struct {
	rootSlot uint32
	retained uint32
	active   bool
}

// A foreign caller can request its P while an ordinary locked suspension has
// introduced another physical handoff below its replacement. Observe the
// original generation-bound request; a doorbell alone is not return authority.
func coroNativeRotationOuterReturnRequestedV1(owner *coroNativeMOwnerV1) (bool, bool) {
	if owner == nil {
		return false, false
	}
	rotation := owner.rotation
	if rotation == nil || !rotation.active {
		return false, true
	}
	root, ok := coroNativeMOwnerForSlotV1(rotation.rootSlot)
	if !ok || root.rotation != rotation || root.handle != owner.handle {
		return false, false
	}
	if !root.baton.Valid() {
		return false, root.parentSlot == 0
	}
	parent, ok := coroNativeMOwnerForSlotV1(root.parentSlot)
	if !ok || parent.handle != root.handle {
		return false, false
	}
	return parent.handoff.ReturnRequested(root.baton), true
}

// Returning a foreign replacement with cold affinity owners transfers those
// roots to the exact returning caller. It does not release a pinned pthread to
// the clean cache. The C receipt is acquired before modifying returned Go
// storage, and every later redispatch consumes that exact token/generation.
func coroNativeRotationAdoptReturnedV1(slot uint32, owner *coroNativeMOwnerV1, parentSlot uint32, parent *coroNativeMOwnerV1) bool {
	if owner == nil || parent == nil || slot == 0 || parentSlot == 0 || owner.parentSlot != parentSlot ||
		owner.handle != parent.handle || !parent.handoff.Returned(owner.baton) {
		return false
	}
	rotation := owner.rotation
	if rotation == nil {
		return coroNativeMRecycleReplacementV1(slot)
	}
	if rotation.active {
		return false
	}
	if rotation.retained == 0 && owner.suspension == (coro.OSThreadSuspension{}) {
		return coroNativeMRecycleReplacementV1(slot)
	}
	if (rotation.rootSlot != slot && rotation.rootSlot != owner.lineageRootSlot) ||
		owner.rotationNext != 0 || owner.rotationDispatch != 0 || parent.rotation == rotation {
		return false
	}
	var dispatch uint32
	if corofleet.RetainReturnedOwner(owner.thread, owner.token, slot, &dispatch) != 0 || dispatch == 0 ||
		coroNativeMOwnerLifecycleLoadV1(owner) != coroNativeMOwnerReturnedV1 {
		return false
	}
	// Validate the complete cold list before transferring any link. FIFO
	// selection remains in core's ready queue; this list owns only native roots.
	current := rotation.retained
	for depth := uint32(0); current != 0 && depth < coroNativeMDirectoryCapacityV1; depth++ {
		child, ok := coroNativeMOwnerForSlotV1(current)
		if !ok || current == slot || current == parentSlot || child.rotation != rotation ||
			child.handle != owner.handle || child.rotationDispatch == 0 ||
			child.suspension == (coro.OSThreadSuspension{}) ||
			coroNativeMOwnerLifecycleLoadV1(child) != coroNativeMOwnerReturnedV1 {
			return false
		}
		current = child.rotationNext
	}
	if current != 0 {
		return false
	}
	destination := parent.rotation
	if destination == nil {
		destination = rotation
		destination.rootSlot = parentSlot
		parent.rotation = destination
	} else {
		root, ok := coroNativeMOwnerForSlotV1(destination.rootSlot)
		if !ok || root.rotation != destination || root.handle != parent.handle {
			return false
		}
		for current = rotation.retained; current != 0; {
			child, _ := coroNativeMOwnerForSlotV1(current)
			next := child.rotationNext
			child.rotation = destination
			child.rotationNext = destination.retained
			destination.retained = current
			current = next
		}
		rotation.retained = 0
		rotation.rootSlot = 0
	}
	if owner.suspension == (coro.OSThreadSuspension{}) {
		return coroNativeMRecycleReplacementV1(slot)
	}
	owner.rotation = destination
	owner.rotationDispatch = dispatch
	owner.rotationNext = destination.retained
	destination.retained = slot
	return coroNativeMDetachReturnedLineageV1(slot, owner)
}

func coroNativeRotationRequestReturnV1(owner *coroNativeMOwnerV1) bool {
	if owner == nil || !owner.baton.Valid() || owner.parentSlot == 0 {
		return false
	}
	parent, ok := coroNativeMOwnerForSlotV1(owner.parentSlot)
	return ok && parent.handle == owner.handle &&
		(parent.handoff.ReturnRequested(owner.baton) || parent.handoff.RequestClaimedReturn(owner.baton))
}

func coroNativeRotationRemoveRetainedV1(rotation *coroNativeOSThreadRotationV1, slot uint32) bool {
	if rotation == nil || slot == 0 {
		return false
	}
	var previous *coroNativeMOwnerV1
	current := rotation.retained
	for depth := uint32(0); current != 0 && depth < coroNativeMDirectoryCapacityV1; depth++ {
		owner, ok := coroNativeMOwnerForSlotV1(current)
		if !ok || owner.rotation != rotation {
			return false
		}
		if current == slot {
			if previous == nil {
				rotation.retained = owner.rotationNext
			} else {
				previous.rotationNext = owner.rotationNext
			}
			owner.rotationNext = 0
			return true
		}
		previous, current = owner, owner.rotationNext
	}
	return false
}

// A locked task can retire its physical M while its clean successor services
// the same released lineage. Wait on the current exact physical record; if
// RetireSelf removes it, follow the already-published scalar lineage. Each
// successful wait acknowledges the C wrapper's return before Go metadata is
// inspected or that physical pthread can be redispatched.
func coroNativeRotationAwaitReturnedV1(rootSlot uint32, root, parent *coroNativeMOwnerV1, baton coro.ExecutionDomainHandoffHandle) (uint32, *coroNativeMOwnerV1, bool) {
	for {
		epoch, slot, observed := coroNativeMObserveLineageV1(root)
		if !observed {
			return 0, nil, false
		}
		owner, ok := coroNativeMOwnerForSlotV1(slot)
		if !ok {
			return 0, nil, false
		}
		lifecycle := coroNativeMOwnerLifecycleLoadV1(owner)
		if lifecycle == coroNativeMOwnerUnusedV1 || lifecycle == coroNativeMOwnerPreparingV1 || lifecycle == coroNativeMOwnerFailedV1 || !owner.retirement.AcquireMetadata() {
			nextEpoch, _, valid := coroNativeMObserveLineageV1(root)
			if valid && nextEpoch != epoch {
				continue
			}
			return 0, nil, false
		}
		// The slot may have been retired and reused between the root snapshot
		// and this pin. Validate its publication epoch before reading any native
		// outputs; Open alone does not mean OwnerReady has published them yet.
		pinnedEpoch, pinnedSlot, pinned := coroNativeMObserveLineageV1(root)
		if !pinned || pinnedEpoch != epoch || pinnedSlot != slot {
			if !coroNativeMReleaseMetadataV1(slot, owner) || !pinned {
				return 0, nil, false
			}
			continue
		}
		if owner.lineageRootSlot != rootSlot || owner.parentSlot == 0 || owner.baton != baton {
			released := coroNativeMReleaseMetadataV1(slot, owner)
			nextEpoch, _, valid := coroNativeMObserveLineageV1(root)
			if released && valid && nextEpoch != epoch {
				continue
			}
			return 0, nil, false
		}
		var dispatch uint32
		if corofleet.RetainReturnedOwner(owner.thread, owner.token, slot, &dispatch) == 0 {
			returnedSlot, returned, valid := coroNativeMReplacementLineageOwnerV1(rootSlot, root, parent, baton)
			valid = valid && returnedSlot == slot && returned == owner && dispatch != 0 && coroNativeMOwnerLifecycleLoadV1(owner) == coroNativeMOwnerReturnedV1
			if !coroNativeMReleaseMetadataV1(slot, owner) || !valid {
				return 0, nil, false
			}
			// C returned the exact live endpoint to this unique parent. It cannot
			// retire or be reused until the parent next dispatches or releases it.
			owner.rotationDispatch = dispatch
			return slot, owner, true
		}
		// Keep metadata pinned while proving physical retirement and a newer,
		// fully published successor. Slot reuse cannot turn this observation into
		// an ABA; the root epoch changes on every successor publication.
		nextEpoch, nextSlot, valid := coroNativeMObserveLineageV1(root)
		retired := valid && nextEpoch > epoch && nextSlot != slot && coroNativeMOwnerLifecycleLoadV1(owner) == coroNativeMOwnerRetiredV1
		if !coroNativeMReleaseMetadataV1(slot, owner) || !retired {
			return 0, nil, false
		}
	}
}

func coroNativeRotateOSThreadOwnersV1(p *coro.P, driver *coro.ExecutorDriver, task *coro.G) bool {
	owner, domain, slot, epoch, ok := coroNativeMCurrentOwnerV1(driver)
	if !ok || domain == nil || domain.pOwnerV1() != p || slot == 0 || epoch == 0 {
		return false
	}
	rotation := owner.rotation
	if rotation == nil {
		rotation = new(coroNativeOSThreadRotationV1)
		rotation.rootSlot = slot
		owner.rotation = rotation
	}
	if task != nil && !coro.DetachOSThreadSuspension(driver, task, &owner.suspension, slot) {
		return false
	}
	if rotation.active {
		if slot == rotation.rootSlot || owner.parentSlot != rotation.rootSlot {
			return false
		}
		return coroNativeRotationRequestReturnV1(owner)
	}
	rotation.rootSlot = slot
	rotation.active = true
	for {
		outerReturn, valid := coroNativeRotationOuterReturnRequestedV1(owner)
		if !valid {
			return false
		}
		if outerReturn {
			rotation.active = false
			return true
		}
		record, readySlot, readyOK := coro.ReadyOSThreadSuspension(driver)
		if !readyOK {
			return false
		}
		if record != nil && readySlot == slot {
			if record != &owner.suspension || !coro.RestoreOSThreadSuspension(driver, record, slot) {
				return false
			}
			rotation.active = false
			return true
		}
		if record == nil && owner.suspension == (coro.OSThreadSuspension{}) {
			rotation.active = false
			return true
		}
		baton, begun := owner.handoff.Begin(epoch)
		if !begun {
			return false
		}
		var child *coroNativeMOwnerV1
		var childSlot uint32
		if record != nil {
			childSlot = readySlot
			child, ok = coroNativeMOwnerForSlotV1(childSlot)
			if !ok || record != &child.suspension || child.rotation != rotation || child.rotationDispatch == 0 ||
				coroNativeMOwnerLifecycleLoadV1(child) != coroNativeMOwnerReturnedV1 ||
				!child.handoff.Idle() || !child.deferred.Idle() || child.resume.Detached() ||
				!coroNativeRotationRemoveRetainedV1(rotation, childSlot) {
				return false
			}
			child.baton = baton
			child.parentSlot = slot
			child.ownerEpoch = epoch
			child.predecessorSlot = 0
			child.lineageRootSlot = childSlot
			coroNativeAtomicStoreV1(&child.lineageSlot, childSlot)
			coroNativeAtomicStoreV1(&child.lineageEpoch, 1)
			coroNativeAtomicStoreV1(&child.lifecycle, uint32(coroNativeMOwnerReplacementPublishedV1))
			dispatch := child.rotationDispatch
			if !coro.RestoreOSThreadSuspension(driver, record, childSlot) {
				return false
			}
			// C acknowledges the new dispatch, not continued Go-record lifetime.
			// The child can retire and recycle its slot before this call returns.
			// Consume all parent-owned receipt storage before publication.
			child.rotationDispatch = 0
			if corofleet.RedispatchReturnedOwner(child.thread, child.token, childSlot, dispatch) != 0 {
				return false
			}
		} else {
			var allocated bool
			childSlot, child, allocated = coroNativeMAllocateReplacementV1(slot, domain.handle, baton)
			if !allocated {
				return false
			}
			child.rotation = rotation
			if !coroNativeMStartPhysicalOwnerV1(child, childSlot) {
				return false
			}
		}
		returnedSlot, returned, returnedOK := coroNativeRotationAwaitReturnedV1(childSlot, child, owner, baton)
		if !returnedOK || returned.rotation != rotation ||
			coroNativeAtomicLoadV1(&coroNativeMDirectoryV1State.active[domain.handle.Route-1]) != slot {
			return false
		}
		if returned.suspension != (coro.OSThreadSuspension{}) {
			if returned.rotationNext != 0 {
				return false
			}
			returned.rotationNext = rotation.retained
			rotation.retained = returnedSlot
			if !coroNativeMDetachReturnedLineageV1(returnedSlot, returned) {
				return false
			}
		} else if !coroNativeMRecycleReplacementV1(returnedSlot) {
			return false
		}
		if !owner.handoff.Complete(baton) {
			return false
		}
	}
}
