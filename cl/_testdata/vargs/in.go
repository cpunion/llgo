// LITTEST
// Scope: common
package main

import _ "unsafe"

//go:linkname cstr llgo.cstr
func cstr(string) *int8

//go:linkname printf C.printf
func printf(format *int8, __llgo_va_list ...any) int32

func main() {
	test(1, 2, 3)
}

func test(a ...any) {
	for _, v := range a {
		printf(cstr("%d\n"), v.(int))
	}
}

// Keep this check focused on the variadic slice, interface boxing, and
// assertion-to-C-varargs data flow. Bounds-loop details are owned elsewhere.
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK-NOT: call ptr @"{{.*}}Alloc
// CHECK: store [3 x %"{{.*}}eface"] zeroinitializer, ptr [[ARGS:%[0-9]+]]
// CHECK: store %"{{.*}}eface" { ptr @_llgo_int, ptr @{{[0-9]+}} }, ptr
// CHECK: store %"{{.*}}eface" { ptr @_llgo_int, ptr @{{[0-9]+}} }, ptr
// CHECK: store %"{{.*}}eface" { ptr @_llgo_int, ptr @{{[0-9]+}} }, ptr
// CHECK: [[SLICE_LEN:%[0-9]+]] = insertvalue %"{{.*}}Slice" {{.*}}, i64 3, 1
// CHECK-NEXT: [[SLICE:%[0-9]+]] = insertvalue %"{{.*}}Slice" [[SLICE_LEN]], i64 3, 2
// CHECK: call ptr @"main.test$coro"({{.*}}%"{{.*}}Slice" [[SLICE]])

// CHECK-LABEL: define ptr @"main.test$coro"(
// CHECK: [[LOOP_INDEX:%[0-9]+]] = phi i64
// CHECK: [[LEN:%[0-9]+]] = extractvalue %"{{.*}}Slice" %2, 1
// CHECK: [[INDEX:%[0-9]+]] = add i64 [[LOOP_INDEX]], 1
// CHECK: [[DATA_PTR:%[0-9]+]] = extractvalue %"{{.*}}Slice" %2, 0
// CHECK-NEXT: [[SLOT:%[0-9]+]] = getelementptr inbounds %"{{.*}}eface", ptr [[DATA_PTR]], i64 [[INDEX]]
// CHECK-NEXT: [[VALUE:%[0-9]+]] = load %"{{.*}}eface", ptr [[SLOT]]
// CHECK-NEXT: [[TYPE:%[0-9]+]] = extractvalue %"{{.*}}eface" [[VALUE]], 0
// CHECK-NEXT: [[IS_INT:%[0-9]+]] = icmp eq ptr [[TYPE]], @_llgo_int
// CHECK: [[DATA:%[0-9]+]] = extractvalue %"{{.*}}eface" [[VALUE]], 1
// CHECK-NEXT: [[INT:%[0-9]+]] = load i64, ptr [[DATA]]
// CHECK: call ptr @"{{.*}}PanicTypeAssert$coro"({{.*}}ptr [[TYPE]], ptr @_llgo_int)
// CHECK: call i32 @__llgo_coro_os_thread_foreign_call_v1({{.*}}ptrtoint (ptr @printf to i64)
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}ptrtoint (ptr @printf to i64)
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_foreign_thunk_v1_
// CHECK: call i32 (ptr, ...) @printf(ptr {{%[0-9]+}}, i64 {{%[0-9]+}})
