//go:build (coro_sema_owner_test || coro_notify_owner_test) && !llgo

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
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/coro"
)

// The keyed registry is exercised as a named production-source island. These
// definitions provide only the runtime globals referenced by the production
// post adapter; the registry tests never start an executor.
var (
	coroProgramManualSourceV2State     coro.ManualOperationSource
	coroProgramExecutorRegistryV1State coro.ExecutorRegistry
	coroProgramExecutorHandleV1State   coro.ExecutorHandle
)

// This named registry source island does not compile the typed hchan adapter.
func coroMaterializeChannelResumeCleanupStepV1(coro.ResumeCleanupStep) bool { return false }

func coroRuntimeAbort(message string) {
	panic(message)
}

func keyedRegistryOperation(t *testing.T, slot, generation uint32) coro.OperationID {
	t.Helper()
	id, ok := coro.MakeOperationID(coro.OperationSourceManual, slot, generation)
	if !ok {
		t.Fatalf("make manual operation %d/%d", slot, generation)
	}
	return id
}

func TestCoroKeyedRegistryFIFOAndExactLogicalSelectionV2(t *testing.T) {
	registry := new(coroKeyedRegistryV2)
	firstID := keyedRegistryOperation(t, 1, 1)
	secondID := keyedRegistryOperation(t, 2, 1)
	otherID := keyedRegistryOperation(t, 3, 1)
	first, ok1 := registry.register(coroKeyedParkSemaphoreV2, 0x10, 0, firstID)
	second, ok2 := registry.register(coroKeyedParkSemaphoreV2, 0x10, 0, secondID)
	other, ok3 := registry.register(coroKeyedParkNotifyV2, 0x20, 7, otherID)
	if !ok1 || !ok2 || !ok3 {
		t.Fatal("register keyed FIFO fixtures")
	}

	claimed, operation, ok := registry.claimOne(coroKeyedParkSemaphoreV2, 0x10, 0, false)
	if !ok || claimed != first || operation != firstID ||
		registry.finishPost(claimed, operation) != coroKeyedRegistryPublishReadyV2 {
		t.Fatalf("first FIFO claim = %+v/%+v/%t", claimed, operation, ok)
	}
	claimed, operation, ok = registry.claimOne(coroKeyedParkSemaphoreV2, 0x10, 0, false)
	if !ok || claimed != second || operation != secondID ||
		registry.finishPost(claimed, operation) != coroKeyedRegistryPublishReadyV2 {
		t.Fatalf("second FIFO claim = %+v/%+v/%t", claimed, operation, ok)
	}
	if claimed, operation, ok = registry.claimOne(coroKeyedParkNotifyV2, 0x20, 6, true); ok ||
		claimed != (coroKeyedRegistryHandleV2{}) || operation != (coro.OperationID{}) {
		t.Fatalf("wrong logical ticket claimed = %+v/%+v/%t", claimed, operation, ok)
	}
	claimed, operation, ok = registry.claimOne(coroKeyedParkNotifyV2, 0x20, 7, true)
	if !ok || claimed != other || operation != otherID ||
		registry.finishPost(claimed, operation) != coroKeyedRegistryPublishReadyV2 {
		t.Fatalf("exact logical claim = %+v/%+v/%t", claimed, operation, ok)
	}
	for handle, id := range map[coroKeyedRegistryHandleV2]coro.OperationID{
		first: firstID, second: secondID, other: otherID,
	} {
		if !registry.retire(handle, id) {
			t.Fatalf("retire delivered keyed slot %+v", handle)
		}
	}
}

func TestCoroKeyedRegistryConcurrentClaimIsSingleOwnerV2(t *testing.T) {
	registry := new(coroKeyedRegistryV2)
	id := keyedRegistryOperation(t, 1, 1)
	handle, ok := registry.register(coroKeyedParkSemaphoreV2, 0x30, 0, id)
	if !ok {
		t.Fatal("register concurrent keyed claim")
	}

	const claimers = 32
	var owners atomic.Uint32
	var already atomic.Uint32
	var invalid atomic.Uint32
	var group sync.WaitGroup
	group.Add(claimers)
	for range claimers {
		go func() {
			defer group.Done()
			switch registry.claimExact(handle, id) {
			case coroKeyedRegistryClaimOwnerV2:
				owners.Add(1)
			case coroKeyedRegistryClaimAlreadyV2:
				already.Add(1)
			default:
				invalid.Add(1)
			}
		}()
	}
	group.Wait()
	if owners.Load() != 1 || already.Load() != claimers-1 || invalid.Load() != 0 {
		t.Fatalf("concurrent claims owner/already/invalid = %d/%d/%d", owners.Load(), already.Load(), invalid.Load())
	}
	if registry.finishPost(handle, id) != coroKeyedRegistryPublishReadyV2 ||
		!registry.retire(handle, id) {
		t.Fatal("finish and retire concurrent keyed claim")
	}
}

