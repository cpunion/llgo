//go:build (llgo && llgo_coro && llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal && !coro_runtime_adapter_test) || coro_sema_owner_test || coro_notify_owner_test

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

type coroKeyedParkKindV2 uint32

const (
	coroKeyedParkInvalidV2 coroKeyedParkKindV2 = iota
	coroKeyedParkSemaphoreV2
	coroKeyedParkNotifyV2
)

const (
	coroKeyedParkPreparedMagicV2 uint32 = 0x4b505250 // KPRP
	coroKeyedParkActiveMagicV2   uint32 = 0x4b504152 // KPAR
)

const (
	coroKeyedResumeSuccessV2 uint32 = iota + 1
	coroKeyedResumeTaskAbortV2
	coroKeyedResumeShutdownV2
)

// Two native domains each configure 1024 manual-operation slots. The registry
// stores only scalar equality keys and POD identities; it never retains G, P,
// ParkState, an LLVM handle, or any other Go pointer.
const coroKeyedRegistryCapacityV2 = 2 * 1024

type coroKeyedRegistryHandleV2 struct {
	Slot       uint32
	Generation uint32
}

type coroKeyedRegistrySlotStateV2 uint32

const (
	coroKeyedRegistryFreeV2 coroKeyedRegistrySlotStateV2 = iota
	coroKeyedRegistryActiveV2
	coroKeyedRegistryPostingV2
	coroKeyedRegistryDeliveredV2
)

type coroKeyedRegistrySlotV2 struct {
	generation uint32
	state      coroKeyedRegistrySlotStateV2
	kind       coroKeyedParkKindV2
	logical    uint32
	key        uintptr
	sequence   uint64
	operation  coro.OperationID
}

type coroKeyedRegistryV2 struct {
	mutex    channelMutex
	sequence uint64
	slots    [coroKeyedRegistryCapacityV2]coroKeyedRegistrySlotV2
}

var coroProgramKeyedRegistryV2State coroKeyedRegistryV2

// CoroKeyedParkV2 is compiler-spilled state for semaphore and notify waits.
// Its source OperationRecord lives in the route-local ManualOperationSource;
// this frame state owns only the WaitSet queue links and scalar identities.
type CoroKeyedParkV2 struct {
	wait      coro.WaitSetRecord
	ticket    coro.ParkTicket
	operation coro.OperationID
	executor  coro.ExecutorHandle
	registry  coroKeyedRegistryHandleV2
	key       uintptr
	logical   uint32
	kind      coroKeyedParkKindV2
	magic     uint32
}

// The standard-library wrappers reserve [16]uintptr of opaque frame storage.
// Keep the runtime view allocation-free and target-pointer-width neutral.
var (
	_ [16*unsafe.Sizeof(uintptr(0)) - unsafe.Sizeof(CoroKeyedParkV2{})]byte
	_ [unsafe.Alignof(uintptr(0)) - unsafe.Alignof(CoroKeyedParkV2{})]byte
)

func validPreparedCoroKeyedParkV2(state *CoroKeyedParkV2) bool {
	return state != nil && state.magic == coroKeyedParkPreparedMagicV2 &&
		(state.kind == coroKeyedParkSemaphoreV2 || state.kind == coroKeyedParkNotifyV2) &&
		state.key != 0 && state.wait == (coro.WaitSetRecord{}) && state.ticket == (coro.ParkTicket{}) &&
		state.operation == (coro.OperationID{}) && state.executor == (coro.ExecutorHandle{}) &&
		state.registry == (coroKeyedRegistryHandleV2{})
}

func validActiveCoroKeyedParkV2(state *CoroKeyedParkV2) bool {
	return state != nil && state.magic == coroKeyedParkActiveMagicV2 &&
		(state.kind == coroKeyedParkSemaphoreV2 || state.kind == coroKeyedParkNotifyV2) &&
		state.key != 0 && state.ticket != (coro.ParkTicket{}) && state.operation.Valid() &&
		state.operation.Source() == coro.OperationSourceManual &&
		state.executor.Slot != 0 && state.executor.Generation != 0 &&
		state.registry.Slot != 0 && state.registry.Generation != 0
}

func coroKeyedRegistryReusableSlotV2(slot *coroKeyedRegistrySlotV2) bool {
	return slot != nil && slot.state == coroKeyedRegistryFreeV2 && slot.kind == coroKeyedParkInvalidV2 &&
		slot.logical == 0 && slot.key == 0 && slot.sequence == 0 && slot.operation == (coro.OperationID{})
}

