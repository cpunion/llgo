// LITTEST
package main

import (
	"bytes"
)

// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: alloca %bytes.Buffer
// CHECK: call void @"{{.*}}/runtime/internal/runtime.StringToBytes$outcome"
// CHECK: [[WRITE:%[0-9]+]] = call ptr @"bytes.(*Buffer).Write$coro"
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[WRITE]]
// CHECK: call ptr @"bytes.(*Buffer).WriteString$coro"
// CHECK: call void @"bytes.(*Buffer).Bytes$outcome"
// CHECK: call void @"bytes.(*Buffer).String$outcome"
// CHECK: call void @"bytes.EqualFold$outcome"
func main() {
	var b bytes.Buffer // A Buffer needs no initialization.
	b.Write([]byte("Hello "))
	b.WriteString("World")

	println("buf", b.Bytes(), b.String())

	println(bytes.EqualFold([]byte("Go"), []byte("go")))
}
