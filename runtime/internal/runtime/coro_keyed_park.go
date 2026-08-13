//go:build (llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal) || wasm || tinygo.wasm || baremetal || llgo_coro_host)) || coro_sema_owner_test || coro_notify_owner_test

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

	catomic "github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
	"github.com/goplus/llgo/runtime/internal/coro"
)

const (
	coroKeyedResumeSuccessV2 uint32 = iota + 1
	coroKeyedResumeTaskAbortV2
	coroKeyedResumeShutdownV2
)

// raiseScanLimit publishes the shortest prefix which can contain an Active
// slot. The prefix is monotonic: retirement never needs to coordinate a
// downward move with concurrent registration or claim. This keeps the common
// one-waiter semaphore path O(1), while a registry which has actually reached
// N simultaneous slots retains the original bounded O(N) FIFO selection.
func (registry *coroKeyedRegistryV2) raiseScanLimit(needed uint32) bool {
	if registry == nil || needed == 0 || needed > uint32(len(registry.slots)) {
		return false
	}
	for {
		limit := coroKeyedAtomicLoadUint32(&registry.scanLimit)
		if limit > uint32(len(registry.slots)) {
			return false
		}
		if limit >= needed {
			return true
		}
		if coroKeyedAtomicCompareAndSwapUint32(&registry.scanLimit, limit, needed) {
			return true
		}
	}
}

func (registry *coroKeyedRegistryV2) register(
	kind coroKeyedParkKindV2,
	key uintptr,
	logical uint32,
	operation coro.OperationID,
) (coroKeyedRegistryHandleV2, bool) {
	if registry == nil || (kind != coroKeyedParkSemaphoreV2 && kind != coroKeyedParkNotifyV2) ||
		key == 0 || !operation.Valid() || operation.Source() != coro.OperationSourceManual {
		return coroKeyedRegistryHandleV2{}, false
	}
	for index := range registry.slots {
		slot := &registry.slots[index]
		control := coroKeyedAtomicLoadUint32(&slot.control)
		generation := coroKeyedRegistryControlGenerationV2(control)
		if coroKeyedRegistryControlStateV2(control) != coroKeyedRegistryFreeV2 ||
			generation == coroKeyedRegistryMaxGenerationV2 {
			continue
		}
		generation++
		registering := coroKeyedRegistryControlV2(generation, coroKeyedRegistryRegisteringV2)
		if !coroKeyedAtomicCompareAndSwapUint32(&slot.control, control, registering) {
			continue
		}
		if !registry.raiseScanLimit(uint32(index) + 1) {
			_ = coroKeyedAtomicCompareAndSwapUint32(&slot.control, registering, control)
			return coroKeyedRegistryHandleV2{}, false
		}
		sequence := coroKeyedAtomicAddUint32(&registry.sequence, 1)
		coroKeyedAtomicStoreUint32((*uint32)(unsafe.Pointer(&slot.kind)), uint32(kind))
		coroKeyedAtomicStoreUint32(&slot.logical, logical)
		coroKeyedAtomicStoreUintptr(&slot.key, key)
		coroKeyedAtomicStoreUint32(&slot.sequence, sequence)
		coroKeyedAtomicStoreUint32(&slot.operation.SourceSlot, operation.SourceSlot)
		coroKeyedAtomicStoreUint32(&slot.operation.Generation, operation.Generation)
		coroKeyedAtomicStoreUint32(
			&slot.control, coroKeyedRegistryControlV2(generation, coroKeyedRegistryActiveV2),
		)
		return coroKeyedRegistryHandleV2{Slot: uint32(index) + 1, Generation: generation}, true
	}
	return coroKeyedRegistryHandleV2{}, false
}

type coroKeyedRegistryClaimV2 uint8

const (
	coroKeyedRegistryClaimInvalidV2 coroKeyedRegistryClaimV2 = iota
	coroKeyedRegistryClaimOwnerV2
	coroKeyedRegistryClaimAlreadyV2
)

