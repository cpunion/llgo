// LITTEST
package main

import (
	"reflect"
	"strings"
)

// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call ptr @{{.*}}reflect.FuncOf$coro{{.*}}(ptr %0, ptr %{{.*}},
// CHECK: call ptr @{{.*}}reflect.MakeFunc$coro{{.*}}(ptr %0, ptr %{{.*}},
// CHECK: call ptr @{{.*}}reflect.Value.Interface$coro{{.*}}(ptr %0, ptr %{{.*}},
// CHECK: call ptr @{{.*}}runtime.MatchesClosure$coro{{.*}}(ptr %0, ptr %{{.*}},
// CHECK: call i1 @{{.*}}runtime.StringEqual{{.*}}(
// CHECK: call ptr %{{.*}}(ptr %0, ptr %{{.*}}, ptr %{{.*}}, %"{{.*}}String" { ptr @{{.*}}, i64 3 }, i64 2)
func main() {
	typ := reflect.FuncOf([]reflect.Type{reflect.TypeOf(""), reflect.TypeOf(0)}, []reflect.Type{reflect.TypeOf("")}, false)
	fn := reflect.MakeFunc(typ, func(args []reflect.Value) []reflect.Value {
		r := strings.Repeat(args[0].String(), int(args[1].Int()))
		return []reflect.Value{reflect.ValueOf(r)}
	})
	r := fn.Interface().(func(string, int) string)("abc", 2)
	if r != "abcabc" {
		panic("error")
	}
}

// CHECK-LABEL: define ptr @"main.main$1$coro"(ptr %0, ptr %1, %"{{.*}}Slice" %2){{.*}} {
// CHECK: call void @__llgo_coro_complete_prepare_v2
// CHECK: call ptr @{{.*}}reflect.Value.String$coro{{.*}}(
// CHECK: call ptr @{{.*}}reflect.Value.Int$coro{{.*}}(
// CHECK: call ptr @{{.*}}strings.Repeat$coro{{.*}}(
// CHECK: call ptr @{{.*}}reflect.ValueOf$coro{{.*}}(
// CHECK: }
