//go:build llgo && (!wasm || (wasip1 && llgo.wasi_threads))

package runtime

func activateLocalBlocks(*LocalContext)   {}
func publishLocalBlocks(*LocalContext)    {}
func deactivateLocalBlocks(*LocalContext) {}
