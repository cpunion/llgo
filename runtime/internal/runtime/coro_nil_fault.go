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

	"github.com/goplus/llgo/runtime/internal/coro"
)

// memoryError is the stable package-owned payload used by both ordinary Go
// nil dereference panics and physical-coroutine explicit status.  Keeping the
// interface global roots its dynamic value after an LLVM coroutine frame has
// been destroyed.
//
// The corresponding declaration in the imported Go runtime source is inside
// a disabled compatibility block in this runtime. Keep the canonical name and
// value here so physical coroutine lowering does not need a private panic
// value or a heap allocation.
var memoryError = error(errorString("invalid memory address or nil pointer dereference"))

var coroIndexBoundsErrorV1 = error(errorString("index out of range"))
var coroChannelSendClosedErrorV1 = error(plainError("send on closed channel"))
var coroUnsafeSliceLenErrorV1 = error(errorString("unsafe.Slice: len out of range"))
var coroUnsafeSliceNilErrorV1 = error(errorString("unsafe.Slice: ptr is nil and len is not zero"))
var coroChannelCloseNilErrorV1 = error(plainError("close of nil channel"))
var coroChannelCloseClosedErrorV1 = error(plainError("close of closed channel"))
var coroUnsafeStringLenErrorV1 = error(errorString("unsafe.String: len out of range"))
var coroUnsafeStringNilErrorV1 = error(errorString("unsafe.String: nil pointer with non-zero length"))

// V1 remains the allocation-free payload for operand-free faults. Slice
// conversion uses the parameterized V2 hook below because Go's boundsError
// message is part of observable panic/recover behavior.
var coroSliceConvertErrorV1 = error(errorString("cannot convert slice to array or pointer to array: length too short"))

// coroBoundsErrorTypeV2 roots the exact dynamic value type used by the
// standard runtime's bounds panics. Parameterized faults allocate only the
// scalar payload object; the interface type word is shared and immutable.
var coroBoundsErrorTypeV2 = error(boundsError{})

const (
	coroFaultNilV1 uint32 = iota + 1
	coroFaultIndexBoundsV1
	coroFaultChannelSendClosedV1
	coroFaultUnsafeSliceLenV1
	coroFaultUnsafeSliceNilV1
	coroFaultChannelCloseNilV1
	coroFaultChannelCloseClosedV1
	coroFaultUnsafeStringLenV1
	coroFaultUnsafeStringNilV1
	coroFaultSliceConvertV1
)

const (
	coroFaultBoundsBaseV2  uint32 = coroFaultSliceConvertV1 + 1
	coroFaultBoundsLimitV2        = coroFaultBoundsBaseV2 + 2*8
)

// coroNilFaultPayloadV1 extracts the concrete empty-interface pair expected by
// coro.PreparePanic from the existing non-empty error interface. This is a
// two-word read only: it performs no interface conversion and cannot allocate.
func coroNilFaultPayloadV1() (typeWord, dataWord unsafe.Pointer) {
	return coroErrorFaultPayloadV1(&memoryError)
}

func coroErrorFaultPayloadV1(value *error) (typeWord, dataWord unsafe.Pointer) {
	if value == nil {
		return nil, nil
	}
	payload := *(*iface)(unsafe.Pointer(value))
	if payload.tab == nil {
		return nil, nil
	}
	return unsafe.Pointer(payload.tab._type), payload.data
}

func coroFaultPayloadV1(kind uint32) (typeWord, dataWord unsafe.Pointer) {
	switch kind {
	case coroFaultNilV1:
		return coroNilFaultPayloadV1()
	case coroFaultIndexBoundsV1:
		return coroErrorFaultPayloadV1(&coroIndexBoundsErrorV1)
	case coroFaultChannelSendClosedV1:
		return coroErrorFaultPayloadV1(&coroChannelSendClosedErrorV1)
	case coroFaultUnsafeSliceLenV1:
		return coroErrorFaultPayloadV1(&coroUnsafeSliceLenErrorV1)
	case coroFaultUnsafeSliceNilV1:
		return coroErrorFaultPayloadV1(&coroUnsafeSliceNilErrorV1)
	case coroFaultChannelCloseNilV1:
		return coroErrorFaultPayloadV1(&coroChannelCloseNilErrorV1)
	case coroFaultChannelCloseClosedV1:
		return coroErrorFaultPayloadV1(&coroChannelCloseClosedErrorV1)
	case coroFaultUnsafeStringLenV1:
		return coroErrorFaultPayloadV1(&coroUnsafeStringLenErrorV1)
	case coroFaultUnsafeStringNilV1:
		return coroErrorFaultPayloadV1(&coroUnsafeStringNilErrorV1)
	case coroFaultSliceConvertV1:
		return coroErrorFaultPayloadV1(&coroSliceConvertErrorV1)
	default:
		return nil, nil
	}
}

func coroFaultBoundsMetadataV2(kind uint32) (code boundsErrorCode, signed, ok bool) {
	if kind < coroFaultBoundsBaseV2 || kind >= coroFaultBoundsLimitV2 {
		return 0, false, false
	}
	offset := kind - coroFaultBoundsBaseV2
	code = boundsErrorCode(offset / 2)
	signed = offset&1 == 0
	return code, signed, code <= boundsSlice3C
}

