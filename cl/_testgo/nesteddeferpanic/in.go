// LITTEST
package main

func replacement() {
	defer func() {
		println("replacement", recover().(int))
	}()

	func() {
		for {
			defer func() {
				defer panic(5)
			}()
			break
		}
		panic(4)
	}()
}

func resumeOuterPanic() {
	defer func() {
		println("resume outer", recover().(int))
	}()

	func() {
		defer func() {
			defer func() {
				println("resume inner", recover().(int))
			}()
			panic(2)
		}()
		panic(1)
	}()
}

func recoverThenPanic() {
	defer func() {
		println("recover-then-panic outer", recover().(int))
	}()

	func() {
		defer func() {
			old := recover().(int)
			defer panic(3)
			println("recover-then-panic old", old)
		}()
		panic(2)
	}()
}

func main() {
	replacement()
	resumeOuterPanic()
	recoverThenPanic()
}

// The source functions and every nested cleanup are stackless activations.
// These checks pin the three parent/child transactions and the exact nested
// cleanup sites that consume or replace a panic. expect.txt verifies the full
// replacement/resumption order and payload values.
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK-DAG: call ptr @"main.replacement$coro"(
// CHECK-DAG: call ptr @"main.resumeOuterPanic$coro"(
// CHECK-DAG: call ptr @"main.recoverThenPanic$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(

// CHECK-LABEL: define ptr @"main.recoverThenPanic$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.recoverThenPanic$1$coro"(
// CHECK-DAG: call void @__llgo_coro_recover_take_v1(
// CHECK-DAG: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.recoverThenPanic$2$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.recoverThenPanic$2$1$coro"(
// CHECK-DAG: call void @__llgo_coro_recover_take_v1(
// CHECK-DAG: call void @__llgo_coro_panic_prepare_v1(

// CHECK-LABEL: define ptr @"main.replacement$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.replacement$1$coro"(
// CHECK-DAG: call void @__llgo_coro_recover_take_v1(
// CHECK-DAG: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.replacement$2$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.replacement$2$1$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(

// CHECK-LABEL: define ptr @"main.resumeOuterPanic$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.resumeOuterPanic$1$coro"(
// CHECK-DAG: call void @__llgo_coro_recover_take_v1(
// CHECK-DAG: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.resumeOuterPanic$2$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.resumeOuterPanic$2$1$coro"(
// CHECK: call void @__llgo_coro_panic_prepare_v1(
// CHECK-LABEL: define ptr @"main.resumeOuterPanic$2$1$1$coro"(
// CHECK-DAG: call void @__llgo_coro_recover_take_v1(
// CHECK-DAG: call void @__llgo_coro_panic_prepare_v1(
