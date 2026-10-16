// LITTEST
// Scope: common
package main

// fail's recovery defer is part of its logical coroutine cleanup frame. The
// panic, ordinary defer, and recovery defer all propagate through structured
// outcomes rather than a thread-local defer chain.
// CHECK-LABEL: define ptr @"main.fail$coro"(ptr %0, ptr %1){{.*}} {
// CHECK-NOT: GetThreadDefer
// CHECK-NOT: SetThreadDefer
// CHECK-NOT: FreeDeferNode
// CHECK: call void @__llgo_coro_panic_prepare_v1(

// The ordinary "bye" defer is a recover-aware cleanup child.
// CHECK: [[BYE_PRINT:%[0-9]+]] = call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: [[BYE_ALIAS:%[0-9]+]] = call ptr @__llgo_coro_recover_alias_begin_v1(ptr %0, ptr null, ptr [[BYE_PRINT]], i1 %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[BYE_PRINT]],
// CHECK: call void @__llgo_coro_recover_alias_end_v1(ptr %0, ptr [[BYE_ALIAS]])

// The recovery closure is another typed coroutine child using the same alias.
// CHECK: [[RECOVER_CALL:%[0-9]+]] = call ptr @"main.fail$1$coro"(ptr %0, ptr %{{[0-9]+}})
// CHECK: [[RECOVER_ALIAS:%[0-9]+]] = call ptr @__llgo_coro_recover_alias_begin_v1(ptr %0, ptr null, ptr [[RECOVER_CALL]], i1 %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[RECOVER_CALL]],
// CHECK: call void @__llgo_coro_recover_alias_end_v1(ptr %0, ptr [[RECOVER_ALIAS]])

// Recover binds to the current logical frame, consumes the explicit panic
// payload, validates the asserted string type, and prints through the event
// aware runtime path.
// CHECK-LABEL: define ptr @"main.fail$1$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call void @"{{.*}}BindRecoverFrame$outcome"({{.*}}ptr @"main.fail$1$coro", ptr %coro.handle)
// CHECK: icmp eq ptr %{{[0-9]+}}, @_llgo_string
// CHECK: call void @__llgo_coro_recover_take_v1(ptr %0, ptr %coro.handle,
// CHECK: [[EQUAL_CALL:%[0-9]+]] = call ptr @"{{.*}}EfaceEqual$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[EQUAL_CALL]],
// CHECK: [[RECOVER_PRINT:%[0-9]+]] = call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[RECOVER_PRINT]],
// CHECK: [[ASSERT_CALL:%[0-9]+]] = call ptr @"{{.*}}PanicTypeAssert$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[ASSERT_CALL]],

// main awaits fail, continues to the reachable print, and then runs its three
// statically registered cleanup paths with logical recover aliases.
// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK-NOT: GetThreadDefer
// CHECK-NOT: SetThreadDefer
// CHECK: [[FAIL_CALL:%[0-9]+]] = call ptr @"main.fail$coro"(ptr %0, ptr %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[FAIL_CALL]],
// CHECK: [[REACHABLE_PRINT:%[0-9]+]] = call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[REACHABLE_PRINT]],

// CHECK: [[MAIN_CLOSURE:%[0-9]+]] = call ptr @"main.main$1$coro"(ptr %0, ptr %{{[0-9]+}})
// CHECK: [[MAIN_CLOSURE_ALIAS:%[0-9]+]] = call ptr @__llgo_coro_recover_alias_begin_v1(ptr %0, ptr null, ptr [[MAIN_CLOSURE]], i1 %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[MAIN_CLOSURE]],
// CHECK: call void @__llgo_coro_recover_alias_end_v1(ptr %0, ptr [[MAIN_CLOSURE_ALIAS]])

// CHECK: [[MAIN_PRINT0:%[0-9]+]] = call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: [[MAIN_ALIAS0:%[0-9]+]] = call ptr @__llgo_coro_recover_alias_begin_v1(ptr %0, ptr null, ptr [[MAIN_PRINT0]], i1 %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[MAIN_PRINT0]],
// CHECK: call void @__llgo_coro_recover_alias_end_v1(ptr %0, ptr [[MAIN_ALIAS0]])

// CHECK: [[MAIN_PRINT1:%[0-9]+]] = call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: [[MAIN_ALIAS1:%[0-9]+]] = call ptr @__llgo_coro_recover_alias_begin_v1(ptr %0, ptr null, ptr [[MAIN_PRINT1]], i1 %{{[0-9]+}})
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[MAIN_PRINT1]],
// CHECK: call void @__llgo_coro_recover_alias_end_v1(ptr %0, ptr [[MAIN_ALIAS1]])

// CHECK-LABEL: define ptr @"main.main$1$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: [[HI_PRINT:%[0-9]+]] = call ptr @"{{.*}}PrintBatchV1$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[HI_PRINT]],

func f(s string) bool {
	return len(s) > 2
}

func fail() {
	defer println("bye")
	defer func() {
		if e := recover(); e != nil {
			println("recover:", e.(string))
		}
	}()
	panic("panic message")
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
	fail()
	println("reachable")
}
