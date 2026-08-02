//go:build llgo && wasm && llgo.wasm_resume && (js || wasip1) && !(wasip1 && llgo.wasi_threads)

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

	"github.com/goplus/llgo/runtime/internal/wasmresume"
)

// wasmResumeOwners keeps compiler-created frames on runtime-owned storage
// while nested compatibility wrappers temporarily change the visible owner.
type wasmResumeOwners struct {
	compat      wasmresume.Context
	frameOwner  *wasmresume.Context
	compatOwner *wasmresume.Context
}

//go:linkname wasmResumeAlloc __llgo_wasm_resume_alloc
func wasmResumeAlloc(ctx *wasmresume.Context, size, align uintptr) unsafe.Pointer {
	state := currentWasmResumeOwners()
	if state != nil && ctx == state.compatOwner && state.frameOwner != nil {
		ctx = state.frameOwner
	}
	return ctx.AllocateFrame(size, align, AllocRoot)
}

//go:linkname wasmResumeAllocDynamic __llgo_wasm_resume_alloc_dynamic
func wasmResumeAllocDynamic(ctx *wasmresume.Context, size, align uintptr) unsafe.Pointer {
	return wasmResumeAlloc(ctx, size, align)
}

//go:linkname wasmResumeFree __llgo_wasm_resume_free
func wasmResumeFree(ctx *wasmresume.Context, frame *wasmresume.Frame) {
	state := currentWasmResumeOwners()
	if state != nil && ctx == state.compatOwner && state.frameOwner != nil {
		ctx = state.frameOwner
	}
	ctx.ReleaseFrame(frame)
}

//go:linkname wasmResumeCompatEnter __llgo_wasm_resume_compat_enter
func wasmResumeCompatEnter(ctx *wasmresume.Context) unsafe.Pointer {
	state := currentWasmResumeOwners()
	if ctx == nil || state == nil {
		fatal("runtime: invalid WebAssembly compatibility arena entry")
		return nil
	}
	owner := state.compatOwner
	if owner == nil {
		owner = &state.compat
		state.frameOwner = owner
	}
	state.compatOwner = ctx
	return unsafe.Pointer(owner)
}

//go:linkname wasmResumeCompatLeave __llgo_wasm_resume_compat_leave
func wasmResumeCompatLeave(ctx *wasmresume.Context, rawOwner unsafe.Pointer) {
	state := currentWasmResumeOwners()
	owner := (*wasmresume.Context)(rawOwner)
	if state == nil || owner == nil || state.compatOwner != ctx {
		fatal("runtime: invalid WebAssembly compatibility arena exit")
		return
	}
	state.compatOwner = owner
}
