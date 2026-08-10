// LITTEST
package main

// Nested blocking selects share the runtime select transaction and suspend the
// current stackless task. The lexical closure is a managed spawned child.
//
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @__llgo_coro_spawn_begin_v1(
// CHECK: call void @__llgo_coro_spawn_commit_v1(
// CHECK: call i1 @"{{.*}}CoroChanTrySend"(
// CHECK: call { i64, i1, i1, i1 } @"{{.*}}CoroChanSelectTry"(
// CHECK: call void @"{{.*}}CoroChanSelectPark"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK: call { i64, i1, i32 } @"{{.*}}CoroChanSelectResume"(
// CHECK-LABEL: define ptr @"main.main$1$coro"(ptr %0, ptr %1, ptr swiftself %2)
// CHECK: call { i1, i1 } @"{{.*}}CoroChanTryRecv"(
// CHECK: call { i64, i1, i1, i1 } @"{{.*}}CoroChanSelectTry"(
// CHECK: call void @"{{.*}}CoroChanSelectPark"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK: call { i64, i1, i32 } @"{{.*}}CoroChanSelectResume"(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

func main() {
	c1 := make(chan struct{}, 1)
	c2 := make(chan struct{}, 1)
	c3 := make(chan struct{}, 1)
	c4 := make(chan struct{}, 1)

	go func() {
		<-c1
		println("<-c1")

		select {
		case c2 <- struct{}{}:
			println("c2<-")
		case <-c3:
			println("<-c3")
		}
	}()

	c1 <- struct{}{}
	println("c1<-")

	select {
	case <-c2:
		println("<-c2")
	case <-c4:
		println("<-c4")
	}
}
