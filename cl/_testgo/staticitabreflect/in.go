package main

import "reflect"

type valueInterface interface {
	Value() int
}

type value struct {
	n int
}

func (v value) Value() int {
	return v.n
}

//go:noinline
func reflected(v value) valueInterface {
	return reflect.ValueOf(v).Interface().(valueInterface)
}

func main() {
	v := value{n: 23}
	static := valueInterface(v)
	dynamic := reflected(v)
	println(static == dynamic, static.Value(), dynamic.Value())
}
