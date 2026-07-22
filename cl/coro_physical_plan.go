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
	"golang.org/x/tools/go/ssa"
)

// coroPhysicalInstructionRecipe is the closed code-generation choice for one
// source SSA instruction. Ordinary means that physical preflight accepted the
// legacy value recipe without a coroutine-specific fault branch. Every other
// value is selected and observed through the physical SitePlan; codegen may
// not rediscover it from SSA, target types, or frame-retention maps.
type coroPhysicalInstructionRecipe uint8

const (
	coroPhysicalInstructionOrdinary coroPhysicalInstructionRecipe = iota
	coroPhysicalInstructionFieldAddr
	coroPhysicalInstructionDeref
	coroPhysicalInstructionIndexAddr
	coroPhysicalInstructionIndex
	coroPhysicalInstructionSlice
	coroPhysicalInstructionSliceToArrayPointer
	coroPhysicalInstructionBuiltinNilGuard
)

func (recipe coroPhysicalInstructionRecipe) String() string {
	switch recipe {
	case coroPhysicalInstructionOrdinary:
		return "ordinary"
	case coroPhysicalInstructionFieldAddr:
		return "fieldaddr"
	case coroPhysicalInstructionDeref:
		return "deref"
	case coroPhysicalInstructionIndexAddr:
		return "indexaddr"
	case coroPhysicalInstructionIndex:
		return "index"
	case coroPhysicalInstructionSlice:
		return "slice"
	case coroPhysicalInstructionSliceToArrayPointer:
		return "slice-to-array-pointer"
	case coroPhysicalInstructionBuiltinNilGuard:
		return "builtin-nil-guard"
	default:
		return fmt.Sprintf("physical-recipe(%d)", uint8(recipe))
	}
}

type coroPhysicalContainerKind uint8

const (
	coroPhysicalContainerNone coroPhysicalContainerKind = iota
	coroPhysicalContainerString
	coroPhysicalContainerArray
	coroPhysicalContainerSlice
	coroPhysicalContainerArrayPointer
)

// coroPhysicalControlRecipe is the frozen coroutine control operation selected
// for one source instruction. It is deliberately orthogonal to the value/fault
// recipe above: an awaited call can still carry an implicit guard plan, while
// ordinary calls and values keep the zero control recipe.
type coroPhysicalControlRecipe uint8

const (
	coroPhysicalControlNone coroPhysicalControlRecipe = iota
	coroPhysicalControlDirectAwait
	coroPhysicalControlDispatchAwait
	coroPhysicalControlClosedInterfaceAwait
	coroPhysicalControlManagedInterfaceAwait
	coroPhysicalControlPlainDispatch
	coroPhysicalControlDirectSpawn
	coroPhysicalControlDispatchSpawn
)

func (recipe coroPhysicalControlRecipe) String() string {
	switch recipe {
	case coroPhysicalControlNone:
		return "none"
	case coroPhysicalControlDirectAwait:
		return "direct-await"
	case coroPhysicalControlDispatchAwait:
		return "dispatch-await"
	case coroPhysicalControlClosedInterfaceAwait:
		return "closed-interface-await"
	case coroPhysicalControlManagedInterfaceAwait:
		return "managed-interface-await"
	case coroPhysicalControlPlainDispatch:
		return "plain-dispatch"
	case coroPhysicalControlDirectSpawn:
		return "direct-spawn"
	case coroPhysicalControlDispatchSpawn:
		return "dispatch-spawn"
	default:
		return fmt.Sprintf("physical-control-recipe(%d)", uint8(recipe))
	}
}

type coroPhysicalLoweringCapabilities struct {
	childAwait       bool
	staticSpawn      bool
	managedDispatch  bool
	explicitPanic    bool
	interfacePlain   *coroClosedInterfacePlainPlan
	managedInterface *coroManagedInterfaceDispatchPlan
}

