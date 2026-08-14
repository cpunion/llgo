// LITTEST
package main

import (
	"math/bits"
)

// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	// CHECK: call void @"math/bits.Len8$outcome"({{.*}}i8 20)
	// CHECK: [[PRINT:%[0-9]+]] = call ptr @"{{.*}}PrintInt$coro"
	// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[PRINT]]
	// CHECK: call i64 @"math/bits.OnesCount"(i64 20)
	println(bits.Len8(20))
	println(bits.OnesCount(20))
}
