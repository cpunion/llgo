//go:build wasm && llgo_wasm_gc

package runtime

import c "github.com/xgo-dev/llgo/runtime/internal/clite"

var (
	printFormatPrefixInt  = c.Str("%lld")
	printFormatPrefixUInt = c.Str("%llu")
	printFormatPrefixHex  = c.Str("%llx")
)
