// LITTEST
package main

// Bind the four messages so the checks below prove which defer/panic payload
// flows through each lowering path without depending on numbered globals.

func main() {
	// The function installs one defer frame and preserves both the previous
	// thread frame and the initial resume block used after longjmp.
	// DARWIN-ARM64: [[SETJMP_RESULT:%[0-9]+]] = call i32 @sigsetjmp(ptr [[DEFER_JMPBUF]], i32 0)
	// LINUX-AMD64: [[SETJMP_RESULT:%[0-9]+]] = call i32 @__sigsetjmp(ptr [[DEFER_JMPBUF]], i32 0)


	// Plain println defers are registered as linked nodes.  Their state and
	// payload identify A as the outer defer and B as the inner one.

	// The state machine invokes defer 2 first, then enters a recover frame for
	// defer 1.  Capturing block labels keeps the relation without pinning their
	// generated numbers.

	// Both linked println nodes are popped, freed, and printed through the
	// values read from the same defer-head field.
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
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK-LABEL: define ptr @"main.main$1$coro"(
// CHECK: call void @__llgo_coro_recover_take_v1(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.main$2$coro"(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"
// CHECK: call void @__llgo_coro_panic_prepare_v1(
