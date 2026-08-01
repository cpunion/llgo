// LITTEST
package main

/*
#cgo pkg-config: python3-embed
#include <stdio.h>
#include <Python.h>

void test_stdout() {
	printf("stdout ptr: %p\n", stdout);
	fputs("outputs to stdout\n", stdout);
}
*/
import "C"
import (
	"unsafe"

	"github.com/goplus/lib/c"
)

// Generated cgo functions and macros remain synchronous raw/native-stack
// adapters. Managed source calls, including the deferred Py_Finalize call,
// execute them only through typed bounded-worker thunks.
//
// CHECK-LABEL: define [0 x i8] @main._Cfunc_Py_Finalize
// CHECK: call [0 x i8] %
// CHECK-LABEL: define ptr @main._Cmacro_stdout
//
// Package initialization and main use the default stackless ABI.
//
// CHECK-LABEL: define ptr @"main.init$coro"
// CHECK: call token @llvm.coro.id
// CHECK: call i8 @llvm.coro.suspend
//
// CHECK-LABEL: define ptr @"main.main$coro"
// CHECK: call token @llvm.coro.id
// CHECK: call void @__llgo_coro_worker_park_v1
// CHECK: ptr @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call i8 @llvm.coro.suspend
// CHECK: call i32 @__llgo_coro_worker_resume_v1
//
// The generated thunk fleet preserves exact raw adapter identities. In
// particular, deferred Py_Finalize has its own synchronous thunk; it is not
// called directly from the executor or recovered from an untyped function
// address at runtime.
//
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call [0 x i8] @main._Cfunc_test_stdout
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call [0 x i8] @main._Cfunc_Py_Initialize
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call [0 x i8] @main._Cfunc_Py_Finalize
func main() {
	C.test_stdout()
	C.fputs((*C.char)(unsafe.Pointer(c.Str("hello\n"))), C.stdout)
	C.Py_Initialize()
	defer C.Py_Finalize()
	C.PyObject_Print(C.Py_True, C.stdout, 0)
	C.fputs((*C.char)(unsafe.Pointer(c.Str("\n"))), C.stdout)
	C.PyObject_Print(C.Py_False, C.stdout, 0)
	C.fputs((*C.char)(unsafe.Pointer(c.Str("\n"))), C.stdout)
	C.PyObject_Print(C.Py_None, C.stdout, 0)
	C.fputs((*C.char)(unsafe.Pointer(c.Str("\n"))), C.stdout)
}
