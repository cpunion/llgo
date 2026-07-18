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
	"golang.org/x/tools/go/ssa"
)

// coroClosedInterfacePlainPlan is a compilation-scoped proof that selected
// ordinary Go interface invokes remain synchronous plain islands inside a
// physical coroutine. The invoke itself keeps LLGo's existing itab ABI: this
// certificate neither creates a function-value descriptor nor adds another
// scheduler/event path.
//
// A CHA candidate receives FuncRep=Dispatch because it is dynamically
// reachable. That does not mean the concrete method body is ever materialized
// as a first-class function value. targets records exactly the methods for
// which every emitted consumer preserves that distinction.
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

// analyzeCoroClosedInterfacePlainPlan freezes the code-generation proof once,
// before any package can materialize a body. It deliberately derives every
// fact from exact SSA objects and immutable CallPlan/ValuePlan records.
func analyzeCoroClosedInterfacePlainPlan(plan *coro.SSAPlan, explicitStatusPanic bool) (*coroClosedInterfacePlainPlan, error) {
	if plan == nil {
		return nil, fmt.Errorf("closed interface plain island requires a compilation plan")
	}
	result := &coroClosedInterfacePlainPlan{
		calls:   make(map[ssa.CallInstruction]struct{}),
		targets: make(map[coro.FunctionID]*ssa.Function),
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
				if value, ok := instruction.(ssa.Value); ok {
					recordValue(fn, value)
				}
				for _, operand := range instruction.Operands(nil) {
					if operand != nil {
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
			return nil, fmt.Errorf("closed interface plain target %q also has a function-value consumer: %s", id, reason)
		}
		if reason := dynamicUse[id]; reason != "" {
			return nil, fmt.Errorf("closed interface plain target %q also has a dynamic consumer: %s", id, reason)
		}
		targetPlan, ok := plan.FunctionPlan(target)
		if !ok || targetPlan.ID != id {
			return nil, fmt.Errorf("closed interface plain target %q lost its exact function plan", id)
		}
	}
	return result, nil
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
	if plan.Exec.Contains(coro.NeedsPreempt) || plan.Exec.IsOpaque() {
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
