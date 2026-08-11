//go:build !coro_runtime_adapter_test && !coro_native_fleet_test

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

func coroRuntimeContextParent(task, parent *coro.G) (*g, bool) {
	if task == nil || coro.TaskLocal(task) != nil {
		return nil, false
	}
	var parentG *g
	if parent != nil {
		parentContext := (*coroRuntimeContext)(coro.TaskLocal(parent))
		if !validCoroRuntimeTaskContext(parent, parentContext) {
			return nil, false
		}
		parentG = &parentContext.g
		if parentG.m == nil || parentG.m.curg != parentG || parentG.m.p == nil ||
			parentG.m.p.m != parentG.m || readgstatus(parentG) != _Grunning ||
			readpstatus(parentG.m.p) != _Prunning {
			return nil, false
		}
	}
	return parentG, true
}

func coroBindRuntimeContextAt(task *coro.G, parentG *g, ctx *coroRuntimeContext, embedded bool) bool {
	if ctx == nil || ctx.g.context != nil || embedded != (parentG != nil) {
		return false
	}
	gp := initCoroRuntimeContext(ctx, parentG, _Grunnable)
	gp.localContext = &ctx.local
	gp.isMain = !embedded
	gp.coroEmbedded = embedded
	if coro.BindTaskLocal(task, unsafe.Pointer(ctx)) {
		gp.startarg = unsafe.Pointer(task)
		return true
	}
	discardCoroRuntimeContext(ctx, !embedded)
	return false
}

// coroBindRuntimeContext creates the independently rooted runtime sidecar for
// the static command G. Spawned tasks use coroBindTaskAllocationRuntimeContext
// so the sidecar shares their physical task allocation.
func coroBindRuntimeContext(task, parent *coro.G, main bool) bool {
	if !main || parent != nil {
		return false
	}
	parentG, ok := coroRuntimeContextParent(task, parent)
	if !ok {
		return false
	}
	size := unsafe.Sizeof(coroRuntimeContext{})
	raw := AllocRoot(size)
	if raw == nil {
		coroRuntimeAbort("failed to allocate coroutine runtime context")
		return false
	}
	c.Memset(raw, 0, size)
	ctx := (*coroRuntimeContext)(raw)
	return coroBindRuntimeContextAt(task, parentG, ctx, false)
}

// coroBindTaskAllocationRuntimeContext initializes the sidecar in the tail of
// a zeroed spawned-task envelope. The task allocation remains its sole
// physical owner and is released only after this logical context is detached.
func coroBindTaskAllocationRuntimeContext(task, parent *coro.G) bool {
	if parent == nil {
		return false
	}
	parentG, ok := coroRuntimeContextParent(task, parent)
	if !ok {
		return false
	}
	ctx, ok := coroTaskAllocationContext(task)
	if !ok {
		return false
	}
	return coroBindRuntimeContextAt(task, parentG, ctx, true)
}

// validCoroRuntimeContext checks only state which follows the logical G. Its
// temporary physical M/P attachment is validated exactly once by enter/leave;
// parent spawn admission performs its own running-state check.
func validCoroRuntimeContext(ctx *coroRuntimeContext) bool {
	if ctx == nil {
		return false
	}
	gp := &ctx.g
	return gp.context == ctx && gp.localContext == &ctx.local
}

func validCoroRuntimeTaskContext(task *coro.G, ctx *coroRuntimeContext) bool {
	if task == nil || !validCoroRuntimeContext(ctx) || ctx.g.startfn != nil ||
		ctx.g.startarg != unsafe.Pointer(task) {
		return false
	}
	// This predicate runs around every physical coroutine resume. The task is
	// already scheduler-valid and coroEmbedded selects the combined allocation
	// representation. The command root is the only independently allocated
	// logical-task context; every dynamic G must match the exact tail address.
	if ctx.g.coroEmbedded {
		return ctx == &(*coroTaskAllocation)(unsafe.Pointer(task)).context
	}
	return ctx.g.isMain
}

