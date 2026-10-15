//go:build llgo && llgo_coro && !coro_runtime_adapter_test && ((llgo_coro_native_pipe && llgo_coro_native_timer && (darwin || linux) && !baremetal) || wasm || tinygo.wasm || baremetal || llgo_coro_host)

package runtime

import "github.com/xgo-dev/llgo/runtime/internal/coro"

//export __llgo_coro_domain_post_v1
func __llgo_coro_domain_post_v1(sourceSlot, generation uint32) bool {
	id := coro.OperationID{SourceSlot: sourceSlot, Generation: generation}
	if !id.Valid() || id.Source() != coro.OperationSourceManual {
		return false
	}
	registry := &coroProgramKeyedRegistryV2State
	limit := coroKeyedAtomicLoadUint32(&registry.scanLimit)
	if limit > uint32(len(registry.slots)) {
		return false
	}
	for index := uint32(0); index < limit; index++ {
		slot := &registry.slots[index]
		control := coroKeyedAtomicLoadUint32(&slot.control)
		if coroKeyedRegistryControlStateV2(control) != coroKeyedRegistryActiveV2 {
			continue
		}
		operation, stable := coroKeyedRegistryLoadOperationV2(slot, control)
		if stable && operation == id {
			return coroKeyedPostExactV2(coroKeyedRegistryHandleV2{
				Slot: index + 1, Generation: coroKeyedRegistryControlGenerationV2(control),
			}, operation)
		}
	}
	return false
}
