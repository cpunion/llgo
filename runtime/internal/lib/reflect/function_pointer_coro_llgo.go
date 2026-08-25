//go:build llgo_coro

package reflect

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/ffi"
)

func functionCodePointer(descriptor unsafe.Pointer) unsafe.Pointer {
	return ffi.CodeEntry(descriptor)
}
