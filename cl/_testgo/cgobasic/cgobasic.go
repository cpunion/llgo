// LITTEST
// Scope: common
package main

/*
#include <stdlib.h>

static int add(int a, int b) { return a + b; }
static int macro_object = 7;
#define TEST_OBJECT (&macro_object)
static int read_object(const int *p) { return *p; }

typedef struct { int value; } wrapped_object;
static wrapped_object *new_wrapped(int value) {
	wrapped_object *object = (wrapped_object *)malloc(sizeof(wrapped_object));
	object->value = value;
	return object;
}
static int read_wrapped(const wrapped_object *object) { return object->value; }
*/
import "C"

import "unsafe"

// The raw C wrapper remains a single symbol-slot forwarder. Managed Go code
// reaches it only through a typed generated-cgo thunk.
// CHECK-LABEL: define i32 @main._Cfunc_add(i32 %0, i32 %1){{.*}} {
// CHECK: [[ADD_SLOT:%[0-9]+]] = load ptr, ptr @main._cgo_{{.*}}_Cfunc_add
// CHECK-NEXT: [[ADD_FN:%[0-9]+]] = load ptr, ptr [[ADD_SLOT]]
// CHECK-NEXT: [[ADD_RESULT:%[0-9]+]] = call i32 [[ADD_FN]](i32 %0, i32 %1)
// CHECK-NEXT: ret i32 [[ADD_RESULT]]

// Conversion helpers use structured outcomes. Unproved generated-cgo calls
// preserve possible C-to-Go ingress through the outcome-aware same-M boundary.
// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call void @"{{.*}}CString$outcome"(
// CHECK: call void @"main.main$1$outcome"(
// CHECK-NOT: _Cgo_use
// CHECK: call i32 @__llgo_coro_same_m_foreign_reentry_call_v2
// CHECK: call i32 @__llgo_coro_same_m_foreign_reentry_call_v2
// CHECK: call i32 @__llgo_coro_same_m_foreign_reentry_call_v2
// CHECK: call i32 @__llgo_coro_same_m_foreign_reentry_call_v2
// CHECK: call i32 @__llgo_coro_same_m_foreign_reentry_call_v2

// CBytes is a bounded outcome helper and therefore needs no coroutine frame.
// CHECK-LABEL: define void @"main.main$1$outcome"(ptr %0, ptr %1, ptr %2){{.*}} {
// CHECK-NOT: llvm.coro.
// CHECK: [[BYTE_LEN:%[0-9]+]] = insertvalue %"{{.*}}Slice" {{.*}}, i64 3, 1
// CHECK-NEXT: [[BYTE_SLICE:%[0-9]+]] = insertvalue %"{{.*}}Slice" [[BYTE_LEN]], i64 3, 2
// CHECK: call void @"{{.*}}CBytes$outcome"({{.*}}%"{{.*}}Slice" [[BYTE_SLICE]])

// GoBytes remains suspend-aware and receives its captured C pointer through the
// architecture's hidden closure-context register.
// CHECK-LABEL: define ptr @"main.main$2$coro"(ptr %0, ptr %1, ptr {{.*}}%2){{.*}} {
// CHECK: call void @"{{.*}}GoBytes$outcome"({{.*}}i64 3)

// The generated thunk is the only place that calls the raw C wrapper.
// CHECK-LABEL: define linkonce i64 @__llgo_coro_generated_cgo_thunk_v2_{{[0-9a-f]+}}(i64 %0){{.*}} {
// CHECK: call i32 @main._Cfunc_add(i32
// CHECK: ret i64 0
func main() {
	text := C.CString("llgo")
	if C.GoString(text) != "llgo" {
		panic("CString/GoString")
	}
	if C.GoStringN(text, 2) != "ll" {
		panic("GoStringN")
	}

	bytes := C.CBytes([]byte{1, 2, 3})
	roundTrip := C.GoBytes(bytes, 3)
	if len(roundTrip) != 3 || roundTrip[0] != 1 || roundTrip[2] != 3 {
		panic("CBytes/GoBytes")
	}

	if C.add(20, 22) != 42 {
		panic("cgo wrapper")
	}
	if C.read_object(C.TEST_OBJECT) != 7 {
		panic("object-like macro")
	}
	if C.read_wrapped((*C.wrapped_object)(unsafe.Pointer(C.new_wrapped(11)))) != 11 {
		panic("C pointer conversion")
	}

	// The conversion helpers allocate C storage. Cleanup mechanics are checked
	// by cgodefer; direct calls here avoid another defer CFG.
	C.free(unsafe.Pointer(text))
	C.free(bytes)
}
