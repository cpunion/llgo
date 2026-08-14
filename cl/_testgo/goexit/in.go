// LITTEST
package main

import "runtime"

// Goexit is a structured coroutine outcome. Deferred closures run as awaited
// children, and channel sends/receives remain scheduler events throughout.
//
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.demo1$coro"(
// CHECK: call ptr @__llgo_coro_spawn_begin_v1(
// CHECK: call void @__llgo_coro_spawn_commit_v1(
// CHECK: call { i1, i1 } @"{{.*}}CoroChanTryRecv"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.demo1$1$coro"(
// CHECK: call ptr @"runtime.Goexit$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK: call ptr @"main.demo1$1$1$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.demo1$1$1$coro"(
// CHECK: call i1 @"{{.*}}CoroChanTrySend"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.demo2$1$coro"(
// CHECK: call ptr @"runtime.Goexit$coro"(
// CHECK: call ptr @"main.demo2$1$1$coro"(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.demo3$1$coro"(
// CHECK: call ptr @"runtime.Goexit$coro"(
// CHECK: call ptr @"main.demo3$1$1$coro"(
// CHECK: call ptr @"main.demo3$1$2$coro"(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"main.demo1$coro"(
// CHECK: call ptr @"main.demo2$coro"(
// CHECK: call ptr @"main.demo3$coro"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

func main() {
	demo1()
	demo2()
	demo3()
}

func demo1() {
	ch := make(chan bool)
	go func() {
		defer func() {
			ch <- true
		}()
		runtime.Goexit()
	}()
	<-ch
}

func demo2() {
	ch := make(chan bool)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				panic("must nil")
			}
			ch <- true
		}()
		runtime.Goexit()
	}()
	<-ch
}

func demo3() {
	ch := make(chan bool)
	go func() {
		defer func() {
			r := recover()
			if r != "error" {
				panic("must error")
			}
			ch <- true
		}()
		defer func() {
			if r := recover(); r != nil {
				panic("must nil")
			}
			panic("error")
		}()
		runtime.Goexit()
	}()
	<-ch
}
