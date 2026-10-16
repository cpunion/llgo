// LITTEST darwin/arm64 linux/amd64
// Scope: arch (arm64, amd64)
package main

import _ "unsafe" // for go:linkname

type (
	CDouble = float64
	CInt    = int32
)

//go:linkname cSqrt C.sqrt
func cSqrt(x CDouble) CDouble

// llgo:link cAbs C.abs
func cAbs(x CInt) CInt { return 0 }

// llgo:type C
type CCallback func(CInt) CInt

//go:noinline
func callCInt(fn func(CInt) CInt, x CInt) CInt {
	return fn(x)
}

func callCallback(cb CCallback, v CInt) CInt {
	return cb(v)
}

func main() {
	cs := cSqrt
	_ = cs(4)
	_ = callCInt(cAbs, -3)

	cb := CCallback(func(x CInt) CInt { return x + 1 })
	_ = callCallback(cb, 7)
}

// A managed function descriptor may still select an external raw-C target;
// its invocation remains in the bounded worker domain.
// CHECK-LABEL: define ptr @"main.callCInt$coro"(ptr %0, ptr %1, { ptr, ptr } %2, i32 %3){{.*}} {
// CHECK: extractvalue { ptr, ptr } %2, 0
// CHECK: [[C_DESC:%[0-9]+]] = extractvalue { ptr, ptr } %2, 0
// CHECK-NEXT: [[C_ENV:%[0-9]+]] = extractvalue { ptr, ptr } %2, 1
// CHECK-NEXT: load { i32, i32, i64, i64, ptr, ptr, i64, i64, ptr }, ptr [[C_DESC]]
// CHECK: %coro.dispatch.version.invalid = icmp ne i32
// CHECK: [[C_STRUCTURED:%[0-9]+]] = call ptr %{{[0-9]+}}(ptr %0, ptr %{{[0-9]+}}, ptr [[C_ENV]], i32 %3)
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[C_STRUCTURED]],
// CHECK: [[C_PLAIN:%[0-9]+]] = call i32 %{{[0-9]+}}(ptr [[C_ENV]], i32 %3)

// A raw C callback is not executed on the scheduler thread. Submission,
// parking and completion all use the common worker event path.
// CHECK-LABEL: define ptr @"main.callCallback$coro"(ptr %0, ptr %1, ptr %2, i32 %3){{.*}} {
// CHECK: call i32 @__llgo_coro_os_thread_foreign_call_v1({{.*}}@__llgo_coro_worker_foreign_thunk_v1_{{[0-9a-f]+}}
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}@__llgo_coro_worker_foreign_thunk_v1_{{[0-9a-f]+}}
// CHECK: call i32 @__llgo_coro_worker_resume_v1(

// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call ptr @"main.callCInt$coro"({{.*}}{ ptr, ptr } { ptr @__llgo_coro_func_descriptor_v2.{{[0-9a-f.]+}}, ptr null }, i32 -3)
// CHECK: call i32 @__llgo_coro_os_thread_foreign_call_v1({{.*}}ptr @sqrt
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}ptr @sqrt
// CHECK: call ptr @"main.callCallback$coro"(ptr %0, ptr %{{[0-9]+}}, ptr @"main.main$1", i32 7)

// The C callback target itself keeps the raw C ABI.
// CHECK-LABEL: define i32 @"main.main$1"(i32 %0){{.*}} {
// CHECK: [[CALLBACK_RESULT:%[0-9]+]] = add i32 %0, 1
// CHECK-NEXT: ret i32 [[CALLBACK_RESULT]]

// CHECK-NOT: __llgo_stub
