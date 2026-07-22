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

// coroClosedInterfacePlainPlan is a compilation-scoped proof that selected
// ordinary Go interface invokes and runtime ABI method-table references keep
// LLGo's existing receiver-aware raw method ABI. These uses are not first-class
// Go function values, so the certificate neither creates a function-value
// descriptor nor adds another scheduler/event path.
//
// A CHA candidate receives FuncRep=Dispatch because it is dynamically
// reachable. That does not mean the concrete method body is ever materialized
// as a first-class function value. In particular, an invoke in an EmitNone
// body can select Dispatch globally even though it has no physical ABI
// consumer. targets records exactly the methods for which every emitted
// consumer preserves that distinction.
type coroClosedInterfacePlainPlan struct {
	calls   map[ssa.CallInstruction]struct{}
	targets map[coro.FunctionID]*ssa.Function
}

func (p *coroClosedInterfacePlainPlan) acceptsCall(call ssa.CallInstruction) bool {
	if p == nil || call == nil {
		return false
	}
	_, ok := p.calls[call]
	return ok
}

func (p *coroClosedInterfacePlainPlan) acceptsTarget(fn *ssa.Function, plan coro.FunctionPlan) bool {
	if p == nil || fn == nil {
		return false
	}
	target, ok := p.targets[plan.ID]
	return ok && target == fn
}

// resolveMethodToken keeps a closed async method's itab discriminator on the
// exact physical entry. The word is compared but never called through the
// legacy method ABI, so wrapping it with closureWrapDecl would manufacture an
// invalid source-signature call to a (g,out,receiver,args...) coroutine entry.
func (p *context) resolveMethodToken(
	resolvedName string, method *types.Func, signature *types.Signature,
) (llssa.Expr, bool) {
	if p == nil || p.compilation == nil || p.compilation.coroClosedInterfacePlain == nil ||
		method == nil || signature == nil {
		return llssa.Nil, false
	}
	target := p.resolveInterfaceMethodSSA(method, signature)
	entry := p.mustFunctionSymbol(target)
	if entry.plan.Emission != coro.EmitCoroutine || resolvedName != entry.name ||
		!p.compilation.coroClosedInterfacePlain.acceptsTarget(entry.function, entry.plan) {
		return llssa.Nil, false
	}
	fn, _, kind := p.funcOfEntry(entry)
	if fn == nil || kind != goFunc {
		panic(fmt.Errorf("coroutine method token target %q did not resolve to one physical Go entry", entry.plan.ID))
	}
	return fn.Expr, true
}

