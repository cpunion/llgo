// LITTEST
package main

import (
	"strings"
	"unicode"
)

// CHECK: @__llgo_coro_func_descriptor_v2.{{.*}} = {{.*}}ptr @__llgo_coro_func_coro_v2.{{.*}}
// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	// All Builder operations use one receiver, and the queried values are printed.
	// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 32)
	// CHECK: call void @"{{.*}}/runtime/internal/runtime.StringToBytes$outcome"
	// CHECK: call void @"strings.(*Builder).Write$outcome"
	// CHECK: call void @"strings.(*Builder).WriteString$outcome"
	// CHECK: call void @"strings.(*Builder).Len$outcome"
	// CHECK: call void @"strings.(*Builder).Cap$outcome"
	// CHECK: call void @"strings.(*Builder).String$outcome"
	// CHECK: [[INDEX:%[0-9]+]] = call ptr @"strings.indexFunc$coro"({{.*}}{ ptr, ptr } { ptr @__llgo_coro_func_descriptor_v2.{{.*}}, ptr null }
	// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[INDEX]]
	var b strings.Builder
	b.Write([]byte("Hello "))
	b.WriteString("World")

	println("len:", b.Len(), "cap:", b.Cap(), "string:", b.String())

	f := func(c rune) bool {
		return unicode.Is(unicode.Han, c)
	}
	println(strings.IndexFunc("Hello, 世界", f))
	println(strings.IndexFunc("Hello, world", f))
}
