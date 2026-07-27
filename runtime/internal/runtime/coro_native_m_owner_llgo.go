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
	"github.com/goplus/llgo/runtime/internal/corofleet"
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
	coroNativeMOwnerSuccessorPublishedV1
	coroNativeMOwnerSuccessorActiveV1
	coroNativeMOwnerRetiringV1
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
// Initial owners retain their slots for process life. A replacement slot is
// recycled only after its parent baton proves Returned, the raw wrapper has
// acknowledged that exact token/slot, and this entry's nested-handoff gate is
// back at Idle. The physical pthread may then remain in the bounded C standby
// cache independently of this logical directory slot.
type coroNativeMOwnerV1 struct {
	handoff coro.ExecutionDomainHandoff
	resume  coro.ExecutorResumeHandoff

	thread pthread.Thread
	self   pthread.Thread
	token  uint32
	handle coro.ExecutorFleetHandle
	baton  coro.ExecutionDomainHandoffHandle

	parentSlot      uint32
	predecessorSlot uint32
	lineageRootSlot uint32
	lineageSlot     uint32
	ownerEpoch      uint32
	lifecycle       uint32
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

func coroNativeMStartPhysicalOwnerV1(
	owner *coroNativeMOwnerV1,
	slot uint32,
) bool {
	if owner == nil || slot == 0 || owner.thread != nil || owner.token != 0 {
		return false
	}
	switch corofleet.TryReuseOwner(&owner.thread, &owner.token, slot) {
	case 0:
		return owner.thread != nil && owner.token != 0
	case 1:
		owner.thread = nil
		owner.token = 0
	default:
		owner.thread = nil
		owner.token = 0
		return false
	}
	if !coroTargetReservePhysicalThreadV1() {
		return false
	}
	if corofleet.CreateOwner(&owner.thread, &owner.token, slot) == 0 &&
		owner.thread != nil && owner.token != 0 {
		return true
	}
	owner.thread = nil
	owner.token = 0
	if !coroTargetReleasePhysicalThreadV1() {
		coroRuntimeAbort("native coroutine M create reservation rollback failed")
	}
	return false
}

func coroNativeMJoinPhysicalOwnerV1(owner *coroNativeMOwnerV1) bool {
	if owner == nil || owner.thread == nil || owner.token == 0 ||
		corofleet.JoinOwner(owner.thread, owner.token) != 0 {
		return false
	}
	return coroTargetReleasePhysicalThreadV1()
}

func coroNativeMStopStandbyV1() bool {
	var joined uint32
	if corofleet.StopStandby(&joined) != 0 {
		return false
	}
	for joined != 0 {
		if !coroTargetReleasePhysicalThreadV1() {
			return false
		}
		joined--
	}
	return true
}

func coroNativeMStartCleanFactoryV1() bool {
	if !coroTargetReservePhysicalThreadV1() {
		return false
	}
	if corofleet.StartFactory() == 0 {
		return true
	}
	if !coroTargetReleasePhysicalThreadV1() {
		coroRuntimeAbort("native coroutine clean M factory reservation rollback failed")
	}
	return false
}

func coroNativeMStopCleanFactoryV1() bool {
	terminalToken := uint32(0)
	slot := coroNativeAtomicLoadV1(&coroNativeMDirectoryV1State.active[0])
	owner, ownerOK := coroNativeMOwnerForSlotV1(slot)
	if ownerOK && owner.token != 0 && owner.self != nil &&
		pthread.Equal(owner.self, pthread.Self()) != 0 {
		if owner.handle.Route != 1 ||
			coroNativeMOwnerLifecycleLoadV1(owner) !=
				coroNativeMOwnerSuccessorActiveV1 {
			return false
		}
		terminalToken = owner.token
	}
	return corofleet.StopFactory(terminalToken) == 0 &&
		coroTargetReleasePhysicalThreadV1()
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
			owner.token != 0 ||
			owner.handle != (coro.ExecutorFleetHandle{}) ||
			owner.baton != (coro.ExecutionDomainHandoffHandle{}) ||
			owner.parentSlot != 0 || owner.predecessorSlot != 0 ||
			owner.lineageRootSlot != 0 ||
			coroNativeAtomicLoadV1(&owner.lineageSlot) != 0 ||
			owner.ownerEpoch != 0 ||
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
		if owner.parentSlot == 0 || owner.predecessorSlot != 0 ||
			owner.lineageRootSlot != slot ||
			coroNativeAtomicLoadV1(&owner.lineageSlot) != slot ||
			owner.ownerEpoch == 0 || !owner.baton.Valid() {
			return nil, nil, 0, 0, false
		}
		epoch = owner.ownerEpoch
	case coroNativeMOwnerSuccessorActiveV1:
		if owner.predecessorSlot == 0 || owner.ownerEpoch == 0 {
			return nil, nil, 0, 0, false
		}
		if owner.baton.Valid() {
			if owner.parentSlot == 0 || owner.lineageRootSlot == 0 {
				return nil, nil, 0, 0, false
			}
		} else if owner.parentSlot != 0 || owner.lineageRootSlot != 0 {
			return nil, nil, 0, 0, false
		}
		if domain.handle.Route == 1 {
			if !domain.adopted || owner.ownerEpoch != coroNativeProgramOwnerEpochV1 {
				return nil, nil, 0, 0, false
			}
		} else if domain.adopted || domain.ownerEpoch == 0 ||
			owner.ownerEpoch != domain.ownerEpoch {
			return nil, nil, 0, 0, false
		}
		epoch = owner.ownerEpoch
	default:
		return nil, nil, 0, 0, false
	}
	return owner, domain, slot, epoch, true
}

