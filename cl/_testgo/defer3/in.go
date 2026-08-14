// LITTEST
package main

func f(s string) bool {
	return len(s) > 2
}

func fail() {
	defer println("bye")
	panic("panic message")
}

func main() {
	defer func() {
		println("hi")
	}()
	if s := "hello"; f(s) {
		defer println(s)
	} else {
		defer println("world")
		return
	}
	fail()
	println("unreachable")
}

// CHECK-LABEL: define i1 @main.f(
// CHECK-NOT: @llvm.coro.
// CHECK: ret i1
// CHECK-LABEL: define ptr @"main.fail$coro"(
// CHECK: call void @__llgo_coro_frame_publish_v3(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK: call ptr @"main.fail$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK-LABEL: define ptr @"main.main$1$coro"(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"
