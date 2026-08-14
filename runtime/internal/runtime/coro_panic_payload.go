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

// coroPanicNilErrorV1 and its empty-interface header are package globals so a
// panic(nil) payload remains rooted after the LLVM coroutine frame carrying
// the original interface has been destroyed. In particular, normalization at
// the compiler/runtime boundary neither allocates nor depends on a stack-local
// interface conversion.
var coroPanicNilErrorV1 PanicNilError
var coroPanicNilPayloadV1 any = &coroPanicNilErrorV1

// coroNormalizePanicPayloadV1 returns the concrete empty-interface words used
// by the explicit-status scheduler ABI. A nil type word is Go's untyped nil
// panic value and is normalized to the Go 1.21+ *PanicNilError payload. A
// non-nil type word, including a typed nil whose data word is nil, is preserved
// exactly.
func coroNormalizePanicPayloadV1(typeWord, dataWord unsafe.Pointer) (unsafe.Pointer, unsafe.Pointer) {
	if typeWord != nil {
		return typeWord, dataWord
	}
	payload := *(*eface)(unsafe.Pointer(&coroPanicNilPayloadV1))
	if payload._type == nil {
		return nil, nil
	}
	return unsafe.Pointer(payload._type), payload.data
}

// __llgo_coro_panic_prepare_v1 is the compiler-to-runtime terminal panic
// handoff. The physical G is an explicit ABI argument: this boundary must
// never discover scheduler ownership through TLS or a process-global current
// G. A rejected once-only publication is a terminal ABI violation and aborts
// immediately, so malformed cleanup/recover/Goexit/implicit-fault lowering
// cannot resume ordinary execution on a poisoned G.
//
//export __llgo_coro_panic_prepare_v1
func __llgo_coro_panic_prepare_v1(g, handle, header, typeWord, dataWord unsafe.Pointer) {
	typeWord, dataWord = coroNormalizePanicPayloadV1(typeWord, dataWord)
	task := (*coro.G)(g)
	if typeWord == nil || !coro.PreparePanic(
		task,
		handle,
		(*coro.HeaderV1)(header),
		typeWord,
		dataWord,
	) {
		coroRuntimeAbort("invalid coroutine panic handoff")
	}
	coroReleaseDiscardedPanicTraceV1(task)
}

// __llgo_coro_panic_trace_replace_v1 is emitted only for a cleanup-local
// language operation which starts a new panic. It carries no payload because
// its purpose is to distinguish replacement even when both panic interface
// words are bit-identical.
//
//export __llgo_coro_panic_trace_replace_v1
func __llgo_coro_panic_trace_replace_v1(g, handle unsafe.Pointer) {
	task := (*coro.G)(g)
	if !coro.ReplacePanicTrace(task, handle) {
		coroRuntimeAbort("invalid coroutine panic trace replacement")
	}
	coroReleaseDiscardedPanicTraceV1(task)
}

// __llgo_coro_await_prepare_v3 is the unified managed-child handoff. Mode zero
// is an ordinary call. Mode one marks an exact deferred child as eligible to
// recover the supplied panic; normalization here ensures a successfully
// recovered panic(nil) observes the Go 1.21+ *PanicNilError value. Both modes
// use the same parent CompletionRecord and terminal consume operation.
//
//export __llgo_coro_await_prepare_v3
func __llgo_coro_await_prepare_v3(g, parent, child unsafe.Pointer, mode uint32, typeWord, dataWord unsafe.Pointer) {
	switch mode {
	case 0:
		if typeWord != nil || dataWord != nil ||
			!coro.PrepareAwaitCompletionCompiler((*coro.G)(g), parent, child) {
			coroRuntimeAbort("invalid ordinary coroutine child completion handoff")
		}
	case 1:
		typeWord, dataWord = coroNormalizePanicPayloadV1(typeWord, dataWord)
		if typeWord == nil || !coro.PrepareAwaitCompletionRecover(
			(*coro.G)(g), parent, child, typeWord, dataWord,
		) {
			coroRuntimeAbort("invalid recoverable coroutine child completion handoff")
		}
	default:
		coroRuntimeAbort("invalid coroutine child completion mode")
	}
}

// __llgo_coro_recover_take_v1 implements one syntactic recover in the active
// physical frame. A valid but non-direct call writes a nil empty interface;
// malformed scheduler ownership aborts instead of consulting legacy TLS.
//
//export __llgo_coro_recover_take_v1
func __llgo_coro_recover_take_v1(g, child, typeOut, dataOut unsafe.Pointer) {
	if typeOut == nil || dataOut == nil {
		coroRuntimeAbort("invalid coroutine recover outputs")
	}
	*(*unsafe.Pointer)(typeOut) = nil
	*(*unsafe.Pointer)(dataOut) = nil
	snapshot, recovered, valid := coro.TakeRecover((*coro.G)(g), child)
	if !valid {
		coroRuntimeAbort("invalid coroutine recover take")
	}
	if recovered {
		*(*unsafe.Pointer)(typeOut) = snapshot.TypeWord
		*(*unsafe.Pointer)(dataOut) = snapshot.DataWord
	}
}
