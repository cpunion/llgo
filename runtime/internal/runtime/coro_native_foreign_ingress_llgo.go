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
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/clite/tls"
	"github.com/xgo-dev/llgo/runtime/internal/coro"
	"github.com/xgo-dev/llgo/runtime/internal/corofleet"
	psync "github.com/xgo-dev/llgo/runtime/internal/sync"
	threadpkg "github.com/xgo-dev/llgo/runtime/internal/thread"
)

const coroNativeForeignIngressCapacityV1 = coroNativeFleetDomainCapacityV1

type coroNativeForeignIngressLifecycleV1 uint8

const (
	coroNativeForeignIngressUnusedV1 coroNativeForeignIngressLifecycleV1 = iota
	coroNativeForeignIngressActiveV1
	coroNativeForeignIngressStoppingV1
	coroNativeForeignIngressRetiredV1
	coroNativeForeignIngressFailedV1
)

type coroNativeForeignIngressPhaseV1 uint8

const (
	coroNativeForeignIngressFreeV1 coroNativeForeignIngressPhaseV1 = iota
	coroNativeForeignIngressPublishedV1
	coroNativeForeignIngressClaimingV1
	coroNativeForeignIngressGrantedV1
	coroNativeForeignIngressRunningV1
	coroNativeForeignIngressReturnedV1
	coroNativeForeignIngressRejectedV1
)

// coroNativeForeignIngressSlotV1 contains only stable rendezvous metadata. The
// compiler passes the independently allocated G directly to the run hook, so
// this process-global slot never roots a task, LLVM handle, argument, result,
// callback function, or wrapper-stack pointer.
type coroNativeForeignIngressSlotV1 struct {
	thread threadpkg.Thread

	generation uint32
	ownerSlot  uint32
	parentSlot uint32
	route      uint32
	phase      coroNativeForeignIngressPhaseV1
}

type coroNativeForeignIngressStateV1 struct {
	mutex psync.Mutex
	cond  psync.Cond

	ingress coro.TargetIngress
	slots   [coroNativeForeignIngressCapacityV1]coroNativeForeignIngressSlotV1

	nextGeneration uint32
	// pending is the sole hot owner probe. Slot contents remain protected by
	// mutex; zero proves that no physical owner needs to enter that cold path.
	pending   uint32
	lifecycle coroNativeForeignIngressLifecycleV1
}

var coroNativeForeignIngressV1State coroNativeForeignIngressStateV1
var coroNativeForeignIngressTLSV1 tls.StaticHandle[*coroNativeForeignIngressSlotV1]
var coroNativeForeignIngressTLSReadyV1 bool

func coroNativeForeignIngressStartV1() bool {
	state := &coroNativeForeignIngressV1State
	if state.lifecycle != coroNativeForeignIngressUnusedV1 ||
		state.nextGeneration != 0 || coroNativeAtomicLoadV1(&state.pending) != 0 ||
		!state.ingress.CanReleaseResources() || coroNativeForeignIngressTLSReadyV1 ||
		state.mutex.Init(nil) != 0 {
		return false
	}
	if state.cond.Init(nil) != 0 {
		state.mutex.Destroy()
		return false
	}
	if !state.ingress.Start() {
		state.cond.Destroy()
		state.mutex.Destroy()
		return false
	}
	coroNativeForeignIngressTLSV1 =
		tls.AllocStatic[*coroNativeForeignIngressSlotV1]()
	coroNativeForeignIngressTLSReadyV1 = true
	state.lifecycle = coroNativeForeignIngressActiveV1
	return true
}

func coroNativeForeignIngressSlotsQuiescedLockedV1(
	state *coroNativeForeignIngressStateV1,
) bool {
	if state == nil {
		return false
	}
	for index := range state.slots {
		slot := &state.slots[index]
		if slot.phase != coroNativeForeignIngressFreeV1 || slot.thread != nil ||
			slot.ownerSlot != 0 || slot.parentSlot != 0 || slot.route != 0 {
			return false
		}
	}
	return true
}

