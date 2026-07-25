// LITTEST
package main

import "os"

// CHECK-LABEL: define ptr @"{{.*}}/cl/_testlibgo/os.main$coro"(
func main() {
	// CHECK: [[GETWD:%[0-9]+]] = call ptr @"os.Getwd$coro"(
	// CHECK: call void @__llgo_coro_await_prepare_v3({{.*}}ptr [[GETWD]]
	// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.IfaceType"
	// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.EfaceEqual$coro"
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	println("cwd:", wd)
}
