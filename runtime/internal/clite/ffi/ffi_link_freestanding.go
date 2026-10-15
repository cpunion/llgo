//go:build wasm || tinygo.wasm || baremetal

package ffi

import (
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
)

// DynamicCallsSupported lets managed dispatch discard its native libffi stack
// cut before lowering. Retaining reflect.Value's method metadata must not add
// an unimplemented host import to otherwise valid freestanding programs.
const DynamicCallsSupported = false

// Freestanding targets do not have a target libffi. Keep the
// package link manifest empty so ordinary programs that merely retain reflect
// metadata do not acquire a host libffi dependency. Dynamic ABI operations
// fail explicitly until a target-owned dynamic ABI implementation
// is selected for the concrete signature.
func PrepCif(cif *Cif, abi c.Uint, nargs c.Uint, rtype *Type, atype **Type) c.Uint {
	return BAD_ABI
}

func PrepCifVar(cif *Cif, abi c.Uint, nfixedargs c.Uint, ntotalargs c.Uint, rtype *Type, atype **Type) c.Uint {
	return BAD_ABI
}

func Call(cif *Cif, fn unsafe.Pointer, rvalue unsafe.Pointer, avalue *unsafe.Pointer) {
	panic("libffi is unsupported on this freestanding target")
}

func CallWithEnv(cif *Cif, fn unsafe.Pointer, rvalue unsafe.Pointer, avalue *unsafe.Pointer, env unsafe.Pointer) {
	panic("libffi is unsupported on this freestanding target")
}

func ClosureAlloc(code *unsafe.Pointer) unsafe.Pointer {
	if code != nil {
		*code = nil
	}
	return nil
}

func ClosureFree(unsafe.Pointer) {}

func PreClosureLoc(closure unsafe.Pointer, cif *Cif, fn ClosureFunc, userdata unsafe.Pointer, codeloc unsafe.Pointer) c.Uint {
	return BAD_ABI
}