func coroNativeMAllocateSuccessorV1(
	predecessorSlot uint32,
	predecessor *coroNativeMOwnerV1,
	epoch uint32,
	replacement bool,
) (uint32, *coroNativeMOwnerV1, bool) {
	if predecessorSlot == 0 || predecessor == nil || epoch == 0 ||
		!predecessor.handle.Valid() ||
		coroNativeMOwnerLifecycleLoadV1(predecessor) != coroNativeMOwnerRetiringV1 {
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
			owner.token != 0 ||
			owner.handle != (coro.ExecutorFleetHandle{}) ||
			owner.baton != (coro.ExecutionDomainHandoffHandle{}) ||
			owner.parentSlot != 0 || owner.predecessorSlot != 0 ||
			owner.lineageRootSlot != 0 ||
			coroNativeAtomicLoadV1(&owner.lineageSlot) != 0 ||
			owner.ownerEpoch != 0 ||
			owner.resume.Detached() || !owner.handoff.Idle() {
			coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerFailedV1))
			return 0, nil, false
		}
		owner.handle = predecessor.handle
		owner.predecessorSlot = predecessorSlot
		owner.ownerEpoch = epoch
		if replacement {
			rootSlot := predecessor.lineageRootSlot
			if rootSlot == 0 || predecessor.parentSlot == 0 ||
				!predecessor.baton.Valid() {
				coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerFailedV1))
				return 0, nil, false
			}
			owner.baton = predecessor.baton
			owner.parentSlot = predecessor.parentSlot
			owner.lineageRootSlot = rootSlot
		} else if predecessor.parentSlot != 0 ||
			predecessor.lineageRootSlot != 0 || predecessor.baton.Valid() {
			coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerFailedV1))
			return 0, nil, false
		}
		coroNativeAtomicStoreV1(
			&owner.lifecycle,
			uint32(coroNativeMOwnerSuccessorPublishedV1),
		)
		return slot, owner, true
	}
	return 0, nil, false
}

