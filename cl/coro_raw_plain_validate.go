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

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// validateCoroRawPlainConsumers proves every call edge that is compiled a
// second time inside a dedicated legacy-stack body. The managed-body consumer
// verifier cannot cover these entries: EmitRawPlain has no managed body, and
// an EmitCoroutine raw variant resolves static/lowered calls differently from
// its managed primary.
//
// Local descriptor construction uses the same capability-complete immutable
// descriptor as a managed body. The producer validator below proves its exact
// ValuePlan and target; codegen then publishes a managed primary and, when the
// plan has one, the separately validated raw/plain twin. This permits a raw
// runtime island to enqueue a closure for later managed execution without
// reinterpreting either entry ABI.
func validateCoroRawPlainConsumers(plan *coro.SSAPlan, universe *EmissionUniverse, plainDispatch bool) error {
	if plan == nil || universe == nil {
		return fmt.Errorf("coroutine raw plain consumer validation requires a compilation plan and emission universe")
	}
	for _, function := range plan.Functions() {
		fn, functionPlan := function.Function, function.Plan
		if !plan.HasRawPlainVariant(fn) ||
			(functionPlan.Emission != coro.EmitRawPlain && functionPlan.Emission != coro.EmitCoroutine) {
			continue
		}

		for _, lowered := range plan.LoweredCalls(fn) {
			target, frozen, err := universe.ResolveCoroLoweredCall(fn, lowered.LogicalName)
			if err != nil {
				return fmt.Errorf("coroutine raw plain ABI: function %q lowered call %q: %w", functionPlan.ID, lowered.LogicalName, err)
			}
			if !frozen || target == nil || target != lowered.Target {
				return fmt.Errorf(
					"coroutine raw plain ABI: function %q lowered call %q disagrees between the frozen emission universe and SSA plan",
					functionPlan.ID, lowered.LogicalName,
				)
			}
			if err := validateCoroRawPlainCallTarget(plan, target); err != nil {
				return fmt.Errorf("coroutine raw plain ABI: function %q lowered call %q: %w", functionPlan.ID, lowered.LogicalName, err)
			}
		}

		for _, block := range fn.Blocks {
			for _, instruction := range block.Instrs {
				if _, debug := instruction.(*ssa.DebugRef); debug {
					// DebugRef is source-position metadata only. Its operands
					// are never emitted and therefore cannot publish a managed
					// descriptor from a raw/plain body.
					continue
				}
				if err := validateCoroRawPlainLocalDescriptorProducer(plan, universe, fn, instruction); err != nil {
					return err
				}
				call, isCall := instruction.(ssa.CallInstruction)
				if !isCall {
					continue
				}
				if direct, ok := call.(*ssa.Call); ok {
					_, critical, err := universe.coroCriticalCallSite(direct)
					if err != nil {
						return coroLeafInstructionError(fn, functionPlan, instruction, "invalid critical marker: "+err.Error())
					}
					if critical {
						return coroLeafInstructionError(fn, functionPlan, instruction,
							"managed critical intrinsic is invalid in a raw/plain body")
					}
				}
				if plan.ElidesCall(call) {
					continue
				}
				common := call.Common()
				if common == nil {
					return coroLeafInstructionError(fn, functionPlan, instruction, "raw plain call has no CallCommon")
				}
				if _, builtin := common.Value.(*ssa.Builtin); builtin {
					continue
				}
				callPlan, planned := plan.CallPlan(call)
				if !planned {
					return coroLeafInstructionError(fn, functionPlan, instruction, "raw plain call has no compilation CallPlan")
				}
				if callPlan.RawPlain {
					direct, ordinary := call.(*ssa.Call)
					if !ordinary {
						return coroLeafInstructionError(fn, functionPlan, instruction,
							"nested raw/plain invocation is not an ordinary call")
					}
					if _, _, err := validateCoroRawPlainSourceCall(plan, universe.Resolve, direct); err != nil {
						return coroLeafInstructionError(fn, functionPlan, instruction,
							"invalid nested raw/plain invocation: "+err.Error())
					}
					continue
				}
				if _, spawn := call.(*ssa.Go); spawn || callPlan.Kind == coro.CallSpawn {
					return coroLeafInstructionError(fn, functionPlan, instruction, "raw plain body cannot spawn a goroutine")
				}

				static := common.StaticCallee()
				if static == nil || common.IsInvoke() || common.Method != nil {
					if callPlan.Transport == coro.RawCCodePointer {
						if _, ordinary := call.(*ssa.Call); !ordinary || common.IsInvoke() || common.Method != nil ||
							callPlan.Kind != coro.CallForeign || callPlan.Rep != coro.DirectPlain || !callPlan.Open ||
							callPlan.Unresolved != coro.UnknownForeign || callPlan.SyncDispatch {
							return coroLeafInstructionError(fn, functionPlan, instruction,
								"raw plain body has a malformed raw C code-pointer call")
						}
						if err := validateCoroCallableTransportValue(plan, fn, common.Value, universe); err != nil {
							return coroLeafInstructionError(fn, functionPlan, instruction,
								"raw C code-pointer callee: "+err.Error())
						}
						if callPlan.MayBeNil && !ssaFunctionValueProvenNonNilAt(common.Value, call) {
							return coroLeafInstructionError(fn, functionPlan, instruction,
								"nullable raw C code-pointer call has no dominating non-nil proof")
						}
						continue
					}
					if !plainDispatch {
						return coroLeafInstructionError(fn, functionPlan, instruction, "raw plain synchronous descriptor call requires the v1 plain dispatch capability")
					}
					if !callPlan.SyncDispatch {
						return coroLeafInstructionError(fn, functionPlan, instruction, "raw plain body has a dynamic call without an exact SyncDispatch certificate")
					}
					if err := validateCoroPlainDispatchCall(plan, fn, call, callPlan, universe); err != nil {
						return coroLeafInstructionError(fn, functionPlan, instruction, "invalid raw plain SyncDispatch call: "+err.Error())
					}
					continue
				}

				canonical, ok := universe.Resolve(static)
				if !ok || canonical == nil {
					return coroLeafInstructionError(fn, functionPlan, instruction, fmt.Sprintf(
						"raw plain static callee %q is outside the frozen emission universe", static.Name(),
					))
				}
				if callPlan.Open || len(callPlan.Targets) != 1 {
					return coroLeafInstructionError(fn, functionPlan, instruction, "raw plain static call does not have one exact closed target")
				}
				target, found := plan.Function(callPlan.Targets[0])
				if !found || target == nil || target != canonical {
					return coroLeafInstructionError(fn, functionPlan, instruction, fmt.Sprintf(
						"raw plain static call target disagrees with its frozen CallPlan target %q", callPlan.Targets[0],
					))
				}
				if err := validateCoroRawPlainCallTarget(plan, target); err != nil {
					return coroLeafInstructionError(fn, functionPlan, instruction, err.Error())
				}
			}
		}
	}
	return nil
}

