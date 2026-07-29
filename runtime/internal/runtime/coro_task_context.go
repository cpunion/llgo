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
// llvm.coro.resume interval. Nested foreign reentry naturally saves and
// restores the outer logical G.
func coroEnterRuntimeContext(task *coro.G) (*g, bool) {
	ctx := (*runtimeContext)(coro.TaskLocal(task))
	if !validCoroRuntimeContext(ctx) {
		return nil, false
	}
	gp, pp := &ctx.g, &ctx.p
	if readgstatus(gp) != _Grunnable || readpstatus(pp) != _Pidle {
		return nil, false
	}
	previous := getg()
	casgstatus(gp, _Grunnable, _Grunning)
	setpstatus(pp, _Prunning)
	setg(gp)
	return previous, true
}

func coroLeaveRuntimeContext(task *coro.G, previous *g) bool {
	ctx := (*runtimeContext)(coro.TaskLocal(task))
	if !validCoroRuntimeContext(ctx) {
		return false
	}
	gp, pp := &ctx.g, &ctx.p
	if getg() != gp || readgstatus(gp) != _Grunning || readpstatus(pp) != _Prunning {
		return false
	}
	casgstatus(gp, _Grunning, _Grunnable)
	setpstatus(pp, _Pidle)
	setg(previous)
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
