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
	"go/token"
	"go/types"
	"strconv"

	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroForeignReentryAdapterPrefixV1   = "__llgo_coro_foreign_reentry_adapter_v1_"
	coroForeignReentryPlainRampPrefixV1 = "__llgo_coro_foreign_reentry_plain_ramp_v1_"
	coroForeignReentryAcquireHookV1     = "__llgo_coro_foreign_reentry_acquire_v1"
	coroForeignReentryRunHookV1         = "__llgo_coro_foreign_reentry_run_v1"
	coroForeignReentryFailureHookV1     = "__llgo_coro_foreign_reentry_failure_v1"
	coroReentrantForeignCallHookV1      = "__llgo_coro_reentrant_foreign_call_v1"
)

func (p *context) coroForeignReentryTargetEntry(
	target *ssa.Function,
	entry plannedFunctionSymbol,
	sourceSignature *types.Signature,
	abi coroPhysicalABI,
) llssa.Function {
	if p == nil || p.compilation == nil || target == nil ||
		!entry.planned || entry.function != target {
		panic("coroutine foreign reentry callback target requires a frozen compilation plan")
	}
	if entry.plan.Effect.MaySuspend() {
		oldInCFunc := p.inCFunc
		p.inCFunc = false
		childEntry, _, kind := p.compileFunctionEntry(entry)
		p.inCFunc = oldInCFunc
		if childEntry == nil || kind != goFunc {
			panic("coroutine foreign reentry adapter lost its managed callback entry")
		}
		return childEntry
	}
	return p.coroForeignReentryPlainRamp(target, entry, sourceSignature, abi)
}

// coroForeignReentryPlainRamp gives a proven non-suspending callback the same
// scheduler child transaction without cloning its source body. The target
// remains one ordinary plain primary; only this generated, typed crossing is a
// stackless coroutine. This is the dual-entry exception required by a value
// that is invoked in both ordinary Go and synchronous C callback contexts.
func (p *context) coroForeignReentryPlainRamp(
	target *ssa.Function,
	entry plannedFunctionSymbol,
	sourceSignature *types.Signature,
	abi coroPhysicalABI,
) llssa.Function {
	if p == nil || p.compilation == nil || target == nil ||
		sourceSignature == nil || sourceSignature.Recv() != nil ||
		sourceSignature.Variadic() || abi.resultCount > 1 {
		panic("coroutine foreign reentry plain ramp requires one exact plain target")
	}
	oldInCFunc := p.inCFunc
	p.inCFunc = false
	plainTarget, _, kind := p.compileFunctionEntry(entry)
	p.inCFunc = oldInCFunc
	if plainTarget == nil || kind != goFunc {
		panic("coroutine foreign reentry plain ramp lost its plain target")
	}
	key := framedEmissionKey(
		"cl-coro-foreign-reentry-plain-ramp-v1",
		string(entry.plan.ID),
		entry.name,
		structuralEmissionABITypeKey(sourceSignature),
		strconv.Itoa(p.prog.PointerSize()),
	)
	name := coroForeignReentryPlainRampPrefixV1 + emissionDigest(key)
	ramp := p.pkg.NewFuncEx(name, abi.physicalSig, llssa.InGo, false, true)
	if ramp.HasBody() {
		return ramp
	}

	// The generated ramp is a new physical function, not nested source
	// lowering. Give it only the immutable compiler capabilities needed by the
	// generic coroutine prologue/result helpers; copying the caller context
	// would also copy its active emission session and mutable SSA ledgers.
	rampContext := context{
		prog:        p.prog,
		pkg:         p.pkg,
		fn:          ramp,
		goFn:        target,
		compilation: p.compilation,
	}
	b := ramp.MakeBody(1)
	body := rampContext.beginCoroBody(b, abi, nil)
	body.completion = ramp.MakeBlock()
	body.finalSuspend = ramp.MakeBlock()
	body.bindCancellationCompletion(b)

	b.SetBlock(body.coro.InitialResumeBlock())
	body.activate(b)
	args := make([]llssa.Expr, sourceSignature.Params().Len())
	for index := range args {
		args[index] = ramp.PhysicalParam(index + 2)
	}
	result := b.Call(plainTarget.Expr, args...)
	results := []llssa.Expr(nil)
	if sourceSignature.Results().Len() == 1 {
		results = append(results, result)
	}
	rampContext.storeCoroLeafResult(b, abi, body.resultSlot, results)
	b.Jump(body.completion)

	b.SetBlock(body.completion)
	body.complete(b)
	b.SetBlock(body.finalSuspend)
	body.finish(b)
	b.EndBuild()
	b.Dispose()
	return ramp
}

