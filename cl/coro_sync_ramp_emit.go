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

package cl

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"

	"github.com/xgo-dev/llgo/internal/coro"
	llssa "github.com/xgo-dev/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

// -----------------------------------------------------------------------------
// Emission: $coro.slow, $coro.try, nullable-handle $coro ramp, outcome propagator
// -----------------------------------------------------------------------------

type coroSyncSlowResumeState struct {
	plan               *coroPhysicalSyncRampPlan
	resumeTagParam     llssa.Expr
	preChildParam      llssa.Expr
	preResultSlotParam llssa.Expr
	liveParams         []llssa.Expr
	liveSlots          map[ssa.Value]llssa.Expr
	resumeAwaitBlk     llssa.BasicBlock
	resumePostBlk      llssa.BasicBlock
	singleAwaitDone    bool
}

// dominatesDebugUse reports whether iv's LLVM SSA definition dominates ref
// inside the augmented CFG of $coro.slow (after adding slow-resume entry edges).
func (s *coroSyncSlowResumeState) dominatesDebugUse(iv instrOrValue, ref *ssa.DebugRef) bool {
	if s == nil || s.plan == nil || s.plan.slowAugCFG == nil {
		return true
	}
	cfg := s.plan.slowAugCFG
	defBlock, useBlock := iv.Block(), ref.Block()
	if defBlock == nil || useBlock == nil {
		return false
	}
	if defBlock == useBlock {
		if cfg.splitBlock != nil && defBlock == cfg.splitBlock {
			if iv == s.plan.singleAwaitCall {
				return true
			}
			defIdx, useIdx := -1, -1
			for i, ins := range defBlock.Instrs {
				if ins == iv {
					defIdx = i
				}
				if ins == ref {
					useIdx = i
				}
			}
			if defIdx < 0 || useIdx < 0 {
				return false
			}
			return (defIdx < cfg.splitCallIdx && useIdx < cfg.splitCallIdx) ||
				(defIdx > cfg.splitCallIdx && useIdx > cfg.splitCallIdx)
		}
		return true
	}
	if !cfg.reach[useBlock] {
		return true
	}
	if cfg.splitBlock != nil && defBlock == cfg.splitBlock && iv != s.plan.singleAwaitCall {
		for i, ins := range defBlock.Instrs {
			if ins == iv {
				if i < cfg.splitCallIdx {
					return false
				}
				break
			}
		}
	}
	return cfg.dom[useBlock][defBlock]
}

type coroSyncEmitEnv struct {
	vals      map[ssa.Value]llssa.Expr
	zero      map[ssa.Value]bool
	nilIfaces map[ssa.Value]bool
}

func newCoroSyncEmitEnv() coroSyncEmitEnv {
	return coroSyncEmitEnv{
		vals:      make(map[ssa.Value]llssa.Expr),
		zero:      make(map[ssa.Value]bool),
		nilIfaces: make(map[ssa.Value]bool),
	}
}

func (e coroSyncEmitEnv) clone() coroSyncEmitEnv {
	return coroSyncEmitEnv{
		vals:      cloneMap(e.vals),
		zero:      cloneMap(e.zero),
		nilIfaces: cloneMap(e.nilIfaces),
	}
}

func coroSyncSlowPhysicalSig(baseSig *types.Signature, ramp *coroPhysicalSyncRampPlan) *types.Signature {
	if ramp == nil || (len(ramp.slowTargets) == 0 && ramp.kind != coroSyncRampSingleAwait) {
		return baseSig
	}
	params := make([]*types.Var, 0, baseSig.Params().Len()+3+len(ramp.liveValues))
	for i := 0; i < baseSig.Params().Len(); i++ {
		params = append(params, baseSig.Params().At(i))
	}
	params = append(params, types.NewParam(token.NoPos, nil, "__llgo_resume_tag", types.Typ[types.Uint8]))
	if ramp.kind == coroSyncRampSingleAwait {
		params = append(params, types.NewParam(token.NoPos, nil, "__llgo_pre_child", types.Typ[types.UnsafePointer]))
		if ramp.singleAwaitBoxed {
			params = append(params, types.NewParam(token.NoPos, nil, "__llgo_pre_result_slot", types.Typ[types.UnsafePointer]))
		}
	}
	for i, v := range ramp.liveValues {
		params = append(params, types.NewParam(token.NoPos, nil, fmt.Sprintf("__llgo_live_%d", i), v.Type()))
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), baseSig.Results(), false)
}

func coroSyncBoxResultTuple() *types.Tuple {
	ptr := types.Typ[types.UnsafePointer]
	return types.NewTuple(
		types.NewParam(token.NoPos, nil, "__llgo_child", ptr),
		types.NewParam(token.NoPos, nil, "__llgo_async_out", ptr),
	)
}

func coroSyncBoxHelperSig(sourceSig *types.Signature) *types.Signature {
	sourceSig = coroPhysicalNormalizeSourceSignature(sourceSig)
	params := make([]*types.Var, 0, sourceSig.Params().Len()+2)
	params = append(params,
		types.NewParam(token.NoPos, nil, "__llgo_g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "__llgo_out", types.Typ[types.UnsafePointer]),
	)
	for i := 0; i < sourceSig.Params().Len(); i++ {
		params = append(params, sourceSig.Params().At(i))
	}
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), coroSyncBoxResultTuple(), false)
}

func (p *context) coroSyncBoxHelperFunc(target *ssa.Function) llssa.Function {
	entry := p.mustFunctionSymbol(target)
	coroFn, _, kind := p.compileFunctionEntry(entry)
	if kind != goFunc || coroFn == nil {
		panic("sync-ramp .box callee did not resolve to a Go coroutine entry")
	}
	sourceSig, err := p.emissionUniverse.coroPhysicalSourceSignature(target)
	if err != nil {
		panic(fmt.Errorf("sync-ramp .box callee signature: %w", err))
	}
	return p.pkg.NewFuncEx(coroFn.Name()+".box", coroSyncBoxHelperSig(sourceSig), llssa.InGo, false, false)
}

func coroSyncTryHelperSig(sourceSig *types.Signature) *types.Signature {
	sourceSig = coroPhysicalNormalizeSourceSignature(sourceSig)
	params := make([]*types.Var, 0, sourceSig.Params().Len()+2)
	params = append(params,
		types.NewParam(token.NoPos, nil, "__llgo_g", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "__llgo_out", types.Typ[types.UnsafePointer]),
	)
	for i := 0; i < sourceSig.Params().Len(); i++ {
		params = append(params, sourceSig.Params().At(i))
	}
	results := types.NewTuple(types.NewParam(token.NoPos, nil, "__llgo_ok", types.Typ[types.Bool]))
	return types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), results, false)
}

func (p *context) coroSyncTryHelperFunc(target *ssa.Function) llssa.Function {
	entry := p.mustFunctionSymbol(target)
	coroFn, _, kind := p.compileFunctionEntry(entry)
	if kind != goFunc || coroFn == nil {
		panic("sync-ramp .try callee did not resolve to a Go coroutine entry")
	}
	sourceSig, err := p.emissionUniverse.coroPhysicalSourceSignature(target)
	if err != nil {
		panic(fmt.Errorf("sync-ramp .try callee signature: %w", err))
	}
	return p.pkg.NewFuncEx(coroFn.Name()+".try", coroSyncTryHelperSig(sourceSig), llssa.InGo, false, false)
}

