//go:build !nogc || windows

package runtime

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/abi"
	"github.com/xgo-dev/llgo/runtime/internal/ffi"
)

var finalizerFFITypeClosure = ffi.StructOf(ffi.TypePointer, ffi.TypePointer)

// Keep the Go ABI conversion local to the runtime. The low-level ffi package
// should not depend on runtime/abi.
func finalizerFFIType(typ *abi.Type) *ffi.Type {
	switch typ.Kind() {
	case abi.Bool:
		return ffi.TypeBool
	case abi.Int:
		return ffi.TypeInt
	case abi.Int8:
		return ffi.TypeInt8
	case abi.Int16:
		return ffi.TypeInt16
	case abi.Int32:
		return ffi.TypeInt32
	case abi.Int64:
		return ffi.TypeInt64
	case abi.Uint:
		return ffi.TypeUint
	case abi.Uint8:
		return ffi.TypeUint8
	case abi.Uint16:
		return ffi.TypeUint16
	case abi.Uint32:
		return ffi.TypeUint32
	case abi.Uint64:
		return ffi.TypeUint64
	case abi.Uintptr:
		return ffi.TypeUintptr
	case abi.Float32:
		return ffi.TypeFloat32
	case abi.Float64:
		return ffi.TypeFloat64
	case abi.Complex64:
		return ffi.TypeComplex64
	case abi.Complex128:
		return ffi.TypeComplex128
	case abi.Array:
		at := typ.ArrayType()
		return ffi.ArrayOf(finalizerFFIType(at.Elem), int(at.Len))
	case abi.Chan, abi.Map, abi.Pointer, abi.UnsafePointer:
		return ffi.TypePointer
	case abi.Func:
		return finalizerFFITypeClosure
	case abi.Interface:
		return ffi.TypeInterface
	case abi.Slice:
		return ffi.TypeSlice
	case abi.String:
		return ffi.TypeString
	case abi.Struct:
		if typ.IsClosure() {
			return finalizerFFITypeClosure
		}
		return finalizerFFIStructType(typ)
	}
	panic("runtime.SetFinalizer: unsupported result type " + typ.String())
}

func finalizerFFIStructType(typ *abi.Type) *ffi.Type {
	st := typ.StructType()
	fields := make([]*ffi.Type, 0, len(st.Fields))
	var off uintptr
	for _, field := range st.Fields {
		if field.Offset > off {
			fields, off = appendFinalizerFFIPadding(fields, off, field.Offset-off)
		}
		if field.Typ.Size_ == 0 {
			continue
		}
		fields = append(fields, finalizerFFIType(field.Typ))
		off = field.Offset + field.Typ.Size_
	}
	// Zero-sized fields do not consume registers in llgo's callable ABI.
	// Interior padding is already reconstructed from the following field's
	// offset, so do not pad solely to typ.Size_: a trailing zero-sized field
	// can enlarge the Go-visible size without adding an ABI slot.
	return ffi.StructOf(fields...)
}

