//go:build llgo && wasm && !(wasip1 && llgo.wasi_threads) && !llgo.wasm_workers && !(js && llgo.wasm_resume)

package wasmevent

const LLGoFiles = "_wrap/event_wasm.c"
