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
	"github.com/goplus/llgo/runtime/internal/coroalloc"
)

//export __llgo_coro_frame_alloc_v1
func __llgo_coro_frame_alloc_v1(g unsafe.Pointer, size, align uintptr, descriptor unsafe.Pointer) unsafe.Pointer {
	total, ok := coro.FrameAllocationSize(size, align)
	if !ok {
		coroRuntimeAbort("invalid coroutine frame allocation size")
	}
	raw := coroalloc.AllocFrame(total)
	if raw == nil {
		coroRuntimeAbort("coroutine frame allocation failed")
	}
	storage, ok := coro.RegisterFrameCompiler((*coro.G)(g), raw, total, align, descriptor)
	if !ok {
		if !coroalloc.FreeFrame(raw, total) {
			coroRuntimeAbort("coroutine frame allocation rollback failed")
		}
		coroRuntimeAbort("invalid coroutine frame allocation")
	}
	return storage
}

//export __llgo_coro_frame_publish_v1
func __llgo_coro_frame_publish_v1(g, handle, header, storage unsafe.Pointer) {
	if !coro.PublishFrame((*coro.G)(g), handle, (*coro.HeaderV1)(header), storage) {
		coroRuntimeAbort("invalid coroutine frame publication")
	}
}

//go:noinline
//export __llgo_coro_frame_publish_v3
func __llgo_coro_frame_publish_v3(
	g, handle, header, storage, metadata, descriptor, resultSlot unsafe.Pointer,
) {
	if !coro.PublishFrameV3Compiler(
		(*coro.G)(g), handle, (*coro.HeaderV1)(header), storage, metadata,
		descriptor, resultSlot,
	) {
		coroRuntimeAbort("invalid initialized borrowable coroutine frame publication")
	}
}

//export __llgo_coro_await_prepare_v1
func __llgo_coro_await_prepare_v1(g, parent, child unsafe.Pointer) {
	if !coro.PrepareAwait((*coro.G)(g), parent, child) {
		coroRuntimeAbort("invalid coroutine child handoff")
	}
}

//export __llgo_coro_await_prepare_v2
func __llgo_coro_await_prepare_v2(g, parent, child unsafe.Pointer) {
	if !coro.PrepareAwaitCompletionCompiler((*coro.G)(g), parent, child) {
		coroRuntimeAbort("invalid coroutine child completion handoff")
	}
}

//export __llgo_coro_await_consume_v1
func __llgo_coro_await_consume_v1(g, parent, typeOut, dataOut unsafe.Pointer) uint32 {
	if typeOut == nil || dataOut == nil {
		coroRuntimeAbort("invalid coroutine child outcome output")
	}
	task := (*coro.G)(g)
	snapshot, ok := coro.ConsumeAwaitCompletionCompiler(task, parent)
	if !ok {
		coroRuntimeAbort("invalid coroutine child outcome consume")
	}
	coroReleaseDiscardedPanicTraceV1(task)
	*(*unsafe.Pointer)(typeOut) = snapshot.TypeWord
	*(*unsafe.Pointer)(dataOut) = snapshot.DataWord
	return uint32(snapshot.Status)
}

//export __llgo_coro_preempt_poll_v1
func __llgo_coro_preempt_poll_v1(g unsafe.Pointer) bool {
	return coro.PollPreemptCompiler((*coro.G)(g))
}

//export __llgo_coro_yield_prepare_v1
func __llgo_coro_yield_prepare_v1(g, handle, header unsafe.Pointer) {
	if !coro.PrepareYield((*coro.G)(g), handle, (*coro.HeaderV1)(header)) {
		coroRuntimeAbort("invalid coroutine yield handoff")
	}
}

//export __llgo_coro_complete_prepare_v1
func __llgo_coro_complete_prepare_v1(g, handle, header unsafe.Pointer) {
	if !coro.PrepareComplete((*coro.G)(g), handle, (*coro.HeaderV1)(header)) {
		coroRuntimeAbort("invalid coroutine completion handoff")
	}
}

// __llgo_coro_complete_prepare_v2 publishes the exact frame-local cleanup
// base. Panic continues to use the payload-carrying panic transaction.
//
//export __llgo_coro_complete_prepare_v2
func __llgo_coro_complete_prepare_v2(g, handle, header unsafe.Pointer, status uint32) {
	task := (*coro.G)(g)
	frameHeader := (*coro.HeaderV1)(header)
	completion := coro.CompletionStatus(status)
	if !coro.PrepareCompleteStatusCompiler(
		task, handle, frameHeader, completion,
	) {
		coroRuntimeAbort("invalid coroutine terminal completion handoff")
	}
	if completion == coro.CompletionGoexit && frameHeader.Parent == nil &&
		task == &coroProgramGV1State && !coroProgramCommitMainGoexitV1(task) {
		coroRuntimeAbort("invalid coroutine command-main Goexit handoff")
	}
}

//export __llgo_coro_frame_free_v1
func __llgo_coro_frame_free_v1(g, storage unsafe.Pointer, size, align uintptr, descriptor unsafe.Pointer) {
	task := (*coro.G)(g)
	raw, total, ok := coro.ReleaseFrameCompiler(task, storage, size, align, descriptor)
	if !ok {
		coroRuntimeAbort("invalid coroutine frame destruction")
	}
	// A managed child panic remains recoverable by its parent, so every logical
	// G may retain that pending frame. Only the static command G may retain a
	// terminal frame chain because its native entry owns the no-return report.
	if coro.RetainPendingPanicTraceFrame(task, raw, total) ||
		task == &coroProgramGV1State && coro.RetainPanicTraceFrame(task, raw, total) {
		return
	}
	if !coroalloc.FreeFrame(raw, total) {
		coroRuntimeAbort("coroutine frame release failed")
	}
}
