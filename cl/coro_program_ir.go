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
	"slices"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// coroProgramIR owns production SitePlan state. Later replacement cohorts add
// global summaries, physical control and storage projections to this same
// object rather than creating independently-versioned lowering documents.
type coroProgramIR struct {
	sitePlans           map[emissionFunctionOwnerKey]map[ssa.Instruction]coroEmissionSitePlan
	siteOwners          map[emissionFunctionOwnerKey]none
	semanticPlans       map[emissionFunctionOwnerKey]map[ssa.Instruction]coroSemanticInstructionPlan
	localBodyFacts      map[*ssa.Function]coro.SSAFunctionBodyFacts
	callPlans           map[ssa.CallInstruction]coroFrozenCallSitePlan
	callsFrozen         bool
	physicalPlans       map[emissionFunctionOwnerKey]*coroPhysicalFunctionPlan
	physicalPlansSealed bool
}

func newCoroProgramIR() *coroProgramIR {
	return &coroProgramIR{
		sitePlans:      make(map[emissionFunctionOwnerKey]map[ssa.Instruction]coroEmissionSitePlan),
		siteOwners:     make(map[emissionFunctionOwnerKey]none),
		semanticPlans:  make(map[emissionFunctionOwnerKey]map[ssa.Instruction]coroSemanticInstructionPlan),
		localBodyFacts: make(map[*ssa.Function]coro.SSAFunctionBodyFacts),
		callPlans:      make(map[ssa.CallInstruction]coroFrozenCallSitePlan),
		physicalPlans:  make(map[emissionFunctionOwnerKey]*coroPhysicalFunctionPlan),
	}
}

func (ir *coroProgramIR) freezeSemanticInstruction(
	function *ssa.Function,
	owner *preparedEmissionPackage,
	instruction ssa.Instruction,
) error {
	if ir == nil || function == nil || owner == nil || instruction == nil || instruction.Parent() != function {
		return fmt.Errorf("semantic SitePlan requires one exact program IR, owner, and source instruction")
	}
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	if _, sealed := ir.siteOwners[key]; sealed {
		return fmt.Errorf("semantic SitePlan for function %q owner %q was added after owner freeze", function.Name(), owner.identity)
	}
	plan, err := planCoroSemanticInstruction(instruction)
	if err != nil {
		return err
	}
	byInstruction := ir.semanticPlans[key]
	if byInstruction == nil {
		byInstruction = make(map[ssa.Instruction]coroSemanticInstructionPlan)
		ir.semanticPlans[key] = byInstruction
	}
	if previous, exists := byInstruction[instruction]; exists {
		if previous != plan {
			return fmt.Errorf("source instruction acquired conflicting semantic recipes")
		}
		return nil
	}
	byInstruction[instruction] = plan
	return nil
}

func (ir *coroProgramIR) freezeSite(function *ssa.Function, owner *preparedEmissionPackage, instruction ssa.Instruction, plan coroEmissionSitePlan) error {
	if ir == nil || function == nil || owner == nil || instruction == nil || instruction.Parent() != function {
		return fmt.Errorf("requires one exact program IR, owner, and source instruction")
	}
	plan = cloneCoroEmissionSitePlan(plan)
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	byInstruction := ir.sitePlans[key]
	if byInstruction == nil {
		byInstruction = make(map[ssa.Instruction]coroEmissionSitePlan)
		ir.sitePlans[key] = byInstruction
	}
	if previous, exists := byInstruction[instruction]; exists {
		if !sameCoroEmissionSitePlan(previous, plan) {
			return fmt.Errorf("source instruction acquired conflicting helper plans")
		}
		return nil
	}
	if len(plan.managedRuntimeHelpers) != 0 || len(plan.plainRuntimeHelpers) != 0 {
		byInstruction[instruction] = plan
	}
	return nil
}

func (ir *coroProgramIR) freezeSiteOwner(function *ssa.Function, owner *preparedEmissionPackage) error {
	if ir == nil || function == nil || owner == nil {
		return fmt.Errorf("coroutine site plan owner requires an exact program IR, function, and owner")
	}
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	if _, frozen := ir.siteOwners[key]; frozen {
		return fmt.Errorf("coroutine site plan owner %q was frozen more than once", function.Name())
	}
	semantic := ir.semanticPlans[key]
	facts := coro.SSAFunctionBodyFacts{Effect: coro.NoSuspend}
	if function.Blocks != nil {
		facts.Exec = coro.MayUnwind
	}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			plan, ok := semantic[instruction]
			if !ok {
				return fmt.Errorf("coroutine semantic SitePlan owner %q omitted source instruction %q", function.Name(), instruction.String())
			}
			facts.Effect = facts.Effect.Join(plan.effect)
			facts.Exec = facts.Exec.Join(plan.exec)
			if !plan.debug {
				facts.InstructionCount++
			}
		}
	}
	facts.Effect = facts.Effect.Normalize()
	facts.HasCycle = coroSemanticCFGHasCycle(function.Blocks)
	if previous, exists := ir.localBodyFacts[function]; exists && previous != facts {
		return fmt.Errorf("function %q acquired owner-dependent local semantic facts", function.Name())
	}
	ir.localBodyFacts[function] = facts
	ir.siteOwners[key] = none{}
	return nil
}

