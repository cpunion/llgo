// LITTEST
// Scope: common
package main

// A pure helper remains on the ordinary ABI.
// CHECK-LABEL: define i1 @main.f(%"{{.*}}String" %0){{.*}} {
// CHECK: [[F_LEN:%[0-9]+]] = extractvalue %"{{.*}}String" %0, 1
// CHECK-NEXT: [[F_RESULT:%[0-9]+]] = icmp sgt i64 [[F_LEN]], 2
// CHECK-NEXT: ret i1 [[F_RESULT]]

// Defer lowering is part of the coroutine cleanup graph. Every deferred child
// is awaited with a recover alias, so panic/recover state follows the logical G
// across suspension; no thread-local Defer list is constructed.
// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK-NOT: GetThreadDefer
// CHECK-NOT: SetThreadDefer
// CHECK-NOT: FreeDeferNode
// CHECK: [[LOOP_CALL:%[0-9]+]] = call ptr @"main.zLoopJoin$coro"(ptr %0, ptr %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[LOOP_CALL]],

// CHECK: [[CLOSURE_CALL:%[0-9]+]] = call ptr @"main.main$1$coro"(ptr %0, ptr %{{[0-9]+}})
// CHECK: [[CLOSURE_ALIAS:%[0-9]+]] = call ptr @__llgo_coro_recover_alias_begin_v1(ptr %0, ptr null, ptr [[CLOSURE_CALL]], i1 %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[CLOSURE_CALL]],
// CHECK: call void @__llgo_coro_recover_alias_end_v1(ptr %0, ptr [[CLOSURE_ALIAS]])

// CHECK: [[PRINT0:%[0-9]+]] = call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: [[PRINT_ALIAS0:%[0-9]+]] = call ptr @__llgo_coro_recover_alias_begin_v1(ptr %0, ptr null, ptr [[PRINT0]], i1 %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[PRINT0]],
// CHECK: call void @__llgo_coro_recover_alias_end_v1(ptr %0, ptr [[PRINT_ALIAS0]])

// CHECK: [[PRINT1:%[0-9]+]] = call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: [[PRINT_ALIAS1:%[0-9]+]] = call ptr @__llgo_coro_recover_alias_begin_v1(ptr %0, ptr null, ptr [[PRINT1]], i1 %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[PRINT1]],
// CHECK: call void @__llgo_coro_recover_alias_end_v1(ptr %0, ptr [[PRINT_ALIAS1]])

// CHECK: [[PRINT2:%[0-9]+]] = call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: [[PRINT_ALIAS2:%[0-9]+]] = call ptr @__llgo_coro_recover_alias_begin_v1(ptr %0, ptr null, ptr [[PRINT2]], i1 %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[PRINT2]],
// CHECK: call void @__llgo_coro_recover_alias_end_v1(ptr %0, ptr [[PRINT_ALIAS2]])

// The closure body itself uses the same scheduler-aware print operation.
// CHECK-LABEL: define ptr @"main.main$1$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: [[HI_PRINT:%[0-9]+]] = call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[HI_PRINT]],

func f(s string) bool {
	return len(s) > 2
}

func main() {
	defer func() {
		println("hi")
	}()
	if s := "hello"; f(s) {
		defer println(s)
	} else {
		defer println("world")
		return
	}
	defer println("bye")
	// Keep zLoopJoin reachable without changing the expected output. The call
	// is intentionally guarded by a run-time false predicate so its cleanup
	// graph remains part of this compiler regression fixture.
	if f("") {
		zLoopJoin()
	}
}

// zLoopJoin preserves the former nextblock regression exactly: an empty range
// before and after registration must not let block[0] movement bypass cleanup.
// Both loop backedges retain preemption polls, and the deferred print remains a
// structured cleanup child after those loops.
// CHECK-LABEL: define ptr @"main.zLoopJoin$coro"(ptr %0, ptr %1){{.*}} {
// CHECK-NOT: GetThreadDefer
// CHECK-NOT: SetThreadDefer
// CHECK: call i1 @__llgo_coro_preempt_poll_v1(ptr %0)
// CHECK: call i1 @__llgo_coro_preempt_poll_v1(ptr %0)
// CHECK: [[LOOP_PRINT:%[0-9]+]] = call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: [[LOOP_ALIAS:%[0-9]+]] = call ptr @__llgo_coro_recover_alias_begin_v1(ptr %0, ptr null, ptr [[LOOP_PRINT]], i1 %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[LOOP_PRINT]],
// CHECK: call void @__llgo_coro_recover_alias_end_v1(ptr %0, ptr [[LOOP_ALIAS]])
func zLoopJoin() {
	values := []int{}
	for range values {
	}
	defer println("loop-join")
	for range values {
	}
}
