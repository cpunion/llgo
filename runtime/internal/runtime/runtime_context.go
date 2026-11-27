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

	// startfn and startRoot belong exclusively to native pthread goroutines.
	// Stackless tasks carry their entry and arguments in the task/frame
	// allocation and never pay for these fields inside coroTaskAllocation.
	startfn   goroutineFunc
	startRoot unsafe.Pointer
}

const runtimeContextCoreOffset = unsafe.Offsetof(runtimeContext{}.coroRuntimeContext)
const coroRuntimeContextGOffset = unsafe.Offsetof(coroRuntimeContext{}.g)

// Native teardown frees g.context as the original independent-root base, and
// panic boundaries resolve (*g)(coro.TaskLocal(task)) directly in O(1).
var _ [runtimeContextCoreOffset]byte = [0]byte{}
var _ [coroRuntimeContextGOffset]byte = [0]byte{}

const coroGCountShardBias = uint64(1) << 62

type coroGCountShard struct {
	state uint64
	_     [120]byte
}

var (
	_ [8 - coro.ExecutorFleetCapacity]byte
	_ [coro.ExecutorFleetCapacity - 8]byte
)

// coroGCountShards holds biased per-route live-G counters for embedded
// stackless goroutines so concurrent spawn/exit across Ps never bounce
// sched.gstate's cache line. Bit 63 mirrors mainExitedBit so normal exits
// perform a single atomic operation on one route-local cache line.
var coroGCountShards = [coro.ExecutorFleetCapacity]coroGCountShard{
	{state: coroGCountShardBias},
	{state: coroGCountShardBias},
	{state: coroGCountShardBias},
	{state: coroGCountShardBias},
	{state: coroGCountShardBias},
	{state: coroGCountShardBias},
	{state: coroGCountShardBias},
	{state: coroGCountShardBias},
}

var sched struct {
	goidgen uint64
	_pad0   [120]byte
	midgen  int64
	pidgen  int32
	_pad1   [116]byte

	// gstate packs the unsharded/global live logical-context count with the
	// main-exited bit. One atomic word makes the native main-Goexit decision
	// independent of release order.
	gstate uint64
	// gstateSeq counts retains observed after mainExitedBit is set so gState
	// retries across concurrent post-Goexit cross-shard spawn/exit chains
	// without adding atomic traffic to normal spawn/exit paths.
	gstateSeq uint64
	_pad2     [112]byte
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
	gp.goid = nextGoid(gp, callergp)
	if callergp != nil {
		gp.parentGoid = callergp.goid
	}
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
	gp.goid = nextGoid(gp, callergp)
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
	// A physical executor placeholder is not a logical goroutine. In
	// particular, bootstrap must not consume the command goroutine's ID 1.
	gp := &ctx.g
	gp.atomicstatus = status
	gp.context = &ctx.coroRuntimeContext
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