func coroFaultBoundsPayloadV2(
	arg0 uint64,
	arg1 uintptr,
	signed bool,
	code boundsErrorCode,
) (typeWord, dataWord unsafe.Pointer) {
	typeWord, _ = coroErrorFaultPayloadV1(&coroBoundsErrorTypeV2)
	if typeWord == nil {
		return nil, nil
	}
	payload := (*boundsError)(AllocZ(unsafe.Sizeof(boundsError{})))
	if payload == nil {
		return nil, nil
	}
	*payload = boundsError{
		x:      int64(arg0),
		y:      int(arg1),
		signed: signed,
		code:   code,
	}
	return typeWord, unsafe.Pointer(payload)
}

// coroFaultPayloadV2 adds one exact 64-bit operand and one target-width
// non-negative bound without changing the stable V1 kind namespace.
// Operand-free kinds delegate to V1 only when both words are zero. Slice
// conversion interprets arg0 as the target array length and arg1 as the source
// slice length, matching PanicSliceConvert(x, y). Dynamic index/slice kinds
// encode boundsError.code and signedness in kind so uint64 indexes remain
// observable on wasm32.
//
// The boundsError object must outlive the active LLVM frame: a child fault is
// copied as an interface pair into its parent's CompletionRecord after the
// child frame is destroyed. AllocZ provides GC-visible storage on native,
// tinygogc storage on bare metal, and the configured malloc storage on
// wasm/nogc targets.
func coroFaultPayloadV2(kind uint32, arg0 uint64, arg1 uintptr) (typeWord, dataWord unsafe.Pointer) {
	if kind == coroFaultSliceConvertV1 {
		return coroFaultBoundsPayloadV2(arg0, arg1, true, boundsConvert)
	}
	if code, signed, ok := coroFaultBoundsMetadataV2(kind); ok {
		return coroFaultBoundsPayloadV2(arg0, arg1, signed, code)
	}
	if arg0 != 0 || arg1 != 0 {
		return nil, nil
	}
	return coroFaultPayloadV1(kind)
}

// __llgo_coro_fault_payload_v1 materializes one stable language-fault panic
// pair without publishing a terminal scheduler outcome. Compiler cleanup
// lowering uses it before invoking deferred children so an ordinary recover
// can consume the fault through the same direct-child CompletionRecord as an
// explicit panic. The operation is a pair of global interface-word reads and
// two stores: it allocates no object and owns no scheduler state.
//
//export __llgo_coro_fault_payload_v1
func __llgo_coro_fault_payload_v1(kind uint32, typeOut, dataOut unsafe.Pointer) {
	if typeOut == nil || dataOut == nil {
		coroRuntimeAbort("invalid coroutine fault payload output")
	}
	typeWord, dataWord := coroFaultPayloadV1(kind)
	if typeWord == nil {
		coroRuntimeAbort("invalid coroutine fault payload kind")
	}
	*(*unsafe.Pointer)(typeOut) = typeWord
	*(*unsafe.Pointer)(dataOut) = dataWord
}

// __llgo_coro_fault_payload_v2 materializes an operand-carrying fault payload
// for cleanup/recover lowering. Its output ownership is identical to V1; only
// the payload construction receives two target-width scalar words.
//
//export __llgo_coro_fault_payload_v2
func __llgo_coro_fault_payload_v2(kind uint32, arg0 uint64, arg1 uintptr, typeOut, dataOut unsafe.Pointer) {
	if typeOut == nil || dataOut == nil {
		coroRuntimeAbort("invalid parameterized coroutine fault payload output")
	}
	typeWord, dataWord := coroFaultPayloadV2(kind, arg0, arg1)
	if typeWord == nil {
		coroRuntimeAbort("invalid parameterized coroutine fault payload")
	}
	*(*unsafe.Pointer)(typeOut) = typeWord
	*(*unsafe.Pointer)(dataOut) = dataWord
}

// __llgo_coro_fault_prepare_v1 is the target-neutral compiler-to-runtime
// implicit-fault handoff. kind selects a stable language fault payload; the
// physical coroutine identity is explicit, with no signal handler, TLS
// current-G, libuv, or host callback involved. coro.PreparePanic selects the
// parent CompletionRecord path for an awaited child and the task PanicRecord
// path for a root.
//
//export __llgo_coro_fault_prepare_v1
func __llgo_coro_fault_prepare_v1(g, handle, header unsafe.Pointer, kind uint32) {
	typeWord, dataWord := coroFaultPayloadV1(kind)
	if typeWord == nil || !coro.PreparePanic(
		(*coro.G)(g),
		handle,
		(*coro.HeaderV1)(header),
		typeWord,
		dataWord,
	) {
		coroRuntimeAbort("invalid coroutine fault panic handoff")
	}
}

// __llgo_coro_fault_prepare_v2 is the immediate-publication counterpart of
// fault_payload_v2 for physical bodies without a cleanup drainer.
//
//export __llgo_coro_fault_prepare_v2
func __llgo_coro_fault_prepare_v2(
	g, handle, header unsafe.Pointer,
	kind uint32,
	arg0 uint64,
	arg1 uintptr,
) {
	typeWord, dataWord := coroFaultPayloadV2(kind, arg0, arg1)
	if typeWord == nil || !coro.PreparePanic(
		(*coro.G)(g),
		handle,
		(*coro.HeaderV1)(header),
		typeWord,
		dataWord,
	) {
		coroRuntimeAbort("invalid parameterized coroutine fault panic handoff")
	}
}
