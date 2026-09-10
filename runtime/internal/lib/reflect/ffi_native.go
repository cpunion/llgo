//go:build !wasm

package reflect

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/abi"
	"github.com/xgo-dev/llgo/runtime/internal/ffi"
	"github.com/xgo-dev/llgo/runtime/internal/runtime"
)

var ffiTypeClosure = ffi.StructOf(ffi.TypePointer, ffi.TypePointer)

func prepareReflectClosureCall(env unsafe.Pointer, tin []*abi.Type, args []unsafe.Pointer) ([]*abi.Type, []unsafe.Pointer, int) {
	if env == nil || !ffi.ClosureEnvExplicit {
		return tin, args, 0
	}
	tin = append([]*abi.Type{rtypeOf(unsafe.Pointer(nil))}, tin...)
	args = append(args, unsafe.Pointer(&env))
	return tin, args, 1
}

func toFFIType(typ *abi.Type) *ffi.Type {
	kind := typ.Kind()
	switch kind {
	case abi.Bool, abi.Int, abi.Int8, abi.Int16, abi.Int32, abi.Int64,
		abi.Uint, abi.Uint8, abi.Uint16, abi.Uint32, abi.Uint64, abi.Uintptr,
		abi.Float32, abi.Float64, abi.Complex64, abi.Complex128:
		return ffi.Typ[kind]
	case abi.Array:
		st := typ.ArrayType()
		return ffi.ArrayOf(toFFIType(st.Elem), int(st.Len))
	case abi.Chan:
		return ffi.TypePointer
	case abi.Func:
		return ffiTypeClosure
	case abi.Interface:
		return ffi.TypeInterface
	case abi.Map:
		return ffi.TypePointer
	case abi.Pointer:
		return ffi.TypePointer
	case abi.Slice:
		return ffi.TypeSlice
	case abi.String:
		return ffi.TypeString
	case abi.Struct:
		if typ.IsClosure() {
			return ffiTypeClosure
		}
		return toFFIStructType(typ)
	case abi.UnsafePointer:
		return ffi.TypePointer
	}
	panic("reflect.toFFIType unsupport type " + typ.String())
}

func toFFIStructType(typ *abi.Type) *ffi.Type {
	st := typ.StructType()
	fields := make([]*ffi.Type, 0, len(st.Fields))
	var off uintptr
	for _, fs := range st.Fields {
		if fs.Offset > off {
			fields, off = appendFFIPadding(fields, off, fs.Offset-off)
		}
		if fs.Typ.Size_ == 0 {
			continue
		}
		fields = append(fields, toFFIType(fs.Typ))
		off = fs.Offset + fs.Typ.Size_
	}
	// Do not pad to typ.Size_: trailing zero-sized fields can enlarge the
	// Go-visible size without consuming registers in llgo's callable ABI.
	return ffi.StructOf(fields...)
}

func appendFFIPadding(fields []*ffi.Type, off, size uintptr) ([]*ffi.Type, uintptr) {
	for size > 0 {
		switch {
		case off%8 == 0 && size >= 8:
			fields = append(fields, ffi.TypeUint64)
			off += 8
			size -= 8
		case off%4 == 0 && size >= 4:
			fields = append(fields, ffi.TypeUint32)
			off += 4
			size -= 4
		case off%2 == 0 && size >= 2:
			fields = append(fields, ffi.TypeUint16)
			off += 2
			size -= 2
		default:
			fields = append(fields, ffi.TypeUint8)
			off++
			size--
		}
	}
	return fields, off
}

func toFFISig(tin, tout []*abi.Type) (*ffi.Signature, error) {
	args := make([]*ffi.Type, len(tin))
	for i, in := range tin {
		args[i] = toFFIType(in)
	}
	return ffi.NewSignature(toFFIRetType(tout), args...)
}

func toFFIRetType(tout []*abi.Type) *ffi.Type {
	switch n := len(tout); n {
	case 0:
		return ffi.TypeVoid
	case 1:
		return toFFIType(tout[0])
	default:
		fields := make([]*ffi.Type, n)
		for i, out := range tout {
			fields[i] = toFFIType(out)
		}
		return ffi.StructOf(fields...)
	}
}

func callFFIBridge(ft *abi.FuncType, fn, env unsafe.Pointer, tin []*abi.Type, ioff int, args []unsafe.Pointer, in []Value) (out []Value) {
	ffiArgs := make([]*ffi.Type, 0, len(tin)+4)
	for i := 0; i < ioff; i++ {
		ffiArgs = append(ffiArgs, toFFIType(tin[i]))
	}
	for i, arg := range in {
		typ := tin[ioff+i]
		if ffiCallSliceAsTriple && typ.Kind() == abi.Slice {
			h := (*unsafeheaderSlice)(arg.ptr)
			ffiArgs = append(ffiArgs, ffi.TypePointer, ffi.TypeInt, ffi.TypeInt)
			args = append(args, unsafe.Pointer(&h.Data), unsafe.Pointer(&h.Len), unsafe.Pointer(&h.Cap))
			continue
		}
		ffiArgs = append(ffiArgs, toFFIType(typ))
		args = append(args, toFFIArg(arg, typ))
	}

	sig, err := ffi.NewSignature(toFFIRetType(ft.Out), ffiArgs...)
	if err != nil {
		panic(err)
	}
	var ret unsafe.Pointer
	if sig.RType != ffi.TypeVoid {
		ret = runtime.AllocZ(sig.RType.Size)
	}

	ffi.CallWithEnv(sig, fn, env, ret, args...)
	tout := toRuntimeTypes(ft.Out)
	switch n := len(tout); n {
	case 0:
	case 1:
		out := NewAt(toType(tout[0]), ret).Elem()
		resolveIndirectValue(&out, tout[0])
		return []Value{out}
	default:
		out = make([]Value, n)
		alignment := uintptr(sig.RType.Alignment)
		var off uintptr
		for i, typ := range tout {
			out[i] = NewAt(toType(typ), add(ret, off, "")).Elem()
			resolveIndirectValue(&out[i], typ)
			off += (typ.Size_ + alignment - 1) &^ (alignment - 1)
		}
	}
	return
}
