//go:build !llgo || ((!wasm || !wasip1) && (!goexperiment.simd || (!amd64 && !arm64)))

package reflect

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/abi"
)

const useWasmReflectBridges = false

func callWasmBridge(ft *abi.FuncType, fn, env unsafe.Pointer, method bool, prefix []unsafe.Pointer, in []Value) []Value {
	return nil
}

func resetWasmFuncBridge(ft *abi.FuncType) {}

func copyWasmFuncBridge(dst, src *abi.FuncType) {}

func hasTypedCallBridge(ft *abi.FuncType) bool { return false }

func makeProviderFunc(ft *funcType, fn func([]Value) []Value, recoverTo unsafe.Pointer) Value {
	return makeFallbackFunc(ft, fn, recoverTo)
}
