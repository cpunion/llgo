// LITTEST
package main

// Bind the four messages so the checks below prove which defer/panic payload
// flows through each lowering path without depending on numbered globals.

func main() {
	// The stackless cleanup state machine invokes defer 2 first, then transfers
	// the replacement panic through defer 1's recover frame. No native
	// setjmp/longjmp defer frame is part of this path.
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
// CHECK-NOT: sigsetjmp
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK-LABEL: define ptr @"main.main$1$coro"(
// CHECK-DAG: call void @__llgo_coro_recover_take_v1(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"
// CHECK-DAG: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.main$2$coro"(
// CHECK-DAG: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"
// CHECK-DAG: call void @__llgo_coro_panic_prepare_v1(
