//go:build !wasm

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

func makeWasmFunc(ft *abi.FuncType, fn func([]Value) []Value, recoverTo unsafe.Pointer) Value {
	return Value{}
}