func coroForeignReentryAcquireSignature() *types.Signature {
	parent := types.NewParam(
		token.NoPos, nil, "parent",
		types.NewPointer(types.Typ[types.UnsafePointer]),
	)
	task := types.NewVar(token.NoPos, nil, "task", types.Typ[types.UnsafePointer])
	return types.NewSignatureType(
		nil, nil, nil,
		types.NewTuple(parent),
		types.NewTuple(task),
		false,
	)
}

func coroForeignReentryRunSignature() *types.Signature {
	params := []*types.Var{
		types.NewParam(token.NoPos, nil, "child", types.Typ[types.UnsafePointer]),
		types.NewParam(
			token.NoPos, nil, "typeOut",
			types.NewPointer(types.Typ[types.UnsafePointer]),
		),
		types.NewParam(
			token.NoPos, nil, "dataOut",
			types.NewPointer(types.Typ[types.UnsafePointer]),
		),
	}
	status := types.NewVar(token.NoPos, nil, "status", types.Typ[types.Uint32])
	return types.NewSignatureType(
		nil, nil, nil,
		types.NewTuple(params...),
		types.NewTuple(status),
		false,
	)
}

func coroForeignReentryFailureSignature() *types.Signature {
	params := []*types.Var{
		types.NewParam(token.NoPos, nil, "status", types.Typ[types.Uint32]),
		types.NewParam(token.NoPos, nil, "typeWord", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "dataWord", types.Typ[types.UnsafePointer]),
	}
	return types.NewSignatureType(
		nil, nil, nil, types.NewTuple(params...), nil, false,
	)
}

func coroReentrantForeignCallSignature() *types.Signature {
	params := []*types.Var{
		types.NewParam(token.NoPos, nil, "task", types.Typ[types.UnsafePointer]),
		types.NewParam(token.NoPos, nil, "thunk", types.Typ[types.Uintptr]),
		types.NewParam(token.NoPos, nil, "record", types.Typ[types.Uintptr]),
	}
	return types.NewSignatureType(
		nil, nil, nil, types.NewTuple(params...), nil, false,
	)
}

// coroForeignReentryAdapter emits one exact raw C ABI entry for one exact
// managed Go callback. It never clones the callback body and never performs
// address-to-function recovery: its physical target and signature are part of
// the frozen call-site shape and therefore of the generated symbol digest.
func (p *context) coroForeignReentryAdapter(
	target *ssa.Function,
	callbackSignature *types.Signature,
) llssa.Function {
	if p == nil || p.emissionUniverse == nil || target == nil ||
		callbackSignature == nil || callbackSignature.Recv() != nil ||
		callbackSignature.Variadic() || len(target.FreeVars) != 0 {
		panic("coroutine foreign reentry adapter requires one exact non-capturing callback")
	}
	entry := p.mustFunctionSymbol(target)
	sourceSignature, err := p.emissionUniverse.coroPhysicalSourceSignature(target)
	if err != nil {
		panic(fmt.Errorf("coroutine foreign reentry callback ABI: %w", err))
	}
	if sourceSignature == nil || !types.Identical(
		coroPhysicalNormalizeSourceSignature(callbackSignature),
		coroPhysicalNormalizeSourceSignature(sourceSignature),
	) {
		panic("coroutine foreign reentry adapter target and callback signatures differ")
	}
	abi := newCoroPhysicalABI(p, entry, sourceSignature)
	childEntry := p.coroForeignReentryTargetEntry(
		target, entry, sourceSignature, abi,
	)
	key := framedEmissionKey(
		"cl-coro-foreign-reentry-adapter-v1",
		string(entry.plan.ID),
		entry.name,
		structuralEmissionABITypeKey(callbackSignature),
		strconv.Itoa(p.prog.PointerSize()),
	)
	name := coroForeignReentryAdapterPrefixV1 + emissionDigest(key)
	adapter := p.pkg.NewFuncEx(name, callbackSignature, llssa.InC, false, true)
	if adapter.HasBody() {
		return adapter
	}

	b := adapter.MakeBody(1)
	parentSlot := b.AllocaT(p.prog.VoidPtr())
	typeSlot := b.AllocaT(p.prog.VoidPtr())
	dataSlot := b.AllocaT(p.prog.VoidPtr())
	b.Store(parentSlot, p.prog.Nil(p.prog.VoidPtr()))
	b.Store(typeSlot, p.prog.Nil(p.prog.VoidPtr()))
	b.Store(dataSlot, p.prog.Nil(p.prog.VoidPtr()))

	acquire := p.pkg.NewFunc(
		coroForeignReentryAcquireHookV1,
		coroForeignReentryAcquireSignature(),
		llssa.InC,
	)
	task := b.Call(acquire.Expr, parentSlot)
	resultSlot := b.AllocaT(p.prog.Type(abi.resultSlotType, llssa.InGo))
	callArgs := make([]llssa.Expr, 0, callbackSignature.Params().Len()+2)
	callArgs = append(
		callArgs,
		task,
		b.Convert(p.prog.VoidPtr(), resultSlot),
	)
	for index := 0; index < callbackSignature.Params().Len(); index++ {
		callArgs = append(callArgs, adapter.Param(index))
	}
	child := b.Call(childEntry.Expr, callArgs...)
	childHeader := b.CoroPromise(child, coroHeaderType(p.prog))
	b.Store(b.FieldAddr(childHeader, coroHeaderParent), b.Load(parentSlot))

	run := p.pkg.NewFunc(
		coroForeignReentryRunHookV1,
		coroForeignReentryRunSignature(),
		llssa.InC,
	)
	status := b.Call(run.Expr, child, typeSlot, dataSlot)
	returned := adapter.MakeBlock()
	failed := adapter.MakeBlock()
	dispatch := b.Switch(status, failed)
	dispatch.Case(
		p.prog.IntVal(coroAwaitCompletionReturn, p.prog.Uint32()),
		returned,
	)
	dispatch.End(b)

	b.SetBlockEx(failed, llssa.AtEnd, false)
	failure := p.pkg.NewFunc(
		coroForeignReentryFailureHookV1,
		coroForeignReentryFailureSignature(),
		llssa.InC,
	)
	b.Call(failure.Expr, status, b.Load(typeSlot), b.Load(dataSlot))
	b.Unreachable()

	b.SetBlockEx(returned, llssa.AtEnd, false)
	results := sourceSignature.Results()
	if results == nil || results.Len() == 0 {
		b.Return()
	} else if results.Len() == 1 {
		b.Return(b.LoadKnownNonNil(b.FieldAddr(resultSlot, 0)))
	} else {
		panic("coroutine foreign reentry adapter cannot return multiple C results")
	}
	b.EndBuild()
	b.Dispose()
	return adapter
}

