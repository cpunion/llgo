// LITTEST
// Scope: common
package main

import (
	_ "github.com/xgo-dev/llgo/cl/_testpy/callpy/pylink"
	_ "unsafe"
)

// This fixture owns Go-to-Python value conversion, list and tuple construction
// and access, and Python object/string access. All declarations are local so
// the compiler test does not inherit coverage from goplus/lib.
type Object struct {
	unused [0]byte
}

//go:linkname list llgo.pyList
func list(__llgo_va_list ...any) *Object

//go:linkname tuple llgo.pyTuple
func tuple(__llgo_va_list ...any) *Object

// llgo:link (*Object).IsTrue C.PyObject_IsTrue
func (*Object) IsTrue() int32 { return -1 }

// llgo:link (*Object).ListLen C.PyList_Size
func (*Object) ListLen() int { return 0 }

// llgo:link (*Object).ListItem C.PyList_GetItem
func (*Object) ListItem(index int) *Object { return nil }

// llgo:link (*Object).TupleLen C.PyTuple_Size
func (*Object) TupleLen() int { return 0 }

// llgo:link (*Object).Str C.PyObject_Str
func (*Object) Str() *Object { return nil }

// llgo:link (*Object).CStr C.PyUnicode_AsUTF8
func (*Object) CStr() *byte { return nil }

func makeValues(pointer *int) *Object {
	return list(
		true,
		int64(-2),
		uint64(3),
		4.5,
		1+2i,
		"go",
		[]byte("bytes"),
		[...]byte{7, 8},
		[...]byte{},
		pointer,
	)
}

func makeTuple() *Object {
	return tuple(1.0, 2.0)
}

func makeNested() *Object {
	return list(list(int64(10)))
}

func main() {
	value := 100
	values := makeValues(&value)
	nested := makeNested()
	items := makeTuple()
	text := values.Str()

	println("list =", values.ListLen(), values.ListItem(0).IsTrue(), values.ListItem(9).IsTrue())
	println("nested =", nested.ListLen(), nested.ListItem(0).ListLen())
	println("tuple =", items.TupleLen())
	println("str =", text.IsTrue(), text.CStr() != nil)
}

// Local helpers are colored automatically from the compiler-owned Python
// operation and inferred Python object type. Every Python episode keeps the
// interpreter thread through the generic same-M boundary.
// CHECK-LABEL: define ptr @"main.main$coro"(
// CHECK-COUNT-12: call void @__llgo_coro_same_m_foreign_call_v1(
// CHECK-LABEL: define ptr @"main.makeNested$coro"(
// CHECK-COUNT-5: call void @__llgo_coro_same_m_foreign_call_v1(
// CHECK-LABEL: define ptr @"main.makeTuple$coro"(
// CHECK-COUNT-5: call void @__llgo_coro_same_m_foreign_call_v1(
// CHECK-LABEL: define ptr @"main.makeValues$coro"(
// CHECK-COUNT-21: call void @__llgo_coro_same_m_foreign_call_v1(

// The typed thunks retain the original Python C API signatures. Runtime output
// checks construction order and values without coupling this check to unstable
// coroutine SSA numbering or generated thunk hashes.
// CHECK-DAG: call ptr @PyObject_Str(ptr
// CHECK-DAG: call i64 @PyList_Size(ptr
// CHECK-DAG: call ptr @PyList_GetItem(ptr
// CHECK-DAG: call i32 @PyObject_IsTrue(ptr
// CHECK-DAG: call i64 @PyTuple_Size(ptr
// CHECK-DAG: call ptr @PyUnicode_AsUTF8(ptr
// CHECK-DAG: call ptr @PyList_New(i64
// CHECK-DAG: call ptr @PyLong_FromLongLong(i64
// CHECK-DAG: call i32 @PyList_SetItem(ptr
// CHECK-DAG: call ptr @PyTuple_New(i64
// CHECK-DAG: call ptr @PyFloat_FromDouble(double
// CHECK-DAG: call i32 @PyTuple_SetItem(ptr
// CHECK-DAG: call ptr @PyBool_FromLong(i32
// CHECK-DAG: call ptr @PyLong_FromUnsignedLongLong(i64
// CHECK-DAG: call ptr @PyComplex_FromDoubles(double
// CHECK-DAG: call ptr @PyUnicode_FromStringAndSize(ptr
// CHECK-DAG: call ptr @PyByteArray_FromStringAndSize(ptr
// CHECK-DAG: call ptr @PyBytes_FromStringAndSize(ptr
