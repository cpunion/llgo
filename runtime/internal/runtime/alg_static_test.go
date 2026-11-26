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

	"github.com/xgo-dev/llgo/runtime/abi"
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
	if typehashImpl(stringType, unsafe.Pointer(&s1), 17) != typehashImpl(stringType, unsafe.Pointer(&s2), 17) {
		t.Fatal("equal strings have different hashes")
	}

	floatType := staticTestType(abi.Float64, unsafe.Sizeof(float64(0)), "float64")
	zero := 0.0
	negativeZeroBits := uint64(1 << 63)
	negativeZero := *(*float64)(unsafe.Pointer(&negativeZeroBits))
	if !typeequal(floatType, unsafe.Pointer(&zero), unsafe.Pointer(&negativeZero)) {
		t.Fatal("+0 and -0 must compare equal")
	}
	if typehashImpl(floatType, unsafe.Pointer(&zero), 19) != typehashImpl(floatType, unsafe.Pointer(&negativeZero), 19) {
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
	if typehashImpl(&arrayType.Type, unsafe.Pointer(&a), 23) != typehashImpl(&arrayType.Type, unsafe.Pointer(&b), 23) {
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
	if typehashImpl(&structType.Type, unsafe.Pointer(&r1), 29) != typehashImpl(&structType.Type, unsafe.Pointer(&r2), 29) {
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
	if !EfaceEqual(a, b) {
		t.Fatal("EfaceEqual reports equal ints as unequal")
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
	EfaceEqual(u, u)
}

func TestInterfaceEqualityAcceptsDistinctItabsForSameDynamicType(t *testing.T) {
	intType := staticTestType(abi.Int, unsafe.Sizeof(int(0)), "int")
	intType.TFlag = abi.TFlagRegularMemory
	interfaceType := &abi.InterfaceType{}
	staticTab := &itab{inter: interfaceType, _type: intType}
	dynamicTab := &itab{inter: interfaceType, _type: intType}
	x, y := 17, 17
	staticValue := iface{tab: staticTab, data: unsafe.Pointer(&x)}
	dynamicValue := iface{tab: dynamicTab, data: unsafe.Pointer(&y)}
	if !interequal(unsafe.Pointer(&staticValue), unsafe.Pointer(&dynamicValue)) {
		t.Fatal("equal interface values with static and dynamic itabs compare unequal")
	}

	otherType := staticTestType(abi.Int, unsafe.Sizeof(int(0)), "otherInt")
	otherType.TFlag = abi.TFlagRegularMemory
	otherValue := iface{tab: &itab{inter: interfaceType, _type: otherType}, data: unsafe.Pointer(&y)}
	if interequal(unsafe.Pointer(&staticValue), unsafe.Pointer(&otherValue)) {
		t.Fatal("interface values with different dynamic types compare equal")
	}
}

func TestTypeHashAndEqualFastMatchSlow(t *testing.T) {
	intType := staticTestType(abi.Int, unsafe.Sizeof(int(0)), "int")
	intType.TFlag = abi.TFlagRegularMemory
	u32Type := staticTestType(abi.Uint32, 4, "uint32")
	u32Type.TFlag = abi.TFlagRegularMemory
	stringType := staticTestType(abi.String, unsafe.Sizeof(""), "string")

	// Direct-iface and non-direct-iface empty interface keys
	efaceType := &abi.InterfaceType{
		Type: abi.Type{
			Size_: unsafe.Sizeof(eface{}),
			Kind_: uint8(abi.Interface),
			Str_:  "interface {}",
		},
	}
	ptrType := staticTestType(abi.Pointer, unsafe.Sizeof(uintptr(0)), "*int")
	ptrType.TFlag = abi.TFlagRegularMemory
	ptrType.Kind_ |= abi.KindDirectIface
	target1, target2 := 1, 2
	e1 := eface{_type: ptrType, data: unsafe.Pointer(&target1)}
	e2 := eface{_type: ptrType, data: unsafe.Pointer(&target1)}
	e3 := eface{_type: ptrType, data: unsafe.Pointer(&target2)}
	for _, seed := range []uintptr{0, 17, 0x9e3779b9} {
		hFast, ok := typehashFast(&efaceType.Type, unsafe.Pointer(&e1), seed)
		if !ok {
			t.Fatal("typehashFast did not handle direct-iface pointer")
		}
		if hSlow := typehashSlow(&efaceType.Type, unsafe.Pointer(&e1), seed); hFast != hSlow {
			t.Fatalf("direct-iface hash mismatch: fast=%#x slow=%#x", hFast, hSlow)
		}
	}
	if eq, ok := typeequalFast(&efaceType.Type, unsafe.Pointer(&e1), unsafe.Pointer(&e2)); !ok || !eq {
		t.Fatalf("direct-iface equal = (%t, %t), want (true, true)", eq, ok)
	}
	if eq, ok := typeequalFast(&efaceType.Type, unsafe.Pointer(&e1), unsafe.Pointer(&e3)); !ok || eq {
		t.Fatalf("direct-iface unequal = (%t, %t), want (false, true)", eq, ok)
	}

	s1, s2 := "alpha-key", "alpha-key"
	es1 := eface{_type: stringType, data: unsafe.Pointer(&s1)}
	es2 := eface{_type: stringType, data: unsafe.Pointer(&s2)}
	if hFast, ok := typehashFast(&efaceType.Type, unsafe.Pointer(&es1), 42); !ok || hFast != typehashSlow(&efaceType.Type, unsafe.Pointer(&es1), 42) {
		t.Fatalf("string eface fast/slow mismatch: fast=%#x ok=%t slow=%#x", hFast, ok, typehashSlow(&efaceType.Type, unsafe.Pointer(&es1), 42))
	}
	if eq, ok := typeequalFast(&efaceType.Type, unsafe.Pointer(&es1), unsafe.Pointer(&es2)); !ok || !eq {
		t.Fatalf("string eface equal = (%t, %t), want (true, true)", eq, ok)
	}

	// 4-field struct key (matches net/http.connectMethodKey shape)
	type fourKey struct {
		A string
		B string
		C uint32
		D int
	}
	sType := &abi.StructType{
		Type: abi.Type{
			Size_: unsafe.Sizeof(fourKey{}),
			Kind_: uint8(abi.Struct),
			Str_:  "fourKey",
		},
		Fields: []abi.StructField{
			{Name_: "A", Typ: stringType, Offset: unsafe.Offsetof(fourKey{}.A)},
			{Name_: "B", Typ: stringType, Offset: unsafe.Offsetof(fourKey{}.B)},
			{Name_: "C", Typ: u32Type, Offset: unsafe.Offsetof(fourKey{}.C)},
			{Name_: "D", Typ: intType, Offset: unsafe.Offsetof(fourKey{}.D)},
		},
	}
	k1 := fourKey{"http", "127.0.0.1:8080", 7, 99}
	k2 := fourKey{"http", "127.0.0.1:8080", 7, 99}
	k3 := fourKey{"http", "127.0.0.1:8080", 8, 99}
	hFast, ok := typehashFast(&sType.Type, unsafe.Pointer(&k1), 12345)
	if !ok {
		t.Fatal("typehashFast did not handle 4-field struct")
	}
	if hSlow := typehashSlow(&sType.Type, unsafe.Pointer(&k1), 12345); hFast != hSlow {
		t.Fatalf("4-field struct hash mismatch: fast=%#x slow=%#x", hFast, hSlow)
	}
	if eq, ok := typeequalFast(&sType.Type, unsafe.Pointer(&k1), unsafe.Pointer(&k2)); !ok || !eq {
		t.Fatalf("4-field struct equal = (%t, %t), want (true, true)", eq, ok)
	}
	if eq, ok := typeequalFast(&sType.Type, unsafe.Pointer(&k1), unsafe.Pointer(&k3)); !ok || eq {
		t.Fatalf("4-field struct unequal = (%t, %t), want (false, true)", eq, ok)
	}
}

func TestMemhashShortAndWordMatchReference(t *testing.T) {
	buf := make([]byte, 64)
	for i := range buf {
		buf[i] = byte(i*37 + 11)
	}
	for _, seed := range []uintptr{0, 1, 0x12345678} {
		for n := uintptr(0); n <= 48; n++ {
			var p unsafe.Pointer
			if n > 0 {
				p = unsafe.Pointer(&buf[0])
			}
			got, ok := memhashShort(p, seed, n)
			if n <= 24 && !ok {
				t.Fatalf("memhashShort rejected len %d", n)
			}
			if ok && n > 16 {
				want := memhashLong(p, seed, n)
				if got != want {
					t.Fatalf("memhashShort(len=%d, seed=%#x)=%#x != memhashLong=%#x", n, seed, got, want)
				}
			}
		}
		v32 := uint32(0x13579bdf)
		if got, want := memhashWord32(v32, seed), memhash32(unsafe.Pointer(&v32), seed); got != want {
			t.Fatalf("memhashWord32=%#x != memhash32=%#x", got, want)
		}
		v64 := uint64(0x0123456789abcdef)
		if got, want := memhashWord64(v64, seed), memhash64(unsafe.Pointer(&v64), seed); got != want {
			t.Fatalf("memhashWord64=%#x != memhash64=%#x", got, want)
		}
	}
}
