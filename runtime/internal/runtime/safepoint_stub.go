//go:build !llgo || !wasm || !llgo_wasm_gc || (wasip1 && llgo.wasi_threads)

package runtime

// CooperativeSafepoint is inactive on runtimes without single-worker wasm
// cooperative scheduling.
func CooperativeSafepoint() {}
