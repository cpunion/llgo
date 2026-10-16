// LITTEST
// Scope: common
package main

import (
	_ "unsafe"

	_ "github.com/xgo-dev/llgo/cl/_testrt/linkname/linktarget"
)

//go:linkname cstr llgo.cstr
func cstr(string) *int8

//go:linkname print github.com/xgo-dev/llgo/cl/_testrt/linkname/linktarget.F
func print(a, b, c, d *int8)

type m struct {
	s string
}

//go:linkname setInfo github.com/xgo-dev/llgo/cl/_testrt/linkname/linktarget.(*m).setInfo
func setInfo(*m, string)

//go:linkname info github.com/xgo-dev/llgo/cl/_testrt/linkname/linktarget.m.info
func info(m) string

func callPrint(fn func(a, b, c, d *int8), a, b, c, d *int8) {
	fn(a, b, c, d)
}

var evaluationOrder int

func mark(n int, value *int8) *int8 {
	evaluationOrder = evaluationOrder*10 + n
	return value
}

// All four source arguments reach the managed descriptor call. Its compiler-
// owned thunk below forwards only the definition's compatible two-argument
// prefix. The two static calls use the same prefix facade directly.
// CHECK-LABEL: define ptr @"main.callPrint$coro"(
// CHECK-SAME: { ptr, ptr } %[[FN:[0-9]+]], ptr %[[A:[0-9]+]], ptr %[[B:[0-9]+]], ptr %[[C:[0-9]+]], ptr %[[D:[0-9]+]])
// CHECK: call ptr %{{[0-9]+}}({{.*}}ptr %[[A]], ptr %[[B]], ptr %[[C]], ptr %[[D]])

// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK-DAG: call ptr @"{{.*}}/cl/_testrt/linkname/linktarget.F$coro"(ptr {{.*}}, ptr {{.*}}, ptr @{{[0-9]+}}, ptr @{{[0-9]+}})
// CHECK-DAG: call ptr @"{{.*}}/cl/_testrt/linkname/linktarget.F$coro"(ptr {{.*}}, ptr {{.*}}, ptr @{{[0-9]+}}, ptr @{{[0-9]+}})
// CHECK-DAG: call ptr @"{{.*}}/cl/_testrt/linkname/linktarget.F$coro"({{.*}}ptr %{{[0-9]+}}, ptr %{{[0-9]+}})
// CHECK-DAG: call ptr @"main.callPrint$coro"({{.*}})

// CHECK: define linkonce ptr @[[THUNK:__llgo_coro_func_coro_v2\.[0-9a-f.]+]](ptr %[[G:[0-9]+]], ptr %[[OUT:[0-9]+]], ptr %[[ENV:[0-9]+]], ptr %[[TA:[0-9]+]], ptr %[[TB:[0-9]+]], ptr %[[TC:[0-9]+]], ptr %[[TD:[0-9]+]])
// CHECK-NEXT: _llgo_0:
// CHECK-NEXT: call ptr @"{{.*}}/cl/_testrt/linkname/linktarget.F$coro"(ptr %[[G]], ptr %[[OUT]], ptr %[[TA]], ptr %[[TB]])
func main() {
	var m m
	setInfo(&m, "hello")
	println(info(m))
	print(cstr("a"), cstr("b"), cstr("c"), cstr("d"))
	print(cstr("1"), cstr("2"), cstr("3"), cstr("4"))
	evaluationOrder = 0
	print(mark(1, cstr("e")), mark(2, cstr("f")), mark(3, cstr("g")), mark(4, cstr("h")))
	if evaluationOrder != 1234 {
		panic("linkname facade skipped or reordered a source argument")
	}
	callPrint(print, cstr("x"), cstr("y"), cstr("z"), cstr("w"))
}
