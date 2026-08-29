// LITTEST
package main

/*
#cgo pkg-config: python3-embed
#ifdef _WIN32
// Keep the Windows declaration surface local so cross-target IR tests do not
// depend on Windows Python SDK headers. Native runs still obtain the Python
// import library from python3-embed above.
void Py_Initialize(void);
void Py_Finalize(void);
int PyRun_SimpleString(const char *command);
#else
#include <Python.h>
#endif
*/
import "C"

import "runtime"

// Generated C adapters remain synchronous native-stack entries. Each adapter
// performs exactly one typed call through its cgo slot; worker and coroutine
// dispatch belongs to the managed caller and is not duplicated here.
//
// CHECK-LABEL: define i32 @main._Cfunc_PyRun_SimpleString(ptr %0){{.*}} {
// CHECK: [[RUN_SLOT:%[0-9]+]] = load ptr, ptr @main._cgo_{{.*}}_Cfunc_PyRun_SimpleString
// CHECK-NEXT: [[RUN_TARGET:%[0-9]+]] = load ptr, ptr [[RUN_SLOT]]
// CHECK-NEXT: [[RUN_RESULT:%[0-9]+]] = call i32 [[RUN_TARGET]](ptr %0)
// CHECK-NEXT: ret i32 [[RUN_RESULT]]
// CHECK-LABEL: define [0 x i8] @main._Cfunc_Py_Finalize(){{.*}} {
// CHECK: [[FINALIZE_SLOT:%[0-9]+]] = load ptr, ptr @main._cgo_{{.*}}_Cfunc_Py_Finalize
// CHECK-NEXT: [[FINALIZE_TARGET:%[0-9]+]] = load ptr, ptr [[FINALIZE_SLOT]]
// CHECK-NEXT: [[FINALIZE_RESULT:%[0-9]+]] = call [0 x i8] [[FINALIZE_TARGET]]()
// CHECK-NEXT: ret [0 x i8] [[FINALIZE_RESULT]]
// CHECK-LABEL: define [0 x i8] @main._Cfunc_Py_Initialize(){{.*}} {
// CHECK: [[INIT_SLOT:%[0-9]+]] = load ptr, ptr @main._cgo_{{.*}}_Cfunc_Py_Initialize
// CHECK-NEXT: [[INIT_TARGET:%[0-9]+]] = load ptr, ptr [[INIT_SLOT]]
// CHECK-NEXT: [[INIT_RESULT:%[0-9]+]] = call [0 x i8] [[INIT_TARGET]]()
// CHECK-NEXT: ret [0 x i8] [[INIT_RESULT]]

// The source keeps normal synchronous Go syntax. LockOSThread is a managed
// call; every potentially blocking C call branches on the exact current-G
// affinity state. The locked edge invokes the foreign thunk on this executor
// M, while the unlocked edge retains the bounded worker transaction.
//
// CHECK-LABEL: define ptr @"main.main$coro"
// CHECK: call token @llvm.coro.id
// CHECK: call ptr @"runtime.LockOSThread$coro"
// CHECK: call i1 @__llgo_coro_os_thread_locked_v1
// CHECK: call i32 @__llgo_coro_os_thread_foreign_call_v1
// CHECK: call void @__llgo_coro_worker_park_v1
// CHECK: call i32 @__llgo_coro_worker_resume_v1

func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	C.Py_Initialize()
	defer C.Py_Finalize()
	C.PyRun_SimpleString(C.CString("print('Hello, Python!')"))
}
