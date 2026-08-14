// LITTEST
package main

func main() {
	ch := make(chan int, 10)
	var v any = ch
	println(ch, len(ch), cap(ch), v)
	go func() {
		ch <- 100
	}()
	n := <-ch
	println(n)

	ch2 := make(chan int, 10)
	go func() {
		close(ch2)
	}()
	n2, ok := <-ch2
	println(n2, ok)
}

// Keep this fixture focused on the channel scheduler contract rather than the
// incidental block layout produced by LLVM.
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.NewChan$coro"
// CHECK: call ptr @__llgo_coro_spawn_begin_v1(
// CHECK: call void @__llgo_coro_spawn_commit_v1(
// CHECK: CoroChanTryRecv
// CHECK: call i8 @llvm.coro.suspend(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.NewChan$coro"
// CHECK: call ptr @__llgo_coro_spawn_begin_v1(
// CHECK: call void @__llgo_coro_spawn_commit_v1(
// CHECK: CoroChanTryRecv
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-LABEL: define ptr @"main.main$1$coro"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK: CoroChanTrySend
// CHECK-LABEL: define ptr @"main.main$2$coro"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK: CoroChanTryCloseTask
