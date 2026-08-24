//go:build !wasm && !tinygo.wasm

package syscall

import (
	_ "unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

//go:linkname c_syscall C.syscall
func c_syscall(number c.Long, __llgo_va_list ...any) c.Long
