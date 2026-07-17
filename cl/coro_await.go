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

// resolveCoroStaticAwait proves the exact subset implemented by the physical
// child-await lowering. The returned function is the canonical target recorded
// by the whole-program plan, not an identity inferred from an SSA display name.
func resolveCoroStaticAwait(plan *coro.SSAPlan, caller coro.FunctionPlan, call ssa.CallInstruction) (*ssa.Function, coro.FunctionPlan, error) {
	if plan == nil || call == nil || call.Common() == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires a compilation CallPlan")
	}
	common := call.Common()
	if common.IsInvoke() || common.StaticCallee() == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires a static non-invoke call")
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
	return target, targetPlan, nil
}

func validateCoroAwaitTarget(caller, target coro.FunctionPlan) error {
	if caller.Emission != coro.EmitCoroutine {
		return fmt.Errorf("caller emission is %s, want coroutine", caller.Emission)
	}
	if target.External != coro.Defined || target.Emission != coro.EmitCoroutine || target.FuncRep != coro.DirectCoro || target.Demand != coro.AsyncDemand {
		return fmt.Errorf(
			"target %q is not an async-only defined direct coroutine (external=%s emission=%s representation=%s demand=%s)",
			target.ID, target.External, target.Emission, target.FuncRep, target.Demand,
		)
	}
	return nil
}

// tryCompileCoroStaticAwait lowers a source-style synchronous call into one
// stackless child handoff. It creates the child only to its initial suspend;
// this function never resumes or destroys a handle. Those operations belong to
// the scheduler after the parent's resume episode has returned.
func (p *context) tryCompileCoroStaticAwait(b llssa.Builder, call *ssa.Call) (llssa.Expr, bool) {
	if p.currentCoro == nil || p.compilation == nil || p.compilation.CoroPlan == nil || !p.compilation.EnableCoroChildAwait || call == nil {
		return llssa.Nil, false
	}
	callPlan, ok := p.compilation.CoroPlan.CallPlan(call)
	if !ok || callPlan.Rep != coro.DirectCoro {
		return llssa.Nil, false
	}
	callerPlan, ok := p.compilation.CoroPlan.FunctionPlan(p.goFn)
	if !ok {
		panic("coroutine child await: current function has no compilation plan")
	}
	callee, _, err := resolveCoroStaticAwait(p.compilation.CoroPlan, callerPlan, call)
	if err != nil {
		panic(fmt.Sprintf("coroutine child await: function %q: %v", callerPlan.ID, err))
	}

	p.recordCallerLocationForCall(b, &call.Call)
	p.emitPCLineLabel(b, call.Pos())

	// Preserve Go's left-to-right argument evaluation before publishing any
	// child or parent scheduler state.
	args := p.compileValues(b, call.Call.Args, p.funcKind(call.Call.Value))
	return p.compileCoroTargetAwait(b, callee, args), true
}

// compileCoroTargetAwait lowers one already-resolved exact managed target.
// args must have been evaluated in source order before this function is called.
// It is shared by source SSA calls and compiler-inserted runtime helper calls.
func (p *context) compileCoroTargetAwait(b llssa.Builder, callee *ssa.Function, args []llssa.Expr) llssa.Expr {
	if p.currentCoro == nil || p.compilation == nil || p.compilation.CoroPlan == nil || !p.compilation.EnableCoroChildAwait {
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

	entry := p.mustFunctionSymbol(callee)
	if p.emissionUniverse == nil {
		panic("coroutine child await requires a prepared emission universe")
	}
	sourceSig, err := p.emissionUniverse.coroPhysicalSourceSignature(callee)
	if err != nil {
		panic(fmt.Sprintf("coroutine child await: derive target %q ABI: %v", entry.plan.ID, err))
	}
	abi := newCoroPhysicalABI(p, entry, sourceSig)
	childFn, _, kind := p.compileFunction(callee)
	if kind != goFunc {
		panic(fmt.Sprintf("coroutine child await: target %q did not resolve to a Go entry", entry.plan.ID))
	}

	resultType := p.prog.Type(abi.resultSlotType, llssa.InGo)
	resultSlot := b.AllocaT(resultType)
	physicalArgs := make([]llssa.Expr, 0, len(args)+2)
	physicalArgs = append(physicalArgs,
		p.currentCoro.task,
		b.Convert(p.prog.VoidPtr(), resultSlot),
	)
	physicalArgs = append(physicalArgs, args...)
	child := b.Call(childFn.Expr, physicalArgs...)
	childHeader := b.CoroPromise(child, coroHeaderType(p.prog))
	b.Store(b.FieldAddr(childHeader, coroHeaderParent), p.currentCoro.coro.Handle())
	p.currentCoro.suspendForChild(b)

	if p.currentCoro.abi.awaitPrepareHook == "" {
		panic("coroutine child await has no scheduler handoff hook")
	}
	publish := p.pkg.NewFunc(p.currentCoro.abi.awaitPrepareHook, coroAwaitPrepareSignature(), llssa.InC)
	b.Call(publish.Expr, p.currentCoro.task, p.currentCoro.coro.Handle(), child)
	// Child await remains a zero-ticket continuation for now. It may branch to
	// shared task cleanup, but exact result/cancel reconciliation must remain at
	// this site once CompletionRecord and result-lease lowering are connected.
	p.currentCoro.coro.SuspendCurrentBlock()
	p.currentCoro.activate(b)

	return p.loadCoroAwaitResult(b, resultSlot, sourceSig.Results())
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
