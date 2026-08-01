// LITTEST
package main

// CHECK-LABEL: define i64 @main.Foo(%"{{.*}}String" %0){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT:   %1 = extractvalue %"{{.*}}String" %0, 1
// CHECK-NEXT:   ret i64 %1
func Foo(s string) int {
	return len(s)
}

// A long-running loop is a stackless coroutine and must contain scheduler
// polling without turning its per-iteration scalar work into heap allocation.
// CHECK-LABEL: define ptr @"main.Test$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call i1 @__llgo_coro_preempt_poll_v1(ptr %0)
// CHECK: call i64 @main.Foo(%"{{.*}}String" { ptr @{{[0-9]+}}, i64 5 })
// CHECK: call ptr @"{{.*}}PrintInt$coro"
// CHECK: call void @__llgo_coro_await_prepare_v3
// CHECK: icmp slt i64 {{.*}}, 10000000
// CHECK: call ptr @"{{.*}}PrintByte$coro"({{.*}}i8 10)
// CHECK: call void @__llgo_coro_await_prepare_v3
// CHECK-NOT: call ptr @"{{.*}}AllocZ"
func Test() {
	j := 0
	for i := 0; i < 10000000; i++ {
		j += Foo("hello")
	}
	println(j)
}

// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call ptr @"main.Test$coro"
// CHECK: call void @__llgo_coro_await_prepare_v3
func main() {
	Test()
}
