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

package bitcast

import (
	"testing"
	"unsafe"
)

func TestBitcastPreservesExactBits(t *testing.T) {
	for _, bits := range []uint64{
		0,
		1,
		0x8000000000000000, // negative zero
		0x3ff0000000000000, // 1
		0x7ff8000000001234, // NaN with a non-zero payload
	} {
		if got := uint64(FromFloat64(ToFloat64(int64(bits)))); got != bits {
			t.Fatalf("64-bit roundtrip = %#016x, want %#016x", got, bits)
		}
	}
	for _, bits := range []uint32{
		0,
		1,
		0x80000000, // negative zero
		0x3f800000, // 1
		0x7fc01234, // NaN with a non-zero payload
	} {
		if got := uint32(FromFloat32(ToFloat32(int32(bits)))); got != bits {
			t.Fatalf("32-bit roundtrip = %#08x, want %#08x", got, bits)
		}
	}
}

func TestFromPointerPreservesMachineWord(t *testing.T) {
	value := byte(1)
	pointer := unsafe.Pointer(&value)
	if got, want := FromPointer(pointer), uintptr(pointer); got != want {
		t.Fatalf("pointer word = %#x, want %#x", got, want)
	}
}
