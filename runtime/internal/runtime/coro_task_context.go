//go:build !coro_runtime_adapter_test

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

// coroBindRuntimeContext creates the runtime sidecar which follows one
// stackless logical G across executor threads. The target-neutral scheduler
// retains only its opaque scanned pointer.
func coroBindRuntimeContext(task, parent *coro.G, main bool) bool {
	if task == nil || coro.TaskLocal(task) != nil {
		return false
	}
	var parentG *g
	if parent != nil {
		parentContext := (*runtimeContext)(coro.TaskLocal(parent))
		if !validCoroRuntimeContext(parentContext) {
			return false
		}
		parentG = &parentContext.g
	}
	ctx := allocRuntimeContext()
	gp := initRuntimeContext(ctx, parentG, _Grunnable)
	gp.localContext = &ctx.local
	gp.isMain = main
	if coro.BindTaskLocal(task, unsafe.Pointer(ctx)) {
		return true
	}
	discardCoroRuntimeContext(ctx)
	return false
}

func validCoroRuntimeContext(ctx *runtimeContext) bool {
	if ctx == nil || ctx.root == nil {
		return false
	}
	gp, mp, pp := &ctx.g, &ctx.m, &ctx.p
	return gp.context == ctx && gp.m == mp && mp.curg == gp && mp.p == pp &&
		pp.m == mp && gp.localContext == &ctx.local
}

// coroEnterRuntimeContext installs task's runtime G only for the physical
// llvm.coro.resume interval. A synchronous C-to-Go reentry resumes a child
// frame of the same logical G while its parent resume remains active below C;
// that exact nested case borrows the existing install.
func coroEnterRuntimeContext(task *coro.G) (coroRuntimeContextActivationV1, bool) {
	ctx := (*runtimeContext)(coro.TaskLocal(task))
	if !validCoroRuntimeContext(ctx) {
		return coroRuntimeContextActivationV1{}, false
	}
	gp, pp := &ctx.g, &ctx.p
	current := getg()
	if current == gp {
		if readgstatus(gp) != _Grunning || readpstatus(pp) != _Prunning {
			return coroRuntimeContextActivationV1{}, false
		}
		return coroRuntimeContextActivationV1{borrowed: true}, true
	}
	if readgstatus(gp) != _Grunnable || readpstatus(pp) != _Pidle {
		return coroRuntimeContextActivationV1{}, false
	}
	casgstatus(gp, _Grunnable, _Grunning)
	setpstatus(pp, _Prunning)
	setg(gp)
	return coroRuntimeContextActivationV1{previous: unsafe.Pointer(current)}, true
}

func coroLeaveRuntimeContext(task *coro.G, activation coroRuntimeContextActivationV1) bool {
	ctx := (*runtimeContext)(coro.TaskLocal(task))
	if !validCoroRuntimeContext(ctx) {
		return false
	}
	gp, pp := &ctx.g, &ctx.p
	if getg() != gp || readgstatus(gp) != _Grunning || readpstatus(pp) != _Prunning {
		return false
	}
	if activation.borrowed {
		return activation.previous == nil
	}
	casgstatus(gp, _Grunning, _Grunnable)
	setpstatus(pp, _Pidle)
	setg((*g)(activation.previous))
	return true
}

// coroReleaseRuntimeContext tears down the runtime sidecar after the scheduler
// has made the G terminal but before its scanned task allocation is cleared.
func coroReleaseRuntimeContext(task *coro.G) bool {
	raw, ok := coro.ReleaseTaskLocal(task)
	if !ok {
		return false
	}
	ctx := (*runtimeContext)(raw)
	if !validCoroRuntimeContext(ctx) {
		return false
	}
	gp, mp, pp := &ctx.g, &ctx.m, &ctx.p
	if readgstatus(gp) != _Grunnable || readpstatus(pp) != _Pidle {
		return false
	}
	if gp.localContext != nil {
		releaseLocalBlocks(gp.localContext)
		gp.localContext = nil
	}
	if gp.panic_ != nil {
		c.Free(gp.panic_)
		gp.panic_ = nil
	}
	casgstatus(gp, _Grunnable, _Gdead)
	setpstatus(pp, _Pdead)
	pp.m = nil
	mp.p = nil
	mp.curg = nil
	gp.m = nil
	root := ctx.root
	ctx.root = nil
	FreeRoot(root)
	return true
}

func discardCoroRuntimeContext(ctx *runtimeContext) {
	if ctx == nil {
		return
	}
	if ctx.g.localContext != nil {
		releaseLocalBlocks(ctx.g.localContext)
		ctx.g.localContext = nil
	}
	root := ctx.root
	ctx.root = nil
	if root != nil {
		FreeRoot(root)
	}
}
