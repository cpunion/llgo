//go:build !baremetal && !wasm && !tinygo.wasm

package runtime

import (
	c "github.com/goplus/llgo/runtime/internal/clite"
)

var (
	printFormatPrefixInt  = c.Str("%lld")
	printFormatPrefixUInt = c.Str("%llu")
	printFormatPrefixHex  = c.Str("%llx")
)

// Rethrow rethrows a panic.
func Rethrow(link *Defer) {
	gp := getg()
	if ptr := gp.panic_; ptr != nil {
		if link == nil {
			TracePanic(*(*any)(ptr))
			traceTerminalPanic(2)
			c.Free(ptr)
			c.Exit(2)
		} else {
			c.Siglongjmp(link.Addr, 1)
		}
	} else if gp.goexit {
		// Goexit must run deferred functions before terminating the current
		// goroutine. Reuse the longjmp-based defer unwinding:
		// 1) If we have a defer frame, longjmp to it so it can execute defers.
		// 2) Once we've unwound past the last frame (link==nil), terminate the
		//    current pthread.
		if link != nil {
			c.Siglongjmp(link.Addr, 1)
		}
		if gp.isMain {
			markMainExited()
		}
		leaveCurrentLocalContext()
		exitCurrentM()
	}
}
