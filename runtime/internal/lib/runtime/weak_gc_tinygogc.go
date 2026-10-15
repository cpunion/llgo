//go:build !nogc && (baremetal || ((wasm || tinygo.wasm) && llgo_wasm_gc))

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/runtime/tinygogc"
)

func llgoWeakRegister(p unsafe.Pointer, live *uintptr) bool {
	return tinygogc.RegisterWeak(p, live)
}

func llgoWeakStrong(key uintptr, live *uintptr) unsafe.Pointer {
	return tinygogc.StrongFromWeak(key, live)
}

func llgoWeakHeapBase(p unsafe.Pointer) unsafe.Pointer {
	return tinygogc.WeakHeapBase(p)
}
