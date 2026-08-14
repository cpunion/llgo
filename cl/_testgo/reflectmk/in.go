// LITTEST
package main

import (
	"fmt"
	"reflect"
)

// Keep this fixture focused on reflection semantics and stable stackless ABI
// invariants. expect.txt makes every failed constructor/method assertion a
// runtime failure; these checks cover the descriptor/coroutine representation.
// CHECK: @__llgo_coro_func_descriptor_v1.method-value.{{.*}} = linkonce_odr unnamed_addr constant
// CHECK: @__llgo_coro_func_descriptor_v1.method.{{.*}} = linkonce_odr unnamed_addr constant
// CHECK-LABEL: define ptr @"main.Point.String$coro"(ptr %0, ptr %1, %main.Point %2){{.*}} {
// CHECK: call ptr @"fmt.Sprintf$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK-LABEL: define ptr @"main.(*Point).Set$coro"(ptr %0, ptr %1, ptr %2, i64 %3, i64 %4){{.*}} {
// CHECK: store i64 %3
// CHECK: store i64 %4
// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK-DAG: call ptr @"reflect.ArrayOf$coro"(
// CHECK-DAG: call ptr @"reflect.ChanOf$coro"(
// CHECK-DAG: call ptr @"reflect.FuncOf$coro"(
// CHECK-DAG: call ptr @"reflect.MapOf$coro"(
// CHECK-DAG: call ptr @"reflect.PointerTo$coro"(
// CHECK-DAG: call ptr @"reflect.SliceOf$coro"(
// CHECK-DAG: call ptr @"reflect.StructOf$coro"(
// CHECK-DAG: call void @"reflect.Value.Method$outcome"(
// CHECK-DAG: call ptr @"reflect.Value.MethodByName$coro"(
// CHECK-DAG: call ptr @"reflect.Value.Call$coro"(
// CHECK-LABEL: define ptr @"main.method$coro"(ptr %0, ptr %1, i64 %2){{.*}} {
// CHECK: call ptr @"reflect.Value.Call$coro"(
// CHECK-LABEL: define ptr @"main.methodByName$coro"(ptr %0, ptr %1, %"{{.*}}String" %2){{.*}} {
// CHECK: call ptr @"reflect.Value.MethodByName$coro"(
// CHECK: call ptr @"reflect.Value.Call$coro"(
// CHECK-LABEL: define linkonce ptr @__llgo_coro_func_coro_v1.method-value.{{.*}}(ptr %0, ptr %1, ptr %2, %main.Point %3) {
// CHECK: call ptr @"main.Point.String$coro"(ptr %0, ptr %1, %main.Point %3)
// CHECK-NOT: __llgo_stub.

type Point struct {
	x int
	y int
}

func (p *Point) Set(x int, y int) {
	p.x = x
	p.y = y
}

func (p Point) String() string {
	return fmt.Sprintf("(%v,%v)", p.x, p.y)
}

func main() {
	rt := reflect.TypeOf((*Point)(nil)).Elem()
	if t := reflect.ArrayOf(1, rt); t.Elem() != rt {
		panic("arrayOf error")
	}
	if t := reflect.ChanOf(reflect.SendDir, rt); t.Elem() != rt {
		panic("chanOf error")
	}
	if t := reflect.FuncOf([]reflect.Type{rt}, []reflect.Type{rt}, false); t.In(0) != rt || t.Out(0) != rt {
		panic("funcOf error")
	}
	if t := reflect.MapOf(rt, rt); t.Key() != rt || t.Elem() != rt {
		panic("mapOf error")
	}
	if t := reflect.PointerTo(rt); t.Elem() != rt {
		panic("pointerTo error")
	}
	if t := reflect.SliceOf(rt); t.Elem() != rt {
		panic("sliceOf error")
	}
	if t := reflect.StructOf([]reflect.StructField{
		{Name: "T", Type: rt},
	}); t.Field(0).Type != rt {
		panic("structOf error")
	}
	if t := rt.Method(0); t.Name != "String" {
		panic("method error")
	}
	if t, ok := rt.MethodByName("String"); !ok || t.Name != "String" {
		panic("methodByName error")
	}
	v := reflect.ValueOf(&Point{1, 2})
	if r := v.Method(1).Call(nil); r[0].String() != "(1,2)" {
		panic("value.Method error")
	}
	if r := v.MethodByName("String").Call(nil); r[0].String() != "(1,2)" {
		panic("value.MethodByName error")
	}
	method(1)
	methodByName("String")
}

func method(n int) {
	v := reflect.ValueOf(&Point{1, 2})
	if r := v.Method(n).Call(nil); r[0].String() != "(1,2)" {
		panic("value.Method error")
	}
}

func methodByName(name string) {
	v := reflect.ValueOf(&Point{1, 2})
	if r := v.MethodByName(name).Call(nil); r[0].String() != "(1,2)" {
		panic("value.MethodByName error")
	}
}