// compileCoroSyncRampPhysicalBody emits $coro.slow (reusing the standard
// coroutine physical body emitter), an optional $coro.try helper, and the
// plain nullable-handle $coro ramp.
func (p *context) compileCoroSyncRampPhysicalBody(
	b llssa.Builder,
	fn *ssa.Function,
	physicalPlan *coroPhysicalFunctionPlan,
	abi coroPhysicalABI,
	sourceParamBase int,
	isInit bool,
) {
	ramp := physicalPlan.syncRamp
	if ramp == nil || !ramp.needsSlowSplit {
		panic("compileCoroSyncRampPhysicalBody requires a split sync-fast ramp plan")
	}
	rampFn := p.fn
	slowSig := coroSyncSlowPhysicalSig(abi.physicalSig, ramp)
	slowFn := p.pkg.NewFuncEx(rampFn.Name()+".slow", slowSig, llssa.InGo, false, true)
	// Keep the cold coroutine-allocating .slow ramp out of the hot sync-fast
	// entry so the fast path stays leaf-like with minimal register spills.
	slowFn.Inline(llssa.NoInline)

	// 1. Materialize $coro.slow via the standard coroutine physical body emitter.
	if !slowFn.HasBody() {
		savedFn, savedBvals, savedAddrs := p.fn, p.bvals, p.coroValueAddrs
		savedPhis, savedBlkInfos, savedSlow := p.phis, p.blkInfos, p.coroSyncSlowResume
		savedDebugDIVars := p.debugDIVars

		p.fn = slowFn
		p.bvals = make(map[ssa.Value]llssa.Expr)
		p.coroValueAddrs = make(map[ssa.Value]llssa.Expr)
		p.phis, p.blkInfos = nil, nil
		if savedDebugDIVars != nil {
			p.debugDIVars = make(map[*types.Var]llssa.DIVar)
		}

		if len(ramp.slowTargets) > 0 || ramp.kind == coroSyncRampSingleAwait {
			extraBase := abi.physicalSig.Params().Len()
			state := &coroSyncSlowResumeState{
				plan:           ramp,
				resumeTagParam: slowFn.PhysicalParam(extraBase),
			}
			liveBase := extraBase + 1
			if ramp.kind == coroSyncRampSingleAwait {
				state.preChildParam = slowFn.PhysicalParam(extraBase + 1)
				liveBase = extraBase + 2
				if ramp.singleAwaitBoxed {
					state.preResultSlotParam = slowFn.PhysicalParam(extraBase + 2)
					liveBase = extraBase + 3
				}
			}
			if len(ramp.liveValues) > 0 {
				state.liveParams = make([]llssa.Expr, len(ramp.liveValues))
				for i := range ramp.liveValues {
					state.liveParams[i] = slowFn.PhysicalParam(liveBase + i)
				}
			}
			p.coroSyncSlowResume = state
		}

		slowBuilder := slowFn.MakeBody(1)
		if p.options.Debug && p.goProg != nil && p.goProg.Fset != nil {
			pos := p.goProg.Fset.Position(fn.Pos())
			bodyPos := p.getFuncBodyPos(fn)
			logicalName := slowFn.Name()
			if fn.Pkg != nil && fn.Pkg.Pkg != nil {
				logicalName = funcInfoDisplayName(funcName(fn.Pkg.Pkg, fn, false))
			}
			slowBuilder.DebugFunctionNamed(slowFn, logicalName, slowFn.Name(), debugFunctionScope(fn), pos, bodyPos)
		}
		p.compileCoroPhysicalBodyCore(slowBuilder, fn, physicalPlan, abi, sourceParamBase, isInit)
		slowBuilder.EndBuild()
		slowBuilder.Dispose()

		p.debugDIVars = savedDebugDIVars
		p.coroSyncSlowResume, p.blkInfos, p.phis = savedSlow, savedBlkInfos, savedPhis
		p.coroValueAddrs, p.bvals, p.fn = savedAddrs, savedBvals, savedFn
	}

	nullPtr := p.prog.Nil(p.prog.VoidPtr())

	// 2. If hasTryHelper, materialize F$coro.try(g, out, args...) -> bool and
	// make F$coro a thin wrapper around F$coro.try + F$coro.slow.
	var tryFn llssa.Function
	if ramp.hasTryHelper {
		sourceSig, err := p.emissionUniverse.coroPhysicalSourceSignature(fn)
		if err != nil {
			panic(fmt.Errorf("sync-ramp .try signature: %w", err))
		}
		_, isLinkOnce := p.linkOnceFns[fn]
		tryLinkOnce := isLinkOnce || fn.Synthetic != "" || fn.Origin() != nil || len(fn.TypeArgs()) != 0
		tryFn = p.pkg.NewFuncEx(rampFn.Name()+".try", coroSyncTryHelperSig(sourceSig), llssa.InGo, false, tryLinkOnce)
		if !tryFn.HasBody() {
			savedFn := p.fn
			p.fn = tryFn
			tb := tryFn.MakeBody(1)
			tb.SetBlock(tryFn.Block(0))
			emitter := &coroSyncFastEmitter{
				p: p, b: tb, fn: fn, plan: physicalPlan, ramp: ramp, abi: abi,
				task: tryFn.PhysicalParam(0), resultSlot: tryFn.PhysicalParam(1), tryMode: true,
			}
			emitter.initParams(tryFn, sourceParamBase)
			if ramp.kind == coroSyncRampBoundedLoop {
				bailBlk := tryFn.MakeBlock()
				emitter.bailStart = bailBlk
				emitter.emitBoundedLoopCFG()
				tb.SetBlockEx(bailBlk, llssa.AtEnd, false)
				emitter.emitBailStart()
			} else {
				emitter.emitNode(ramp.root, newCoroSyncEmitEnv())
			}
			tb.EndBuild()
			tb.Dispose()
			p.fn = savedFn
		}
	}

	// 2b. If hasBoxHelper, materialize F$coro.box(g, fastOut, args...) -> (child, asyncOut).
	// Fast completions write directly into caller's stack fastOut and return (nil, nil);
	// slow completions heap-allocate a fresh result slot so preChild never retains a stack pointer.
	if ramp.hasBoxHelper {
		sourceSig, err := p.emissionUniverse.coroPhysicalSourceSignature(fn)
		if err != nil {
			panic(fmt.Errorf("sync-ramp .box signature: %w", err))
		}
		_, isLinkOnce := p.linkOnceFns[fn]
		boxLinkOnce := isLinkOnce || fn.Synthetic != "" || fn.Origin() != nil || len(fn.TypeArgs()) != 0
		boxFn := p.pkg.NewFuncEx(rampFn.Name()+".box", coroSyncBoxHelperSig(sourceSig), llssa.InGo, false, boxLinkOnce)
		if !boxFn.HasBody() {
			savedFn := p.fn
			p.fn = boxFn
			bb := boxFn.MakeBody(1)
			bb.SetBlock(boxFn.Block(0))
			boxEmitter := &coroSyncFastEmitter{
				p: p, b: bb, fn: fn, plan: physicalPlan, ramp: ramp, abi: abi,
				task: boxFn.PhysicalParam(0), resultSlot: boxFn.PhysicalParam(1),
				slowFn: slowFn, boxMode: true,
			}
			boxEmitter.initParams(boxFn, sourceParamBase)
			if ramp.hasTryHelper {
				paramCount := abi.physicalSig.Params().Len()
				args := make([]llssa.Expr, paramCount)
				for i := 0; i < paramCount; i++ {
					args[i] = boxFn.PhysicalParam(i)
				}
				fastBlk, slowBlk, doneBlk := boxFn.MakeBlock(), boxFn.MakeBlock(), boxFn.MakeBlock()
				bb.IfWithBranchWeights(bb.BinOp(token.NEQ, args[1], nullPtr), fastBlk, slowBlk, 1000, 1)

				bb.SetBlockEx(fastBlk, llssa.AtEnd, false)
				bb.IfWithBranchWeights(bb.Call(tryFn.Expr, args...), doneBlk, slowBlk, 1000, 1)

				bb.SetBlockEx(doneBlk, llssa.AtEnd, false)
				boxEmitter.returnBoxPair(nullPtr, nullPtr)

				bb.SetBlockEx(slowBlk, llssa.AtEnd, false)
				boxEmitter.emitSlowCall(0, nullPtr, nil)
			} else {
				fastEntry, bailStart := boxFn.MakeBlock(), boxFn.MakeBlock()
				boxEmitter.bailStart = bailStart
				bb.IfWithBranchWeights(bb.BinOp(token.NEQ, boxFn.PhysicalParam(1), nullPtr), fastEntry, bailStart, 1000, 1)

				bb.SetBlockEx(bailStart, llssa.AtEnd, false)
				boxEmitter.emitSlowCall(0, nullPtr, nil)

				bb.SetBlockEx(fastEntry, llssa.AtEnd, false)
				if ramp.kind == coroSyncRampBoundedLoop {
					boxEmitter.emitBoundedLoopCFG()
				} else {
					boxEmitter.emitNode(ramp.root, newCoroSyncEmitEnv())
				}
			}
			bb.EndBuild()
			bb.Dispose()
			p.fn = savedFn
		}
	}

	if ramp.hasTryHelper {
		b.SetBlock(rampFn.Block(0))
		paramCount := abi.physicalSig.Params().Len()
		args := make([]llssa.Expr, paramCount)
		for i := 0; i < paramCount; i++ {
			args[i] = rampFn.PhysicalParam(i)
		}
		fastBlk, slowBlk, doneBlk := rampFn.MakeBlock(), rampFn.MakeBlock(), rampFn.MakeBlock()
		b.IfWithBranchWeights(b.BinOp(token.NEQ, args[1], nullPtr), fastBlk, slowBlk, 1000, 1)

		b.SetBlockEx(fastBlk, llssa.AtEnd, false)
		b.IfWithBranchWeights(b.Call(tryFn.Expr, args...), doneBlk, slowBlk, 1000, 1)

		b.SetBlockEx(doneBlk, llssa.AtEnd, false)
		b.Return(nullPtr)

		b.SetBlockEx(slowBlk, llssa.AtEnd, false)
		if abi.resultCount == 0 {
			args[1] = nullPtr
		}
		b.Return(b.Call(slowFn.Expr, args...))
		return
	}

	// 3. Emit the plain nullable-handle F$coro ramp directly.
	b.SetBlock(rampFn.Block(0))
	g, out := rampFn.PhysicalParam(0), rampFn.PhysicalParam(1)
	fastEntry, bailStart := rampFn.MakeBlock(), rampFn.MakeBlock()
	b.IfWithBranchWeights(b.BinOp(token.NEQ, out, nullPtr), fastEntry, bailStart, 1000, 1)

	b.SetBlockEx(bailStart, llssa.AtEnd, false)
	emitter := &coroSyncFastEmitter{
		p: p, b: b, fn: fn, plan: physicalPlan, ramp: ramp, abi: abi,
		task: g, resultSlot: out, slowFn: slowFn, bailStart: bailStart,
	}
	emitter.initParams(rampFn, sourceParamBase)
	emitter.emitSlowCall(0, nullPtr, nil)

	b.SetBlockEx(fastEntry, llssa.AtEnd, false)
	if ramp.kind == coroSyncRampBoundedLoop {
		emitter.emitBoundedLoopCFG()
	} else {
		emitter.emitNode(ramp.root, newCoroSyncEmitEnv())
	}
}

func (p *context) compileCoroCallerInlineYieldAwait(
	b llssa.Builder,
	target *ssa.Function,
	targetPlan *coroPhysicalFunctionPlan,
	args []llssa.Expr,
) {
	ramp := targetPlan.syncRamp
	if ramp == nil || ramp.kind != coroSyncRampCallerInlineYield {
		panic("caller-inline-yield await requires a frozen yield-inline plan")
	}
	doneBlk := p.fn.MakeBlock()
	emitter := &coroSyncFastEmitter{
		p: p, b: b, fn: target, plan: targetPlan, ramp: ramp,
		task: p.coroTask(), callerInline: true, callerDoneBlk: doneBlk, callerArgVals: args,
	}
	emitter.emitNode(ramp.root, newCoroSyncEmitEnv())
	b.SetBlockContinuation(doneBlk)
}

type coroSyncFastEmitter struct {
	p                  *context
	b                  llssa.Builder
	fn                 *ssa.Function
	plan               *coroPhysicalFunctionPlan
	ramp               *coroPhysicalSyncRampPlan
	abi                coroPhysicalABI
	task               llssa.Expr
	resultSlot         llssa.Expr
	slowFn             llssa.Function
	bailStart          llssa.BasicBlock
	paramExprs         []llssa.Expr
	tryMode            bool
	boxMode            bool
	callerInline       bool
	callerDoneBlk      llssa.BasicBlock
	callerArgVals      []llssa.Expr
	localAllocs        map[*ssa.Alloc]llssa.Expr
	singleAwaitPreEnv  map[ssa.Value]llssa.Expr
	singleAwaitTmpSlot llssa.Expr
	inSingleAwaitPost  bool
	outcomeCompletion  llssa.Expr
	traceDescriptor    llssa.Expr
}

func (e *coroSyncFastEmitter) initParams(llFn llssa.Function, sourceParamBase int) {
	e.paramExprs = make([]llssa.Expr, len(e.fn.Params))
	for i := range e.fn.Params {
		e.paramExprs[i] = llFn.PhysicalParam(sourceParamBase + i)
	}
}

func (e *coroSyncFastEmitter) entryAlloca(typ llssa.Type) llssa.Expr {
	alloc := e.b.Func.NewBuilder()
	defer alloc.Dispose()
	alloc.SetBlockEx(e.b.Func.Block(0), llssa.AtStart, true)
	return alloc.AllocaT(typ)
}

func (e *coroSyncFastEmitter) completionSlot() llssa.Expr {
	if e.outcomeCompletion.IsNil() {
		e.outcomeCompletion = e.entryAlloca(outcomePlainCompletionType(e.p.prog))
	}
	return e.outcomeCompletion
}

func (e *coroSyncFastEmitter) descriptorPtr() llssa.Expr {
	if e.traceDescriptor.IsNil() {
		e.traceDescriptor = e.b.Convert(e.p.prog.VoidPtr(), e.p.materializeCoroFrameDescriptor(e.abi))
	}
	return e.traceDescriptor
}

func (e *coroSyncFastEmitter) boxResultType() llssa.Type {
	return e.p.prog.Type(coroSyncBoxResultTuple(), llssa.InGo)
}