// analyzeCoroClosedInterfacePlainPlan freezes the code-generation proof once,
// before any package can materialize a body. It deliberately derives every
// fact from exact SSA objects and immutable CallPlan/ValuePlan records.
func analyzeCoroClosedInterfacePlainPlan(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	explicitStatusPanic, interfaceAwait bool,
	managedPlans ...*coroManagedInterfaceDispatchPlan,
) (*coroClosedInterfacePlainPlan, error) {
	if plan == nil {
		return nil, fmt.Errorf("closed interface plain island requires a compilation plan")
	}
	result := &coroClosedInterfacePlainPlan{
		calls:   make(map[ssa.CallInstruction]struct{}),
		targets: make(map[coro.FunctionID]*ssa.Function),
	}
	var managed *coroManagedInterfaceDispatchPlan
	if len(managedPlans) != 0 {
		managed = managedPlans[0]
	}
	// Restricted CHA may mark a receiver method Dispatch because of an
	// unreachable interface consumer. A live type descriptor can independently
	// demand that same method's raw ifn/tfn address. Freeze those exact live raw
	// references before scanning SSA consumers so they are not mistaken for
	// descriptor-backed Go function values.
	for _, owner := range plan.Functions() {
		if owner.Function == nil || (owner.Plan.Emission != coro.EmitPlain && owner.Plan.Emission != coro.EmitCoroutine) {
			continue
		}
		references, err := universe.CoroDemandReferences(owner.Function)
		if err != nil {
			return nil, err
		}
		synchronous, err := universe.CoroSyncDemandReferences(owner.Function)
		if err != nil {
			return nil, err
		}
		syncTargets := make(map[*ssa.Function]struct{}, len(synchronous))
		for _, target := range synchronous {
			syncTargets[target] = struct{}{}
		}
		for _, target := range references {
			targetPlan, ok := plan.FunctionPlan(target)
			if !ok {
				return nil, fmt.Errorf("raw ABI method target %q has no compilation plan", target)
			}
			_, rawSyncTarget := syncTargets[target]
			asyncMethodToken := !rawSyncTarget && target.Signature != nil && target.Signature.Recv() != nil && targetPlan.Emission == coro.EmitCoroutine
			if rawSyncTarget {
				if err := validateCoroRawABIEntryTarget(target, targetPlan); err != nil {
					return nil, err
				}
			} else if asyncMethodToken {
				if err := validateCoroRawABIMethodTokenTarget(target, targetPlan); err != nil {
					return nil, err
				}
			} else if err := validateCoroRawABIPlainTarget(target, targetPlan); err != nil {
				return nil, err
			}
			if asyncMethodToken || targetPlan.FuncRep == coro.Dispatch {
				result.targets[targetPlan.ID] = target
			}
		}
	}
	// Function representation is selected before graph demand has removed dead
	// bodies. Consequently a dormant interface invoke can be the sole reason a
	// live, statically-called receiver method has FuncRep=Dispatch. Freeze only
	// the exact demanded method candidates of EmitNone invokes here. This grants
	// no invoke or descriptor capability: the scan below still rejects any live
	// first-class value or non-interface dynamic consumer of the same method.
	if err := freezeCoroDormantInterfaceDispatchTargets(plan, universe, result); err != nil {
		return nil, err
	}
	firstClassUse := make(map[coro.FunctionID]string)
	dynamicUse := make(map[coro.FunctionID]string)
	seenValues := make(map[ssa.Value]struct{})

	recordValue := func(owner *ssa.Function, value ssa.Value) {
		if value == nil {
			return
		}
		if _, seen := seenValues[value]; seen {
			return
		}
		seenValues[value] = struct{}{}
		valuePlan, ok := plan.ValuePlan(value)
		if !ok {
			return
		}
		for _, leaf := range valuePlan.Funcs {
			for _, id := range leaf.Targets {
				if _, exists := firstClassUse[id]; !exists {
					firstClassUse[id] = fmt.Sprintf("function %q materializes target through first-class value %q", owner.Name(), value.Name())
				}
			}
		}
	}

	for _, owner := range plan.Functions() {
		if owner.Function == nil || (owner.Plan.Emission != coro.EmitPlain && owner.Plan.Emission != coro.EmitCoroutine) {
			continue
		}
		fn := owner.Function
		for _, param := range fn.Params {
			recordValue(fn, param)
		}
		for _, free := range fn.FreeVars {
			recordValue(fn, free)
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				var exactStaticCallee ssa.Value
				if call, ok := instruction.(ssa.CallInstruction); ok && call.Common() != nil && call.Common().StaticCallee() != nil {
					exactStaticCallee = call.Common().Value
				}
				if value, ok := instruction.(ssa.Value); ok {
					recordValue(fn, value)
				}
				for _, operand := range instruction.Operands(nil) {
					if operand != nil && *operand != exactStaticCallee {
						recordValue(fn, *operand)
					}
				}

				call, isCall := instruction.(ssa.CallInstruction)
				if !isCall || plan.ElidesCall(call) || call.Common() == nil {
					continue
				}
				common := call.Common()
				if _, builtin := common.Value.(*ssa.Builtin); builtin {
					continue
				}
				callPlan, found := plan.CallPlan(call)
				if !found {
					if owner.Plan.Emission == coro.EmitCoroutine && common.IsInvoke() {
						return nil, coroLeafInstructionError(fn, owner.Plan, instruction, "interface invoke has no compilation CallPlan")
					}
					continue
				}

				if common.IsInvoke() {
					if managed.acceptsCall(call) {
						continue
					}
					if callPlan.Open && callPlan.Unresolved == coro.UnknownManagedInterfaceDispatch {
						if !interfaceAwait || owner.Plan.Emission != coro.EmitCoroutine {
							return nil, coroLeafInstructionError(fn, owner.Plan, instruction,
								"managed interface descriptor requires coroutine child-await lowering")
						}
						if err := validateCoroManagedInterfaceDispatchCall(plan, universe, fn, call, callPlan); err != nil {
							return nil, err
						}
						continue
					}
					targets, err := resolveCoroClosedInterfacePlainCall(plan, call)
					if err == nil {
						if explicitStatusPanic {
							return nil, coroLeafInstructionError(fn, owner.Plan, instruction, "closed interface plain invoke requires the legacy panic ABI")
						}
						result.calls[call] = struct{}{}
						for _, target := range targets {
							result.targets[target.plan.ID] = target.function
						}
						continue
					}
					if interfaceAwait && owner.Plan.Emission == coro.EmitCoroutine {
						if direct, ok := call.(*ssa.Call); ok {
							if dispatch, awaitErr := resolveCoroInterfaceDispatchPlan(plan, universe, direct); awaitErr == nil && coroInterfaceDispatchNeedsAwait(dispatch) {
								for _, candidate := range dispatch.candidates {
									result.targets[candidate.id] = candidate.function
								}
								continue
							} else {
								err = fmt.Errorf("plain island: %v; coroutine dispatch: %v", err, awaitErr)
							}
						}
					}
					if owner.Plan.Emission == coro.EmitCoroutine {
						return nil, coroLeafInstructionError(fn, owner.Plan, instruction, "unsupported interface invoke: "+err.Error())
					}
					for _, id := range callPlan.Targets {
						if _, exists := dynamicUse[id]; !exists {
							dynamicUse[id] = fmt.Sprintf("function %q has another unverified interface invoke", fn.Name())
						}
					}
					continue
				}

				// Exact static calls consume the method body entry directly and do
				// not require a receiver-aware function-value descriptor. Every
				// other CallPlan target is a second dynamic consumer.
				if common.StaticCallee() == nil {
					for _, id := range callPlan.Targets {
						if _, exists := dynamicUse[id]; !exists {
							dynamicUse[id] = fmt.Sprintf("function %q has another dynamic call consumer", fn.Name())
						}
					}
				}
			}
		}
	}

	for _, function := range plan.Functions() {
		id := function.Plan.ID
		target, accepted := result.targets[id]
		if !accepted {
			continue
		}
		if reason := firstClassUse[id]; reason != "" {
			return nil, fmt.Errorf("raw/interface plain target %q also has a function-value consumer: %s", id, reason)
		}
		if reason := dynamicUse[id]; reason != "" {
			return nil, fmt.Errorf("raw/interface plain target %q also has a dynamic consumer: %s", id, reason)
		}
		targetPlan, ok := plan.FunctionPlan(target)
		if !ok || targetPlan.ID != id {
			return nil, fmt.Errorf("closed interface plain target %q lost its exact function plan", id)
		}
	}
	return result, nil
}

