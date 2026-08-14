// LITTEST
package main

import (
	"fmt"
	"runtime"

	dep "github.com/goplus/llgo/cl/_testgo/rewrite/dep"
)

// Rewritten globals and runtime strings keep their ordinary data ABI, while
// package initialization and formatting calls participate in structured
// coroutine sequencing.
//
// CHECK: @main.VarName = global
// CHECK: @main.VarPlain = global
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.init$coro"(
// CHECK: call ptr @"fmt.init$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK: call ptr @"github.com/goplus/llgo/cl/_testgo/rewrite/dep.init$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"main.printLine$coro"(
// CHECK: call ptr @"github.com/goplus/llgo/cl/_testgo/rewrite/dep.PrintVar$coro"(
// CHECK: call %"{{.*}}String" @runtime.GOROOT()
// CHECK: call ptr @"main.printLine$coro"(
// CHECK: call %"{{.*}}String" @runtime.Version()
// CHECK: call ptr @"main.printLine$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK-LABEL: define ptr @"main.printLine$coro"(
// CHECK: call ptr @"fmt.Printf$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

var VarName = "main-default"
var VarPlain string

func printLine(label, value string) {
	fmt.Printf("%s: %s\n", label, value)
}

func main() {
	printLine("main.VarName", VarName)
	printLine("main.VarPlain", VarPlain)
	dep.PrintVar()
	printLine("runtime.GOROOT()", runtime.GOROOT())
	printLine("runtime.Version()", runtime.Version())
}
