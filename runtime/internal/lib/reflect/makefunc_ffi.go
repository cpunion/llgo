//go:build !wasm

package reflect

import (
	"sync"
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/internal/ffi"
)

func makeFFIFunc(ftyp *funcType, fn func([]Value) []Value, recoverTo unsafe.Pointer) Value {
	sig, err := toFFISig(ftyp.In, ftyp.Out)
	if err != nil {
		panic(err)
	}
	closure := ffi.NewClosure()
	userdata := &funcData{
		ftyp:        ftyp,
		fn:          fn,
		nin:         len(ftyp.In),
		tout:        toRuntimeTypes(ftyp.Out),
		recoverFrom: closure.Fn,
		recoverTo:   recoverTo,
	}

	err = closure.Bind(sig, makeFuncCallback(len(ftyp.Out)), unsafe.Pointer(userdata))
	if err != nil {
		panic("libffi error: " + err.Error())
	}
	// Keep the executable closure, its signature graph and callback state alive.
	keepMutex.Lock()
	keepAlive = append(keepAlive, closure, sig, userdata)
	keepMutex.Unlock()

	fv := &struct {
		fn  unsafe.Pointer
		env unsafe.Pointer
	}{closure.Fn, nil}
	return Value{closureOf(ftyp), unsafe.Pointer(fv), flagIndir | flag(Func)}
}

var (
	keepMutex sync.Mutex
	keepAlive []any
)

func bind0(_ *ffi.Signature, _ unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	fd := (*funcData)(userdata)
	ins := make([]Value, fd.nin)
	for i := 0; i < fd.nin; i++ {
		ins[i] = ffiToValue(ffi.Index(args, uintptr(i)), fd.ftyp.In[i])
	}
	fd.call(ins)
}

func bind1(_ *ffi.Signature, ret unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	fd := (*funcData)(userdata)
	ins := make([]Value, fd.nin)
	for i := 0; i < fd.nin; i++ {
		ins[i] = ffiToValue(ffi.Index(args, uintptr(i)), fd.ftyp.In[i])
	}
	out := validateMakeFuncResults(fd.call(ins), fd.ftyp, fd.tout)
	storeMakeFuncResult(ret, out[0], fd.tout[0])
}

func bindn(cif *ffi.Signature, ret unsafe.Pointer, args *unsafe.Pointer, userdata unsafe.Pointer) {
	fd := (*funcData)(userdata)
	ins := make([]Value, fd.nin)
	for i := 0; i < fd.nin; i++ {
		ins[i] = ffiToValue(ffi.Index(args, uintptr(i)), fd.ftyp.In[i])
	}
	outs := validateMakeFuncResults(fd.call(ins), fd.ftyp, fd.tout)
	var offset uintptr
	alignment := uintptr(cif.RType.Alignment)
	for i, out := range outs {
		typ := fd.tout[i]
		storeMakeFuncResult(add(ret, offset, ""), out, typ)
		offset += (typ.Size_ + alignment - 1) &^ (alignment - 1)
	}
}
