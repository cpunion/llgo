//go:build !llgo_coro

package runtime

import "github.com/goplus/llgo/runtime/internal/clite/debug"

// traceTerminalPanic preserves the full Go traceback for the ordinary runtime
// profile. The hook may initialize PCLN metadata, which is safe while this
// profile still owns the native stack synchronously.
func traceTerminalPanic(skip int) {
	if PanicTraceback == nil || !PanicTraceback(skip) {
		debug.PrintStack(skip)
	}
}
