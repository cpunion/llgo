package main

import (
	"reflect"
	"runtime"
)

type empty struct{}

func (empty) Eval(n int) int { return n + 5 }

type scalar int

func (v scalar) Eval(n int) int { return int(v) + n }

type cell struct{ value int }

func (v *cell) Eval(n int) int {
	runtime.GC()
	v.value += n
	return v.value
}

type sum int

func (v sum) Eval(values ...int) int {
	result := int(v)
	for _, n := range values {
		result += n
	}
	return result
}

func reflected(receiver any, argument reflect.Value, variadic bool) int {
	typ := reflect.TypeOf(receiver)
	method := typ.Method(0)
	named, ok := typ.MethodByName("Eval")
	if !ok || method.Name != "Eval" || named.Type != method.Type ||
		method.Type.NumIn() != 2 || method.Type.NumOut() != 1 ||
		method.Type.In(0) != typ || method.Type.IsVariadic() != variadic {
		panic("reflected method expression signature")
	}
	// Do not introduce a static method-expression funcval or function type:
	// either could register the missing Go-role metadata independently.
	arguments := []reflect.Value{reflect.ValueOf(receiver), argument}
	runtime.GC()
	var result []reflect.Value
	if variadic {
		result = method.Func.CallSlice(arguments)
	} else {
		result = method.Func.Call(arguments)
	}
	if len(result) != 1 || result[0].Kind() != reflect.Int {
		panic("reflected method expression result type")
	}
	return int(result[0].Int())
}

func main() {
	pointer := &cell{40}
	results := [4]int{
		reflected(empty{}, reflect.ValueOf(37), false),
		reflected(scalar(40), reflect.ValueOf(2), false),
		reflected(pointer, reflect.ValueOf(2), false),
		reflected(sum(39), reflect.ValueOf([]int{1, 2}), true),
	}
	for _, value := range results {
		if value != 42 {
			panic("reflected method expression value")
		}
	}
	if pointer.value != 42 {
		panic("reflected pointer receiver memory effect")
	}
	println("wasm reflect method expressions:", results[0], results[1], results[2], results[3])
}
