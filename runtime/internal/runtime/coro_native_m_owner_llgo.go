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
	"github.com/goplus/llgo/runtime/internal/clite/pthread"
	"github.com/goplus/llgo/runtime/internal/coro"
)

const (
	// Match Go's initial runtime/debug.SetMaxThreads contract. Entries are BSS
	// storage, not eagerly created pthreads; the fixed directory makes every
	// C-to-Go owner edge scalar-only and allocation-free.
	coroNativeMDirectoryCapacityV1 uint32 = 10_000
	coroNativeProgramOwnerEpochV1  uint32 = 1
)

type coroNativeMOwnerLifecycleV1 uint32

const (
	coroNativeMOwnerUnusedV1 coroNativeMOwnerLifecycleV1 = iota
	coroNativeMOwnerPreparingV1
	coroNativeMOwnerProgramV1
	coroNativeMOwnerPeerPublishedV1
	coroNativeMOwnerPeerActiveV1
	coroNativeMOwnerReplacementPublishedV1
	coroNativeMOwnerReplacementActiveV1
	coroNativeMOwnerReturnedV1
	coroNativeMOwnerRetiredV1
	coroNativeMOwnerFailedV1
)

type coroNativeMDirectoryLifecycleV1 uint32

const (
	coroNativeMDirectoryUnusedV1 coroNativeMDirectoryLifecycleV1 = iota
	coroNativeMDirectoryPreparingV1
	coroNativeMDirectoryActiveV1
	coroNativeMDirectoryFailedV1
)

// coroNativeMOwnerV1 is stable process storage for one physical M identity.
// Initial owners retain their slots for process life. Replacement slots are
// recycled only after pthread_join, the parent's baton is Complete, and this
// entry's own nested-handoff gate is back at Idle.
type coroNativeMOwnerV1 struct {
	handoff coro.ExecutionDomainHandoff
	resume  coro.ExecutorResumeHandoff

	thread pthread.Thread
	self   pthread.Thread
	handle coro.ExecutorFleetHandle
	baton  coro.ExecutionDomainHandoffHandle

	parentSlot uint32
	ownerEpoch uint32
	lifecycle  uint32
}

type coroNativeMDirectoryV1 struct {
	owners [coroNativeMDirectoryCapacityV1]coroNativeMOwnerV1
	active [coroNativeFleetDomainCapacityV1]uint32
	state  uint32
}

var coroNativeMDirectoryV1State coroNativeMDirectoryV1

func coroNativeMOwnerForSlotV1(slot uint32) (*coroNativeMOwnerV1, bool) {
	if slot == 0 || slot > coroNativeMDirectoryCapacityV1 {
		return nil, false
	}
	return &coroNativeMDirectoryV1State.owners[slot-1], true
}

func coroNativeMOwnerLifecycleLoadV1(owner *coroNativeMOwnerV1) coroNativeMOwnerLifecycleV1 {
	if owner == nil {
		return coroNativeMOwnerFailedV1
	}
	return coroNativeMOwnerLifecycleV1(coroNativeAtomicLoadV1(&owner.lifecycle))
}

func coroNativeMOwnerLifecycleCASV1(
	owner *coroNativeMOwnerV1,
	old, next coroNativeMOwnerLifecycleV1,
) bool {
	return owner != nil && coroNativeAtomicCASV1(
		&owner.lifecycle,
		uint32(old),
		uint32(next),
	)
}

func coroNativeMDirectoryStartV1(program coro.ExecutorFleetHandle) bool {
	directory := &coroNativeMDirectoryV1State
	count := coroNativeFleetV1State.domainCount
	if !program.Valid() || program.Route != 1 ||
		count == 0 || count > coroNativeFleetDomainCapacityV1 ||
		!coroNativeAtomicCASV1(
			&directory.state,
			uint32(coroNativeMDirectoryUnusedV1),
			uint32(coroNativeMDirectoryPreparingV1),
		) {
		return false
	}
	for route := uint32(1); route <= count; route++ {
		owner := &directory.owners[route-1]
		handle, ok := coroNativeFleetHandleV1(route - 1)
		if !ok || handle.Route != route || !owner.handoff.Idle() ||
			owner.resume.Detached() || owner.thread != nil || owner.self != nil ||
			owner.handle != (coro.ExecutorFleetHandle{}) ||
			owner.baton != (coro.ExecutionDomainHandoffHandle{}) ||
			owner.parentSlot != 0 || owner.ownerEpoch != 0 ||
			coroNativeMOwnerLifecycleLoadV1(owner) != coroNativeMOwnerUnusedV1 ||
			coroNativeAtomicLoadV1(&directory.active[route-1]) != 0 {
			coroNativeAtomicStoreV1(&directory.state, uint32(coroNativeMDirectoryFailedV1))
			return false
		}
		owner.handle = handle
		coroNativeAtomicStoreV1(&directory.active[route-1], route)
		if route == 1 {
			self := pthread.Self()
			if self == nil {
				coroNativeAtomicStoreV1(&directory.state, uint32(coroNativeMDirectoryFailedV1))
				return false
			}
			owner.thread = self
			owner.self = self
			owner.ownerEpoch = coroNativeProgramOwnerEpochV1
			coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerProgramV1))
		} else {
			coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerPeerPublishedV1))
		}
	}
	coroNativeAtomicStoreV1(&directory.state, uint32(coroNativeMDirectoryActiveV1))
	return true
}

