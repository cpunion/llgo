// LITTEST
package main

/*
#include <stdlib.h>
*/
import "C"

// Go 1.26 gives C.malloc a real Go wrapper around _cgo_cmalloc. Both it and
// the ordinary free adapter remain native-stack bodies: managed callers park
// through typed bounded-worker transactions instead of executing C on an
// executor.
//
// CHECK-LABEL: define ptr @main._Cfunc__CMalloc
// CHECK: call ptr @main._cgo_cmalloc
// CHECK-LABEL: define [0 x i8] @main._Cfunc_free
//
// CHECK-LABEL: define ptr @"main.main$coro"
// CHECK: call token @llvm.coro.id
// CHECK: call void @__llgo_coro_worker_park_v1
// CHECK: ptr @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call i8 @llvm.coro.suspend
//
// CHECK-LABEL: define ptr @"main.main$1$1$coro"
// CHECK: call void @__llgo_coro_worker_park_v1
// CHECK: ptr @__llgo_coro_worker_cgo_thunk_v1_
//
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call ptr @main._Cfunc__CMalloc
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_cgo_thunk_v1_
// CHECK: call [0 x i8] @main._Cfunc_free
func main() {
	p := C.malloc(1024)
	defer C.free(p)
}
