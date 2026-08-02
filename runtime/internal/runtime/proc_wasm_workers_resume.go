//go:build llgo && js && wasm && llgo.wasm_workers && llgo.wasm_resume

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
	"github.com/goplus/llgo/runtime/internal/clite/sync/atomic"
	"github.com/goplus/llgo/runtime/internal/gcroot"
	"github.com/goplus/llgo/runtime/internal/wasmresume"
)

type runtimeContextPlatform struct {
	wasmWorkerContextState
	context    wasmresume.Context
	unwind     unsafe.Pointer
	unwindRoot unsafe.Pointer
	retired    bool
}

type wasmWorkerPlatform struct {
	resume wasmResumeOwners
	ready  bool
}

//go:linkname wasmMainStart C.__llgo_wasm_start.__llgo_wasm_main
func wasmMainStart(*wasmresume.Context, unsafe.Pointer) *wasmresume.Frame

func initWasmWorkerMain(gp *g) {
	context := &gp.context.platform.context
	context.Start(wasmMainStart(context, nil))
}

func initWasmWorkerBackendSystem(worker *wasmWorker) bool {
	if worker.platform.ready {
		return false
	}
	worker.platform.ready = true
	return true
}

func runWasmWorkerContext(worker *wasmWorker, gp *g) {
	action := runWasmResumeContext(worker, gp)
	status := readgstatus(gp)
	switch action {
	case wasmresume.Return:
		if status != _Grunning {
			fatal("runtime: invalid completed WebAssembly goroutine")
			return
		}
		retireWasmResumeG(gp)
	case wasmresume.Suspend:
		if status == _Gdead {
			retireWasmResumeG(gp)
		}
	default:
		fatal("runtime: invalid WebAssembly resume action")
	}
}

func runWasmResumeContext(worker *wasmWorker, gp *g) wasmresume.Action {
	platform := &gp.context.platform
	if wasmGCRootEnabled {
		switchWasmGCRoot(&platform.gcRoot)
		// Scheduler state is anchored by wasmMultiSched. Keep no native frame
		// after leaving system code; an STW in active system code publishes its
		// transient compatibility roots before acknowledging the request.
		gcroot.ClearSuspendedChain(&worker.gc.systemRoot)
	}
	unwind := c.AllocaSigjmpBuf()
	previous := platform.unwind
	previousRoot := platform.unwindRoot
	platform.unwind = unwind
	platform.unwindRoot = captureWasmResumeGCRoot()

	owners := &worker.platform.resume
	previousFrameOwner := owners.frameOwner
	previousCompatOwner := owners.compatOwner
	owners.frameOwner = &platform.context
	owners.compatOwner = &platform.context
	if c.Sigsetjmp(unwind, 0) != 0 {
		owners.frameOwner = previousFrameOwner
		owners.compatOwner = previousCompatOwner
		if !platform.context.Unwind(unsafe.Pointer(gp.defer_)) {
			platform.unwind = previous
			platform.unwindRoot = previousRoot
			if gp.goexit {
				casgstatus(gp, _Grunning, _Gdead)
				if wasmGCRootEnabled {
					switchWasmGCRoot(&worker.gc.systemRoot)
				}
				return wasmresume.Suspend
			}
			Rethrow(nil)
			if wasmGCRootEnabled {
				switchWasmGCRoot(&worker.gc.systemRoot)
			}
			return wasmresume.Return
		}
		owners.frameOwner = &platform.context
		owners.compatOwner = &platform.context
	}
	action := platform.context.Run()
	if wasmGCRootEnabled {
		restoreWasmResumeGCRoot(platform.context.RootChain())
	}
	owners.frameOwner = previousFrameOwner
	owners.compatOwner = previousCompatOwner
	platform.unwind = previous
	platform.unwindRoot = previousRoot
	if wasmGCRootEnabled {
		switchWasmGCRoot(&worker.gc.systemRoot)
	}
	return action
}

func initWasmWorkerG(
	gp *g, fn goroutineFunc, arg unsafe.Pointer, _ uintptr,
) {
	platform := &gp.context.platform
	gp.startfn = nil
	gp.startarg = nil
	platform.context.Start(fn(&platform.context, arg))
}

func closeWasmWorkerContext(platform *runtimeContextPlatform) {
	platform.context.Close(FreeRoot)
}

func suspendWasmWorkerG(worker *wasmWorker, gp *g) {
	if worker == nil || gp == nil || gp.context.platform.owner != worker ||
		currentWasmWorker() != worker {
		fatal("runtime: invalid resumable WebAssembly worker suspension")
		return
	}
	if readgstatus(gp) == _Gdead {
		gp.context.platform.retired = true
	}
	wasmresume.SuspendCurrent()
}

func retireWasmResumeG(gp *g) {
	platform := &gp.context.platform
	if platform.retired {
		return
	}
	if status := readgstatus(gp); status == _Grunning {
		casgstatus(gp, _Grunning, _Gdead)
	} else if status != _Gdead {
		fatal("runtime: retired WebAssembly goroutine is not dead")
		return
	}
	platform.retired = true
	atomic.Add(&wasmMultiSched.active, ^uint32(0))
	if gp.isMain {
		if gp.goexit {
			wasmMultiSched.mainGoexit = true
		} else {
			wasmMultiSched.mainReturned = true
		}
	}
	wakeWasmEventWorker()
}

func currentWasmResumeOwners() *wasmResumeOwners {
	worker := currentWasmWorker()
	if worker == nil {
		if wasmMultiSched.started {
			return nil
		}
		worker = &wasmMultiSched.workers[0]
	}
	return &worker.platform.resume
}
