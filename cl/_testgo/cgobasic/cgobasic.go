// LITTEST
package main

/*
#include <stdio.h>
#include <stdlib.h>
#include <math.h>
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// Generated C adapters remain synchronous raw/native-stack entries.
//
// CHECK-LABEL: define double @"{{.*}}/cl/_testgo/cgobasic._Cfunc_cos"
// CHECK: call double %
// CHECK-LABEL: define [0 x i8] @"{{.*}}/cl/_testgo/cgobasic._Cfunc_free"
// CHECK: call [0 x i8] %
// CHECK-LABEL: define double @"{{.*}}/cl/_testgo/cgobasic._Cfunc_sqrt"
// CHECK: call double %
//
// Package initialization and source main use the default stackless ABI; C
// calls park the logical task and execute through exact typed worker thunks.
//
// CHECK-LABEL: define ptr @"{{.*}}/cl/_testgo/cgobasic.init$coro"
// CHECK: call token @llvm.coro.id
// CHECK: call i8 @llvm.coro.suspend
// CHECK-LABEL: define ptr @"{{.*}}/cl/_testgo/cgobasic.main$coro"
// CHECK: call token @llvm.coro.id
// CHECK: call void @__llgo_coro_worker_park_v1
// CHECK: ptr @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call i8 @llvm.coro.suspend
// CHECK: call i32 @__llgo_coro_worker_resume_v1
//
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call i32 @"{{.*}}/cl/_testgo/cgobasic._Cfunc_puts"
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call double @"{{.*}}/cl/_testgo/cgobasic._Cfunc_sqrt"
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call double @"{{.*}}/cl/_testgo/cgobasic._Cfunc_sin"
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call double @"{{.*}}/cl/_testgo/cgobasic._Cfunc_cos"
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call double @"{{.*}}/cl/_testgo/cgobasic._Cfunc_log"
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call [0 x i8] @"{{.*}}/cl/_testgo/cgobasic._Cfunc_free"
func main() {
	// C.CString example
	cstr := C.CString("Hello, World!")
	C.puts(cstr)

	// C.CBytes example
	bytes := []byte{65, 66, 67, 68} // ABCD
	cbytes := C.CBytes(bytes)

	// C.GoString example
	gostr := C.GoString(cstr)
	println("Converted back to Go string: ", gostr)

	// C.GoStringN example (with length limit)
	gostringN := C.GoStringN(cstr, 5) // only take first 5 characters
	println("Length-limited string: ", gostringN)

	// C.GoBytes example
	gobytes := C.GoBytes(cbytes, 4) // 4 is the length
	println("Converted back to Go byte slice: ", gobytes)

	// C math library examples
	x := 2.0
	sqrtResult := C.sqrt(C.double(x))
	fmt.Printf("sqrt(%v) = %v\n", x, float64(sqrtResult))

	sinResult := C.sin(C.double(x))
	fmt.Printf("sin(%v) = %v\n", x, float64(sinResult))

	cosResult := C.cos(C.double(x))
	fmt.Printf("cos(%v) = %v\n", x, float64(cosResult))

	logResult := C.log(C.double(x))
	fmt.Printf("log(%v) = %v\n", x, float64(logResult))

	C.free(unsafe.Pointer(cstr))
	C.free(cbytes)
}
