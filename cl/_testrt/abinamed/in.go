// LITTEST
// Scope: common
package main

import (
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/abi"
)

// Converting the two named values to interface{} must select their distinct
// runtime type descriptors; the test body then exercises lazy pointer-to-this
// and element links from those descriptors.
// CHECK-LABEL: define void @"main.checkBasicNames$outcome"(
// CHECK: store %"{{.*}}eface" { ptr @_llgo_int32,
// CHECK: call void @"{{.*}}/runtime/abi.(*Type).String$outcome"
// CHECK: store %"{{.*}}eface" { ptr @_llgo_uint8,
// CHECK: call void @"{{.*}}/runtime/abi.(*Type).String$outcome"
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK: call token @llvm.coro.id
// CHECK: call void @"main.checkBasicNames$outcome"
// CHECK: call void @"main.toEface$outcome"({{.*}}{ ptr @_llgo_main.T,
// CHECK: call void @"main.toEface$outcome"({{.*}}{ ptr @"_llgo_{{.*}}/runtime/abi.Type",
// CHECK: call void @"{{.*}}/runtime/abi.(*Type).StructType$outcome"
// CHECK: call void @"{{.*}}/runtime/abi.(*Type).Elem$outcome"

type T struct {
	p *T
	t *abi.Type
	n uintptr
	a []T
}

type eface struct {
	typ  *abi.Type
	data unsafe.Pointer
}

// checkBasicNames folds the former abitype fixture into this runtime-type
// owner while preserving the rune/byte descriptor-name regression.
func checkBasicNames() {
	var v any = rune(0)
	if (*eface)(unsafe.Pointer(&v)).typ.String() != "int32" {
		panic("abi rune error")
	}
	v = byte(0)
	if (*eface)(unsafe.Pointer(&v)).typ.String() != "uint8" {
		panic("abi byte error")
	}
}

func main() {
	checkBasicNames()
	e := toEface(T{})
	e2 := toEface(abi.Type{})

	println(e.typ)
	println(e.typ.PtrToThis_)
	println(e2.typ)
	println(e2.typ.PtrToThis_)

	f0 := e.typ.StructType().Fields[0]
	if e.typ.PtrToThis_ != nil {
		panic("methodless pointer-to-this was generated eagerly")
	}
	if f0.Typ.Elem() != e.typ {
		panic("error field 0 elem")
	}
	f1 := e.typ.StructType().Fields[1]
	if f1.Typ != e2.typ.PtrToThis_ {
		panic("error field 1")
	}
	if f1.Typ.Elem() != e2.typ {
		panic("error field 1 elem")
	}
	f2 := e.typ.StructType().Fields[2]
	if f2.Typ != e2.typ.StructType().Fields[0].Typ {
		panic("error field 2")
	}
	f3 := e.typ.StructType().Fields[3]
	if f3.Typ.Elem() != e.typ {
		panic("error field 3")
	}
}

func toEface(i any) *eface {
	// CHECK-LABEL: define void @"main.toEface$outcome"(
	// CHECK: [[TO_EFACE_ADDR:%[0-9]+]] = call ptr @"{{.*}}/runtime/internal/runtime.AllocZ"(i64 16)
	// CHECK-NEXT: store %"{{.*}}eface" [[TO_EFACE_VALUE:%[0-9]+]], ptr [[TO_EFACE_ADDR]]
	// CHECK: store ptr [[TO_EFACE_ADDR]], ptr %{{[0-9]+}}
	// CHECK: ret void
	return (*eface)(unsafe.Pointer(&i))
}
