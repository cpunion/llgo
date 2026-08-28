//go:build llgo && windows && !nogc && !baremetal

package reflect

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/ffi"
	"github.com/xgo-dev/llgo/runtime/internal/runtime"
)

func bindMakeFuncCoro(cif *ffi.Signature, ret unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	registered := runtime.EnterForeignThread()
	bindCoro(cif, ret, args, userdata)
	// Retained registrations belong to the thread's G lifecycle, including
	// runtime.Goexit. Keep cleanup off the defer chain: a defer here would try
	// to unwind across the libffi callback frame, which is not a Go frame.
	runtime.ExitForeignThread(registered)
}
