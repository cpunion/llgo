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
// CHECK: [[RUN:%[0-9]+]] = call ptr @"main.run$coro"(
// CHECK-NEXT: ret ptr [[RUN]]
// CHECK-LABEL: define ptr @"main.run$coro"(
// CHECK: call i32 @llvm.coro.size.i32()
// CHECK: call i8 @llvm.coro.suspend(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// WASI may keep the leaf batch printer plain; the source defer still owns one
// batch call per println and no per-operand print helper.
// CHECK: runtime.PrintBatchV1{{(\$coro)?}}
// CHECK: runtime.PrintBatchV1{{(\$coro)?}}
