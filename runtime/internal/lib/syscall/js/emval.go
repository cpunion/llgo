//go:build js && wasm

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package js

import (
	"unsafe"

	c "github.com/goplus/llgo/runtime/internal/clite"
)

// The LLGo JS boundary is an ordinary direct WebAssembly import ABI. It does
// not use Emscripten's private embind internals or Go's private gojs
// stack-pointer ABI. Values remain opaque wasm32 handles; only the runner owns
// the corresponding JavaScript references.
var (
	valueGlobal       = emval_get_global(nil)
	objectConstructor = emval_get_global(c.Str("Object"))
	arrayConstructor  = emval_get_global(c.Str("Array"))
)

var (
	valueUndefined = Value{2}
	valueNull      = Value{4}
	valueTrue      = Value{6}
	valueFalse     = Value{8}
	valueNaN       = emval_get_global(c.Str("NaN"))
	valueZero      = emval_new_double(0)
)

const (
	emvalHostGetGlobalV1 uint32 = iota + 2
	emvalHostNewDoubleV1
	emvalHostNewStringV1
	emvalHostNewObjectV1
	emvalHostNewArrayV1
	emvalHostSetPropertyV1
	emvalHostGetPropertyV1
	emvalHostDeleteV1
	emvalHostIsNumberV1
	emvalHostIsStringV1
	emvalHostContainsV1
	emvalHostTypeOfV1
	emvalHostInstanceOfV1
	emvalHostAsDoubleV1
	emvalHostStringSizeV1
	emvalHostCopyStringV1
	emvalHostEqualsV1
	emvalHostMethodCallV1
	emvalHostCallV1
	emvalHostMemoryViewUint8V1
	emvalHostDumpV1
)

// One versioned, fixed-size record is the complete low-level JS reflection
// boundary. The operation is synchronously consumed before return and never
// retains record pointers. Go wrappers below remain ordinary inferred bodies,
// so adding an operation does not add another annotation or external effect
// seed.
//
//llgo:coro noblock
//go:wasmimport llgo_js invoke_v1
//go:noescape
func emvalHostInvokeV1(opcode uint32, words *[8]uint64) uint32

func emvalInvokeV1(opcode uint32, words *[8]uint64) {
	if emvalHostInvokeV1(opcode, words) != 1 {
		panic("syscall/js: invalid LLGo JavaScript host response")
	}
}

func emvalPointer(value unsafe.Pointer) uint64 {
	return uint64(uintptr(value))
}

func emval_get_global(name *c.Char) Value {
	words := [8]uint64{emvalPointer(unsafe.Pointer(name))}
	emvalInvokeV1(emvalHostGetGlobalV1, &words)
	return Value{ref: uintptr(words[0])}
}

func emval_new_double(value float64) Value {
	words := [8]uint64{*(*uint64)(unsafe.Pointer(&value))}
	emvalInvokeV1(emvalHostNewDoubleV1, &words)
	return Value{ref: uintptr(words[0])}
}

func emval_new_string(value *c.Char) Value {
	words := [8]uint64{emvalPointer(unsafe.Pointer(value))}
	emvalInvokeV1(emvalHostNewStringV1, &words)
	return Value{ref: uintptr(words[0])}
}

func emval_new_object() Value {
	words := [8]uint64{}
	emvalInvokeV1(emvalHostNewObjectV1, &words)
	return Value{ref: uintptr(words[0])}
}

func emval_new_array() Value {
	words := [8]uint64{}
	emvalInvokeV1(emvalHostNewArrayV1, &words)
	return Value{ref: uintptr(words[0])}
}

func emval_set_property(object, key, value Value) {
	words := [8]uint64{uint64(object.ref), uint64(key.ref), uint64(value.ref)}
	emvalInvokeV1(emvalHostSetPropertyV1, &words)
}

