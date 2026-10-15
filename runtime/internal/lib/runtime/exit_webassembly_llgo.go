//go:build !baremetal && !wasip1 && (wasm || tinygo.wasm)

package runtime

import _ "unsafe"

// Core WebAssembly embeddings without a process ABI cannot implement Exit.
// Keep this distinct from WASI Preview 1's proc_exit rather than fabricating
// success or importing a process service into every freestanding module.
//
//go:linkname syscall_Exit syscall.Exit
//go:nosplit
func syscall_Exit(code int) {
	_ = code
	c_debugtrap()
	for {
	}
}
