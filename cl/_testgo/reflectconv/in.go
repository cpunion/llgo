// LITTEST
// Scope: common
package main

import (
	"math"
	"reflect"
)

type namedString string
type namedBytes []byte
type namedFloat32 float32
type namedFunc func(int) int

// Reflection calls are ordinary Go source calls, but their imported effect
// summaries make these callers suspend-aware. Check the physical coroutine
// entries and descriptor environments rather than the obsolete plain ABI.

// A named function conversion must preserve its captured environment.
// CHECK-LABEL: define ptr @"main.functionConversion$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: [[FUNC_ENV:%[0-9]+]] = call ptr @"{{.*}}AllocU"(i64 8)
// CHECK: [[FUNC_PAIR:%[0-9]+]] = insertvalue { ptr, ptr } { ptr @__llgo_coro_func_descriptor_v2.{{[0-9a-f]+}}.{{[0-9a-f]+}}, ptr undef }, ptr [[FUNC_ENV]], 1
// CHECK: [[FUNC_VALUE:%[0-9]+]] = call ptr @"reflect.ValueOf$coro"(
// CHECK: call i1 @__llgo_coro_await_prepare_inline_v4({{.*}}ptr [[FUNC_VALUE]],
// CHECK: [[FUNC_TYPE:%[0-9]+]] = call ptr @"reflect.TypeOf$coro"(
// CHECK: [[FUNC_RESULT:%[0-9]+]] = call ptr @"reflect.Value.Convert$coro"(
// CHECK: [[FUNC_IFACE:%[0-9]+]] = call ptr @"reflect.valueInterface$coro"(
// CHECK-NOT: call %reflect.Value @reflect.Value.Convert(

// Captured and declared functions both use descriptor-backed {fn,env} values;
// only the captured one allocates the eight-byte environment.
// CHECK-LABEL: define ptr @"main.functionPointers$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: [[CAPTURED_ENV:%[0-9]+]] = call ptr @"{{.*}}AllocU"(i64 8)
// CHECK: insertvalue { ptr, ptr } { ptr @__llgo_coro_func_descriptor_v2.{{[0-9a-f]+}}.{{[0-9a-f]+}}, ptr undef }, ptr [[CAPTURED_ENV]], 1
// CHECK: call ptr @"reflect.ValueOf$coro"(
// CHECK: call ptr @"reflect.Value.UnsafePointer$coro"(
// CHECK: store { ptr, ptr } { ptr @__llgo_coro_func_descriptor_v2.{{[0-9a-f]+}}.{{[0-9a-f]+}}, ptr null },
// CHECK: call ptr @"reflect.ValueOf$coro"(
// CHECK: call ptr @"reflect.Value.UnsafePointer$coro"(

// Main awaits each conversion scenario directly; no sync bridge or legacy stub
// is introduced between source calls.
// CHECK-LABEL: define ptr @"main.main$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call ptr @"main.numericConversions$coro"(
// CHECK: call ptr @"main.textConversions$coro"(
// CHECK: call ptr @"main.sliceArrayConversions$coro"(
// CHECK: call ptr @"main.functionConversion$coro"(
// CHECK: call ptr @"main.functionPointers$coro"(
// CHECK: call ptr @"main.nanConversion$coro"(
// CHECK-NOT: __llgo_stub

// CHECK-LABEL: define ptr @"main.nanConversion$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call void @"math.Float32frombits$outcome"(
// CHECK: call ptr @"reflect.ValueOf$coro"(
// CHECK: call ptr @"reflect.TypeOf$coro"(
// CHECK: call ptr @"reflect.Value.Convert$coro"(
// CHECK: call ptr @"reflect.valueInterface$coro"(
// CHECK: call void @"math.Float32bits$outcome"(

// CHECK-LABEL: define ptr @"main.numericConversions$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call ptr @"reflect.ValueOf$coro"(
// CHECK: call ptr @"reflect.TypeOf$coro"(
// CHECK: call ptr @"reflect.Value.Convert$coro"(
// CHECK: call void @"reflect.Value.Uint$outcome"(
// CHECK: call ptr @"reflect.Value.Convert$coro"(
// CHECK: call void @"reflect.Value.Int$outcome"(

