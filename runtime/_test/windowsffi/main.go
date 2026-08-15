package main

import (
	"reflect"
	"runtime"
	_ "unsafe"

	_ "github.com/goplus/llgo/runtime/internal/runtime"
)

const LLGoFiles = "_wrap/ffi.c"

//go:linkname callOnForeignThread C.llgo_windows_call_foreign_thread
func callOnForeignThread(fn, arg uintptr, result *uintptr) int32

type pair struct {
	Integer int64
	Float   float64
}

type mixedFunc func(int64, float64, complex128, pair) (int64, float64, complex128, pair)

type foreignGCProbe struct {
	value uintptr
}

func checkMixed(label string, got []reflect.Value) {
	if len(got) != 4 || got[0].Int() != 47 || got[1].Float() != 5.25 ||
		got[2].Complex() != complex(4.5, -1.25) ||
		got[3].Interface().(pair) != (pair{Integer: 16, Float: 5.5}) {
		panic("Windows reflect FFI corrupted " + label + " arguments or results")
	}
}

func makeForeignCallback(finalized chan uintptr, deferred, recovered *bool) func(uintptr) uintptr {
	probe := &foreignGCProbe{value: 100}
	runtime.SetFinalizer(probe, func(value *foreignGCProbe) {
		finalized <- value.value
	})
	return reflect.MakeFunc(reflect.TypeOf((func(uintptr) uintptr)(nil)), func(args []reflect.Value) []reflect.Value {
		defer func() { *deferred = true }()
		func() {
			defer func() {
				if value := recover(); value == "Windows foreign callback panic" {
					*recovered = true
				}
			}()
			panic("Windows foreign callback panic")
		}()
		stackProbe := &foreignGCProbe{value: 7}
		runtime.SetFinalizer(stackProbe, func(value *foreignGCProbe) {
			finalized <- value.value
		})
		runtime.GC()
		select {
		case <-finalized:
			panic("Windows foreign-thread callback lost a live GC root")
		default:
		}
		if stackProbe.value != 7 {
			panic("Windows foreign-thread callback corrupted a stack root")
		}
		runtime.KeepAlive(stackProbe)
		return []reflect.Value{reflect.ValueOf(probe.value + uintptr(args[0].Uint()))}
	}).Interface().(func(uintptr) uintptr)
}

//go:noinline
func checkForeignCallback(finalized chan uintptr) {
	deferred := false
	recovered := false
	foreign := makeForeignCallback(finalized, &deferred, &recovered)
	var result uintptr
	if errno := callOnForeignThread(reflect.ValueOf(foreign).Pointer(), 23, &result); errno != 0 || result != 123 {
		panic("Windows foreign-thread callback failed")
	}
	runtime.KeepAlive(foreign)
	if !deferred || !recovered {
		panic("Windows foreign-thread callback lost defer or panic/recover state")
	}
}

func main() {
	base := int64(40)
	integer := func(value int64) int64 { return base + value }
	if got := reflect.ValueOf(integer).Call([]reflect.Value{reflect.ValueOf(int64(2))})[0].Int(); got != 42 {
		panic("Windows reflect FFI corrupted an integer call")
	}
	floating := func(value float64) float64 { return value + 1.5 }
	if got := reflect.ValueOf(floating).Call([]reflect.Value{reflect.ValueOf(2.25)})[0].Float(); got != 3.75 {
		panic("Windows reflect FFI corrupted a floating-point call")
	}
	aggregate := func(value pair) pair {
		return pair{Integer: value.Integer + 1, Float: value.Float + 2}
	}
	if got := reflect.ValueOf(aggregate).Call([]reflect.Value{reflect.ValueOf(pair{3, 4})})[0].Interface().(pair); got != (pair{4, 6}) {
		panic("Windows reflect FFI corrupted an aggregate call")
	}
	complexValue := func(value complex128) complex128 { return value + complex(1, -2) }
	if got := reflect.ValueOf(complexValue).Call([]reflect.Value{reflect.ValueOf(complex(3.5, 0.75))})[0].Complex(); got != complex(4.5, -1.25) {
		panic("Windows reflect FFI corrupted a complex call")
	}
	dynamic := mixedFunc(func(integer int64, floating float64, value complex128, aggregate pair) (int64, float64, complex128, pair) {
		return base + integer, floating + 2, value + complex(1, -2), pair{
			Integer: aggregate.Integer + 5,
			Float:   aggregate.Float + 1.5,
		}
	})
	args := []reflect.Value{
		reflect.ValueOf(int64(7)),
		reflect.ValueOf(3.25),
		reflect.ValueOf(complex(3.5, 0.75)),
		reflect.ValueOf(pair{Integer: 11, Float: 4}),
	}
	checkMixed("dynamic", reflect.ValueOf(dynamic).Call(args))

	typ := reflect.TypeOf(mixedFunc(nil))
	made := reflect.MakeFunc(typ, func(args []reflect.Value) []reflect.Value {
		return reflect.ValueOf(dynamic).Call(args)
	}).Interface().(mixedFunc)
	gotInteger, gotFloat, gotComplex, gotPair := made(7, 3.25, complex(3.5, 0.75), pair{Integer: 11, Float: 4})
	if gotInteger != 47 || gotFloat != 5.25 || gotComplex != complex(4.5, -1.25) ||
		gotPair != (pair{Integer: 16, Float: 5.5}) {
		panic("Windows libffi closure corrupted a direct call")
	}
	checkMixed("MakeFunc", reflect.ValueOf(made).Call(args))

	for attempt := 0; attempt < 4; attempt++ {
		finalized := make(chan uintptr, 2)
		checkForeignCallback(finalized)
	}

	println("windows FFI smoke: ok")
}
