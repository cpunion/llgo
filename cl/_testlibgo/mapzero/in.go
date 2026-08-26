// LITTEST
package main

import "fmt"

var a = 0

// The zero-length array check uses a as the index; the lowered nil map is still explicit.

func main() {
	defer func() {
		err := recover()
		fmt.Println(err)
	}()
	// CHECK-LABEL: define ptr @"main.main$coro"(
	// Signed index bounds are encoded by the V2 bounds family beginning at 13.
	// CHECK: call void @__llgo_coro_fault_payload_v2(i32 13, i64 {{%.*}}, i64 0,
	// CHECK: [[LOOKUP:%[0-9]+]] = call ptr @"github.com/xgo-dev/llgo/runtime/internal/runtime.mapaccess1_fast64$coro"({{.*}}ptr @"map[_llgo_int]_llgo_int", ptr null, i64 0)
	// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[LOOKUP]]
	m := [0]map[int]int{}[a][0]
	print(m)
}
