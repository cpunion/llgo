// LITTEST
package main

import "sync"

// CHECK: {{^}}@{{[0-9]+}} = private unnamed_addr constant [4 x i8] c"done", align 1{{$}}
// CHECK: {{^}}@{{[0-9]+}} = private unnamed_addr constant [6 x i8] c"work 1", align 1{{$}}
// CHECK: {{^}}@{{[0-9]+}} = private unnamed_addr constant [6 x i8] c"work 2", align 1{{$}}

func main() {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		println("work 1")
	}()
	go func() {
		defer wg.Done()
		println("work 2")
	}()
	wg.Wait()
	println("done")
}

// WaitGroup calls and both goroutines share the managed coroutine scheduler.
// These assertions intentionally cover protocol boundaries rather than the
// scheduler's internal block layout.
// CHECK-LABEL: define ptr @"main.init$coro"(
// CHECK: call ptr @"sync.init$coro"(

// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"sync.(*WaitGroup).Add$coro"(
// CHECK: call ptr @__llgo_coro_spawn_begin_v1(
// CHECK: call void @__llgo_coro_spawn_commit_v1(
// CHECK: call ptr @__llgo_coro_spawn_begin_v1(
// CHECK: call void @__llgo_coro_spawn_commit_v1(
// CHECK: call ptr @"sync.(*WaitGroup).Wait$coro"(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"(

// Captured goroutine bodies use the explicit environment parameter and await
// their deferred Done calls through the same protocol.
// CHECK-LABEL: define ptr @"main.main$1$coro"(ptr %0, ptr %1, ptr swiftself %2)
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"(
// CHECK: call ptr @"sync.(*WaitGroup).Done$coro"(

// CHECK-LABEL: define ptr @"main.main$2$coro"(ptr %0, ptr %1, ptr swiftself %2)
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintString$coro"(
// CHECK: call ptr @"sync.(*WaitGroup).Done$coro"(
