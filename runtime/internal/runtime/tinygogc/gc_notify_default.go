//go:build !llgo_coro && (baremetal || (!nogc && (wasm || tinygo.wasm) && llgo_wasm_gc))

package tinygogc

func gcCompleted() {}
