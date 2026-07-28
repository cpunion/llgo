//go:build !linux && !baremetal && !wasm && !tinygo.wasm

package runtime

import (
	_ "unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
)

//llgo:coro sync
//go:linkname Sigsetjmp C.sigsetjmp
func Sigsetjmp(env *SigjmpBuf, savemask c.Int) c.Int

// Siglongjmp is emitted only as the exact nonlocal-transfer leaf of the
// compiler-owned raw panic/defer control closure.
//
//go:linkname Siglongjmp C.siglongjmp
func Siglongjmp(env *SigjmpBuf, val c.Int)
