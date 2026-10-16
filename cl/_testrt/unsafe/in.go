// LITTEST
// Scope: common
package main

import (
	"unsafe"
)

//go:linkname cstr llgo.cstr
func cstr(string) *int8

//llgo:type C
type T func()

type M struct {
	fn T
	v  int
}

type N struct {
	fn func()
	v  int
}

// A constant uintptr-to-pointer conversion is emitted directly, without a
// runtime helper.
// CHECK-LABEL: define ptr @main.constantPointer(){{.*}} {
// CHECK: ret ptr inttoptr (i64 100 to ptr)

// The panic-capable owner is an outcome-aware coroutine. Sizeof, Alignof, and
// Offsetof still fold; unsafe.String/Slice keep their ordinary data layouts;
// and unsafe.Add remains byte-wise pointer arithmetic.
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call token @llvm.coro.id
// CHECK-COUNT-10: br i1 false
// CHECK: %[[ARRAY:[0-9]+]] = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 16)
// CHECK: %[[BASE:[0-9]+]] = getelementptr inbounds i64, ptr %[[ARRAY]], i64 0
// CHECK: icmp ne i64 ptrtoint (ptr getelementptr (i8, ptr null, i64 1) to i64), 1
// CHECK: call ptr @main.constantPointer()
// CHECK: call void @"main.zeroSlice$outcome"
// CHECK: call i1 @"{{.*}}/runtime/internal/runtime.StringEqual"
// CHECK: %[[SLICE_PTR:[0-9]+]] = insertvalue %"{{.*}}Slice" undef, ptr %[[BASE]], 0
// CHECK-NEXT: %[[SLICE_LEN:[0-9]+]] = insertvalue %"{{.*}}Slice" %[[SLICE_PTR]], i64 2, 1
// CHECK-NEXT: %[[SLICE:[0-9]+]] = insertvalue %"{{.*}}Slice" %[[SLICE_LEN]], i64 2, 2
// CHECK-DAG: extractvalue %"{{.*}}Slice" %[[SLICE]], 0
// CHECK-DAG: extractvalue %"{{.*}}Slice" %[[SLICE]], 1

// A nil pointer with a zero, narrower length returns through the outcome result
// slot as a zero-length slice.
// CHECK-LABEL: define void @"main.zeroSlice$outcome"(
// CHECK: store %"{{.*}}Slice" zeroinitializer, ptr %{{[0-9]+}}
// CHECK: ret void

func main() {
	if unsafe.Sizeof(*(*T)(nil)) != unsafe.Sizeof(0) {
		panic("error")
	}
	if unsafe.Sizeof(*(*M)(nil)) != unsafe.Sizeof([2]int{}) {
		panic("error")
	}
	// TODO(lijie): inconsistent with golang
	if unsafe.Sizeof(*(*N)(nil)) != unsafe.Sizeof([3]int{}) {
		panic("error")
	}

	if unsafe.Alignof(*(*T)(nil)) != unsafe.Alignof(0) {
		panic("error")
	}
	if unsafe.Alignof(*(*M)(nil)) != unsafe.Alignof([2]int{}) {
		panic("error")
	}
	if unsafe.Alignof(*(*N)(nil)) != unsafe.Alignof([3]int{}) {
		panic("error")
	}

	if unsafe.Offsetof(M{}.fn) != 0 {
		panic("error")
	}
	if unsafe.Offsetof(M{}.v) != unsafe.Sizeof(int(0)) {
		panic("error")
	}
	if unsafe.Offsetof(N{}.fn) != 0 {
		panic("error")
	}
	// TODO(lijie): inconsistent with golang
	if unsafe.Offsetof(N{}.v) != unsafe.Sizeof([2]int{}) {
		panic("error")
	}

	s := unsafe.String((*byte)(unsafe.Pointer(cstr("abc"))), 3)
	if s != "abc" {
		panic("error")
	}

	p := unsafe.StringData(s)
	arr := (*[3]byte)(unsafe.Pointer(p))
	if arr[0] != 'a' || arr[1] != 'b' || arr[2] != 'c' {
		panic("error")
	}

	intArr := [2]int{1, 2}
	pi := &intArr[0]
	intSlice := unsafe.Slice(pi, 2)
	if intSlice[0] != 1 || intSlice[1] != 2 {
		panic("error")
	}

	pi = unsafe.SliceData(intSlice)
	if *pi != 1 {
		panic("error")
	}

	if uintptr(unsafe.Add(unsafe.Pointer(nil), 1)) != 1 {
		panic("error")
	}

	_ = constantPointer()
	_ = zeroSlice()

}

func constantPointer() unsafe.Pointer {
	return unsafe.Pointer(uintptr(100))
}

func zeroSlice() []int {
	var p *int
	var n uint32
	return unsafe.Slice(p, n)
}
