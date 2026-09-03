//go:build wasm && llgo_wasm_gc

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/gcroot"
)

// Defer presents defer statements in a function.
type Defer struct {
	Addr   unsafe.Pointer // sigjmpbuf
	Bits   uintptr
	Link   *Defer
	Reth   unsafe.Pointer // native block address or wasm continuation selector
	Rund   unsafe.Pointer // native block address or wasm continuation selector
	Args   unsafe.Pointer // defer func and args links
	gcRoot unsafe.Pointer // compiler root chain at this function's setjmp
}

// SetDeferGCRoot records the chain that longjmp must restore.
func SetDeferGCRoot(frame *Defer) {
	frame.gcRoot = gcroot.CurrentChain()
}
