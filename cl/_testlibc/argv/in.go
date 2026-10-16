// LITTEST
// Scope: common
package main

import _ "unsafe"

type integer interface{ ~int32 }

//go:linkname argc __llgo_argc
var argc int32

//go:linkname argv __llgo_argv
var argv **int8

// llgo:link index llgo.index
func index[T any, I integer](ptr *T, offset I) T { return *ptr }

//go:linkname cstr llgo.cstr
func cstr(string) *int8

//go:linkname printf C.printf
func printf(format *int8, __llgo_va_list ...any) int32

// printf is an opaque C boundary, so main is colored automatically and the
// loop captures each argument before submitting a typed worker episode.
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: [[ARGV_INDEX:%[0-9]+]] = phi i32 [ 0, %{{.*}} ], [ %{{[0-9]+}}, %{{.*}} ]
// CHECK-DAG: load i32, ptr @__llgo_argc
// CHECK-DAG: load ptr, ptr @__llgo_argv
// CHECK-DAG: getelementptr ptr, ptr %{{[0-9]+}}, i32 [[ARGV_INDEX]]
// CHECK-DAG: add i32 [[ARGV_INDEX]], 1
// CHECK: call i32 @__llgo_coro_os_thread_foreign_call_v1({{.*}}ptrtoint (ptr @printf to i64)
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}ptrtoint (ptr @printf to i64)
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_foreign_thunk_v1_{{[0-9a-f]+}}(
// CHECK: call i32 (ptr, ...) @printf(ptr %{{[0-9]+}}, ptr %{{[0-9]+}})

func main() {
	for i := int32(0); i < argc; i++ {
		printf(cstr("%s\n"), index(argv, i))
	}
}
