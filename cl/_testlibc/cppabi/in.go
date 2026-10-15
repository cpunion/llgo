// LITTEST
// Scope: common
package main

import "github.com/xgo-dev/llgo/cl/_testlibc/cppabi/local"

// The declaration has no producer-side no-block proof, so its typed ABI is
// preserved in a worker thunk and the Go caller is colored automatically.
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call i32 @__llgo_coro_os_thread_foreign_call_v1({{.*}}ptrtoint (ptr @llgo_test_cpp_add to i64)
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}ptrtoint (ptr @llgo_test_cpp_add to i64)
// CHECK-LABEL: define linkonce i64 @__llgo_coro_worker_foreign_thunk_v1_{{[0-9a-f]+}}(
// CHECK: call i64 @llgo_test_cpp_add(i64 %{{[0-9]+}}, i64 %{{[0-9]+}})
func main() {
	if local.Add(20, 22) != 42 {
		panic("C++ ABI bridge returned the wrong value")
	}
	println("cpp abi ok")
}
