package main

import (
	"reflect"
	"runtime"
	"unsafe"
)

func reflectedBase(n int, s string) (bool, int) { return true, n + len(s) }

//llgo:cdecl
type reflectedCFunc func(int, string) (bool, int)

type reflectedFunc func(int, string) (bool, int)

func (f reflectedFunc) Call(s string) (bool, int) { return f(100, s) }
func (f reflectedFunc) Demo() int                 { return 100 }

type reflectedHolder struct{ Fn reflectedFunc }

func checkCall(value reflect.Value) {
	result := value.Call([]reflect.Value{reflect.ValueOf(100), reflect.ValueOf("hello")})
	if !result[0].Bool() || result[1].Int() != 105 {
		panic("reflected function call")
	}
}

func testFunctionMetadata() {
	typ := reflect.TypeOf((*reflectedFunc)(nil)).Elem()
	if typ.Kind() != reflect.Func || typ.NumIn() != 2 || typ.NumOut() != 2 || typ.NumMethod() != 2 {
		panic("named function metadata")
	}
	if typ.In(0).Kind() != reflect.Int || typ.In(1).Kind() != reflect.String ||
		typ.Out(0).Kind() != reflect.Bool || typ.Out(1).Kind() != reflect.Int || typ.IsVariadic() {
		panic("named function input/output metadata")
	}
	if reflect.TypeOf(reflectedHolder{}).Field(0).Type.Kind() != reflect.Func {
		panic("function field metadata")
	}
	intType := reflect.TypeOf(0)
	if intType.Kind() != reflect.Int || intType.Name() != "int" {
		panic("named int metadata #1412")
	}

	base := reflect.ValueOf(reflectedBase)
	named := reflect.New(typ).Elem()
	named.Set(base)
	checkCall(named)
	method := named.MethodByName("Call").Call([]reflect.Value{reflect.ValueOf("hello")})
	if !method[0].Bool() || method[1].Int() != 105 {
		panic("named function method")
	}
	if result := named.MethodByName("Demo").Call(nil); len(result) != 1 || result[0].Int() != 100 {
		panic("zero-argument named function method")
	}

	closure := func(n int, s string) (bool, int) { return true, n + len(s) }
	newBase := reflect.New(base.Type()).Elem()
	newBase.Set(base)
	checkCall(newBase)
	newClosure := reflect.New(reflect.TypeOf(closure)).Elem()
	newClosure.Set(reflect.ValueOf(closure))
	checkCall(newClosure)
	baseFunction := reflectedBase
	newAtBase := reflect.NewAt(base.Type(), unsafe.Pointer(&baseFunction)).Elem()
	checkCall(newAtBase)
	newAt := reflect.NewAt(reflect.TypeOf(closure), unsafe.Pointer(&closure)).Elem()
	checkCall(newAt)

	noArguments := func() {}
	if reflect.ValueOf(noArguments).Kind() != reflect.Func || reflect.TypeOf(noArguments).NumIn() != 0 || reflect.TypeOf(noArguments).NumOut() != 0 {
		panic("no-argument function metadata")
	}
	returnsClosure := func(offset int) func(int) int {
		return func(value int) int { return value + offset }
	}
	returned := reflect.ValueOf(returnsClosure).Call([]reflect.Value{reflect.ValueOf(40)})[0]
	if returned.Kind() != reflect.Func || returned.Call([]reflect.Value{reflect.ValueOf(2)})[0].Int() != 42 {
		panic("function-returning-closure metadata")
	}

	// Convert at the compiler boundary before reflecting the native entry.
	// reflect.Set only copies a function representation; it cannot turn a Go
	// descriptor into a native C entry pointer.
	native := reflect.ValueOf(reflectedCFunc(reflectedBase))
	cTyp := native.Type()
	cValue := reflect.New(cTyp).Elem()
	cValue.Set(native)
	checkCall(cValue)
	copied := cValue.Interface().(reflectedCFunc)
	if ok, n := copied(100, "hello"); !ok || n != 105 {
		panic("reflected native function roundtrip")
	}
	cValue.Set(reflect.Zero(cTyp))
	if !cValue.IsNil() {
		panic("reflected nil native function")
	}
	cValue.Set(native)
	checkCall(cValue)

	// Stock Go ignores llgo:cdecl and gives these types the same function
	// representation. LLGo must reject copying between the two ABIs in
	// either direction, without changing the destination on failure.
	if runtime.Compiler == "llgo" {
		if base.Type().AssignableTo(cTyp) || cTyp.AssignableTo(base.Type()) {
			panic("Go and native function representations are assignable")
		}
		shouldPanic("is not assignable", func() { cValue.Set(base) })
		checkCall(cValue)
		shouldPanic("is not assignable", func() { newBase.Set(native) })
		checkCall(newBase)
	} else {
		if !base.Type().AssignableTo(cTyp) || !cTyp.AssignableTo(base.Type()) {
			panic("ordinary Go function assignment control")
		}
		cValue.Set(base)
		checkCall(cValue)
		newBase.Set(native)
		checkCall(newBase)
	}
}
