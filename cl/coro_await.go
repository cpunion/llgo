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
	"go/types"

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

const (
	coroAwaitCompletionReturn          uint64 = 1
	coroAwaitCompletionPanic           uint64 = 2
	coroAwaitCompletionAbort           uint64 = 3
	coroAwaitCompletionShutdown        uint64 = 4
	coroAwaitCompletionReturnRecovered uint64 = 5

	coroAwaitRecoverNone   uint64 = 0
	coroAwaitRecoverDirect uint64 = 1
)

// resolveCoroStaticAwait proves the exact subset implemented by the physical
// child-await lowering. The returned function is the canonical target recorded
// by the whole-program plan, not an identity inferred from an SSA display name.
func resolveCoroStaticAwait(plan *coro.SSAPlan, caller coro.FunctionPlan, call ssa.CallInstruction, universe *EmissionUniverse) (*ssa.Function, coro.FunctionPlan, error) {
	if plan == nil || call == nil || call.Common() == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires a compilation CallPlan")
	}
	common := call.Common()
	if common.IsInvoke() {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires a non-invoke call")
	}
	callPlan, ok := plan.CallPlan(call)
	if !ok {
		return nil, coro.FunctionPlan{}, fmt.Errorf("call has no compilation CallPlan")
	}
	if callPlan.Kind != coro.CallDirect || callPlan.Rep != coro.DirectCoro || callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"requires one closed non-nil direct coroutine target, got kind=%v representation=%s open=%t may-be-nil=%t targets=%d",
			callPlan.Kind, callPlan.Rep, callPlan.Open, callPlan.MayBeNil, len(callPlan.Targets),
		)
	}
	target, ok := plan.Function(callPlan.Targets[0])
	if !ok || target == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("direct coroutine target %q is absent from the compilation plan", callPlan.Targets[0])
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.ID != callPlan.Targets[0] {
		return nil, coro.FunctionPlan{}, fmt.Errorf("direct coroutine target %q has no canonical function plan", callPlan.Targets[0])
	}
	if err := validateCoroAwaitTarget(caller, targetPlan); err != nil {
		return nil, coro.FunctionPlan{}, err
	}
	if target.Signature != nil && target.Signature.Recv() != nil {
		if err := validateCoroStaticMethodCallOperands(call, target, universe); err != nil {
			return nil, coro.FunctionPlan{}, err
		}
	} else if len(target.FreeVars) != 0 {
		closure, exact := common.Value.(*ssa.MakeClosure)
		closureTarget, targetExact := func() (*ssa.Function, bool) {
			if !exact || closure == nil {
				return nil, false
			}
			fn, ok := closure.Fn.(*ssa.Function)
			return fn, ok
		}()
		if !targetExact || closureTarget != target || len(closure.Bindings) != len(target.FreeVars) {
			return nil, coro.FunctionPlan{}, fmt.Errorf("closed captured coroutine target requires its exact MakeClosure environment")
		}
	} else if common.StaticCallee() == nil {
		if common.Method != nil || target.Signature == nil || target.Signature.Variadic() || len(common.Args) != target.Signature.Params().Len() {
			return nil, coro.FunctionPlan{}, fmt.Errorf("closed direct coroutine target has an incompatible dynamic call shape")
		}
		for index, argument := range common.Args {
			if argument == nil || !types.Identical(argument.Type(), target.Signature.Params().At(index).Type()) {
				return nil, coro.FunctionPlan{}, fmt.Errorf("closed direct coroutine operand %d does not match the target parameter ABI", index)
			}
		}
	}
	return target, targetPlan, nil
}

