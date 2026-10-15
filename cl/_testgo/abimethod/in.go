// LITTEST
package main

import (
	"bytes"
	"fmt"
	"sync/atomic"
	"unsafe"
)

type T struct {
	n int
}

func (t T) Demo1() int {
	return t.n
}

func (t *T) Demo2() int {
	return t.n
}

func (t *T) demo3() int {
	return t.n
}

func main() {
	testGeneric()
	testNamed1()
	testNamed2()
	testNamed3()
	testAnonymous1()
	testAnonymous2()
	testAnonymous3()
	testAnonymous4()
	testAnonymous5()
	testAnonymous6()
	testAnonymous7()
	testAnonymous8()
	testAnonymousBuffer()
}

func testAnonymous1() {
	var s I = &struct {
		m int
		*T
	}{10, &T{100}}
	if s.Demo1() != 100 {
		panic("testAnonymous1 error")
	}
}

func testAnonymous2() {
	var s I = struct {
		m int
		*T
	}{10, &T{100}}
	if s.Demo1() != 100 {
		panic("testAnonymous2 error")
	}
}

func testAnonymous3() {
	var s I = struct {
		m int
		T
	}{10, T{100}}
	if s.Demo1() != 100 {
		panic("testAnonymous3 error")
	}
}

func testAnonymous4() {
	var s I = &struct {
		m int
		T
	}{10, T{100}}
	if s.Demo1() != 100 {
		panic("testAnonymous4 error")
	}
}

func testAnonymous5() {
	var s I2 = &struct {
		m int
		T
	}{10, T{100}}
	if s.Demo2() != 100 {
		panic("testAnonymous5 error")
	}
}

func testAnonymous6() {
	var s I2 = struct {
		m int
		*T
	}{10, &T{100}}
	if s.Demo2() != 100 {
		panic("testAnonymous6 error")
	}
}

func testAnonymous7() {
	var s interface {
		Demo1() int
		Demo2() int
	} = struct {
		m int
		*T
	}{10, &T{100}}
	if s.Demo1() != 100 {
		panic("testAnonymous7 error")
	}
	if s.Demo2() != 100 {
		panic("testAnonymous7 error")
	}
}

func testAnonymous8() {
	var s interface {
		Demo1() int
		Demo2() int
		demo3() int
	} = struct {
		m int
		*T
	}{10, &T{100}}
	if s.Demo1() != 100 {
		panic("testAnonymous8 error")
	}
	if s.Demo2() != 100 {
		panic("testAnonymous8 error")
	}
	if s.demo3() != 100 {
		panic("testAnonymous8 error")
	}
}

func testAnonymousBuffer() {
	var s fmt.Stringer = &struct {
		m int
		*bytes.Buffer
	}{10, bytes.NewBufferString("hello")}
	if s.String() != "hello" {
		panic("testAnonymousBuffer error")
	}
}

func testGeneric() {
	var p IP = &Pointer[any]{}
	p.Store(func() *any {
		var a any = 100
		return &a
	}())
	if (*p.Load()).(int) != 100 {
		panic("testGeneric error")
	}
}

func testNamed1() {
	var a I = &T{100}
	if a.Demo1() != 100 {
		panic("testNamed1 error")
	}
}

func testNamed2() {
	var a I = T{100}
	if a.Demo1() != 100 {
		panic("testNamed2 error")
	}
}

func testNamed3() {
	var a I2 = &T{100}
	if a.Demo2() != 100 {
		panic("testNamed4 error")
	}
}

type Pointer[T any] struct {
	// Mention *T in a field to disallow conversion between Pointer types.
	// See go.dev/issue/56603 for more details.
	// Use *T, not T, to avoid spurious recursive type definition errors.
	_ [0]*T
	v unsafe.Pointer
}

// Load atomically loads and returns the value stored in x.
func (x *Pointer[T]) Load() *T { return (*T)(atomic.LoadPointer(&x.v)) }

// Store atomically stores val into x.
func (x *Pointer[T]) Store(val *T) { atomic.StorePointer(&x.v, unsafe.Pointer(val)) }

type IP interface {
	Store(*any)
	Load() *any
}

type I interface {
	Demo1() int
}

type I2 interface {
	Demo2() int
}

// Plain value methods keep the native ABI. Pointer-receiver methods that can
// fault use the structured coroutine/outcome ABI so nil dereferences propagate
// through the same channel as suspendable calls.
// CHECK-LABEL: define i64 @main.T.Demo1(%main.T %0){{.*}} {
// CHECK: [[T_ADDR:%[0-9]+]] = alloca %main.T
// CHECK: store %main.T %0, ptr [[T_ADDR]]
// CHECK: [[T_N_PTR:%[0-9]+]] = getelementptr inbounds nuw %main.T, ptr [[T_ADDR]], i32 0, i32 0
// CHECK: [[T_N:%[0-9]+]] = load i64, ptr [[T_N_PTR]]
// CHECK: ret i64 [[T_N]]

