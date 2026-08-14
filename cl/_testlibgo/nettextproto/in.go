// LITTEST
package main

import "net/textproto"

// CHECK-LABEL: define ptr @"main.main$coro"(
func main() {
	// CHECK: [[CANON:%[0-9]+]] = call ptr @"net/textproto.CanonicalMIMEHeaderKey$coro"({{.*}}%"{{.*}}/runtime/internal/runtime.String" { ptr @{{[0-9]+}}, i64 4 })
	// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[CANON]]
	println(textproto.CanonicalMIMEHeaderKey("host"))
}