// validateCoroStaticMethodCallOperands freezes the x/tools receiver convention
// at the exact call boundary. A declared receiver is target.Params[0] and the
// same SSA value is common.Args[0]; bound method values, closures, invokes, and
// synthetic receiver adapters do not satisfy this shape.
func validateCoroStaticMethodCallOperands(call ssa.CallInstruction, target *ssa.Function, universe *EmissionUniverse) error {
	if call == nil || call.Common() == nil || target == nil || target.Signature == nil || target.Signature.Recv() == nil {
		return fmt.Errorf("static coroutine method requires an exact declared method target")
	}
	common := call.Common()
	raw, exactValue := common.Value.(*ssa.Function)
	if common.IsInvoke() || common.StaticCallee() == nil || !exactValue || raw != common.StaticCallee() {
		return fmt.Errorf("static coroutine method requires an exact function operand, not an invoke or method value")
	}
	rawNormalized := coroPhysicalNormalizeSourceSignature(raw.Signature)
	if raw.Signature == nil || raw.Signature.Recv() == nil || rawNormalized.Params().Len() != len(raw.Params) || len(common.Args) != len(raw.Params) {
		return fmt.Errorf("static coroutine method source operand has no exact receiver-first SSA shape")
	}
	for index, parameter := range raw.Params {
		if parameter == nil || common.Args[index] == nil ||
			!types.Identical(parameter.Type(), rawNormalized.Params().At(index).Type()) ||
			!types.Identical(common.Args[index].Type(), parameter.Type()) {
			return fmt.Errorf("static coroutine method source operand %d does not match its exact SSA parameter", index)
		}
	}
	normalized := coroPhysicalNormalizeSourceSignature(target.Signature)
	var targetContext *context
	if universe != nil {
		if canonical := universe.canonicalAlias(raw); canonical == nil || canonical != target {
			return fmt.Errorf("static coroutine method target is not the frozen canonical alias of its source operand")
		}
		effectiveRaw, err := universe.coroPhysicalSourceSignature(raw)
		if err != nil {
			return fmt.Errorf("derive source static coroutine method signature: %w", err)
		}
		normalized, err = universe.coroPhysicalSourceSignature(target)
		if err != nil {
			return fmt.Errorf("derive canonical static coroutine method signature: %w", err)
		}
		if !coroInterfaceDispatchSignaturesIdentical(effectiveRaw, normalized) {
			return fmt.Errorf("static coroutine method source ABI %s does not match canonical target ABI %s", effectiveRaw, normalized)
		}
		targetContext, err = universe.functionABIContext(target, universe.ownerOf(target))
		if err != nil {
			return fmt.Errorf("derive static coroutine method target ABI: %w", err)
		}
	} else if raw != target {
		return fmt.Errorf("static coroutine method alias requires a frozen emission universe")
	}
	if normalized.Params().Len() != len(target.Params) || len(common.Args) != len(target.Params) {
		return fmt.Errorf(
			"static coroutine method receiver/argument shape mismatch: normalized=%d SSA-params=%d call-args=%d",
			normalized.Params().Len(), len(target.Params), len(common.Args),
		)
	}
	for index, parameter := range target.Params {
		if parameter == nil || common.Args[index] == nil {
			return fmt.Errorf("static coroutine method operand %d is incomplete", index)
		}
		normalizedType := normalized.Params().At(index).Type()
		parameterType := parameter.Type()
		if targetContext != nil {
			parameterType = targetContext.patchType(parameterType)
		}
		if !types.Identical(parameterType, normalizedType) {
			return fmt.Errorf(
				"static coroutine method canonical operand %d does not match the normalized receiver/parameter ABI (normalized=%s SSA-parameter=%s)",
				index, normalizedType, parameterType,
			)
		}
	}
	return nil
}

func validateCoroAwaitTarget(caller, target coro.FunctionPlan) error {
	if caller.Emission != coro.EmitCoroutine {
		return fmt.Errorf("caller emission is %s, want coroutine", caller.Emission)
	}
	if target.External != coro.Defined || target.Emission != coro.EmitCoroutine ||
		(target.FuncRep != coro.DirectCoro && target.FuncRep != coro.Dispatch) || !target.Demand.Contains(coro.AsyncDemand) {
		return fmt.Errorf(
			"target %q has no defined coroutine entry with async demand (external=%s emission=%s representation=%s demand=%s)",
			target.ID, target.External, target.Emission, target.FuncRep, target.Demand,
		)
	}
	return nil
}

