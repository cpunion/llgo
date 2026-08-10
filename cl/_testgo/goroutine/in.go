// LITTEST
package main

// A builtin has no independently callable SSA body. Spawning it must therefore
// use a compiler-owned typed carrier while preserving the same scheduler
// transaction as an ordinary goroutine.
//
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @__llgo_coro_spawn_begin_v1(ptr %0)
// CHECK: call ptr @"main.println$wrapper$llgo$builtin-spawn$v1${{.*}}$coro"(
// CHECK: call void @__llgo_coro_spawn_commit_v1(
// CHECK: call ptr @__llgo_coro_spawn_begin_v1(ptr %0)
// CHECK: call void @__llgo_coro_spawn_commit_v1(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$1$coro"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK: call ptr @"{{.*}}PrintString$coro"(
// CHECK: store i1 true
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.println$wrapper$llgo$builtin-spawn$v1${{.*}}$coro"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK: call ptr @"{{.*}}PrintString$coro"(
// CHECK: call ptr @"{{.*}}PrintByte$coro"(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

func main() {
	done := false
	go println("hello")
	go func(s string) {
		println(s)
		done = true
	}("Hello, goroutine")
	for !done {
		print(".")
	}
}
