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

	c "github.com/xgo-dev/llgo/runtime/internal/clite"
	"github.com/xgo-dev/llgo/runtime/internal/coro"
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
		// Spawn already carries and validates the exact scheduler-owned parent
		// task. Child initialization consumes only its immutable goid, so it does
		// not require the logical runtime G to be installed in pthread TLS. A
		// context-free parent remains detached/runnable during the physical
		// resume; legacy or genuinely context-dependent callers remain attached
		// and running. Reject every mixed graph rather than inferring ownership
		// from ambient runtime state.
		switch readgstatus(parentG) {
		case _Grunnable:
			if parentG.m != nil {
				return nil, false
			}
		case _Grunning:
			if parentG.m == nil || parentG.m.curg != parentG || parentG.m.p == nil ||
				parentG.m.p.m != parentG.m || readpstatus(parentG.m.p) != _Prunning {
				return nil, false
			}
		default:
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

// coroInitializeTaskAllocationRuntimeContextCompiler consumes the task-local
// pointer published by BeginSpawnCompilerLocal. That core receipt proves the
// parent is in its exact resume and the child is still private. A logical
// runtime context is attached once and remains immutable until terminal task
// release, so child initialization needs only the parent-sidecar identity and
// the child's exact zero-filled tail; its parent's temporary physical M/P
// attachment is unrelated to copying the immutable parent goid.
//
// All potentially failing checks precede initialization, so the caller can use
// the ordinary scheduler rollback without a partially retained logical G.
func coroInitializeTaskAllocationRuntimeContextCompiler(
	task, parent *coro.G,
	ctx *coroRuntimeContext,
) bool {
	if task == nil || parent == nil || ctx == nil ||
		unsafe.Pointer(ctx) != unsafe.Add(unsafe.Pointer(task), coroTaskAllocationContextOffset) {
		return false
	}
	parentContext := (*coroRuntimeContext)(coro.TaskLocal(parent))
	if parentContext == nil {
		return false
	}
	parentG := &parentContext.g
	if parentG.context != parentContext || parentG.startarg != unsafe.Pointer(parent) {
		return false
	}

	if ctx != &(*coroTaskAllocation)(unsafe.Pointer(task)).context ||
		ctx.g.context != nil || ctx.g.localContext != nil {
		return false
	}
	gp := initCoroRuntimeContext(ctx, parentG, _Grunnable)
	gp.localContext = &ctx.local
	gp.isMain = false
	gp.coroEmbedded = true
	gp.startarg = unsafe.Pointer(task)
	return true
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
	return ctx != nil && validCoroRuntimeTaskG(task, &ctx.g)
}

func coroRecoverRuntimeGV1(task *coro.G) (*g, bool) {
	ctx := (*coroRuntimeContext)(coro.TaskLocal(task))
	if !validCoroRuntimeTaskContext(task, ctx) {
		return nil, false
	}
	gp := &ctx.g
	// A physical body which otherwise needs no runtime context deliberately
	// resumes without installing its logical G in pthread TLS. Recover aliases
	// are nevertheless task-owned metadata: taskLocal is the stable sidecar
	// identity across both attached and detached resumes. Accept only those two
	// complete lifecycle shapes instead of consulting the ambient executor G.
	switch readgstatus(gp) {
	case _Grunnable:
		if gp.m != nil {
			return nil, false
		}
	case _Grunning:
		if gp.m == nil || gp.m.curg != gp || gp.m.p == nil ||
			gp.m.p.m != gp.m || readpstatus(gp.m.p) != _Prunning {
			return nil, false
		}
	default:
		return nil, false
	}
	return gp, true
}

func coroBeginRecoverAliasRuntimeV1(
	task *coro.G, expected, token unsafe.Pointer, active bool,
) (unsafe.Pointer, bool) {
	gp, ok := coroRecoverRuntimeGV1(task)
	if !ok || gp.recoverPanic != nil {
		return nil, false
	}
	previous := gp.recoverFrame
	if active && (expected == nil || previous == expected) {
		gp.recoverFrame = token
	}
	return previous, true
}

func coroEndRecoverAliasRuntimeV1(task *coro.G, previous unsafe.Pointer) bool {
	gp, ok := coroRecoverRuntimeGV1(task)
	if !ok || gp.recoverPanic != nil {
		return false
	}
	gp.recoverFrame = previous
	return true
}

func coroHasRecoverAliasRuntimeV1(task *coro.G, token unsafe.Pointer) bool {
	gp, ok := coroRecoverRuntimeGV1(task)
	return ok && gp.recoverFrame == token
}

// validCoroRuntimeTaskG validates a task sidecar starting from the logical G.
// Enter starts from taskLocal, while the adjacent leave starts from the exact
// G installed in the previous physical M. Both directions converge on the
// same immutable context/task identity without adding another activation word.
func validCoroRuntimeTaskG(task *coro.G, gp *g) bool {
	if task == nil || gp == nil || gp.context == nil || &gp.context.g != gp ||
		gp.localContext != &gp.context.local || gp.startfn != nil ||
		gp.startarg != unsafe.Pointer(task) {
		return false
	}
	// This predicate runs around every physical coroutine resume. The task is
	// already scheduler-valid and coroEmbedded selects the combined allocation
	// representation. The command root is the only independently allocated
	// logical-task context; every dynamic G must match the exact tail address.
	if gp.coroEmbedded {
		return gp.context == &(*coroTaskAllocation)(unsafe.Pointer(task)).context
	}
	return gp.isMain
}

// coroCaptureRuntimeContextV1 snapshots the executor context once at a stable
// scheduler boundary. A bounded run slice always restores this exact context
// after every physical resume, so its later actions do not need another
// pthread TLS lookup merely to rediscover the same owner.
func coroCaptureRuntimeContextV1() unsafe.Pointer {
	return unsafe.Pointer(getg())
}

// coroEnterRuntimeContextFrom installs task's runtime G only for the physical
// llvm.coro.resume interval. A synchronous C-to-Go reentry resumes a child
// frame of the same logical G while its parent resume remains active below C;
// that exact nested case borrows the existing install. current is captured at
// the surrounding stable scheduler boundary and is restored by leave.
func coroEnterRuntimeContextFrom(task *coro.G, currentRaw unsafe.Pointer) (coroRuntimeContextActivationV1, bool) {
	ctx := (*coroRuntimeContext)(coro.TaskLocal(task))
	if !validCoroRuntimeTaskContext(task, ctx) {
		return coroRuntimeContextActivationV1{}, false
	}
	gp := &ctx.g
	current := (*g)(currentRaw)
	if current == gp {
		if gp.m == nil || gp.m.curg != gp || gp.m.p == nil || gp.m.p.m != gp.m ||
			readgstatus(gp) != _Grunning || readpstatus(gp.m.p) != _Prunning {
			return coroRuntimeContextActivationV1{}, false
		}
		return coroRuntimeContextActivationV1{
			previous: unsafe.Pointer(gp),
			borrowed: true,
		}, true
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
	setgCoro(gp)
	return coroRuntimeContextActivationV1{previous: unsafe.Pointer(current)}, true
}

func coroEnterRuntimeContext(task *coro.G) (coroRuntimeContextActivationV1, bool) {
	return coroEnterRuntimeContextFrom(task, coroCaptureRuntimeContextV1())
}

func coroLeaveRuntimeContext(task *coro.G, activation coroRuntimeContextActivationV1) bool {
	anchor := (*g)(activation.previous)
	if anchor == nil {
		return false
	}
	gp := anchor
	if !activation.borrowed {
		if anchor.m == nil {
			return false
		}
		gp = anchor.m.curg
	}
	if !validCoroRuntimeTaskG(task, gp) {
		return false
	}
	// Enter is the only operation which installs this logical G, and every
	// nested C-to-Go entry must borrow and restore the same installation before
	// llvm.coro.resume returns. No other runtime path writes the getg slot in
	// this interval. The activation's exact G/M edge is therefore the
	// authoritative post-resume certificate; re-reading taskLocal or pthread TLS
	// here would prove no additional state. setgCoro(previous) remains the
	// checked restore of the direct logical-G TLS mirror.
	if gp.m == nil || gp.m.curg != gp || gp.m.p == nil ||
		gp.m.p.m != gp.m || readgstatus(gp) != _Grunning || readpstatus(gp.m.p) != _Prunning {
		return false
	}
	if activation.borrowed {
		return gp == anchor
	}
	previous := anchor
	mp := gp.m
	if previous == nil || previous == gp || previous.context == nil || previous.startarg != nil ||
		previous.m != mp || readgstatus(previous) != _Grunning {
		return false
	}
	casgstatus(gp, _Grunning, _Grunnable)
	mp.curg = previous
	gp.m = nil
	setgCoro(previous)
	return true
}

// coroReleaseRuntimeContext tears down the runtime sidecar after the scheduler
// has made the G terminal but before its scanned task allocation is cleared.
func coroReleaseRuntimeContext(task *coro.G, raw unsafe.Pointer) bool {
	ctx := (*coroRuntimeContext)(raw)
	if !validCoroRuntimeTaskContext(task, ctx) {
		return false
	}
	gp := &ctx.g
	if gp.m != nil || readgstatus(gp) != _Grunnable {
		return false
	}
	embedded := ctx.g.coroEmbedded
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
