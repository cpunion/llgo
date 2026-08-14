// LITTEST
package main

import "errors"

// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call void @"errors.New$outcome"
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.IfaceType"
// CHECK: call void @__llgo_coro_panic_prepare_v1
func main() {
	err := errors.New("error")
	panic(err)
}
