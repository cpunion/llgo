//go:build windows && !baremetal && !wasm

package runtime

import (
	_ "unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

// Windows has no signal-mask variant of setjmp. These are logical runtime
// declarations: the SSA builder lowers x86 targets to non-unwinding CRT
// entries and ARM64 to LLGo's ABI-only context entry. Go defers are unwound
// by LLGo itself, so the Windows virtual unwinder must not run here.
//
//go:linkname Sigsetjmp C.setjmp
func Sigsetjmp(env *SigjmpBuf, savemask c.Int) c.Int

//go:linkname Siglongjmp C.longjmp
func Siglongjmp(env *SigjmpBuf, val c.Int)
