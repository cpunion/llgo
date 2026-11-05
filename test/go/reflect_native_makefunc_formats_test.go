//go:build !wasm || (!wasip1 && (!js || llgo.wasm.reflect.libffi))

package gotest

import (
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

type reflectNativeNamedWord uintptr

//llgo:cdecl
type reflectNativeNamedWordFunc func(reflectNativeNamedWord) reflectNativeNamedWord

//llgo:cdecl
type reflectNativeSignedWordFunc func(int) int

//llgo:cdecl
type reflectNativeUnsignedWordFunc func(uint) uint

//llgo:cdecl
type reflectNativeInt64Func func(int64) int64

//llgo:cdecl
type reflectNativeUint64Func func(uint64) uint64

//llgo:cdecl
type reflectNativeFloat32Func func(float32) float32

//llgo:cdecl
type reflectNativeFloat64Func func(float64) float64

//llgo:cdecl
type reflectNativePointerFunc func(*uint64) *uint64

//llgo:cdecl
type reflectNativeUnsafePointerFunc func(unsafe.Pointer) unsafe.Pointer

//llgo:cdecl
type reflectNativeWordArrayFunc func([2]uintptr) [2]uintptr

//llgo:cdecl
type reflectNativeNestedWordArrayFunc func([2][2]uintptr) [2][2]uintptr

//llgo:cdecl
type reflectNativePointerArrayFunc func([2]*uint64) [2]*uint64

func TestReflectNativeMakeFuncArrayFormats(t *testing.T) {
	first, second := new(uint64), new(uint64)
	*first, *second = 17, 29
	for _, test := range []struct {
		name        string
		prototype   any
		input, want any
		invoke      func(reflect.Value) any
	}{
		{
			"uintptr", reflectNativeWordArrayFunc(nil), [2]uintptr{17, 0xfedcba98}, [2]uintptr{0xfedcba87, 29},
			func(v reflect.Value) any {
				return v.Interface().(reflectNativeWordArrayFunc)([2]uintptr{17, 0xfedcba98})
			},
		},
		{
			"pointer", reflectNativePointerArrayFunc(nil), [2]*uint64{first, second}, [2]*uint64{second, first},
			func(v reflect.Value) any {
				return v.Interface().(reflectNativePointerArrayFunc)([2]*uint64{first, second})
			},
		},
		{
			"nested-uintptr", reflectNativeNestedWordArrayFunc(nil),
			[2][2]uintptr{{17, 0xfedcba98}, {29, 31}}, [2][2]uintptr{{31, 29}, {0xfedcba87, 17}},
			func(v reflect.Value) any {
				return v.Interface().(reflectNativeNestedWordArrayFunc)([2][2]uintptr{{17, 0xfedcba98}, {29, 31}})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var retained reflect.Value
			fn := reflect.MakeFunc(reflect.TypeOf(test.prototype), func(args []reflect.Value) []reflect.Value {
				if len(args) != 1 {
					t.Fatalf("array callback argument count = %d, want 1", len(args))
				}
				if got := args[0].Interface(); !reflect.DeepEqual(got, test.input) {
					t.Fatalf("array callback argument = %#v (%v, size %d), want %#v (%T)", got, args[0].Type(), args[0].Type().Size(), test.input, test.input)
				}
				retained = args[0]
				return []reflect.Value{reflect.ValueOf(test.want)}
			})
			out := fn.Call([]reflect.Value{reflect.ValueOf(test.input)})
			if len(out) != 1 || !reflect.DeepEqual(out[0].Interface(), test.want) {
				t.Fatalf("array Call = %v, want %v", out, test.want)
			}
			if got := test.invoke(fn); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("array Interface = %v, want %v", got, test.want)
			}
			runtime.GC()
			if !reflect.DeepEqual(retained.Interface(), test.input) {
				t.Fatal("array callback retained temporary native storage", retained)
			}
		})
	}
}

func TestReflectNativeMakeFuncScalarFormats(t *testing.T) {
	large := uint64(1)<<40 | 37
	signed := -int64(large)
	first, second := new(uint64), new(uint64)
	*first, *second = 11, 29
	for _, test := range []struct {
		name        string
		prototype   any
		input, want any
		invoke      func(reflect.Value) any
	}{
		{
			"named-uintptr", reflectNativeNamedWordFunc(nil), reflectNativeNamedWord(0xfedcba98), reflectNativeNamedWord(0xfedcba87),
			func(v reflect.Value) any {
				return v.Interface().(reflectNativeNamedWordFunc)(0xfedcba98)
			},
		},
		{
			"int", reflectNativeSignedWordFunc(nil), int(signed), int(signed + 19),
			func(v reflect.Value) any {
				return v.Interface().(reflectNativeSignedWordFunc)(int(signed))
			},
		},
		{
			"uint", reflectNativeUnsignedWordFunc(nil), uint(large), uint(large + 19),
			func(v reflect.Value) any {
				return v.Interface().(reflectNativeUnsignedWordFunc)(uint(large))
			},
		},
		{
			"int64", reflectNativeInt64Func(nil), signed, signed + 19,
			func(v reflect.Value) any {
				return v.Interface().(reflectNativeInt64Func)(signed)
			},
		},
		{
			"uint64", reflectNativeUint64Func(nil), large, large + 19,
			func(v reflect.Value) any {
				return v.Interface().(reflectNativeUint64Func)(large)
			},
		},
		{
			"float32", reflectNativeFloat32Func(nil), float32(1.25), float32(-2.5),
			func(v reflect.Value) any {
				return v.Interface().(reflectNativeFloat32Func)(1.25)
			},
		},
		{
			"float64", reflectNativeFloat64Func(nil), float64(1.25), float64(-2.5),
			func(v reflect.Value) any {
				return v.Interface().(reflectNativeFloat64Func)(1.25)
			},
		},
		{
			"pointer", reflectNativePointerFunc(nil), first, second,
			func(v reflect.Value) any {
				return v.Interface().(reflectNativePointerFunc)(first)
			},
		},
		{
			"unsafe-pointer", reflectNativeUnsafePointerFunc(nil), unsafe.Pointer(first), unsafe.Pointer(second),
			func(v reflect.Value) any {
				return v.Interface().(reflectNativeUnsafePointerFunc)(unsafe.Pointer(first))
			},
		},
		{
			"Go-uintptr", (func(uintptr) uintptr)(nil), uintptr(large), uintptr(large + 19),
			func(v reflect.Value) any {
				return v.Interface().(func(uintptr) uintptr)(uintptr(large))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var retained reflect.Value
			fn := reflect.MakeFunc(reflect.TypeOf(test.prototype), func(args []reflect.Value) []reflect.Value {
				if len(args) != 1 || !reflect.DeepEqual(args[0].Interface(), test.input) {
					t.Fatalf("callback argument = %v, want %v", args, test.input)
				}
				retained = args[0]
				return []reflect.Value{reflect.ValueOf(test.want)}
			})
			out := fn.Call([]reflect.Value{reflect.ValueOf(test.input)})
			if len(out) != 1 || !reflect.DeepEqual(out[0].Interface(), test.want) {
				t.Fatalf("Call = %v, want %v", out, test.want)
			}
			if got := test.invoke(fn); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Interface = %v, want %v", got, test.want)
			}
			runtime.GC()
			if !reflect.DeepEqual(retained.Interface(), test.input) {
				t.Fatal("callback retained a temporary native argument buffer", retained)
			}
		})
	}
}