// coroNativeForeignIngressStopV1 closes the process-wide callback rendezvous
// before any fleet route or worker resource is retired. Seal rejects callers
// which have not entered yet; already admitted publishers either finish their
// exact route handoff or leave after observing Stopping. Physical owners stay
// alive for the whole join, so a running callback can still complete timer,
// channel, poll, or worker work without a second shutdown executor.
func coroNativeForeignIngressStopV1() bool {
	state := &coroNativeForeignIngressV1State
	if state.lifecycle != coroNativeForeignIngressActiveV1 ||
		!state.ingress.Seal() {
		return false
	}
	state.mutex.Lock()
	if state.lifecycle != coroNativeForeignIngressActiveV1 {
		state.mutex.Unlock()
		return false
	}
	state.lifecycle = coroNativeForeignIngressStoppingV1
	coroNativeAtomicStoreV1(&state.pending, 0)
	for index := range state.slots {
		slot := &state.slots[index]
		if slot.phase == coroNativeForeignIngressPublishedV1 {
			slot.phase = coroNativeForeignIngressRejectedV1
		}
	}
	state.cond.Broadcast()
	state.mutex.Unlock()

	for {
		state.mutex.Lock()
		quiesced := coroNativeForeignIngressSlotsQuiescedLockedV1(state)
		state.mutex.Unlock()
		if quiesced && state.ingress.Quiesced() {
			break
		}
		if corofleet.Yield() != 0 {
			return false
		}
	}
	if !state.ingress.Retire() {
		return false
	}
	state.mutex.Lock()
	if state.lifecycle != coroNativeForeignIngressStoppingV1 ||
		!coroNativeForeignIngressSlotsQuiescedLockedV1(state) ||
		coroNativeAtomicLoadV1(&state.pending) != 0 {
		state.lifecycle = coroNativeForeignIngressFailedV1
		state.mutex.Unlock()
		return false
	}
	state.lifecycle = coroNativeForeignIngressRetiredV1
	state.mutex.Unlock()
	return true
}

func coroNativeForeignIngressIsRetiredV1() bool {
	state := &coroNativeForeignIngressV1State
	if state.lifecycle != coroNativeForeignIngressRetiredV1 ||
		!state.ingress.Retired() || coroNativeAtomicLoadV1(&state.pending) != 0 {
		return false
	}
	state.mutex.Lock()
	quiesced := coroNativeForeignIngressSlotsQuiescedLockedV1(state)
	state.mutex.Unlock()
	return quiesced
}

func coroNativeForeignIngressRefreshPendingLockedV1(
	state *coroNativeForeignIngressStateV1,
) {
	for index := range state.slots {
		if state.slots[index].phase == coroNativeForeignIngressPublishedV1 {
			coroNativeAtomicStoreV1(&state.pending, 1)
			return
		}
	}
	coroNativeAtomicStoreV1(&state.pending, 0)
}

func coroNativeForeignIngressNextGenerationLockedV1(
	state *coroNativeForeignIngressStateV1,
) (uint32, bool) {
	if state == nil || state.nextGeneration == ^uint32(0) {
		return 0, false
	}
	state.nextGeneration++
	if state.nextGeneration == 0 {
		return 0, false
	}
	return state.nextGeneration, true
}

func coroNativeForeignIngressRouteOwnedV1(
	fleet *coroNativeFleetStateV1,
	index uint32,
) bool {
	if fleet == nil || index >= fleet.domainCount ||
		index >= coroNativeFleetDomainCapacityV1 {
		return false
	}
	domain := &fleet.domains[index]
	owner, resolved, slot, epoch, ok := coroNativeMActiveOwnerInDomainV1(
		domain,
		coro.RouteID(index+1),
	)
	return ok && owner != nil && resolved == domain && slot != 0 && epoch != 0
}