// compileCoroStaticAwait lowers a source-style synchronous call into one
// stackless child handoff. It creates the child only to its initial suspend;
// this function never resumes or destroys a handle. Those operations belong to
// the scheduler after the parent's resume episode has returned.
func (p *context) compileCoroStaticAwait(
	b llssa.Builder, call *ssa.Call, instructionPlan coroPhysicalInstructionPlan,
) llssa.Expr {
	if !p.hasCoroPhysicalBody() || call == nil || instructionPlan.control != coroPhysicalControlDirectAwait {
		panic("coroutine child await escaped its frozen physical control recipe")
	}
	// Keep the ordinary call lowerer's frontend-elided package-init rule ahead
	// of coroutine CallPlan dispatch. fnIgnore is not a variadic arity; passing
	// it to compileValues would subtract two operands from a zero-argument call.
	if p.funcKind(call.Call.Value) == fnIgnore {
		panic("coroutine child await selected a frontend-elided initializer")
	}
	callee := instructionPlan.controlTarget
	if callee == nil || instructionPlan.controlTargetID == "" {
		panic("coroutine child await has an incomplete frozen physical control recipe")
	}
	p.recordCallerLocationForCall(b, &call.Call)
	p.emitPCLineLabel(b, call.Pos())

	// Preserve Go's left-to-right argument evaluation before publishing any
	// child or parent scheduler state.
	args := p.compileValues(b, call.Call.Args, p.funcKind(call.Call.Value))
	var closureContext llssa.Expr
	if len(callee.FreeVars) != 0 {
		closure, exact := call.Call.Value.(*ssa.MakeClosure)
		if !exact {
			panic("coroutine child await lost its exact captured closure")
		}
		closureValue := p.compileValue(b, closure)
		closureContext = b.Field(closureValue, 1)
	}
	keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)
	return p.compileCoroTargetAwaitWithContextAndRecovery(b, callee, closureContext, args, nil, keepaliveSlots)
}

// compileCoroTargetAwait lowers one already-resolved exact managed target.
// args must have been evaluated in source order before this function is called.
// It is shared by source SSA calls and compiler-inserted runtime helper calls.
func (p *context) compileCoroTargetAwait(b llssa.Builder, callee *ssa.Function, args []llssa.Expr) llssa.Expr {
	return p.compileCoroTargetAwaitWithContextAndRecovery(b, callee, llssa.Nil, args, nil, nil)
}

func (p *context) compileCoroTargetAwaitWithKeepalive(
	b llssa.Builder, callee *ssa.Function, args, keepalive []llssa.Expr,
) llssa.Expr {
	return p.compileCoroTargetAwaitWithContextAndRecovery(b, callee, llssa.Nil, args, nil, keepalive)
}

func (p *context) compileCoroTargetAwaitWithContext(
	b llssa.Builder, callee *ssa.Function, closureContext llssa.Expr, args []llssa.Expr,
) llssa.Expr {
	return p.compileCoroTargetAwaitWithContextAndRecovery(b, callee, closureContext, args, nil, nil)
}

func (p *context) compileCoroCleanupTargetAwait(
	b llssa.Builder, callee *ssa.Function, args []llssa.Expr, cleanup *coroStaticCleanupState,
) llssa.Expr {
	body := p.coroBody()
	if cleanup == nil || body == nil || body.cleanup != cleanup {
		panic("coroutine cleanup await requires the active static cleanup drainer")
	}
	return p.compileCoroTargetAwaitWithContextAndRecovery(b, callee, llssa.Nil, args, cleanup, nil)
}

func (p *context) compileCoroTargetAwaitWithContextAndRecovery(
	b llssa.Builder, callee *ssa.Function, closureContext llssa.Expr, args []llssa.Expr,
	cleanup *coroStaticCleanupState, keepaliveSlots []llssa.Expr,
) llssa.Expr {
	return p.compileCoroTargetEntryAwaitWithContextAndRecovery(
		b, p.mustFunctionSymbol(callee), closureContext, args, cleanup, keepaliveSlots,
	)
}

