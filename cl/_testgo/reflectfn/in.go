// LITTEST
package main

import (
	"fmt"
	"reflect"
)

// Function values exposed through interface/reflect use managed descriptors;
// native closure bodies retain the target hidden-context attribute.
// CHECK: @__llgo_coro_func_descriptor_v1.{{.*}} = linkonce_odr unnamed_addr constant
// CHECK-LABEL: define ptr @"main.demo$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"(
// CHECK: call void @__llgo_coro_complete_prepare_v2
func demo() {
	println("demo")
}

// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: store { ptr, ptr } { ptr @__llgo_coro_func_descriptor_v1.{{.*}}, ptr null }
// CHECK: call ptr @{{.*}}reflect.Value.UnsafePointer$coro{{.*}}(
// CHECK: call void @__llgo_coro_await_prepare_v3
func main() {
	v := 100
	fn := func() {
		println(v)
	}
	fdemo := demo
	fmt.Println(fn)
	fmt.Println(demo)
	fmt.Println(fdemo)
	fmt.Println(reflect.ValueOf(fn).UnsafePointer())
	fmt.Println(reflect.ValueOf(demo).UnsafePointer())
	fmt.Println(reflect.ValueOf(fdemo).UnsafePointer())
}

// CHECK-LABEL: define ptr @"main.main$1$coro"(ptr %0, ptr %1, ptr swiftself %2){{.*}} {
// CHECK: load { ptr }, ptr %2
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintInt$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3
// CHECK-NOT: __llgo_stub.