// coroNativeForeignIngressSelectRouteV1 rotates only across routes which have
// a live physical owner in the M directory. Fleet configuration capacity is
// deliberately not availability: at a low execution limit most configured
// Ps have no pthread which could observe an executor request. The directory is
// a fixed eight-word atomic scan on this cold external-entry path; succession
// replaces a slot without removing the route, and shutdown seals ingress before
// stopping any owner.
func coroNativeForeignIngressSelectRouteV1(generation uint32) (uint32, bool) {
	fleet := &coroNativeFleetV1State
	if generation == 0 || fleet.lifecycle != coroNativeFleetActiveV1 ||
		fleet.domainCount == 0 || fleet.domainCount > coroNativeFleetDomainCapacityV1 {
		return 0, false
	}
	active := uint32(0)
	for index := uint32(0); index < fleet.domainCount; index++ {
		domain := &fleet.domains[index]
		if domain.lifecycle == coroNativeFleetDomainActiveV1 &&
			domain.handle.Route == index+1 &&
			coroNativeForeignIngressRouteOwnedV1(fleet, index) {
			active++
		}
	}
	if active == 0 {
		return 0, false
	}
	wanted := (generation - 1) % active
	for index := uint32(0); index < fleet.domainCount; index++ {
		domain := &fleet.domains[index]
		if domain.lifecycle != coroNativeFleetDomainActiveV1 ||
			domain.handle.Route != index+1 ||
			!coroNativeForeignIngressRouteOwnedV1(fleet, index) {
			continue
		}
		if wanted == 0 {
			return index + 1, true
		}
		wanted--
	}
	return 0, false
}

func coroNativeForeignIngressPublishV1(
	thread threadpkg.Thread,
) (*coroNativeForeignIngressSlotV1, bool) {
	state := &coroNativeForeignIngressV1State
	if thread == nil || !state.ingress.Enter() {
		return nil, false
	}
	state.mutex.Lock()
	for state.lifecycle == coroNativeForeignIngressActiveV1 {
		for index := range state.slots {
			slot := &state.slots[index]
			if slot.phase != coroNativeForeignIngressFreeV1 {
				continue
			}
			generation, generationOK :=
				coroNativeForeignIngressNextGenerationLockedV1(state)
			route, routeOK := coroNativeForeignIngressSelectRouteV1(generation)
			if !generationOK || !routeOK {
				state.lifecycle = coroNativeForeignIngressFailedV1
				state.cond.Broadcast()
				state.mutex.Unlock()
				_, _ = state.ingress.Leave()
				return nil, false
			}
			slot.thread = thread
			slot.generation = generation
			slot.route = route
			slot.phase = coroNativeForeignIngressPublishedV1
			coroNativeAtomicStoreV1(&state.pending, 1)
			state.mutex.Unlock()
			return slot, true
		}
		if state.cond.Wait(&state.mutex) != 0 {
			state.lifecycle = coroNativeForeignIngressFailedV1
			break
		}
	}
	state.mutex.Unlock()
	_, _ = state.ingress.Leave()
	return nil, false
}

func coroNativeForeignIngressRequestOwnerV1(
	slot *coroNativeForeignIngressSlotV1,
) bool {
	state := &coroNativeFleetV1State
	if slot == nil || state.lifecycle != coroNativeFleetActiveV1 ||
		slot.route == 0 || slot.route > state.domainCount ||
		state.domainCount > coroNativeFleetDomainCapacityV1 {
		return false
	}
	domain := &state.domains[slot.route-1]
	if domain.lifecycle != coroNativeFleetDomainActiveV1 ||
		!domain.handle.Valid() || domain.handle.Route != slot.route ||
		!domain.ingress.Enter() {
		return false
	}
	request := state.fleet.RequestExecutor(domain.handle)
	requestOK := coroNativeFleetFinishExecutorRequestV1(domain, request)
	_, leaveOK := domain.ingress.Leave()
	return coro.ExecutorRequestAccepted(request) && requestOK && leaveOK
}

func coroNativeForeignIngressAwaitGrantV1(
	slot *coroNativeForeignIngressSlotV1,
) (uint32, bool) {
	state := &coroNativeForeignIngressV1State
	if slot == nil {
		return 0, false
	}
	state.mutex.Lock()
	for slot.phase == coroNativeForeignIngressPublishedV1 ||
		slot.phase == coroNativeForeignIngressClaimingV1 {
		if state.lifecycle != coroNativeForeignIngressActiveV1 ||
			state.cond.Wait(&state.mutex) != 0 {
			state.mutex.Unlock()
			return 0, false
		}
	}
	ownerSlot := slot.ownerSlot
	ok := slot.phase == coroNativeForeignIngressGrantedV1 && ownerSlot != 0
	rejected := slot.phase == coroNativeForeignIngressRejectedV1 &&
		ownerSlot == 0 && slot.parentSlot == 0 && slot.route != 0
	if rejected {
		slot.thread = nil
		slot.route = 0
		slot.phase = coroNativeForeignIngressFreeV1
		state.cond.Broadcast()
	}
	state.mutex.Unlock()
	if rejected {
		_, _ = state.ingress.Leave()
	}
	return ownerSlot, ok
}

