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

import "github.com/xgo-dev/llgo/runtime/internal/corofleet"

// The original parent keeps a retired lineage root alive until it consumes
// the returned endpoint. Temporary readers additionally pin intermediate Ms
// while waiting for their exact C token to return or retire.
func coroNativeMReclaimRetiredV1(slot uint32, owner *coroNativeMOwnerV1) bool {
	if slot <= coroNativeFleetDomainCapacityV1 || owner == nil ||
		!owner.handoff.Idle() || !owner.deferred.Idle() || owner.resume.Detached() ||
		!coroNativeMOwnerLifecycleCASV1(owner, coroNativeMOwnerRetiredV1, coroNativeMOwnerPreparingV1) {
		return false
	}
	coroNativeMClearReplacementStorageV1(owner)
	return true
}

func coroNativeMReleaseMetadataV1(slot uint32, owner *coroNativeMOwnerV1) bool {
	reclaim, ok := owner.retirement.ReleaseMetadata()
	if !ok {
		return false
	}
	return !reclaim || coroNativeMReclaimRetiredV1(slot, owner)
}

func coroNativeMReleaseLineageV1(slot uint32, owner *coroNativeMOwnerV1) bool {
	if owner == nil || slot <= coroNativeFleetDomainCapacityV1 || owner.lineageRootSlot != slot {
		return false
	}
	reclaim, ok := owner.retirement.ReleaseLineage()
	if !ok {
		return false
	}
	return !reclaim || coroNativeMReclaimRetiredV1(slot, owner)
}

// Even epochs publish both the canonical slot and the creator's native
// thread/token outputs. The successor opens an odd epoch in Claim and closes
// it only after OwnerReady returns from C's publication rendezvous.
func coroNativeMObserveLineageV1(root *coroNativeMOwnerV1) (uint32, uint32, bool) {
	if root == nil {
		return 0, 0, false
	}
	for {
		epoch := coroNativeAtomicLoadV1(&root.lineageEpoch)
		if epoch == 0 {
			return 0, 0, false
		}
		if epoch&1 != 0 {
			if corofleet.Yield() != 0 {
				return 0, 0, false
			}
			continue
		}
		slot := coroNativeAtomicLoadV1(&root.lineageSlot)
		if epoch == coroNativeAtomicLoadV1(&root.lineageEpoch) {
			return epoch, slot, slot != 0
		}
	}
}

func coroNativeMPublishLineageReadyV1(owner *coroNativeMOwnerV1, slot uint32) bool {
	if owner == nil || slot == 0 {
		return false
	}
	if !owner.baton.Valid() {
		return owner.lineageRootSlot == 0
	}
	root, ok := coroNativeMOwnerForSlotV1(owner.lineageRootSlot)
	if !ok || root.baton != owner.baton || root.parentSlot != owner.parentSlot ||
		coroNativeAtomicLoadV1(&root.lineageSlot) != slot {
		return false
	}
	epoch := coroNativeAtomicLoadV1(&root.lineageEpoch)
	return epoch&1 != 0 && epoch != ^uint32(0) &&
		coroNativeAtomicCASV1(&root.lineageEpoch, epoch, epoch+1)
}

// A retained returned M starts its next dispatch as its own lineage root.
// The old root can be released only after every old-lineage read has ended.
func coroNativeMDetachReturnedLineageV1(slot uint32, owner *coroNativeMOwnerV1) bool {
	rootSlot := owner.lineageRootSlot
	if rootSlot == slot {
		return true
	}
	root, ok := coroNativeMOwnerForSlotV1(rootSlot)
	if !ok || root.baton != owner.baton || root.parentSlot != owner.parentSlot ||
		coroNativeMOwnerLifecycleLoadV1(owner) != coroNativeMOwnerReturnedV1 {
		return false
	}
	owner.predecessorSlot = 0
	owner.lineageRootSlot = slot
	coroNativeAtomicStoreV1(&owner.lineageSlot, slot)
	coroNativeAtomicStoreV1(&owner.lineageEpoch, 2)
	return coroNativeMReleaseLineageV1(rootSlot, root)
}
