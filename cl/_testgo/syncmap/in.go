// LITTEST
package main

import (
	"fmt"
	"sync"
)

// Standard-library synchronization methods and the callback passed through a
// function value remain on the managed coroutine/descriptor ABI.
//
// CHECK: @__llgo_coro_func_descriptor_v1.{{.*}} = linkonce_odr unnamed_addr constant
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"sync.(*Map).Store$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK: call ptr @"sync.(*Map).Load$coro"(
// CHECK: call ptr @"fmt.Println$coro"(
// CHECK: call ptr @"sync.(*Map).Range$coro"(
// CHECK-SAME: { ptr, ptr } { ptr @__llgo_coro_func_descriptor_v1.{{.*}}, ptr null }
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-LABEL: define ptr @"main.main$1$coro"(
// CHECK: call ptr @"fmt.Printf$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK-NOT: __llgo_stub.
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

func main() {
	var m sync.Map
	m.Store(1, "hello")
	m.Store("1", 100)
	v, ok := m.Load("1")
	fmt.Println(v, ok)
	m.Range(func(k, v interface{}) bool {
		fmt.Printf("%#v %v\n", k, v)
		return true
	})
}
