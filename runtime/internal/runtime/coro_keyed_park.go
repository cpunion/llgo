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
	registry.mutex.Lock()
	if registry.sequence == ^uint64(0) {
		registry.mutex.Unlock()
		return coroKeyedRegistryHandleV2{}, false
	}
	for index := range registry.slots {
		slot := &registry.slots[index]
		if !coroKeyedRegistryReusableSlotV2(slot) || slot.generation == ^uint32(0) {
			continue
		}
		generation := slot.generation + 1
		registry.sequence++
		if generation == 0 || registry.sequence == 0 {
			registry.mutex.Unlock()
			return coroKeyedRegistryHandleV2{}, false
		}
		slot.generation = generation
		slot.kind = kind
		slot.logical = logical
		slot.key = key
		slot.sequence = registry.sequence
		slot.operation = operation
		slot.state = coroKeyedRegistryActiveV2
		registry.mutex.Unlock()
		return coroKeyedRegistryHandleV2{Slot: uint32(index) + 1, Generation: generation}, true
	}
	registry.mutex.Unlock()
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
	registry.mutex.Lock()
	slot, ok := coroKeyedRegistrySlotV2For(registry, handle)
	if !ok || slot.generation != handle.Generation || slot.operation != operation {
		registry.mutex.Unlock()
		return coroKeyedRegistryClaimInvalidV2
	}
	var result coroKeyedRegistryClaimV2
	switch slot.state {
	case coroKeyedRegistryActiveV2:
		slot.state = coroKeyedRegistryPostingV2
		result = coroKeyedRegistryClaimOwnerV2
	case coroKeyedRegistryPostingV2, coroKeyedRegistryDeliveredV2:
		result = coroKeyedRegistryClaimAlreadyV2
	default:
		result = coroKeyedRegistryClaimInvalidV2
	}
	registry.mutex.Unlock()
	return result
}

func (registry *coroKeyedRegistryV2) claimOne(
	kind coroKeyedParkKindV2,
	key uintptr,
	logical uint32,
	exact bool,
) (coroKeyedRegistryHandleV2, coro.OperationID, bool) {
	registry.mutex.Lock()
	selected := -1
	var sequence uint64
	for index := range registry.slots {
		slot := &registry.slots[index]
		if slot.state != coroKeyedRegistryActiveV2 || slot.kind != kind || slot.key != key ||
			exact && slot.logical != logical {
			continue
		}
		if selected < 0 || slot.sequence < sequence {
			selected, sequence = index, slot.sequence
		}
	}
	if selected < 0 {
		registry.mutex.Unlock()
		return coroKeyedRegistryHandleV2{}, coro.OperationID{}, false
	}
	slot := &registry.slots[selected]
	slot.state = coroKeyedRegistryPostingV2
	handle := coroKeyedRegistryHandleV2{Slot: uint32(selected) + 1, Generation: slot.generation}
	operation := slot.operation
	registry.mutex.Unlock()
	return handle, operation, true
}

type coroKeyedRegistryPublishV2 uint8

const (
	coroKeyedRegistryPublishInvalidV2 coroKeyedRegistryPublishV2 = iota
	coroKeyedRegistryPublishReadyV2
	coroKeyedRegistryPublishRetiredV2
)

func coroKeyedRegistryPublicationRetiredV2(
	slot *coroKeyedRegistrySlotV2,
	handle coroKeyedRegistryHandleV2,
) bool {
	if slot == nil || slot.generation < handle.Generation {
		return false
	}
	return slot.generation > handle.Generation ||
		slot.generation == handle.Generation && coroKeyedRegistryReusableSlotV2(slot)
}

// finishPost publishes the private registry terminal state before the Manual
// source fact. A concurrent cancellation may already have retired this exact
// generation; in that case the producer owns no remaining frame-visible work
// and must skip its source Post.
func (registry *coroKeyedRegistryV2) finishPost(
	handle coroKeyedRegistryHandleV2,
	operation coro.OperationID,
) coroKeyedRegistryPublishV2 {
	registry.mutex.Lock()
	slot, ok := coroKeyedRegistrySlotV2For(registry, handle)
	if !ok {
		registry.mutex.Unlock()
		return coroKeyedRegistryPublishInvalidV2
	}
	if slot.generation == handle.Generation && slot.operation == operation &&
		slot.state == coroKeyedRegistryPostingV2 {
		slot.state = coroKeyedRegistryDeliveredV2
		registry.mutex.Unlock()
		return coroKeyedRegistryPublishReadyV2
	}
	retired := coroKeyedRegistryPublicationRetiredV2(slot, handle)
	registry.mutex.Unlock()
	if retired {
		return coroKeyedRegistryPublishRetiredV2
	}
	return coroKeyedRegistryPublishInvalidV2
}

func (registry *coroKeyedRegistryV2) publicationRetired(
	handle coroKeyedRegistryHandleV2,
) bool {
	registry.mutex.Lock()
	slot, ok := coroKeyedRegistrySlotV2For(registry, handle)
	retired := ok && coroKeyedRegistryPublicationRetiredV2(slot, handle)
	registry.mutex.Unlock()
	return retired
}

func coroKeyedTicketLessV2(a, b uint32) bool {
	return int32(a-b) < 0
}

func coroKeyedPostClaimedV2(handle coroKeyedRegistryHandleV2, operation coro.OperationID) bool {
	switch coroProgramKeyedRegistryV2State.finishPost(handle, operation) {
	case coroKeyedRegistryPublishRetiredV2:
		return true
	case coroKeyedRegistryPublishReadyV2:
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
	driver, wantExecutor, wantRoute, ok := coro.CurrentExecutorManualDriver(task)
	if !ok {
		coroKeyedAbortV2("cannot resolve coroutine keyed Park V2 owner")
		return
	}
	ticket, operation, executor, prepared := coro.PrepareCurrentExecutorManualPark(
		driver, task, handle, (*coro.HeaderV1)(header), &state.wait, 1, 1,
	)
	if !prepared || executor != wantExecutor || operation.Route() != wantRoute {
		coroKeyedAbortV2("cannot prepare coroutine keyed Park V2 source")
		return
	}
	state.ticket = ticket
	state.operation = operation
	state.magic = coroKeyedParkActiveMagicV2
	if !coro.BindWaitSetResumeCleanup(
		&state.wait,
		&state.packet,
		&state.cleanup,
		coro.ResumeCleanupBinding{
			Kind:         coro.ResumeCleanupKeyedPark,
			Context:      unsafe.Pointer(state),
			Entries:      unsafe.Pointer(&state.operation),
			Count:        1,
			RuntimeCount: 1,
			Stride:       unsafe.Sizeof(coro.OperationID{}),
		},
	) {
		coroKeyedAbortV2("cannot bind coroutine keyed Park V2 cleanup")
		return
	}
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
