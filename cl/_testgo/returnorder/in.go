// LITTEST
package main

import "fmt"

// A call in a return list is evaluated before the sibling variable is loaded.
// The call itself uses the structured outcome path and its caller awaits the
// coroutine result rather than falling back to a native goroutine wrapper.
//
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call ptr @"main.returnStateAndMut$coro"(
// CHECK: call void @__llgo_coro_await_prepare_v3(
// CHECK: call i8 @llvm.coro.suspend(
// CHECK-LABEL: define ptr @"main.returnStateAndMut$coro"(
// CHECK: store i64 1
// CHECK: call void @"main.(*state).mutate$outcome"(
// CHECK: call void @__llgo_coro_complete_prepare_v2(
// CHECK: load %main.state
// CHECK: store %main.state
// CHECK-LABEL: define void @"main.(*state).mutate$outcome"(
// CHECK: store i64 %4
// CHECK: load i64
// CHECK-NOT: NewProc
// CHECK-NOT: _llgo_routine

type state struct {
	v int
}

func main() {
	a, b := returnStateAndMut()
	if a.v != 2 || b != 2 {
		panic(fmt.Sprintf("return order mismatch: got (%d,%d), want (2,2)", a.v, b))
	}
	println("ok")
}

func returnStateAndMut() (state, int) {
	x := state{v: 1}
	return x, x.mutate(2)
}

func (s *state) mutate(next int) int {
	s.v = next
	return s.v
}