func (e *coroSyncFastEmitter) returnBoxPair(child, asyncOut llssa.Expr) {
	e.b.Return(e.b.Aggregate(e.boxResultType(), child, asyncOut))
}

func (p *context) allocCoroSyncBox(b llssa.Builder, elem llssa.Type) llssa.Expr {
	if p.prog.SizeOf(elem) == 0 {
		return b.Convert(p.prog.VoidPtr(), b.Alloc(elem, true))
	}
	if p.emissionUniverse != nil && p.emissionUniverse.CompleteRuntimeABI() {
		rtPkg := p.emissionUniverse.byPath[llssa.PkgRuntime]
		if rtPkg == nil {
			panic("sync-ramp async box requires runtime package")
		}
		allocZ := p.emissionUniverse.canonicalAlias(rtPkg.ssa.Func("AllocZ"))
		if allocZ == nil {
			panic("sync-ramp async box requires runtime.AllocZ")
		}
		targetPlan, planned := p.immutablePlan().FunctionPlan(allocZ)
		if !planned {
			panic("sync-ramp async box runtime.AllocZ is unplanned")
		}
		var fn llssa.Function
		var kind int
		if targetPlan.Emission == coro.EmitPlain {
			fn, _, kind = p.compileFunction(allocZ)
		} else if p.immutablePlan().HasRawPlainVariant(allocZ) {
			fn, _, kind = p.compileRawPlainFunction(allocZ)
		}
		if fn == nil || kind != goFunc {
			panic("sync-ramp async box runtime.AllocZ did not resolve to a callable Go entry")
		}
		return b.Call(fn.Expr, llssa.SizeOf(p.prog, elem))
	}
	return b.Convert(p.prog.VoidPtr(), b.Alloc(elem, true))
}

func (e *coroSyncFastEmitter) allocAsyncBox() llssa.Expr {
	resSlotType := e.p.prog.Type(e.abi.resultSlotType, llssa.InGo)
	return e.p.allocCoroSyncBox(e.b, resSlotType)
}

func (e *coroSyncFastEmitter) emitBailStart() {
	if e.tryMode {
		e.b.Return(e.p.prog.BoolVal(false))
		return
	}
	e.b.Jump(e.bailStart)
}

func (e *coroSyncFastEmitter) emitResumePostCall() {
	nullPtr := e.p.prog.Nil(e.p.prog.VoidPtr())
	if e.ramp.singleAwaitBoxed {
		callSlotType := e.p.prog.Type(coro.ResultSlotType(e.ramp.singleAwaitResults), llssa.InGo)
		boxPtr := e.p.allocCoroSyncBox(e.b, callSlotType)
		typedBox := e.b.Convert(e.p.prog.Pointer(callSlotType), boxPtr)
		e.b.StoreKnownNonNil(typedBox, e.b.LoadKnownNonNil(e.singleAwaitTmpSlot))
		e.emitSlowCallWithSlot(2, nullPtr, boxPtr, e.singleAwaitPreEnv)
		return
	}
	e.emitSlowCallWithSlot(2, llssa.Nil, llssa.Nil, e.singleAwaitPreEnv)
}

func (e *coroSyncFastEmitter) emitSlowCall(tag uint8, preChild llssa.Expr, vals map[ssa.Value]llssa.Expr) {
	e.emitSlowCallWithSlot(tag, preChild, llssa.Nil, vals)
}

func (e *coroSyncFastEmitter) emitSlowCallWithSlot(tag uint8, preChild, preResultSlot llssa.Expr, vals map[ssa.Value]llssa.Expr) {
	nullPtr := e.p.prog.Nil(e.p.prog.VoidPtr())
	outArg := e.resultSlot
	var asyncBox llssa.Expr
	if e.boxMode {
		asyncBox = e.allocAsyncBox()
		outArg = asyncBox
	} else if e.abi.resultCount == 0 {
		outArg = nullPtr
	}
	args := make([]llssa.Expr, 0, len(e.paramExprs)+5+len(e.ramp.liveValues))
	args = append(args, e.task, outArg)
	args = append(args, e.paramExprs...)
	if len(e.ramp.slowTargets) > 0 || e.ramp.kind == coroSyncRampSingleAwait {
		u8 := e.p.type_(types.Typ[types.Uint8], llssa.InGo)
		args = append(args, e.p.prog.IntVal(uint64(tag), u8))
		if e.ramp.kind == coroSyncRampSingleAwait {
			if preChild.IsNil() {
				preChild = nullPtr
			}
			args = append(args, preChild)
			if e.ramp.singleAwaitBoxed {
				if preResultSlot.IsNil() {
					preResultSlot = nullPtr
				}
				args = append(args, preResultSlot)
			}
		}
		required := e.ramp.liveInByTag[tag]
		for _, lv := range e.ramp.liveValues {
			if vals != nil {
				if val, ok := vals[lv]; ok {
					args = append(args, val)
					continue
				}
			}
			if tag != 0 && required[lv] {
				panic(fmt.Sprintf("sync-ramp slow exit tag=%d in %q missing live-in SSA value %s", tag, e.fn.String(), lv.String()))
			}
			args = append(args, e.p.prog.Zero(e.p.type_(lv.Type(), llssa.InGo)))
		}
	}
	child := e.b.Call(e.slowFn.Expr, args...)
	if e.boxMode {
		e.returnBoxPair(child, asyncBox)
		return
	}
	e.b.Return(child)
}

func (e *coroSyncFastEmitter) evalValue(v ssa.Value, vals map[ssa.Value]llssa.Expr) llssa.Expr {
	if val, ok := vals[v]; ok {
		return val
	}
	switch v := v.(type) {
	case *ssa.Parameter:
		for i, param := range e.fn.Params {
			if param == v {
				if e.callerInline {
					return e.callerArgVals[i]
				}
				return e.paramExprs[i]
			}
		}
		panic("sync-ramp parameter not found")
	case *ssa.Const:
		return e.b.Const(v.Value, e.p.type_(types.Default(v.Type()), llssa.InGo))
	case *ssa.Global:
		return e.p.varOf(e.b, v)
	case *ssa.Function:
		return e.p.compileValue(e.b, v)
	default:
		panic(fmt.Sprintf("sync-ramp missing SSA value %T %q", v, v.String()))
	}
}

func (e *coroSyncFastEmitter) evalValueAs(v ssa.Value, target types.Type, vals map[ssa.Value]llssa.Expr) llssa.Expr {
	if isUntypedNilConst(v) {
		return e.p.nilOf(target)
	}
	return e.evalValue(v, vals)
}

func (e *coroSyncFastEmitter) emitOutcomePropagateCall(status, typeWord, dataWord llssa.Expr, pos token.Pos) {
	if e.tryMode {
		panic("sync-ramp .try helper cannot emit handle-returning outcome propagation")
	}
	line := uint32(0)
	if e.p.fset != nil && pos.IsValid() {
		line = uint32(e.p.fset.Position(pos).Line)
	}
	handle := e.b.Call(
		e.p.coroOutcomePropagateCoroutine().Expr,
		e.task,
		e.p.prog.Nil(e.p.prog.VoidPtr()),
		e.descriptorPtr(),
		e.p.prog.IntVal(uint64(line), e.p.prog.Uint32()),
		status,
		typeWord,
		dataWord,
	)
	if e.boxMode {
		e.returnBoxPair(handle, e.p.prog.Nil(e.p.prog.VoidPtr()))
		return
	}
	e.b.Return(handle)
}

func (e *coroSyncFastEmitter) emitNilGuard(ptr llssa.Expr, mutated bool, pos token.Pos) {
	isNil := e.b.BinOp(token.EQL, ptr, e.p.prog.Nil(ptr.Type))
	failBlk, contBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
	e.b.IfWithBranchWeights(isNil, failBlk, contBlk, 1, 1000)

	e.b.SetBlockEx(failBlk, llssa.AtEnd, false)
	if !mutated {
		e.emitBailStart()
	} else {
		nullPtr := e.p.prog.Nil(e.p.prog.VoidPtr())
		e.emitOutcomePropagateCall(
			e.p.prog.IntVal(coroOutcomePropagateLocalFaultNil, e.p.prog.Uint32()),
			nullPtr, nullPtr, pos,
		)
	}
	e.b.SetBlockContinuation(contBlk)
}