// compileCoroTargetEntryAwaitWithContextAndRecovery consumes an already
// resolved physical symbol role. Most callers use the generic wrapper above;
// patch initialization passes its exact private original-init role so neither
// declaration nor body materialization can silently resolve back to public
// init.
func (p *context) compileCoroTargetEntryAwaitWithContextAndRecovery(
	b llssa.Builder, entry plannedFunctionSymbol, closureContext llssa.Expr, args []llssa.Expr,
	cleanup *coroStaticCleanupState, keepaliveSlots []llssa.Expr,
) llssa.Expr {
	callee := entry.function
	body := p.coroBody()
	if body == nil || p.compilation == nil || p.compilation.CoroPlan == nil {
		panic("coroutine child await requires an active physical coroutine body")
	}
	if b.Func != p.fn {
		panic("coroutine child await builder does not belong to the active physical coroutine function")
	}
	callerPlan, ok := p.compilation.CoroPlan.FunctionPlan(p.goFn)
	if !ok {
		panic("coroutine child await: current function has no compilation plan")
	}
	targetPlan, ok := p.compilation.CoroPlan.FunctionPlan(callee)
	if !ok {
		panic("coroutine child await: target has no compilation plan")
	}
	if err := validateCoroAwaitTarget(callerPlan, targetPlan); err != nil {
		panic(fmt.Sprintf("coroutine child await: function %q: %v", callerPlan.ID, err))
	}

	if p.emissionUniverse == nil {
		panic("coroutine child await requires a prepared emission universe")
	}
	sourceSig, err := p.emissionUniverse.coroPhysicalSourceSignature(callee)
	if err != nil {
		panic(fmt.Sprintf("coroutine child await: derive target %q ABI: %v", entry.plan.ID, err))
	}
	physicalArgs := args
	if len(callee.FreeVars) != 0 {
		if closureContext.IsNil() {
			panic(fmt.Sprintf("coroutine child await: captured target %q has no exact closure context", entry.plan.ID))
		}
		sourceSig, err = p.emissionUniverse.coroPhysicalEntrySourceSignature(callee)
		if err != nil {
			panic(fmt.Sprintf("coroutine child await: derive captured target %q ABI: %v", entry.plan.ID, err))
		}
		physicalArgs = make([]llssa.Expr, 0, len(args)+1)
		physicalArgs = append(physicalArgs, closureContext)
		physicalArgs = append(physicalArgs, args...)
	} else if !closureContext.IsNil() {
		panic(fmt.Sprintf("coroutine child await: non-captured target %q received a closure context", entry.plan.ID))
	}
	abi := newCoroPhysicalABI(p, entry, sourceSig)
	if len(physicalArgs) != sourceSig.Params().Len() {
		panic(fmt.Sprintf(
			"coroutine child await: target %q arguments=%d do not match normalized source parameters=%d",
			entry.plan.ID, len(physicalArgs), sourceSig.Params().Len(),
		))
	}
	childFn, _, kind := p.compileFunctionEntry(entry)
	if kind != goFunc {
		panic(fmt.Sprintf("coroutine child await: target %q did not resolve to a Go entry", entry.plan.ID))
	}

	resultType := p.prog.Type(abi.resultSlotType, llssa.InGo)
	resultSlot := p.coroFrameAlloca(resultType)
	callArgs := make([]llssa.Expr, 0, len(physicalArgs)+2)
	callArgs = append(callArgs,
		body.task,
		b.Convert(p.prog.VoidPtr(), resultSlot),
	)
	callArgs = append(callArgs, physicalArgs...)
	child := b.Call(childFn.Expr, callArgs...)
	if child.Type == nil || !types.Identical(child.RawType(), types.Typ[types.UnsafePointer]) {
		var childType types.Type
		if child.Type != nil {
			childType = child.RawType()
		}
		panic(fmt.Sprintf(
			"coroutine child await: caller %q target %q symbol %q returned %v; declaration=%v, want unsafe.Pointer physical handle",
			callerPlan.ID, targetPlan.ID, childFn.Name(), childType, childFn.Expr.RawType(),
		))
	}
	return p.awaitCoroChildWithRecovery(b, child, resultSlot, sourceSig.Results(), cleanup, keepaliveSlots)
}

// compileCoroPatchInitAwait lowers the compiler-inserted call from a patch
// package initializer to the original package initializer. Both source
// signatures are func(), but their managed entries are physical coroutines;
// this edge therefore uses the same scheduler-owned child transaction as an
// ordinary static synchronous-style call.
func (p *context) compileCoroPatchInitAwait(b llssa.Builder) {
	if !p.hasCoroPhysicalBody() || b == nil || b.Func != p.fn {
		panic("coroutine patch initializer await requires an active physical body")
	}
	if p.emissionUniverse == nil || p.compilation.CoroPlan == nil || p.goFn == nil {
		panic("coroutine patch initializer await requires a frozen exact plan")
	}
	original, frozen, err := p.emissionUniverse.ResolveCoroLoweredCall(p.goFn, coroPatchOriginalInitCall)
	if err != nil {
		panic(fmt.Errorf("coroutine patch initializer edge: %w", err))
	}
	planned, exact := p.compilation.CoroPlan.ResolveLoweredCall(p.goFn, coroPatchOriginalInitCall)
	if !frozen || original == nil || !exact || planned != original {
		panic("coroutine patch initializer edge disagrees between the frozen emission universe and SSA plan")
	}
	entry := p.mustPatchOriginalInitFunctionSymbol(original)
	if entry.function != original || !entry.patchOriginalInit {
		panic("coroutine patch initializer edge lost its exact private original-init role")
	}
	result := p.compileCoroTargetEntryAwaitWithContextAndRecovery(b, entry, llssa.Nil, nil, nil, nil)
	if !result.IsNil() {
		panic("coroutine original package initializer returned a value")
	}
}

