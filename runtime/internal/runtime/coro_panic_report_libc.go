//go:build !wasip1 && !wasip2 && !wasm_unknown

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
	"unsafe"

	"github.com/goplus/llgo/runtime/abi"
	c "github.com/goplus/llgo/runtime/internal/clite"
	clitedebug "github.com/goplus/llgo/runtime/internal/clite/debug"
	"github.com/goplus/llgo/runtime/internal/coro"
)

const coroTerminalPanicFrameLimit = 4096

// coroPanicTerminalFputc is deliberately a distinct declaration identity from
// coroTerminalFputc. The latter is a private synchronous leaf used by managed
// fail-stop adapters; this occurrence exists only in the raw no-return command
// reporter after scheduler ownership has ended. The closed-use planner can
// therefore infer its conservative raw-host-only contract without adding a
// source annotation or mixing the two execution domains.
//
//go:linkname coroPanicTerminalFputc C.__llgo_coro_panic_fputc_v1
func coroPanicTerminalFputc(ch c.Int, fp c.FilePtr) c.Int

func coroTerminalWriteString(text string) {
	for index := 0; index < len(text); index++ {
		coroPanicTerminalFputc(c.Int(text[index]), c.Stderr)
	}
}

func coroTerminalWriteUint(value uint64) {
	var digits [20]byte
	index := len(digits)
	for {
		index--
		digits[index] = byte(value%10) + '0'
		value /= 10
		if value == 0 {
			break
		}
	}
	for ; index < len(digits); index++ {
		coroPanicTerminalFputc(c.Int(digits[index]), c.Stderr)
	}
}

func coroTerminalWriteInt(value int64) {
	if value >= 0 {
		coroTerminalWriteUint(uint64(value))
		return
	}
	coroPanicTerminalFputc(c.Int('-'), c.Stderr)
	// -(MinInt64) is not representable. Convert the magnitude without a
	// signed overflow.
	coroTerminalWriteUint(uint64(-(value + 1)) + 1)
}

func coroTerminalWriteHex(value uintptr) {
	coroTerminalWriteString("0x")
	var digits [2 * unsafe.Sizeof(uintptr(0))]byte
	const alphabet = "0123456789abcdef"
	index := len(digits)
	for {
		index--
		digits[index] = alphabet[value&15]
		value >>= 4
		if value == 0 {
			break
		}
	}
	for ; index < len(digits); index++ {
		coroPanicTerminalFputc(c.Int(digits[index]), c.Stderr)
	}
}

func coroTerminalHasSuffix(text, suffix string) bool {
	if len(suffix) > len(text) {
		return false
	}
	offset := len(text) - len(suffix)
	for index := 0; index < len(suffix); index++ {
		if text[offset+index] != suffix[index] {
			return false
		}
	}
	return true
}

func coroTerminalWriteType(typ *_type) {
	if typ == nil {
		coroTerminalWriteString("<nil>")
		return
	}
	if typ.TFlag&abi.TFlagExtraStar != 0 {
		coroPanicTerminalFputc(c.Int('*'), c.Stderr)
	}
	coroTerminalWriteString(typ.Str_)
}

func coroTerminalWriteTypedPrefix(typ *_type, builtin string) bool {
	if typ.TFlag&abi.TFlagExtraStar == 0 && typ.Str_ == builtin {
		return false
	}
	coroTerminalWriteType(typ)
	coroPanicTerminalFputc(c.Int('('), c.Stderr)
	return true
}

