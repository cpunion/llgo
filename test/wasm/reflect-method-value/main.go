package main

import "reflect"

type value int

func (v value) Add(x int) int { return int(v) + x }

type pointer struct{ n int }

func (p *pointer) Add(x int) int { return p.n + x }

type adder interface{ Add(int) int }

func main() {
	// Keep this executable independent of Call/MakeFunc tests: those entry
	// points would enable bridges and mask a missing method-value root.
	for _, receiver := range []reflect.Value{
		reflect.ValueOf(value(40)),
		reflect.ValueOf(&pointer{40}),
	} {
		if receiver.Method(0).Interface().(func(int) int)(2) != 42 {
			panic("indexed method value")
		}
		if receiver.MethodByName("Add").Interface().(func(int) int)(2) != 42 {
			panic("named method value")
		}
	}
	var v adder = value(40)
	if reflect.ValueOf(&v).Elem().Method(0).Interface().(func(int) int)(2) != 42 {
		panic("interface method value")
	}
	println("wasm reflect method value ok")
}