func coroNativeMClaimSuccessorV1(
	slot uint32,
) (
	owner, predecessor *coroNativeMOwnerV1,
	domain *coroNativeFleetDomainV1,
	ok bool,
) {
	owner, ownerOK := coroNativeMOwnerForSlotV1(slot)
	if !ownerOK ||
		coroNativeMOwnerLifecycleLoadV1(owner) != coroNativeMOwnerSuccessorPublishedV1 ||
		owner.predecessorSlot == 0 || owner.ownerEpoch == 0 || !owner.handle.Valid() ||
		owner.self != nil {
		return nil, nil, nil, false
	}
	self := pthread.Self()
	if self == nil {
		return nil, nil, nil, false
	}
	predecessor, predecessorOK := coroNativeMOwnerForSlotV1(owner.predecessorSlot)
	domain, domainOK := coroNativeFleetDomainForHandleV1(
		&coroNativeFleetV1State,
		owner.handle,
		coroNativeFleetDomainActiveV1,
	)
	if !predecessorOK || !domainOK ||
		coroNativeMOwnerLifecycleLoadV1(predecessor) != coroNativeMOwnerRetiringV1 ||
		predecessor.handle != owner.handle ||
		coroNativeAtomicLoadV1(
			&coroNativeMDirectoryV1State.active[owner.handle.Route-1],
		) != owner.predecessorSlot {
		return nil, nil, nil, false
	}
	if owner.handle.Route == 1 {
		if !domain.adopted || owner.ownerEpoch != coroNativeProgramOwnerEpochV1 {
			return nil, nil, nil, false
		}
	} else if domain.adopted || domain.ownerEpoch == 0 ||
		owner.ownerEpoch != domain.ownerEpoch {
		return nil, nil, nil, false
	}
	var lineageRoot *coroNativeMOwnerV1
	if owner.baton.Valid() {
		var rootOK bool
		lineageRoot, rootOK = coroNativeMOwnerForSlotV1(owner.lineageRootSlot)
		parent, parentOK := coroNativeMOwnerForSlotV1(owner.parentSlot)
		if !rootOK || !parentOK || lineageRoot.lineageRootSlot != owner.lineageRootSlot ||
			lineageRoot.baton != owner.baton ||
			lineageRoot.parentSlot != owner.parentSlot ||
			coroNativeAtomicLoadV1(&lineageRoot.lineageSlot) != owner.predecessorSlot ||
			parent.handle != owner.handle {
			return nil, nil, nil, false
		}
	} else if owner.parentSlot != 0 || owner.lineageRootSlot != 0 {
		return nil, nil, nil, false
	}
	var peer *coroNativeFleetPhysicalOwnerV1
	if !owner.baton.Valid() && owner.handle.Route != 1 {
		var peerOK bool
		peer, peerOK = coroNativeFleetPhysicalOwnerForHandleV1(owner.handle)
		if !peerOK ||
			coroNativeAtomicLoadV1(&peer.slot) != owner.predecessorSlot {
			return nil, nil, nil, false
		}
	}
	owner.self = self
	if !coroNativeAtomicCASV1(
		&coroNativeMDirectoryV1State.active[owner.handle.Route-1],
		owner.predecessorSlot,
		slot,
	) || !coroNativeMOwnerLifecycleCASV1(
		owner,
		coroNativeMOwnerSuccessorPublishedV1,
		coroNativeMOwnerSuccessorActiveV1,
	) {
		return nil, nil, nil, false
	}
	if lineageRoot != nil && !coroNativeAtomicCASV1(
		&lineageRoot.lineageSlot,
		owner.predecessorSlot,
		slot,
	) {
		return nil, nil, nil, false
	}
	if peer != nil {
		coroNativeAtomicStoreV1(&peer.slot, slot)
	}
	return owner, predecessor, domain, true
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
			owner.token != 0 ||
			owner.handle != (coro.ExecutorFleetHandle{}) ||
			owner.baton != (coro.ExecutionDomainHandoffHandle{}) ||
			owner.parentSlot != 0 || owner.predecessorSlot != 0 ||
			owner.lineageRootSlot != 0 ||
			coroNativeAtomicLoadV1(&owner.lineageSlot) != 0 ||
			owner.ownerEpoch != 0 ||
			owner.resume.Detached() || !owner.handoff.Idle() {
			coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerFailedV1))
			return 0, nil, false
		}
		owner.handle = handle
		owner.baton = baton
		owner.parentSlot = parentSlot
		owner.lineageRootSlot = slot
		coroNativeAtomicStoreV1(&owner.lineageSlot, slot)
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
		owner.token != 0 ||
		!owner.handoff.Idle() || owner.predecessorSlot != 0 ||
		owner.lineageRootSlot != slot ||
		coroNativeAtomicLoadV1(&owner.lineageSlot) != slot {
		return false
	}
	owner.handle = coro.ExecutorFleetHandle{}
	owner.baton = coro.ExecutionDomainHandoffHandle{}
	owner.parentSlot = 0
	owner.lineageRootSlot = 0
	coroNativeAtomicStoreV1(&owner.lineageSlot, 0)
	owner.ownerEpoch = 0
	coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerUnusedV1))
	return true
}

