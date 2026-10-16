// LITTEST
// Scope: os+arch (Python C long width; call forms otherwise common)
package main

import (
	"github.com/xgo-dev/llgo/cl/_testpy/callpy/builtins"
	_ "github.com/xgo-dev/llgo/cl/_testpy/callpy/pylink"
)

// This fixture owns Python zero-, one-, two-, and variadic-argument calls plus
// boolean, float, and platform-long result extraction. The imported packages
// are deliberately tiny local ABI declarations, not wrapper-library coverage.
func main() {
	truth := builtins.Bool()
	converted := builtins.Float(builtins.NewFloat(2))
	power := builtins.Pow(builtins.NewFloat(2), builtins.NewFloat(3))
	integer := builtins.Int(builtins.NewFloat(5))
	maximum := builtins.Max(
		builtins.NewFloat(3),
		builtins.NewFloat(9),
		builtins.NewFloat(23),
		builtins.NewFloat(100),
	)
	maximumAgain := builtins.Max(builtins.NewFloat(-1), builtins.NewFloat(4))

	println("bool =", truth.IsTrue())
	println("float =", converted.Float64() == 2)
	println("pow =", power.Float64() == 8)
	println("long =", integer.Long())
	println("max =", maximum.Float64() == 100)
	println("max again =", maximumAgain.Float64() == 4)
}

// CHECK-LABEL: define ptr @"main.main$coro"(
// Ten object constructions, six Python function calls, and six result
// extractions all retain the interpreter thread through the generic same-M
// boundary. In particular, none may silently fall back to the blocking worker.
// CHECK-COUNT-22: call void @__llgo_coro_same_m_foreign_call_v1(
// The typed thunks retain the original C/Python call signatures. The same
// pyfunc remains callable after its first invocation (funcOf historically
// cached/destructively transformed the global slot).
// CHECK-DAG: call ptr @PyObject_CallNoArgs(ptr
// CHECK-DAG: call ptr @PyFloat_FromDouble(double
// CHECK-DAG: call ptr @PyObject_CallOneArg(ptr
// CHECK-DAG: call ptr (ptr, ...) @PyObject_CallFunctionObjArgs(ptr
// CHECK-DAG: call i32 @PyObject_IsTrue(ptr
// CHECK-DAG: call double @PyFloat_AsDouble(ptr
// CHECK-DAG: call i{{32|64}} @PyLong_AsLong(ptr
// WINDOWS: declare i32 @PyLong_AsLong(ptr)
