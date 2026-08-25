// LITTEST
package main

import (
	"math"
)

// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	// Each math result must flow unchanged to the corresponding print.
	// CHECK: [[SQRT:%[0-9]+]] = call ptr @"math.sqrt$coro"({{.*}}double 2.000000e+00)
	// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[SQRT]]
	// CHECK: [[PRINT:%[0-9]+]] = call ptr @"{{.*}}PrintFloat$coro"
	// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[PRINT]]
	// CHECK: call void @"math.Abs$outcome"({{.*}}double -1.200000e+00)
	// CHECK: call void @"math.Ldexp$outcome"({{.*}}double 1.200000e+00, i64 3)
	println(math.Sqrt(2))
	println(math.Abs(-1.2))
	println(math.Ldexp(1.2, 3))
}