func coroNativeMInitialPeerV1(
	handle coro.ExecutorFleetHandle,
) (uint32, *coroNativeMOwnerV1, bool) {
	directory := &coroNativeMDirectoryV1State
	if coroNativeAtomicLoadV1(&directory.state) != uint32(coroNativeMDirectoryActiveV1) ||
		!handle.Valid() || handle.Route < 2 ||
		handle.Route > coroNativeFleetV1State.domainCount {
		return 0, nil, false
	}
	slot := handle.Route
	owner, ok := coroNativeMOwnerForSlotV1(slot)
	return slot, owner, ok && owner.handle == handle &&
		coroNativeMOwnerLifecycleLoadV1(owner) == coroNativeMOwnerPeerPublishedV1 &&
		coroNativeAtomicLoadV1(&directory.active[handle.Route-1]) == slot
}

func coroNativeMCurrentOwnerV1(
	driver *coro.ExecutorDriver,
) (
	owner *coroNativeMOwnerV1,
	domain *coroNativeFleetDomainV1,
	slot, epoch uint32,
	ok bool,
) {
	domain, route, domainOK := coroNativeFleetExecutionDomainV1(driver)
	if !domainOK || domain == nil || !route.Valid() {
		return nil, nil, 0, 0, false
	}
	slot = coroNativeAtomicLoadV1(&coroNativeMDirectoryV1State.active[uint32(route)-1])
	owner, ownerOK := coroNativeMOwnerForSlotV1(slot)
	if !ownerOK || owner.handle != domain.handle || owner.self == nil ||
		pthread.Equal(owner.self, pthread.Self()) == 0 {
		return nil, nil, 0, 0, false
	}
	switch coroNativeMOwnerLifecycleLoadV1(owner) {
	case coroNativeMOwnerProgramV1:
		if domain.handle.Route != 1 || owner.ownerEpoch != coroNativeProgramOwnerEpochV1 {
			return nil, nil, 0, 0, false
		}
		epoch = owner.ownerEpoch
	case coroNativeMOwnerPeerActiveV1:
		if domain.handle.Route == 1 || domain.ownerEpoch == 0 {
			return nil, nil, 0, 0, false
		}
		epoch = domain.ownerEpoch
	case coroNativeMOwnerReplacementActiveV1:
		if owner.parentSlot == 0 || owner.ownerEpoch == 0 || !owner.baton.Valid() {
			return nil, nil, 0, 0, false
		}
		epoch = owner.ownerEpoch
	default:
		return nil, nil, 0, 0, false
	}
	return owner, domain, slot, epoch, true
}

func coroNativeMAllocateReplacementV1(
	parentSlot uint32,
	handle coro.ExecutorFleetHandle,
	baton coro.ExecutionDomainHandoffHandle,
) (uint32, *coroNativeMOwnerV1, bool) {
	if parentSlot == 0 || !handle.Valid() || !baton.Valid() {
		return 0, nil, false
	}
	for slot := uint32(coroNativeFleetDomainCapacityV1 + 1); slot <= coroNativeMDirectoryCapacityV1; slot++ {
		owner := &coroNativeMDirectoryV1State.owners[slot-1]
		if !coroNativeMOwnerLifecycleCASV1(
			owner,
			coroNativeMOwnerUnusedV1,
			coroNativeMOwnerPreparingV1,
		) {
			continue
		}
		if owner.thread != nil || owner.self != nil ||
			owner.handle != (coro.ExecutorFleetHandle{}) ||
			owner.baton != (coro.ExecutionDomainHandoffHandle{}) ||
			owner.parentSlot != 0 || owner.ownerEpoch != 0 ||
			owner.resume.Detached() || !owner.handoff.Idle() {
			coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerFailedV1))
			return 0, nil, false
		}
		owner.handle = handle
		owner.baton = baton
		owner.parentSlot = parentSlot
		owner.ownerEpoch = baton.OwnerEpoch
		coroNativeAtomicStoreV1(
			&owner.lifecycle,
			uint32(coroNativeMOwnerReplacementPublishedV1),
		)
		return slot, owner, true
	}
	return 0, nil, false
}

