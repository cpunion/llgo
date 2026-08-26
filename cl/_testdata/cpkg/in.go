// LITTEST
package C

// The managed package stage retains the plain add export and the declarative
// coroutine/export summary. The versioned synchronous C ingress adapter is a
// separate whole-program capability gate.
// CHECK: {{^}}@llvm.compiler.used = appending global [2 x ptr] [ptr @add, ptr @__llgo_coro_library_effect_v11.{{[0-9a-f]+}}], section "llvm.metadata"{{$}}
// CHECK: {{^}}@llvm.used = appending global [1 x ptr] [ptr @add], section "llvm.metadata"{{$}}

// CHECK-LABEL: define void @"Double$outcome"(ptr %0, ptr %1, ptr %2, double %3){{.*}} {
// CHECK: [[DOUBLE_RESULT:%[0-9]+]] = fmul double 2.000000e+00, %3
// CHECK: store double [[DOUBLE_RESULT]], ptr %{{[0-9]+}}, align 8
// CHECK: store i32 1, ptr %{{[0-9]+}}, align 4
// CHECK-NEXT: ret void
func Double(x float64) float64 {
	return 2 * x
}

// CHECK-LABEL: define i64 @add(i64 %0, i64 %1){{.*}} {
// CHECK: [[XADD_RESULT:%[0-9]+]] = call i64 @"{{.*}}.add"(i64 %0, i64 %1)
// CHECK-NEXT: ret i64 [[XADD_RESULT]]
func Xadd(a, b int) int {
	return add(a, b)
}

// CHECK-LABEL: define i64 @"{{.*}}.add"(i64 %0, i64 %1){{.*}} {
// CHECK: [[ADD_RESULT:%[0-9]+]] = add i64 %0, %1
// CHECK-NEXT: ret i64 [[ADD_RESULT]]
func add(a, b int) int {
	return a + b
}
