// LITTEST darwin/arm64 linux/amd64
// Scope: arch (arm64/amd64 closure-call ABI; aggregate results otherwise common)
package main

var a int = 1

// Returning a closure inside an aggregate must preserve both function words;
// this is the historical result/funcPtr regression owner.
// CHECK-LABEL: define { { ptr, ptr }, i64 } @main.closureResult(){{.*}} {
// CHECK: ret { { ptr, ptr }, i64 } { { ptr, ptr } { ptr @__llgo_coro_func_descriptor_v2.{{[^,]+}}, ptr null }, i64 1 }
// CHECK-LABEL: define i64 @"main.closureResult$1"(i64 %0){{.*}} {
// CHECK: [[CLOSURE_RESULT:%[0-9]+]] = add i64 %0, 1
// CHECK-NEXT: ret i64 [[CLOSURE_RESULT]]
func closureResult() (func(int) int, int) {
	return func(v int) int { return v + 1 }, 1
}

// CHECK-LABEL: define { i64, double } @main.foo(double %0){{.*}} {
// CHECK: [[FOO_INT:%[0-9]+]] = load i64, ptr @main.a
// CHECK-NEXT: [[FOO_PAIR0:%[0-9]+]] = insertvalue { i64, double } undef, i64 [[FOO_INT]], 0
// CHECK-NEXT: [[FOO_PAIR:%[0-9]+]] = insertvalue { i64, double } [[FOO_PAIR0]], double %0, 1
// CHECK-NEXT: ret { i64, double } [[FOO_PAIR]]
func foo(f float64) (int, float64) {
	return a, f
}

func main() {
	// CHECK-LABEL: define ptr @"main.main$coro"(
	// CHECK: [[FOO_RESULT:%[0-9]+]] = call { i64, double } @main.foo(double 2.000000e+00)
	// CHECK-NEXT: [[MAIN_INT:%[0-9]+]] = extractvalue { i64, double } [[FOO_RESULT]], 0
	// CHECK-NEXT: [[MAIN_FLOAT:%[0-9]+]] = extractvalue { i64, double } [[FOO_RESULT]], 1
	// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.PrintBatchV1$coro"
	// CHECK: [[CLOSURE_PAIR:%[0-9]+]] = call { { ptr, ptr }, i64 } @main.closureResult()
		// CHECK-NEXT: [[CLOSURE_FN:%[0-9]+]] = extractvalue { { ptr, ptr }, i64 } [[CLOSURE_PAIR]], 0
		// CHECK-NEXT: [[CLOSURE_N:%[0-9]+]] = extractvalue { { ptr, ptr }, i64 } [[CLOSURE_PAIR]], 1
		// CHECK: [[CLOSURE_DESCRIPTOR:%[0-9]+]] = extractvalue { ptr, ptr } [[CLOSURE_FN]], 0
		// CHECK: [[DYNAMIC_DESCRIPTOR:%[0-9]+]] = extractvalue { ptr, ptr } [[CLOSURE_FN]], 0
		// CHECK-NEXT: [[CLOSURE_ENV:%[0-9]+]] = extractvalue { ptr, ptr } [[CLOSURE_FN]], 1
		// CHECK-NEXT: load { i32, i32, i64, i64, ptr, ptr, i64, i64, ptr }, ptr [[DYNAMIC_DESCRIPTOR]]
	// CHECK: call ptr %{{[0-9]+}}({{.*}}ptr [[CLOSURE_ENV]], i64 41)
		// CHECK: call i64 %{{[0-9]+}}(ptr [[CLOSURE_ENV]], i64 41)
		// CHECK: [[DYNAMIC_RESULT:%[0-9]+]] = load i64, ptr %{{[0-9]+}}
		// CHECK: [[BAD_CLOSURE_RESULT:%[0-9]+]] = icmp ne i64 [[DYNAMIC_RESULT]], 42
	i, f := foo(2.0)
	println(i, f)
	fn, n := closureResult()
	if fn(41) != 42 || n != 1 {
		panic("bad closure result")
	}
}