func coroKeyedRegistrySlotV2For(
	registry *coroKeyedRegistryV2,
	handle coroKeyedRegistryHandleV2,
) (*coroKeyedRegistrySlotV2, bool) {
	if registry == nil || handle.Slot == 0 || handle.Generation == 0 ||
		handle.Slot > uint32(len(registry.slots)) {
		return nil, false
	}
	return &registry.slots[handle.Slot-1], true
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

func (registry *coroKeyedRegistryV2) finishPost(
	handle coroKeyedRegistryHandleV2,
	operation coro.OperationID,
) bool {
	registry.mutex.Lock()
	slot, ok := coroKeyedRegistrySlotV2For(registry, handle)
	if !ok || slot.generation != handle.Generation || slot.operation != operation ||
		slot.state != coroKeyedRegistryPostingV2 {
		registry.mutex.Unlock()
		return false
	}
	slot.state = coroKeyedRegistryDeliveredV2
	registry.mutex.Unlock()
	return true
}

func coroKeyedTicketLessV2(a, b uint32) bool {
	return int32(a-b) < 0
}

func (registry *coroKeyedRegistryV2) retire(
	handle coroKeyedRegistryHandleV2,
	operation coro.OperationID,
) bool {
	for {
		registry.mutex.Lock()
		slot, ok := coroKeyedRegistrySlotV2For(registry, handle)
		if !ok || slot.generation != handle.Generation || slot.operation != operation {
			registry.mutex.Unlock()
			return false
		}
		if slot.state == coroKeyedRegistryPostingV2 {
			registry.mutex.Unlock()
			continue
		}
		if slot.state != coroKeyedRegistryActiveV2 && slot.state != coroKeyedRegistryDeliveredV2 {
			registry.mutex.Unlock()
			return false
		}
		generation := slot.generation
		*slot = coroKeyedRegistrySlotV2{generation: generation}
		registry.mutex.Unlock()
		return true
	}
}

func coroKeyedPostClaimedV2(handle coroKeyedRegistryHandleV2, operation coro.OperationID) bool {
	if !coroTargetPostKeyedOperationV2(operation) {
		return false
	}
	return coroProgramKeyedRegistryV2State.finishPost(handle, operation)
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
	registry, registered := coroProgramKeyedRegistryV2State.register(
		state.kind, state.key, state.logical, operation,
	)
	if !registered {
		coroKeyedAbortV2("cannot publish coroutine keyed Park V2 registry")
		return
	}
	state.ticket = ticket
	state.operation = operation
	state.executor = executor
	state.registry = registry
	state.magic = coroKeyedParkActiveMagicV2

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
	if g == nil || !validActiveCoroKeyedParkV2(state) {
		coroKeyedAbortV2("invalid coroutine keyed Park V2 resume ABI")
		return 0
	}
	task := (*coro.G)(g)
	outcome, caseID, lease, cancel, taken := coro.TakeRunDecision(task, state.ticket)
	if !taken || !coroProgramKeyedRegistryV2State.retire(state.registry, state.operation) {
		coroKeyedAbortV2("invalid coroutine keyed Park V2 decision or registry")
		return 0
	}
	driver, executor, route, ok := coro.CurrentExecutorManualDriver(task)
	if !ok || executor != state.executor || route != state.operation.Route() {
		coroKeyedAbortV2("coroutine keyed Park V2 resumed on the wrong source")
		return 0
	}
	status := uint32(0)
	discard := outcome == coro.ParkOutcomeCanceled
	switch {
	case outcome == coro.ParkOutcomeCompleted && caseID == 1 && lease.Valid() && cancel == coro.TaskCancelNone:
		status, discard = coroKeyedResumeSuccessV2, false
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelAbort:
		status = coroKeyedResumeTaskAbortV2
	case outcome == coro.ParkOutcomeCanceled && caseID == 0 && cancel == coro.TaskCancelShutdown:
		status = coroKeyedResumeShutdownV2
	default:
		coroKeyedAbortV2("unsupported coroutine keyed Park V2 run decision")
		return 0
	}
	if !coro.FinishCurrentExecutorManualPark(
		driver, task, state.executor, state.operation, lease, discard,
	) {
		coroKeyedAbortV2("cannot retire coroutine keyed Park V2 source")
		return 0
	}
	*state = CoroKeyedParkV2{}
	return status
}