func (e *coroSyncFastEmitter) emitBoundedLoopCFG() {
	loop := e.ramp.boundedLoop
	if loop == nil {
		panic("sync-ramp bounded-loop emission requires boundedLoop plan")
	}

	var ir *coroProgramIR
	if e.p.emissionUniverse != nil {
		ir = e.p.emissionUniverse.coroProgramIR
	}

	// Visit all live blocks in dominator-tree preorder so every non-Phi SSA
	// value is emitted before any block that uses it.
	var order []*ssa.BasicBlock
	var collectDom func(blk *ssa.BasicBlock)
	collectDom = func(blk *ssa.BasicBlock) {
		if blk == nil || !loop.liveBlocks[blk] {
			return
		}
		order = append(order, blk)
		for _, child := range blk.Dominees() {
			collectDom(child)
		}
	}
	collectDom(e.fn.Blocks[0])

	llvmBlocks := make(map[*ssa.BasicBlock]llssa.BasicBlock, len(order))
	for _, blk := range order {
		llvmBlocks[blk] = e.b.Func.MakeBlock()
	}
	var preheaderBlk llssa.BasicBlock
	if loop.entryGuardKind != coroSyncLoopEntryGuardNone {
		preheaderBlk = e.b.Func.MakeBlock()
	}
	backedgeCheckBlk := e.b.Func.MakeBlock()
	backedgeContBlk := e.b.Func.MakeBlock()

	edgeTarget := func(from, to *ssa.BasicBlock) llssa.BasicBlock {
		if from == loop.latch && to == loop.header {
			return backedgeCheckBlk
		}
		if preheaderBlk != nil && from == loop.entryPred && to == loop.header {
			return preheaderBlk
		}
		return llvmBlocks[to]
	}

	// Track which CFG edges were actually emitted so Pass 2 wires exact Phis.
	type cfgEdge struct{ from, to *ssa.BasicBlock }
	emittedEdges := make(map[cfgEdge]bool, len(order)*2)
	recordTarget := func(from, to *ssa.BasicBlock) llssa.BasicBlock {
		emittedEdges[cfgEdge{from, to}] = true
		return edgeTarget(from, to)
	}

	e.b.Jump(llvmBlocks[e.fn.Blocks[0]])

	vals := make(map[ssa.Value]llssa.Expr, len(order)*4)
	type pendingPhi struct {
		llvmPhi llssa.Phi
		ssaPhi  *ssa.Phi
	}
	var pendingPhis []pendingPhi
	var remPhi llssa.Phi
	u32 := e.p.prog.Uint32()

	for _, blk := range order {
		e.b.SetBlock(llvmBlocks[blk])
		if blk == loop.header {
			remPhi = e.b.Phi(u32)
		}
		end := len(blk.Instrs) - 1
		for i := 0; i < end; i++ {
			ins := blk.Instrs[i]
			phys := e.plan.instructions[ins]
			switch v := ins.(type) {
			case *ssa.DebugRef, *ssa.RunDefers:
			case *ssa.Phi:
				phi := e.b.Phi(e.p.type_(v.Type(), llssa.InGo))
				vals[v] = phi.Expr
				pendingPhis = append(pendingPhis, pendingPhi{llvmPhi: phi, ssaPhi: v})
			case *ssa.Extract:
				vals[v] = e.b.Extract(e.evalValue(v.Tuple, vals), v.Index)
			case *ssa.Field:
				vals[v] = e.b.Field(e.evalValue(v.X, vals), v.Field)
			case *ssa.FieldAddr:
				x := e.evalValue(v.X, vals)
				if phys.recipe == coroPhysicalInstructionFieldAddr && phys.nilGuard {
					e.emitNilGuard(x, false, v.Pos())
				}
				vals[v] = e.b.FieldAddrKnownNonNil(x, v.Field)
			case *ssa.BinOp:
				if phys.recipe == coroPhysicalInstructionInterfaceNilCompare {
					val := e.evalValue(phys.valueOperand, vals)
					vals[v] = e.b.BinOp(v.Op, e.b.InterfaceTypeWord(val), e.p.prog.Nil(e.p.prog.VoidPtr()))
				} else if cval, ok := foldConstComparison(v); ok {
					vals[v] = e.p.prog.BoolVal(cval)
				} else {
					x := e.evalValueAs(v.X, v.Y.Type(), vals)
					y := e.evalValueAs(v.Y, v.X.Type(), vals)
					switch v.Op {
					case token.QUO, token.REM:
						vals[v] = e.b.BinOpWithNonZeroDivisor(v.Op, x, y)
					case token.SHL, token.SHR:
						vals[v] = e.b.BinOpWithNonNegativeShiftCount(v.Op, x, y)
					default:
						vals[v] = e.b.BinOp(v.Op, x, y)
					}
				}
			case *ssa.UnOp:
				x := e.evalValue(v.X, vals)
				if v.Op == token.MUL {
					if phys.recipe == coroPhysicalInstructionDeref && phys.nilGuard {
						e.emitNilGuard(x, false, v.Pos())
					}
					t := e.p.type_(v.Type(), llssa.InGo)
					if t.RawType() != nil && e.p.prog.SizeOf(t) == 0 {
						vals[v] = e.p.prog.Zero(t)
					} else {
						vals[v] = e.b.LoadKnownNonNil(x)
					}
				} else {
					vals[v] = e.b.UnOp(v.Op, x)
				}
			case *ssa.Convert:
				if isUntypedNilConst(v.X) {
					vals[v] = e.p.nilOf(v.Type())
				} else {
					vals[v] = e.b.Convert(e.p.type_(v.Type(), llssa.InGo), e.evalValue(v.X, vals))
				}
			case *ssa.ChangeType:
				if isUntypedNilConst(v.X) {
					vals[v] = e.p.nilOf(v.Type())
				} else {
					vals[v] = e.b.ChangeType(e.p.type_(v.Type(), llssa.InGo), e.evalValue(v.X, vals))
				}
			case *ssa.MakeInterface:
				t := e.p.type_(v.Type(), llssa.InGo)
				if isUntypedNilConst(v.X) {
					vals[v] = e.p.prog.Nil(t)
				} else {
					vals[v] = e.b.MakeInterface(t, e.evalValue(v.X, vals))
				}
			case *ssa.Call:
				if !isPureLenCapBuiltin(v) {
					panic(fmt.Sprintf("sync-ramp bounded-loop unexpected call %s", v.String()))
				}
				vals[v] = e.b.BuiltinCall(v.Common().Value.(*ssa.Builtin).Name(), e.evalValue(v.Call.Args[0], vals))
			case *ssa.IndexAddr:
				base := e.evalValue(v.X, vals)
				idx := e.evalValue(v.Index, vals)
				if phys.nilGuard {
					e.emitNilGuard(base, false, v.Pos())
				}
				normalized := idx
				if phys.boundsGuard {
					var limit llssa.Expr
					switch phys.container {
					case coroPhysicalContainerSlice:
						limit = e.b.SliceLen(base)
					case coroPhysicalContainerArrayPointer:
						limit = e.p.prog.IntVal(uint64(phys.bound), e.p.prog.Int())
					default:
						panic("sync-ramp bounded-loop unsupported IndexAddr container")
					}
					var outOfRange llssa.Expr
					normalized, outOfRange = e.b.IndexBounds(idx, limit)
					if !outOfRange.IsNil() {
						failBlk, contBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
						e.b.IfWithBranchWeights(outOfRange, failBlk, contBlk, 1, 1000)
						e.b.SetBlockEx(failBlk, llssa.AtEnd, false)
						e.emitBailStart()
						e.b.SetBlockContinuation(contBlk)
					}
				}
				vals[v] = e.b.IndexAddrUnchecked(base, normalized)
			case *ssa.Index:
				base := e.evalValue(v.X, vals)
				idx := e.evalValue(v.Index, vals)
				if phys.nilGuard {
					e.emitNilGuard(base, false, v.Pos())
				}
				normalized := idx
				if phys.boundsGuard {
					var limit llssa.Expr
					switch phys.container {
					case coroPhysicalContainerString:
						limit = e.b.StringLen(base)
					case coroPhysicalContainerSlice:
						limit = e.b.SliceLen(base)
					case coroPhysicalContainerArrayPointer:
						limit = e.p.prog.IntVal(uint64(phys.bound), e.p.prog.Int())
					default:
						panic("sync-ramp bounded-loop unsupported Index container")
					}
					var outOfRange llssa.Expr
					normalized, outOfRange = e.b.IndexBounds(idx, limit)
					if !outOfRange.IsNil() {
						failBlk, contBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
						e.b.IfWithBranchWeights(outOfRange, failBlk, contBlk, 1, 1000)
						e.b.SetBlockEx(failBlk, llssa.AtEnd, false)
						e.emitBailStart()
						e.b.SetBlockContinuation(contBlk)
					}
				}
				switch phys.container {
				case coroPhysicalContainerString:
					vals[v] = e.b.IndexUnchecked(base, normalized, nil)
				case coroPhysicalContainerSlice, coroPhysicalContainerArrayPointer:
					vals[v] = e.b.LoadKnownNonNil(e.b.IndexAddrUnchecked(base, normalized))
				default:
					panic("sync-ramp bounded-loop unsupported Index container")
				}
			default:
				panic(fmt.Sprintf("sync-ramp bounded-loop unsupported instruction %T", ins))
			}
		}

		switch term := blk.Instrs[end].(type) {
		case *ssa.Return:
			e.emitFastReturn(term, vals)
		case *ssa.Jump:
			e.b.Jump(recordTarget(blk, blk.Succs[0]))
		case *ssa.If:
			if ir != nil {
				if kv, ok := coroSyncStaticBranch(term, ir, nil); ok {
					succ := blk.Succs[1]
					if kv {
						succ = blk.Succs[0]
					}
					e.b.Jump(recordTarget(blk, succ))
					continue
				}
			}
			cond := e.evalValue(term.Cond, vals)
			e.b.If(cond, recordTarget(blk, blk.Succs[0]), recordTarget(blk, blk.Succs[1]))
		default:
			panic(fmt.Sprintf("sync-ramp bounded-loop unexpected terminator %T", term))
		}
	}

	if preheaderBlk != nil {
		e.b.SetBlock(preheaderBlk)
		var boundExpr llssa.Expr
		switch loop.entryGuardKind {
		case coroSyncLoopEntryGuardValue:
			boundExpr = e.evalValue(loop.entryGuardValue, vals)
		case coroSyncLoopEntryGuardLen:
			boundExpr = e.b.BuiltinCall("len", e.evalValue(loop.entryGuardValue, vals))
		case coroSyncLoopEntryGuardCap:
			boundExpr = e.b.BuiltinCall("cap", e.evalValue(loop.entryGuardValue, vals))
		}
		limitExpr := e.p.prog.IntVal(loop.entryGuardLimit, boundExpr.Type)
		tooLong := e.b.BinOp(token.GTR, boundExpr, limitExpr)
		e.b.IfWithBranchWeights(tooLong, e.bailStart, llvmBlocks[loop.header], 1, 1000)
	}

	// Emit single-backedge trip-budget guard: bail cleanly to $coro.slow when
	// the proven work bound would be exceeded.
	e.b.SetBlock(backedgeCheckBlk)
	exhausted := e.b.BinOp(token.EQL, remPhi.Expr, e.p.prog.IntVal(0, u32))
	e.b.IfWithBranchWeights(exhausted, e.bailStart, backedgeContBlk, 1, 1000)

	e.b.SetBlock(backedgeContBlk)
	remNext := e.b.BinOp(token.SUB, remPhi.Expr, e.p.prog.IntVal(1, u32))
	e.b.Jump(llvmBlocks[loop.header])

	// Pass 2: wire remPhi and all source SSA Phi incoming edges.
	entryIncomingBlk := llvmBlocks[loop.entryPred]
	if preheaderBlk != nil {
		entryIncomingBlk = preheaderBlk
	}
	remPhi.AddIncoming(e.b, []llssa.BasicBlock{entryIncomingBlk, backedgeContBlk}, func(i int, _ llssa.BasicBlock) llssa.Expr {
		if i == 0 {
			return e.p.prog.IntVal(uint64(loop.maxBackedges), u32)
		}
		return remNext
	})

	for _, pp := range pendingPhis {
		blk := pp.ssaPhi.Block()
		var inBlks []llssa.BasicBlock
		var inVals []ssa.Value
		for idx, pred := range blk.Preds {
			if !emittedEdges[cfgEdge{pred, blk}] {
				continue
			}
			inBlk := llvmBlocks[pred]
			if blk == loop.header {
				if pred == loop.latch {
					inBlk = backedgeContBlk
				} else if preheaderBlk != nil && pred == loop.entryPred {
					inBlk = preheaderBlk
				}
			}
			inBlks = append(inBlks, inBlk)
			inVals = append(inVals, pp.ssaPhi.Edges[idx])
		}
		pp.llvmPhi.AddIncoming(e.b, inBlks, func(i int, blk llssa.BasicBlock) llssa.Expr {
			e.b.SetBlockEx(blk, llssa.BeforeLast, false)
			return e.evalValueAs(inVals[i], pp.ssaPhi.Type(), vals)
		})
	}
}

