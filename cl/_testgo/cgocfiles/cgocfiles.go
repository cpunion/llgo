// LITTEST
// Scope: os (host CGo errno and aggregate ABI)
package main

/*
#include "in.h"
*/
import "C"

var checkErrno = func(error) {}

// The errno-aware C2 wrapper itself is a coroutine because the C call may
// block. It carries the typed call record through one same-M reentry episode;
// errno is sampled inside that same generated thunk before control returns.
// CHECK-LABEL: define ptr @"main._C2func_test_structs$coro"(ptr %0, ptr %1, ptr %2, ptr %3, ptr %4, ptr %5, ptr %6){{.*}} {
// CHECK: [[C2_SLOT:%[0-9]+]] = load ptr, ptr @main._cgo_{{.*}}_C2func_test_structs
// CHECK: call i32 @__llgo_coro_same_m_foreign_reentry_call_v2(
// CHECK: [[HAS_ERR:%[0-9]+]] = icmp ne i32 %{{[0-9]+}}, 0
// CHECK: [[ERRNO_VALUE:%[0-9]+]] = sext i32 %{{[0-9]+}} to i64
// CHECK: br i1 [[HAS_ERR]],

// The single-result spelling retains one compact raw wrapper for thunk use.
// CHECK-LABEL: define i32 @main._Cfunc_test_structs(ptr %0, ptr %1, ptr %2, ptr %3, ptr %4){{.*}} {
// CHECK: [[C_SLOT:%[0-9]+]] = load ptr, ptr @main._cgo_{{.*}}_Cfunc_test_structs
// CHECK-NEXT: [[C_FN:%[0-9]+]] = load ptr, ptr [[C_SLOT]]
// CHECK-NEXT: [[C_RESULT:%[0-9]+]] = call i32 [[C_FN]](ptr %0, ptr %1, ptr %2, ptr %3, ptr %4)
// CHECK-NEXT: ret i32 [[C_RESULT]]

// Aggregate allocation remains exact. The plain call uses the same-M boundary,
// while the C2 call is awaited as a child coroutine.
// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: [[S4:%[0-9]+]] = call ptr @"{{.*}}AllocZ"(i64 4)
// CHECK: [[S8:%[0-9]+]] = call ptr @"{{.*}}AllocZ"(i64 8)
// CHECK: [[S12:%[0-9]+]] = call ptr @"{{.*}}AllocZ"(i64 12)
// CHECK: [[S16:%[0-9]+]] = call ptr @"{{.*}}AllocZ"(i64 16)
// CHECK: [[S20:%[0-9]+]] = call ptr @"{{.*}}AllocZ"(i64 20)
// CHECK: call i32 @__llgo_coro_same_m_foreign_reentry_call_v2(
// CHECK: [[C2_HANDLE:%[0-9]+]] = call ptr @"main._C2func_test_structs$coro"(ptr %0, {{.*}}ptr [[S4]], ptr [[S8]], ptr [[S12]], ptr [[S16]], ptr [[S20]])
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[C2_HANDLE]],

// The C2 record captures both values before returning to managed execution.
// CHECK-LABEL: define linkonce i64 @__llgo_coro_generated_cgo_thunk_v2_{{[0-9a-f]+}}(i64 %0){{.*}} {
// CHECK: [[C2_RESULT:%[0-9]+]] = call i32 %{{[0-9]+}}(ptr
// CHECK: store i32 [[C2_RESULT]],
// CHECK: [[ERRNO:%[0-9]+]] = call i32 @cliteErrno()
// CHECK: store i32 [[ERRNO]],
// CHECK: ret i64 0

// CHECK-LABEL: define linkonce i64 @__llgo_coro_generated_cgo_thunk_v2_{{[0-9a-f]+}}(i64 %0){{.*}} {
// CHECK: call i32 @main._Cfunc_test_structs(ptr
// CHECK: ret i64 0
func main() {
	s4 := C.s4{a: 1}
	s8 := C.s8{a: 1, b: 2}
	s12 := C.s12{a: 1, b: 2, c: 3}
	s16 := C.s16{a: 1, b: 2, c: 3, d: 4}
	s20 := C.s20{a: 1, b: 2, c: 3, d: 4, e: 5}

	plain := C.test_structs(&s4, &s8, &s12, &s16, &s20)
	if plain != 35 {
		panic("plain aggregate wrapper")
	}

	withErrno, err := C.test_structs(&s4, &s8, &s12, &s16, &s20)
	if withErrno != 35 {
		panic("errno aggregate wrapper")
	}
	checkErrno(err)
}
