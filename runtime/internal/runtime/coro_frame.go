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
	storage, ok := coro.RegisterFrame((*coro.G)(g), raw, total, size, align, descriptor)
	if !ok {
		if !coroalloc.FreeFrame(raw) {
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

//export __llgo_coro_await_prepare_v1
func __llgo_coro_await_prepare_v1(g, parent, child unsafe.Pointer) {
	if !coro.PrepareAwait((*coro.G)(g), parent, child) {
		coroRuntimeAbort("invalid coroutine child handoff")
	}
}

//export __llgo_coro_await_prepare_v2
func __llgo_coro_await_prepare_v2(g, parent, child unsafe.Pointer) {
	if !coro.PrepareAwaitCompletion((*coro.G)(g), parent, child) {
		coroRuntimeAbort("invalid coroutine child completion handoff")
	}
}

//export __llgo_coro_await_consume_v1
func __llgo_coro_await_consume_v1(g, parent, typeOut, dataOut unsafe.Pointer) uint32 {
	if typeOut == nil || dataOut == nil {
		coroRuntimeAbort("invalid coroutine child outcome output")
	}
	snapshot, ok := coro.ConsumeAwaitCompletion((*coro.G)(g), parent)
	if !ok {
		coroRuntimeAbort("invalid coroutine child outcome consume")
	}
	*(*unsafe.Pointer)(typeOut) = snapshot.TypeWord
	*(*unsafe.Pointer)(dataOut) = snapshot.DataWord
	return uint32(snapshot.Status)
}

//export __llgo_coro_preempt_poll_v1
func __llgo_coro_preempt_poll_v1(g unsafe.Pointer) bool {
	return coro.PollPreempt((*coro.G)(g))
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
	if !coro.PrepareCompleteStatus(
		(*coro.G)(g), handle, (*coro.HeaderV1)(header), coro.CompletionStatus(status),
	) {
		coroRuntimeAbort("invalid coroutine terminal completion handoff")
	}
}

//export __llgo_coro_frame_free_v1
func __llgo_coro_frame_free_v1(g, storage unsafe.Pointer, size, align uintptr, descriptor unsafe.Pointer) {
	raw, total, ok := coro.ReleaseFrame((*coro.G)(g), storage, size, align, descriptor)
	if !ok {
		coroRuntimeAbort("invalid coroutine frame destruction")
	}
	coro.Zero(raw, total)
	if !coroalloc.FreeFrame(raw) {
		coroRuntimeAbort("coroutine frame release failed")
	}
}