// CHECK-LABEL: define ptr @"main.sliceArrayConversions$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call ptr @"reflect.Value.Convert$coro"(
// CHECK: call void @"reflect.Value.Index$outcome"(
// CHECK: call ptr @"reflect.Value.Elem$coro"(
// CHECK: call ptr @"reflect.Value.CanConvert$coro"(

// CHECK-LABEL: define ptr @"main.textConversions$coro"(ptr %0, ptr %1){{.*}} {
// CHECK: call ptr @"reflect.ValueOf$coro"(
// CHECK: call ptr @"reflect.TypeOf$coro"(
// CHECK: call ptr @"reflect.Value.Convert$coro"(
// CHECK: call ptr @"reflect.valueInterface$coro"(
func main() {
	numericConversions()
	textConversions()
	sliceArrayConversions()
	functionConversion()
	functionPointers()
	nanConversion()
}

// Numeric conversion exercises signed-to-unsigned overflow and floating-point
// truncation without enumerating the standard library's Cartesian matrix.
func numericConversions() {
	overflow := reflect.ValueOf(int64(257)).Convert(reflect.TypeOf(uint8(0)))
	if overflow.Uint() != 1 {
		panic("integer overflow conversion")
	}

	truncated := reflect.ValueOf(float64(-3.75)).Convert(reflect.TypeOf(int32(0)))
	if truncated.Int() != -3 {
		panic("floating-point truncation")
	}
}

// String/slice conversions use allocation-backed conversion helpers in both
// directions, including named source types.
func textConversions() {
	b := reflect.ValueOf(namedString("go")).Convert(reflect.TypeOf([]byte(nil))).Interface().([]byte)
	if len(b) != 2 || b[0] != 'g' || b[1] != 'o' {
		panic("string to byte slice")
	}

	s := reflect.ValueOf(namedBytes{'l', 'l'}).Convert(reflect.TypeOf("")).String()
	if s != "ll" {
		panic("byte slice to string")
	}
}

// Array conversion copies, pointer-to-array conversion aliases, and a short
// slice is rejected before the conversion helper is called.
func sliceArrayConversions() {
	src := []byte{1, 2, 3, 4}
	sv := reflect.ValueOf(src)
	av := sv.Convert(reflect.TypeOf([4]byte{}))
	src[0] = 9
	if av.Index(0).Uint() != 1 || av.CanAddr() {
		panic("slice-to-array must be an unaddressable copy")
	}

	pv := sv.Convert(reflect.TypeOf((*[4]byte)(nil)))
	src[1] = 8
	if pv.Elem().Index(1).Uint() != 8 {
		panic("slice-to-array-pointer must alias")
	}

	short := reflect.ValueOf(src[:2])
	arrayType := reflect.TypeOf([4]byte{})
	if short.CanConvert(arrayType) {
		panic("short slice reported convertible")
	}
	assertPanics(func() { short.Convert(arrayType) })
}

// Named-to-unnamed function conversion must preserve the closure environment.
func functionConversion() {
	bias := 40
	var fn namedFunc = func(v int) int { return bias + v }
	converted := reflect.ValueOf(fn).Convert(reflect.TypeOf((func(int) int)(nil))).Interface().(func(int) int)
	if converted(2) != 42 {
		panic("function conversion lost closure environment")
	}
}

// Func-kind UnsafePointer is a separate reflect path from conversion. Keep a
// captured closure and a declared function: the former carries an environment,
// while both must expose their non-nil code pointer.
func functionPointers() {
	bias := 1
	captured := func(v int) int { return bias + v }
	capturedPointer := reflect.ValueOf(captured).UnsafePointer()
	declaredPointer := reflect.ValueOf(numericConversions).UnsafePointer()
	if capturedPointer == nil || declaredPointer == nil || capturedPointer == declaredPointer {
		panic("bad reflected function pointer")
	}
}

// A signaling NaN's payload must survive conversion from a named float. This
// is the non-ordinary numeric edge that previously caught backend differences.
func nanConversion() {
	const signalingNaN = uint32(0x7f800001)
	in := namedFloat32(math.Float32frombits(signalingNaN))
	out := reflect.ValueOf(in).Convert(reflect.TypeOf(float32(0))).Interface().(float32)
	if math.Float32bits(out) != signalingNaN {
		panic("signaling NaN payload changed")
	}
}

func assertPanics(fn func()) {
	defer func() {
		if recover() == nil {
			panic("conversion did not panic")
		}
	}()
	fn()
}
