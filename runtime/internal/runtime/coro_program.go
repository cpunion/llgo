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

type coroProgramLifecycleV1 uint8

const (
	coroProgramUnusedV1 coroProgramLifecycleV1 = iota
	coroProgramBegunV1
	coroProgramRunningV1
	coroProgramMainReturnRequestedV1
	coroProgramStoppingV1
	coroProgramCompleteV1
	coroProgramFailedV1
)

// The coroutine program globals form the allocation-free, single-start state used by
// the process entry coroutine. Keeping G and P in static storage avoids a
// pthread, TLS, or event-library dependency for scheduler state. LLVM frames
// use the explicitly bootstrapped, statically selected coroalloc backend:
// native GC builds use BDWGC uncollectable ranges, nogc/wasm profiles use C
// malloc/free, and bare-metal builds use tinygogc.
//
// The entry path is intentionally single-use. No failure path resets this
// object: exported ABI failures terminate the process, and successful startup
// transitions from unused to complete or permanently failed.
// Keep phase-0 fields as separate globals. Besides making ownership explicit,
// this avoids a synthetic nil-dereference helper on field access through the
// address of one aggregate global; the process-entry ABI must remain a plain,
// non-suspending call island.
var (
	coroProgramLifecycleV1State coroProgramLifecycleV1
	coroProgramManifestV1State  *coro.ProgramManifestV1
	coroProgramFactoryV1State   unsafe.Pointer
	coroProgramGV1State         coroG
	coroProgramPV1State         coroP
)

func coroProgramBeginV1(manifest, expectedFactory unsafe.Pointer) (unsafe.Pointer, bool) {
	if coroProgramLifecycleV1State != coroProgramUnusedV1 {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return nil, false
	}
	if !coroalloc.Ready() {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return nil, false
	}
	if manifest == nil {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return nil, false
	}
	programManifest := (*coro.ProgramManifestV1)(manifest)
	_, v2Code := coro.ValidateRunnableProgramV2(programManifest, expectedFactory)
	_, v1Code := coro.ValidateRunnableDirectProgramV1(programManifest, expectedFactory)
	if v2Code != coro.ProgramValidationOKV2 && v1Code != coro.ProgramValidationOKV1 {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return nil, false
	}
	if !coroInitG(&coroProgramGV1State) {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return nil, false
	}
	coroProgramManifestV1State = (*coro.ProgramManifestV1)(manifest)
	coroProgramFactoryV1State = expectedFactory
	coroProgramLifecycleV1State = coroProgramBegunV1
	return unsafe.Pointer(&coroProgramGV1State), true
}

func coroProgramRunV1(gPointer, handle unsafe.Pointer) bool {
	if coroProgramLifecycleV1State != coroProgramBegunV1 || coroProgramManifestV1State == nil || coroProgramFactoryV1State == nil ||
		gPointer != unsafe.Pointer(&coroProgramGV1State) || handle == nil {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return false
	}
	if !coroAdoptRoot(&coroProgramGV1State, handle) || !coroEnqueue(&coroProgramPV1State, &coroProgramGV1State) {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return false
	}
	coroProgramLifecycleV1State = coroProgramRunningV1
	if !coroRun(&coroProgramPV1State, &coroProgramGV1State) {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return false
	}
	switch coroProgramLifecycleV1State {
	case coroProgramRunningV1:
		// Backward-compatible no-spawn startup tables do not yet contain the
		// explicit main-return hook. They remain valid only when the whole P is
		// already terminal; a surviving child fails closed.
		if !coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
			coroProgramLifecycleV1State = coroProgramFailedV1
			return false
		}
	case coroProgramMainReturnRequestedV1:
		if !coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
			if !coro.BeginCommandShutdown(&coroProgramPV1State, &coroProgramGV1State) {
				coroProgramLifecycleV1State = coroProgramFailedV1
				return false
			}
			coroProgramLifecycleV1State = coroProgramStoppingV1
			if !coroCancelReady(&coroProgramPV1State) ||
				!coro.FinishCommandShutdown(&coroProgramPV1State, &coroProgramGV1State) ||
				!coro.TerminalG(&coroProgramPV1State, &coroProgramGV1State) {
				coroProgramLifecycleV1State = coroProgramFailedV1
				return false
			}
		}
	default:
		coroProgramLifecycleV1State = coroProgramFailedV1
		return false
	}
	coroProgramLifecycleV1State = coroProgramCompleteV1
	return true
}

func coroProgramMainReturnV1(gPointer unsafe.Pointer) bool {
	if coroProgramLifecycleV1State != coroProgramRunningV1 ||
		gPointer != unsafe.Pointer(&coroProgramGV1State) ||
		!coro.CommandMainReturnPoint(&coroProgramPV1State, &coroProgramGV1State) {
		coroProgramLifecycleV1State = coroProgramFailedV1
		return false
	}
	coroProgramLifecycleV1State = coroProgramMainReturnRequestedV1
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

//export __llgo_coro_program_main_return_v1
func __llgo_coro_program_main_return_v1(g unsafe.Pointer) {
	if !coroProgramMainReturnV1(g) {
		coroRuntimeAbort("invalid coroutine command main return")
	}
}
