//go:build llgo && wasip1 && wasm && llgo.wasm_resume && !llgo.wasi_threads

package runtime

func initWasmResumeHost() {}

func stopWasmResumeHost() {}

func waitWasmResumeRunq() (*g, bool) {
	return waitWasmRunq(), false
}