func validateCoroRawPlainCallTarget(plan *coro.SSAPlan, target *ssa.Function) error {
	if plan == nil || target == nil {
		return fmt.Errorf("raw plain call has no exact target")
	}
	targetPlan, planned := plan.FunctionPlan(target)
	if !planned {
		return fmt.Errorf("raw plain call target %q has no function plan", target.Name())
	}
	switch targetPlan.Emission {
	case coro.EmitPlain:
		if targetPlan.External != coro.Defined || targetPlan.Primary != coro.PrimaryPlain || targetPlan.Effect.MaySuspend() {
			return fmt.Errorf(
				"raw plain call target %q has an invalid plain entry (external=%s effect=%s primary=%s)",
				targetPlan.ID, targetPlan.External, targetPlan.Effect, targetPlan.Primary,
			)
		}
		return nil
	case coro.EmitExternal:
		if targetPlan.External == coro.Defined || targetPlan.FuncRep == coro.DirectCoro {
			return fmt.Errorf(
				"raw plain call target %q has an invalid external entry (external=%s representation=%s)",
				targetPlan.ID, targetPlan.External, targetPlan.FuncRep,
			)
		}
		return nil
	case coro.EmitRawPlain, coro.EmitCoroutine:
		if err := validatePlannedRawPlainVariant(target, targetPlan, plan.HasRawPlainVariant(target)); err != nil {
			return fmt.Errorf("raw plain call target %q has no valid raw entry: %w", targetPlan.ID, err)
		}
		return nil
	case coro.EmitNone:
		return fmt.Errorf("raw plain call target %q is not emitted", targetPlan.ID)
	default:
		return fmt.Errorf("raw plain call target %q has invalid emission %d", targetPlan.ID, uint8(targetPlan.Emission))
	}
}

