//go:build !wasip1 && !wasip2 && !wasm_unknown

/*
 * Copyright (c) 2024 The XGo Authors (xgo.dev). All rights reserved.
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
	"unsafe"

	"github.com/xgo-dev/llgo/runtime/abi"
	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/clite/bitcast"
)

// PrintBatchV1 emits one complete source-level print/println operation. The
// compiler deliberately gives the caller a single managed child edge; this
// loop keeps the number of static suspension sites independent of operand
// count while the existing Print* leaves retain their blocking-I/O semantics.
func PrintBatchV1(args []PrintArgV1, flags uint8) {
	newline := flags&abi.PrintFlagNewlineV1 != 0
	for index := range args {
		if newline && index != 0 {
			PrintByte(' ')
		}
		arg := args[index]
		switch abi.PrintKindV1(arg.kind) {
		case abi.PrintBoolV1:
			PrintBool(arg.word != 0)
		case abi.PrintIntV1:
			PrintInt(int64(arg.word))
		case abi.PrintUintV1:
			PrintUint(arg.word)
		case abi.PrintFloatV1:
			PrintFloat(bitcast.ToFloat64(int64(arg.word)))
		case abi.PrintComplexV1:
			PrintComplex(complex(
				bitcast.ToFloat64(int64(arg.word)),
				bitcast.ToFloat64(int64(arg.extra)),
			))
		case abi.PrintPointerV1:
			PrintPointer(arg.pointer)
		case abi.PrintStringV1:
			PrintString(String{arg.pointer, int(arg.word)})
		case abi.PrintSliceV1:
			PrintSlice(Slice{arg.pointer, int(arg.word), int(arg.extra)})
		case abi.PrintEfaceV1:
			PrintEface(Eface{(*_type)(arg.pointer), arg.aux})
		case abi.PrintIfaceV1:
			PrintIface(Iface{(*itab)(arg.pointer), arg.aux})
		default:
			// Only the compiler creates this internal descriptor. Stop rather
			// than interpreting a mismatched ABI as a different physical type.
			args[index].pointer = nil
			args[index].aux = nil
			return
		}
		// The caller reuses one max-capacity frame scratch. Clear its pointer
		// words after the operand is consumed so a later shorter print does not
		// retain objects referenced only by an older operation.
		args[index].pointer = nil
		args[index].aux = nil
		arg.pointer = nil
		arg.aux = nil
	}
	if newline {
		PrintByte('\n')
	}
}

func PrintBool(v bool) {
	if v {
		printGoString("true")
		return
	}
	printGoString("false")
}

func PrintByte(v byte) {
	c.Fputc(c.Int(v), c.Stderr)
}

func PrintUint(v uint64) {
	var buf [32]byte
	n := c.Snprintf(
		(*c.Char)(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		printFormatPrefixUInt,
		v,
	)
	printFormattedBuffer(unsafe.Pointer(&buf[0]), n, uintptr(len(buf)))
}

func PrintInt(v int64) {
	var buf [32]byte
	n := c.Snprintf(
		(*c.Char)(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		printFormatPrefixInt,
		v,
	)
	printFormattedBuffer(unsafe.Pointer(&buf[0]), n, uintptr(len(buf)))
}

func PrintFloat(v float64) {
	printGoString(formatFloat(v))
}

func PrintComplex(v complex128) {
	printGoString(formatComplex(v))
}

func PrintHex(v uint64) {
	var buf [32]byte
	n := c.Snprintf(
		(*c.Char)(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		printFormatPrefixHex,
		v,
	)
	printFormattedBuffer(unsafe.Pointer(&buf[0]), n, uintptr(len(buf)))
}

func PrintPointer(p unsafe.Pointer) {
	// Match Go's builtin print/println pointer formatting (0x... even for nil).
	printGoString("0x")
	PrintHex(uint64(bitcast.FromPointer(p)))
}

func PrintString(s String) {
	c.Fwrite(s.data, 1, uintptr(s.len), c.Stderr)
}

// printGoString writes the length-delimited Go representation directly.
// Besides avoiding an unnecessary copy and trailing NUL, this keeps builtin
// printing valid in a stackless coroutine: llvm.coro cannot retain a
// variable-sized native-stack alloca across a suspension.
func printGoString(s string) {
	PrintString(*(*String)(unsafe.Pointer(&s)))
}

func printFormattedBuffer(data unsafe.Pointer, n c.Int, capacity uintptr) {
	if n <= 0 || capacity == 0 {
		return
	}
	count := uintptr(n)
	if count >= capacity {
		count = capacity - 1
	}
	c.Fwrite(data, 1, count, c.Stderr)
}

func PrintSlice(s Slice) {
	print("[", s.len, "/", s.cap, "]", s.data)
}

func PrintEface(e Eface) {
	print("(", e._type, ",", e.data, ")")
}

func PrintIface(i Iface) {
	print("(", i.tab, ",", i.data, ")")
}
