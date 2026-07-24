// LITTEST
package main

/*
#include "in.h"
*/
import "C"
import "fmt"

// The generated adapter remains one synchronous raw/native-stack body.
//
// CHECK-LABEL: define i32 @"{{.*}}/cl/_testgo/cgocfiles._Cfunc_test_structs"
// CHECK: call i32 %
//
// The managed main is stackless and publishes the typed five-pointer record
// to a bounded worker instead of executing the C body on the executor.
//
// CHECK-LABEL: define ptr @"{{.*}}/cl/_testgo/cgocfiles.init$coro"
// CHECK: call token @llvm.coro.id
// CHECK-LABEL: define ptr @"{{.*}}/cl/_testgo/cgocfiles.main$coro"
// CHECK: call token @llvm.coro.id
// CHECK: call void @__llgo_coro_worker_park_v1
// CHECK: ptr @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call i8 @llvm.coro.suspend
// CHECK: call i32 @__llgo_coro_worker_resume_v1
//
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call i32 @"{{.*}}/cl/_testgo/cgocfiles._Cfunc_test_structs"
func main() {
	r := C.test_structs(
		&C.s4{a: 1},
		&C.s8{a: 1, b: 2},
		&C.s12{a: 1, b: 2, c: 3},
		&C.s16{a: 1, b: 2, c: 3, d: 4},
		&C.s20{a: 1, b: 2, c: 3, d: 4, e: 5},
	)
	fmt.Println(r)
	if r != 35 {
		panic("test_structs failed")
	}
}
