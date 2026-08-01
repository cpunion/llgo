// LITTEST
package main

import (
	"math"
)

// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	// CHECK: [[SQRT:%[0-9]+]] = call ptr @"math.Sqrt$coro"({{.*}}double 2.000000e+00)
	// CHECK: call void @__llgo_coro_await_prepare_v3({{.*}}ptr [[SQRT]]
	// CHECK: call ptr @"math.Abs$coro"({{.*}}double -1.200000e+00)
	// CHECK: call ptr @"math.Ldexp$coro"({{.*}}double 1.200000e+00, i64 3)
	println(math.Sqrt(2))
	println(math.Abs(-1.2))
	println(math.Ldexp(1.2, 3))
}
