// LITTEST
package main

/*
#cgo pkg-config: python3-embed
#include <Python.h>
*/
import "C"

import "runtime"

// Generated C adapters remain synchronous native-stack entries. Their managed
// caller chooses the physical execution domain.
//
// CHECK-LABEL: define i32 @"{{.*}}/cl/_testgo/cgopython._Cfunc_PyRun_SimpleString"
// CHECK: call i32 %
// CHECK-LABEL: define [0 x i8] @"{{.*}}/cl/_testgo/cgopython._Cfunc_Py_Finalize"
// CHECK: call [0 x i8] %
// CHECK-LABEL: define [0 x i8] @"{{.*}}/cl/_testgo/cgopython._Cfunc_Py_Initialize"
// CHECK: call [0 x i8] %
//
// The source main keeps standard synchronous Go syntax. LockOSThread is an
// ordinary managed call; every potentially blocking C call then branches on
// the exact current-G affinity state. The locked edge invokes the foreign
// thunk on this executor M, while the unlocked edge retains the normal bounded
// worker transaction.
//
// CHECK-LABEL: define ptr @"{{.*}}/cl/_testgo/cgopython.main$coro"
// CHECK: call token @llvm.coro.id
// CHECK: call ptr @"runtime.LockOSThread$coro"
// CHECK: call i1 @__llgo_coro_os_thread_locked_v1
// CHECK: call void @__llgo_coro_os_thread_foreign_call_v1
// CHECK: call void @__llgo_coro_worker_park_v1
// CHECK: call i32 @__llgo_coro_worker_resume_v1

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	C.Py_Initialize()
	defer C.Py_Finalize()
	C.PyRun_SimpleString(C.CString("print('Hello, Python!')"))
}