func freezeCoroDormantInterfaceDispatchTargets(
	plan *coro.SSAPlan,
	universe *EmissionUniverse,
	result *coroClosedInterfacePlainPlan,
) error {
	if plan == nil || universe == nil || result == nil {
		return fmt.Errorf("dormant interface dispatch requires an exact plan, emission universe, and receiver plan")
	}
	for _, owner := range plan.Functions() {
		if owner.Function == nil || owner.Plan.Emission != coro.EmitNone {
			continue
		}
		for _, block := range owner.Function.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || plan.ElidesCall(call) || call.Common() == nil || !call.Common().IsInvoke() {
					continue
				}
				callPlan, found := plan.CallPlan(call)
				if !found || callPlan.Call != call || callPlan.Kind != coro.CallDirect || callPlan.Rep != coro.Dispatch {
					continue
				}
				common := call.Common()
				if _, ok := types.Unalias(common.Value.Type()).Underlying().(*types.Interface); !ok {
					return coroLeafInstructionError(owner.Function, owner.Plan, instruction,
						fmt.Sprintf("dormant interface receiver %s is not an interface", common.Value.Type()))
				}
				for _, targetID := range callPlan.Targets {
					target, found := plan.Function(targetID)
					if !found || target == nil {
						return coroLeafInstructionError(owner.Function, owner.Plan, instruction,
							fmt.Sprintf("dormant interface target %q is absent from the compilation plan", targetID))
					}
					targetPlan, found := plan.FunctionPlan(target)
					if !found || targetPlan.ID != targetID {
						return coroLeafInstructionError(owner.Function, owner.Plan, instruction,
							fmt.Sprintf("dormant interface target %q has no exact function plan", targetID))
					}
					// An undemanded target has no physical entry to validate or
					// certify. Its dormant CallPlan remains useful only as analysis
					// metadata and cannot affect code generation.
					if targetPlan.Emission == coro.EmitNone {
						continue
					}
					// The dormant invoke has no physical call ABI, so its patched
					// source signature need not match another package's effective
					// method signature. It certifies only why representation analysis
					// selected Dispatch. The actual emitted uses are proven below to
					// be static/raw/receiver-aware, and the ordinary entry validator
					// remains authoritative for the selected body.
					if targetPlan.External != coro.Defined || targetPlan.FuncRep != coro.Dispatch ||
						target.Signature == nil || target.Signature.Recv() == nil || len(target.Blocks) == 0 ||
						target.Parent() != nil || len(target.FreeVars) != 0 {
						return coroLeafInstructionError(owner.Function, owner.Plan, instruction,
							fmt.Sprintf("dormant interface target %q is not one exact emitted receiver-only Dispatch body", targetID))
					}
					if previous := result.targets[targetID]; previous != nil && previous != target {
						return coroLeafInstructionError(owner.Function, owner.Plan, instruction,
							fmt.Sprintf("dormant interface target %q resolves to both %q and %q", targetID, previous.Name(), target.Name()))
					}
					result.targets[targetID] = target
				}
			}
		}
	}
	return nil
}

