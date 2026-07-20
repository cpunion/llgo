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

	"github.com/goplus/llgo/internal/coro"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
)

func coroSpawnBeginSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	return types.NewSignatureType(nil, nil, nil,
		types.NewTuple(types.NewParam(token.NoPos, nil, "parent", pointer)),
		types.NewTuple(types.NewParam(token.NoPos, nil, "child", pointer)),
		false,
	)
}

func coroSpawnCommitSignature() *types.Signature {
	pointer := types.Typ[types.UnsafePointer]
	return types.NewSignatureType(nil, nil, nil,
		types.NewTuple(
			types.NewParam(token.NoPos, nil, "parent", pointer),
			types.NewParam(token.NoPos, nil, "child", pointer),
			types.NewParam(token.NoPos, nil, "handle", pointer),
		), nil, false,
	)
}

func resolveCoroDirectStaticSpawn(
	plan *coro.SSAPlan,
	spawn *ssa.Go,
	managedDispatch bool,
) (*ssa.Function, coro.FunctionPlan, error) {
	if plan == nil || spawn == nil || spawn.Common() == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires a compilation CallPlan")
	}
	callPlan, found := plan.CallPlan(spawn)
	if !found {
		return nil, coro.FunctionPlan{}, fmt.Errorf("spawn has no compilation CallPlan")
	}
	if callPlan.Transport == coro.RawCCodePointer {
		return nil, coro.FunctionPlan{}, fmt.Errorf("raw C code-pointer callee cannot be spawned through the managed coroutine scheduler")
	}
	target, targetPlan, directErr := plan.ResolveClosedStaticSpawn(spawn)
	if directErr == nil {
		if err := validateCoroDirectSpawnArgumentTransport(plan, spawn, target, managedDispatch); err != nil {
			return nil, coro.FunctionPlan{}, err
		}
		return target, targetPlan, nil
	}
	common := spawn.Common()
	raw, direct := common.Value.(*ssa.Function)
	if direct && raw != nil && raw.Signature != nil && raw.Signature.Recv() == nil {
		return nil, coro.FunctionPlan{}, directErr
	}
	if !direct || raw == nil || common.StaticCallee() != raw || common.IsInvoke() || common.Method != nil ||
		raw.Signature == nil || raw.Signature.Recv() == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("requires an exact static function or method operand")
	}
	if callPlan.Kind != coro.CallSpawn || callPlan.Rep != coro.DirectCoro ||
		callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"requires one closed non-nil DirectCoro spawn target, got kind=%v representation=%s open=%t may-be-nil=%t targets=%d",
			callPlan.Kind, callPlan.Rep, callPlan.Open, callPlan.MayBeNil, len(callPlan.Targets),
		)
	}
	target, found = plan.Function(callPlan.Targets[0])
	if !found || target == nil {
		return nil, coro.FunctionPlan{}, fmt.Errorf("spawn target %q is absent from the compilation plan", callPlan.Targets[0])
	}
	targetPlan, found = plan.FunctionPlan(target)
	if !found || targetPlan.ID != callPlan.Targets[0] || targetPlan.External != coro.Defined ||
		targetPlan.Emission != coro.EmitCoroutine || targetPlan.Primary != coro.PrimaryCoroutine ||
		targetPlan.FuncRep != coro.DirectCoro || targetPlan.Demand != coro.AsyncDemand ||
		!targetPlan.Effect.Contains(coro.YieldOnly) {
		return nil, coro.FunctionPlan{}, fmt.Errorf(
			"spawn method target %q is not one demanded preemptible direct coroutine (external=%s emission=%s primary=%s representation=%s demand=%s effect=%s)",
			callPlan.Targets[0], targetPlan.External, targetPlan.Emission, targetPlan.Primary,
			targetPlan.FuncRep, targetPlan.Demand, targetPlan.Effect,
		)
	}
	if target.Signature == nil || target.Signature.Recv() == nil || target.Signature.Variadic() ||
		target.Signature.Results().Len() != 0 || typeParamCount(target.Signature.TypeParams()) != 0 ||
		typeParamCount(target.Signature.RecvTypeParams()) != 0 {
		return nil, coro.FunctionPlan{}, fmt.Errorf("spawn target %q is not an exact non-generic zero-result method", targetPlan.ID)
	}
	ownerPlan, found := plan.FunctionPlan(spawn.Parent())
	if !found || ownerPlan.Emission != coro.EmitCoroutine || ownerPlan.Primary != coro.PrimaryCoroutine ||
		ownerPlan.Demand != coro.AsyncDemand || !ownerPlan.Effect.Contains(coro.YieldOnly) {
		return nil, coro.FunctionPlan{}, fmt.Errorf("spawn owner is not one demanded preemptible coroutine primary")
	}
	if err := validateCoroDirectSpawnArgumentTransport(plan, spawn, target, managedDispatch); err != nil {
		return nil, coro.FunctionPlan{}, err
	}
	return target, targetPlan, nil
}

