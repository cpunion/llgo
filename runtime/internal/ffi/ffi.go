package ffi

import (
	"unsafe"

	"github.com/goplus/llgo/runtime/internal/clite/ffi"
)

type Type = ffi.Type

type Signature = ffi.Cif

type Error int

func (s Error) Error() string {
	switch s {
	case ffi.OK:
		return "ok"
	case ffi.BAD_TYPEDEF:
		return "bad type def"
	case ffi.BAD_ABI:
		return "bad ABI"
	case ffi.BAD_ARGTYPE:
		return "bad argument type"
	}
	return "invalid status"
}

func NewSignature(ret *Type, args ...*Type) (*Signature, error) {
	panic("llgo: reflect call requires managed coroutine dispatch")
}

func NewSignatureVar(ret *Type, fixed int, args ...*Type) (*Signature, error) {
	panic("llgo: variadic reflect call requires managed coroutine dispatch")
}

func Call(cif *Signature, fn unsafe.Pointer, ret unsafe.Pointer, args ...unsafe.Pointer) {
	// A managed LLGo function value carries a coroutine dispatch descriptor,
	// not a directly callable code address. Calling ffi_call here would execute
	// the descriptor as code and, for a coroutine primary, would also omit the
	// current G, result slot, and child-await transaction. Keep this boundary
	// fail-closed until the compiler-owned reflect-call lowering can select the
	// descriptor entry, create the child handle through libffi, and feed it into
	// the ordinary scheduler-owned child await path.
	panic("llgo: reflect call requires managed coroutine dispatch")
}

type Closure struct {
	ptr unsafe.Pointer
	Fn  unsafe.Pointer
}

func NewClosure() *Closure {
	panic("llgo: reflect.MakeFunc requires managed coroutine dispatch")
}

func (c *Closure) Free() {
	if c != nil && c.ptr != nil {
		panic("llgo: reflect.MakeFunc closure release requires managed coroutine dispatch")
	}
}

func (c *Closure) Bind(cif *Signature, fn ffi.ClosureFunc, userdata unsafe.Pointer) error {
	panic("llgo: reflect.MakeFunc closure binding requires managed coroutine dispatch")
}

func Index(args *unsafe.Pointer, i uintptr) unsafe.Pointer {
	return ffi.Index(args, i)
}
