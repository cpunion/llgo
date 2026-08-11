//go:build !llgo_coro

package reflect

import "unsafe"

func functionCodePointer(code unsafe.Pointer) unsafe.Pointer {
	return code
}