// coroPhysicalInstructionPlan contains only information that changes emitted
// control flow. container and bound make the guarded Index/Slice recipes
// target-independent; nilGuard and boundsGuard are independent because a
// frozen safe array index removes only the range edge, never a nullable
// pointer-to-array dereference.
type coroPhysicalInstructionPlan struct {
	semantic           coroSemanticInstructionPlan
	recipe             coroPhysicalInstructionRecipe
	control            coroPhysicalControlRecipe
	controlTarget      *ssa.Function
	controlTargetID    coro.FunctionID
	controlInterface   *coroInterfaceDispatchPlan
	controlSignature   *types.Signature
	controlFailure     string
	controlFailureHard bool
	container          coroPhysicalContainerKind
	bound              int64
	nilGuard           bool
	boundsGuard        bool
}

func (plan coroPhysicalInstructionPlan) mayFault() bool {
	return plan.nilGuard || plan.boundsGuard ||
		plan.recipe == coroPhysicalInstructionFieldAddr ||
		plan.recipe == coroPhysicalInstructionDeref ||
		plan.recipe == coroPhysicalInstructionBuiltinNilGuard
}

// coroPhysicalFunctionPlan is the post-analysis physical projection of one
// exact function emission. It owns every proof and per-instruction choice used
// after preflight. The pointed-to proofs are built to completion before this
// object is frozen and are read-only thereafter.
type coroPhysicalFunctionPlan struct {
	function       *ssa.Function
	owner          *preparedEmissionPackage
	frameRetention *coroFrameRetentionProof
	critical       *coroCriticalProof
	cleanup        *coroStaticCleanupPlan
	instructions   map[ssa.Instruction]coroPhysicalInstructionPlan
}

func prepareCoroPhysicalFunctionPlan(
	audit *coroPhysicalPureSSAAudit,
	owner *preparedEmissionPackage,
	whole *coro.SSAPlan,
	cleanup *coroStaticCleanupPlan,
	critical *coroCriticalProof,
	explicitPanic bool,
	capabilities coroPhysicalLoweringCapabilities,
) (*coroPhysicalFunctionPlan, error) {
	if audit == nil || audit.fn == nil {
		return nil, fmt.Errorf("physical function planning requires one exact pure-SSA audit")
	}
	plan := &coroPhysicalFunctionPlan{
		function:       audit.fn,
		owner:          owner,
		frameRetention: audit.currentFrameRetentionProof(),
		critical:       critical,
		cleanup:        cleanup,
		instructions:   make(map[ssa.Instruction]coroPhysicalInstructionPlan),
	}
	for _, block := range audit.fn.Blocks {
		for _, instruction := range block.Instrs {
			instructionPlan, err := planCoroPhysicalInstruction(audit, owner, whole, instruction, explicitPanic, capabilities)
			if err != nil {
				return nil, fmt.Errorf("block %d instruction %T: %w", block.Index, instruction, err)
			}
			plan.instructions[instruction] = instructionPlan
		}
	}
	return plan, nil
}

func (plan *coroPhysicalFunctionPlan) instructionPlan(instruction ssa.Instruction) (coroPhysicalInstructionPlan, error) {
	if plan == nil || plan.function == nil || plan.owner == nil || instruction == nil || instruction.Parent() != plan.function {
		return coroPhysicalInstructionPlan{}, fmt.Errorf("physical instruction plan requires one exact frozen function owner and source instruction")
	}
	physical, ok := plan.instructions[instruction]
	if !ok {
		return coroPhysicalInstructionPlan{}, fmt.Errorf("source instruction %q is absent from its frozen physical plan", instruction.String())
	}
	return physical, nil
}

type coroPhysicalPlanStage struct {
	plans map[emissionFunctionOwnerKey]*coroPhysicalFunctionPlan
}

func newCoroPhysicalPlanStage() *coroPhysicalPlanStage {
	return &coroPhysicalPlanStage{plans: make(map[emissionFunctionOwnerKey]*coroPhysicalFunctionPlan)}
}

func (stage *coroPhysicalPlanStage) freezePhysicalFunctionPlan(plan *coroPhysicalFunctionPlan) error {
	if stage == nil || plan == nil || plan.function == nil || plan.owner == nil || plan.instructions == nil {
		return fmt.Errorf("physical plan freeze requires one complete function-owner projection")
	}
	key := emissionFunctionOwnerKey{function: plan.function, owner: plan.owner}
	if _, exists := stage.plans[key]; exists {
		return fmt.Errorf("physical plan for function %q owner %q was frozen more than once", plan.function.Name(), plan.owner.identity)
	}
	for _, block := range plan.function.Blocks {
		for _, instruction := range block.Instrs {
			if _, ok := plan.instructions[instruction]; !ok {
				return fmt.Errorf("physical plan for function %q omitted source instruction %q", plan.function.Name(), instruction.String())
			}
		}
	}
	stage.plans[key] = plan
	return nil
}

