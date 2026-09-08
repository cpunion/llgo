//go:build wasm && llgo.wasm.gc.linear && !llgo.wasm.workers && !llgo.wasi_threads

package runtime

import llrt "github.com/xgo-dev/llgo/runtime/internal/runtime"

func init() {
	llrt.InitWasmMemProfile(&MemProfileRate)
}
