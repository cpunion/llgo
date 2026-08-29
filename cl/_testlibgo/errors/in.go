// LITTEST
package main

import "errors"

// CHECK-LABEL: define void @"main.main$outcome"(
// CHECK: call void @"errors.New$outcome"
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.IfaceType"
// CHECK: call void @__llgo_coro_panic_trace_append_v1
// CHECK: store i32 2
// CHECK: ret void
func main() {
	err := errors.New("error")
	panic(err)
}