func (e *coroSyncFastEmitter) emitNode(node *coroSyncFastNode, parentEnv coroSyncEmitEnv) {
	switch node.exitKind {
	case coroSyncFastExitBailStart:
		e.emitBailStart()
		return
	case coroSyncFastExitResumeSlow:
		e.emitSlowCall(node.resumeTag, llssa.Nil, parentEnv.vals)
		return
	case coroSyncFastExitResumePostCall:
		e.emitResumePostCall()
		return
	}

	env := parentEnv.clone()
	end := node.endInstr
	if end < 0 {
		end = len(node.block.Instrs) - 1
	}
	mutated := node.mutated
	for i := node.startInstr; i < end; i++ {
		ins := node.block.Instrs[i]
		phys := e.plan.instructions[ins]
		switch v := ins.(type) {
		case *ssa.DebugRef, *ssa.RunDefers:
		case *ssa.Phi:
			edge := v.Edges[coroSyncPhiEdgeIndex(v, node.predBlock)]
			env.vals[v] = e.evalValueAs(edge, v.Type(), env.vals)
			env.zero[v] = coroSyncIsZeroIntConst(edge) || env.zero[edge]
			env.nilIfaces[v] = coroSyncIsNilInterfaceValue(edge, env.nilIfaces)
		case *ssa.Extract:
			env.vals[v] = e.b.Extract(e.evalValue(v.Tuple, env.vals), v.Index)
		case *ssa.Field:
			env.vals[v] = e.b.Field(e.evalValue(v.X, env.vals), v.Field)
		case *ssa.FieldAddr:
			x := e.evalValue(v.X, env.vals)
			if phys.recipe == coroPhysicalInstructionFieldAddr && phys.nilGuard {
				e.emitNilGuard(x, mutated, v.Pos())
			}
			env.vals[v] = e.b.FieldAddrKnownNonNil(x, v.Field)
		case *ssa.IndexAddr:
			base := e.evalValue(v.X, env.vals)
			idx := e.evalValue(v.Index, env.vals)
			if phys.nilGuard {
				e.emitNilGuard(base, mutated, v.Pos())
			}
			normalized := idx
			if phys.boundsGuard {
				var limit llssa.Expr
				switch phys.container {
				case coroPhysicalContainerSlice:
					limit = e.b.SliceLen(base)
				case coroPhysicalContainerArrayPointer:
					limit = e.p.prog.IntVal(uint64(phys.bound), e.p.prog.Int())
				default:
					panic("sync-ramp unsupported IndexAddr container")
				}
				var outOfRange llssa.Expr
				normalized, outOfRange = e.b.IndexBounds(idx, limit)
				if !outOfRange.IsNil() {
					failBlk, contBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
					e.b.IfWithBranchWeights(outOfRange, failBlk, contBlk, 1, 1000)
					e.b.SetBlockEx(failBlk, llssa.AtEnd, false)
					if e.inSingleAwaitPost {
						e.emitResumePostCall()
					} else {
						e.emitBailStart()
					}
					e.b.SetBlockContinuation(contBlk)
				}
			}
			env.vals[v] = e.b.IndexAddrUnchecked(base, normalized)
		case *ssa.Index:
			base := e.evalValue(v.X, env.vals)
			idx := e.evalValue(v.Index, env.vals)
			if phys.nilGuard {
				e.emitNilGuard(base, mutated, v.Pos())
			}
			normalized := idx
			if phys.boundsGuard {
				var limit llssa.Expr
				switch phys.container {
				case coroPhysicalContainerString:
					limit = e.b.StringLen(base)
				case coroPhysicalContainerSlice:
					limit = e.b.SliceLen(base)
				case coroPhysicalContainerArrayPointer:
					limit = e.p.prog.IntVal(uint64(phys.bound), e.p.prog.Int())
				default:
					panic("sync-ramp unsupported Index container")
				}
				var outOfRange llssa.Expr
				normalized, outOfRange = e.b.IndexBounds(idx, limit)
				if !outOfRange.IsNil() {
					failBlk, contBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
					e.b.IfWithBranchWeights(outOfRange, failBlk, contBlk, 1, 1000)
					e.b.SetBlockEx(failBlk, llssa.AtEnd, false)
					if e.inSingleAwaitPost {
						e.emitResumePostCall()
					} else {
						e.emitBailStart()
					}
					e.b.SetBlockContinuation(contBlk)
				}
			}
			switch phys.container {
			case coroPhysicalContainerString:
				env.vals[v] = e.b.IndexUnchecked(base, normalized, nil)
			case coroPhysicalContainerSlice, coroPhysicalContainerArrayPointer:
				env.vals[v] = e.b.LoadKnownNonNil(e.b.IndexAddrUnchecked(base, normalized))
			default:
				panic("sync-ramp unsupported Index container")
			}
		case *ssa.BinOp:
			if phys.recipe == coroPhysicalInstructionInterfaceNilCompare {
				val := e.evalValue(phys.valueOperand, env.vals)
				env.vals[v] = e.b.BinOp(v.Op, e.b.InterfaceTypeWord(val), e.p.prog.Nil(e.p.prog.VoidPtr()))
			} else if (v.Op == token.EQL || v.Op == token.NEQ) &&
				coroSyncIsConcreteInterfaceType(v.X.Type()) && coroSyncIsConcreteInterfaceType(v.Y.Type()) {
				xNil := coroSyncIsNilInterfaceValue(v.X, env.nilIfaces)
				yNil := coroSyncIsNilInterfaceValue(v.Y, env.nilIfaces)
				switch {
				case xNil && yNil:
					env.vals[v] = e.p.prog.BoolVal(v.Op == token.EQL)
				case xNil:
					val := e.evalValue(v.Y, env.vals)
					env.vals[v] = e.b.BinOp(v.Op, e.b.InterfaceTypeWord(val), e.p.prog.Nil(e.p.prog.VoidPtr()))
				case yNil:
					val := e.evalValue(v.X, env.vals)
					env.vals[v] = e.b.BinOp(v.Op, e.b.InterfaceTypeWord(val), e.p.prog.Nil(e.p.prog.VoidPtr()))
				default:
					panic("sync-ramp dynamic interface comparison requires nil operand")
				}
			} else if cval, ok := foldConstComparison(v); ok {
				env.vals[v] = e.p.prog.BoolVal(cval)
			} else {
				x := e.evalValueAs(v.X, v.Y.Type(), env.vals)
				y := e.evalValueAs(v.Y, v.X.Type(), env.vals)
				switch v.Op {
				case token.QUO, token.REM:
					env.vals[v] = e.b.BinOpWithNonZeroDivisor(v.Op, x, y)
				case token.SHL, token.SHR:
					env.vals[v] = e.b.BinOpWithNonNegativeShiftCount(v.Op, x, y)
				default:
					env.vals[v] = e.b.BinOp(v.Op, x, y)
				}
			}
		case *ssa.UnOp:
			x := e.evalValue(v.X, env.vals)
			if v.Op == token.MUL {
				if phys.recipe == coroPhysicalInstructionDeref && phys.nilGuard {
					e.emitNilGuard(x, mutated, v.Pos())
				}
				t := e.p.type_(v.Type(), llssa.InGo)
				if t.RawType() != nil && e.p.prog.SizeOf(t) == 0 {
					env.vals[v] = e.p.prog.Zero(t)
				} else {
					env.vals[v] = e.b.LoadKnownNonNil(x)
				}
			} else {
				env.vals[v] = e.b.UnOp(v.Op, x)
			}
		case *ssa.Convert:
			if isUntypedNilConst(v.X) {
				env.vals[v] = e.p.nilOf(v.Type())
			} else {
				env.vals[v] = e.b.Convert(e.p.type_(v.Type(), llssa.InGo), e.evalValue(v.X, env.vals))
			}
		case *ssa.ChangeType:
			if isUntypedNilConst(v.X) {
				env.vals[v] = e.p.nilOf(v.Type())
			} else {
				env.vals[v] = e.b.ChangeType(e.p.type_(v.Type(), llssa.InGo), e.evalValue(v.X, env.vals))
			}
		case *ssa.MakeInterface:
			t := e.p.type_(v.Type(), llssa.InGo)
			if isUntypedNilConst(v.X) {
				env.vals[v] = e.p.prog.Nil(t)
				env.nilIfaces[v] = true
			} else {
				env.vals[v] = e.b.MakeInterface(t, e.evalValue(v.X, env.vals))
			}
		case *ssa.TypeAssert:
			x := e.evalValue(v.X, env.vals)
			assertedTyp := e.p.type_(v.AssertedType, llssa.InGo)
			val, ok := e.b.DirectTypeAssert(x, assertedTyp)
			if v.CommaOk {
				tupType := e.p.prog.Struct(assertedTyp, e.p.prog.Bool())
				zeroVal := e.p.prog.Zero(assertedTyp)
				selected := e.b.SelectValue(ok, val, zeroVal)
				env.vals[v] = e.b.Aggregate(tupType, selected, ok)
			} else {
				okBlk, failBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
				e.b.IfWithBranchWeights(ok, okBlk, failBlk, 1000, 1)
				e.b.SetBlockEx(failBlk, llssa.AtEnd, false)
				if e.inSingleAwaitPost {
					e.emitResumePostCall()
				} else {
					e.emitBailStart()
				}
				e.b.SetBlockContinuation(okBlk)
				env.vals[v] = val
			}
		case *ssa.Slice:
			x := e.evalValue(v.X, env.vals)
			isZeroLow := v.Low == nil || coroSyncIsZeroIntConst(v.Low) || env.zero[v.Low]
			if isZeroLow && v.High == nil {
				env.vals[v] = x
			} else {
				var low llssa.Expr
				if v.Low == nil {
					low = e.p.prog.IntVal(0, e.p.prog.Int())
				} else {
					low = e.evalValue(v.Low, env.vals)
				}
				_, isSlice := types.Unalias(v.X.Type()).Underlying().(*types.Slice)
				var high, limit llssa.Expr
				if isSlice {
					if v.High == nil {
						high = e.b.SliceLen(x)
					} else {
						high = e.evalValue(v.High, env.vals)
					}
					limit = e.b.SliceCap(x)
				} else {
					limit = e.b.StringLen(x)
					if v.High == nil {
						high = limit
					} else {
						high = e.evalValue(v.High, env.vals)
					}
				}
				nlow, nhigh, nmax, outOfRange := e.b.SliceBounds(low, high, llssa.Nil, limit)
				if !outOfRange.IsNil() {
					failBlk, contBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
					e.b.IfWithBranchWeights(outOfRange, failBlk, contBlk, 1, 1000)
					e.b.SetBlockEx(failBlk, llssa.AtEnd, false)
					if e.inSingleAwaitPost {
						e.emitResumePostCall()
					} else {
						e.emitBailStart()
					}
					e.b.SetBlockContinuation(contBlk)
				}
				env.vals[v] = e.b.SliceUnchecked(x, nlow, nhigh, nmax)
			}
		case *ssa.Alloc:
			elem := e.p.type_(v.Type().(*types.Pointer).Elem(), llssa.InGo)
			if e.localAllocs == nil {
				e.localAllocs = make(map[*ssa.Alloc]llssa.Expr)
			}
			slot, ok := e.localAllocs[v]
			if !ok {
				slot = e.entryAlloca(elem)
				e.localAllocs[v] = slot
			}
			e.b.StoreKnownNonNil(slot, e.p.prog.Zero(elem))
			env.vals[v] = slot
		case *ssa.Store:
			addr := e.evalValue(v.Addr, env.vals)
			val := e.evalValueAs(v.Val, v.Addr.Type().Underlying().(*types.Pointer).Elem(), env.vals)
			e.b.StoreKnownNonNil(addr, val)
			if alloc, local := v.Addr.(*ssa.Alloc); !local || alloc.Heap {
				mutated = true
			}
		case *ssa.Call:
			env.vals[v] = e.emitCall(v, phys, node, env.vals, &mutated)
		default:
			panic(fmt.Sprintf("sync-ramp unsupported instruction %T", ins))
		}
	}

	if node.singleAwait != nil {
		e.emitSingleAwait(node.singleAwait, node.singleAwaitPost, env)
		return
	}

	switch node.exitKind {
	case coroSyncFastExitReturn:
		e.emitFastReturn(node.retInstr, env.vals)
	case coroSyncFastExitTailCall:
		e.emitTailCall(node.tailCall, node.tailTarget, env.vals)
	case coroSyncFastExitJump:
		e.emitNode(node.nextNode, env)
	case coroSyncFastExitIf:
		cond := e.evalValue(node.cond, env.vals)
		tBlk, fBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
		e.b.If(cond, tBlk, fBlk)
		e.b.SetBlockEx(tBlk, llssa.AtEnd, false)
		e.emitNode(node.trueNode, env)
		e.b.SetBlockEx(fBlk, llssa.AtEnd, false)
		e.emitNode(node.falseNode, env)
	default:
		panic(fmt.Sprintf("sync-ramp unexpected exit kind %d", node.exitKind))
	}
}

