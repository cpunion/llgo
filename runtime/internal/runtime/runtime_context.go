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

// runtimeContext keeps the G, M, and P for one native or stackless logical
// execution context in one allocation. Keeping this ownership core independent
// of pthread creation lets every scheduler backend reuse it directly.
type runtimeContext struct {
	g g
	m m
	p p
	// local is used only by a stackless logical G. Keeping it inside the rooted
	// sidecar gives package-local blocks the same lifetime as that G.
	local LocalContext

	// root is non-nil for contexts passed through a host-thread API or retained
	// by a stackless task. Such contexts remain visible to the collector until
	// their owning M or logical G exits.
	root unsafe.Pointer
}

var sched struct {
	goidgen uint64
	midgen  int64
	pidgen  int32

	// gstate packs the live/registered logical-context count with the
	// main-exited bit. One atomic word makes the native main-Goexit decision
	// independent of release order.
	gstate uint64
}

func allocRuntimeContext() *runtimeContext {
	size := unsafe.Sizeof(runtimeContext{})
	root := AllocRoot(size)
	if root == nil {
		coroRuntimeAbort("failed to allocate runtime context")
		return nil
	}
	c.Memset(root, 0, size)
	ctx := (*runtimeContext)(root)
	ctx.root = root
	return ctx
}

func initRuntimeContext(ctx *runtimeContext, callergp *g, status uint32) *g {
	gp := initRuntimeContextUntracked(ctx, callergp, status)
	retainG()
	return gp
}

// initRuntimeContextUntracked initializes the executor-thread placeholder
// installed in pthread TLS. It is a physical context used only while no
// stackless logical G is active, so it must not participate in the logical-G
// count or the main-Goexit deadlock decision.
func initRuntimeContextUntracked(ctx *runtimeContext, callergp *g, status uint32) *g {
	gp := &ctx.g
	mp := &ctx.m
	pp := &ctx.p

	gp.m = mp
	gp.atomicstatus = status
	gp.goid = nextGoid(gp)
	if callergp != nil {
		gp.parentGoid = callergp.goid
	}
	gp.context = ctx

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
