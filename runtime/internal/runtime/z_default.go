//go:build !baremetal

package runtime

import (
	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/debug"
	"github.com/goplus/llgo/runtime/internal/clite/pthread"
)

var (
	printFormatPrefixInt  = c.Str("%lld")
	printFormatPrefixUInt = c.Str("%llu")
	printFormatPrefixHex  = c.Str("%llx")
)

// Rethrow rethrows a panic.
func Rethrow(link *Defer) {
	if ptr := excepKey.Get(); ptr != nil {
		if link == nil {
			TracePanic(*(*any)(ptr))
			// This is the terminal fallback of the legacy longjmp unwinder.
			// A callback may be coroutine-capable and therefore cannot run after
			// the last managed defer frame has already been abandoned. Keep the
			// emergency trace bounded and synchronous; coroutine-aware panic
			// cleanup owns richer Go traceback formatting before this point.
			debug.PrintStack(2)
			c.Free(ptr)
			c.Exit(2)
		} else {
			c.Siglongjmp(link.Addr, 1)
		}
	} else if ptr := goexitKey.Get(); ptr != nil {
		// Goexit must run deferred functions before terminating the current
		// goroutine. Reuse the longjmp-based defer unwinding:
		// 1) If we have a defer frame, longjmp to it so it can execute defers.
		// 2) Once we've unwound past the last frame (link==nil), terminate the
		//    current pthread.
		if link != nil {
			c.Siglongjmp(link.Addr, 1)
		}
		if pthread.Equal(mainThread, pthread.Self()) != 0 {
			fatal("no goroutines (main called runtime.Goexit) - deadlock!")
			c.Exit(2)
		}
		pthread.Exit(nil)
	}
}