// validateCoroDirectSpawnArgumentTransport proves the physical receiver/args
// tuple consumed by a direct coroutine ramp. Transport and representation are
// orthogonal per function leaf: a raw C function is one DirectPlain code
// pointer, while a managed Go function is the universal Dispatch descriptor
// closure. Aggregates may contain both and retain that exact recursive physical
// layout. Only managed leaves depend on the descriptor transport capability.
func validateCoroDirectSpawnArgumentTransport(
	plan *coro.SSAPlan,
	spawn *ssa.Go,
	target *ssa.Function,
	managedDispatch bool,
) error {
	if plan == nil || spawn == nil || spawn.Common() == nil || target == nil || target.Signature == nil {
		return fmt.Errorf("direct spawn argument transport requires an exact target signature")
	}
	physical := coroPhysicalNormalizeSourceSignature(target.Signature)
	args := spawn.Common().Args
	if physical == nil || physical.Params().Len() != len(args) {
		return fmt.Errorf("direct spawn arguments=%d do not match normalized target parameters=%d", len(args), physical.Params().Len())
	}
	for index, argument := range args {
		parameter := physical.Params().At(index).Type()
		if !types.Identical(argument.Type(), parameter) {
			return fmt.Errorf("direct spawn argument %d type %s does not match target parameter %s", index, argument.Type(), parameter)
		}
		if !coroPhysicalTypeContainsFunctionValue(argument.Type(), make(map[types.Type]bool)) {
			continue
		}
		valuePlan, found := plan.ValuePlan(argument)
		if !found || valuePlan.Value != argument || len(valuePlan.Funcs) == 0 {
			return fmt.Errorf("direct spawn function-containing argument %d has no exact ValuePlan", index)
		}
		_, scalar := types.Unalias(argument.Type()).Underlying().(*types.Signature)
		if scalar && (len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0) {
			return fmt.Errorf("direct spawn scalar function argument %d has no exact scalar ValuePlan", index)
		}
		for leafIndex, leaf := range valuePlan.Funcs {
			switch leaf.Transport {
			case coro.RawCCodePointer:
				if leaf.Rep != coro.DirectPlain {
					return fmt.Errorf(
						"direct spawn argument %d function leaf %d has raw C transport with representation %s",
						index, leafIndex, leaf.Rep,
					)
				}
			case coro.ManagedTransport:
				if leaf.Rep != coro.Dispatch {
					return fmt.Errorf(
						"direct spawn argument %d function leaf %d has managed transport with representation %s",
						index, leafIndex, leaf.Rep,
					)
				}
				if !managedDispatch {
					return fmt.Errorf(
						"direct spawn argument %d managed function leaf %d requires the universal descriptor transport capability",
						index, leafIndex,
					)
				}
			default:
				return fmt.Errorf(
					"direct spawn argument %d function leaf %d has invalid transport %s",
					index, leafIndex, leaf.Transport,
				)
			}
		}
	}
	return nil
}