// validateCoroRawABIEntryTarget validates the physical entry selected by one
// exact CoroSyncDemandReferences use. The historical strict single-plain-body
// validator remains unchanged. A coroutine managed primary is accepted only
// through the separately planned RawPlainEntry capability and its independent
// legacy symbol/body validation.
func validateCoroRawABIEntryTarget(target *ssa.Function, plan coro.FunctionPlan) error {
	switch plan.Emission {
	case coro.EmitPlain, coro.EmitExternal:
		return validateCoroRawABIPlainTarget(target, plan)
	case coro.EmitCoroutine, coro.EmitRawPlain:
		return validatePlannedRawPlainEntry(target, plan)
	default:
		return fmt.Errorf("raw ABI function target %q (%s): unsupported emission %s", target, plan.ID, plan.Emission)
	}
}

func validateCoroRawABIPlainTarget(target *ssa.Function, plan coro.FunctionPlan) error {
	fail := func(format string, args ...any) error {
		name := "<nil>"
		if target != nil {
			name = target.String()
		}
		return fmt.Errorf("raw ABI function target %q (%s): %s", name, plan.ID, fmt.Sprintf(format, args...))
	}
	if target == nil || target.Signature == nil || len(target.FreeVars) != 0 {
		return fail("requires one non-capturing raw ABI function")
	}
	receiver := target.Signature.Recv()
	externalMethodEntry := receiver != nil && plan.External != coro.Defined
	if externalMethodEntry {
		// Runtime type data embeds a receiver method's raw symbol address but does
		// not call it while constructing the descriptor.  C/assembly method
		// entries therefore need no synthetic Go body here.  A real interface
		// invoke is validated separately by validateCoroClosedInterfacePlainCandidate
		// (or the coroutine interface dispatcher), so this does not authorize a
		// blocking foreign call on a scheduler thread.  Receiver-less equality and
		// hash callbacks remain on the strict owned-body path below.
		if plan.Emission != coro.EmitExternal || plan.Primary != coro.PrimaryExternal ||
			plan.Demand == coro.NoDemand || plan.Effect != coro.NoSuspend || plan.Effect.IsOpaque() {
			return fail(
				"requires a demanded external no-suspend method entry, got external=%s emission=%s primary=%s demand=%s effect=%s",
				plan.External, plan.Emission, plan.Primary, plan.Demand, plan.Effect,
			)
		}
	} else {
		if len(target.Blocks) == 0 || plan.External != coro.Defined || plan.Emission != coro.EmitPlain || plan.Primary != coro.PrimaryPlain ||
			plan.Demand == coro.NoDemand || plan.Effect != coro.NoSuspend || plan.Effect.IsOpaque() {
			return fail(
				"requires a demanded defined no-suspend plain body, got external=%s emission=%s primary=%s demand=%s effect=%s",
				plan.External, plan.Emission, plan.Primary, plan.Demand, plan.Effect,
			)
		}
		if plan.Exec&(coro.ThreadAffine|coro.NeedsPreempt) != 0 || plan.Exec.IsOpaque() {
			return fail("execution constraints %s require a coroutine adapter", plan.Exec)
		}
	}
	parameterBase := 0
	if receiver != nil {
		parameterBase = 1
	}
	if len(target.Params) != target.Signature.Params().Len()+parameterBase ||
		(receiver != nil && !types.Identical(target.Params[0].Type(), receiver.Type())) {
		return fail("SSA body has no exact raw ABI parameter shape (receiver=%v, SSA params=%d, declared params=%d)",
			receiver, len(target.Params), target.Signature.Params().Len())
	}
	for index := 0; index < target.Signature.Params().Len(); index++ {
		if !types.Identical(target.Params[index+parameterBase].Type(), target.Signature.Params().At(index).Type()) {
			return fail("SSA parameter %d does not match declared parameter %d", index+parameterBase, index)
		}
	}
	if plan.FuncRep != coro.DirectPlain && plan.FuncRep != coro.Dispatch {
		return fail("representation %s has no raw plain method entry", plan.FuncRep)
	}
	return nil
}

