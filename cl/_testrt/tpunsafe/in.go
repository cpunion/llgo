// LITTEST
// Scope: common
package main

import (
	"unsafe"
)

type N[T any] struct {
	n1 T
	n2 T
}

type M[T any] struct {
	m0 T
	m1 int32
	m2 N[T]
}

type packedLayout struct {
	a byte
	b uint8
	c uint16
	d byte
	e [8]int8
}

// CHECK-LABEL: define i64 @main.layoutSize(){{.*}} {
// CHECK: ret i64 14
func layoutSize() uintptr {
	return unsafe.Sizeof(packedLayout{})
}

func main() {
	if got := layoutSize(); got != 14 {
		panic("packed layout size mismatch")
	}
	m1 := M[bool]{}
	m1.check(1, 8, 1)
	m2 := M[int64]{}
	m2.check(8, 16, 8)
}

// Each instantiation folds Alignof/Offsetof to its concrete layout. The error
// paths can print and therefore use coroutine entries, but the comparison
// constants must remain the concrete layout values.
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

// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call i64 @main.layoutSize()
// CHECK: call ptr @"main.(*M[bool]).check$coro"({{.*}}i64 1, i64 8, i64 1)
// CHECK: call ptr @"main.(*M[int64]).check$coro"({{.*}}i64 8, i64 16, i64 8)

// CHECK-LABEL: define linkonce ptr @"main.(*M[bool]).check$coro"(
// CHECK: icmp ne i64 1, %{{[0-9]+}}
// CHECK: icmp ne i64 8, %{{[0-9]+}}
// CHECK: icmp ne i64 1, %{{[0-9]+}}
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintBatchV1$coro"

// CHECK-LABEL: define linkonce ptr @"main.(*M[int64]).check$coro"(
// CHECK: icmp ne i64 8, %{{[0-9]+}}
// CHECK: icmp ne i64 16, %{{[0-9]+}}
// CHECK: icmp ne i64 8, %{{[0-9]+}}
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintBatchV1$coro"
