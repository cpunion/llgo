// LITTEST
// Scope: common
package main

import "unsafe"

// Keep these thin declarations local: this fixture owns LLGo's C-string and
// stack-allocation intrinsics, not github.com/goplus/lib's wrappers.

//go:linkname alloca llgo.alloca
func alloca(size uintptr) unsafe.Pointer

//go:linkname allocaCStr llgo.allocaCStr
func allocaCStr(s string) *int8

//go:linkname allocaCStrs llgo.allocaCStrs
func allocaCStrs(strs []string, endWithNil bool) **int8

//go:linkname cstr llgo.cstr
func cstr(s string) *int8

//go:linkname memcpy C.memcpy
func memcpy(dst, src unsafe.Pointer, n uintptr) unsafe.Pointer

//go:linkname strlen C.strlen
func strlen(str *int8) uintptr

//go:linkname printf C.printf
func printf(format *int8, __llgo_va_list ...any) int32

var strlenInput = [...]int8{'a', 'b', 'c', 0}

// CHECK: [[HELLO_TEXT:@[0-9]+]] = private unnamed_addr constant [12 x i8] c"Hello world\0A"
// CHECK-LABEL: define %"{{.*}}/runtime/internal/runtime.String" @main.hello(){{.*}} {
// CHECK: ret %"{{.*}}/runtime/internal/runtime.String" { ptr [[HELLO_TEXT]], i64 12 }
func hello() string {
	return "Hello world\n"
}

// One bounded case owns the four related intrinsic shapes: dynamic C-string
// copy, constant C string, raw allocation, and a null-terminated C-string
// vector. Values that survive a suspension live in the coroutine frame/heap;
// potentially blocking C calls must be represented by worker parks.
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call token @llvm.coro.id
// CHECK: [[TEXT:%[0-9]+]] = call %"{{.*}}String" @main.hello()
// CHECK: [[TEXT_LEN:%[0-9]+]] = extractvalue %"{{.*}}String" [[TEXT]], 1
// CHECK: [[CSTR_LEN:%[0-9]+]] = add i64 [[TEXT_LEN]], 1
// CHECK: [[CSTR_BUF:%[0-9]+]] = call ptr @"{{.*}}/runtime/internal/runtime.AllocU"(i64 [[CSTR_LEN]])
// CHECK: call void @"{{.*}}/runtime/internal/runtime.CStrCopy$outcome"({{.*}}ptr [[CSTR_BUF]], %"{{.*}}String" [[TEXT]])
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}i64 ptrtoint (ptr @printf to i64)
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}i64 ptrtoint (ptr @memcpy to i64)
// CHECK: [[SLICE_LEN:%[0-9]+]] = extractvalue %"{{.*}}Slice" %{{[0-9]+}}, 1
// CHECK: [[VECTOR_LEN:%[0-9]+]] = add i64 [[SLICE_LEN]], 1
// CHECK: call void @"{{.*}}/runtime/internal/runtime.CStrCopy$outcome"
// CHECK: store ptr null, ptr %{{[0-9]+}}
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}i64 ptrtoint (ptr @strlen to i64)
func main() {
	printf(allocaCStr(hello()))
	printf(cstr("Hello, world\n"))

	buf := alloca(4)
	memcpy(buf, unsafe.Pointer(cstr("Hi\n")), 4)
	printf(cstr("%s"), buf)

	values := allocaCStrs([]string{"a", "b", "c"}, true)
	items := unsafe.Slice(values, 4)
	for _, item := range items {
		if item == nil {
			break
		}
		printf(cstr("%s\n"), item)
	}

	if strlen(&strlenInput[0]) != 3 {
		panic("unexpected C string length")
	}
	// ESP-IDF's compact printf does not implement the C99 %zu modifier.
	printf(cstr("Length 3\n"))
}
