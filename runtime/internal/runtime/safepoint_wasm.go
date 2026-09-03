//go:build llgo && wasm && llgo_wasm_gc && !(wasip1 && llgo.wasi_threads)

package runtime

import "github.com/xgo-dev/llgo/runtime/internal/pollbudget"

const wasmSafepointQuantum = uint32(1024)

var wasmSafepointBudget = pollbudget.New(wasmSafepointQuantum)

// CooperativeSafepoint gives the single wasm worker a bounded opportunity to
// run host events and another runnable goroutine.
func CooperativeSafepoint() {
	if !wasmSafepointBudget.Poll() {
		return
	}
	cooperativeSafepointSlow()
}

//go:noinline
func cooperativeSafepointSlow() {
	if !wasmSched.started {
		return
	}
	pollWasmEvents()
	if wasmSched.runq.Len() != 0 {
		goschedBackend()
	}
}
