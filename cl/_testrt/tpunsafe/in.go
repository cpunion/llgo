// LITTEST
package main

import "unsafe"

type N[T any] struct {
	n1 T
	n2 T
}

type M[T any] struct {
	m0 T
	m1 int32
	m2 N[T]
}

func main() {
	m1 := M[bool]{}
	m1.check(1, 8, 1)
	m2 := M[int64]{}
	m2.check(8, 16, 8)
}

func (m *M[T]) check(align, offset1, offset2 uintptr) {
	if v := unsafe.Alignof(m.m2); v != align {
		println("have", v, "want", align)
		panic("unsafe.Alignof error")
	}
	if v := unsafe.Offsetof(m.m2); v != offset1 {
		println("have", v, "want", offset1)
		panic("unsafe.Offsetof error")
	}
	if v := unsafe.Offsetof(m.m2.n2); v != offset2 {
		println("have", v, "want", offset2)
		panic("unsafe.Offsetof error")
	}
}

// The generic unsafe layout builtins are type-only operations, but the
// diagnostic paths use managed print helpers. Keep this check focused on the
// stable semantic boundary: Offsetof/Alignof remain uintptr (PrintUint), each
// managed helper is awaited, and both instantiated layouts retain their exact
// generic instantiations remain distinct.

// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call ptr @"main.(*M[bool]).check$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK: call ptr @"main.(*M[int64]).check$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4

// CHECK-LABEL: define linkonce ptr @"main.(*M[bool]).check$coro"(ptr %0, ptr %1, ptr %2, i64 %3, i64 %4, i64 %5){{.*}} {
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4

// CHECK-LABEL: define linkonce ptr @"main.(*M[int64]).check$coro"(ptr %0, ptr %1, ptr %2, i64 %3, i64 %4, i64 %5){{.*}} {
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintUint$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4
