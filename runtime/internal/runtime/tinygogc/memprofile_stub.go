//go:build !wasm || !llgo.wasm.gc.linear || llgo.wasm.workers || llgo.wasi_threads

package tinygogc

func memProfileFree(uintptr) {}
