//go:build !nogc && !baremetal && !llgo_wasm_gc

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/clite/bdwgc"
)

func llgoWeakRegister(p unsafe.Pointer, live *uintptr) bool {
	return bdwgc.RegisterWeak(p, live) != 0
}

func llgoWeakStrong(key uintptr, live *uintptr) unsafe.Pointer {
	return bdwgc.StrongFromWeak(key, live)
}

func llgoWeakHeapBase(p unsafe.Pointer) unsafe.Pointer {
	return bdwgc.WeakHeapBase(p)
}
