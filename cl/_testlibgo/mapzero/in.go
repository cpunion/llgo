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
	// CHECK: [[LOOKUP:%[0-9]+]] = call ptr @"github.com/xgo-dev/llgo/runtime/internal/runtime.MapAccess1$coro"({{.*}}ptr @"map[_llgo_int]_llgo_int", ptr null, ptr {{%.*}})
	// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[LOOKUP]]
	m := [0]map[int]int{}[a][0]
	print(m)
}

// DARWIN-ARM64-NEXT:   %[[TMP1:[0-9]+]] = alloca i8, i64 196, align 1
// LINUX-AMD64-NEXT:   %[[TMP1:[0-9]+]] = alloca i8, i64 200, align 1
// DARWIN-ARM64-NEXT:   %[[TMP11:[0-9]+]] = call i32 @sigsetjmp(ptr %[[TMP1]], i32 0)
// LINUX-AMD64-NEXT:   %[[TMP11:[0-9]+]] = call i32 @__sigsetjmp(ptr %[[TMP1]], i32 0)
