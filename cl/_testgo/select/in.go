// LITTEST
package main

// A default select needs only the atomic try operation. A blocking select uses
// the same case set through try, park, suspension, and resume. Spawned case
// partners are committed as scheduler tasks, never native NewProc wrappers.
//
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"main.send$coro"(
// CHECK: call ptr @"main.recv$coro"(
// CHECK-LABEL: define ptr @"main.recv$coro"(
// CHECK-DAG: call { i64, i1, i1, i1 } @"{{.*}}CoroChanSelectTry"(
// CHECK-DAG: call ptr @__llgo_coro_spawn_begin_v1(
// CHECK-DAG: call void @__llgo_coro_spawn_commit_v1(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-LABEL: define ptr @"main.send$coro"(
// CHECK: call ptr @__llgo_coro_spawn_begin_v1(
// CHECK: call void @__llgo_coro_spawn_commit_v1(
// CHECK: call { i64, i1, i1, i1 } @"{{.*}}CoroChanSelectTry"(
// CHECK: call void @"{{.*}}CoroChanSelectPark"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK: call { i64, i1, i32 } @"{{.*}}CoroChanSelectResume"(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

func main() {
	send()
	recv()
}

func send() {
	ch1 := make(chan int)
	ch2 := make(chan int)

	go func() {
		println(<-ch1)
	}()
	go func() {
		println(<-ch2)
	}()

	select {
	case ch1 <- 100:
	case ch2 <- 200:
	}
}

func recv() {
	c1 := make(chan string)
	c2 := make(chan string)
	go func() {
		c1 <- "ch1"
	}()
	go func() {
		c2 <- "ch2"
	}()

	for i := 0; i < 2; i++ {
		select {
		case msg1 := <-c1:
			println(msg1)
		case msg2 := <-c2:
			println(msg2)
		default:
			println("exit")
		}
	}
}