// coroFrameAlloca emits storage in the physical ramp entry so the definition
// dominates every selected dispatch branch and every post-suspend resume edge.
// LLVM CoroSplit then owns deciding which live slots become fields of the
// stackless frame.  Emitting an alloca at a dynamic call site is invalid: that
// block executes only in the pre-suspend activation and does not dominate the
// generated resume function after coroutine splitting.
func (p *context) coroFrameAlloca(typ llssa.Type) llssa.Expr {
	if !p.hasCoroPhysicalBody() || p.fn == nil || typ == nil {
		panic("coroutine frame alloca requires an active physical body and type")
	}
	entry := p.fn.Block(0)
	alloc := p.fn.NewBuilder()
	defer alloc.Dispose()
	alloc.SetBlockEx(entry, llssa.AtStart, true)
	return alloc.AllocaT(typ)
}

// coroFrameAlloc emits zero-initialized function-lifetime storage in the
// physical ramp entry. Source SSA stack Allocs normally live in source block
// zero, but that block is no longer the LLVM entry of a physical coroutine:
// cancellation and static-cleanup dispatch may enter one of its continuations
// without a CFG edge from the source block. Keeping the allocation (and its
// one-time Go zero initialization) in the ramp makes its address dominate all
// such compiler-owned entries while still leaving CoroSplit to retain only
// values that are actually live across a suspension.
func (p *context) coroFrameAlloc(typ llssa.Type) llssa.Expr {
	if !p.hasCoroPhysicalBody() || p.fn == nil || typ == nil {
		panic("coroutine frame allocation requires an active physical body and type")
	}
	entry := p.fn.Block(0)
	alloc := p.fn.NewBuilder()
	defer alloc.Dispose()
	alloc.SetBlockEx(entry, llssa.AtStart, true)
	return alloc.Alloc(typ, false)
}

// awaitCoroChild completes the scheduler-owned half of one already-created
// child transaction. Exact static calls, interface dispatch, and the universal
// function descriptor all converge here, so registration, parent suspension,
// activation, and result reconstruction cannot drift between call shapes.
func (p *context) awaitCoroChild(
	b llssa.Builder, child, resultSlot llssa.Expr, results *types.Tuple,
) llssa.Expr {
	return p.awaitCoroChildWithRecovery(b, child, resultSlot, results, nil, nil)
}

func (p *context) awaitCoroChildWithKeepalive(
	b llssa.Builder, child, resultSlot llssa.Expr, results *types.Tuple, keepaliveSlots []llssa.Expr,
) llssa.Expr {
	return p.awaitCoroChildWithRecovery(b, child, resultSlot, results, nil, keepaliveSlots)
}

