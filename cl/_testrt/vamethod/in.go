// LITTEST
// Scope: common
package main

import (
	"unsafe"
)

//go:linkname cstr llgo.cstr
func cstr(string) *int8

// Variadic C-linked methods must forward their receiver-derived format pointer
// and preserve the variadic arguments for direct and interface calls.

//llgo:link (*T).Printf C.printf
func (*T) Printf(__llgo_va_list ...any) int32 { return 0 }

type T int8

//go:linkname Printf C.printf
func Printf(format *int8, __llgo_va_list ...any) int32

type CFmt struct {
	*T
}

func (f *CFmt) SetFormat(fmt *int8) {
	f.T = (*T)(unsafe.Pointer(fmt))
}

type IFmt interface {
	SetFormat(fmt *int8)
	Printf(__llgo_va_list ...any) int32
}

func main() {
	cfmt := &CFmt{}
	cfmt.SetFormat(cstr("%s (%d)\n"))
	cfmt.Printf(cstr("hello"), 100)
	cfmt.SetFormat(cstr("(%d) %s\n"))
	cfmt.Printf(200, cstr("world"))

	var i any = &CFmt{}
	ifmt, ok := i.(IFmt)
	if !ok {
		panic("error")
	}
	ifmt.SetFormat(cstr("%s (%d,%d)\n"))
	ifmt.Printf(cstr("ifmt"), 100, 200)
}

// The three concrete calls must become typed worker records. In particular,
// the interface call must retain all three concrete C ellipsis operands rather
// than forwarding the synthetic []any through a generic managed descriptor.
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}ptr @[[DIRECT1:__llgo_coro_worker_foreign_thunk_v1_[0-9a-f]+]] to i64{{.*}})
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}ptr @[[DIRECT2:__llgo_coro_worker_foreign_thunk_v1_[0-9a-f]+]] to i64{{.*}})
// CHECK: call void @__llgo_coro_worker_park_v1({{.*}}ptr @[[INTERFACE:__llgo_coro_worker_foreign_thunk_v1_[0-9a-f]+]] to i64{{.*}})
// CHECK: call i8 @llvm.coro.suspend(token none, i1 false)

// CHECK: define linkonce i64 @[[DIRECT1]](
// CHECK: call i32 (ptr, ...) @printf(ptr %{{[0-9]+}}, ptr %{{[0-9]+}}, i64 %{{[0-9]+}})
// CHECK: define linkonce i64 @[[DIRECT2]](
// CHECK: call i32 (ptr, ...) @printf(ptr %{{[0-9]+}}, i64 %{{[0-9]+}}, ptr %{{[0-9]+}})
// CHECK: define linkonce i64 @[[INTERFACE]](
// CHECK: call i32 (ptr, ...) @printf(ptr %{{[0-9]+}}, ptr %{{[0-9]+}}, i64 %{{[0-9]+}}, i64 %{{[0-9]+}})