func (registry *coroKeyedRegistryV2) claimExact(
	handle coroKeyedRegistryHandleV2,
	operation coro.OperationID,
) coroKeyedRegistryClaimV2 {
	slot, ok := coroKeyedRegistrySlotV2For(registry, handle)
	if !ok {
		return coroKeyedRegistryClaimInvalidV2
	}
	for {
		control := coroKeyedAtomicLoadUint32(&slot.control)
		if coroKeyedRegistryControlGenerationV2(control) != handle.Generation {
			return coroKeyedRegistryClaimInvalidV2
		}
		loaded, stable := coroKeyedRegistryLoadOperationV2(slot, control)
		if !stable {
			continue
		}
		if loaded != operation {
			return coroKeyedRegistryClaimInvalidV2
		}
		switch coroKeyedRegistryControlStateV2(control) {
		case coroKeyedRegistryActiveV2:
			posting := coroKeyedRegistryControlV2(handle.Generation, coroKeyedRegistryPostingV2)
			if coroKeyedAtomicCompareAndSwapUint32(&slot.control, control, posting) {
				return coroKeyedRegistryClaimOwnerV2
			}
		case coroKeyedRegistryPostingV2, coroKeyedRegistryDeliveredV2:
			return coroKeyedRegistryClaimAlreadyV2
		default:
			return coroKeyedRegistryClaimInvalidV2
		}
	}
}

type coroKeyedRegistrySnapshotV2 struct {
	control   uint32
	kind      coroKeyedParkKindV2
	logical   uint32
	key       uintptr
	sequence  uint32
	operation coro.OperationID
}

func coroKeyedRegistryLoadSnapshotV2(
	slot *coroKeyedRegistrySlotV2,
	control uint32,
) (coroKeyedRegistrySnapshotV2, bool) {
	if slot == nil || coroKeyedAtomicLoadUint32(&slot.control) != control {
		return coroKeyedRegistrySnapshotV2{}, false
	}
	snapshot := coroKeyedRegistrySnapshotV2{
		control:  control,
		kind:     coroKeyedParkKindV2(coroKeyedAtomicLoadUint32((*uint32)(unsafe.Pointer(&slot.kind)))),
		logical:  coroKeyedAtomicLoadUint32(&slot.logical),
		key:      coroKeyedAtomicLoadUintptr(&slot.key),
		sequence: coroKeyedAtomicLoadUint32(&slot.sequence),
		operation: coro.OperationID{
			SourceSlot: coroKeyedAtomicLoadUint32(&slot.operation.SourceSlot),
			Generation: coroKeyedAtomicLoadUint32(&slot.operation.Generation),
		},
	}
	return snapshot, coroKeyedAtomicLoadUint32(&slot.control) == control
}

func coroKeyedRegistrySequenceLessV2(a, b uint32) bool {
	return int32(a-b) < 0
}

func (registry *coroKeyedRegistryV2) claimOne(
	kind coroKeyedParkKindV2,
	key uintptr,
	logical uint32,
	exact bool,
) (coroKeyedRegistryHandleV2, coro.OperationID, bool) {
	if registry == nil || (kind != coroKeyedParkSemaphoreV2 && kind != coroKeyedParkNotifyV2) || key == 0 {
		return coroKeyedRegistryHandleV2{}, coro.OperationID{}, false
	}
	for {
		limit := coroKeyedAtomicLoadUint32(&registry.scanLimit)
		if limit > uint32(len(registry.slots)) {
			return coroKeyedRegistryHandleV2{}, coro.OperationID{}, false
		}
		selected := -1
		var candidate coroKeyedRegistrySnapshotV2
		for index := uint32(0); index < limit; index++ {
			slot := &registry.slots[index]
			control := coroKeyedAtomicLoadUint32(&slot.control)
			if coroKeyedRegistryControlStateV2(control) != coroKeyedRegistryActiveV2 {
				continue
			}
			snapshot, stable := coroKeyedRegistryLoadSnapshotV2(slot, control)
			if !stable || snapshot.kind != kind || snapshot.key != key ||
				exact && snapshot.logical != logical {
				continue
			}
			if selected < 0 || coroKeyedRegistrySequenceLessV2(snapshot.sequence, candidate.sequence) {
				selected, candidate = int(index), snapshot
			}
		}
		if selected < 0 {
			return coroKeyedRegistryHandleV2{}, coro.OperationID{}, false
		}
		generation := coroKeyedRegistryControlGenerationV2(candidate.control)
		posting := coroKeyedRegistryControlV2(generation, coroKeyedRegistryPostingV2)
		if coroKeyedAtomicCompareAndSwapUint32(
			&registry.slots[selected].control, candidate.control, posting,
		) {
			return coroKeyedRegistryHandleV2{
				Slot: uint32(selected) + 1, Generation: generation,
			}, candidate.operation, true
		}
	}
}

type coroKeyedRegistryPublishV2 uint8

const (
	coroKeyedRegistryPublishInvalidV2 coroKeyedRegistryPublishV2 = iota
	coroKeyedRegistryPublishReadyV2
	coroKeyedRegistryPublishRetiredV2
)