func coroTerminalWritePanicValue(record coro.PanicRecordSnapshot) {
	typ := (*_type)(record.TypeWord)
	if typ == nil {
		coroTerminalWriteString("nil")
		return
	}
	direct := typ.IsDirectIface()
	closeTyped := false
	switch typ.Kind() {
	case abi.String:
		if record.DataWord == nil {
			coroTerminalWriteType(typ)
			coroTerminalWriteString("(\"\")")
			return
		}
		text := *(*string)(record.DataWord)
		switch {
		case typ.TFlag&abi.TFlagExtraStar == 0 && typ.Str_ == "string",
			coroTerminalHasSuffix(typ.Str_, ".plainError"),
			coroTerminalHasSuffix(typ.Str_, ".typeAssertionErrorString"):
			coroTerminalWriteString(text)
		case coroTerminalHasSuffix(typ.Str_, ".errorString"):
			coroTerminalWriteString("runtime error: ")
			coroTerminalWriteString(text)
		default:
			coroTerminalWriteType(typ)
			coroTerminalWriteString("(\"")
			coroTerminalWriteString(text)
			coroTerminalWriteString("\")")
		}
		return
	case abi.Bool:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "bool")
		value := record.DataWord != nil
		if !direct {
			value = record.DataWord != nil && *(*bool)(record.DataWord)
		}
		if value {
			coroTerminalWriteString("true")
		} else {
			coroTerminalWriteString("false")
		}
	case abi.Int:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "int")
		if direct {
			coroTerminalWriteInt(int64(int(uintptr(record.DataWord))))
		} else {
			coroTerminalWriteInt(int64(*(*int)(record.DataWord)))
		}
	case abi.Int8:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "int8")
		if direct {
			coroTerminalWriteInt(int64(int8(uintptr(record.DataWord))))
		} else {
			coroTerminalWriteInt(int64(*(*int8)(record.DataWord)))
		}
	case abi.Int16:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "int16")
		if direct {
			coroTerminalWriteInt(int64(int16(uintptr(record.DataWord))))
		} else {
			coroTerminalWriteInt(int64(*(*int16)(record.DataWord)))
		}
	case abi.Int32:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "int32")
		if direct {
			coroTerminalWriteInt(int64(int32(uintptr(record.DataWord))))
		} else {
			coroTerminalWriteInt(int64(*(*int32)(record.DataWord)))
		}
	case abi.Int64:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "int64")
		if direct {
			coroTerminalWriteInt(int64(uintptr(record.DataWord)))
		} else {
			coroTerminalWriteInt(*(*int64)(record.DataWord))
		}
	case abi.Uint:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "uint")
		if direct {
			coroTerminalWriteUint(uint64(uintptr(record.DataWord)))
		} else {
			coroTerminalWriteUint(uint64(*(*uint)(record.DataWord)))
		}
	case abi.Uint8:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "uint8")
		if direct {
			coroTerminalWriteUint(uint64(uint8(uintptr(record.DataWord))))
		} else {
			coroTerminalWriteUint(uint64(*(*uint8)(record.DataWord)))
		}
	case abi.Uint16:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "uint16")
		if direct {
			coroTerminalWriteUint(uint64(uint16(uintptr(record.DataWord))))
		} else {
			coroTerminalWriteUint(uint64(*(*uint16)(record.DataWord)))
		}
	case abi.Uint32:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "uint32")
		if direct {
			coroTerminalWriteUint(uint64(uint32(uintptr(record.DataWord))))
		} else {
			coroTerminalWriteUint(uint64(*(*uint32)(record.DataWord)))
		}
	case abi.Uint64:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "uint64")
		if direct {
			coroTerminalWriteUint(uint64(uintptr(record.DataWord)))
		} else {
			coroTerminalWriteUint(*(*uint64)(record.DataWord))
		}
	case abi.Uintptr:
		closeTyped = coroTerminalWriteTypedPrefix(typ, "uintptr")
		if direct {
			coroTerminalWriteUint(uint64(uintptr(record.DataWord)))
		} else {
			coroTerminalWriteUint(uint64(*(*uintptr)(record.DataWord)))
		}
	case abi.Pointer:
		if coroTerminalHasSuffix(typ.Str_, "PanicNilError") {
			coroTerminalWriteString("panic called with nil argument")
			return
		}
		coroTerminalWriteType(typ)
		coroPanicTerminalFputc(c.Int('('), c.Stderr)
		coroTerminalWriteHex(uintptr(record.DataWord))
		closeTyped = true
	default:
		coroPanicTerminalFputc(c.Int('('), c.Stderr)
		coroTerminalWriteType(typ)
		coroTerminalWriteString(") ")
		coroTerminalWriteHex(uintptr(record.DataWord))
		return
	}
	if closeTyped {
		coroPanicTerminalFputc(c.Int(')'), c.Stderr)
	}
}

func coroTerminalWriteCString(text *c.Char) bool {
	if text == nil {
		return false
	}
	for index := uintptr(0); index < coroTerminalPanicFrameLimit; index++ {
		ch := *(*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(text)) + index))
		if ch == 0 {
			return index != 0
		}
		coroPanicTerminalFputc(c.Int(ch), c.Stderr)
	}
	return true
}

