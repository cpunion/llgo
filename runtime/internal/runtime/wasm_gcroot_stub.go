//go:build llgo && wasm && !llgo.wasm.gc.linear

package runtime

import "unsafe"

const wasmGCRootEnabled = false

type wasmGCRootContext struct{}

func registerWasmGCRoot(*wasmGCRootContext, bool) {}

func wasmGCRootPointer(*wasmGCRootContext) unsafe.Pointer { return nil }

func setWasmGCRootStack(*wasmGCRootContext, uintptr, uintptr) {}

func setWasmGCRoot(*wasmGCRootContext, unsafe.Pointer) {}

func adoptWasmGCRoot(*wasmGCRootContext) {}

func finishWasmGCRootRebuild() {}

func unregisterWasmGCRoot(*wasmGCRootContext) {}
