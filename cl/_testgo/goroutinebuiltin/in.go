// LITTEST
package main

// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @__llgo_coro_spawn_begin_v1(ptr %0)
// CHECK: call ptr @"main.close$wrapper$llgo$builtin-spawn$v1${{.*}}$coro"(
// CHECK: call void @__llgo_coro_spawn_commit_v1(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.close$wrapper$llgo$builtin-spawn$v1${{.*}}$coro"(
// CHECK: call i32 @"{{.*}}CoroChanTryCloseTask"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

func main() {
	done := make(chan struct{})
	go close(done)
	<-done
	println("ok")
}