func (p *context) awaitCoroChildWithRecovery(
	b llssa.Builder, child, resultSlot llssa.Expr, results *types.Tuple,
	cleanup *coroStaticCleanupState, keepaliveSlots []llssa.Expr,
) llssa.Expr {
	body := p.coroBody()
	if body == nil {
		panic("coroutine child await requires an active PhysicalABIV1 body")
	}
	if b.Func != p.fn || child.IsNil() || resultSlot.IsNil() {
		panic("coroutine child await requires a child handle and result slot in the active function")
	}
	childHeader := b.CoroPromise(child, coroHeaderType(p.prog))
	b.Store(b.FieldAddr(childHeader, coroHeaderParent), body.coro.Handle())
	recoverMode := p.prog.IntVal(coroAwaitRecoverNone, p.prog.Uint32())
	recoverType := p.prog.Nil(p.prog.VoidPtr())
	recoverData := p.prog.Nil(p.prog.VoidPtr())
	if cleanup != nil {
		recoverMode, recoverType, recoverData = cleanup.recoverAwaitArguments(p, b)
	}
	body.suspendForChild(b)

	if body.abi.awaitPrepareHook == "" {
		panic("coroutine child await has no scheduler handoff hook")
	}
	publish := p.pkg.NewFunc(body.abi.awaitPrepareHook, coroAwaitPrepareSignature(), llssa.InC)
	b.Call(
		publish.Expr,
		body.task,
		body.coro.Handle(),
		child,
		recoverMode,
		recoverType,
		recoverData,
	)
	if body.abi.awaitConsumeHook == "" {
		panic("coroutine child await has no outcome consume hook")
	}
	typeWord := p.coroFrameAlloca(p.prog.VoidPtr())
	dataWord := p.coroFrameAlloca(p.prog.VoidPtr())
	b.Store(typeWord, p.prog.Nil(p.prog.VoidPtr()))
	b.Store(dataWord, p.prog.Nil(p.prog.VoidPtr()))
	consume := p.pkg.NewFunc(body.abi.awaitConsumeHook, coroAwaitConsumeSignature(), llssa.InC)

	// A task-cancellation decision is taken before the site's ordinary resumed
	// continuation. Give child-await a per-site gate so cancellation still
	// consumes the now-dead child's CompletionRecord before entering shared
	// cleanup; otherwise an older deferred child could collide with the stale
	// transaction. When cleanup is active, the gate retains Abort/Shutdown as
	// the base while reconciling a concurrent deferred-child recovery/panic as
	// the overlay. This preserves both cancellation and Go panic ordering.
	canceled := p.fn.MakeBlock()
	body.coro.SuspendCurrentBlockWithResumeDispatch(func(gate llssa.Builder, normal llssa.BasicBlock) {
		body.dispatchZeroRunDecisionTo(gate, normal, canceled)
	})
	body.activate(b)
	cancelBuilder := p.fn.NewBuilder()
	cancelBuilder.SetBlock(canceled)
	body.activate(cancelBuilder)
	cancelStatus := cancelBuilder.Call(
		consume.Expr,
		body.task,
		body.coro.Handle(),
		cancelBuilder.Convert(p.prog.VoidPtr(), typeWord),
		cancelBuilder.Convert(p.prog.VoidPtr(), dataWord),
	)
	p.emitCoroKeepaliveSlots(cancelBuilder, keepaliveSlots)
	if ownerCleanup := body.cleanup; ownerCleanup == nil {
		cancelBuilder.Jump(body.completion)
	} else {
		ownerCleanup.setCancellationBase(cancelBuilder)
		returnedCancel := p.fn.MakeBlock()
		panickedCancel := p.fn.MakeBlock()
		abortedCancel := p.fn.MakeBlock()
		shutdownCancel := p.fn.MakeBlock()
		drainCancel := p.fn.MakeBlock()
		invalidCancel := p.fn.MakeBlock()
		cancelDispatch := cancelBuilder.Switch(cancelStatus, invalidCancel)
		cancelDispatch.Case(p.prog.IntVal(coroAwaitCompletionReturn, p.prog.Uint32()), returnedCancel)
		cancelDispatch.Case(p.prog.IntVal(coroAwaitCompletionPanic, p.prog.Uint32()), panickedCancel)
		cancelDispatch.Case(p.prog.IntVal(coroAwaitCompletionAbort, p.prog.Uint32()), abortedCancel)
		cancelDispatch.Case(p.prog.IntVal(coroAwaitCompletionShutdown, p.prog.Uint32()), shutdownCancel)
		var recoveredCancel llssa.BasicBlock
		if cleanup != nil {
			if cleanup != ownerCleanup {
				panic("deferred child cancellation recovery escaped its owner cleanup")
			}
			recoveredCancel = p.fn.MakeBlock()
			cancelDispatch.Case(p.prog.IntVal(coroAwaitCompletionReturnRecovered, p.prog.Uint32()), recoveredCancel)
		}
		cancelDispatch.End(cancelBuilder)

		cancelBuilder.SetBlockEx(returnedCancel, llssa.AtEnd, false)
		cancelBuilder.Jump(drainCancel)
		cancelBuilder.SetBlockEx(panickedCancel, llssa.AtEnd, false)
		ownerCleanup.setPanicOverlay(cancelBuilder, cancelBuilder.Load(typeWord), cancelBuilder.Load(dataWord))
		cancelBuilder.Jump(drainCancel)
		cancelBuilder.SetBlockEx(abortedCancel, llssa.AtEnd, false)
		cancelBuilder.Jump(drainCancel)
		cancelBuilder.SetBlockEx(shutdownCancel, llssa.AtEnd, false)
		cancelBuilder.Jump(drainCancel)
		cancelBuilder.SetBlockEx(invalidCancel, llssa.AtEnd, false)
		cancelBuilder.Unreachable()
		if cleanup != nil {
			cancelBuilder.SetBlockEx(recoveredCancel, llssa.AtEnd, false)
			cleanup.reconcileDeferredChildReturn(p, cancelBuilder, coroAwaitCompletionReturnRecovered)
			cancelBuilder.Jump(drainCancel)
		}
		cancelBuilder.SetBlockEx(drainCancel, llssa.AtEnd, false)
		ownerCleanup.resume(cancelBuilder)
	}
	cancelBuilder.Dispose()

	// The child allocation is gone before this continuation is resumed.  Its
	// terminal outcome therefore lives in scheduler-owned parent metadata, not
	// in the result slot or child promise.  Consume exactly once before reading
	// results or allowing another child transaction to start.
	status := b.Call(
		consume.Expr,
		body.task,
		body.coro.Handle(),
		b.Convert(p.prog.VoidPtr(), typeWord),
		b.Convert(p.prog.VoidPtr(), dataWord),
	)
	p.emitCoroKeepaliveSlots(b, keepaliveSlots)
	returned := p.fn.MakeBlock()
	panicked := p.fn.MakeBlock()
	aborted := p.fn.MakeBlock()
	shutdown := p.fn.MakeBlock()
	invalid := p.fn.MakeBlock()
	dispatch := b.Switch(status, invalid)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionReturn, p.prog.Uint32()), returned)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionPanic, p.prog.Uint32()), panicked)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionAbort, p.prog.Uint32()), aborted)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionShutdown, p.prog.Uint32()), shutdown)
	var recovered llssa.BasicBlock
	if cleanup != nil {
		recovered = p.fn.MakeBlock()
		dispatch.Case(p.prog.IntVal(coroAwaitCompletionReturnRecovered, p.prog.Uint32()), recovered)
	}
	dispatch.End(b)

	b.SetBlockEx(panicked, llssa.AtEnd, false)
	if body.panicPrepare.IsNil() {
		// A compilation without the ExplicitStatus identity cannot produce a
		// managed child panic. Treat an injected/corrupt status as unreachable
		// instead of falling back to legacy stack unwinding.
		b.Unreachable()
	} else if body.cleanup == nil {
		body.panic(b, b.Load(typeWord), b.Load(dataWord))
	} else if cleanup != nil {
		cleanup.replacePanic(b, b.Load(typeWord), b.Load(dataWord))
	} else {
		body.cleanup.enterPanic(b, b.Load(typeWord), b.Load(dataWord))
	}

	b.SetBlockEx(aborted, llssa.AtEnd, false)
	body.enterCancellation(b, coroAwaitCompletionAbort)
	b.SetBlockEx(shutdown, llssa.AtEnd, false)
	body.enterCancellation(b, coroAwaitCompletionShutdown)

	b.SetBlockEx(invalid, llssa.AtEnd, false)
	b.Unreachable()
	if cleanup != nil {
		b.SetBlockEx(recovered, llssa.AtEnd, false)
		cleanup.reconcileDeferredChildReturn(p, b, coroAwaitCompletionReturnRecovered)
		b.Jump(returned)
	}
	b.SetBlockContinuation(returned)
	return p.loadCoroAwaitResult(b, resultSlot, results)
}

// loadCoroAwaitResult reconstructs the exact source call value after the
// scheduler has resumed the parent. Multi-result calls are one SSA tuple value,
// not a result-slot struct: preserving that distinction keeps the ordinary
// Extract lowering and ValuePlan paths identical to a synchronous Go call.
func (p *context) loadCoroAwaitResult(b llssa.Builder, resultSlot llssa.Expr, results *types.Tuple) llssa.Expr {
	count := 0
	if results != nil {
		count = results.Len()
	}
	switch count {
	case 0:
		return llssa.Nil
	case 1:
		return b.Load(b.FieldAddr(resultSlot, 0))
	default:
		fields := make([]llssa.Expr, results.Len())
		for i := range fields {
			fields[i] = b.Load(b.FieldAddr(resultSlot, i))
		}
		return b.Aggregate(p.prog.Type(results, llssa.InGo), fields...)
	}
}
