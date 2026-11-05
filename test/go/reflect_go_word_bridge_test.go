package gotest

import (
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

type reflectGoNamedWord uintptr

//go:noinline
func reflectGoWordStatic(value uintptr) uintptr { return value + 42 }

func TestReflectGoWordCallBridge(t *testing.T) {
	wide := uint64(1) << 40
	value := uintptr(wide)
	for _, test := range []struct {
		name string
		fn   func(uintptr) uintptr
	}{
		{"without environment", reflectGoWordStatic},
		{"with environment", func(input uintptr) uintptr { return input + value + 42 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := reflect.ValueOf(test.fn).Call([]reflect.Value{reflect.ValueOf(value)})
			if len(out) != 1 || uintptr(out[0].Uint()) != test.fn(value) {
				t.Fatalf("Go-word reflection result %v, want %d", out, test.fn(value))
			}
		})
	}
}

func TestReflectGoWordMakeBridge(t *testing.T) {
	wide := uint64(1) << 40
	value := uintptr(wide)
	for _, typ := range []reflect.Type{
		reflect.TypeOf((func(uintptr) uintptr)(nil)),
		reflect.TypeOf((func(reflectGoNamedWord) reflectGoNamedWord)(nil)),
	} {
		t.Run(typ.String(), func(t *testing.T) {
			fn := reflect.MakeFunc(typ, func(in []reflect.Value) []reflect.Value {
				runtime.GC()
				out := reflect.New(typ.Out(0)).Elem()
				out.SetUint(in[0].Uint() + 42)
				return []reflect.Value{out}
			})
			arg := reflect.New(typ.In(0)).Elem()
			arg.SetUint(uint64(value))
			out := fn.Call([]reflect.Value{arg})
			if len(out) != 1 || uintptr(out[0].Uint()) != value+42 {
				t.Fatalf("Go-word MakeFunc result %v, want %d", out, value+42)
			}
			var typedResult uintptr
			switch typed := fn.Interface().(type) {
			case func(uintptr) uintptr:
				typedResult = typed(value)
			case func(reflectGoNamedWord) reflectGoNamedWord:
				typedResult = uintptr(typed(reflectGoNamedWord(value)))
			default:
				t.Fatal("MakeFunc lost the actual Go function type")
			}
			if typedResult != value+42 {
				t.Fatalf("typed Go-word MakeFunc result %d, want %d", typedResult, value+42)
			}
		})
	}
}

func TestReflectGoPointerMakeBridge(t *testing.T) {
	object := new(uint64)
	*object = 42
	typ := reflect.TypeOf((func(unsafe.Pointer) unsafe.Pointer)(nil))
	fn := reflect.MakeFunc(typ, func(in []reflect.Value) []reflect.Value {
		runtime.GC()
		return in
	})
	pointer := unsafe.Pointer(object)
	out := fn.Call([]reflect.Value{reflect.ValueOf(pointer)})
	if len(out) != 1 || out[0].UnsafePointer() != pointer || *object != 42 {
		t.Fatal("Go pointer MakeFunc lost its storage or GC root")
	}
	typed := fn.Interface().(func(unsafe.Pointer) unsafe.Pointer)
	if typed(pointer) != pointer || *object != 42 {
		t.Fatal("typed Go pointer MakeFunc lost its storage or GC root")
	}
	runtime.KeepAlive(object)
}
