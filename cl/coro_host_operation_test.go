//go:build !llgo

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

package cl

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

const coroHostOperationValidationSource = `package hostop

import "unsafe"

func controlled(
	opcode, deadlineLo, deadlineHi, timeoutErrno, controlKey, controlLane, controlEpoch, fd uintptr,
	buffer unsafe.Pointer, bufferSize, flags uintptr,
	address unsafe.Pointer, addressSize uintptr,
) (uintptr, uintptr, uintptr)

func plain(
	opcode, a0, a1, a2, a3, a4, a5, a6, a7, a8 uintptr,
) (uintptr, uintptr, uintptr)

func short(opcode, a0, a1, a2, a3 uintptr) (uintptr, uintptr, uintptr)

func pointerMetadata(
	opcode uintptr, deadlineLo unsafe.Pointer,
	deadlineHi, timeoutErrno, controlKey, controlLane, controlEpoch uintptr,
) (uintptr, uintptr, uintptr)

func tooMany(
	opcode, a0, a1, a2, a3, a4, a5, a6, a7, a8, a9 uintptr,
) (uintptr, uintptr, uintptr)

func twoResults(opcode uintptr) (uintptr, uintptr)

func ValidControlled(buffer, address unsafe.Pointer) (uintptr, uintptr, uintptr) {
	return controlled(1<<31|0x2000b, 0, 0, 116, 1, 1, 0, 7, buffer, 1, 0, address, 32)
}

func ValidPlain() (uintptr, uintptr, uintptr) {
	return plain(0x10001, 0, 1, 2, 3, 4, 5, 6, 7, 8)
}

func MissingMetadata() (uintptr, uintptr, uintptr) {
	return short(1<<31|0x10002, 0, 0, 116, 1)
}

func DynamicOpcode(opcode uintptr, buffer, address unsafe.Pointer) (uintptr, uintptr, uintptr) {
	return controlled(opcode, 0, 0, 116, 1, 1, 0, 7, buffer, 1, 0, address, 32)
}

func PointerMetadata(pointer unsafe.Pointer) (uintptr, uintptr, uintptr) {
	return pointerMetadata(1<<31|0x10002, pointer, 0, 116, 1, 1, 0)
}

func TooMany() (uintptr, uintptr, uintptr) {
	return tooMany(0x10001, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9)
}

func TwoResults() (uintptr, uintptr) {
	return twoResults(0x10001)
}
`

func TestCoroHostOperationProgramIRCallShape(t *testing.T) {
	pkg, _, _ := buildGoSSAPkg(t, coroHostOperationValidationSource)
	call := func(name string) *ssa.Call {
		t.Helper()
		fn := pkg.Func(name)
		if fn == nil {
			t.Fatalf("missing fixture function %q", name)
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if value, ok := instruction.(*ssa.Call); ok {
					return value
				}
			}
		}
		t.Fatalf("fixture function %q has no call", name)
		return nil
	}

	controlled, err := planCoroHostOperationCallShape(call("ValidControlled"))
	if err != nil || controlled.opcode != 0x2000b ||
		controlled.metadataWords != coroHostOperationDeadlineMetadataWordsV1 ||
		controlled.argumentCount != 6 || controlled.pointerMask != 0x12 {
		t.Fatalf("controlled shape = %+v, %v", controlled, err)
	}
	plain, err := planCoroHostOperationCallShape(call("ValidPlain"))
	if err != nil || plain.opcode != 0x10001 || plain.metadataWords != 0 ||
		plain.argumentCount != coroWorkerMaxArgsV1 || plain.pointerMask != 0 {
		t.Fatalf("plain shape = %+v, %v", plain, err)
	}
	for _, test := range []struct {
		name string
		want string
	}{
		{name: "MissingMetadata", want: "invalid deadline metadata"},
		{name: "DynamicOpcode", want: "compile-time uint32 opcode"},
		{name: "PointerMetadata", want: "deadline metadata 0 is not uintptr-shaped"},
		{name: "TooMany", want: "more than 9 host argument words"},
		{name: "TwoResults", want: "exactly three uintptr results"},
	} {
		_, err := planCoroHostOperationCallShape(call(test.name))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%s validation error = %v; want %q", test.name, err, test.want)
		}
	}
}