func (e *coroSyncFastEmitter) evalCallArgs(call *ssa.Call, vals map[ssa.Value]llssa.Expr) []llssa.Expr {
	sig := coroPhysicalNormalizeSourceSignature(call.Common().Signature())
	if call.Common().IsInvoke() {
		sig = call.Common().Signature()
	}
	args := make([]llssa.Expr, len(call.Call.Args))
	for i, arg := range call.Call.Args {
		if sig != nil && i < sig.Params().Len() {
			args[i] = e.evalValueAs(arg, sig.Params().At(i).Type(), vals)
		} else {
			args[i] = e.evalValue(arg, vals)
		}
	}
	return args
}

func (e *coroSyncFastEmitter) emitCall(
	call *ssa.Call,
	phys coroPhysicalInstructionPlan,
	node *coroSyncFastNode,
	vals map[ssa.Value]llssa.Expr,
	mutated *bool,
) llssa.Expr {
	if isPureLenCapBuiltin(call) {
		return e.b.BuiltinCall(call.Common().Value.(*ssa.Builtin).Name(), e.evalValue(call.Call.Args[0], vals))
	}
	if isPureCopyBuiltin(call) {
		*mutated = true
		return e.emitCopyBuiltin(call, vals)
	}
	if constVal, isConst := node.constBools[call]; isConst {
		return e.p.prog.BoolVal(constVal)
	}
	if node.yieldCalls[call] {
		if !e.callerInline {
			panic("sync-ramp yield call requires caller-inline mode")
		}
		e.p.compileCoroYield(e.b)
		return llssa.Expr{}
	}
	if opcode, isAtomic := node.atomicOpcodes[call]; isAtomic {
		args := e.evalCallArgs(call, vals)
		if !coroSyncIsReadOnlyAtomicOpcode(opcode) {
			*mutated = true
		}
		return e.p.compileAtomicIntrinsic(e.b, opcode, args)
	}
	if spec, isInlineAtomic := node.inlineAtomics[call]; isInlineAtomic {
		args := e.evalCallArgs(call, vals)
		e.emitNilGuard(args[0], *mutated, call.Pos())
		if spec.fieldIndex >= 0 {
			args[0] = e.b.FieldAddrKnownNonNil(args[0], spec.fieldIndex)
		}
		if !coroSyncIsReadOnlyAtomicOpcode(spec.opcode) {
			*mutated = true
		}
		return e.p.compileAtomicIntrinsic(e.b, spec.opcode, args)
	}
	if tryTarget, isTry := node.tryCalls[call]; isTry {
		args := e.evalCallArgs(call, vals)
		tryFn := e.p.coroSyncTryHelperFunc(tryTarget)
		sourceSig, err := e.p.emissionUniverse.coroPhysicalSourceSignature(tryTarget)
		if err != nil {
			panic(err)
		}
		outSlot := e.p.prog.Nil(e.p.prog.VoidPtr())
		if sourceSig.Results().Len() > 0 {
			outSlot = e.entryAlloca(e.p.prog.Type(coro.ResultSlotType(sourceSig.Results()), llssa.InGo))
		}
		callArgs := append([]llssa.Expr{e.task, e.b.Convert(e.p.prog.VoidPtr(), outSlot)}, args...)
		okVal := e.b.Call(tryFn.Expr, callArgs...)
		contBlk, failBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
		e.b.IfWithBranchWeights(okVal, contBlk, failBlk, 1000, 1)
		e.b.SetBlockEx(failBlk, llssa.AtEnd, false)
		e.emitBailStart()
		e.b.SetBlockContinuation(contBlk)
		if entry := e.p.mustFunctionSymbol(tryTarget); entry.plan.ID != "" {
			if targetPlan, err := entry.sealedPhysicalFunctionPlan(); err != nil ||
				targetPlan == nil || targetPlan.syncRamp == nil ||
				targetPlan.syncRamp.kind != coroSyncRampBoundedLoop {
				*mutated = true
			}
		} else {
			*mutated = true
		}
		return e.loadSlotTuple(outSlot, sourceSig.Results())
	}
	if phys.control == coroPhysicalControlDirectOutcome {
		args := e.evalCallArgs(call, vals)
		if e.callerInline {
			return e.p.compileCoroStaticOutcomeTargetCall(e.b, phys.controlTarget, args, true)
		}
		*mutated = true
		return e.emitDirectOutcomeCall(phys.controlTarget, args, call.Pos())
	}
	callee := call.Common().StaticCallee()
	args := e.evalCallArgs(call, vals)
	if !isRuntimeKeepAliveCall(call) {
		*mutated = true
	}
	fnExpr, _, kind := e.p.compileFunction(callee)
	if kind != goFunc || fnExpr == nil {
		panic(fmt.Sprintf("sync-ramp plain callee %q did not resolve to a Go entry", callee.String()))
	}
	return e.b.Call(fnExpr.Expr, args...)
}

func (e *coroSyncFastEmitter) emitCopyBuiltin(call *ssa.Call, vals map[ssa.Value]llssa.Expr) llssa.Expr {
	dst := e.evalValue(call.Call.Args[0], vals)
	src := e.evalValue(call.Call.Args[1], vals)
	if e.p.emissionUniverse != nil && e.p.emissionUniverse.CompleteRuntimeABI() {
		rtPkg := e.p.emissionUniverse.byPath[llssa.PkgRuntime]
		if rtPkg == nil {
			panic("sync-ramp copy builtin requires runtime package")
		}
		sliceCopy := e.p.emissionUniverse.canonicalAlias(rtPkg.ssa.Func("SliceCopy"))
		if sliceCopy == nil {
			panic("sync-ramp copy builtin requires runtime.SliceCopy")
		}
		fn, _, kind := e.p.compileFunction(sliceCopy)
		if fn == nil || kind != goFunc {
			panic("sync-ramp copy builtin runtime.SliceCopy did not resolve to a plain Go entry")
		}
		prog := e.p.prog
		etSize := prog.Val(int(prog.SizeOf(prog.Elem(dst.Type))))
		var srcData, srcLen llssa.Expr
		if _, isSlice := types.Unalias(call.Call.Args[1].Type()).Underlying().(*types.Slice); isSlice {
			srcData = e.b.SliceData(src)
			srcLen = e.b.SliceLen(src)
		} else {
			srcData = e.b.StringData(src)
			srcLen = e.b.StringLen(src)
		}
		return e.b.Call(fn.Expr, dst, srcData, srcLen, etSize)
	}
	return e.b.BuiltinCall("copy", dst, src)
}

func (e *coroSyncFastEmitter) emitDirectOutcomeCall(callee *ssa.Function, args []llssa.Expr, pos token.Pos) llssa.Expr {
	sourceSig, err := e.p.emissionUniverse.coroPhysicalSourceSignature(callee)
	if err != nil {
		panic(err)
	}
	abi := newOutcomePlainPhysicalABI(sourceSig)
	calleeFn, _, kind := e.p.compileOutcomePlainFunction(callee)
	if kind != goFunc || calleeFn == nil {
		panic(fmt.Sprintf("sync-ramp outcome callee %q did not resolve to a Go entry", callee.String()))
	}
	resSlot := e.p.prog.Nil(e.p.prog.VoidPtr())
	if sourceSig.Results().Len() > 0 {
		resSlot = e.entryAlloca(e.p.prog.Type(abi.resultSlotType, llssa.InGo))
	}
	comp := e.completionSlot()
	callArgs := append([]llssa.Expr{e.task, e.b.Convert(e.p.prog.VoidPtr(), resSlot), e.b.Convert(e.p.prog.VoidPtr(), comp)}, args...)
	e.b.Call(calleeFn.Expr, callArgs...)

	retBlk := e.b.Func.MakeBlock()
	e.checkOutcomeCompletionTo(comp, pos, retBlk)
	e.b.SetBlockContinuation(retBlk)
	return e.loadSlotTuple(resSlot, sourceSig.Results())
}