func coroKeyedRegistryPublicationRetiredV2(
	control uint32,
	handle coroKeyedRegistryHandleV2,
) bool {
	generation := coroKeyedRegistryControlGenerationV2(control)
	if generation < handle.Generation {
		return false
	}
	return generation > handle.Generation ||
		generation == handle.Generation &&
			coroKeyedRegistryControlStateV2(control) == coroKeyedRegistryFreeV2
}

// finishPost publishes the private registry terminal state before the Manual
// source fact. A concurrent cancellation may already have retired this exact
// generation; in that case the producer owns no remaining frame-visible work
// and must skip its source Post.
func (registry *coroKeyedRegistryV2) finishPost(
	handle coroKeyedRegistryHandleV2,
	operation coro.OperationID,
) coroKeyedRegistryPublishV2 {
	slot, ok := coroKeyedRegistrySlotV2For(registry, handle)
	if !ok {
		return coroKeyedRegistryPublishInvalidV2
	}
	for {
		control := coroKeyedAtomicLoadUint32(&slot.control)
		if coroKeyedRegistryPublicationRetiredV2(control, handle) {
			return coroKeyedRegistryPublishRetiredV2
		}
		if coroKeyedRegistryControlGenerationV2(control) != handle.Generation ||
			coroKeyedRegistryControlStateV2(control) != coroKeyedRegistryPostingV2 {
			return coroKeyedRegistryPublishInvalidV2
		}
		loaded, stable := coroKeyedRegistryLoadOperationV2(slot, control)
		if !stable {
			continue
		}
		if loaded != operation {
			return coroKeyedRegistryPublishInvalidV2
		}
		delivered := coroKeyedRegistryControlV2(handle.Generation, coroKeyedRegistryDeliveredV2)
		if coroKeyedAtomicCompareAndSwapUint32(&slot.control, control, delivered) {
			return coroKeyedRegistryPublishReadyV2
		}
	}
}

func (registry *coroKeyedRegistryV2) publicationRetired(
	handle coroKeyedRegistryHandleV2,
) bool {
	slot, ok := coroKeyedRegistrySlotV2For(registry, handle)
	return ok && coroKeyedRegistryPublicationRetiredV2(
		coroKeyedAtomicLoadUint32(&slot.control), handle,
	)
}

func coroKeyedTicketLessV2(a, b uint32) bool {
	return int32(a-b) < 0
}

func coroKeyedPostClaimedV2(handle coroKeyedRegistryHandleV2, operation coro.OperationID) bool {
	switch coroProgramKeyedRegistryV2State.finishPost(handle, operation) {
	case coroKeyedRegistryPublishRetiredV2:
		return true
	case coroKeyedRegistryPublishReadyV2:
		current, driver, route := coroCurrentTaskV1()
		if current != nil && driver != nil && route == operation.Route() {
			completion, cleanup, local, localOK := coro.BeginOwnerLocalManualCompletionCurrent(
				current,
				driver,
				operation,
			)
			if !localOK {
				return coroProgramKeyedRegistryV2State.publicationRetired(handle)
			}
			if local {
				return coroMaterializePrivateResumeCleanupStepV1(cleanup) &&
					coro.FinishOwnerLocalManualCompletionCurrent(&completion)
			}
		}
		if coroTargetPostKeyedOperationV2(operation) {
			return true
		}
		// Cancellation may retire the registry and close/recycle the source
		// between private publication and the target Post. Exact generation
		// retirement makes that failed Post an ordinary lost wake: semaphore
		// count or notify ticket remains the durable repair fact.
		return coroProgramKeyedRegistryV2State.publicationRetired(handle)
	default:
		return false
	}
}

func coroKeyedPostExactV2(handle coroKeyedRegistryHandleV2, operation coro.OperationID) bool {
	switch coroProgramKeyedRegistryV2State.claimExact(handle, operation) {
	case coroKeyedRegistryClaimOwnerV2:
		return coroKeyedPostClaimedV2(handle, operation)
	case coroKeyedRegistryClaimAlreadyV2:
		return true
	default:
		return false
	}
}

func coroKeyedPostOneV2(kind coroKeyedParkKindV2, key uintptr, logical uint32, exact bool) (bool, bool) {
	handle, operation, found := coroProgramKeyedRegistryV2State.claimOne(kind, key, logical, exact)
	if !found {
		return false, true
	}
	return true, coroKeyedPostClaimedV2(handle, operation)
}

func coroKeyedAbortV2(message string) {
	coroRuntimeAbort(message)
	for {
	}
}

