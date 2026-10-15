//go:build coro_sema_owner_test && !llgo

package runtime

import (
	"testing"
	"unsafe"
)

func TestCoroKeyedSemaphorePrepareDurabilityV3(t *testing.T) {
	for _, durable := range []bool{false, true} {
		var state CoroKeyedParkV2
		var key uint32
		__llgo_coro_sema_prepare_or_abort_v3(unsafe.Pointer(&state), unsafe.Pointer(&key), durable)
		var logical uint32
		if durable {
			logical = 1
		}
		if !validPreparedCoroKeyedParkV2(&state) || state.kind != coroKeyedParkSemaphoreV2 ||
			state.key != uintptr(unsafe.Pointer(&key)) || state.logical != logical {
			t.Fatalf("durable=%t prepared wrong keyed state: %+v", durable, state)
		}
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("durable=%t allowed live state reprepare", durable)
				}
			}()
			__llgo_coro_sema_prepare_or_abort_v3(unsafe.Pointer(&state), unsafe.Pointer(&key), !durable)
		}()
	}
	for _, missing := range []string{"storage", "key"} {
		t.Run("nil-"+missing, func(t *testing.T) {
			var state CoroKeyedParkV2
			var key uint32
			storage, addr := unsafe.Pointer(&state), unsafe.Pointer(&key)
			if missing == "storage" {
				storage = nil
			} else {
				addr = nil
			}
			defer func() {
				if recover() == nil {
					t.Fatal("accepted incomplete semaphore preparation")
				}
			}()
			__llgo_coro_sema_prepare_or_abort_v3(storage, addr, true)
		})
	}
}