func validateCoroRawPlainLocalDescriptorProducer(plan *coro.SSAPlan, universe *EmissionUniverse, owner *ssa.Function, instruction ssa.Instruction) error {
	if plan == nil || owner == nil || instruction == nil {
		return nil
	}
	if box, ok := instruction.(*ssa.MakeInterface); ok && coroCompilerElidedFunctionAddressBox(plan, universe, owner, box) {
		return nil
	}
	if closure, ok := instruction.(*ssa.MakeClosure); ok {
		dispatch, err := coroValueIsScalarManagedDispatch(plan, closure)
		if err != nil {
			return coroPlainDispatchInstructionError(owner, instruction, err.Error())
		}
		if dispatch {
			target, exact := closure.Fn.(*ssa.Function)
			if !exact || target == nil || len(closure.Bindings) != len(target.FreeVars) {
				return coroPlainDispatchInstructionError(owner, instruction,
					"raw plain descriptor closure has no exact target and binding shape")
			}
			targetPlan, planned := plan.FunctionPlan(target)
			if !planned {
				return coroPlainDispatchInstructionError(owner, instruction,
					fmt.Sprintf("raw plain descriptor closure target %q has no function plan", target.Name()))
			}
			if err := validateCoroDynamicDispatchTarget(target, targetPlan, universe); err != nil {
				return coroPlainDispatchInstructionError(owner, instruction,
					"raw plain descriptor closure target: "+err.Error())
			}
		}
	}
	call, _ := instruction.(ssa.CallInstruction)
	var staticValue ssa.Value
	if call != nil && call.Common() != nil && call.Common().StaticCallee() != nil {
		staticValue = call.Common().Value
	}
	for _, operand := range instruction.Operands(nil) {
		if operand == nil || *operand == nil || *operand == staticValue {
			continue
		}
		function, ok := (*operand).(*ssa.Function)
		if !ok {
			continue
		}
		dispatch, err := coroValueIsScalarManagedDispatch(plan, function)
		if err != nil {
			return coroPlainDispatchInstructionError(owner, instruction, err.Error())
		}
		if !dispatch {
			continue
		}
		targetPlan, planned := plan.FunctionPlan(function)
		if !planned {
			return coroPlainDispatchInstructionError(owner, instruction,
				fmt.Sprintf("raw plain descriptor value target %q has no function plan", function.Name()))
		}
		if err := validateCoroDynamicDispatchTarget(function, targetPlan, universe); err != nil {
			return coroPlainDispatchInstructionError(owner, instruction,
				"raw plain descriptor value target: "+err.Error())
		}
	}
	return nil
}

// coroCompilerElidedFunctionAddressBox mirrors the frontend recipe used by
// funcPCABI0/funcAddr: x/tools inserts MakeInterface, but code generation
// inspects its exact static function operand and emits only a code address.
// This is not descriptor construction and grants no worker-call capability.
func coroCompilerElidedFunctionAddressBox(plan *coro.SSAPlan, universe *EmissionUniverse, owner *ssa.Function, box *ssa.MakeInterface) bool {
	if plan == nil || universe == nil || owner == nil || box == nil || box.Parent() != owner {
		return false
	}
	refs := box.Referrers()
	if refs == nil || len(*refs) != 1 {
		return false
	}
	direct, ok := (*refs)[0].(*ssa.Call)
	if !ok || direct.Parent() != owner || direct.Common() == nil || len(direct.Common().Args) != 1 ||
		direct.Common().Args[0] != box || !plan.ElidesCall(direct) {
		return false
	}
	if plan.StaticCodeAddressArgument(direct, 0) {
		target, exact := coroFuncPCABI0ExactStaticOperand(direct)
		return exact && target == box.X && universe.validateCoroFuncPCABI0CallSite(direct) == nil
	}
	if !plan.RawFunctionAddressArgument(direct, 0) {
		return false
	}
	validatedBox, target, err := universe.validateCoroFuncAddrCallSite(direct)
	return err == nil && validatedBox == box && target == box.X
}

func coroValueIsScalarManagedDispatch(plan *coro.SSAPlan, value ssa.Value) (bool, error) {
	if plan == nil || value == nil {
		return false, nil
	}
	valuePlan, found := plan.ValuePlan(value)
	if !found || len(valuePlan.Funcs) != 1 || len(valuePlan.Funcs[0].Path) != 0 || valuePlan.Funcs[0].Rep != coro.Dispatch {
		return false, nil
	}
	if valuePlan.Funcs[0].Transport != coro.ManagedTransport {
		return false, fmt.Errorf(
			"value %q has Dispatch representation with non-managed transport %s",
			value.Name(), valuePlan.Funcs[0].Transport,
		)
	}
	return true, nil
}
