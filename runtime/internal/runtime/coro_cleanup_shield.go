package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/coro"
)

//export __llgo_coro_cleanup_shield_begin_v1
func __llgo_coro_cleanup_shield_begin_v1(g, owner unsafe.Pointer) unsafe.Pointer {
	receipt, valid := coro.BeginCleanupShield((*coro.G)(g), owner)
	if !valid {
		coroRuntimeAbort("invalid coroutine cleanup shield begin")
	}
	return receipt
}

//export __llgo_coro_cleanup_shield_end_v1
func __llgo_coro_cleanup_shield_end_v1(g, owner, receipt unsafe.Pointer) uint32 {
	kind, valid := coro.EndCleanupShield((*coro.G)(g), owner, receipt)
	if !valid {
		coroRuntimeAbort("invalid coroutine cleanup shield end")
	}
	return uint32(kind)
}
