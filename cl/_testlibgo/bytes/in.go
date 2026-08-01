// LITTEST
package main

import (
	"bytes"
)

// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 40)
// CHECK: [[S2B:%[0-9]+]] = call ptr @"{{.*}}/runtime/internal/runtime.StringToBytes$coro"
// CHECK: call void @__llgo_coro_await_prepare_v3({{.*}}ptr [[S2B]]
// CHECK: [[WRITE:%[0-9]+]] = call ptr @"bytes.(*Buffer).Write$coro"
// CHECK: call void @__llgo_coro_await_prepare_v3({{.*}}ptr [[WRITE]]
// CHECK: call ptr @"bytes.(*Buffer).WriteString$coro"
// CHECK: call ptr @"bytes.(*Buffer).Bytes$coro"
// CHECK: call ptr @"bytes.(*Buffer).String$coro"
// CHECK: call ptr @"bytes.EqualFold$coro"
func main() {
	var b bytes.Buffer // A Buffer needs no initialization.
	b.Write([]byte("Hello "))
	b.WriteString("World")

	println("buf", b.Bytes(), b.String())

	println(bytes.EqualFold([]byte("Go"), []byte("go")))
}