func appendFinalizerFFIPadding(fields []*ffi.Type, off, size uintptr) ([]*ffi.Type, uintptr) {
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

func finalizerFFIReturnType(results []*abi.Type) *ffi.Type {
	switch len(results) {
	case 0:
		return ffi.TypeVoid
	case 1:
		return finalizerFFIType(results[0])
	default:
		fields := make([]*ffi.Type, len(results))
		for i, result := range results {
			fields[i] = finalizerFFIType(result)
		}
		return ffi.StructOf(fields...)
	}
}

func newFinalizerFFISignature(ft *abi.FuncType, explicitEnv bool, argType *ffi.Type) (*ffi.Signature, []*ffi.Type, uintptr) {
	paramTypes := make([]*ffi.Type, 0, 2)
	if explicitEnv {
		paramTypes = append(paramTypes, ffi.TypePointer)
	}
	paramTypes = append(paramTypes, argType)
	sig, err := ffi.NewSignature(finalizerFFIReturnType(ft.Out), paramTypes...)
	if err != nil {
		panic(err)
	}
	if sig.RType == ffi.TypeVoid {
		return sig, paramTypes, 0
	}
	retSize := sig.RType.Size
	if argSize := unsafe.Sizeof(uintptr(0)); retSize < argSize {
		// Conservatively reserve one word for every non-void result. In
		// particular, libffi writes sub-register scalar results as ffi_arg.
		retSize = argSize
	}
	return sig, paramTypes, retSize
}

// newFinalizerDispatchSignatures describes the three canonical managed
// function descriptor entries. The plain entry receives (env,arg), the
// outcome entry receives (g,out,completion,env,arg), and the coroutine entry
// receives (g,out,env,arg) and returns its initially suspended child handle.
// Result layout is the LLGo physical tuple layout recorded in the descriptor,
// while retSize also covers any wider temporary storage libffi may write.
func newFinalizerDispatchSignatures(ft *abi.FuncType, argType *ffi.Type) (
	plain, outcome, coro *ffi.Signature,
	resultSize, resultAlign, retSize uintptr,
) {
	plain, _, retSize = newFinalizerFFISignature(ft, true, argType)
	outcomeArgs := []*ffi.Type{
		ffi.TypePointer,
		ffi.TypePointer,
		ffi.TypePointer,
		ffi.TypePointer,
		argType,
	}
	var err error
	outcome, err = ffi.NewSignature(ffi.TypeVoid, outcomeArgs...)
	if err != nil {
		panic(err)
	}
	coroArgs := []*ffi.Type{
		ffi.TypePointer,
		ffi.TypePointer,
		ffi.TypePointer,
		argType,
	}
	coro, err = ffi.NewSignature(ffi.TypePointer, coroArgs...)
	if err != nil {
		panic(err)
	}

	resultSize, resultAlign = finalizerResultLayout(ft.Out)
	if resultSize > retSize {
		retSize = resultSize
	}
	if storage := finalizerResultStorageSize(ft.Out); storage > retSize {
		retSize = storage
	}
	return
}

// finalizerABITypeLayout mirrors the unpacked LLVM value layout used by the
// managed descriptor ABI. In particular, function values are always the
// canonical two-pointer {descriptor,environment} carrier and zero-length
// arrays retain alignment without consuming storage.
func finalizerABITypeLayout(typ *abi.Type) (size, alignment uintptr) {
	if typ == nil {
		panic("runtime.SetFinalizer: nil ABI type")
	}
	alignment = uintptr(typ.Align_)
	if alignment == 0 {
		alignment = 1
	}
	switch typ.Kind() {
	case abi.Func:
		return 2 * unsafe.Sizeof(uintptr(0)), unsafe.Alignof(uintptr(0))
	case abi.Array:
		array := typ.ArrayType()
		elemSize, elemAlign := finalizerABITypeLayout(array.Elem)
		return elemSize * array.Len, elemAlign
	case abi.Struct:
		if typ.IsClosure() {
			return 2 * unsafe.Sizeof(uintptr(0)), unsafe.Alignof(uintptr(0))
		}
		var offset uintptr
		alignment = 1
		for _, field := range typ.StructType().Fields {
			fieldSize, fieldAlign := finalizerABITypeLayout(field.Typ)
			offset = finalizerAlignUp(offset, fieldAlign)
			offset += fieldSize
			if fieldAlign > alignment {
				alignment = fieldAlign
			}
		}
		return finalizerAlignUp(offset, alignment), alignment
	default:
		return typ.Size_, alignment
	}
}

func finalizerResultLayout(results []*abi.Type) (size, alignment uintptr) {
	alignment = 1
	for _, result := range results {
		resultSize, resultAlign := finalizerABITypeLayout(result)
		size = finalizerAlignUp(size, resultAlign)
		size += resultSize
		if resultAlign > alignment {
			alignment = resultAlign
		}
	}
	return finalizerAlignUp(size, alignment), alignment
}

func finalizerResultStorageSize(results []*abi.Type) uintptr {
	var offset, storage uintptr
	for _, result := range results {
		resultSize, resultAlign := finalizerABITypeLayout(result)
		offset = finalizerAlignUp(offset, resultAlign)
		end := offset + result.Size_
		if end > storage {
			storage = end
		}
		offset += resultSize
	}
	return storage
}

func finalizerAlignUp(value, alignment uintptr) uintptr {
	return (value + alignment - 1) &^ (alignment - 1)
}
