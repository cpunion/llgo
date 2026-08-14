// LITTEST
package main

func run() {
	defer println("first")
	defer println("second")
}

func main() {
	run()
}

// CHECK: target triple = "wasm32-unknown-wasip1"
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call i32 @llvm.coro.size.i32()
// CHECK: call ptr @"main.run$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-LABEL: define ptr @"main.run$coro"(
// CHECK: call i32 @llvm.coro.size.i32()
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK: runtime.PrintString{{(\$coro)?}}
// CHECK: runtime.PrintString{{(\$coro)?}}
