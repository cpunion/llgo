//go:build llgo

/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package runtime

import (
	"testing"
	"unsafe"

	"github.com/goplus/llgo/runtime/abi"
)

func staticTestType(kind abi.Kind, size uintptr, name string) *abi.Type {
	return &abi.Type{
		Size_: size,
		Kind_: uint8(kind),
		Str_:  name,
		Equal: func(unsafe.Pointer, unsafe.Pointer) bool {
			panic("runtime type equality callback was invoked")
		},
	}
}

func TestTypeEqualStaticScalars(t *testing.T) {
	intType := staticTestType(abi.Int, unsafe.Sizeof(int(0)), "int")
	intType.TFlag = abi.TFlagRegularMemory
	x, y, z := 42, 42, 7
	if !typeequal(intType, unsafe.Pointer(&x), unsafe.Pointer(&y)) {
		t.Fatal("equal ints compare unequal")
	}
	if typeequal(intType, unsafe.Pointer(&x), unsafe.Pointer(&z)) {
		t.Fatal("unequal ints compare equal")
	}

	stringType := staticTestType(abi.String, unsafe.Sizeof(""), "string")
	s1, s2, s3 := "llgo", "llgo", "coro"
	if !typeequal(stringType, unsafe.Pointer(&s1), unsafe.Pointer(&s2)) {
		t.Fatal("equal strings compare unequal")
	}
	if typeequal(stringType, unsafe.Pointer(&s1), unsafe.Pointer(&s3)) {
		t.Fatal("unequal strings compare equal")
	}
	if typehash(stringType, unsafe.Pointer(&s1), 17) != typehash(stringType, unsafe.Pointer(&s2), 17) {
		t.Fatal("equal strings have different hashes")
	}

	floatType := staticTestType(abi.Float64, unsafe.Sizeof(float64(0)), "float64")
	zero := 0.0
	negativeZeroBits := uint64(1 << 63)
	negativeZero := *(*float64)(unsafe.Pointer(&negativeZeroBits))
	if !typeequal(floatType, unsafe.Pointer(&zero), unsafe.Pointer(&negativeZero)) {
		t.Fatal("+0 and -0 must compare equal")
	}
	if typehash(floatType, unsafe.Pointer(&zero), 19) != typehash(floatType, unsafe.Pointer(&negativeZero), 19) {
		t.Fatal("+0 and -0 must have equal hashes")
	}
	nanBits := uint64(0x7ff8000000000001)
	nan := *(*float64)(unsafe.Pointer(&nanBits))
	if typeequal(floatType, unsafe.Pointer(&nan), unsafe.Pointer(&nan)) {
		t.Fatal("NaN must compare unequal to itself")
	}
}

func TestTypeEqualStaticComposite(t *testing.T) {
	intType := staticTestType(abi.Int, unsafe.Sizeof(int(0)), "int")
	intType.TFlag = abi.TFlagRegularMemory
	stringType := staticTestType(abi.String, unsafe.Sizeof(""), "string")

	type pair [2]int
	arrayType := &abi.ArrayType{
		Type: abi.Type{
			Size_: unsafe.Sizeof(pair{}),
			Kind_: uint8(abi.Array),
			Str_:  "[2]int",
		},
		Elem: intType,
		Len:  2,
	}
	a, b, c := pair{1, 2}, pair{1, 2}, pair{1, 3}
	if !typeequal(&arrayType.Type, unsafe.Pointer(&a), unsafe.Pointer(&b)) {
		t.Fatal("equal arrays compare unequal")
	}
	if typeequal(&arrayType.Type, unsafe.Pointer(&a), unsafe.Pointer(&c)) {
		t.Fatal("unequal arrays compare equal")
	}
	if typehash(&arrayType.Type, unsafe.Pointer(&a), 23) != typehash(&arrayType.Type, unsafe.Pointer(&b), 23) {
		t.Fatal("equal arrays have different hashes")
	}

	type record struct {
		Name string
		IDs  pair
	}
	structType := &abi.StructType{
		Type: abi.Type{
			Size_: unsafe.Sizeof(record{}),
			Kind_: uint8(abi.Struct),
			Str_:  "runtime.record",
		},
		Fields: []abi.StructField{
			{Name_: "Name", Typ: stringType, Offset: unsafe.Offsetof(record{}.Name)},
			{Name_: "IDs", Typ: &arrayType.Type, Offset: unsafe.Offsetof(record{}.IDs)},
		},
	}
	r1, r2, r3 := record{"timer", pair{3, 5}}, record{"timer", pair{3, 5}}, record{"timer", pair{3, 8}}
	if !typeequal(&structType.Type, unsafe.Pointer(&r1), unsafe.Pointer(&r2)) {
		t.Fatal("equal structs compare unequal")
	}
	if typeequal(&structType.Type, unsafe.Pointer(&r1), unsafe.Pointer(&r3)) {
		t.Fatal("unequal structs compare equal")
	}
	if typehash(&structType.Type, unsafe.Pointer(&r1), 29) != typehash(&structType.Type, unsafe.Pointer(&r2), 29) {
		t.Fatal("equal structs have different hashes")
	}
}

func TestTypeEqualStaticInterface(t *testing.T) {
	interfaceType := &abi.InterfaceType{
		Type: abi.Type{
			Size_: unsafe.Sizeof(eface{}),
			Kind_: uint8(abi.Interface),
			Str_:  "interface {}",
		},
	}

	intType := staticTestType(abi.Int, unsafe.Sizeof(int(0)), "int")
	x, y := 11, 11
	a := eface{_type: intType, data: unsafe.Pointer(&x)}
	b := eface{_type: intType, data: unsafe.Pointer(&y)}
	if !typeequal(&interfaceType.Type, unsafe.Pointer(&a), unsafe.Pointer(&b)) {
		t.Fatal("interfaces containing equal ints compare unequal")
	}

	sliceType := &abi.Type{
		Size_: unsafe.Sizeof([]int(nil)),
		Kind_: uint8(abi.Slice),
		Str_:  "[]int",
	}
	s := []int{1}
	u := eface{_type: sliceType, data: unsafe.Pointer(&s)}
	defer func() {
		if recover() == nil {
			t.Fatal("comparing an interface containing a slice did not panic")
		}
	}()
	typeequal(&interfaceType.Type, unsafe.Pointer(&u), unsafe.Pointer(&u))
}