func (e *coroSyncFastEmitter) emitSingleAwait(call *ssa.Call, postNode *coroSyncFastNode, env coroSyncEmitEnv) {
	phys := e.ramp.singleAwaitPlan
	callResults := e.ramp.singleAwaitResults
	nullPtr := e.p.prog.Nil(e.p.prog.VoidPtr())
	syncCompleted := e.b.Func.MakeBlock()

	branchChild := func(child llssa.Expr) {
		asyncBlk := e.b.Func.MakeBlock()
		e.b.IfWithBranchWeights(e.b.BinOp(token.NEQ, child, nullPtr), asyncBlk, syncCompleted, 1, 1000)
		e.b.SetBlockEx(asyncBlk, llssa.AtEnd, false)
		e.emitSlowCall(1, child, env.vals)
	}

	outSlotArg := e.resultSlot
	if callResults == nil || callResults.Len() == 0 {
		if phys.control == coroPhysicalControlDirectAwait || phys.control == coroPhysicalControlExactInterfaceAwait {
			// Pass a non-nil sentinel so a void sync-fast ramp knows it is
			// awaited (not spawned) without allocating a result slot.
			outSlotArg = e.task
		} else {
			outSlotArg = nullPtr
		}
	}

	if e.ramp.singleAwaitBoxed {
		args := e.evalCallArgs(call, env.vals)
		if phys.control == coroPhysicalControlExactInterfaceAwait {
			args = append([]llssa.Expr{e.evalValue(phys.controlReceiver, env.vals)}, args...)
		}
		callSlotType := e.p.prog.Type(coro.ResultSlotType(callResults), llssa.InGo)
		tmpSlot := e.entryAlloca(callSlotType)
		e.singleAwaitTmpSlot = tmpSlot
		boxFn := e.p.coroSyncBoxHelperFunc(phys.controlTarget)
		pair := e.b.Call(boxFn.Expr, append([]llssa.Expr{e.task, e.b.Convert(e.p.prog.VoidPtr(), tmpSlot)}, args...)...)
		child := e.b.Extract(pair, 0)
		asyncOut := e.b.Extract(pair, 1)
		asyncBlk := e.b.Func.MakeBlock()
		e.b.IfWithBranchWeights(e.b.BinOp(token.NEQ, child, nullPtr), asyncBlk, syncCompleted, 1, 1000)
		e.b.SetBlockEx(asyncBlk, llssa.AtEnd, false)
		e.emitSlowCallWithSlot(1, child, asyncOut, env.vals)

		e.b.SetBlockEx(syncCompleted, llssa.AtEnd, false)
		e.singleAwaitPreEnv = cloneMap(env.vals)
		e.inSingleAwaitPost = true
		env.vals[call] = e.loadSlotTuple(tmpSlot, callResults)
		e.emitNode(postNode, env)
		return
	}

	switch phys.control {
	case coroPhysicalControlDirectAwait, coroPhysicalControlExactInterfaceAwait:
		args := e.evalCallArgs(call, env.vals)
		if phys.control == coroPhysicalControlExactInterfaceAwait {
			args = append([]llssa.Expr{e.evalValue(phys.controlReceiver, env.vals)}, args...)
		}
		calleeFn, _, kind := e.p.compileFunctionEntry(e.p.mustFunctionSymbol(phys.controlTarget))
		if kind != goFunc || calleeFn == nil {
			panic("sync-ramp single-await target did not resolve to a Go entry")
		}
		branchChild(e.b.Call(calleeFn.Expr, append([]llssa.Expr{e.task, outSlotArg}, args...)...))

	case coroPhysicalControlManagedInterfaceAwait, coroPhysicalControlDispatchAwait:
		var fnVal llssa.Expr
		var sig *types.Signature
		args := e.evalCallArgs(call, env.vals)
		if phys.control == coroPhysicalControlManagedInterfaceAwait {
			intf := e.evalValue(call.Common().Value, env.vals)
			e.emitNilGuard(e.b.InterfaceTypeWord(intf), false, call.Pos())
			if phys.rawInterfaceReceiver {
				fnVal = e.b.ImethodRawDataKnownNonNil(intf, call.Common().Method)
			} else {
				fnVal = e.b.Imethod(intf, call.Common().Method)
			}
			sig = phys.controlSignature
		} else {
			fnVal = e.evalValue(call.Common().Value, env.vals)
			e.emitNilGuard(e.b.Field(fnVal, 0), false, call.Pos())
			sig = call.Common().Signature()
		}
		dabi, err := newCoroPlainDispatchABI(e.p, sig)
		if err != nil {
			panic(err)
		}
		opts := llssa.CoroDispatchCallOptions{
			Version:           coroPlainDispatchVersion,
			ABIHash:           dabi.hash,
			Result:            e.p.prog.Type(dabi.resultSlotType, llssa.InC),
			DescriptorNonNil:  true,
			TrustedDescriptor: phys.dispatchEntry != coro.ManagedEntryNone,
		}
		if phys.control == coroPhysicalControlManagedInterfaceAwait {
			if callPlan, ok := e.p.immutablePlan().CallPlan(call); ok && !callPlan.Open {
				opts.TrustedDescriptor = true
			}
		}
		switch phys.dispatchEntry {
		case coro.ManagedEntryCoroutine:
			sel := e.b.PrepareCoroDispatchStructuredOnly(fnVal, opts, false)
			branchChild(e.b.CallPreparedCoroDispatchCoroOnly(sel, e.task, outSlotArg, args))
		case coro.ManagedEntryOutcomePlain:
			sel := e.b.PrepareCoroDispatchStructuredOnly(fnVal, opts, false)
			comp := e.completionSlot()
			e.b.CallPreparedCoroDispatchOutcomeOnly(sel, e.task, outSlotArg, e.b.Convert(e.p.prog.VoidPtr(), comp), args)
			e.checkOutcomeCompletionTo(comp, call.Pos(), syncCompleted)
		default:
			sel := e.b.PrepareCoroDispatchCall(fnVal, opts)
			coroBlk, nonCoroBlk, outcomeBlk, plainBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock(), e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
			e.b.If(sel.HasCoro(), coroBlk, nonCoroBlk)
			e.b.SetBlockEx(nonCoroBlk, llssa.AtEnd, false)
			e.b.If(sel.HasOutcome(), outcomeBlk, plainBlk)

			e.b.SetBlockEx(coroBlk, llssa.AtEnd, false)
			branchChild(e.b.CallPreparedCoroDispatchCoro(sel, e.task, outSlotArg, args))

			e.b.SetBlockEx(outcomeBlk, llssa.AtEnd, false)
			comp := e.completionSlot()
			e.b.CallPreparedCoroDispatchOutcome(sel, e.task, outSlotArg, e.b.Convert(e.p.prog.VoidPtr(), comp), args)
			e.checkOutcomeCompletionTo(comp, call.Pos(), syncCompleted)

			e.b.SetBlockEx(plainBlk, llssa.AtEnd, false)
			plainRes := e.b.CallPreparedCoroDispatchPlain(sel, args)
			if callResults != nil && callResults.Len() > 0 {
				typedOut := e.b.Convert(e.p.prog.Pointer(e.p.prog.Type(e.abi.resultSlotType, llssa.InGo)), outSlotArg)
				e.p.storeCoroDynamicDispatchResult(e.b, typedOut, plainRes, dabi.signature.Results())
			}
			e.b.Jump(syncCompleted)
		}
	default:
		panic(fmt.Sprintf("sync-ramp unsupported single-await control %s", phys.control))
	}

	e.b.SetBlockEx(syncCompleted, llssa.AtEnd, false)
	// Snapshot pre-call env before post-call blocks can rebind loop Phis so
	// ResumePostCall (mode==2) restores the exact pre-call live values.
	e.singleAwaitPreEnv = cloneMap(env.vals)
	e.inSingleAwaitPost = true
	if callResults != nil && callResults.Len() > 0 {
		typedOut := e.b.Convert(e.p.prog.Pointer(e.p.prog.Type(e.abi.resultSlotType, llssa.InGo)), outSlotArg)
		env.vals[call] = e.loadSlotTuple(typedOut, callResults)
	}
	e.emitNode(postNode, env)
}

func coroSyncIsZeroIntConst(v ssa.Value) bool {
	c, ok := v.(*ssa.Const)
	if !ok || c.Value == nil || c.Value.Kind() != constant.Int {
		return false
	}
	n, exact := constant.Int64Val(c.Value)
	return exact && n == 0
}

func (e *coroSyncFastEmitter) checkOutcomeCompletionTo(comp llssa.Expr, pos token.Pos, okBlk llssa.BasicBlock) {
	status := e.b.LoadKnownNonNil(e.b.FieldAddrKnownNonNil(comp, outcomePlainCompletionStatus))
	isRet := e.b.BinOp(token.EQL, status, e.p.prog.IntVal(coroAwaitCompletionReturn, e.p.prog.Uint32()))
	errBlk := e.b.Func.MakeBlock()
	e.b.IfWithBranchWeights(isRet, okBlk, errBlk, 1000, 1)
	e.b.SetBlockEx(errBlk, llssa.AtEnd, false)
	tw := e.b.LoadKnownNonNil(e.b.FieldAddrKnownNonNil(comp, outcomePlainCompletionTypeWord))
	dw := e.b.LoadKnownNonNil(e.b.FieldAddrKnownNonNil(comp, outcomePlainCompletionDataWord))
	e.emitOutcomePropagateCall(status, tw, dw, pos)
}

func (e *coroSyncFastEmitter) loadSlotTuple(slot llssa.Expr, results *types.Tuple) llssa.Expr {
	if results == nil || results.Len() == 0 {
		return llssa.Expr{}
	}
	if results.Len() == 1 {
		return e.b.LoadKnownNonNil(e.b.FieldAddr(slot, 0))
	}
	fields := make([]llssa.Expr, results.Len())
	for i := range fields {
		fields[i] = e.b.LoadKnownNonNil(e.b.FieldAddr(slot, i))
	}
	return e.b.Aggregate(e.p.prog.Type(results, llssa.InGo), fields...)
}

func (e *coroSyncFastEmitter) emitFastReturn(ret *ssa.Return, vals map[ssa.Value]llssa.Expr) {
	if e.callerInline {
		e.b.Jump(e.callerDoneBlk)
		return
	}
	resTuple := coroPhysicalNormalizeSourceSignature(e.fn.Signature).Results()
	if resTuple.Len() > 0 {
		typedOut := e.b.Convert(e.p.prog.Pointer(e.p.prog.Type(e.abi.resultSlotType, llssa.InGo)), e.resultSlot)
		for i, resVal := range ret.Results {
			e.b.StoreKnownNonNil(e.b.FieldAddrKnownNonNil(typedOut, i), e.evalValueAs(resVal, resTuple.At(i).Type(), vals))
		}
	}
	if e.tryMode {
		e.b.Return(e.p.prog.BoolVal(true))
		return
	}
	if e.boxMode {
		nullPtr := e.p.prog.Nil(e.p.prog.VoidPtr())
		e.returnBoxPair(nullPtr, nullPtr)
		return
	}
	e.b.Return(e.p.prog.Nil(e.p.prog.VoidPtr()))
}

func (e *coroSyncFastEmitter) emitTailCall(call *ssa.Call, target *ssa.Function, vals map[ssa.Value]llssa.Expr) {
	args := e.evalCallArgs(call, vals)
	if e.callerInline {
		e.p.compileCoroTargetAwait(e.b, target, args)
		e.b.Jump(e.callerDoneBlk)
		return
	}
	entry := e.p.mustFunctionSymbol(target)
	if e.boxMode {
		if targetPlan, err := entry.sealedPhysicalFunctionPlan(); err == nil &&
			targetPlan != nil && targetPlan.syncRamp != nil && targetPlan.syncRamp.hasBoxHelper {
			boxFn := e.p.coroSyncBoxHelperFunc(target)
			e.b.Return(e.b.Call(boxFn.Expr, append([]llssa.Expr{e.task, e.resultSlot}, args...)...))
			return
		}
		calleeFn, _, kind := e.p.compileFunctionEntry(entry)
		if kind != goFunc || calleeFn == nil {
			panic("sync-ramp tail-call target did not resolve to a Go entry")
		}
		asyncBox := e.allocAsyncBox()
		child := e.b.Call(calleeFn.Expr, append([]llssa.Expr{e.task, asyncBox}, args...)...)
		nullPtr := e.p.prog.Nil(e.p.prog.VoidPtr())
		syncBlk, asyncBlk := e.b.Func.MakeBlock(), e.b.Func.MakeBlock()
		e.b.IfWithBranchWeights(e.b.BinOp(token.EQL, child, nullPtr), syncBlk, asyncBlk, 1, 1000)
		e.b.SetBlockEx(syncBlk, llssa.AtEnd, false)
		slotPtrType := e.p.prog.Pointer(e.p.prog.Type(e.abi.resultSlotType, llssa.InGo))
		e.b.StoreKnownNonNil(e.b.Convert(slotPtrType, e.resultSlot), e.b.LoadKnownNonNil(e.b.Convert(slotPtrType, asyncBox)))
		e.returnBoxPair(nullPtr, nullPtr)
		e.b.SetBlockEx(asyncBlk, llssa.AtEnd, false)
		e.returnBoxPair(child, asyncBox)
		return
	}
	calleeFn, _, kind := e.p.compileFunctionEntry(entry)
	if kind != goFunc || calleeFn == nil {
		panic("sync-ramp tail-call target did not resolve to a Go entry")
	}
	e.b.Return(e.b.Call(calleeFn.Expr, append([]llssa.Expr{e.task, e.resultSlot}, args...)...))
}

