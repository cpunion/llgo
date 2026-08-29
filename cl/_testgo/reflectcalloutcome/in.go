// LITTEST
package main

import "reflect"

var outcomeFn = outcomeLeaf

func outcomeLeaf(value int) int {
	if value < 0 {
		panic("reflect outcome panic")
	}
	return value + 2
}

func main() {
	// The direct function-value call makes outcomeFn's closed descriptor a
	// synchronous consumer. The reflective call must use the same outcome-only
	// descriptor rather than requiring a coroutine twin.
	println("direct", outcomeFn(3))
	out := reflect.ValueOf(outcomeFn).Call([]reflect.Value{reflect.ValueOf(5)})
	println("reflect", out[0].Int())

	defer func() {
		println(recover().(string))
	}()
	reflect.ValueOf(outcomeFn).Call([]reflect.Value{reflect.ValueOf(-1)})
}

// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call void %{{.*}}(ptr %{{.*}}, ptr %{{.*}}, ptr %{{.*}}, ptr %{{.*}}, i64 3)
// CHECK: call ptr @{{.*}}reflect.Value.Call$coro{{.*}}(
// CHECK-LABEL: define void @"main.outcomeLeaf$outcome"(
// CHECK: store i32 2,