// coroEnterRuntimeContext installs task's runtime G only for the physical
// llvm.coro.resume interval. A synchronous C-to-Go reentry resumes a child
// frame of the same logical G while its parent resume remains active below C;
// that exact nested case borrows the existing install.
func coroEnterRuntimeContext(task *coro.G) (coroRuntimeContextActivationV1, bool) {
	ctx := (*coroRuntimeContext)(coro.TaskLocal(task))
	if !validCoroRuntimeTaskContext(task, ctx) {
		return coroRuntimeContextActivationV1{}, false
	}
	gp := &ctx.g
	current := getg()
	if current == gp {
		if gp.m == nil || gp.m.curg != gp || gp.m.p == nil || gp.m.p.m != gp.m ||
			readgstatus(gp) != _Grunning || readpstatus(gp.m.p) != _Prunning {
			return coroRuntimeContextActivationV1{}, false
		}
		return coroRuntimeContextActivationV1{borrowed: true}, true
	}
	if current == nil || current.context == nil || current.startarg != nil ||
		gp.m != nil || readgstatus(gp) != _Grunnable ||
		current.m == nil || current.m.curg != current || current.m.p == nil ||
		current.m.p.m != current.m || readgstatus(current) != _Grunning ||
		readpstatus(current.m.p) != _Prunning {
		return coroRuntimeContextActivationV1{}, false
	}
	mp := current.m
	casgstatus(gp, _Grunnable, _Grunning)
	gp.m = mp
	mp.curg = gp
	setg(gp)
	return coroRuntimeContextActivationV1{previous: unsafe.Pointer(current)}, true
}

func coroLeaveRuntimeContext(task *coro.G, activation coroRuntimeContextActivationV1) bool {
	ctx := (*coroRuntimeContext)(coro.TaskLocal(task))
	if !validCoroRuntimeTaskContext(task, ctx) {
		return false
	}
	gp := &ctx.g
	if getg() != gp || gp.m == nil || gp.m.curg != gp || gp.m.p == nil ||
		gp.m.p.m != gp.m || readgstatus(gp) != _Grunning || readpstatus(gp.m.p) != _Prunning {
		return false
	}
	if activation.borrowed {
		return activation.previous == nil
	}
	previous := (*g)(activation.previous)
	mp := gp.m
	if previous == nil || previous == gp || previous.context == nil || previous.startarg != nil ||
		previous.m != mp || readgstatus(previous) != _Grunning {
		return false
	}
	casgstatus(gp, _Grunning, _Grunnable)
	mp.curg = previous
	gp.m = nil
	setg(previous)
	return true
}

// coroReleaseRuntimeContext tears down the runtime sidecar after the scheduler
// has made the G terminal but before its scanned task allocation is cleared.
func coroReleaseRuntimeContext(task *coro.G) bool {
	raw := coro.TaskLocal(task)
	ctx := (*coroRuntimeContext)(raw)
	if !validCoroRuntimeTaskContext(task, ctx) {
		return false
	}
	gp := &ctx.g
	if gp.m != nil || readgstatus(gp) != _Grunnable {
		return false
	}
	embedded := ctx.g.coroEmbedded
	released, ok := coro.ReleaseTaskLocal(task)
	if !ok || released != raw {
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
	releasePanicPCStore(gp)
	gp.startarg = nil
	gp.coroEmbedded = false
	casgstatus(gp, _Grunnable, _Gdead)
	gp.context = nil
	releaseGAndCheckDeadlock()
	if !embedded {
		FreeRoot(unsafe.Pointer(ctx))
	}
	return true
}

func discardCoroRuntimeContext(ctx *coroRuntimeContext, freeContext bool) {
	if ctx == nil {
		return
	}
	if ctx.g.localContext != nil {
		releaseLocalBlocks(ctx.g.localContext)
		ctx.g.localContext = nil
	}
	releasePanicPCStore(&ctx.g)
	ctx.g.context = nil
	ctx.g.coroEmbedded = false
	releaseG()
	if freeContext {
		FreeRoot(unsafe.Pointer(ctx))
	}
}