func coroPrepareKeyedStateV2(state *CoroKeyedParkV2, kind coroKeyedParkKindV2, key uintptr, logical uint32) bool {
	if state == nil || *state != (CoroKeyedParkV2{}) || key == 0 ||
		(kind != coroKeyedParkSemaphoreV2 && kind != coroKeyedParkNotifyV2) {
		return false
	}
	state.kind = kind
	state.key = key
	state.logical = logical
	state.magic = coroKeyedParkPreparedMagicV2
	return true
}

//export __llgo_coro_keyed_park_v2
func __llgo_coro_keyed_park_v2(g, handle, header, storage unsafe.Pointer) {
	state := (*CoroKeyedParkV2)(storage)
	if g == nil || handle == nil || header == nil || !validPreparedCoroKeyedParkV2(state) {
		coroKeyedAbortV2("invalid coroutine keyed Park V2 ABI")
		return
	}
	task := (*coro.G)(g)
	driver, wantExecutor, wantRoute, reservation, current, reserved :=
		coro.CurrentExecutorManualReservation(task)
	if current && !reserved {
		if !ensureCoroManualOperationCapacityV1(driver, task, coroRuntimeManualCapacityV1) {
			coroKeyedAbortV2("cannot resolve coroutine keyed Park V2 owner")
			return
		}
		var retryDriver *coro.ExecutorDriver
		retryDriver, wantExecutor, wantRoute, reservation, current, reserved =
			coro.CurrentExecutorManualReservation(task)
		if retryDriver != driver {
			current = false
		}
	}
	if !current || !reserved {
		coroKeyedAbortV2("cannot resolve coroutine keyed Park V2 owner")
		return
	}
	ticket, operation, executor, prepared := coro.PrepareCurrentExecutorManualCleanupParkReserved(
		driver,
		task,
		handle,
		(*coro.HeaderV1)(header),
		&state.wait,
		reservation,
		&state.packet,
		&state.cleanup,
		unsafe.Pointer(state),
		&state.operation,
		1,
		1,
	)
	if !prepared || executor != wantExecutor || operation.Route() != wantRoute {
		coroKeyedAbortV2("cannot prepare coroutine keyed Park V2 source")
		return
	}
	state.ticket = ticket
	state.magic = coroKeyedParkActiveMagicV2
	// Publish the scalar key only after the complete P-neutral cleanup
	// descriptor is bound. A concurrent release can post the Manual fact
	// immediately, but the current owner cannot suspend this G with a partially
	// initialized resume contract.
	registry, registered := coroProgramKeyedRegistryV2State.register(
		state.kind, state.key, state.logical, operation,
	)
	if !registered {
		coroKeyedAbortV2("cannot publish coroutine keyed Park V2 registry")
		return
	}
	state.registry = registry

	ready := state.kind == coroKeyedParkSemaphoreV2 && catomic.Load((*uint32)(unsafe.Pointer(state.key))) != 0 ||
		state.kind == coroKeyedParkNotifyV2 && coroKeyedTicketLessV2(
			state.logical, catomic.Load((*uint32)(unsafe.Pointer(state.key))),
		)
	if ready && !coroKeyedPostExactV2(registry, operation) {
		coroKeyedAbortV2("cannot repair coroutine keyed Park V2 wake")
	}
}

//export __llgo_coro_keyed_resume_v2
func __llgo_coro_keyed_resume_v2(g, storage unsafe.Pointer) uint32 {
	state := (*CoroKeyedParkV2)(storage)
	if g == nil || !validMaterializedCoroKeyedParkV2(state) {
		coroKeyedAbortV2("invalid coroutine keyed Park V2 resume ABI")
		return 0
	}
	task := (*coro.G)(g)
	outcome, caseID, cancel, result, small, taken := coro.TakeResumePacket(
		task,
		state.ticket,
		&state.packet,
		nil,
	)
	if !taken || result != coro.ResumeResultNone || small != coro.ResumeSmallInvalid {
		coroKeyedAbortV2("invalid coroutine keyed Park V2 resume packet")
		return 0
	}
	status := uint32(0)
	switch {
	case outcome == coro.ParkOutcomeCompleted && caseID == 1 && cancel == coro.TaskCancelNone:
		status = coroKeyedResumeSuccessV2
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelAbort:
		status = coroKeyedResumeTaskAbortV2
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelShutdown:
		status = coroKeyedResumeShutdownV2
	default:
		coroKeyedAbortV2("unsupported coroutine keyed Park V2 run decision")
		return 0
	}
	*state = CoroKeyedParkV2{}
	return status
}
