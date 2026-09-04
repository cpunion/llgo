//go:build llgo.wasm.freestanding

package setjmp

// Freestanding WebAssembly has no libc setjmp ABI. Programs which require
// panic/recover therefore fail to link against this profile, but the shared
// runtime defer layout still needs a concrete buffer type for programs whose
// unreachable unwind path is removed by the linker.
const (
	SigjmpBufSize = 1
	JmpBufSize    = 1
)