func coroNativeForeignIngressAcquireV1(
	parentOut *unsafe.Pointer,
) unsafe.Pointer {
	if parentOut == nil || !coroNativeForeignIngressTLSReadyV1 ||
		coroNativeForeignIngressTLSV1.Get() != nil {
		coroRuntimeAbort("invalid standalone foreign ingress acquire")
	}
	// On collector builds this registers a genuinely foreign pthread before
	// getg allocates its retained physical placeholder. Collector-created M
	// threads and previously attached foreign threads take the no-op path.
	_ = EnterForeignThread()
	if getg() == nil {
		coroRuntimeAbort("standalone foreign ingress cannot attach runtime thread")
	}
	self := threadpkg.Self()
	slot, published := coroNativeForeignIngressPublishV1(self)
	if !published || !coroNativeForeignIngressRequestOwnerV1(slot) {
		coroRuntimeAbort("standalone foreign ingress publication failed")
	}
	ownerSlot, granted := coroNativeForeignIngressAwaitGrantV1(slot)
	if !granted {
		coroRuntimeAbort("standalone foreign ingress grant failed")
	}
	owner, _, _, claimed := coroNativeMClaimForeignIngressV1(ownerSlot)
	if !claimed || owner == nil || owner.self == nil ||
		threadpkg.Equal(owner.self, self) == 0 {
		coroRuntimeAbort("standalone foreign ingress owner claim failed")
	}
	state := &coroNativeForeignIngressV1State
	state.mutex.Lock()
	if slot.ownerSlot != ownerSlot || slot.phase != coroNativeForeignIngressGrantedV1 {
		state.mutex.Unlock()
		coroRuntimeAbort("standalone foreign ingress grant changed during claim")
	}
	slot.phase = coroNativeForeignIngressRunningV1
	state.cond.Broadcast()
	state.mutex.Unlock()
	coroNativeForeignIngressTLSV1.Set(slot)
	task, taskOK := coroForeignIngressRootBeginV1()
	if !taskOK {
		coroRuntimeAbort("standalone foreign ingress root allocation failed")
	}
	*parentOut = nil
	return unsafe.Pointer(task)
}

func coroNativeForeignIngressSelectPublishedV1(
	route uint32,
) (*coroNativeForeignIngressSlotV1, bool) {
	state := &coroNativeForeignIngressV1State
	state.mutex.Lock()
	if state.lifecycle != coroNativeForeignIngressActiveV1 {
		// A physical owner may have sampled pending immediately before the
		// close owner sealed this ingress. The lock orders that stale hot-path
		// observation after Stopping, so it is an empty successful probe rather
		// than a scheduler failure.
		stopping := state.lifecycle == coroNativeForeignIngressStoppingV1 ||
			state.lifecycle == coroNativeForeignIngressRetiredV1
		state.mutex.Unlock()
		return nil, stopping
	}
	if route == 0 || route > coroNativeFleetV1State.domainCount {
		state.mutex.Unlock()
		return nil, false
	}
	for index := range state.slots {
		slot := &state.slots[index]
		if slot.phase == coroNativeForeignIngressPublishedV1 &&
			slot.route == route {
			slot.phase = coroNativeForeignIngressClaimingV1
			coroNativeForeignIngressRefreshPendingLockedV1(state)
			state.mutex.Unlock()
			return slot, true
		}
	}
	// Another route may own a different published slot. Clearing the shared
	// fast-path word merely because this route found no match can strand that
	// exact request until an unrelated future publication. Retain the word from
	// the complete fixed-capacity snapshot instead.
	coroNativeForeignIngressRefreshPendingLockedV1(state)
	state.mutex.Unlock()
	return nil, true
}

