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

type coroProgramLifecycleV1 uint8

const (
	coroProgramUnusedV1 coroProgramLifecycleV1 = iota
	coroProgramBegunV1
	coroProgramRunningV1
	coroProgramCompleteV1
	coroProgramFailedV1
)

// coroProgramV1 is the allocation-free, single-start scheduler state used by
// the process entry coroutine. Keeping G and P in static storage avoids a
// pthread, TLS, or event-library dependency for scheduler state. The LLVM
// coroutine frame is still allocated through the target's AllocRoot backend;
// native currently uses BDWGC or C malloc, while allocator-independent
// wasm/embedded/bare-metal profiles require their planned linear-memory or
// static/slab backend.
//
// The entry path is intentionally single-use. No failure path resets this
// object: exported ABI failures terminate the process, and successful startup
// transitions from unused to complete or permanently failed.
type coroProgramStateV1 struct {
	lifecycle coroProgramLifecycleV1
	manifest  *coro.ProgramManifestV1
	factory   unsafe.Pointer
	g         coroG
	p         coroP
}

var coroProgramV1 coroProgramStateV1

func coroProgramBeginV1(manifest, expectedFactory unsafe.Pointer) (unsafe.Pointer, bool) {
	state := &coroProgramV1
	if state.lifecycle != coroProgramUnusedV1 {
		state.lifecycle = coroProgramFailedV1
		return nil, false
	}
	if _, code := coro.ValidateRunnableDirectProgramV1(
		(*coro.ProgramManifestV1)(manifest), expectedFactory,
	); code != coro.ProgramValidationOKV1 {
		state.lifecycle = coroProgramFailedV1
		return nil, false
	}
	if !coroInitG(&state.g) {
		state.lifecycle = coroProgramFailedV1
		return nil, false
	}
	state.manifest = (*coro.ProgramManifestV1)(manifest)
	state.factory = expectedFactory
	state.lifecycle = coroProgramBegunV1
	return unsafe.Pointer(&state.g), true
}

func coroProgramRunV1(gPointer, handle unsafe.Pointer) bool {
	state := &coroProgramV1
	if state.lifecycle != coroProgramBegunV1 || state.manifest == nil || state.factory == nil ||
		gPointer != unsafe.Pointer(&state.g) || handle == nil {
		state.lifecycle = coroProgramFailedV1
		return false
	}
	if !coroAdoptRoot(&state.g, handle) || !coroEnqueue(&state.p, &state.g) {
		state.lifecycle = coroProgramFailedV1
		return false
	}
	state.lifecycle = coroProgramRunningV1
	if !coroRun(&state.p) || !coro.TerminalG(&state.p, &state.g) {
		state.lifecycle = coroProgramFailedV1
		return false
	}
	state.lifecycle = coroProgramCompleteV1
	return true
}

//export __llgo_coro_program_begin_v1
func __llgo_coro_program_begin_v1(manifest, expectedFactory unsafe.Pointer) unsafe.Pointer {
	g, ok := coroProgramBeginV1(manifest, expectedFactory)
	if !ok {
		coroRuntimeAbort("invalid coroutine program bootstrap")
		return nil
	}
	return g
}

//export __llgo_coro_program_run_v1
func __llgo_coro_program_run_v1(g, handle unsafe.Pointer) {
	if !coroProgramRunV1(g, handle) {
		coroRuntimeAbort("invalid coroutine program execution")
	}
}
