// LITTEST
package main

import (
	"github.com/goplus/lib/c"
	"github.com/goplus/lib/c/math/cmplx"
)

func f(c, z complex64, addr c.Pointer) {
	println("addr:", addr)
	println("abs(3+4i):", cmplx.Absf(c))
	println("real(3+4i):", real(z))
	println("imag(3+4i):", imag(z))
}

func main() {
	re := float32(3.0)
	im := float32(4.0)
	z := complex64(3 + 4i)
	x := complex(re, im)
	f(x, z, c.Func(f))
}

// The C callback address keeps the raw synchronous entry while the managed
// call path awaits the automatically coloured coroutine variant.
// CHECK-LABEL: define ptr @"main.f$coro"(
// CHECK: call void @__llgo_coro_worker_park_v1
// CHECK-LABEL: define void @main.f(
// CHECK: call float @cabsf(
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: store ptr @main.f
// CHECK: call ptr @"main.f$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3
