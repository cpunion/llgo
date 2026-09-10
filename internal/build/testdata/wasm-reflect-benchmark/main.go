package main

import (
	"reflect"
	"time"
)

const iterations = 20000

var sink int64

func add(a, b int64) int64 { return a + b }

func main() {
	value := reflect.ValueOf(add)
	args := []reflect.Value{reflect.ValueOf(int64(20)), reflect.ValueOf(int64(22))}
	start := time.Now()
	for i := 0; i < iterations; i++ {
		sink += value.Call(args)[0].Int()
	}
	println("Value.Call", time.Since(start).Nanoseconds()/iterations, sink)

	typ := reflect.TypeOf((func(int64, int64) int64)(nil))
	made := reflect.MakeFunc(typ, func(args []reflect.Value) []reflect.Value {
		return []reflect.Value{reflect.ValueOf(args[0].Int() + args[1].Int())}
	}).Interface().(func(int64, int64) int64)
	start = time.Now()
	for i := 0; i < iterations; i++ {
		sink += made(20, 22)
	}
	println("MakeFunc", time.Since(start).Nanoseconds()/iterations, sink)
}
