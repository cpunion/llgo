//go:build llgo && wasm && llgo.wasm_resume && llgo_wasm_gc && (js || wasip1) && !(wasip1 && llgo.wasi_threads)

package runtime

import "unsafe"

// These are direct aliases because a wrapper around RestoreChain would add its
// own compiler root frame and leave that frame installed after it returns.
//
//go:linkname captureWasmResumeGCRoot github.com/goplus/llgo/runtime/internal/gcroot.CurrentChain
func captureWasmResumeGCRoot() unsafe.Pointer

//go:linkname restoreWasmResumeGCRoot github.com/goplus/llgo/runtime/internal/gcroot.RestoreChain
func restoreWasmResumeGCRoot(unsafe.Pointer)
