// LITTEST
package main

import "net/textproto"

// CHECK-LABEL: define ptr @"{{.*}}/cl/_testlibgo/nettextproto.main$coro"(
func main() {
	// CHECK: [[CANON:%[0-9]+]] = call ptr @"net/textproto.CanonicalMIMEHeaderKey$coro"({{.*}}%"{{.*}}/runtime/internal/runtime.String" { ptr @0, i64 4 })
	// CHECK: call void @__llgo_coro_await_prepare_v3({{.*}}ptr [[CANON]]
	println(textproto.CanonicalMIMEHeaderKey("host"))
}