func (ir *coroProgramIR) commitPhysicalFunctionPlans(stage *coroPhysicalPlanStage, expected map[emissionFunctionOwnerKey]none) error {
	if ir == nil || !ir.callsFrozen {
		return fmt.Errorf("physical plan commit requires the call SitePlan stage")
	}
	if ir.physicalPlansSealed {
		return fmt.Errorf("physical plans were committed more than once")
	}
	if stage == nil {
		return fmt.Errorf("physical plan commit requires one complete staging transaction")
	}
	if len(stage.plans) != len(expected) {
		return fmt.Errorf("physical plan stage has %d function owners, want %d", len(stage.plans), len(expected))
	}
	for key := range expected {
		if stage.plans[key] == nil {
			name, owner := "<nil>", "<nil>"
			if key.function != nil {
				name = key.function.Name()
			}
			if key.owner != nil {
				owner = key.owner.identity
			}
			return fmt.Errorf("physical plan stage omitted function %q owner %q", name, owner)
		}
	}
	for key := range stage.plans {
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("physical plan stage retained an unexpected function %q owner %q", key.function.Name(), key.owner.identity)
		}
	}
	committed := make(map[emissionFunctionOwnerKey]*coroPhysicalFunctionPlan, len(stage.plans))
	for key, plan := range stage.plans {
		committed[key] = plan
	}
	ir.physicalPlans = committed
	ir.physicalPlansSealed = true
	return nil
}

func (ir *coroProgramIR) physicalFunctionPlan(function *ssa.Function, owner *preparedEmissionPackage) (*coroPhysicalFunctionPlan, error) {
	if ir == nil || !ir.physicalPlansSealed {
		return nil, fmt.Errorf("coroutine physical plans are not sealed")
	}
	if function == nil || owner == nil {
		return nil, fmt.Errorf("physical plan lookup requires one exact function and owner")
	}
	plan := ir.physicalPlans[emissionFunctionOwnerKey{function: function, owner: owner}]
	if plan == nil {
		return nil, fmt.Errorf("function %q owner %q has no frozen physical plan", function.Name(), owner.identity)
	}
	return plan, nil
}