func TestCoroKeyedRegistryConcurrentFIFOClaimsRemainLockFreeV2(t *testing.T) {
	const contenders = 600
	registry := new(coroKeyedRegistryV2)
	operations := make([]coro.OperationID, contenders)
	for index := range operations {
		operations[index] = keyedRegistryOperation(t, uint32(index+1), 1)
		if _, ok := registry.register(coroKeyedParkSemaphoreV2, 0x35, 0, operations[index]); !ok {
			t.Fatalf("register concurrent FIFO contender %d", index)
		}
	}

	var claimed [contenders]atomic.Uint32
	var failures atomic.Uint32
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(contenders)
	for range contenders {
		go func() {
			defer group.Done()
			<-start
			handle, operation, ok := registry.claimOne(coroKeyedParkSemaphoreV2, 0x35, 0, false)
			if !ok || handle.Slot == 0 || handle.Slot > contenders {
				failures.Add(1)
				return
			}
			if claimed[handle.Slot-1].Add(1) != 1 ||
				registry.finishPost(handle, operation) != coroKeyedRegistryPublishReadyV2 ||
				!registry.retire(handle, operation) {
				failures.Add(1)
			}
		}()
	}
	close(start)
	group.Wait()
	if failures.Load() != 0 {
		t.Fatalf("concurrent FIFO claim failures = %d", failures.Load())
	}
	for index := range claimed {
		if claimed[index].Load() != 1 {
			t.Fatalf("registry slot %d claim count = %d, want 1", index+1, claimed[index].Load())
		}
	}
}

func TestCoroKeyedRegistrySequenceWrapPreservesFIFOAndGenerationBoundV2(t *testing.T) {
	registry := new(coroKeyedRegistryV2)
	coroKeyedAtomicStoreUint32(
		&registry.slots[0].control,
		coroKeyedRegistryControlV2(coroKeyedRegistryMaxGenerationV2, coroKeyedRegistryFreeV2),
	)
	coroKeyedAtomicStoreUint32(&registry.sequence, ^uint32(0)-1)

	operations := []coro.OperationID{
		keyedRegistryOperation(t, 1, 1),
		keyedRegistryOperation(t, 2, 1),
		keyedRegistryOperation(t, 3, 1),
	}
	handles := make([]coroKeyedRegistryHandleV2, len(operations))
	for index, operation := range operations {
		var ok bool
		handles[index], ok = registry.register(coroKeyedParkSemaphoreV2, 0x36, 0, operation)
		if !ok {
			t.Fatalf("register sequence-wrap contender %d", index)
		}
		if handles[index].Slot == 1 {
			t.Fatal("registry reused an exhausted generation slot")
		}
	}
	for index, want := range handles {
		handle, operation, ok := registry.claimOne(coroKeyedParkSemaphoreV2, 0x36, 0, false)
		if !ok || handle != want || operation != operations[index] {
			t.Fatalf("sequence-wrap claim %d = %+v/%+v/%t, want %+v/%+v", index, handle, operation, ok, want, operations[index])
		}
		if registry.finishPost(handle, operation) != coroKeyedRegistryPublishReadyV2 ||
			!registry.retire(handle, operation) {
			t.Fatalf("finish sequence-wrap contender %d", index)
		}
	}
}

