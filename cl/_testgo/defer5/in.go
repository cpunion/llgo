// LITTEST
package main

func main() {
	defer println("A")
	defer func() {
		if e := recover(); e != nil {
			println("in defer 1")
			panic("panic in defer 1")
		}
	}()
	defer func() {
		println("in defer 2")
		panic("panic in defer 2")
	}()
	defer println("B")
	panic("panic in main")
}

// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK-LABEL: define ptr @"main.main$1$coro"(
// CHECK: call void @__llgo_coro_recover_take_v1(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.main$2$coro"(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"
// CHECK: call void @__llgo_coro_panic_prepare_v1(
