package ffi

import (
	"unsafe"

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/clite/bitcast"
	"github.com/xgo-dev/llgo/runtime/internal/clite/ffi"
)

type Type = ffi.Type

type Signature = ffi.Cif

type ABI = c.Uint

const DefaultABI ABI = ffi.DefaultAbi

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
	return NewSignatureWithABI(DefaultABI, ret, args...)
}

// NewSignatureWithABI prepares a native call signature using abi. Most calls
// should use NewSignature; the explicit form is needed for platforms such as
// windows/386 that expose more than one C calling convention.
func NewSignatureWithABI(abi ABI, ret *Type, args ...*Type) (*Signature, error) {
	cif := &signatureHolder{
		args: append([]*Type(nil), args...),
		ret:  ret,
	}
	var atype **Type
	if len(cif.args) > 0 {
		atype = &cif.args[0]
	}
	status := ffi.PrepCif(&cif.Signature, abi, c.Uint(len(cif.args)), ret, atype)
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
	status := ffi.PrepCifVar(&cif.Signature, DefaultABI, c.Uint(fixed), c.Uint(len(cif.args)), ret, atype)
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
	dispatchVersionV2 uint32 = 2

	// These are compiler/runtime ABI bits. Keep the values explicit: placing
	// them after dispatchVersionV2 in this block must not shift their positions.
	dispatchHasPlain      uint32 = 1 << 0
	dispatchHasOutcome    uint32 = 1 << 1
	dispatchHasCoro       uint32 = 1 << 2
	dispatchNoCapture     uint32 = 1 << 3
	dispatchRuntimeTyped  uint32 = 1 << 4
	dispatchPlainNoUnwind uint32 = 1 << 5

	dispatchCapabilityMask = dispatchHasPlain | dispatchHasOutcome | dispatchHasCoro
	dispatchKnownFlags     = dispatchCapabilityMask | dispatchNoCapture | dispatchRuntimeTyped | dispatchPlainNoUnwind
)

const dispatchRuntimeTypeMagicV2 uint64 = 0x4c4c474f52545932 // "LLGORTY2"

func validDispatchPlainUnwindFlags(flags uint32) bool {
	hasPlain := flags&dispatchHasPlain != 0
	hasOutcome := flags&dispatchHasOutcome != 0
	hasCoro := flags&dispatchHasCoro != 0
	plainNoUnwind := flags&dispatchPlainNoUnwind != 0
	return !(hasOutcome && hasCoro) &&
		(!plainNoUnwind || hasPlain) &&
		(!hasPlain || hasOutcome || hasCoro || plainNoUnwind)
}

// dispatchDescriptorV2 is the runtime view of the compiler-owned universal
// function descriptor. Keep this layout synchronized with
// ssa.Program.coroDispatchDescriptorType.
type dispatchDescriptorV2 struct {
	Version         uint32
	Flags           uint32
	HashLo          uint64
	HashHi          uint64
	PlainEntry      unsafe.Pointer
	StructuredEntry unsafe.Pointer
	ResultSize      uintptr
	ResultAlign     uintptr
	CodeEntry       unsafe.Pointer
}

func validDispatchEntries(d *dispatchDescriptorV2) bool {
	if d == nil {
		return false
	}
	hasPlain := d.Flags&dispatchHasPlain != 0
	hasOutcome := d.Flags&dispatchHasOutcome != 0
	hasCoro := d.Flags&dispatchHasCoro != 0
	return !(hasOutcome && hasCoro) &&
		hasPlain == (d.PlainEntry != nil) &&
		(hasOutcome || hasCoro) == (d.StructuredEntry != nil)
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
	return unsafe.Pointer(&dispatchDescriptorV2{
		Version:         dispatchVersionV2,
		Flags:           dispatchHasCoro | dispatchRuntimeTyped,
		HashLo:          uint64(bitcast.FromPointer(runtimeType)),
		HashHi:          dispatchRuntimeTypeMagicV2,
		StructuredEntry: coroEntry,
		ResultSize:      resultSize,
		ResultAlign:     resultAlign,
		CodeEntry:       coroEntry,
	})
}

// CoroEntry returns the already-typed coroutine entry of one trusted managed
// descriptor. It is used while reflect.MakeFunc prepares its fixed-signature
// invoker bridge; ordinary calls still go through full descriptor validation.
func CoroEntry(descriptor unsafe.Pointer) unsafe.Pointer {
	if descriptor == nil {
		panic("llgo: nil managed function descriptor")
	}
	d := (*dispatchDescriptorV2)(descriptor)
	flags := d.Flags
	if d.Version != dispatchVersionV2 ||
		flags&^dispatchKnownFlags != 0 ||
		!validDispatchPlainUnwindFlags(flags) ||
		!validDispatchEntries(d) ||
		flags&dispatchHasCoro == 0 ||
		d.CodeEntry == nil {
		panic("llgo: invalid managed function descriptor")
	}
	return d.StructuredEntry
}

// CodeEntry returns the compiler-injected physical function identity carried
// by a managed descriptor. Dispatch thunks remain private call adapters; this
// entry is used only by reflection and runtime symbolization.
func CodeEntry(descriptor unsafe.Pointer) unsafe.Pointer {
	if descriptor == nil {
		return nil
	}
	d := (*dispatchDescriptorV2)(descriptor)
	flags := d.Flags
	if d.Version != dispatchVersionV2 ||
		flags&^dispatchKnownFlags != 0 ||
		!validDispatchPlainUnwindFlags(flags) ||
		flags&dispatchCapabilityMask == 0 ||
		!validDispatchEntries(d) ||
		d.CodeEntry == nil {
		panic("llgo: invalid managed function descriptor")
	}
	return d.CodeEntry
}

