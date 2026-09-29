//go:build llgo && wasm && wasip1

package reflect

import "unsafe"

const useWasmReflectBridges = true

func makeFallbackFunc(ft *funcType, fn func([]Value) []Value, recoverTo unsafe.Pointer) Value {
	panic("reflect: libffi is unavailable on WASI")
}
