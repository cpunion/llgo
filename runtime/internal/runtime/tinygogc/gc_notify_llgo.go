//go:build llgo && llgo_coro && (baremetal || (!nogc && (wasm || tinygo.wasm) && llgo_wasm_gc))

package tinygogc

import _ "unsafe"

// The sole collector completion entry only atomically publishes a retained
// epoch. It does not enter Go scheduling, take locks, allocate, or suspend.
//
//go:linkname gcCompleted github.com/xgo-dev/llgo/runtime/internal/runtime.coroGCCompletedV1
func gcCompleted()