func (ir *coroProgramIR) semanticInstructionPlan(
	function *ssa.Function,
	owner *preparedEmissionPackage,
	instruction ssa.Instruction,
) (coroSemanticInstructionPlan, error) {
	if ir == nil || function == nil || owner == nil || instruction == nil || instruction.Parent() != function {
		return coroSemanticInstructionPlan{}, fmt.Errorf("semantic SitePlan lookup requires one exact function owner and source instruction")
	}
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	if _, frozen := ir.siteOwners[key]; !frozen {
		return coroSemanticInstructionPlan{}, fmt.Errorf("semantic SitePlan owner is not frozen")
	}
	plan, ok := ir.semanticPlans[key][instruction]
	if !ok {
		return coroSemanticInstructionPlan{}, fmt.Errorf("source instruction %q has no frozen semantic recipe", instruction.String())
	}
	return plan, nil
}

func (ir *coroProgramIR) functionLocalBodyFacts(function *ssa.Function) (coro.SSAFunctionBodyFacts, error) {
	if ir == nil || function == nil {
		return coro.SSAFunctionBodyFacts{}, fmt.Errorf("local body facts require one exact function")
	}
	facts, ok := ir.localBodyFacts[function]
	if !ok {
		return coro.SSAFunctionBodyFacts{}, fmt.Errorf("function %q has no frozen local body facts", function.Name())
	}
	return facts, nil
}

func (ir *coroProgramIR) sitePlan(ctx *context, instruction ssa.Instruction) (coroEmissionSitePlan, error) {
	if ir == nil || ctx == nil || ctx.emissionOwner == nil || instruction == nil || instruction.Parent() == nil {
		return coroEmissionSitePlan{}, fmt.Errorf("coroutine site plan lookup requires an exact program IR, owner context, and source instruction")
	}
	owner := ctx.emissionOwner
	if physical := ctx.coroEmissionPlan(); physical != nil {
		if physical.function != instruction.Parent() || physical.owner == nil {
			return coroEmissionSitePlan{}, fmt.Errorf("coroutine physical emission plan does not own the requested source instruction")
		}
		owner = physical.owner
	}
	key := emissionFunctionOwnerKey{function: instruction.Parent(), owner: owner}
	if _, frozen := ir.siteOwners[key]; !frozen {
		return coroEmissionSitePlan{}, fmt.Errorf("coroutine source instruction has no frozen site-plan owner")
	}
	return cloneCoroEmissionSitePlan(ir.sitePlans[key][instruction]), nil
}

func (ir *coroProgramIR) plannedRuntimeHelpers(ctx *context, instruction ssa.Instruction) ([]string, error) {
	plan, err := ir.sitePlan(ctx, instruction)
	if err != nil {
		return nil, err
	}
	helpers := make([]string, len(plan.managedRuntimeHelpers))
	for index, helper := range plan.managedRuntimeHelpers {
		helpers[index] = helper.name
	}
	return helpers, nil
}

func (ir *coroProgramIR) callSitePlan(call ssa.CallInstruction) (coroFrozenCallSitePlan, bool, error) {
	if ir == nil || !ir.callsFrozen {
		return coroFrozenCallSitePlan{}, false, fmt.Errorf("coroutine call SitePlan is not frozen")
	}
	if call == nil || call.Common() == nil || call.Parent() == nil {
		return coroFrozenCallSitePlan{}, false, fmt.Errorf("coroutine call SitePlan lookup requires an exact SSA call")
	}
	plan, ok := ir.callPlans[call]
	return plan, ok, nil
}

func (plan coroEmissionSitePlan) managedRuntimeHelperNames() []string {
	helpers := make([]string, len(plan.managedRuntimeHelpers))
	for index, helper := range plan.managedRuntimeHelpers {
		helpers[index] = helper.name
	}
	return helpers
}

func (plan coroEmissionSitePlan) managedRuntimeHelpersAt(placement coroRuntimeHelperPlacement) []string {
	helpers := make([]string, 0, len(plan.managedRuntimeHelpers))
	for _, helper := range plan.managedRuntimeHelpers {
		if helper.placement == placement {
			helpers = append(helpers, helper.name)
		}
	}
	return helpers
}

func (plan coroEmissionSitePlan) hasManagedRuntimeHelper(name string) bool {
	for _, helper := range plan.managedRuntimeHelpers {
		if helper.name == name {
			return true
		}
	}
	return false
}

func cloneCoroEmissionSitePlan(plan coroEmissionSitePlan) coroEmissionSitePlan {
	plan.managedRuntimeHelpers = append([]coroPlannedRuntimeHelper(nil), plan.managedRuntimeHelpers...)
	plan.plainRuntimeHelpers = append([]string(nil), plan.plainRuntimeHelpers...)
	return plan
}

func sameCoroEmissionSitePlan(first, second coroEmissionSitePlan) bool {
	return slices.Equal(first.managedRuntimeHelpers, second.managedRuntimeHelpers) &&
		slices.Equal(first.plainRuntimeHelpers, second.plainRuntimeHelpers) &&
		first.hasCallPlan == second.hasCallPlan && sameCoroFrozenCallSitePlan(first.callPlan, second.callPlan)
}