func planCoroPhysicalInstruction(
	audit *coroPhysicalPureSSAAudit,
	owner *preparedEmissionPackage,
	whole *coro.SSAPlan,
	instruction ssa.Instruction,
	explicitPanic bool,
	capabilities coroPhysicalLoweringCapabilities,
) (coroPhysicalInstructionPlan, error) {
	result := coroPhysicalInstructionPlan{recipe: coroPhysicalInstructionOrdinary}
	if audit == nil || instruction == nil || instruction.Parent() != audit.fn {
		return result, fmt.Errorf("physical instruction planning requires one exact audit and source instruction")
	}
	semantic, err := planCoroSemanticInstruction(instruction)
	if audit.universe != nil && audit.universe.coroProgramIR != nil && owner != nil {
		semantic, err = audit.universe.coroProgramIR.semanticInstructionPlan(audit.fn, owner, instruction)
	}
	if err != nil {
		return result, fmt.Errorf("load semantic instruction recipe: %w", err)
	}
	result.semantic = semantic
	planCoroPhysicalControlInstruction(audit, whole, instruction, capabilities, &result)
	switch instruction := instruction.(type) {
	case *ssa.FieldAddr:
		if audit.fieldAddrRequiresImplicitNilFault(instruction) {
			result.recipe = coroPhysicalInstructionFieldAddr
			result.nilGuard = true
		}
	case *ssa.UnOp:
		if instruction.Op == token.MUL && audit.derefRequiresImplicitNilFault(instruction) {
			result.recipe = coroPhysicalInstructionDeref
			result.nilGuard = true
		}
	case *ssa.IndexAddr:
		if audit.ctx != nil && emissionIsVargsAlloc(audit.ctx, instruction.X) {
			break
		}
		if !explicitPanic {
			break
		}
		result.recipe = coroPhysicalInstructionIndexAddr
		container, bound, err := coroPhysicalContainerPlan(audit, instruction.X)
		if err != nil {
			return result, err
		}
		if container != coroPhysicalContainerSlice && container != coroPhysicalContainerArrayPointer {
			return result, fmt.Errorf("IndexAddr has unsupported physical container %d", container)
		}
		result.container, result.bound = container, bound
		result.nilGuard = container == coroPhysicalContainerArrayPointer &&
			!emissionKnownNonNilArrayBase(instruction.X) && !ssaValueProvenNonNilAt(instruction.X, instruction)
		safe, err := coroPhysicalSafeFixedArrayIndex(audit, whole, instruction, instruction.X, instruction.Index)
		if err != nil {
			return result, err
		}
		result.boundsGuard = !safe
	case *ssa.Index:
		if !explicitPanic {
			break
		}
		result.recipe = coroPhysicalInstructionIndex
		container, bound, err := coroPhysicalContainerPlan(audit, instruction.X)
		if err != nil {
			return result, err
		}
		result.container, result.bound = container, bound
		result.nilGuard = container == coroPhysicalContainerArrayPointer &&
			!emissionKnownNonNilArrayBase(instruction.X) && !ssaValueProvenNonNilAt(instruction.X, instruction)
		safe, err := coroPhysicalSafeFixedArrayIndex(audit, whole, instruction, instruction.X, instruction.Index)
		if err != nil {
			return result, err
		}
		result.boundsGuard = !safe
	case *ssa.Slice:
		if audit.ctx != nil && emissionIsVargsAlloc(audit.ctx, instruction.X) {
			break
		}
		if !explicitPanic {
			break
		}
		result.recipe = coroPhysicalInstructionSlice
		container, bound, err := coroPhysicalContainerPlan(audit, instruction.X)
		if err != nil {
			return result, err
		}
		if container != coroPhysicalContainerString && container != coroPhysicalContainerSlice &&
			container != coroPhysicalContainerArrayPointer {
			return result, fmt.Errorf("Slice has unsupported physical container %d", container)
		}
		result.container, result.bound = container, bound
		result.nilGuard = container == coroPhysicalContainerArrayPointer &&
			!isKnownNonNilAddr(instruction.X) && !ssaValueProvenNonNilAt(instruction.X, instruction)
		result.boundsGuard = true
	case *ssa.SliceToArrayPointer:
		length, exact := coroSliceToArrayPointerLen(instruction, audit.typeOf)
		if !exact || length < 0 {
			return result, fmt.Errorf("slice-to-array-pointer has no exact physical length")
		}
		result.recipe = coroPhysicalInstructionSliceToArrayPointer
		result.bound = length
		result.boundsGuard = length != 0
	case *ssa.Call:
		if explicitPanic && isWrapNilCheckCall(instruction) {
			result.recipe = coroPhysicalInstructionBuiltinNilGuard
			result.nilGuard = true
		}
	}
	return result, nil
}