func coroNativeForeignIngressRepublishV1(
	slot *coroNativeForeignIngressSlotV1,
) bool {
	state := &coroNativeForeignIngressV1State
	state.mutex.Lock()
	if slot == nil || slot.phase != coroNativeForeignIngressClaimingV1 ||
		slot.ownerSlot != 0 || slot.parentSlot != 0 || slot.route == 0 ||
		slot.route > coroNativeFleetV1State.domainCount {
		state.mutex.Unlock()
		return false
	}
	switch state.lifecycle {
	case coroNativeForeignIngressActiveV1:
		slot.phase = coroNativeForeignIngressPublishedV1
		coroNativeAtomicStoreV1(&state.pending, 1)
	case coroNativeForeignIngressStoppingV1:
		// Stop rejects Published slots itself. A slot already claimed by an
		// owner is completed here after that owner rolls its handoff back.
		slot.phase = coroNativeForeignIngressRejectedV1
	default:
		state.mutex.Unlock()
		return false
	}
	state.cond.Broadcast()
	state.mutex.Unlock()
	return true
}

// coroNativeForeignIngressTryServeV1 is called only at an outer physical-owner
// scheduler boundary. The zero pending load is the complete ordinary fast
// path; a request winner temporarily hands this exact route to the already
// existing foreign pthread and blocks until that owner has stopped touching P.
func coroNativeForeignIngressTryServeV1(
	p *coro.P,
	driver *coro.ExecutorDriver,
) (served, ok bool) {
	state := &coroNativeForeignIngressV1State
	if coroNativeAtomicLoadV1(&state.pending) == 0 {
		return false, true
	}
	owner, domain, parentSlot, epoch, ownerOK :=
		coroNativeMCurrentOwnerV1(driver)
	if !ownerOK || domain == nil || domain.pOwnerV1() != p ||
		parentSlot == 0 || epoch == 0 || !owner.handoff.Idle() ||
		!owner.deferred.Idle() || !coro.EnterExecutorRunCompatibility(driver) ||
		!coro.ExecutorResumeHandoffReturnable(driver) {
		// A locked/split reduction is an ordinary temporary rejection. Another
		// route may claim the shared request, or this owner retries after its next
		// stable reduction.
		return false, true
	}
	slot, selected := coroNativeForeignIngressSelectPublishedV1(
		uint32(domain.handle.Route),
	)
	if !selected || slot == nil {
		return false, selected
	}
	baton, begun := owner.handoff.Begin(epoch)
	if !begun {
		return false, coroNativeForeignIngressRepublishV1(slot)
	}
	childSlot, child, allocated := coroNativeMAllocateForeignIngressV1(
		parentSlot,
		owner,
		baton,
		slot.thread,
	)
	if !allocated {
		rolledBack := owner.handoff.RequestReturn(baton) ==
			coro.ExecutionDomainHandoffReturnUnclaimed &&
			owner.handoff.Complete(baton)
		return false, rolledBack && coroNativeForeignIngressRepublishV1(slot)
	}
	if _, executionOK := coroTargetReleaseManagedExecutionIfHeldV1(driver); !executionOK {
		coroRuntimeAbort("foreign ingress execution lease release failed")
	}
	state.mutex.Lock()
	if state.lifecycle != coroNativeForeignIngressActiveV1 ||
		slot.phase != coroNativeForeignIngressClaimingV1 ||
		slot.ownerSlot != 0 || slot.parentSlot != 0 ||
		slot.route != uint32(domain.handle.Route) {
		state.mutex.Unlock()
		rolledBack := coroNativeMReleaseUnclaimedForeignIngressV1(childSlot) &&
			owner.handoff.RequestReturn(baton) ==
				coro.ExecutionDomainHandoffReturnUnclaimed &&
			owner.handoff.Complete(baton)
		state.mutex.Lock()
		if slot.phase != coroNativeForeignIngressClaimingV1 ||
			slot.ownerSlot != 0 || slot.parentSlot != 0 ||
			slot.route != uint32(domain.handle.Route) {
			state.mutex.Unlock()
			return false, false
		}
		slot.phase = coroNativeForeignIngressRejectedV1
		state.cond.Broadcast()
		state.mutex.Unlock()
		return false, rolledBack
	}
	slot.ownerSlot = childSlot
	slot.parentSlot = parentSlot
	slot.phase = coroNativeForeignIngressGrantedV1
	state.cond.Broadcast()
	for slot.phase != coroNativeForeignIngressReturnedV1 &&
		slot.phase != coroNativeForeignIngressRejectedV1 {
		if state.cond.Wait(&state.mutex) != 0 {
			state.lifecycle = coroNativeForeignIngressFailedV1
			break
		}
	}
	returned := slot.phase == coroNativeForeignIngressReturnedV1
	state.mutex.Unlock()
	if !returned || child == nil ||
		!coroNativeMRecycleForeignIngressV1(childSlot, child, owner) ||
		!owner.handoff.Complete(baton) {
		return false, false
	}
	state.mutex.Lock()
	if slot.phase != coroNativeForeignIngressReturnedV1 ||
		slot.ownerSlot != childSlot || slot.parentSlot != parentSlot ||
		slot.route != uint32(domain.handle.Route) {
		state.mutex.Unlock()
		return false, false
	}
	slot.thread = nil
	slot.ownerSlot = 0
	slot.parentSlot = 0
	slot.route = 0
	slot.phase = coroNativeForeignIngressFreeV1
	state.cond.Broadcast()
	state.mutex.Unlock()
	_, leaveOK := state.ingress.Leave()
	return true, leaveOK
}

