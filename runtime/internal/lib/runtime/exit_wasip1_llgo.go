//go:build wasip1 && !baremetal

package runtime

import _ "unsafe"

// Use the standard package's exact WASI import signature. A Go linkname to a
// bodyless import does not carry the import's physical ABI proof. Process
// termination has no completion, cancellation or borrowed data; it is not an
// asynchronous host operation.
//
//go:wasmimport wasi_snapshot_preview1 proc_exit
//llgo:coro noblock
func syscallProcExit(code int32)

//go:linkname syscall_Exit syscall.Exit
//go:nosplit
func syscall_Exit(code int) {
	syscallProcExit(int32(code))
	// A conforming proc_exit never returns. Fail closed if an embedding violates
	// that contract; do not resume the caller or claim successful termination.
	c_debugtrap()
	for {
	}
}
