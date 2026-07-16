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

	c "github.com/goplus/llgo/runtime/internal/clite"
	"github.com/goplus/llgo/runtime/internal/coro"
)

func coroRuntimeAbort(message string) {
	fatal(message)
	c.Exit(2)
}

//export __llgo_coro_frame_alloc_v1
func __llgo_coro_frame_alloc_v1(g unsafe.Pointer, size, align uintptr, descriptor unsafe.Pointer) unsafe.Pointer {
	total, ok := coro.FrameAllocationSize(size, align)
	if !ok {
		coroRuntimeAbort("invalid coroutine frame allocation size")
	}
	raw := AllocRoot(total)
	if raw == nil {
		coroRuntimeAbort("coroutine frame allocation failed")
	}
	storage, ok := coro.RegisterFrame((*coro.G)(g), raw, total, size, align, descriptor)
	if !ok {
		FreeRoot(raw)
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

//export __llgo_coro_complete_prepare_v1
func __llgo_coro_complete_prepare_v1(g, handle, header unsafe.Pointer) {
	if !coro.PrepareComplete((*coro.G)(g), handle, (*coro.HeaderV1)(header)) {
		coroRuntimeAbort("invalid coroutine completion handoff")
	}
}

//export __llgo_coro_frame_free_v1
func __llgo_coro_frame_free_v1(g, storage unsafe.Pointer, size, align uintptr, descriptor unsafe.Pointer) {
	raw, total, ok := coro.ReleaseFrame((*coro.G)(g), storage, size, align, descriptor)
	if !ok {
		coroRuntimeAbort("invalid coroutine frame destruction")
	}
	coro.Zero(raw, total)
	FreeRoot(raw)
}