func coroNativeMReleaseUnstartedReplacementV1(slot uint32) bool {
	owner, ok := coroNativeMOwnerForSlotV1(slot)
	if !ok || !coroNativeMOwnerLifecycleCASV1(
		owner,
		coroNativeMOwnerReplacementPublishedV1,
		coroNativeMOwnerPreparingV1,
	) || owner.thread != nil || owner.self != nil || owner.resume.Detached() ||
		!owner.handoff.Idle() {
		return false
	}
	owner.handle = coro.ExecutorFleetHandle{}
	owner.baton = coro.ExecutionDomainHandoffHandle{}
	owner.parentSlot = 0
	owner.ownerEpoch = 0
	coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerUnusedV1))
	return true
}

func coroNativeMRecycleReplacementV1(slot uint32) bool {
	owner, ok := coroNativeMOwnerForSlotV1(slot)
	if !ok || !coroNativeMOwnerLifecycleCASV1(
		owner,
		coroNativeMOwnerReturnedV1,
		coroNativeMOwnerPreparingV1,
	) || owner.thread == nil || owner.self == nil ||
		owner.resume.Detached() || !owner.handoff.Idle() {
		return false
	}
	owner.thread = nil
	owner.self = nil
	owner.handle = coro.ExecutorFleetHandle{}
	owner.baton = coro.ExecutionDomainHandoffHandle{}
	owner.parentSlot = 0
	owner.ownerEpoch = 0
	coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerUnusedV1))
	return true
}

func coroNativeMClaimReplacementV1(
	slot uint32,
) (
	owner, parent *coroNativeMOwnerV1,
	domain *coroNativeFleetDomainV1,
	claimed bool,
	ok bool,
) {
	owner, ownerOK := coroNativeMOwnerForSlotV1(slot)
	if !ownerOK ||
		coroNativeMOwnerLifecycleLoadV1(owner) != coroNativeMOwnerReplacementPublishedV1 ||
		owner.parentSlot == 0 || !owner.handle.Valid() || !owner.baton.Valid() {
		return nil, nil, nil, false, false
	}
	self := pthread.Self()
	if self == nil {
		return nil, nil, nil, false, false
	}
	owner.self = self
	parent, parentOK := coroNativeMOwnerForSlotV1(owner.parentSlot)
	domain, domainOK := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		owner.handle,
		coroNativeFleetDomainActiveV1,
	)
	if !parentOK || !domainOK || parent.handle != owner.handle ||
		owner.ownerEpoch != owner.baton.OwnerEpoch {
		return nil, nil, nil, false, false
	}
	released, releasedOK := parent.handoff.Released()
	if !releasedOK {
		if !parent.handoff.Returned(owner.baton) {
			return nil, nil, nil, false, false
		}
		coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerReturnedV1))
		return owner, parent, domain, false, true
	}
	if released != owner.baton || !parent.handoff.Claim(owner.baton) ||
		!coroNativeAtomicCASV1(
			&coroNativeMDirectoryV1State.active[owner.handle.Route-1],
			owner.parentSlot,
			slot,
		) || !coroNativeMOwnerLifecycleCASV1(
		owner,
		coroNativeMOwnerReplacementPublishedV1,
		coroNativeMOwnerReplacementActiveV1,
	) {
		return nil, nil, nil, false, false
	}
	return owner, parent, domain, true, true
}

func coroNativeMFinishReplacementReturnV1(
	slot uint32,
	owner, parent *coroNativeMOwnerV1,
) bool {
	if owner == nil || parent == nil || owner.parentSlot == 0 ||
		owner.baton.OwnerEpoch != owner.ownerEpoch ||
		coroNativeMOwnerLifecycleLoadV1(owner) != coroNativeMOwnerReplacementActiveV1 ||
		coroNativeAtomicLoadV1(&coroNativeMDirectoryV1State.active[owner.handle.Route-1]) != slot ||
		!coroNativeAtomicCASV1(
			&coroNativeMDirectoryV1State.active[owner.handle.Route-1],
			slot,
			owner.parentSlot,
		) || !parent.handoff.FinishReturn(owner.baton) ||
		!coroNativeMOwnerLifecycleCASV1(
			owner,
			coroNativeMOwnerReplacementActiveV1,
			coroNativeMOwnerReturnedV1,
		) {
		return false
	}
	return true
}

func coroNativeMReplacementReturnRequestedV1(
	owner, parent *coroNativeMOwnerV1,
) bool {
	return owner != nil && parent != nil && owner.baton.Valid() &&
		parent.handoff.ReturnRequested(owner.baton)
}