// validateCoroRawABIMethodTokenTarget accepts the one non-callable use of an
// async receiver method's ordinary itab word. In a closed coroutine invoke the
// word is only a stable discriminator: codegen compares it with the exact
// method symbol, then invokes the planned coroutine primary through structured
// child-await. It must never be called with the legacy raw method signature.
//
// Receiver-less equality/hash callbacks are deliberately excluded because the
// runtime calls those words directly. A first-class or otherwise unverified
// consumer is rejected later by analyzeCoroClosedInterfacePlainPlan.
func validateCoroRawABIMethodTokenTarget(target *ssa.Function, plan coro.FunctionPlan) error {
	fail := func(format string, args ...any) error {
		name := "<nil>"
		if target != nil {
			name = target.String()
		}
		return fmt.Errorf("raw ABI coroutine method token %q (%s): %s", name, plan.ID, fmt.Sprintf(format, args...))
	}
	if target == nil || target.Signature == nil || target.Signature.Recv() == nil ||
		len(target.Blocks) == 0 || len(target.FreeVars) != 0 {
		return fail("requires one defined non-capturing receiver body")
	}
	if plan.External != coro.Defined || plan.Emission != coro.EmitCoroutine || plan.Primary != coro.PrimaryCoroutine ||
		plan.Demand == coro.NoDemand || !plan.Effect.MaySuspend() || plan.Effect.IsOpaque() ||
		(plan.FuncRep != coro.DirectCoro && plan.FuncRep != coro.Dispatch) {
		return fail(
			"requires a demanded defined non-opaque coroutine body, got external=%s emission=%s primary=%s demand=%s representation=%s effect=%s",
			plan.External, plan.Emission, plan.Primary, plan.Demand, plan.FuncRep, plan.Effect,
		)
	}
	if plan.Exec&(coro.BlockForeign|coro.ThreadAffine) != 0 || plan.Exec.IsOpaque() {
		return fail("execution constraints %s have no closed coroutine method adapter", plan.Exec)
	}
	receiver := target.Signature.Recv()
	if len(target.Params) != target.Signature.Params().Len()+1 ||
		!types.Identical(target.Params[0].Type(), receiver.Type()) {
		return fail("SSA body has no exact raw ABI receiver shape (receiver=%v, SSA params=%d, declared params=%d)",
			receiver, len(target.Params), target.Signature.Params().Len())
	}
	for index := 0; index < target.Signature.Params().Len(); index++ {
		if !types.Identical(target.Params[index+1].Type(), target.Signature.Params().At(index).Type()) {
			return fail("SSA parameter %d does not match declared parameter %d", index+1, index)
		}
	}
	return nil
}

