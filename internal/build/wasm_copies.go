package build

import (
	"github.com/xgo-dev/llgo/internal/abi"
	"github.com/xgo-dev/llvm"
)

func lowerWasmAggregateCopies(goarch string, td llvm.TargetData, mod llvm.Module, roots bool) int {
	if goarch != "wasm" {
		return 0
	}
	return abi.LowerWasmAggregateCopies(td, mod, roots)
}
