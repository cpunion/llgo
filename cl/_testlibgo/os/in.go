// LITTEST
package main

import "os"

// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	// CHECK: [[GETWD:%[0-9]+]] = call ptr @"os.Getwd$coro"(
	// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[GETWD]]
	// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.IfaceType"
	// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.EfaceEqual$coro"
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	println("cwd:", wd)
}
