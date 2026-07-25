// LITTEST
package main

import "errors"

// CHECK-LABEL: define ptr @"{{.*}}/cl/_testlibgo/errors.main$coro"(
// CHECK: [[NEW:%[0-9]+]] = call ptr @"errors.New$coro"
// CHECK: call void @__llgo_coro_await_prepare_v3({{.*}}ptr [[NEW]]
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.IfaceType"
// CHECK: call void @__llgo_coro_panic_prepare_v1
func main() {
	err := errors.New("error")
	panic(err)
}
