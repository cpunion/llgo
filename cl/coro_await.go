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

const (
	coroAwaitCompletionReturn          uint64 = 1
	coroAwaitCompletionPanic           uint64 = 2
	coroAwaitCompletionAbort           uint64 = 3
	coroAwaitCompletionShutdown        uint64 = 4
	coroAwaitCompletionReturnRecovered uint64 = 5
	coroAwaitCompletionGoexit          uint64 = 6
	coroAwaitCompletionFaultNil        uint64 = 7

	coroAwaitRecoverNone   uint64 = 0
	coroAwaitRecoverDirect uint64 = 1
)

// resolveCoroStaticAwait proves the exact subset implemented by the physical
// child-await lowering. The returned function is the canonical target recorded
// by the whole-program plan, not an identity inferred from an SSA display name.
func resolveCoroStaticAwait(plan *coro.SSAPlan, caller coro.FunctionPlan, call ssa.CallInstruction, universe *EmissionUniverse) (*ssa.Function, coro.FunctionPlan, ssa.Value, error) {
	if plan == nil || call == nil || call.Common() == nil {
		return nil, coro.FunctionPlan{}, nil, fmt.Errorf("requires a compilation CallPlan")
	}
	common := call.Common()
	if common.IsInvoke() {
		return nil, coro.FunctionPlan{}, nil, fmt.Errorf("requires a non-invoke call")
	}
	callPlan, ok := plan.CallPlan(call)
	if !ok {
		return nil, coro.FunctionPlan{}, nil, fmt.Errorf("call has no compilation CallPlan")
	}
	if callPlan.Kind != coro.CallDirect || callPlan.Rep != coro.DirectCoro || callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 {
		return nil, coro.FunctionPlan{}, nil, fmt.Errorf(
			"requires one closed non-nil direct coroutine target, got kind=%v representation=%s open=%t may-be-nil=%t targets=%d",
			callPlan.Kind, callPlan.Rep, callPlan.Open, callPlan.MayBeNil, len(callPlan.Targets),
		)
	}
	target, ok := plan.Function(callPlan.Targets[0])
	if !ok || target == nil {
		return nil, coro.FunctionPlan{}, nil, fmt.Errorf("direct coroutine target %q is absent from the compilation plan", callPlan.Targets[0])
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.ID != callPlan.Targets[0] {
		return nil, coro.FunctionPlan{}, nil, fmt.Errorf("direct coroutine target %q has no canonical function plan", callPlan.Targets[0])
	}
	if err := validateCoroAwaitTarget(caller, targetPlan); err != nil {
		return nil, coro.FunctionPlan{}, nil, err
	}
	var closureValue ssa.Value
	if target.Signature != nil && target.Signature.Recv() != nil {
		if err := validateCoroStaticMethodCallOperands(call, target, universe); err != nil {
			return nil, coro.FunctionPlan{}, nil, err
		}
	} else if len(target.FreeVars) != 0 {
		if universe == nil {
			return nil, coro.FunctionPlan{}, nil, fmt.Errorf("captured coroutine target requires frozen closure-environment facts")
		}
		_, hasEnvironment, err := universe.closureEnvironments.entryEnvironment(target)
		if err != nil {
			return nil, coro.FunctionPlan{}, nil, err
		}
		closure, exact := common.Value.(*ssa.MakeClosure)
		if exact {
			closureTarget, targetExact := closure.Fn.(*ssa.Function)
			if targetExact {
				canonical, required := universe.Resolve(closureTarget)
				if !required || canonical == nil {
					targetExact = false
				} else {
					closureTarget = canonical
				}
			}
			if !targetExact || closureTarget != target || len(closure.Bindings) != len(target.FreeVars) {
				return nil, coro.FunctionPlan{}, nil, fmt.Errorf("closed captured coroutine target has an incompatible MakeClosure environment")
			}
			if hasEnvironment {
				closureValue = closure
			}
		} else if hasEnvironment {
			// A Phi/copy/retagged function value can still be an exact closed
			// carrier. The immutable CallPlan above proves its sole non-nil target;
			// consume only the environment word and invoke that frozen target.
			if common.Value == nil || common.Value.Type() == nil || target.Signature == nil ||
				!types.Identical(common.Value.Type().Underlying(), target.Signature.Underlying()) {
				return nil, coro.FunctionPlan{}, nil, fmt.Errorf("closed captured coroutine target has no compatible frozen function-value environment")
			}
			closureValue = common.Value
		}
	} else if common.StaticCallee() == nil {
		if common.Method != nil || target.Signature == nil || target.Signature.Variadic() || len(common.Args) != target.Signature.Params().Len() {
			return nil, coro.FunctionPlan{}, nil, fmt.Errorf("closed direct coroutine target has an incompatible dynamic call shape")
		}
		for index, argument := range common.Args {
			if argument == nil || !types.Identical(argument.Type(), target.Signature.Params().At(index).Type()) {
				return nil, coro.FunctionPlan{}, nil, fmt.Errorf("closed direct coroutine operand %d does not match the target parameter ABI", index)
			}
		}
	}
	return target, targetPlan, closureValue, nil
}

// resolveCoroCompilerElidedStaticAwaitRetag proves that one or more managed
// ChangeType nodes are only source-level names around a closed direct
// coroutine call. The physical await lowerer consumes the frozen target and
// never evaluates CallCommon.Value, so materializing these nodes would create
// a temporary closure whose code word has the coroutine entry ABI. Skipping
// them is valid only when every non-debug consumer is an independently
// validated static await of the same canonical target.
func resolveCoroCompilerElidedStaticAwaitRetag(
	audit *coroPhysicalPureSSAAudit,
	caller coro.FunctionPlan,
	change *ssa.ChangeType,
) (*ssa.Function, error) {
	if audit == nil {
		return nil, fmt.Errorf("requires one exact physical audit")
	}
	plan, universe, owner := audit.plan, audit.universe, audit.fn
	if plan == nil || universe == nil || owner == nil || change == nil || change.Parent() != owner {
		return nil, fmt.Errorf("requires one owner-local managed ChangeType")
	}
	verifyRetag := func(retag *ssa.ChangeType) error {
		if retag == nil || retag.Parent() != owner || retag.X == nil {
			return fmt.Errorf("retag chain contains an incomplete or foreign-owner ChangeType")
		}
		sourceType := coroCallableEffectiveType(universe, owner, retag.X.Type())
		resultType := coroCallableEffectiveType(universe, owner, retag.Type())
		sourceTransport, err := coroCallableLeafTransport(universe, sourceType)
		if err != nil {
			return fmt.Errorf("source transport: %w", err)
		}
		resultTransport, err := coroCallableLeafTransport(universe, resultType)
		if err != nil {
			return fmt.Errorf("result transport: %w", err)
		}
		if sourceTransport != coro.ManagedTransport || resultTransport != coro.ManagedTransport {
			return fmt.Errorf(
				"retag crosses callable transport (%s -> %s)",
				sourceTransport, resultTransport,
			)
		}
		if !types.Identical(types.Unalias(sourceType).Underlying(), types.Unalias(resultType).Underlying()) {
			return fmt.Errorf("retag changes its effective function signature")
		}
		return nil
	}

	var source ssa.Value = change
	for {
		retag, ok := source.(*ssa.ChangeType)
		if !ok {
			break
		}
		if err := verifyRetag(retag); err != nil {
			return nil, err
		}
		source = retag.X
	}
	rawTarget, ok := source.(*ssa.Function)
	if !ok || rawTarget == nil {
		return nil, fmt.Errorf("retag source is not one exact SSA function")
	}
	target, ok := universe.Resolve(rawTarget)
	if !ok || target == nil {
		return nil, fmt.Errorf("retag source is absent from the frozen emission universe")
	}

	seen := make(map[ssa.Value]bool)
	staticCalls := 0
	var validateConsumers func(ssa.Value) error
	validateConsumers = func(value ssa.Value) error {
		if value == nil || seen[value] {
			return fmt.Errorf("retag consumer graph is cyclic or incomplete")
		}
		seen[value] = true
		refs := value.Referrers()
		if refs == nil || len(*refs) == 0 {
			return fmt.Errorf("retag has no executable consumer")
		}
		for _, ref := range *refs {
			if _, debug := ref.(*ssa.DebugRef); debug {
				continue
			}
			if next, ok := ref.(*ssa.ChangeType); ok && next.X == value {
				if err := verifyRetag(next); err != nil {
					return err
				}
				if err := validateConsumers(next); err != nil {
					return err
				}
				continue
			}
			call, ok := ref.(*ssa.Call)
			if !ok || call.Parent() != owner || call.Common() == nil || call.Common().Value != value {
				return fmt.Errorf("retag escapes to non-static-await consumer %T", ref)
			}
			resolved, _, _, err := resolveCoroStaticAwait(plan, caller, call, universe)
			if err != nil {
				return fmt.Errorf("retag call is not a closed static await: %w", err)
			}
			if resolved != target {
				return fmt.Errorf("retag call resolves to a different canonical target")
			}
			staticCalls++
		}
		return nil
	}
	if err := validateConsumers(change); err != nil {
		return nil, err
	}
	if staticCalls == 0 {
		return nil, fmt.Errorf("retag has no closed static-await consumer")
	}
	return target, nil
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
	if raw.Signature == nil {
		return fmt.Errorf("static coroutine method source operand has no signature")
	}
	flattenedLinkname := false
	if raw.Signature.Recv() == nil && universe != nil {
		if pair, ok := universe.goLinknameDefinitions[raw]; ok && pair.key != "" &&
			universe.canonicalAlias(pair.definition) == target &&
			universe.managedGoLinknameDefinitionHasKey(target, pair.key) {
			// Go source can only name an unexported method through a bodyless
			// receiver-first package function. The frozen managed-linkname pair
			// proves that this is the same physical callable; it is not a
			// general synthetic receiver adapter.
			flattenedLinkname = true
		}
	}
	rawNormalized := coroPhysicalNormalizeSourceSignature(raw.Signature)
	if (raw.Signature.Recv() == nil && !flattenedLinkname) ||
		rawNormalized.Params().Len() != len(raw.Params) || len(common.Args) != len(raw.Params) {
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
		if !flattenedLinkname && !coroInterfaceDispatchSignaturesIdentical(effectiveRaw, normalized, universe.emissionTypeKeys.strictABI) {
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
	callerCoroutine := caller.Emission == coro.EmitCoroutine &&
		caller.ManagedEntry == coro.ManagedEntryCoroutine
	callerOutcome := caller.Emission == coro.EmitOutcomePlain &&
		caller.ManagedEntry == coro.ManagedEntryOutcomePlain &&
		caller.AtomicCostProof.ProvesOutcomePlain() && caller.AtomicCost != 0
	if !callerCoroutine && !callerOutcome {
		return fmt.Errorf("caller emission is %s, want a structured physical entry", caller.Emission)
	}
	defined := target.External == coro.Defined &&
		target.Emission == coro.EmitCoroutine &&
		target.ManagedEntry == coro.ManagedEntryCoroutine &&
		target.Primary == coro.PrimaryCoroutine
	definedOutcome := target.External == coro.Defined &&
		target.Emission == coro.EmitOutcomePlain &&
		target.ManagedEntry == coro.ManagedEntryOutcomePlain &&
		target.Primary == coro.PrimaryCoroutine &&
		target.AtomicCostProof.ProvesOutcomePlain() && target.AtomicCost != 0
	imported := target.External == coro.ExternalKnown &&
		target.Emission == coro.EmitExternal &&
		target.Primary == coro.PrimaryExternal &&
		(target.ManagedEntry == coro.ManagedEntryCoroutine ||
			target.ManagedEntry == coro.ManagedEntryOutcomePlain)
	if !defined && !definedOutcome && !imported ||
		(target.FuncRep != coro.DirectCoro && target.FuncRep != coro.Dispatch) ||
		!target.Demand.Contains(coro.AsyncDemand) {
		return fmt.Errorf(
			"target %q has no defined outcome/coroutine or preflighted imported coroutine entry with async demand (external=%s emission=%s primary=%s representation=%s demand=%s)",
			target.ID, target.External, target.Emission, target.Primary, target.FuncRep, target.Demand,
		)
	}
	if callerOutcome && !target.HasStaticOutcome() {
		return fmt.Errorf(
			"outcome-plain caller %q targets a function without a static outcome capability",
			caller.ID,
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
	p.emitPCLineLabel(b, call.Pos())

	// Preserve Go's left-to-right argument evaluation before publishing any
	// child or parent scheduler state.
	args := p.compileValues(b, call.Call.Args, p.funcKind(call.Call.Value))
	source, _ := call.Call.Value.(*ssa.Function)
	args = p.compileManagedGoLinknameCallArguments(b, source, callee, args)
	var closureContext llssa.Expr
	if len(callee.FreeVars) != 0 {
		if p.emissionUniverse == nil {
			panic("coroutine child await requires frozen closure-environment facts")
		}
		_, hasEnvironment, err := p.emissionUniverse.closureEnvironments.entryEnvironment(callee)
		if err != nil {
			panic(err)
		}
		if hasEnvironment {
			if instructionPlan.controlClosure == nil {
				panic("coroutine child await lost its frozen captured closure carrier")
			}
			closureValue := p.compileValue(b, instructionPlan.controlClosure)
			closureContext = b.Field(closureValue, 1)
		}
	}
	keepaliveSlots := p.compileCoroCallKeepaliveSlots(b, call)
	result := p.compileCoroTargetAwaitWithContextAndRecoveryResult(
		b, callee, closureContext, args, nil, keepaliveSlots,
	)
	value, retagged := p.compileManagedGoLinknameCallResult(b, source, callee, result.value)
	if !retagged {
		p.recordCoroValueAddress(call, result.address)
	}
	return value
}

// tryCompileCoroDirectClosureValue constructs the environment carrier used by
// a closed captured coroutine call. The frozen DirectCoro call consumes only
// its environment word and invokes the selected physical entry itself; it must
// therefore retain the logical Go function type without pretending that the
// physical (g,out,env,args)->handle entry has an ordinary callable signature.
// Escaping or otherwise dynamic producers are intercepted earlier and use a
// coroutine dispatch descriptor instead.
func (p *context) tryCompileCoroDirectClosureValue(
	b llssa.Builder, closure *ssa.MakeClosure, target *ssa.Function, code llssa.Expr,
) (llssa.Expr, bool) {
	if p == nil || closure == nil || target == nil || p.rawPlainBody ||
		p.compilation == nil || p.immutablePlan() == nil || len(target.FreeVars) == 0 {
		return llssa.Expr{}, false
	}
	targetPlan, planned := p.immutablePlan().FunctionPlan(target)
	if !planned || targetPlan.Emission != coro.EmitCoroutine {
		return llssa.Expr{}, false
	}
	if p.emissionUniverse == nil {
		panic("captured coroutine closure requires a prepared emission universe")
	}
	if len(closure.Bindings) != len(target.FreeVars) {
		panic(fmt.Errorf(
			"captured coroutine closure %q has %d bindings for %d free variables",
			targetPlan.ID, len(closure.Bindings), len(target.FreeVars),
		))
	}
	envParam, hasEnv, err := p.emissionUniverse.closureEnvironments.entryEnvironment(target)
	if err != nil {
		panic(fmt.Errorf("captured coroutine closure %q: %w", targetPlan.ID, err))
	}
	var env llssa.Expr
	if hasEnv {
		bindings := p.compileValues(b, closure.Bindings, 0)
		env = b.MakeClosureEnvironment(p.prog.Type(envParam.Type(), llssa.InGo), bindings)
	} else if !p.canElideZeroSizedClosureEnv(target) {
		panic(fmt.Errorf("captured coroutine closure %q has no physical environment", targetPlan.ID))
	}
	return b.MakeClosureValue(p.type_(closure.Type(), llssa.InGo), code, env), true
}

type coroAwaitedValue struct {
	value   llssa.Expr
	address llssa.Expr
}

func (p *context) recordCoroValueAddress(value ssa.Value, address llssa.Expr) {
	if value == nil || address.IsNil() {
		return
	}
	if p.coroValueAddrs == nil {
		panic("coroutine value address escaped its active function")
	}
	if _, duplicate := p.coroValueAddrs[value]; duplicate {
		panic("coroutine value address was recorded more than once")
	}
	p.coroValueAddrs[value] = address
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
	b llssa.Builder, callee *ssa.Function, args []llssa.Expr,
) llssa.Expr {
	body := p.coroBody()
	if body == nil || body.cleanup == nil {
		panic("coroutine cleanup await requires the active static cleanup drainer")
	}
	return p.compileCoroTargetAwaitWithContextAndRecovery(b, callee, llssa.Nil, args, body.cleanup, nil)
}

func (p *context) compileCoroTargetAwaitWithContextAndRecovery(
	b llssa.Builder, callee *ssa.Function, closureContext llssa.Expr, args []llssa.Expr,
	cleanup *coroStaticCleanupState, keepaliveSlots []llssa.Expr,
) llssa.Expr {
	return p.compileCoroTargetAwaitWithContextAndRecoveryResult(
		b, callee, closureContext, args, cleanup, keepaliveSlots,
	).value
}

func (p *context) compileCoroTargetAwaitWithContextAndRecoveryResult(
	b llssa.Builder, callee *ssa.Function, closureContext llssa.Expr, args []llssa.Expr,
	cleanup *coroStaticCleanupState, keepaliveSlots []llssa.Expr,
) coroAwaitedValue {
	return p.compileCoroTargetEntryAwaitWithContextAndRecoveryResult(
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
	return p.compileCoroTargetEntryAwaitWithContextAndRecoveryResult(
		b, entry, closureContext, args, cleanup, keepaliveSlots,
	).value
}

func (p *context) compileCoroTargetEntryAwaitWithContextAndRecoveryResult(
	b llssa.Builder, entry plannedFunctionSymbol, closureContext llssa.Expr, args []llssa.Expr,
	cleanup *coroStaticCleanupState, keepaliveSlots []llssa.Expr,
) coroAwaitedValue {
	callee := entry.function
	body := p.coroBody()
	if body == nil || p.compilation == nil || p.immutablePlan() == nil {
		panic("coroutine child await requires an active physical coroutine body")
	}
	if b.Func != p.fn {
		panic("coroutine child await builder does not belong to the active physical coroutine function")
	}
	callerPlan, ok := p.immutablePlan().FunctionPlan(p.goFn)
	if !ok {
		panic("coroutine child await: current function has no compilation plan")
	}
	targetPlan, ok := p.immutablePlan().FunctionPlan(callee)
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
	entrySig, err := p.emissionUniverse.coroPhysicalEntrySourceSignature(callee)
	if err != nil {
		panic(fmt.Sprintf("coroutine child await: derive target %q entry ABI: %v", entry.plan.ID, err))
	}
	hasEnv := entrySig.Params().Len() == sourceSig.Params().Len()+1
	physicalArgs := args
	if hasEnv {
		if closureContext.IsNil() {
			panic(fmt.Sprintf("coroutine child await: environment-bearing target %q has no exact closure context", entry.plan.ID))
		}
		sourceSig = entrySig
		physicalArgs = make([]llssa.Expr, 0, len(args)+1)
		physicalArgs = append(physicalArgs, closureContext)
		physicalArgs = append(physicalArgs, args...)
	} else if !closureContext.IsNil() {
		panic(fmt.Sprintf("coroutine child await: context-free target %q received a closure context", entry.plan.ID))
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
	resultSlot := p.coroResultSlot(resultType)
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
	// This edge names one exact managed ramp and the child cannot outlive the
	// caller's coroutine frame: every terminal or suspended path is reconciled
	// by awaitCoroChildWithRecovery below. LLVM 22 may therefore embed the child
	// frame in its static parent; dynamic/function-value calls never reach this
	// proof point.
	b.MarkCoroElideSafe(child)
	value := p.awaitCoroChildWithRecovery(
		b, child, resultSlot, sourceSig.Results(), cleanup, keepaliveSlots,
	)
	return coroAwaitedValue{
		value:   value,
		address: p.coroAwaitResultAddress(b, resultSlot, sourceSig.Results()),
	}
}

func (p *context) coroAwaitResultAddress(
	b llssa.Builder, resultSlot llssa.Expr, results *types.Tuple,
) llssa.Expr {
	if b == nil || resultSlot.IsNil() || results == nil {
		return llssa.Nil
	}
	switch results.Len() {
	case 0:
		return llssa.Nil
	case 1:
		return b.FieldAddr(resultSlot, 0)
	default:
		return resultSlot
	}
}

// compileCoroPatchInitAwait lowers the compiler-inserted call from a patch
// package initializer to the original package initializer. Both source
// signatures are func(), but their managed entries are physical coroutines;
// this edge therefore uses the same scheduler-owned child transaction as an
// ordinary static synchronous-style call.
func (p *context) compileCoroPatchInitAwait(b llssa.Builder) {
	if !p.hasStructuredOutcomePhysicalBody() || b == nil || b.Func != p.fn {
		panic("coroutine patch initializer call requires an active structured body")
	}
	if p.emissionUniverse == nil || p.immutablePlan() == nil || p.goFn == nil {
		panic("coroutine patch initializer await requires a frozen exact plan")
	}
	original, frozen, err := p.emissionUniverse.ResolveCoroLoweredCall(p.goFn, coroPatchOriginalInitCall)
	if err != nil {
		panic(fmt.Errorf("coroutine patch initializer edge: %w", err))
	}
	planned, exact := p.immutablePlan().ResolveLoweredCall(p.goFn, coroPatchOriginalInitCall)
	if !frozen || original == nil || !exact || planned != original {
		panic("coroutine patch initializer edge disagrees between the frozen emission universe and SSA plan")
	}
	if p.hasOutcomePlainPhysicalBody() {
		entry, err := p.resolvePatchOriginalInitOutcomeSymbol(original)
		if err != nil {
			panic(err)
		}
		if err := entry.checkOutcomePlainSupported(); err != nil {
			panic(err)
		}
		calleeFn, _, kind := p.compileFuncDeclVariantEntry(p.pkg, entry, false)
		if kind != goFunc {
			panic("patch original initializer outcome target did not resolve to a Go entry")
		}
		completion := p.structuredOutcomeAlloca(outcomePlainCompletionType(p.prog), true)
		resultType := p.prog.Type(newOutcomePlainPhysicalABI(original.Signature).resultSlotType, llssa.InGo)
		resultSlot := p.structuredOutcomeAlloca(resultType, false)
		b.Call(calleeFn.Expr,
			p.managedPhysicalTask(),
			b.Convert(p.prog.VoidPtr(), resultSlot),
			b.Convert(p.prog.VoidPtr(), completion),
		)
		p.dispatchOutcomePlainCompletion(b, completion)
		return
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

// structuredOutcomeAlloca emits function-lifetime storage for either managed
// physical body.  A real coroutine must define the slot in its ramp entry so
// CoroSplit can retain it across suspension.  An outcome-plain body is a normal
// synchronous function, so the same proven-local storage belongs on its native
// entry stack and remains available to LLVM's ordinary SROA/mem2reg pipeline.
func (p *context) structuredOutcomeAlloca(typ llssa.Type, zeroed bool) llssa.Expr {
	if !p.hasStructuredOutcomePhysicalBody() || p.fn == nil || typ == nil {
		panic("structured outcome alloca requires an active physical body and type")
	}
	if p.hasCoroPhysicalBody() {
		if zeroed {
			return p.coroFrameAlloc(typ)
		}
		return p.coroFrameAlloca(typ)
	}
	entry := p.fn.Block(0)
	alloc := p.fn.NewBuilder()
	defer alloc.Dispose()
	alloc.SetBlockEx(entry, llssa.AtStart, true)
	if zeroed {
		return alloc.AllocaZeroedT(typ)
	}
	return alloc.AllocaT(typ)
}

func (p *context) coroFrameByteAlloca(b llssa.Builder, size int64) llssa.Expr {
	if size < 0 {
		panic("coroutine byte alloca requires a non-negative constant size")
	}
	storageType := p.type_(types.NewArray(types.Typ[types.Uint8], size), llssa.InGo)
	return b.Convert(p.prog.VoidPtr(), p.coroFrameAlloca(storageType))
}

// coroResultSlot emits the child-owned result sink in the physical ramp. Small
// results remain inline CoroSplit frame fields. A target-sized value above the
// native-stack limit instead uses the separately planned managed-slot helper,
// so the coroutine frame retains one pointer rather than the whole aggregate.
func (p *context) coroResultSlot(typ llssa.Type) llssa.Expr {
	if !p.hasCoroPhysicalBody() || p.fn == nil || typ == nil {
		panic("coroutine result slot requires an active physical body and type")
	}
	entry := p.fn.Block(0)
	alloc := p.fn.NewBuilder()
	defer alloc.Dispose()
	alloc.SetBlockEx(entry, llssa.AtStart, true)
	if p.prog.LocalAllocExceedsNativeStack(typ) {
		return alloc.AllocZAs(typ, coroManagedFrameSlotAllocZCall)
	}
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
	return alloc.AllocaZeroedT(typ)
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
	line := p.coroCurrentSourceLine()
	body.suspendForChild(b, line)

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
	completedInline := p.emitCoroStaticInlineAwait(b, child)
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
	body.suspendCoroCurrentBlockIf(
		b.UnOp(token.NOT, completedInline),
		nil,
		func(gate llssa.Builder, normal llssa.BasicBlock) {
			body.dispatchZeroRunDecisionTo(gate, normal, canceled)
		},
	)
	body.activate(b)
	cancelBuilder := p.fn.NewBuilder()
	cancelBuilder.DICopyCurrentDebugLocation(b)
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
		goexitedCancel := p.fn.MakeBlock()
		drainCancel := p.fn.MakeBlock()
		invalidCancel := p.fn.MakeBlock()
		cancelDispatch := cancelBuilder.Switch(cancelStatus, invalidCancel)
		cancelDispatch.Case(p.prog.IntVal(coroAwaitCompletionReturn, p.prog.Uint32()), returnedCancel)
		cancelDispatch.Case(p.prog.IntVal(coroAwaitCompletionPanic, p.prog.Uint32()), panickedCancel)
		cancelDispatch.Case(p.prog.IntVal(coroAwaitCompletionAbort, p.prog.Uint32()), abortedCancel)
		cancelDispatch.Case(p.prog.IntVal(coroAwaitCompletionShutdown, p.prog.Uint32()), shutdownCancel)
		cancelDispatch.Case(p.prog.IntVal(coroAwaitCompletionGoexit, p.prog.Uint32()), goexitedCancel)
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
		ownerCleanup.setPanicOverlay(
			cancelBuilder,
			cancelBuilder.Load(typeWord),
			cancelBuilder.Load(dataWord),
			line,
		)
		cancelBuilder.Jump(drainCancel)
		cancelBuilder.SetBlockEx(abortedCancel, llssa.AtEnd, false)
		cancelBuilder.Jump(drainCancel)
		cancelBuilder.SetBlockEx(shutdownCancel, llssa.AtEnd, false)
		cancelBuilder.Jump(drainCancel)
		cancelBuilder.SetBlockEx(goexitedCancel, llssa.AtEnd, false)
		body.enterGoexit(cancelBuilder)
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
	goexited := p.fn.MakeBlock()
	invalid := p.fn.MakeBlock()
	dispatch := b.Switch(status, invalid)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionReturn, p.prog.Uint32()), returned)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionPanic, p.prog.Uint32()), panicked)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionAbort, p.prog.Uint32()), aborted)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionShutdown, p.prog.Uint32()), shutdown)
	dispatch.Case(p.prog.IntVal(coroAwaitCompletionGoexit, p.prog.Uint32()), goexited)
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
		body.panic(b, b.Load(typeWord), b.Load(dataWord), line)
	} else if cleanup != nil {
		cleanup.replacePanic(b, b.Load(typeWord), b.Load(dataWord), line)
	} else {
		body.cleanup.enterPanic(b, b.Load(typeWord), b.Load(dataWord), line)
	}

	b.SetBlockEx(aborted, llssa.AtEnd, false)
	body.enterCancellation(b, coroAwaitCompletionAbort)
	b.SetBlockEx(shutdown, llssa.AtEnd, false)
	body.enterCancellation(b, coroAwaitCompletionShutdown)
	b.SetBlockEx(goexited, llssa.AtEnd, false)
	body.enterGoexit(b)

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

// emitCoroStaticInlineAwait keeps LLVM's handle operations in the exact static
// caller while the runtime owns only scheduler-state transitions. Besides
// removing one opaque runtime round trip, this is the shape required for LLVM
// 22 to select a coro_elide_safe no-allocation ramp and embed the child frame
// in its parent. A declined/deep or genuinely suspended child converges on the
// existing false result and parent suspend path.
func (p *context) emitCoroStaticInlineAwait(b llssa.Builder, child llssa.Expr) llssa.Expr {
	body := p.coroBody()
	if body == nil || b == nil || b.Func != p.fn || child.IsNil() ||
		body.abi.awaitInlineHook == "" || body.abi.awaitInlineFinishHook == "" ||
		body.abi.awaitInlineCommitHook == "" || body.abi.frameDestroyCommitHook == "" {
		panic("coroutine static inline await has an incomplete physical contract")
	}
	begin := p.pkg.NewFunc(body.abi.awaitInlineHook, coroAwaitInlineSignature(), llssa.InC)
	finish := p.pkg.NewFunc(
		body.abi.awaitInlineFinishHook, coroAwaitInlineFinishSignature(), llssa.InC,
	)
	frameCommit := p.pkg.NewFunc(
		body.abi.frameDestroyCommitHook, coroFrameDestroyCommitSignature(), llssa.InC,
	)
	inlineCommit := p.pkg.NewFunc(
		body.abi.awaitInlineCommitHook, coroAwaitInlineCommitSignature(), llssa.InC,
	)

	started, declined, destroy, joined := p.fn.MakeBlock(), p.fn.MakeBlock(), p.fn.MakeBlock(), p.fn.MakeBlock()
	parent := body.coro.Handle()
	b.If(b.Call(begin.Expr, body.task, parent, child), started, declined)

	b.SetBlockEx(started, llssa.AtEnd, false)
	b.CoroResume(child)
	done := b.CoroDone(child)
	b.If(b.Call(finish.Expr, body.task, parent, child, done), destroy, declined)

	b.SetBlockEx(declined, llssa.AtEnd, false)
	b.Jump(joined)
	b.SetBlockEx(destroy, llssa.AtEnd, false)
	b.CoroDestroy(child)
	b.Call(frameCommit.Expr, body.task, child)
	b.Call(inlineCommit.Expr, body.task, parent, child)
	b.Jump(joined)

	b.SetBlockContinuation(joined)
	completed := b.Phi(p.prog.Bool())
	completed.AddIncoming(b, []llssa.BasicBlock{declined, destroy}, func(index int, _ llssa.BasicBlock) llssa.Expr {
		return p.prog.BoolVal(index == 1)
	})
	return completed.Expr
}

// enterCoroPropagatedPanic and enterCoroPropagatedGoexit are the narrow parent
// capabilities shared by scheduler-owned child completion and synchronous
// outcome-plain completion. Call-site lowerers do not inspect the complete
// coroutine body or duplicate cleanup ownership decisions.
func (p *context) enterCoroPropagatedPanic(
	b llssa.Builder,
	typeWord, dataWord llssa.Expr,
	line uint32,
) {
	if outcome := p.outcomePlainBody(); outcome != nil {
		if b == nil || b.Func != p.fn || typeWord.IsNil() || dataWord.IsNil() {
			panic("propagated panic escaped its parent outcome-plain body")
		}
		outcome.publish(b, coroAwaitCompletionPanic, typeWord, dataWord)
		return
	}
	body := p.activeCoroEmissionBody()
	if body == nil || b == nil || b.Func != p.fn || typeWord.IsNil() || dataWord.IsNil() {
		panic("propagated panic escaped its parent coroutine body")
	}
	if body.cleanup == nil {
		body.panic(b, typeWord, dataWord, line)
		return
	}
	body.cleanup.enterPanic(b, typeWord, dataWord, line)
}

func (p *context) enterCoroPropagatedGoexit(b llssa.Builder) {
	if outcome := p.outcomePlainBody(); outcome != nil {
		if b == nil || b.Func != p.fn {
			panic("propagated Goexit escaped its parent outcome-plain body")
		}
		outcome.publish(b, coroAwaitCompletionGoexit, llssa.Nil, llssa.Nil)
		return
	}
	body := p.activeCoroEmissionBody()
	if body == nil || b == nil || b.Func != p.fn {
		panic("propagated Goexit escaped its parent coroutine body")
	}
	body.enterGoexit(b)
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
		// resultSlot is compiler-created coroutine-frame storage. The child
		// transaction cannot publish Return until that slot is valid, so the
		// continuation may load it as known non-nil. This is observable for
		// zero-sized results, where an ordinary Load would otherwise invent a
		// source nil-dereference helper at the call instruction.
		return b.LoadKnownNonNil(b.FieldAddr(resultSlot, 0))
	default:
		fields := make([]llssa.Expr, results.Len())
		for i := range fields {
			fields[i] = b.LoadKnownNonNil(b.FieldAddr(resultSlot, i))
		}
		return b.Aggregate(p.prog.Type(results, llssa.InGo), fields...)
	}
}
