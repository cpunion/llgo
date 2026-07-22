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

	"golang.org/x/tools/go/ssa"
)

// coroProgramIR owns production SitePlan state. Later replacement cohorts add
// global summaries, physical control and storage projections to this same
// object rather than creating independently-versioned lowering documents.
type coroProgramIR struct {
	sitePlans  map[emissionFunctionOwnerKey]map[ssa.Instruction]coroEmissionSitePlan
	siteOwners map[emissionFunctionOwnerKey]none
}

func newCoroProgramIR() *coroProgramIR {
	return &coroProgramIR{
		sitePlans:  make(map[emissionFunctionOwnerKey]map[ssa.Instruction]coroEmissionSitePlan),
		siteOwners: make(map[emissionFunctionOwnerKey]none),
	}
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
	ir.siteOwners[key] = none{}
	return nil
}

func (ir *coroProgramIR) sitePlan(ctx *context, instruction ssa.Instruction) (coroEmissionSitePlan, error) {
	if ir == nil || ctx == nil || ctx.emissionOwner == nil || instruction == nil || instruction.Parent() == nil {
		return coroEmissionSitePlan{}, fmt.Errorf("coroutine site plan lookup requires an exact program IR, owner context, and source instruction")
	}
	key := emissionFunctionOwnerKey{function: instruction.Parent(), owner: ctx.emissionOwner}
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

func cloneCoroEmissionSitePlan(plan coroEmissionSitePlan) coroEmissionSitePlan {
	plan.managedRuntimeHelpers = append([]coroPlannedRuntimeHelper(nil), plan.managedRuntimeHelpers...)
	plan.plainRuntimeHelpers = append([]string(nil), plan.plainRuntimeHelpers...)
	return plan
}

func sameCoroEmissionSitePlan(first, second coroEmissionSitePlan) bool {
	return slices.Equal(first.managedRuntimeHelpers, second.managedRuntimeHelpers) &&
		slices.Equal(first.plainRuntimeHelpers, second.plainRuntimeHelpers)
}