func (p *context) compileCoroForeignReentryCall(
	b llssa.Builder,
	call *ssa.Call,
	shape coroWorkerForeignCallShape,
) llssa.Expr {
	if p == nil || !p.hasCoroPhysicalBody() || call == nil ||
		shape.mode != coroForeignCallModeManagedReentry ||
		shape.target == nil || shape.calleeType != nil ||
		shape.signature == nil || shape.record == nil ||
		len(shape.reentryCallbacks) == 0 || len(shape.rawCallbacks) != 0 {
		panic("coroutine foreign reentry lowering escaped its frozen physical operation recipe")
	}
	target, _, kind := p.compileFunction(shape.target)
	if target == nil || kind != cFunc {
		panic("coroutine foreign reentry lowering lost its exact C target")
	}
	thunk := p.coroWorkerForeignThunk(shape, target)

	oldInCFunc := p.inCFunc
	p.inCFunc = true
	if len(shape.arguments) != shape.argc {
		panic("coroutine foreign reentry lowering disagrees with its frozen arguments")
	}
	compiled := make([]llssa.Expr, shape.argc)
	for index, argument := range shape.arguments {
		if callback := shape.reentryCallbacks[index]; callback != nil {
			signature, ok := types.Unalias(
				shape.signature.Params().At(index).Type(),
			).Underlying().(*types.Signature)
			if !ok || signature == nil {
				panic("coroutine foreign reentry callback lost its exact C signature")
			}
			compiled[index] = p.coroForeignReentryAdapter(callback, signature).Expr
			continue
		}
		compiled[index] = p.compileValue(b, argument)
	}
	p.inCFunc = oldInCFunc

	record := p.coroFrameAlloc(p.type_(shape.record, llssa.InC))
	for index, argument := range compiled {
		b.Store(b.FieldAddr(record, shape.argumentBase+index), argument)
	}
	keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)
	task := p.coroTask()
	if task.IsNil() {
		panic("coroutine foreign reentry call has no active physical body")
	}
	invoke := p.pkg.NewFunc(
		coroReentrantForeignCallHookV1,
		coroReentrantForeignCallSignature(),
		llssa.InC,
	)
	b.Call(
		invoke.Expr,
		task,
		b.Convert(p.prog.Uintptr(), thunk.Expr),
		b.Convert(p.prog.Uintptr(), record),
	)
	p.emitCoroKeepaliveSlots(b, keepaliveSlots)
	b.KeepAlive(record)
	if shape.result == nil {
		return llssa.Expr{}
	}
	if shape.resultField < 0 {
		panic("coroutine foreign reentry lowering lost its result record field")
	}
	return b.LoadKnownNonNil(b.FieldAddr(record, shape.resultField))
}