// coroOutcomePropagateCoroutine returns the module-shared linkonce coroutine
// used by sync-fast ramps to propagate an unexpected non-Return $outcome
// completion (Panic, FaultNil, or Goexit) without allocating frames on the
// fast path.
func (p *context) coroOutcomePropagateCoroutine() llssa.Function {
	ptr, u32 := types.Typ[types.UnsafePointer], types.Typ[types.Uint32]
	sourceSig := types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "desc", ptr),
			types.NewParam(token.NoPos, nil, "line", u32),
			types.NewParam(token.NoPos, nil, "status", u32),
			types.NewParam(token.NoPos, nil, "typeWord", ptr),
			types.NewParam(token.NoPos, nil, "dataWord", ptr),
		), nil, false)
	fn := p.pkg.NewFuncEx(coroOutcomePropagateHelperNameV1, coroPhysicalEntrySignature(sourceSig), llssa.InGo, false, true)
	if fn.HasBody() {
		return fn
	}
	var propComp *Compilation
	if p.compilation != nil {
		propComp = &Compilation{
			CoroABI: p.compilation.CoroABI, SchedulerABI: p.compilation.SchedulerABI,
			PanicABI: p.compilation.PanicABI, FuncRepABI: p.compilation.FuncRepABI,
		}
	}
	rampCtx := context{prog: p.prog, pkg: p.pkg, fn: fn, compilation: propComp}
	abi := newCoroPhysicalABI(&rampCtx, plannedFunctionSymbol{
		name: coroOutcomePropagateHelperNameV1,
		plan: coro.FunctionPlan{ID: coro.FunctionID(coroOutcomePropagateHelperNameV1)},
	}, sourceSig)
	b := fn.MakeBody(1)
	body := rampCtx.beginCoroBody(b, abi, nil)
	body.completion, body.finalSuspend = fn.MakeBlock(), fn.MakeBlock()
	body.bindCancellationCompletion(b)

	b.SetBlock(body.coro.InitialResumeBlock())
	body.activate(b)

	desc, line := fn.PhysicalParam(2), fn.PhysicalParam(3)
	status, typeWord, dataWord := fn.PhysicalParam(4), fn.PhysicalParam(5), fn.PhysicalParam(6)
	traceAppend := p.pkg.NewFunc(abi.panicTraceAppendHook, coroPanicTraceAppendSignature(), llssa.InC).Expr
	faultPrepare := p.pkg.NewFunc(coroFaultPrepareHookV1, coroFaultPrepareSignature(), llssa.InC).Expr

	panicked, faulted, localFaulted, goexited, invalid := fn.MakeBlock(), fn.MakeBlock(), fn.MakeBlock(), fn.MakeBlock(), fn.MakeBlock()
	sw := b.Switch(status, invalid)
	sw.Case(p.prog.IntVal(coroAwaitCompletionPanic, p.prog.Uint32()), panicked)
	sw.Case(p.prog.IntVal(coroAwaitCompletionFaultNil, p.prog.Uint32()), faulted)
	sw.Case(p.prog.IntVal(coroOutcomePropagateLocalFaultNil, p.prog.Uint32()), localFaulted)
	sw.Case(p.prog.IntVal(coroAwaitCompletionGoexit, p.prog.Uint32()), goexited)
	sw.End(b)

	b.SetBlockEx(panicked, llssa.AtEnd, false)
	b.CoroAtomicLogicalPanicTraceAppend(traceAppend, body.task, desc, typeWord, dataWord, line,
		p.prog.IntVal(coroLogicalPanicTracePropagate, p.prog.Uint32()))
	body.panicWithLine(b, typeWord, dataWord, line)

	b.SetBlockEx(faulted, llssa.AtEnd, false)
	nullPtr := p.prog.Nil(p.prog.VoidPtr())
	b.CoroAtomicLogicalPanicTraceAppend(traceAppend, body.task, desc, nullPtr, nullPtr, line,
		p.prog.IntVal(coroLogicalPanicTraceNilPropagate, p.prog.Uint32()))
	body.publishStateValue(b, coroSuspendPanic, coroLifecycleFinalSuspended, line)
	b.Call(faultPrepare, body.task, body.coro.Handle(), b.Convert(p.prog.VoidPtr(), body.header),
		p.prog.IntVal(uint64(coroFaultNilV1), p.prog.Uint32()))
	b.Jump(body.finalSuspend)

	b.SetBlockEx(localFaulted, llssa.AtEnd, false)
	b.CoroAtomicLogicalPanicTraceAppend(traceAppend, body.task, desc, nullPtr, nullPtr, line,
		p.prog.IntVal(coroLogicalPanicTraceNilNew, p.prog.Uint32()))
	body.publishStateValue(b, coroSuspendPanic, coroLifecycleFinalSuspended, line)
	b.Call(faultPrepare, body.task, body.coro.Handle(), b.Convert(p.prog.VoidPtr(), body.header),
		p.prog.IntVal(uint64(coroFaultNilV1), p.prog.Uint32()))
	b.Jump(body.finalSuspend)

	b.SetBlockEx(goexited, llssa.AtEnd, false)
	body.enterGoexit(b)
	b.SetBlockEx(invalid, llssa.AtEnd, false)
	b.Unreachable()

	b.SetBlock(body.completion)
	body.complete(b)
	b.SetBlock(body.finalSuspend)
	body.finish(b)
	b.EndBuild()
	b.Dispose()
	return fn
}

// -----------------------------------------------------------------------------
// $coro.slow Resume Wiring Helpers (called from coro_abi.go / compile.go)
// -----------------------------------------------------------------------------

func (p *context) initCoroSyncSlowLiveSlots(state *coroSyncSlowResumeState) {
	if state == nil || len(state.plan.liveValues) == 0 {
		return
	}
	state.liveSlots = make(map[ssa.Value]llssa.Expr, len(state.plan.liveValues))
	for _, v := range state.plan.liveValues {
		state.liveSlots[v] = p.coroFrameAlloca(p.type_(v.Type(), llssa.InGo))
	}
}

func (p *context) emitCoroSyncSlowEntryDispatch(
	b llssa.Builder,
	state *coroSyncSlowResumeState,
	sourceBlocks []llssa.BasicBlock,
) {
	if state == nil {
		b.Jump(sourceBlocks[0])
		return
	}
	storeLiveParams := func(tb llssa.Builder) {
		for i, v := range state.plan.liveValues {
			tb.StoreKnownNonNil(state.liveSlots[v], state.liveParams[i])
		}
	}
	u8 := p.type_(types.Typ[types.Uint8], llssa.InGo)
	invalid := p.fn.MakeBlock()
	sw := b.Switch(state.resumeTagParam, invalid)
	sw.Case(p.prog.IntVal(0, u8), sourceBlocks[0])
	if state.plan.kind == coroSyncRampSingleAwait {
		state.resumeAwaitBlk, state.resumePostBlk = p.fn.MakeBlock(), p.fn.MakeBlock()
		awaitBridge, postBridge := p.fn.MakeBlock(), p.fn.MakeBlock()
		sw.Case(p.prog.IntVal(1, u8), awaitBridge)
		sw.Case(p.prog.IntVal(2, u8), postBridge)
		sw.End(b)

		b.SetBlockEx(awaitBridge, llssa.AtEnd, false)
		storeLiveParams(b)
		b.Jump(state.resumeAwaitBlk)

		b.SetBlockEx(postBridge, llssa.AtEnd, false)
		storeLiveParams(b)
		b.Jump(state.resumePostBlk)
	} else {
		bridges := make([]llssa.BasicBlock, len(state.plan.slowTargets))
		for i, target := range state.plan.slowTargets {
			bridges[i] = p.fn.MakeBlock()
			sw.Case(p.prog.IntVal(uint64(target.tag), u8), bridges[i])
		}
		sw.End(b)
		for i, target := range state.plan.slowTargets {
			b.SetBlockEx(bridges[i], llssa.AtEnd, false)
			storeLiveParams(b)
			b.Jump(sourceBlocks[target.block.Index])
		}
	}
	b.SetBlockEx(invalid, llssa.AtEnd, false)
	b.Unreachable()
}

func (p *context) joinCoroSyncSlowSingleAwait(b llssa.Builder, call *ssa.Call, normalResult llssa.Expr) llssa.Expr {
	state := p.coroSyncSlowResume
	if state == nil || state.plan.kind != coroSyncRampSingleAwait || call != state.plan.singleAwaitCall {
		return normalResult
	}
	state.singleAwaitDone = true
	results := state.plan.singleAwaitResults
	joinBlk := p.fn.MakeBlock()
	if results == nil || results.Len() == 0 {
		b.Jump(joinBlk)

		b.SetBlockEx(state.resumeAwaitBlk, llssa.AtEnd, false)
		p.awaitCoroSyncSlowPreChild(b, state.preChildParam, llssa.Nil, results)
		b.Jump(joinBlk)

		b.SetBlockEx(state.resumePostBlk, llssa.AtEnd, false)
		b.Jump(joinBlk)

		b.SetBlockContinuation(joinBlk)
		return normalResult
	}
	slot := p.coroFrameAlloca(p.type_(call.Type(), llssa.InGo))

	b.StoreKnownNonNil(slot, normalResult)
	b.Jump(joinBlk)

	b.SetBlockEx(state.resumeAwaitBlk, llssa.AtEnd, false)
	b.StoreKnownNonNil(slot, p.awaitCoroSyncSlowPreChild(b, state.preChildParam, state.preResultSlotParam, results))
	b.Jump(joinBlk)

	b.SetBlockEx(state.resumePostBlk, llssa.AtEnd, false)
	b.StoreKnownNonNil(slot, p.loadCoroSyncSlowIncomingResult(b, state.preResultSlotParam, results))
	b.Jump(joinBlk)

	b.SetBlockContinuation(joinBlk)
	return b.LoadKnownNonNil(slot)
}