// coroFFICall is a compiler-owned stack cut. Its source ABI contains only the
// values known to reflection; lowering stores the current G into gslot, calls
// stock ffi_call to create the child through descriptor.StructuredEntry, and
// awaits the returned handle after ffi_call has left the native stack.
//
//go:linkname coroFFICall llgo.coroFFICall
func coroFFICall(cif *Signature, fn, out unsafe.Pointer, gslot *unsafe.Pointer, args *unsafe.Pointer)

//go:linkname coroCurrentTask llgo.coroCurrentTask
func coroCurrentTask() unsafe.Pointer

//go:linkname coroGoexit llgo.coroGoexit
func coroGoexit()

// coroPropagatePanic transports the already retained panic identity from one
// synchronous managed descriptor outcome into this physical parent. It is not
// source panic: compiler lowering preserves the existing logical trace rather
// than replacing it with a second panic publication.
//
//go:linkname coroPropagatePanic llgo.coroPropagatePanic
func coroPropagatePanic(typeWord, dataWord unsafe.Pointer)

const (
	dispatchOutcomeReturn   uint32 = 1
	dispatchOutcomePanic    uint32 = 2
	dispatchOutcomeGoexit   uint32 = 6
	dispatchOutcomeFaultNil uint32 = 7
)

type dispatchOutcomeCompletion struct {
	status   uint32
	typeWord unsafe.Pointer
	dataWord unsafe.Pointer
}

// CallLLGo invokes one managed {descriptor, environment} function value.
// plainCIF describes (env,args...)->results, outcomeCIF describes
// (g,out,completion,env,args...)->void, and coroCIF describes
// (g,out,env,args...)->handle. All are ordinary target-C-ABI signatures, so
// stock libffi remains the sole dynamic ABI classifier.
func CallLLGo(
	plainCIF, outcomeCIF, coroCIF *Signature,
	descriptor, env, runtimeType, ret unsafe.Pointer,
	resultSize, resultAlign uintptr,
	args ...unsafe.Pointer,
) {
	if descriptor == nil {
		panic("reflect.Value.Call: call of nil function")
	}
	d := (*dispatchDescriptorV2)(descriptor)
	flags := d.Flags
	runtimeTyped := flags&dispatchRuntimeTyped != 0
	if d.Version != dispatchVersionV2 ||
		flags&^dispatchKnownFlags != 0 ||
		!validDispatchPlainUnwindFlags(flags) ||
		flags&dispatchCapabilityMask == 0 ||
		!validDispatchEntries(d) ||
		flags&dispatchNoCapture != 0 && env != nil ||
		d.CodeEntry == nil ||
		runtimeTyped && (runtimeType == nil ||
			d.HashLo != uint64(bitcast.FromPointer(runtimeType)) ||
			d.HashHi != dispatchRuntimeTypeMagicV2) ||
		d.ResultSize != resultSize ||
		d.ResultAlign != resultAlign {
		panic("llgo: invalid managed function descriptor")
	}

	if flags&(dispatchHasOutcome|dispatchHasCoro) != 0 {
		// Every structured entry receives a non-nil out pointer, including a
		// zero-result function which never stores through it.
		var empty byte
		if ret == nil {
			ret = unsafe.Pointer(&empty)
		}
	}
	if flags&dispatchHasOutcome != 0 {
		if outcomeCIF == nil {
			panic("llgo: missing outcome reflection signature")
		}
		g := coroCurrentTask()
		if g == nil {
			panic("llgo: managed outcome call has no current task")
		}
		out := ret
		completion := dispatchOutcomeCompletion{}
		completionPtr := unsafe.Pointer(&completion)
		ctxt := env
		argv := make([]unsafe.Pointer, len(args)+4)
		argv[0] = unsafe.Pointer(&g)
		argv[1] = unsafe.Pointer(&out)
		argv[2] = unsafe.Pointer(&completionPtr)
		argv[3] = unsafe.Pointer(&ctxt)
		copy(argv[4:], args)
		ffi.Call(outcomeCIF, d.StructuredEntry, nil, &argv[0])
		// Consume the synchronous outcome in the same physical frame that
		// entered ffi.Call. A panic trace published by StructuredEntry is owned
		// by this frame; routing it through a Go helper would manufacture a child
		// activation which has no authority to propagate that retained trace.
		switch completion.status {
		case dispatchOutcomeReturn:
			if completion.typeWord == nil && completion.dataWord == nil {
				return
			}
		case dispatchOutcomePanic:
			if completion.typeWord != nil {
				coroPropagatePanic(completion.typeWord, completion.dataWord)
				return
			}
		case dispatchOutcomeGoexit:
			if completion.typeWord == nil && completion.dataWord == nil {
				coroGoexit()
				return
			}
		case dispatchOutcomeFaultNil:
			if completion.typeWord == nil && completion.dataWord == nil {
				var ptr *byte
				_ = *ptr
				return
			}
		}
		panic("llgo: invalid managed function outcome")
	}
	if flags&dispatchHasCoro != 0 {
		if coroCIF == nil {
			panic("llgo: missing coroutine reflection signature")
		}
		var g unsafe.Pointer
		out := ret
		ctxt := env
		argv := make([]unsafe.Pointer, len(args)+3)
		argv[0] = unsafe.Pointer(&g)
		argv[1] = unsafe.Pointer(&out)
		argv[2] = unsafe.Pointer(&ctxt)
		copy(argv[3:], args)
		coroFFICall(coroCIF, d.StructuredEntry, ret, &g, &argv[0])
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
