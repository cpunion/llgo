//go:build llgo && wasm && llgo.wasm_resume && !llgo_wasm_gc && (js || wasip1) && !(wasip1 && llgo.wasi_threads)

package runtime

import "unsafe"

func captureWasmResumeGCRoot() unsafe.Pointer {
	return nil
}

func restoreWasmResumeGCRoot(unsafe.Pointer) {}
