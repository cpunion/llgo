// LITTEST
package main

import "github.com/goplus/llgo/cl/_testdata/foo"

// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define void @"main.(*Game2).initGame"(ptr %0)
// CHECK: ret void
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"{{.*}}NewItab$coro"(
// CHECK: call ptr @"{{.*}}PrintString$coro"(
// CHECK: call ptr %{{[0-9]+}}(ptr %0,
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK: call ptr @"{{.*}}NewItab$coro"(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

type Game1 struct {
	*foo.Game
}

type Game2 struct{}

func (p *Game2) initGame() {}

func main() {
	var g1 any = &Game1{&foo.Game{}}
	var g2 any = &Game2{}

	v1, ok := g1.(foo.Gamer)
	println("OK", v1, ok)
	if ok {
		v1.Load()
	}

	v2, ok := g2.(foo.Gamer)
	println("FAIL", v2, ok)
}