type coroClosedInterfacePlainTarget struct {
	function *ssa.Function
	plan     coro.FunctionPlan
}

// resolveCoroClosedInterfacePlainCall proves one exact ordinary itab invoke.
// Multiple concrete methods are allowed because the existing Go interface ABI
// performs that dispatch; all candidates must nevertheless be bounded plain
// bodies so the current physical frame cannot suspend through the call.
func resolveCoroClosedInterfacePlainCall(plan *coro.SSAPlan, call ssa.CallInstruction) ([]coroClosedInterfacePlainTarget, error) {
	if plan == nil || call == nil || call.Common() == nil {
		return nil, fmt.Errorf("requires an exact call and compilation CallPlan")
	}
	direct, ordinary := call.(*ssa.Call)
	common := call.Common()
	if !ordinary || direct == nil || !common.IsInvoke() || common.StaticCallee() != nil || common.Method == nil {
		return nil, fmt.Errorf("requires an ordinary interface invoke")
	}
	iface, ok := types.Unalias(common.Value.Type()).Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("invoke receiver type %s is not an interface", common.Value.Type())
	}
	iface.Complete()
	callPlan, ok := plan.CallPlan(call)
	if !ok || callPlan.Call != call {
		return nil, fmt.Errorf("invoke has no exact compilation CallPlan")
	}
	if callPlan.Kind != coro.CallDirect || callPlan.Rep != coro.Dispatch || callPlan.Open || len(callPlan.Targets) == 0 {
		return nil, fmt.Errorf(
			"requires a closed nonempty Dispatch CallPlan, got kind=%v representation=%s open=%t may-be-nil=%t targets=%d",
			callPlan.Kind, callPlan.Rep, callPlan.Open, callPlan.MayBeNil, len(callPlan.Targets),
		)
	}
	targets := make([]coroClosedInterfacePlainTarget, 0, len(callPlan.Targets))
	seen := make(map[coro.FunctionID]struct{}, len(callPlan.Targets))
	for _, id := range callPlan.Targets {
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("invoke repeats target ID %q", id)
		}
		seen[id] = struct{}{}
		target, found := plan.Function(id)
		if !found || target == nil {
			return nil, fmt.Errorf("invoke target %q is absent from the compilation plan", id)
		}
		targetPlan, found := plan.FunctionPlan(target)
		if !found || targetPlan.ID != id {
			return nil, fmt.Errorf("invoke target %q has no exact function plan", id)
		}
		if err := validateCoroClosedInterfacePlainCandidate(common, iface, id, target, targetPlan); err != nil {
			return nil, err
		}
		targets = append(targets, coroClosedInterfacePlainTarget{function: target, plan: targetPlan})
	}
	return targets, nil
}

