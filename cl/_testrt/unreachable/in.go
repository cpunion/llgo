// LITTEST
package main

import (
	"github.com/goplus/lib/c"
)

// CHECK-LABEL: define void @main.foo(){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   unreachable
// CHECK-EMPTY:
// CHECK-NEXT: _llgo_1:{{.*}}; No predecessors!
// CHECK-NEXT:   ret void
// CHECK-NEXT: }
func foo() {
	c.Unreachable()
}

// Keep a source Jump and merge Phi after the intrinsic. The unreachable
// lowering must move that tail to a dead physical continuation instead of
// either appending a second terminator or dropping the Phi predecessor.
func unreachableMerge(cond bool, value int) int {
	result := value
	if cond {
		c.Unreachable()
		result = value + 1
	}
	return result
}

// printf is conservatively may-block. The source main therefore becomes a
// coroutine and the C call is isolated behind the bounded worker transaction;
// the compiler intrinsic in foo remains a direct plain terminal operation.
// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call void @main.foo()
// CHECK: call void @__llgo_coro_worker_park_v1
// CHECK: call i8 @llvm.coro.suspend
// CHECK: call i32 @__llgo_coro_worker_resume_v1

// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_foreign_thunk_v1_{{[0-9a-f]+}}(i64 %0){{.*}} {
// CHECK: call i32 (ptr, ...) @printf
func main() {
	foo()
	c.Printf(c.Str("Hello\n"))
}
