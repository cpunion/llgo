//go:build !wasm || !llgo.wasm.gc.linear || llgo.wasm.workers || llgo.wasi_threads

package runtime

import "unsafe"

func recordWasmMemProfileAlloc(unsafe.Pointer, uintptr) {}

func wasmMemProfile([]MemProfileRecord, bool) (int, bool) { return 0, true }
