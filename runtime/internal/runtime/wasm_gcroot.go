//go:build llgo && wasm && llgo_wasm_gc

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/gcroot"
)

const wasmGCRootEnabled = true

type wasmGCRootContext = gcroot.Context

func registerWasmGCRoot(ctx *wasmGCRootContext, active bool) {
	if active {
		gcroot.RegisterActive(ctx)
	} else {
		gcroot.Register(ctx)
	}
}

func wasmGCRootPointer(ctx *wasmGCRootContext) unsafe.Pointer {
	return unsafe.Pointer(ctx)
}

func adoptWasmGCRoot(ctx *wasmGCRootContext) {
	gcroot.AdoptCurrent(ctx)
}

func finishWasmGCRootRebuild() {
	gcroot.FinishRebuild()
}

func unregisterWasmGCRoot(ctx *wasmGCRootContext) {
	gcroot.Unregister(ctx)
}
