// LITTEST
package main

// CHECK-NOT: GetThreadDefer
// CHECK-NOT: SetThreadDefer
// CHECK-LABEL: define ptr @"main.End$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK: call void @__llgo_coro_recover_take_v1(
// CHECK: call ptr @"{{.*}}PrintString$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK: call ptr @"{{.*}}PrintByte$coro"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-NOT: GetThreadDefer
// CHECK-NOT: SetThreadDefer
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK: call ptr @"main.End$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-NOT: GetThreadDefer
// CHECK-NOT: SetThreadDefer

func End() {
	if recovered := recover(); recovered != nil {
		// Record but don't stop the panic.
		defer panic(recovered)
		println("will panic in defer")
	}
	println("end")
}

func main() {
	defer End()
	panic("panic in main")
}
