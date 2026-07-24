package ffi

import (
	"unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/bitcast"
	"github.com/goplus/llgo/runtime/internal/clite/ffi"
)

type Type = ffi.Type

type Signature = ffi.Cif

type Error int

// signatureHolder owns every pointer retained by ffi_cif. libffi stores the
// argument-type array instead of copying it, so a bare stack Cif is only safe
// for an immediate call.
type signatureHolder struct {
	Signature
	args []*Type
	ret  *Type
}

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
	cif := &signatureHolder{
		args: append([]*Type(nil), args...),
		ret:  ret,
	}
	var atype **Type
	if len(cif.args) > 0 {
		atype = &cif.args[0]
	}
	status := ffi.PrepCif(&cif.Signature, ffi.DefaultAbi, c.Uint(len(cif.args)), ret, atype)
	if status == ffi.OK {
		return &cif.Signature, nil
	}
	return nil, Error(status)
}

func NewSignatureVar(ret *Type, fixed int, args ...*Type) (*Signature, error) {
	cif := &signatureHolder{
		args: append([]*Type(nil), args...),
		ret:  ret,
	}
	var atype **Type
	if len(cif.args) > 0 {
		atype = &cif.args[0]
	}
	status := ffi.PrepCifVar(&cif.Signature, ffi.DefaultAbi, c.Uint(fixed), c.Uint(len(cif.args)), ret, atype)
	if status == ffi.OK {
		return &cif.Signature, nil
	}
	return nil, Error(status)
}

// CallRaw performs one bounded libffi call with an already-built argv array.
// The callee must return before this native call returns; a coroutine entry is
// valid because its ramp only creates and initially suspends the child frame.
func CallRaw(cif *Signature, fn, ret unsafe.Pointer, args *unsafe.Pointer) {
	ffi.Call(cif, fn, ret, args)
}

const (
	dispatchVersionV1 uint32 = 1

	// These are compiler/runtime ABI bits. Keep the values explicit: placing
	// them after dispatchVersionV1 in this block must not shift their positions.
	dispatchHasPlain     uint32 = 1 << 0
	dispatchHasCoro      uint32 = 1 << 1
	dispatchNoCapture    uint32 = 1 << 2
	dispatchRuntimeTyped uint32 = 1 << 3

	dispatchCapabilityMask = dispatchHasPlain | dispatchHasCoro
	dispatchKnownFlags     = dispatchCapabilityMask | dispatchNoCapture | dispatchRuntimeTyped
)

const dispatchRuntimeTypeMagicV1 uint64 = 0x4c4c474f52545931 // "LLGORTY1"

// dispatchDescriptorV1 is the runtime view of the compiler-owned universal
// function descriptor. Keep this layout synchronized with
// ssa.Program.coroDispatchDescriptorType.
type dispatchDescriptorV1 struct {
	Version     uint32
	Flags       uint32
	HashLo      uint64
	HashHi      uint64
	PlainEntry  unsafe.Pointer
	CoroEntry   unsafe.Pointer
	ResultSize  uintptr
	ResultAlign uintptr
}

// NewRuntimeCoroDescriptor creates the trusted dynamic descriptor used by
// reflect.MakeFunc. HashLo carries the exact runtime function-type pointer and
// HashHi carries a versioned tag; compiler-created descriptors continue to
// carry their structural ABI digest in those words.
func NewRuntimeCoroDescriptor(
	runtimeType, coroEntry unsafe.Pointer, resultSize, resultAlign uintptr,
) unsafe.Pointer {
	if runtimeType == nil || coroEntry == nil || resultAlign == 0 {
		panic("llgo: invalid runtime coroutine descriptor")
	}
	return unsafe.Pointer(&dispatchDescriptorV1{
		Version:     dispatchVersionV1,
		Flags:       dispatchHasCoro | dispatchRuntimeTyped,
		HashLo:      uint64(bitcast.FromPointer(runtimeType)),
		HashHi:      dispatchRuntimeTypeMagicV1,
		CoroEntry:   coroEntry,
		ResultSize:  resultSize,
		ResultAlign: resultAlign,
	})
}

