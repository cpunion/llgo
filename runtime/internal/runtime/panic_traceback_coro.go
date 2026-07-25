//go:build llgo_coro && !wasip2 && !wasm_unknown

package runtime

import "github.com/goplus/llgo/runtime/internal/clite/debug"

// traceTerminalPanic is the bounded synchronous fallback after the stackless
// panic path has abandoned its last managed frame. A callback here could need
// to suspend or initialize PCLN state; richer coroutine tracebacks must be
// produced before this terminal boundary.
func traceTerminalPanic(skip int) {
	debug.PrintStack(skip)
}
