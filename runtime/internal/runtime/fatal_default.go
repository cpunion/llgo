//go:build !llgo || !wasm || (!js && !wasip1) || (wasip1 && llgo.wasm.wasi.threads)

package runtime

func fatal(s string) {
	print("fatal error: ", s, "\n")
}
