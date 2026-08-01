// LITTEST
package main

import (
	"math/bits"
)

// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	// CHECK: [[LEN:%[0-9]+]] = call ptr @"math/bits.Len8$coro"({{.*}}i8 20)
	// CHECK: call void @__llgo_coro_await_prepare_v3({{.*}}ptr [[LEN]]
	// CHECK: call ptr @"math/bits.OnesCount$coro"({{.*}}i64 20)
	println(bits.Len8(20))
	println(bits.OnesCount(20))
}
