// LITTEST
package main

// This fixture intentionally uses focused checks instead of mirroring the
// complete coroutine state machine. It verifies that generic composite map
// keys retain their typed operations while every potentially suspending map
// and print helper participates in the common structured-await protocol.
// CHECK: {{^}}@[[WORLD:[0-9]+]] = private unnamed_addr constant [5 x i8] c"world", align 1{{$}}

type T1 int

type T2 struct {
	v int
}

type T3[T any] struct {
	v T
}

type cacheKey struct {
	t1 T1
	t2 T2
	t3 T3[any]
	t4 *int
	t5 uintptr
}

func main() {
	m := map[cacheKey]string{}
	m[cacheKey{0, T2{0}, T3[any]{0}, nil, 0}] = "world"
	v, ok := m[cacheKey{0, T2{0}, T3[any]{0}, nil, 0}]
	println(v, ok)
}

// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK-SAME: ptr %[[EXEC:[0-9]+]], ptr %{{[0-9]+}}){{.*}} {
// CHECK: call token @llvm.coro.id(
// CHECK: call ptr @llvm.coro.begin(
// CHECK: [[MAKE:%[0-9]+]] = call ptr @"{{.*}}/runtime/internal/runtime.makemap$coro"(ptr %[[EXEC]], {{.*}}ptr @"map[_llgo_main.cacheKey]_llgo_string", i64 0, ptr null)
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[MAKE]],
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 48)
// CHECK: [[ASSIGN:%[0-9]+]] = call ptr @"{{.*}}/runtime/internal/runtime.mapassign$coro"(ptr %[[EXEC]], {{.*}}ptr @"map[_llgo_main.cacheKey]_llgo_string",
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[ASSIGN]],
// CHECK: store %"{{.*}}/runtime/internal/runtime.String" { ptr @[[WORLD]], i64 5 }, ptr %{{[0-9]+}}
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 48)
// CHECK: [[ACCESS:%[0-9]+]] = call ptr @"{{.*}}/runtime/internal/runtime.mapaccess2$coro"(ptr %[[EXEC]], {{.*}}ptr @"map[_llgo_main.cacheKey]_llgo_string",
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[ACCESS]],
// CHECK: [[PRINT:%[0-9]+]] = call ptr @"{{.*}}/runtime/internal/runtime.PrintBatchV1$coro"(ptr %[[EXEC]],
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[PRINT]],
