//go:build llgo && windows && !nogc && !baremetal

package reflect

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/ffi"
	"github.com/xgo-dev/llgo/runtime/internal/runtime"
)

func makeFuncCallback(nout int) func(*ffi.Signature, unsafe.Pointer, *unsafe.Pointer, unsafe.Pointer) {
	switch nout {
	case 0:
		return bind0ForeignThread
	case 1:
		return bind1ForeignThread
	default:
		return bindnForeignThread
	}
}

func bind0ForeignThread(cif *ffi.Signature, ret unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	registered := runtime.EnterForeignThread()
	defer runtime.ExitForeignThread(registered)
	bind0(cif, ret, args, userdata)
}

func bind1ForeignThread(cif *ffi.Signature, ret unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	registered := runtime.EnterForeignThread()
	defer runtime.ExitForeignThread(registered)
	bind1(cif, ret, args, userdata)
}

func bindnForeignThread(cif *ffi.Signature, ret unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	registered := runtime.EnterForeignThread()
	defer runtime.ExitForeignThread(registered)
	bindn(cif, ret, args, userdata)
}
