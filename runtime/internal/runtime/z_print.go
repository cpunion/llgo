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

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/clite/bitcast"
)

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