func coroNativeForeignIngressFinishRouteV1(
	slot *coroNativeForeignIngressSlotV1,
	driver *coro.ExecutorDriver,
) bool {
	if slot == nil || driver == nil || !coroNativeForeignIngressTLSReadyV1 ||
		coroNativeForeignIngressTLSV1.Get() != slot {
		return false
	}
	owner, domain, ownerSlot, _, ownerOK := coroNativeMCurrentOwnerV1(driver)
	if !ownerOK || domain == nil || ownerSlot == 0 ||
		ownerSlot != slot.ownerSlot ||
		coroNativeMOwnerLifecycleLoadV1(owner) !=
			coroNativeMOwnerForeignIngressActiveV1 {
		return false
	}
	parent, parentOK := coroNativeMOwnerForSlotV1(owner.parentSlot)
	if !parentOK || owner.parentSlot != slot.parentSlot ||
		uint32(domain.handle.Route) != slot.route ||
		!parent.handoff.RequestClaimedReturn(owner.baton) {
		return false
	}
	for {
		returned, returnOK := coroNativeReplacementTryReturnV1(
			ownerSlot,
			owner,
			parent,
			domain,
		)
		if !returnOK {
			return false
		}
		if returned {
			break
		}
		if corofleet.Yield() != 0 {
			return false
		}
	}
	coroNativeForeignIngressTLSV1.Clear()
	state := &coroNativeForeignIngressV1State
	state.mutex.Lock()
	if slot.phase != coroNativeForeignIngressRunningV1 ||
		slot.ownerSlot != ownerSlot {
		state.mutex.Unlock()
		return false
	}
	slot.phase = coroNativeForeignIngressReturnedV1
	state.cond.Broadcast()
	state.mutex.Unlock()
	return true
}

func coroNativeForeignIngressSnapshotV1(
	task *coro.G,
	status coro.CompletionStatus,
) (coro.CompletionSnapshot, bool) {
	snapshot := coro.CompletionSnapshot{Status: status}
	if status != coro.CompletionPanic {
		return snapshot, coro.ValidCompletionSnapshot(snapshot)
	}
	record, ok := coro.LoadPanicRecord(task)
	if !ok || record.Status != coro.ExplicitStatusPanic {
		return coro.CompletionSnapshot{}, false
	}
	snapshot.TypeWord = record.TypeWord
	snapshot.DataWord = record.DataWord
	return snapshot, coro.ValidCompletionSnapshot(snapshot)
}

func coroNativeForeignIngressCommitDestroyV1(
	domain *coroNativeFleetDomainV1,
	result coroRunResultV1,
) (coro.CompletionSnapshot, bool) {
	if domain == nil || result.stop != coroRunDestroyCommitV1 || result.g == nil {
		return coro.CompletionSnapshot{}, false
	}
	next, committed := coro.CommitExecutorRunDomainDestroy(
		domain.driverOwnerV1(),
		result.g,
		result.action,
	)
	if !committed {
		return coro.CompletionSnapshot{}, false
	}
	status, foreign, statusOK := coroForeignIngressTerminalStatus(result.g)
	if !statusOK || !foreign {
		return coro.CompletionSnapshot{}, false
	}
	snapshot, snapshotOK := coroNativeForeignIngressSnapshotV1(result.g, status)
	if !snapshotOK {
		return coro.CompletionSnapshot{}, false
	}
	switch next.Kind {
	case coro.ActionComplete:
		if status == coro.CompletionPanic ||
			!coroReleaseCompletedTask(result.g) {
			return coro.CompletionSnapshot{}, false
		}
	case coro.ActionPanicComplete:
		if status != coro.CompletionPanic {
			return coro.CompletionSnapshot{}, false
		}
	default:
		return coro.CompletionSnapshot{}, false
	}
	return snapshot, true
}

