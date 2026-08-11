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
)

// NewProc creates a new G running fn.
//
// The compiler turns a go statement into a call to NewProc. Unlike the old
// lowering, this ABI contains no pthread types: the selected runtime backend
// decides how to provide an M and execute the G.
func NewProc(fn goroutineFunc, arg unsafe.Pointer, stackSize uintptr) {
	gp := newproc1(fn, arg, getg())
	if errno := newm(gp.m, stackSize); errno != 0 {
		ctx := gp.context
		releaseG()
		FreeRoot(arg)
		FreeRoot(ctx.root)
		panic("runtime: failed to create new OS thread")
	}
}

// newproc1 creates a runnable G and its initial M/P ownership. The pthread
// backend starts that G immediately; a future scheduler can enqueue the same G
// without changing the compiler ABI.
func newproc1(fn goroutineFunc, arg unsafe.Pointer, callergp *g) *g {
	if fn == nil {
		panic("go of nil func value")
	}

	ctx := allocRuntimeContext()
	gp := initRuntimeContext(ctx, callergp, _Grunnable)
	gp.startfn = fn
	gp.startarg = arg
	return gp
}

// newm starts the platform execution resource for mp.
func newm(mp *m, stackSize uintptr) int {
	return newosproc(mp, stackSize)
}

// mstart is the first LLGo runtime function executed on a new M.
func mstart(arg unsafe.Pointer) unsafe.Pointer {
	mp := (*m)(arg)
	if mp == nil || mp.curg == nil || mp.p == nil {
		fatal("runtime: invalid mstart context")
		return nil
	}
	gp := mp.curg
	pp := mp.p

	setg(gp)
	casgstatus(gp, _Grunnable, _Grunning)
	setpstatus(pp, _Prunning)

	fn, arg := gp.startfn, gp.startarg
	gp.startfn = nil
	gp.startarg = nil
	// newproc1 rejects a nil entry before publishing the context, but mstart
	// executes after a cross-thread field handoff that coroutine SSA cannot
	// treat as a dominating proof. Revalidate at the consumption boundary so
	// the raw C callback is both fail-safe and locally proven non-nil.
	if fn == nil {
		fatal("runtime: nil mstart entry")
		return nil
	}
	ret := fn(arg)
	mexit(mp)
	return ret
}

// mexit tears down the current 1:1 G/M/P context. It does not terminate the
// host thread so both a returning start routine and runtime.Goexit can share
// the same ownership cleanup.
func mexit(mp *m) {
	if mp == nil || mp.curg == nil || mp.p == nil {
		fatal("runtime: invalid mexit context")
		return
	}
	gp := mp.curg
	pp := mp.p
	ctx := gp.context
	root := ctx.root
	releaseGAndCheckDeadlock()

	casgstatus(gp, _Grunning, _Gdead)
	setpstatus(pp, _Pdead)

	pp.m = nil
	mp.p = nil
	mp.curg = nil
	gp.m = nil
	releasePanicPCStore(gp)

	setg(nil)
	if root != nil {
		ctx.root = nil
		FreeRoot(root)
	}
}

// GMPForTesting reports the current runtime ownership graph. It is kept
// internal to the compiler runtime and linked only by LLGo execution tests.
func GMPForTesting() (goid, parentGoid uint64, mid int64, pid int32, gstatus, pstatus uint32, linked bool) {
	gp := getg()
	if gp == nil || gp.m == nil || gp.m.p == nil {
		return
	}
	mp := gp.m
	pp := mp.p
	ctx := gp.context
	return gp.goid, gp.parentGoid, mp.id, pp.id, readgstatus(gp), readpstatus(pp),
		mp.curg == gp && pp.m == mp && ctx != nil &&
			&ctx.g == gp && &ctx.m == mp && &ctx.p == pp
}

// GStateForTesting reports the packed scheduler state without changing it.
// Execution tests use it to wait until the logical main G has completed mexit
// before allowing the last worker to return.
func GStateForTesting() (count uint64, mainExited bool) {
	return gStateForTesting()
}
