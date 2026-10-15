// LITTEST
// Scope: common
package main

import "unsafe"

//go:linkname funcAddr llgo.funcAddr
func funcAddr(any) unsafe.Pointer

//go:linkname cabsf C.cabsf
func cabsf(complex64) float32

// The managed primary uses a worker for the opaque cabsf boundary. funcAddr
// additionally demands a raw C-callable body, which retains the exact ABI.
// CHECK-LABEL: define ptr @"main.f$coro"(
// CHECK: call i32 @__llgo_coro_os_thread_foreign_call_v1({{.*}}ptrtoint (ptr @cabsf to i64)
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}ptrtoint (ptr @cabsf to i64)
// CHECK-LABEL: define void @main.f({ float, float } %0, { float, float } %1, ptr %2){{.*}} {
// CHECK: call void @"{{.*}}PrintPointer"(ptr %2)
// CHECK: [[ABS32:%[0-9]+]] = call float @cabsf({ float, float } %0)
// CHECK: [[ABS64:%[0-9]+]] = fpext float [[ABS32]] to double
// CHECK-NEXT: call void @"{{.*}}PrintFloat"(double [[ABS64]])
// CHECK: [[REAL32:%[0-9]+]] = extractvalue { float, float } %1, 0
// CHECK: [[REAL64:%[0-9]+]] = fpext float [[REAL32]] to double
// CHECK-NEXT: call void @"{{.*}}PrintFloat"(double [[REAL64]])
// CHECK: [[IMAG32:%[0-9]+]] = extractvalue { float, float } %1, 1
// CHECK: [[IMAG64:%[0-9]+]] = fpext float [[IMAG32]] to double
// CHECK-NEXT: call void @"{{.*}}PrintFloat"(double [[IMAG64]])
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"main.f$coro"(ptr %{{[0-9]+}}, ptr %{{[0-9]+}}, { float, float } { float 3.000000e+00, float 4.000000e+00 }, { float, float } { float 3.000000e+00, float 4.000000e+00 }, ptr @main.f)

func f(c, z complex64, addr unsafe.Pointer) {
	println("addr:", addr)
	println("abs(3+4i):", cabsf(c))
	println("real(3+4i):", real(z))
	println("imag(3+4i):", imag(z))
}

func main() {
	re := float32(3.0)
	im := float32(4.0)
	z := complex64(3 + 4i)
	x := complex(re, im)
	f(x, z, funcAddr(f))
}
