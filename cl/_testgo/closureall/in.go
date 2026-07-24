// LITTEST
package main

import _ "unsafe" // for go:linkname

import "github.com/goplus/lib/c"

// These checks intentionally describe stable stackless-coroutine contracts
// instead of numbered SSA values or the exact instruction layout.
//
// A statically synchronous function keeps its ordinary ABI.
// CHECK-LABEL: define i64 @"{{.*}}/closureall.S.Inc"
// CHECK: add i64
// CHECK: ret i64
//
// A colored method has the coroutine ABI and publishes a resumable frame.
// CHECK-LABEL: define ptr @"{{.*}}/closureall.(*S).Add$coro"
// CHECK: call token @llvm.coro.id
// CHECK: call ptr @llvm.coro.begin
// CHECK: call void @__llgo_coro_frame_publish_v1
// CHECK: call i8 @llvm.coro.suspend
// CHECK: call void @__llgo_coro_complete_prepare_v2
//
// A statically synchronous free function also keeps its ordinary ABI.
// CHECK-LABEL: define i64 @"{{.*}}/closureall.globalAdd"
// CHECK: add i64
// CHECK: ret i64
//
// The entry coroutine uses the common await protocol for colored Go calls and
// the worker protocol for a potentially blocking foreign call.
// CHECK-LABEL: define ptr @"{{.*}}/closureall.main$coro"
// CHECK: call token @llvm.coro.id
// CHECK: call ptr @llvm.coro.begin
// CHECK: call void @__llgo_coro_frame_publish_v1
// CHECK: call ptr @"{{.*}}/closureall.makeNoFree$coro"
// CHECK: call void @__llgo_coro_await_prepare_v3
// CHECK: call void @__llgo_coro_complete_prepare_v2
// CHECK: call i32 @__llgo_coro_await_consume_v1
// CHECK: call void @__llgo_coro_worker_park_v1
// CHECK: call i32 @__llgo_coro_worker_resume_v1
//
// The closure constructor itself is colored because the callable may cross a
// dynamic boundary; its body follows the same frame protocol.
// CHECK-LABEL: define ptr @"{{.*}}/closureall.makeNoFree$coro"
// CHECK: call token @llvm.coro.id
// CHECK: call ptr @llvm.coro.begin
// CHECK: call void @__llgo_coro_frame_publish_v1
// CHECK: call void @__llgo_coro_complete_prepare_v2

//go:linkname cSqrt C.sqrt
func cSqrt(x c.Double) c.Double

//llgo:coro noblock
// llgo:link cAbs C.abs
func cAbs(x c.Int) c.Int { return 0 }

// llgo:type C
type CCallback func(c.Int) c.Int

type Fn func(int) int

type S struct {
	v int
}

func (s S) Inc(x int) int {
	return s.v + x
}

func (s *S) Add(x int) int {
	return s.v + x
}

func callCallback(cb CCallback, v c.Int) c.Int {
	return cb(v)
}

func globalAdd(x, y int) int {
	return x + y
}

func main() {
	nf := makeNoFree()
	wf := makeWithFree(3)
	_ = nf(1)
	_ = wf(2)

	g := globalAdd
	_ = g(1, 2)

	s := &S{v: 5}
	mv := s.Add
	_ = mv(7)
	me := (*S).Add
	_ = me(s, 8)

	var i interface{ Add(int) int } = s
	im := i.Add
	_ = im(9)

	cs := cSqrt
	_ = cs(4)
	ca := cAbs
	_ = ca(-3)

	cb := CCallback(func(x c.Int) c.Int { return x + 1 })
	_ = callCallback(cb, 7)
}

func makeNoFree() Fn {
	return func(x int) int { return x + 1 }
}

func makeWithFree(base int) Fn {
	return func(x int) int { return x + base }
}
