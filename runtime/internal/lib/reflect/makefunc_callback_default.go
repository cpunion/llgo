//go:build !llgo || !windows || nogc || baremetal

package reflect

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/ffi"
)

// bindMakeFuncCoro is the exact static raw-C callback entry retained by
// MakeFunc. Keeping platform selection in source files avoids turning this
// boundary into a dynamic function-value dispatch.
func bindMakeFuncCoro(cif *ffi.Signature, ret unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	bindCoro(cif, ret, args, userdata)
}