func validateCoroClosedInterfacePlainCandidate(common *ssa.CallCommon, iface *types.Interface, id coro.FunctionID, target *ssa.Function, plan coro.FunctionPlan) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("invoke target %q: %s", id, fmt.Sprintf(format, args...))
	}
	if common == nil || iface == nil || common.Method == nil || target == nil || target.Signature == nil {
		return fail("missing method, receiver interface, or target signature")
	}
	if plan.ID != id {
		return fail("function plan ID is %q", plan.ID)
	}
	if plan.External != coro.Defined || plan.Emission != coro.EmitPlain || plan.Primary != coro.PrimaryPlain || plan.FuncRep != coro.Dispatch || plan.Demand == coro.NoDemand {
		return fail(
			"requires a demanded defined plain Dispatch body, got external=%s emission=%s primary=%s representation=%s demand=%s",
			plan.External, plan.Emission, plan.Primary, plan.FuncRep, plan.Demand,
		)
	}
	if plan.Effect != coro.NoSuspend || plan.Effect.IsOpaque() {
		return fail("effect %s is not exact no-suspend", plan.Effect)
	}
	if plan.Exec&(coro.ThreadAffine|coro.NeedsPreempt) != 0 || plan.Exec.IsOpaque() {
		return fail("execution constraints %s require preemption or open lowering", plan.Exec)
	}
	if len(target.Blocks) == 0 || len(target.FreeVars) != 0 {
		return fail("requires one owned non-capturing SSA body")
	}
	recv := target.Signature.Recv()
	if recv == nil {
		return fail("candidate is not a declared method")
	}
	method, ok := target.Object().(*types.Func)
	if !ok || method == nil {
		return fail("candidate has no exact method object")
	}
	if method.Id() != common.Method.Id() {
		return fail("method ID %q does not match invoke method ID %q", method.Id(), common.Method.Id())
	}
	if !types.Implements(recv.Type(), iface) {
		return fail("receiver %s does not implement invoke interface %s", recv.Type(), iface)
	}
	selected, _, _ := types.LookupFieldOrMethod(recv.Type(), false, common.Method.Pkg(), common.Method.Name())
	selectedMethod, ok := selected.(*types.Func)
	if !ok || selectedMethod == nil || selectedMethod.Id() != method.Id() {
		return fail("receiver method selection does not resolve exact method ID %q", method.Id())
	}
	callSignature := coroClosedInterfacePlainCallableSignature(common.Signature())
	targetSignature := coroClosedInterfacePlainCallableSignature(target.Signature)
	if callSignature == nil || targetSignature == nil || !types.Identical(callSignature, targetSignature) {
		return fail("call signature %v does not match receiver-free target signature %v", callSignature, targetSignature)
	}
	if len(target.Params) != target.Signature.Params().Len()+1 || !types.Identical(target.Params[0].Type(), recv.Type()) {
		return fail("SSA parameters do not contain the exact declared receiver")
	}
	for index := 0; index < target.Signature.Params().Len(); index++ {
		if !types.Identical(target.Params[index+1].Type(), target.Signature.Params().At(index).Type()) {
			return fail("SSA parameter %d does not match declared method parameter %d", index+1, index)
		}
	}
	return nil
}

func coroClosedInterfacePlainCallableSignature(sig *types.Signature) *types.Signature {
	if sig == nil {
		return nil
	}
	return types.NewSignatureType(nil, nil, nil, sig.Params(), sig.Results(), sig.Variadic())
}