// tryCompileCoroClosedStaticSpawn creates exactly one child root to its LLVM
// initial suspend and commits it to the scheduler. Arguments are fully
// materialized before begin mutates scheduler state. The parent then reaches
// an explicit safepoint using its physical G; there is no TLS/current-G
// fallback anywhere in this path.
func (p *context) tryCompileCoroClosedStaticSpawn(b llssa.Builder, spawn *ssa.Go) bool {
	if p.compilation == nil || !p.compilation.EnableCoroClosedStaticSpawn || spawn == nil {
		return false
	}
	if p.currentCoro == nil || p.compilation.CoroPlan == nil || b.Func != p.fn {
		panic("closed static spawn requires an active planned physical coroutine body")
	}
	callPlan, found := p.compilation.CoroPlan.CallPlan(spawn)
	if !found {
		caller, _ := p.compilation.CoroPlan.FunctionPlan(p.goFn)
		panic(fmt.Sprintf("coroutine spawn: function %q has no compilation CallPlan", caller.ID))
	}
	if callPlan.Rep == coro.Dispatch {
		p.compileCoroManagedDispatchSpawn(b, spawn)
		return true
	}
	target, targetPlan, err := resolveCoroDirectStaticSpawn(
		p.compilation.CoroPlan, spawn, p.compilation.EnableCoroPlainDispatch,
	)
	if err != nil {
		caller, _ := p.compilation.CoroPlan.FunctionPlan(p.goFn)
		panic(fmt.Sprintf("closed static spawn: function %q: %v", caller.ID, err))
	}

	p.recordCallerLocationForCall(b, &spawn.Call)
	p.emitPCLineLabel(b, spawn.Pos())
	// Go SSA already sequences argument-producing instructions. Re-materialize
	// every exact operand here, in source order, before the begin transaction.
	args := p.compileValues(b, spawn.Call.Args, fnNormal)

	parent := p.currentCoro.task
	begin := p.pkg.NewFunc(coroSpawnBeginHookV1, coroSpawnBeginSignature(), llssa.InC)
	childG := b.Call(begin.Expr, parent)
	null := p.prog.Nil(p.prog.VoidPtr())
	physicalArgs := make([]llssa.Expr, 0, len(args)+2)
	physicalArgs = append(physicalArgs, childG, null)
	physicalArgs = append(physicalArgs, args...)

	root, _, kind := p.compileFunction(target)
	if kind != goFunc {
		panic(fmt.Sprintf("closed static spawn: target %q did not resolve to a Go coroutine entry", targetPlan.ID))
	}
	if root == nil {
		panic(fmt.Sprintf("closed static spawn: target %q has no physical root", targetPlan.ID))
	}
	handle := b.Call(root.Expr, physicalArgs...)
	commit := p.pkg.NewFunc(coroSpawnCommitHookV1, coroSpawnCommitSignature(), llssa.InC)
	b.Call(commit.Expr, parent, childG, handle)
	p.currentCoro.pollAndSuspendForPreempt(b)
	return true
}

// compileCoroManagedDispatchSpawn creates an independent scheduler G from the
// universal descriptor's coroutine entry. Callee and arguments are fully
// materialized in Go order before begin publishes scheduler state. The child G
// and nil result slot are then passed to the typed descriptor thunk, which
// returns an LLVM initial-suspended handle for the existing commit transaction.
// CallCoroDispatchCoro performs the fail-closed descriptor/version/hash/result
// and HasCoro checks; plain-only or corrupt values never fall back to a native
// callback, TLS, or a synchronous adapter.
func (p *context) compileCoroManagedDispatchSpawn(b llssa.Builder, spawn *ssa.Go) {
	callPlan, err := p.compilation.CoroPlan.ResolveManagedDispatchSpawn(spawn)
	if err != nil {
		caller, _ := p.compilation.CoroPlan.FunctionPlan(p.goFn)
		panic(fmt.Sprintf("managed descriptor spawn: function %q: %v", caller.ID, err))
	}
	if callPlan.Rep != coro.Dispatch {
		panic("managed descriptor spawn requires Dispatch representation")
	}

	p.recordCallerLocationForCall(b, &spawn.Call)
	p.emitPCLineLabel(b, spawn.Pos())
	// Preserve Go's evaluation order at the scheduler transaction boundary:
	// first the function value, then every explicit argument left-to-right.
	fn := p.compileValue(b, spawn.Call.Value)
	args := p.compileValues(b, spawn.Call.Args, fnNormal)
	abi, err := newCoroPlainDispatchABI(p, spawn.Call.Signature())
	if err != nil {
		panic(fmt.Errorf("managed descriptor spawn: %w", err))
	}
	if abi.signature.Results().Len() != 0 {
		panic("managed descriptor spawn requires a zero-result signature")
	}
	opts := llssa.CoroDispatchCallOptions{
		Version: coroPlainDispatchVersion,
		ABIHash: abi.hash,
		Result:  p.prog.Type(abi.resultSlotType, llssa.InC),
	}
	// Preserve Go evaluation order: the callee and arguments are complete
	// before the nil-call check. The physical parent then owns the structured
	// panic edge, so descriptor validation needs no hidden runtime helper.
	p.compileCoroImplicitNilAccessGuard(b, b.Field(fn, 0))
	opts.DescriptorNonNil = true

	parent := p.currentCoro.task
	begin := p.pkg.NewFunc(coroSpawnBeginHookV1, coroSpawnBeginSignature(), llssa.InC)
	childG := b.Call(begin.Expr, parent)
	null := p.prog.Nil(p.prog.VoidPtr())
	handle := b.CallCoroDispatchCoro(fn, childG, null, args, opts)
	commit := p.pkg.NewFunc(coroSpawnCommitHookV1, coroSpawnCommitSignature(), llssa.InC)
	b.Call(commit.Expr, parent, childG, handle)
	p.currentCoro.pollAndSuspendForPreempt(b)
}
