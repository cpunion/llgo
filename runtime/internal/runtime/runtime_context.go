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
)

// coroRuntimeContext is the state which follows one stackless logical G. M and
// P are physical executor resources and are deliberately absent: the logical G
// borrows its current executor's M/P only for a physical coroutine resume.
type coroRuntimeContext struct {
	g g
	// local gives package-local blocks the same lifetime as the logical G.
	local LocalContext
}

// runtimeContext owns a native or executor-placeholder G together with its
// physical M/P. Keeping coroRuntimeContext as the first field gives g.context
// one common prefix while independently allocated native contexts retain their
// allocation-base identity.
type runtimeContext struct {
	coroRuntimeContext
	m m
	p p
}

const runtimeContextCoreOffset = unsafe.Offsetof(runtimeContext{}.coroRuntimeContext)

// Native teardown frees g.context as the original independent-root base.
var _ [runtimeContextCoreOffset]byte = [0]byte{}

var sched struct {
	goidgen uint64
	midgen  int64
	pidgen  int32

	// gstate packs the live/registered logical-context count with the
	// main-exited bit. One atomic word makes the native main-Goexit decision
	// independent of release order.
	gstate uint64
}

// signalPanicPCStore is the allocation-free fallback used only when a legacy
// SA_SIGINFO callback faults before its current G has ever needed a panic
// snapshot. That callback's source snapshot is already process-global and
// documents concurrent faults as a lost race on a doomed process.
var signalPanicPCStore panicPCStore

func loadPanicPCStore(gp *g) *panicPCStore {
	if gp == nil {
		return nil
	}
	return gp.panicPCs
}

func ensurePanicPCStore(gp *g) *panicPCStore {
	if gp == nil {
		return nil
	}
	if gp.panicPCs != nil && gp.panicPCs != &signalPanicPCStore {
		return gp.panicPCs
	}
	size := unsafe.Sizeof(panicPCStore{})
	raw := AllocRoot(size)
	if raw == nil {
		coroRuntimeAbort("failed to allocate panic PC store")
		return nil
	}
	c.Memset(raw, 0, size)
	gp.panicPCs = (*panicPCStore)(raw)
	return gp.panicPCs
}

func signalSafePanicPCStore(gp *g) *panicPCStore {
	if gp == nil {
		return nil
	}
	if gp.panicPCs == nil {
		gp.panicPCs = &signalPanicPCStore
	}
	return gp.panicPCs
}

func releasePanicPCStore(gp *g) {
	if gp == nil || gp.panicPCs == nil {
		return
	}
	store := gp.panicPCs
	gp.panicPCs = nil
	if store == &signalPanicPCStore {
		return
	}
	size := unsafe.Sizeof(panicPCStore{})
	c.Memset(unsafe.Pointer(store), 0, size)
	FreeRoot(unsafe.Pointer(store))
}

func allocRuntimeContext() *runtimeContext {
	size := unsafe.Sizeof(runtimeContext{})
	root := AllocRoot(size)
	if root == nil {
		coroRuntimeAbort("failed to allocate runtime context")
		return nil
	}
	c.Memset(root, 0, size)
	return (*runtimeContext)(root)
}

func initRuntimeContext(ctx *runtimeContext, callergp *g, status uint32) *g {
	gp := initRuntimeContextUntracked(ctx, callergp, status)
	retainG()
	return gp
}

func initCoroRuntimeContext(ctx *coroRuntimeContext, callergp *g, status uint32) *g {
	gp := initCoroRuntimeContextUntracked(ctx, callergp, status)
	retainG()
	return gp
}

func initCoroRuntimeContextUntracked(ctx *coroRuntimeContext, callergp *g, status uint32) *g {
	gp := &ctx.g
	gp.atomicstatus = status
	gp.goid = nextGoid(gp)
	if callergp != nil {
		gp.parentGoid = callergp.goid
	}
	gp.context = ctx
	return gp
}

// initRuntimeContextUntracked initializes the executor-thread placeholder
// installed in pthread TLS. It is a physical context used only while no
// stackless logical G is active, so it must not participate in the logical-G
// count or the main-Goexit deadlock decision.
func initRuntimeContextUntracked(ctx *runtimeContext, callergp *g, status uint32) *g {
	gp := initCoroRuntimeContextUntracked(&ctx.coroRuntimeContext, callergp, status)
	mp := &ctx.m
	pp := &ctx.p

	gp.m = mp
	mp.curg = gp
	mp.p = pp
	mp.id = nextMid(mp)

	pp.id = nextPid(pp)
	pstatus := uint32(_Pidle)
	if status == _Grunning {
		pstatus = _Prunning
	}
	setpstatus(pp, pstatus)
	pp.m = mp
	return gp
}