func coroNativeMClearReplacementStorageV1(owner *coroNativeMOwnerV1) {
	owner.thread = nil
	owner.self = nil
	owner.token = 0
	owner.handle = coro.ExecutorFleetHandle{}
	owner.baton = coro.ExecutionDomainHandoffHandle{}
	owner.parentSlot = 0
	owner.predecessorSlot = 0
	owner.lineageRootSlot = 0
	coroNativeAtomicStoreV1(&owner.lineageSlot, 0)
	owner.ownerEpoch = 0
	coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerUnusedV1))
}

func coroNativeMRecycleReplacementV1(slot uint32) bool {
	owner, ok := coroNativeMOwnerForSlotV1(slot)
	if !ok || coroNativeMOwnerLifecycleLoadV1(owner) != coroNativeMOwnerReturnedV1 ||
		owner.thread == nil || owner.self == nil || owner.token == 0 ||
		!owner.handle.Valid() ||
		owner.handle.Route > coroNativeFleetDomainCapacityV1 ||
		!owner.baton.Valid() || owner.parentSlot == 0 ||
		owner.lineageRootSlot == 0 ||
		owner.ownerEpoch != owner.baton.OwnerEpoch ||
		owner.resume.Detached() || !owner.handoff.Idle() {
		return false
	}
	root, rootOK := coroNativeMOwnerForSlotV1(owner.lineageRootSlot)
	parent, parentOK := coroNativeMOwnerForSlotV1(owner.parentSlot)
	if !rootOK || !parentOK || root.lineageRootSlot != owner.lineageRootSlot ||
		root.baton != owner.baton || root.parentSlot != owner.parentSlot ||
		coroNativeAtomicLoadV1(&root.lineageSlot) != slot ||
		parent.handle != owner.handle || !parent.handoff.Returned(owner.baton) ||
		coroNativeAtomicLoadV1(
			&coroNativeMDirectoryV1State.active[owner.handle.Route-1],
		) != owner.parentSlot ||
		!coroNativeMOwnerLifecycleCASV1(
			owner,
			coroNativeMOwnerReturnedV1,
			coroNativeMOwnerPreparingV1,
		) {
		return false
	}
	released := corofleet.ReleaseOwner(
		owner.thread,
		owner.token,
		slot,
	)
	if released != 0 && released != 1 {
		coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerFailedV1))
		return false
	}
	if released == 1 && !coroTargetReleasePhysicalThreadV1() {
		coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerFailedV1))
		return false
	}
	coroNativeMClearReplacementStorageV1(owner)
	return true
}