// planCoroPhysicalControlInstruction is the sole post-analysis selector for
// direct child-await and source goroutine-spawn control recipes. It records a
// non-hard await mismatch so the validator can still consider the established
// direct-plain path; once an await target was recognized, ABI failures are hard
// and cannot silently fall back. Every spawn mismatch is hard because a source
// Go instruction has no ordinary synchronous lowering.
func planCoroPhysicalControlInstruction(
	audit *coroPhysicalPureSSAAudit,
	whole *coro.SSAPlan,
	instruction ssa.Instruction,
	capabilities coroPhysicalLoweringCapabilities,
	result *coroPhysicalInstructionPlan,
) {
	if audit == nil || result == nil || instruction == nil || instruction.Parent() != audit.fn {
		panic("physical control planning requires one exact instruction plan")
	}
	switch instruction := instruction.(type) {
	case *ssa.Call:
		if !capabilities.childAwait || whole == nil {
			return
		}
		common := instruction.Common()
		callPlan, callPlanned := whole.CallPlan(instruction)
		if common != nil && common.IsInvoke() {
			result.controlFailureHard = true
			if capabilities.managedInterface.acceptsCall(instruction) {
				if !callPlanned || callPlan.Rep != coro.Dispatch {
					result.controlFailure = "managed interface descriptor call lost its frozen Dispatch CallPlan"
					return
				}
				if callPlan.Open {
					if err := validateCoroManagedInterfaceDispatchCall(
						whole, audit.universe, audit.fn, instruction, callPlan,
					); err != nil {
						result.controlFailure = "invalid managed interface await: " + err.Error()
						return
					}
				}
				signature, err := coroInterfaceDispatchSourceSignature(common)
				if err != nil {
					result.controlFailure = "managed interface await signature: " + err.Error()
					return
				}
				result.control = coroPhysicalControlManagedInterfaceAwait
				result.controlSignature = signature
				return
			}
			if !capabilities.explicitPanic {
				if _, err := resolveCoroClosedInterfacePlainCall(whole, instruction); err == nil {
					result.controlFailureHard = false
					return
				}
			}
			if capabilities.interfacePlain.acceptsCall(instruction) {
				result.controlFailureHard = false
				return
			}
			dispatch, err := resolveCoroInterfaceDispatchPlan(whole, audit.universe, instruction)
			if err != nil {
				result.controlFailure = "unsupported interface invoke: " + err.Error()
				return
			}
			if !coroInterfaceDispatchNeedsAwait(dispatch) {
				result.controlFailure = "unsupported interface invoke: closed interface dispatch has no coroutine target"
				return
			}
			result.control = coroPhysicalControlClosedInterfaceAwait
			result.controlInterface = dispatch
			return
		}
		if callPlanned && callPlan.Rep == coro.Dispatch && common != nil && common.StaticCallee() == nil {
			result.controlFailureHard = true
			if !capabilities.managedDispatch {
				result.controlFailure = "managed descriptor call requires the v1 descriptor dispatch capability"
				return
			}
			if callPlan.SyncDispatch {
				if err := validateCoroPlainDispatchCall(whole, audit.fn, instruction, callPlan, audit.universe); err != nil {
					result.controlFailure = "invalid synchronous descriptor call: " + err.Error()
					return
				}
				result.control = coroPhysicalControlPlainDispatch
				return
			}
			if err := validateCoroManagedDispatchCall(whole, audit.fn, instruction, callPlan, audit.universe); err != nil {
				result.controlFailure = "invalid managed descriptor await: " + err.Error()
				return
			}
			if err := validateCoroManagedDispatchAwaitShape(whole, audit.fn, instruction, callPlan); err != nil {
				result.controlFailure = "invalid managed descriptor await: " + err.Error()
				return
			}
			result.control = coroPhysicalControlDispatchAwait
			return
		}
		callerPlan, found := whole.FunctionPlan(audit.fn)
		if !found {
			result.controlFailure = "current function has no compilation plan"
			return
		}
		callee, targetPlan, err := resolveCoroStaticAwait(whole, callerPlan, instruction, audit.universe)
		if err != nil {
			result.controlFailure = err.Error()
			return
		}
		calleeSignature := coroPhysicalNormalizeSourceSignature(callee.Signature)
		if audit.universe != nil {
			calleeSignature, err = audit.universe.coroPhysicalSourceSignature(callee)
		}
		if err == nil {
			err = validateCoroLeafPhysicalSignature(targetPlan, calleeSignature)
		}
		if err != nil {
			result.controlFailure = "child await signature: " + err.Error()
			result.controlFailureHard = true
			return
		}
		result.control = coroPhysicalControlDirectAwait
		result.controlTarget = callee
		result.controlTargetID = targetPlan.ID
	case *ssa.Go:
		result.controlFailureHard = true
		if !capabilities.staticSpawn {
			result.controlFailure = "goroutine spawn requires the closed-static scheduler capability"
			return
		}
		if whole == nil {
			result.controlFailure = "goroutine spawn requires one compilation plan"
			return
		}
		callPlan, found := whole.CallPlan(instruction)
		if !found {
			result.controlFailure = "goroutine spawn has no compilation CallPlan"
			return
		}
		switch callPlan.Rep {
		case coro.DirectCoro:
			target, targetPlan, err := resolveCoroDirectStaticSpawn(whole, instruction, capabilities.managedDispatch)
			if err != nil {
				result.controlFailure = "unsupported closed static spawn: " + err.Error()
				return
			}
			targetSignature := coroPhysicalNormalizeSourceSignature(target.Signature)
			if audit.universe != nil {
				targetSignature, err = audit.universe.coroPhysicalSourceSignature(target)
			}
			if err == nil {
				err = validateCoroLeafPhysicalSignature(targetPlan, targetSignature)
			}
			if err != nil {
				result.controlFailure = "spawn target signature: " + err.Error()
				return
			}
			result.control = coroPhysicalControlDirectSpawn
			result.controlTarget = target
			result.controlTargetID = targetPlan.ID
		case coro.Dispatch:
			if !capabilities.managedDispatch {
				result.controlFailure = "managed descriptor spawn requires the v1 descriptor dispatch capability"
				return
			}
			if _, err := whole.ResolveManagedDispatchSpawn(instruction); err != nil {
				result.controlFailure = "unsupported managed descriptor spawn: " + err.Error()
				return
			}
			if err := validateCoroManagedDispatchSignatureShape(instruction.Common().Signature()); err != nil {
				result.controlFailure = "managed descriptor spawn signature: " + err.Error()
				return
			}
			result.control = coroPhysicalControlDispatchSpawn
		default:
			result.controlFailure = "goroutine spawn has unsupported representation " + callPlan.Rep.String()
		}
	}
}

