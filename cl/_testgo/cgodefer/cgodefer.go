// LITTEST
// Scope: arch (arm64/amd64 closure/defer calling convention)
package main

/*
#include <stdlib.h>
#include "callback.h"

typedef int (*callback_t)(int);
extern int go_callback(int);
extern int go_callback_c_only(int);

static int invoke_callback(callback_t callback, int value) {
	return callback(value);
}
static int invoke_export_only(int value) {
	return go_callback_c_only(value);
}
*/
import "C"

// CHECK-LABEL: define [0 x i8] @main._Cfunc_free(ptr %0){{.*}} {
// CHECK: [[FREE_SLOT:%[0-9]+]] = load ptr, ptr @main._cgo_{{.*}}_Cfunc_free
// CHECK-NEXT: [[FREE_FN:%[0-9]+]] = load ptr, ptr [[FREE_SLOT]]
// CHECK-NEXT: [[FREE_RESULT:%[0-9]+]] = call [0 x i8] [[FREE_FN]](ptr %0)
// CHECK-NEXT: ret [0 x i8] [[FREE_RESULT]]

// Exported Go callbacks keep their ordinary managed bodies, while their public
// C symbols are exact ingress adapters. go_callback_c_only is deliberately
// never referenced by Go: invoke_export_only is its sole caller.
// CHECK-LABEL: define i32 @"go_callback$managed"(i32 %0){{.*}} {
// CHECK: [[GO_CONTEXT:%[0-9]+]] = alloca %"{{.*}}LocalContext"
// CHECK: [[GO_TOKEN:%[0-9]+]] = call ptr @"{{.*}}EnterLocalContext"(ptr [[GO_CONTEXT]])
// CHECK: [[GO_RESULT:%[0-9]+]] = add i32 %0, 2
// CHECK: call void @"{{.*}}LeaveLocalContext"(ptr [[GO_CONTEXT]], ptr [[GO_TOKEN]])
// CHECK: ret i32 [[GO_RESULT]]

// CHECK-LABEL: define i32 @go_callback(i32 %0){{.*}} {
// CHECK: call ptr @__llgo_coro_foreign_reentry_acquire_v1
// CHECK: call i32 @__llgo_coro_foreign_reentry_run_v1
// CHECK: call void @__llgo_coro_foreign_reentry_failure_v1

// CHECK-LABEL: define i32 @"go_callback_c_only$managed"(i32 %0){{.*}} {
// CHECK: [[ONLY_CONTEXT:%[0-9]+]] = alloca %"{{.*}}LocalContext"
// CHECK: [[ONLY_TOKEN:%[0-9]+]] = call ptr @"{{.*}}EnterLocalContext"(ptr [[ONLY_CONTEXT]])
// CHECK: [[ONLY_RESULT:%[0-9]+]] = add i32 %0, 2
// CHECK: call void @"{{.*}}LeaveLocalContext"(ptr [[ONLY_CONTEXT]], ptr [[ONLY_TOKEN]])
// CHECK: ret i32 [[ONLY_RESULT]]

// CHECK-LABEL: define i32 @go_callback_c_only(i32 %0){{.*}} {
// CHECK: call ptr @__llgo_coro_foreign_reentry_acquire_v1
// CHECK: call i32 @__llgo_coro_foreign_reentry_run_v1
// CHECK: call void @__llgo_coro_foreign_reentry_failure_v1

// main covers Go and C callback pointers, the C-only exported callback, and a
// deferred C free. Every unproved generated-cgo call uses the outcome-aware
// same-M boundary; the package-level test also rejects worker fallback.
// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call i32 @__llgo_coro_same_m_foreign_reentry_call_v2
// CHECK: load ptr, ptr @main._Cfpvar_fp_c_callback
// CHECK: call ptr @main._Cgo_ptr
// CHECK: call i32 @__llgo_coro_same_m_foreign_reentry_call_v2
// CHECK: load ptr, ptr @main._Cfpvar_fp_go_callback
// CHECK: call ptr @main._Cgo_ptr
// CHECK: call i32 @__llgo_coro_same_m_foreign_reentry_call_v2

// ARM64-LABEL: define ptr @"main.main$1$1$coro"(ptr %0, ptr %1, ptr swiftself %2){{.*}} {
// AMD64-LABEL: define ptr @"main.main$1$1$coro"(ptr %0, ptr %1, ptr nest %2){{.*}} {
// CHECK: call i32 @__llgo_coro_same_m_foreign_reentry_call_v2

//export go_callback
func go_callback(value C.int) C.int {
	return value + 2
}

//export go_callback_c_only
func go_callback_c_only(value C.int) C.int {
	return value + 2
}

func main() {
	memory := C.malloc(64)
	defer C.free(memory)

	if C.invoke_callback((C.callback_t)(C.go_callback), 40) != 42 {
		panic("Go callback")
	}
	if C.invoke_callback((C.callback_t)(C.c_callback), 39) != 42 {
		panic("C callback")
	}
	if C.invoke_export_only(40) != 42 {
		panic("C-only exported callback")
	}
}