// CHECK-LABEL: define ptr @"main.(*T).Demo2$coro"(ptr %0, ptr %1, ptr %2){{.*}} {
// CHECK: [[T2_NIL:%[0-9]+]] = icmp eq ptr %2, null
// CHECK: br i1 [[T2_NIL]],
// CHECK: call void @__llgo_coro_fault_prepare_v1({{.*}}i32 1)
// CHECK: [[T2_N_PTR:%[0-9]+]] = getelementptr inbounds nuw %main.T, ptr %2, i32 0, i32 0
// CHECK: [[T2_N:%[0-9]+]] = load i64, ptr [[T2_N_PTR]]

// CHECK-LABEL: define void @"main.(*T).Demo2$outcome"(ptr %0, ptr %1, ptr %2, ptr %3){{.*}} {
// CHECK-NOT: llvm.coro.
// CHECK: [[T2O_NIL:%[0-9]+]] = icmp eq ptr %3, null
// CHECK: [[T2O_N_PTR:%[0-9]+]] = getelementptr inbounds nuw %main.T, ptr %3, i32 0, i32 0
// CHECK: [[T2O_N:%[0-9]+]] = load i64, ptr [[T2O_N_PTR]]
// CHECK: store i64 [[T2O_N]], ptr %{{[0-9]+}}

// Interface dispatch to a promoted method is a typed coroutine call followed
// by the common inline await protocol.
// CHECK-LABEL: define ptr @"main.testAnonymous1$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: [[A1_CALL:%[0-9]+]] = call ptr @"__llgo$generated$promoted$Demo1$llgo$promoted$v1${{[0-9a-f]+}}$coro"(ptr %0, ptr %{{[0-9]+}}, ptr %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[A1_CALL]],

// Standard-library construction that cannot suspend stays an outcome call;
// the promoted String method remains a normal coroutine boundary.
// CHECK-LABEL: define ptr @"main.testAnonymousBuffer$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call void @"bytes.NewBufferString$outcome"(
// CHECK: [[STRING_CALL:%[0-9]+]] = call ptr @"__llgo$generated$promoted$String$llgo$promoted$v1${{[0-9a-f]+}}$coro"(ptr %0,
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[STRING_CALL]],

// Materialized generic methods are selected statically and retain their typed
// outcome entries. Only the type-assertion failure path suspends.
// CHECK-LABEL: define ptr @"main.testGeneric$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call void @"main.(*Pointer[interface{}]).Store$outcome"(
// CHECK: call void @"main.(*Pointer[interface{}]).Load$outcome"(
// CHECK: [[ASSERT_CALL:%[0-9]+]] = call ptr @"{{.*}}PanicTypeAssert$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[ASSERT_CALL]],

// A promoted value receiver whose projection and callee are statically proven
// is a frame-free tail forwarder. It must not grow another LLVM state machine.
// CHECK-LABEL: define linkonce ptr @"__llgo$generated$promoted$Grow$llgo$promoted$v1${{[0-9a-f]+}}$coro"(ptr %0, ptr %1, { i64, ptr } %2, i64 %3){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT: [[GROW_BUF:%[0-9]+]] = extractvalue { i64, ptr } %2, 1
// CHECK-NEXT: [[GROW_CALL:%[0-9]+]] = call ptr @"bytes.(*Buffer).Grow$coro"(ptr %0, ptr %1, ptr [[GROW_BUF]], i64 %3)
// CHECK-NEXT: ret ptr [[GROW_CALL]]

// CHECK-LABEL: define linkonce ptr @"__llgo$generated$promoted$WriteString$llgo$promoted$v1${{[0-9a-f]+}}$coro"(ptr %0, ptr %1, { i64, ptr } %2, %"{{.*}}String" %3){{.*}} {
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT: [[WRITE_BUF:%[0-9]+]] = extractvalue { i64, ptr } %2, 1
// CHECK-NEXT: [[WRITE_CALL:%[0-9]+]] = call ptr @"bytes.(*Buffer).WriteString$coro"(ptr %0, ptr %1, ptr [[WRITE_BUF]], %"{{.*}}String" %3)
// CHECK-NEXT: ret ptr [[WRITE_CALL]]

// Both physical entries for the instantiated generic pointer preserve the
// source program's sequentially consistent atomic operations.
// CHECK-LABEL: define linkonce ptr @"main.(*Pointer[interface{}]).Load$coro"(
// CHECK: load atomic ptr, ptr %{{[0-9]+}} seq_cst
// CHECK-LABEL: define linkonce void @"main.(*Pointer[interface{}]).Load$outcome"(
// CHECK-NOT: llvm.coro.
// CHECK: load atomic ptr, ptr %{{[0-9]+}} seq_cst
// CHECK-LABEL: define linkonce ptr @"main.(*Pointer[interface{}]).Store$coro"(
// CHECK: store atomic ptr %3, ptr %{{[0-9]+}} seq_cst
// CHECK-LABEL: define linkonce void @"main.(*Pointer[interface{}]).Store$outcome"(
// CHECK-NOT: llvm.coro.
// CHECK: store atomic ptr %4, ptr %{{[0-9]+}} seq_cst