func emval_get_property(object, key Value) Value {
	words := [8]uint64{uint64(object.ref), uint64(key.ref)}
	emvalInvokeV1(emvalHostGetPropertyV1, &words)
	return Value{ref: uintptr(words[0])}
}

func emval_delete(object, property Value) bool {
	words := [8]uint64{uint64(object.ref), uint64(property.ref)}
	emvalInvokeV1(emvalHostDeleteV1, &words)
	return words[0] != 0
}

func emval_is_number(value Value) bool {
	words := [8]uint64{uint64(value.ref)}
	emvalInvokeV1(emvalHostIsNumberV1, &words)
	return words[0] != 0
}

func emval_is_string(value Value) bool {
	words := [8]uint64{uint64(value.ref)}
	emvalInvokeV1(emvalHostIsStringV1, &words)
	return words[0] != 0
}

func emval_in(item, object Value) bool {
	words := [8]uint64{uint64(item.ref), uint64(object.ref)}
	emvalInvokeV1(emvalHostContainsV1, &words)
	return words[0] != 0
}

func emval_typeof(value Value) Value {
	words := [8]uint64{uint64(value.ref)}
	emvalInvokeV1(emvalHostTypeOfV1, &words)
	return Value{ref: uintptr(words[0])}
}

func emval_instanceof(object, constructor Value) bool {
	words := [8]uint64{uint64(object.ref), uint64(constructor.ref)}
	emvalInvokeV1(emvalHostInstanceOfV1, &words)
	return words[0] != 0
}

func emval_as_double(value Value) float64 {
	words := [8]uint64{uint64(value.ref)}
	emvalInvokeV1(emvalHostAsDoubleV1, &words)
	return *(*float64)(unsafe.Pointer(&words[0]))
}

func emval_as_string(value Value) string {
	words := [8]uint64{uint64(value.ref)}
	emvalInvokeV1(emvalHostStringSizeV1, &words)
	size := uintptr(words[0])
	if size == 0 {
		return ""
	}
	bytes := make([]byte, int(size))
	words = [8]uint64{
		uint64(value.ref),
		emvalPointer(unsafe.Pointer(&bytes[0])),
		uint64(size),
	}
	emvalInvokeV1(emvalHostCopyStringV1, &words)
	if copied := uintptr(words[0]); copied != size {
		panic("syscall/js: host returned an inconsistent string size")
	}
	return string(bytes)
}

func emval_equals(first, second Value) bool {
	words := [8]uint64{uint64(first.ref), uint64(second.ref)}
	emvalInvokeV1(emvalHostEqualsV1, &words)
	return words[0] != 0
}

func emval_method_call(object Value, name *c.Char, args *Value, nargs c.Int, err *c.Int) Value {
	words := [8]uint64{
		uint64(object.ref),
		emvalPointer(unsafe.Pointer(name)),
		emvalPointer(unsafe.Pointer(args)),
		uint64(uint32(nargs)),
	}
	emvalInvokeV1(emvalHostMethodCallV1, &words)
	*err = c.Int(uint32(words[1]))
	return Value{ref: uintptr(words[0])}
}

func emval_call(fn Value, args *Value, nargs c.Int, kind c.Int, err *c.Int) Value {
	words := [8]uint64{
		uint64(fn.ref),
		emvalPointer(unsafe.Pointer(args)),
		uint64(uint32(nargs)),
		uint64(uint32(kind)),
	}
	emvalInvokeV1(emvalHostCallV1, &words)
	*err = c.Int(uint32(words[1]))
	return Value{ref: uintptr(words[0])}
}

func emval_memory_view_uint8(length c.SizeT, data *c.Uint8T) Value {
	words := [8]uint64{uint64(length), emvalPointer(unsafe.Pointer(data))}
	emvalInvokeV1(emvalHostMemoryViewUint8V1, &words)
	return Value{ref: uintptr(words[0])}
}

func emval_dump(value Value) {
	words := [8]uint64{uint64(value.ref)}
	emvalInvokeV1(emvalHostDumpV1, &words)
}
