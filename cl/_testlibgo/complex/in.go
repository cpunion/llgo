// LITTEST
package main

import (
	"math/cmplx"
)

// CHECK-LABEL: define ptr @"main.f$coro"(
func f(c, z complex128) {
	// CHECK: [[ABS:%[0-9]+]] = call ptr @"math/cmplx.Abs$coro"
	// CHECK: call void @__llgo_coro_await_prepare_v3({{.*}}ptr [[ABS]]
	println("abs(3+4i):", cmplx.Abs(c))
	// CHECK: extractvalue { double, double } %3, 0
	// CHECK: extractvalue { double, double } %3, 1
	println("real(3+4i):", real(z))
	println("imag(3+4i):", imag(z))
}

// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	re := 3.0
	im := 4.0
	z := 3 + 4i
	c := complex(re, im)
	// CHECK: call ptr @"main.f$coro"({{.*}}{ double, double } { double 3.000000e+00, double 4.000000e+00 }, { double, double } { double 3.000000e+00, double 4.000000e+00 })
	f(c, z)
}