func TestCoroKeyedRegistryPostingRetireIsBoundedAndProducerNeutralV2(t *testing.T) {
	registry := new(coroKeyedRegistryV2)
	id := keyedRegistryOperation(t, 1, 1)
	handle, ok := registry.register(coroKeyedParkSemaphoreV2, 0x31, 0, id)
	if !ok || registry.claimExact(handle, id) != coroKeyedRegistryClaimOwnerV2 {
		t.Fatal("claim keyed posting race")
	}
	if !registry.retire(handle, id) {
		t.Fatal("retire keyed posting generation")
	}
	if publish := registry.finishPost(handle, id); publish != coroKeyedRegistryPublishRetiredV2 {
		t.Fatalf("finish retired keyed post = %d", publish)
	}
	if !registry.publicationRetired(handle) {
		t.Fatal("retired keyed publication lost its terminal generation fact")
	}
	replacementID := keyedRegistryOperation(t, 1, 2)
	replacement, ok := registry.register(coroKeyedParkNotifyV2, 0x32, 9, replacementID)
	if !ok || replacement.Slot != handle.Slot || replacement.Generation <= handle.Generation ||
		!registry.publicationRetired(handle) {
		t.Fatalf("replacement after posting retire = %+v, old=%+v", replacement, handle)
	}
	if !registry.retire(replacement, replacementID) {
		t.Fatal("retire keyed posting-race replacement")
	}
}

func TestCoroKeyedRegistryCancelCapacityAndGenerationReuseV2(t *testing.T) {
	registry := new(coroKeyedRegistryV2)
	handles := make([]coroKeyedRegistryHandleV2, coroKeyedRegistryCapacityV2)
	operations := make([]coro.OperationID, coroKeyedRegistryCapacityV2)
	for index := range handles {
		operations[index] = keyedRegistryOperation(t, uint32(index+1), 1)
		var ok bool
		handles[index], ok = registry.register(coroKeyedParkSemaphoreV2, 0x40, 0, operations[index])
		if !ok {
			t.Fatalf("register keyed capacity slot %d", index)
		}
	}
	if handle, ok := registry.register(coroKeyedParkSemaphoreV2, 0x40, 0, operations[0]); ok ||
		handle != (coroKeyedRegistryHandleV2{}) {
		t.Fatalf("over-capacity keyed registration = %+v/%t", handle, ok)
	}

	stale := handles[0]
	if !registry.retire(stale, operations[0]) {
		t.Fatal("cancel active keyed registration")
	}
	replacementID := keyedRegistryOperation(t, 1, 2)
	replacement, ok := registry.register(coroKeyedParkNotifyV2, 0x50, 9, replacementID)
	if !ok || replacement.Slot != stale.Slot || replacement.Generation == stale.Generation {
		t.Fatalf("generation reuse = %+v after %+v, ok=%t", replacement, stale, ok)
	}
	if got := registry.claimExact(stale, operations[0]); got != coroKeyedRegistryClaimInvalidV2 {
		t.Fatalf("stale generation claim = %d, want invalid", got)
	}
	if !registry.retire(replacement, replacementID) {
		t.Fatal("retire replacement keyed registration")
	}
	for index := 1; index < len(handles); index++ {
		if !registry.retire(handles[index], operations[index]) {
			t.Fatalf("retire capacity slot %d", index)
		}
	}
	if registry.sequence != coroKeyedRegistryCapacityV2+1 {
		t.Fatalf("keyed registry sequence = %d, want %d", registry.sequence, coroKeyedRegistryCapacityV2+1)
	}
}

func TestCoroKeyedPreparedStateAndTicketOrderingV2(t *testing.T) {
	var state CoroKeyedParkV2
	if !coroPrepareKeyedStateV2(&state, coroKeyedParkSemaphoreV2, 0x60, 0) ||
		!validPreparedCoroKeyedParkV2(&state) {
		t.Fatalf("prepared keyed state = %+v", state)
	}
	if coroPrepareKeyedStateV2(&state, coroKeyedParkSemaphoreV2, 0x60, 0) {
		t.Fatal("reprepared live keyed state")
	}
	if !coroKeyedTicketLessV2(^uint32(0), 0) || coroKeyedTicketLessV2(0, ^uint32(0)) {
		t.Fatal("keyed ticket wrap ordering is not serial-number safe")
	}
	wantSize := uintptr(196)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		wantSize = 256
	}
	if got := unsafe.Sizeof(state); got != wantSize {
		t.Fatalf("CoroKeyedParkV2 size = %d, want %d", got, wantSize)
	}
}