// coroNativeMWaitAndRecycleOSThreadSuspendV1 is the original M's blocking
// rendezvous for an ordinary locked Yield/Park handoff. ReleaseOwner waits on
// corofleet's existing condition variable while the exact child is still in
// Go; there is no scheduler busy loop. This staged protocol admits only the
// original replacement record, not a retirement successor lineage.
func coroNativeMWaitAndRecycleOSThreadSuspendV1(
	slot uint32,
	owner, parent *coroNativeMOwnerV1,
) bool {
	resolved, ownerOK := coroNativeMOwnerForSlotV1(slot)
	if !ownerOK || resolved != owner || owner == nil || parent == nil ||
		owner.thread == nil || owner.self == nil || owner.token == 0 ||
		!owner.handle.Valid() ||
		owner.handle.Route > coroNativeFleetDomainCapacityV1 ||
		!owner.baton.Valid() || owner.parentSlot == 0 ||
		owner.predecessorSlot != 0 ||
		owner.lineageRootSlot != slot ||
		coroNativeAtomicLoadV1(&owner.lineageSlot) != slot ||
		owner.ownerEpoch != owner.baton.OwnerEpoch ||
		owner.resume.Detached() || !owner.handoff.Idle() ||
		parent.handle != owner.handle {
		return false
	}
	switch coroNativeMOwnerLifecycleLoadV1(owner) {
	case coroNativeMOwnerReplacementActiveV1, coroNativeMOwnerReturnedV1:
	default:
		return false
	}
	active := coroNativeAtomicLoadV1(
		&coroNativeMDirectoryV1State.active[owner.handle.Route-1],
	)
	if active != slot && active != owner.parentSlot {
		return false
	}

	released := corofleet.ReleaseOwner(owner.thread, owner.token, slot)
	if released != 0 && released != 1 {
		coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerFailedV1))
		return false
	}
	if coroNativeMOwnerLifecycleLoadV1(owner) != coroNativeMOwnerReturnedV1 ||
		owner.lineageRootSlot != slot ||
		coroNativeAtomicLoadV1(&owner.lineageSlot) != slot ||
		!parent.handoff.Returned(owner.baton) ||
		coroNativeAtomicLoadV1(
			&coroNativeMDirectoryV1State.active[owner.handle.Route-1],
		) != owner.parentSlot ||
		!coroNativeMOwnerLifecycleCASV1(
			owner,
			coroNativeMOwnerReturnedV1,
			coroNativeMOwnerPreparingV1,
		) {
		coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerFailedV1))
		return false
	}
	if released == 1 && !coroTargetReleasePhysicalThreadV1() {
		coroNativeAtomicStoreV1(&owner.lifecycle, uint32(coroNativeMOwnerFailedV1))
		return false
	}
	coroNativeMClearReplacementStorageV1(owner)
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
		owner.parentSlot == 0 || owner.predecessorSlot != 0 ||
		owner.lineageRootSlot != slot ||
		coroNativeAtomicLoadV1(&owner.lineageSlot) != slot ||
		!owner.handle.Valid() || !owner.baton.Valid() {
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
	lifecycle := coroNativeMOwnerLifecycleLoadV1(owner)
	if owner == nil || parent == nil || owner.parentSlot == 0 ||
		owner.baton.OwnerEpoch != owner.ownerEpoch ||
		(lifecycle != coroNativeMOwnerReplacementActiveV1 &&
			(lifecycle != coroNativeMOwnerSuccessorActiveV1 ||
				!owner.baton.Valid())) ||
		coroNativeAtomicLoadV1(&coroNativeMDirectoryV1State.active[owner.handle.Route-1]) != slot ||
		!coroNativeAtomicCASV1(
			&coroNativeMDirectoryV1State.active[owner.handle.Route-1],
			slot,
			owner.parentSlot,
		) || !parent.handoff.FinishReturn(owner.baton) ||
		!coroNativeMOwnerLifecycleCASV1(
			owner,
			lifecycle,
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

func coroNativeMReplacementLineageOwnerV1(
	rootSlot uint32,
	root, parent *coroNativeMOwnerV1,
	baton coro.ExecutionDomainHandoffHandle,
) (uint32, *coroNativeMOwnerV1, bool) {
	if rootSlot == 0 || root == nil || parent == nil || !baton.Valid() ||
		root.lineageRootSlot != rootSlot || root.baton != baton ||
		root.parentSlot == 0 || root.parentSlot > coroNativeMDirectoryCapacityV1 ||
		parent.handle != root.handle || !parent.handoff.Returned(baton) {
		return 0, nil, false
	}
	slot := coroNativeAtomicLoadV1(&root.lineageSlot)
	owner, ownerOK := coroNativeMOwnerForSlotV1(slot)
	if !ownerOK || owner.handle != root.handle || owner.baton != baton ||
		owner.parentSlot != root.parentSlot ||
		owner.lineageRootSlot != rootSlot ||
		coroNativeMOwnerLifecycleLoadV1(owner) != coroNativeMOwnerReturnedV1 {
		return 0, nil, false
	}
	return slot, owner, true
}

func coroNativeMHasBlockedOwnerV1() (bool, bool) {
	directory := &coroNativeMDirectoryV1State
	count := coroNativeFleetV1State.domainCount
	if coroNativeAtomicLoadV1(&directory.state) != uint32(coroNativeMDirectoryActiveV1) ||
		count == 0 || count > coroNativeFleetDomainCapacityV1 {
		return false, false
	}
	for route := uint32(1); route <= count; route++ {
		slot := coroNativeAtomicLoadV1(&directory.active[route-1])
		complete := false
		for depth := uint32(0); depth < coroNativeMDirectoryCapacityV1; depth++ {
			owner, ownerOK := coroNativeMOwnerForSlotV1(slot)
			if !ownerOK || owner.handle.Route != route {
				return false, false
			}
			if !owner.handoff.Idle() {
				return true, true
			}
			if !owner.baton.Valid() || owner.parentSlot == 0 {
				complete = true
				break
			}
			slot = owner.parentSlot
		}
		if !complete {
			return false, false
		}
	}
	return false, true
}

// coroTargetRetirePhysicalOwnerV1 permanently replaces the current native M
// after an unbalanced LockOSThread G exit. The stop ingress is a succession
// lease: close either seals before this transition starts, in which case the
// already-stopping owner may return normally, or waits until a clean successor
// has claimed the exact route before joining the stable peer slots.
func coroTargetRetirePhysicalOwnerV1(
	p *coro.P,
	driver *coro.ExecutorDriver,
) bool {
	state := &coroNativeFleetPhysicalOwnerV1State
	if p == nil || driver == nil {
		return false
	}
	if !state.stop.Enter() {
		// A sealed fleet is already retiring every physical owner. Returning to
		// that one-way shutdown loop cannot expose this M to later managed work.
		return true
	}
	owner, domain, slot, epoch, ownerOK := coroNativeMCurrentOwnerV1(driver)
	if !ownerOK || domain == nil || domain.pOwnerV1() != p ||
		slot == 0 || epoch == 0 {
		_, _ = state.stop.Leave()
		return false
	}
	lifecycle := coroNativeMOwnerLifecycleLoadV1(owner)
	replacement := false
	switch lifecycle {
	case coroNativeMOwnerProgramV1, coroNativeMOwnerPeerActiveV1,
		coroNativeMOwnerSuccessorActiveV1:
		replacement = owner.baton.Valid()
	case coroNativeMOwnerReplacementActiveV1:
		replacement = true
	default:
		_, _ = state.stop.Leave()
		return false
	}
	if replacement {
		parent, parentOK := coroNativeMOwnerForSlotV1(owner.parentSlot)
		if !parentOK || !owner.baton.Valid() {
			_, _ = state.stop.Leave()
			return false
		}
		if parent.handoff.ReturnRequested(owner.baton) {
			// The blocked predecessor already asked this temporary lineage to
			// return. Its next loop boundary exits and is strongly joined, so no
			// reusable M survives this unbalanced lock.
			_, _ = state.stop.Leave()
			return true
		}
	}
	if !coroNativeMOwnerLifecycleCASV1(
		owner,
		lifecycle,
		coroNativeMOwnerRetiringV1,
	) {
		_, _ = state.stop.Leave()
		return false
	}
	successorSlot, successor, allocated := coroNativeMAllocateSuccessorV1(
		slot,
		owner,
		epoch,
		replacement,
	)
	if !allocated || successor == nil ||
		!coroNativeMStartPhysicalOwnerV1(successor, successorSlot) ||
		coroNativeMOwnerLifecycleLoadV1(successor) != coroNativeMOwnerSuccessorActiveV1 {
		coroRuntimeAbort("native coroutine physical owner succession failed")
		return false
	}
	// Seal is allowed to linearize while this succession lease is active.
	// The close owner waits for Quiesced before it snapshots peer.slot, so the
	// successor published above is then the exact thread it will join.
	if _, left := state.stop.Leave(); !left {
		coroRuntimeAbort("native coroutine physical owner succession lease failed")
		return false
	}
	if !coroNativeMOwnerLifecycleCASV1(
		owner,
		coroNativeMOwnerRetiringV1,
		coroNativeMOwnerRetiredV1,
	) {
		coroRuntimeAbort("native coroutine physical owner retirement failed")
		return false
	}
	if slot != 1 && corofleet.RetireSelf(slot) != 0 {
		coroRuntimeAbort("native coroutine physical owner detach failed")
		return false
	}
	if !coroTargetReleasePhysicalThreadV1() {
		coroRuntimeAbort("native coroutine physical owner capacity release failed")
		return false
	}
	pthread.Exit(c.Pointer(nil))
	for {
	}
}

// coroNativeMRunProgramSuccessorV1 continues the already-owned legacy program
// drive admission on a clean raw M. The predecessor never releases that
// logical admission; it exits after CreateOwner observes this successor's
// claim acknowledgement.
func coroNativeMRunProgramSuccessorV1() bool {
	status := coroProgramFinishFreshDriveV1(coroProgramDriveAgainV1)
	switch status {
	case coroProgramDriveCompleteV1:
		c.Exit(0)
	case coroProgramDrivePanicV1:
		coroRuntimeAbort("coroutine program successor terminated by panic")
	default:
		return false
	}
	for {
	}
}
