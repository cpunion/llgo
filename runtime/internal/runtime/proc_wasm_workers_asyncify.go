//go:build llgo && js && wasm && llgo.wasm_workers && !llgo.wasm_resume

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

	"github.com/goplus/llgo/runtime/internal/wasmcontext"
)

type runtimeContextPlatform struct {
	wasmWorkerContextState
	context wasmcontext.Context
}

type wasmWorkerPlatform struct {
	system wasmcontext.Context
}

//go:linkname wasmMainTask __llgo_wasm_main
func wasmMainTask(unsafe.Pointer) unsafe.Pointer

func initWasmWorkerMain(gp *g) {
	initWasmFiber(gp, wasmcontext.Entry(wasmMainStart), unsafe.Pointer(gp), 0)
}

func initWasmWorkerBackendSystem(worker *wasmWorker) bool {
	system := &worker.platform.system
	if system.Ready() {
		return false
	}
	if !system.InitCurrent(AllocRoot) {
		panic("runtime: failed to allocate WebAssembly system context")
	}
	return true
}

func runWasmWorkerContext(worker *wasmWorker, gp *g) {
	worker.platform.system.Swap(
		&gp.context.platform.context,
		wasmGCRootPointer(&gp.context.platform.gcRoot),
	)
}

func initWasmWorkerG(
	gp *g, _ goroutineFunc, _ unsafe.Pointer, stackSize uintptr,
) {
	initWasmFiber(gp, wasmcontext.Entry(wasmGStart), unsafe.Pointer(gp), stackSize)
}

func closeWasmWorkerContext(platform *runtimeContextPlatform) {
	platform.context.Close(FreeRoot)
}

func suspendWasmWorkerG(worker *wasmWorker, gp *g) {
	gp.context.platform.context.Swap(
		&worker.platform.system,
		wasmWorkerSystemRootPointer(worker),
	)
}

func initWasmFiber(gp *g, entry wasmcontext.Entry, arg unsafe.Pointer, stackSize uintptr) {
	platform := &gp.context.platform
	if !platform.context.Init(
		entry,
		arg,
		stackSize,
		AllocRoot,
		FreeRoot,
	) {
		panic("runtime: failed to allocate WebAssembly goroutine stack")
	}
}

func wasmMainStart(arg unsafe.Pointer) {
	gp := (*g)(arg)
	if gp == nil || getg() != gp {
		fatal("runtime: invalid WebAssembly main entry")
		return
	}
	wasmMainTask(nil)
	wasmMultiSched.mainReturned = true
	finishWasmG(gp)
}

func wasmGStart(arg unsafe.Pointer) {
	gp := (*g)(arg)
	if gp == nil || getg() != gp {
		fatal("runtime: invalid WebAssembly goroutine entry")
		return
	}
	fn, fnarg := gp.startfn, gp.startarg
	gp.startfn = nil
	gp.startarg = nil
	fn(fnarg)
	finishWasmG(gp)
}
