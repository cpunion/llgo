//go:build !llgo || !wasm || (!wasip1 && (!js || llgo.wasm.reflect.libffi))

package reflect

import (
	"sync"
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/ffi"
)

func makeProviderFunc(ftyp *funcType, fn func([]Value) []Value, recoverTo unsafe.Pointer) Value {
	if !ftyp.IsClosure() && ftyp.Variadic() {
		panic("reflect.MakeFunc: native variadic callback requires a fixed prototype")
	}
	sig, err := toFFISig(ftyp)
	if err != nil {
		panic(err)
	}
	ffiClosure := ffi.NewClosure()
	userdata := &funcData{
		ftyp:        ftyp,
		ffiSig:      sig,
		fn:          fn,
		nin:         len(ftyp.In),
		tout:        toRuntimeTypes(ftyp.Out),
		recoverFrom: ffiClosure.Fn,
		recoverTo:   recoverTo,
	}

	if err := ffiClosure.Bind(sig, makeFuncCallback(len(ftyp.Out)), unsafe.Pointer(userdata)); err != nil {
		panic("libffi error: " + err.Error())
	}
	// Keep the executable closure, signature graph, and callback state alive.
	keepMutex.Lock()
	keepAlive = append(keepAlive, ffiClosure, sig, userdata)
	keepMutex.Unlock()

	if !ftyp.IsClosure() {
		return Value{&ftyp.Type, ffiClosure.Fn, flag(Func)}
	}
	fv := &closure{fn: ffiClosure.Fn}
	return Value{&ftyp.Type, unsafe.Pointer(fv), flag(Func)}
}

var (
	keepMutex sync.Mutex
	keepAlive []any
)

func makeFuncFFIArgValue(fd *funcData, index int, ptr unsafe.Pointer) Value {
	typ := fd.ftyp.In[index]
	field := (*ffi.Type)(ffi.Index((*unsafe.Pointer)(unsafe.Pointer(fd.ffiSig.ArgTypes)), uintptr(index)))

	if field.Size != typ.Size_ || field.Alignment < uint16(typ.Align_) {
		// A physical native word/array must become owned Go-width storage,
		// rather than reading typ.Size_ bytes from libffi's shorter buffer.
		return valueFromFFIResult(ptr, typ, field)
	}
	return makeFuncArgValue(ptr, typ)
}

func bind0(_ *ffi.Signature, _ unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	fd := (*funcData)(userdata)
	ins := make([]Value, fd.nin)
	for i := 0; i < fd.nin; i++ {
		ins[i] = makeFuncFFIArgValue(fd, i, ffi.Index(args, uintptr(i)))
	}
	fd.call(ins)
}

func bind1(cif *ffi.Signature, ret unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	fd := (*funcData)(userdata)
	ins := make([]Value, fd.nin)
	for i := 0; i < fd.nin; i++ {
		ins[i] = makeFuncFFIArgValue(fd, i, ffi.Index(args, uintptr(i)))
	}
	out := validateMakeFuncResults(fd.call(ins), fd.ftyp, fd.tout)
	storeMakeFuncFFIResult(ret, out[0], fd.tout[0], cif.RType)
}

func bindn(cif *ffi.Signature, ret unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	fd := (*funcData)(userdata)
	ins := make([]Value, fd.nin)
	for i := 0; i < fd.nin; i++ {
		ins[i] = makeFuncFFIArgValue(fd, i, ffi.Index(args, uintptr(i)))
	}
	outs := validateMakeFuncResults(fd.call(ins), fd.ftyp, fd.tout)
	var offset uintptr
	for i, out := range outs {
		typ := fd.tout[i]
		field, fieldOffset, next := ffiResultField(cif.RType, i, offset)
		storeMakeFuncFFIResult(add(ret, fieldOffset, ""), out, typ, field)
		offset = next
	}
}
