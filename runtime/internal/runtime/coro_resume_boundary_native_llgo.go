//go:build llgo && llgo_coro && !baremetal && !wasm && !tinygo.wasm && (darwin || linux)

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

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/coro"
)

// coroHandleResumePhysicalV1 is the native outer counterpart of the compiler's
// inline-child boundary. Each nonlocal landing stages one panic node and
// retries the same LLVM state index; the first generated resume gate consumes
// it and branches away before source instructions can be replayed.
func coroHandleResumePhysicalV1(task *coro.G, handle unsafe.Pointer, panicBoundary bool) bool {
	if !panicBoundary {
		coroHandleResume(handle)
		return true
	}
	if !coroPanicBoundaryCapability(task) {
		return false
	}
	// Both records belong to this physical machine-stack activation.  Ordinary
	// Go address-taking would conservatively move them to the managed heap,
	// adding two allocations to every scheduler resume.  These exact constant
	// compiler intrinsics instead materialize native allocas; neither address is
	// retained after pop/stage closes the boundary.
	boundary := (*Defer)(c.Alloca(unsafe.Sizeof(Defer{})))
	env := (*SigjmpBuf)(c.AllocaSigjmpBuf())
	*boundary = Defer{}
	for {
		landed := Sigsetjmp(env, c.Int(0))
		if landed == 0 {
			if !coroPanicBoundaryPush(task, handle, boundary, unsafe.Pointer(env)) {
				return false
			}
			coroHandleResume(handle)
			return coroPanicBoundaryPop(task, handle, boundary)
		}
		if !coroPanicBoundaryStage(task, handle, boundary) {
			return false
		}
	}
}