// CoroEntry returns the already-typed coroutine entry of one trusted managed
// descriptor. It is used while reflect.MakeFunc prepares its fixed-signature
// invoker bridge; ordinary calls still go through full descriptor validation.
func CoroEntry(descriptor unsafe.Pointer) unsafe.Pointer {
	if descriptor == nil {
		panic("llgo: nil managed function descriptor")
	}
	d := (*dispatchDescriptorV1)(descriptor)
	flags := d.Flags
	if d.Version != dispatchVersionV1 ||
		flags&^dispatchKnownFlags != 0 ||
		flags&dispatchHasCoro == 0 ||
		d.CoroEntry == nil ||
		(flags&dispatchHasPlain != 0) != (d.PlainEntry != nil) {
		panic("llgo: invalid managed function descriptor")
	}
	return d.CoroEntry
}

// coroFFICall is a compiler-owned stack cut. Its source ABI contains only the
// values known to reflection; lowering stores the current G into gslot, calls
// stock ffi_call to create the child through descriptor.CoroEntry, and awaits
// the returned handle after ffi_call has left the native stack.
//
//go:linkname coroFFICall llgo.coroFFICall
func coroFFICall(cif *Signature, fn, out unsafe.Pointer, gslot *unsafe.Pointer, args *unsafe.Pointer)

// CallLLGo invokes one managed {descriptor, environment} function value.
// plainCIF describes (env,args...)->results. coroCIF describes
// (g,out,env,args...)->handle. Both are ordinary target-C-ABI signatures, so
// stock libffi remains the sole dynamic ABI classifier.
func CallLLGo(
	plainCIF, coroCIF *Signature,
	descriptor, env, runtimeType, ret unsafe.Pointer,
	resultSize, resultAlign uintptr,
	args ...unsafe.Pointer,
) {
	if descriptor == nil {
		panic("reflect.Value.Call: call of nil function")
	}
	d := (*dispatchDescriptorV1)(descriptor)
	flags := d.Flags
	runtimeTyped := flags&dispatchRuntimeTyped != 0
	if d.Version != dispatchVersionV1 ||
		flags&^dispatchKnownFlags != 0 ||
		flags&dispatchCapabilityMask == 0 ||
		(flags&dispatchHasPlain != 0) != (d.PlainEntry != nil) ||
		(flags&dispatchHasCoro != 0) != (d.CoroEntry != nil) ||
		flags&dispatchNoCapture != 0 && env != nil ||
		runtimeTyped && (runtimeType == nil ||
			d.HashLo != uint64(bitcast.FromPointer(runtimeType)) ||
			d.HashHi != dispatchRuntimeTypeMagicV1) ||
		d.ResultSize != resultSize ||
		d.ResultAlign != resultAlign {
		panic("llgo: invalid managed function descriptor")
	}

	if flags&dispatchHasCoro != 0 {
		if coroCIF == nil {
			panic("llgo: missing coroutine reflection signature")
		}
		// A zero-result coroutine still records an out pointer in its frame
		// header. Keep that pointer non-nil even though the child never stores
		// through it.
		var empty byte
		if ret == nil {
			ret = unsafe.Pointer(&empty)
		}
		var g unsafe.Pointer
		out := ret
		ctxt := env
		argv := make([]unsafe.Pointer, len(args)+3)
		argv[0] = unsafe.Pointer(&g)
		argv[1] = unsafe.Pointer(&out)
		argv[2] = unsafe.Pointer(&ctxt)
		copy(argv[3:], args)
		coroFFICall(coroCIF, d.CoroEntry, ret, &g, &argv[0])
		return
	}

	if plainCIF == nil {
		panic("llgo: missing plain reflection signature")
	}
	ctxt := env
	argv := make([]unsafe.Pointer, len(args)+1)
	argv[0] = unsafe.Pointer(&ctxt)
	copy(argv[1:], args)
	ffi.Call(plainCIF, d.PlainEntry, ret, &argv[0])
}

type Closure struct {
	ptr unsafe.Pointer
	Fn  unsafe.Pointer
}

func NewClosure() *Closure {
	c := &Closure{}
	c.ptr = ffi.ClosureAlloc(&c.Fn)
	if c.ptr == nil || c.Fn == nil {
		panic("libffi: closure allocation failed")
	}
	return c
}

func (c *Closure) Free() {
	if c != nil && c.ptr != nil {
		ffi.ClosureFree(c.ptr)
		c.ptr = nil
	}
}

func (c *Closure) Bind(cif *Signature, fn ffi.ClosureFunc, userdata unsafe.Pointer) error {
	status := ffi.PreClosureLoc(c.ptr, cif, fn, userdata, c.Fn)
	if status == ffi.OK {
		return nil
	}
	return Error(status)
}

func Index(args *unsafe.Pointer, i uintptr) unsafe.Pointer {
	return ffi.Index(args, i)
}
