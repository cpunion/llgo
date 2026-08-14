// LITTEST
package main

import "fmt"

var a = 0

func main() {
	defer func() {
		err := recover()
		fmt.Println(err)
	}()
	// CHECK-LABEL: define ptr @"main.main$coro"(
	// CHECK: [[LOOKUP:%[0-9]+]] = call ptr @"github.com/goplus/llgo/runtime/internal/runtime.MapAccess1$coro"({{.*}}ptr @"map[_llgo_int]_llgo_int", ptr null, ptr {{%.*}})
	// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[LOOKUP]]
	m := [0]map[int]int{}[a][0]
	print(m)
}
