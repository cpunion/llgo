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
	if ptr := excepKey.Get(); ptr != nil {
		if link == nil {
			TracePanic(*(*any)(ptr))
			traceTerminalPanic(2)
			c.Free(ptr)
			c.Exit(2)
		} else {
			c.Siglongjmp(link.Addr, 1)
		}
	}
}
