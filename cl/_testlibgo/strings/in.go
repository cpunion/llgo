// LITTEST
package main

import (
	"strings"
	"unicode"
)

// CHECK: @__llgo_coro_func_descriptor_v1.{{.*}} = {{.*}}ptr @__llgo_coro_func_coro_v1.{{.*}}
// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 32)
	// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.StringToBytes$coro"
	// CHECK: call ptr @"strings.(*Builder).Write$coro"
	// CHECK: call ptr @"strings.(*Builder).WriteString$coro"
	// CHECK: call ptr @"strings.(*Builder).Len$coro"
	// CHECK: call ptr @"strings.(*Builder).Cap$coro"
	// CHECK: call ptr @"strings.(*Builder).String$coro"
	// CHECK: call ptr @"strings.IndexFunc$coro"({{.*}}{ ptr, ptr } { ptr @__llgo_coro_func_descriptor_v1.{{.*}}, ptr null })
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