// coroTerminalWriteWorkerFaultFrames prints the exact native prefix captured
// by the C-worker landing pad before the retained stackless Go frames. The
// worker result records the prefix length explicitly; terminal reporting never
// guesses whether an address is native by inspecting pointer bits and never
// asks a worker thread to traverse Go scheduler state.
func coroTerminalWriteWorkerFaultFrames(ctx *coroRuntimeContext) {
	if ctx == nil {
		return
	}
	store := loadPanicPCStore(&ctx.g)
	if store == nil || store.fault == 0 || store.native <= 0 {
		return
	}
	count := int(store.native)
	if count > len(store.pcs) || count > coroTerminalPanicFrameLimit {
		coroRuntimeAbort("invalid coroutine C fault trace prefix")
	}
	for index := 0; index < count; index++ {
		pc := store.pcs[index]
		if pc <= 1 {
			coroRuntimeAbort("invalid coroutine C fault trace pc")
		}
		// Stored PCs follow runtime.Callers' return-PC convention.
		rawPC := pc - 1
		var info clitedebug.Info
		if clitedebug.Addrinfo(rawPC, &info) == 0 || !coroTerminalWriteCString(info.Sname) {
			coroTerminalWriteString("unknown")
		}
		coroTerminalWriteString("(...)\n\tpc=")
		coroTerminalWriteHex(rawPC)
		coroPanicTerminalFputc(c.Int('\n'), c.Stderr)
	}
}

func coroTerminalWritePanicFrames(g *coro.G, ctx *coroRuntimeContext) {
	coroTerminalWriteString("\n\ngoroutine ")
	if ctx.g.goid == 0 {
		coroTerminalWriteUint(1)
	} else {
		coroTerminalWriteUint(ctx.g.goid)
	}
	coroTerminalWriteString(" [running]:\n")
	coroTerminalWriteWorkerFaultFrames(ctx)
	cursor := coro.FirstPanicTraceFrame(g)
	if cursor == nil {
		coroRuntimeAbort("coroutine program panic trace is empty")
	}
	for count := uint32(0); cursor != nil; count++ {
		if count >= coroTerminalPanicFrameLimit {
			coroRuntimeAbort("coroutine program panic trace is too deep")
		}
		frame, next, ok := coro.LoadPanicTraceFrame(g, cursor)
		if !ok {
			coroRuntimeAbort("invalid coroutine program panic trace")
		}
		cursor = next
		if frame.Hidden {
			continue
		}
		coroTerminalWriteString(frame.Function)
		coroTerminalWriteString("(...)\n\t")
		if frame.File == "" {
			coroTerminalWriteString("<unknown>")
		} else {
			coroTerminalWriteString(frame.File)
		}
		if frame.Line > 0 {
			coroPanicTerminalFputc(c.Int(':'), c.Stderr)
			coroTerminalWriteUint(uint64(frame.Line))
		}
		coroPanicTerminalFputc(c.Int('\n'), c.Stderr)
	}
}

// __llgo_coro_program_report_panic_v1 is the native command's sole terminal
// owner for a canonical V2 Panic result. It runs only after the bounded
// scheduler call returned and every LLVM coroutine frame was destroyed. The
// published task record and task-local logical caller stack therefore provide
// all input without re-entering managed code, allocating, or invoking a user
// Error/String method.
//
//export __llgo_coro_program_report_panic_v1
func __llgo_coro_program_report_panic_v1(gPointer unsafe.Pointer) {
	g := (*coro.G)(gPointer)
	if g != &coroProgramGV1State ||
		coroProgramLifecycleV1State != coroProgramFailedV1 ||
		coroProgramExecutorBoundV1State ||
		coroProgramContinuationV1State != coroProgramContinuationNoneV1 ||
		!coroProgramDriveAdmissionV1State.CanRelease() {
		coroRuntimeAbort("invalid coroutine program panic report")
	}
	record, published := coro.LoadPanicRecord(g)
	ctx := (*coroRuntimeContext)(coro.TaskLocal(g))
	if !published || record.Status != coro.ExplicitStatusPanic ||
		record.TypeWord == nil || !validCoroRuntimeContext(ctx) {
		coroRuntimeAbort("invalid coroutine program panic record")
	}
	coroTerminalWriteString("panic: ")
	coroTerminalWritePanicValue(record)
	coroTerminalWritePanicFrames(g, ctx)
	coroPanicTerminalFputc(c.Int('\n'), c.Stderr)
	c.Exit(2)
	for {
	}
}