func coroPhysicalContainerPlan(audit *coroPhysicalPureSSAAudit, value ssa.Value) (coroPhysicalContainerKind, int64, error) {
	if audit == nil || value == nil || value.Type() == nil {
		return coroPhysicalContainerNone, 0, fmt.Errorf("physical container has no exact type")
	}
	switch container := types.Unalias(audit.typeOf(value.Type())).Underlying().(type) {
	case *types.Basic:
		if coroPhysicalStringBasic(container) {
			return coroPhysicalContainerString, 0, nil
		}
	case *types.Array:
		return coroPhysicalContainerArray, container.Len(), nil
	case *types.Slice:
		return coroPhysicalContainerSlice, 0, nil
	case *types.Pointer:
		if array, ok := types.Unalias(container.Elem()).Underlying().(*types.Array); ok {
			return coroPhysicalContainerArrayPointer, array.Len(), nil
		}
	}
	return coroPhysicalContainerNone, 0, fmt.Errorf("unsupported physical container type %s", audit.typeOf(value.Type()))
}

func coroPhysicalSafeFixedArrayIndex(
	audit *coroPhysicalPureSSAAudit,
	whole *coro.SSAPlan,
	operation ssa.Instruction,
	collection, index ssa.Value,
) (bool, error) {
	if audit == nil || operation == nil || collection == nil || index == nil || operation.Parent() != audit.fn {
		return false, fmt.Errorf("safe fixed-array plan requires exact source operands")
	}
	if whole == nil {
		// Structural validators have no frozen optimization fact. Preserve the
		// checked recipe; active Compilation paths always provide the SSAPlan.
		return false, nil
	}
	bound, fixed := coroPhysicalFixedArrayBound(audit, collection)
	recomputed := fixed && coro.ProveSSAExactSafeFixedArrayIndex(operation.Parent(), index, bound, operation)
	plannedBound, planned := whole.ExactSafeFixedArrayIndex(operation)
	if planned != recomputed || planned && plannedBound != bound {
		return false, fmt.Errorf(
			"safe fixed-array index disagrees between CoroPlan and physical projection (planned=%t bound=%d recomputed=%t bound=%d)",
			planned, plannedBound, recomputed, bound,
		)
	}
	return planned, nil
}

func coroPhysicalFixedArrayBound(audit *coroPhysicalPureSSAAudit, collection ssa.Value) (int64, bool) {
	if audit == nil || collection == nil || collection.Type() == nil {
		return 0, false
	}
	switch container := types.Unalias(audit.typeOf(collection.Type())).Underlying().(type) {
	case *types.Array:
		return container.Len(), true
	case *types.Pointer:
		if array, ok := types.Unalias(container.Elem()).Underlying().(*types.Array); ok {
			return array.Len(), true
		}
	}
	return 0, false
}
