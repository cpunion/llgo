//go:build (wasm || tinygo.wasm) && !baremetal && !wasip2 && !wasm_unknown

package runtime

import c "github.com/goplus/llgo/runtime/internal/clite"

var (
	printFormatPrefixInt  = c.Str("%lld")
	printFormatPrefixUInt = c.Str("%llu")
	printFormatPrefixHex  = c.Str("%llx")
)

// Rethrow is the terminal legacy-stack fallback for WASM. Managed Go panic,
// defer, and recover use the compiler-owned explicit-status coroutine path and
// never enter this function. A raw/plain boundary cannot unwind through a
// suspended LLVM coroutine frame, and WASM has no process siglongjmp ABI, so
// any panic that escapes such a boundary is reported and terminates instead of
// manufacturing an invalid jump target.
func Rethrow(_ *Defer) {
	if ptr := getg().panic_; ptr != nil {
		TracePanic(*(*any)(ptr))
		traceTerminalPanic(2)
		c.Free(ptr)
	}
	c.Exit(2)
	for {
	}
}