func coroNativeForeignIngressRunV1(
	task *coro.G,
	child unsafe.Pointer,
) coro.CompletionSnapshot {
	slot := coroNativeForeignIngressTLSV1.Get()
	if slot == nil || task == nil || child == nil {
		coroRuntimeAbort("invalid standalone foreign ingress child")
	}
	if slot.route == 0 || slot.route > coroNativeFleetV1State.domainCount {
		coroRuntimeAbort("standalone foreign ingress route invalid")
	}
	domain := &coroNativeFleetV1State.domains[slot.route-1]
	if domain.lifecycle != coroNativeFleetDomainActiveV1 ||
		!domain.handle.Valid() || domain.handle.Route != slot.route {
		coroRuntimeAbort("standalone foreign ingress domain invalid")
	}
	driver := domain.driverOwnerV1()
	owner, resolved, ownerSlot, _, ownerOK := coroNativeMCurrentOwnerV1(driver)
	if !ownerOK || ownerSlot != slot.ownerSlot ||
		resolved != domain || domain.pOwnerV1() == nil ||
		coroNativeMOwnerLifecycleLoadV1(owner) !=
			coroNativeMOwnerForeignIngressActiveV1 ||
		!coro.AdoptRoot(task, child) ||
		!coro.EnqueueForeignIngressRoot(domain.pOwnerV1(), task) {
		coroRuntimeAbort("standalone foreign ingress root publication failed")
	}
	for {
		now, clockOK := coroNativeFleetPhysicalOwnerClockV1()
		if !clockOK {
			coroRuntimeAbort("standalone foreign ingress monotonic clock failed")
		}
		result := coroRunSliceAtV1(
			domain.pOwnerV1(),
			driver,
			now,
			coroNativeFleetRunBudgetV1,
		)
		var snapshot coro.CompletionSnapshot
		switch result.stop {
		case coroRunSliceBudgetV1, coroRunAgainV1:
			continue
		case coroRunExecutionWaitV1:
			if !coroTargetWaitManagedExecutionV1(driver) {
				coroRuntimeAbort("standalone foreign ingress execution wait failed")
			}
			continue
		case coroRunOSThreadSuspendV1:
			if result.g != task ||
				!coroTargetHandleOSThreadSuspendV1(
					domain.pOwnerV1(),
					driver,
					result.g,
					result.action,
				) {
				coroRuntimeAbort("standalone foreign ingress suspension handoff failed")
			}
			continue
		case coroRunForeignIngressCompleteV1:
			var snapshotOK bool
			snapshot, snapshotOK = coroNativeForeignIngressSnapshotV1(
				task,
				result.completion,
			)
			// The task was already retired by the adjacent reducer, so the
			// snapshot helper must not inspect it for a non-panic completion.
			if result.completion == coro.CompletionPanic || !snapshotOK {
				coroRuntimeAbort("standalone foreign ingress completion invalid")
			}
		case coroRunDestroyCommitV1:
			var snapshotOK bool
			snapshot, snapshotOK =
				coroNativeForeignIngressCommitDestroyV1(domain, result)
			if !snapshotOK {
				coroRuntimeAbort("standalone foreign ingress destroy commit failed")
			}
		case coroRunPanicCompleteV1:
			var snapshotOK bool
			snapshot, snapshotOK = coroNativeForeignIngressSnapshotV1(
				result.g,
				result.completion,
			)
			if result.g != task || !snapshotOK {
				coroRuntimeAbort("standalone foreign ingress panic receipt invalid")
			}
		default:
			coroRuntimeAbort("standalone foreign ingress runner stopped unexpectedly")
		}
		if snapshot.Status != coro.CompletionPanic &&
			!coroNativeForeignIngressFinishRouteV1(slot, driver) {
			coroRuntimeAbort("standalone foreign ingress route return failed")
		}
		return snapshot
	}
}
