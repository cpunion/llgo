//go:build wasm && llgo.wasm.gc.linear && !llgo.wasm.workers && !llgo.wasi_threads

package tinygogc

import _ "unsafe"

// Both explicit frees and sweep notify the profiler before address reuse.
// The implementation must not allocate, acquire gcMutex, or schedule work.
//
//go:linkname memProfileFree github.com/xgo-dev/llgo/runtime/internal/runtime.memProfileFree
func memProfileFree(address uintptr)
