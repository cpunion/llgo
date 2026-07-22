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
	"strconv"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// CoroLoweringFactsReport is a canonical snapshot built from one frozen
// emission universe and its completed whole-program plan. Active build-driver
// compilations install its digest into CoroPlanDigest and every archive cache
// identity; focused report-only callers may still build it independently.
type CoroLoweringFactsReport struct {
	Facts  coro.LoweringFacts
	Digest string
}

// BuildCoroLoweringFactsReport constructs the sparse lowering-fact projection
// associated with c. It performs no LLVM emission and changes no plan, cache,
// archive, or runtime artifact.
func (c *Compilation) BuildCoroLoweringFactsReport() (CoroLoweringFactsReport, error) {
	if c == nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts require a compilation")
	}
	if c.CoroLoweringFacts.Schema != "" || c.CoroLoweringFactsDigest != "" {
		if err := c.validateCoroLoweringFactsIdentity(); err != nil {
			return CoroLoweringFactsReport{}, err
		}
		return CoroLoweringFactsReport{Facts: c.CoroLoweringFacts, Digest: c.CoroLoweringFactsDigest}, nil
	}
	if c.CoroPlan == nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts require a frozen CoroPlan")
	}
	if c.EmissionUniverse == nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts require a frozen emission universe")
	}
	return c.EmissionUniverse.BuildCoroLoweringFactsReport(c.CoroPlan)
}

// BuildCoroLoweringFactsReport scans only exact functions and owner contexts
// already frozen in u. Incomplete runtime profiles are accepted for entry-only
// or helper-free compilations; any materialized hidden helper that lacks an
// exact frozen target still fails at its source site below.
func (u *EmissionUniverse) BuildCoroLoweringFactsReport(plan *coro.SSAPlan) (CoroLoweringFactsReport, error) {
	if u == nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts require a frozen emission universe")
	}
	if plan == nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts require a frozen CoroPlan")
	}
	if u.prog == nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts require a frozen target program")
	}
	if err := u.ValidateCoroPlan(plan); err != nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts validate plan coverage: %w", err)
	}

	functions := make([]coro.FunctionLoweringFacts, 0, len(u.functions))
	for _, function := range u.functions {
		functionID, ok := plan.FunctionID(function)
		if !ok {
			return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts: function %q has no frozen FunctionID", function.Name())
		}
		functionPlan, ok := plan.FunctionPlan(function)
		if !ok {
			return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts: function %q has no frozen FunctionPlan", function.Name())
		}
		owners := u.sortedUseOwners(function)
		if len(owners) == 0 {
			return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts: function %q has no frozen owner", function.Name())
		}
		for _, owner := range owners {
			instance, err := u.coroLoweringFactsInstanceID(function, functionID, owner)
			if err != nil {
				return CoroLoweringFactsReport{}, err
			}
			sites, err := u.coroLoweringFunctionSites(plan, function, owner, instance)
			if err != nil {
				return CoroLoweringFactsReport{}, err
			}
			functions = append(functions, coro.FunctionLoweringFacts{
				Instance:    instance,
				LocalEffect: functionPlan.LocalEffect,
				LocalExec:   functionPlan.LocalExec,
				Sites:       sites,
			})
		}
	}

	facts := coro.NewLoweringFacts(functions)
	canonical, err := facts.Canonical()
	if err != nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts verify frozen ledger: %w", err)
	}
	digest, err := canonical.Digest()
	if err != nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts canonical digest: %w", err)
	}
	return CoroLoweringFactsReport{Facts: canonical, Digest: digest}, nil
}

func (u *EmissionUniverse) coroLoweringFactsInstanceID(function *ssa.Function, functionID coro.FunctionID, owner *preparedEmissionPackage) (coro.EmissionInstanceID, error) {
	if function == nil || owner == nil {
		return coro.EmissionInstanceID{}, fmt.Errorf("coroutine lowering facts require an exact function owner")
	}
	if u.linkIdentities[function] == "" {
		return coro.EmissionInstanceID{}, fmt.Errorf("coroutine lowering facts: function %q link identity is not frozen", function.Name())
	}
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	kind, kindOK := u.functionKinds[key]
	state, stateOK := u.ownerStates[function][owner]
	if !kindOK || !stateOK {
		return coro.EmissionInstanceID{}, fmt.Errorf("coroutine lowering facts: function %q owner %q has incomplete frozen provenance", function.Name(), owner.identity)
	}
	opcode := ""
	if value, ok := u.intrinsicOps[key]; ok {
		opcode = strconv.Itoa(value)
	}
	target := u.prog.TargetSpec()
	context := emissionDigest(framedEmissionKey(
		"cl-coro-lowering-context-v0",
		target.Triple,
		target.CPU,
		target.Features,
		target.TargetABI,
		u.prog.DataLayout(),
		strconv.Itoa(u.prog.PointerSize()*8),
		strconv.FormatBool(u.completeRuntimeABI),
		strconv.FormatBool(u.CoroChannelEnabled()),
		owner.identity,
		strconv.Itoa(kind),
		strconv.Itoa(int(state.state)),
		strconv.FormatBool(state.fromPatch),
		u.finalKeys[key],
		u.syntheticKeys[function],
		opcode,
	))
	instance, err := coro.NewEmissionInstanceID(functionID, owner.identity, context)
	if err != nil {
		return coro.EmissionInstanceID{}, fmt.Errorf("coroutine lowering facts: function %q owner %q instance: %w", function.Name(), owner.identity, err)
	}
	return instance, nil
}

func (u *EmissionUniverse) coroLoweringFunctionSites(plan *coro.SSAPlan, function *ssa.Function, owner *preparedEmissionPackage, instance coro.EmissionInstanceID) ([]coro.LoweringFact, error) {
	key := emissionFunctionOwnerKey{function: function, owner: owner}
	if u.functionKinds[key] != goFunc || plan.IgnoresBody(function) || len(function.Blocks) == 0 {
		return []coro.LoweringFact{}, nil
	}
	ctx, err := u.functionABIContext(function, owner)
	if err != nil {
		return nil, fmt.Errorf("coroutine lowering facts: function %q owner %q context: %w", function.Name(), owner.identity, err)
	}
	loweredCalls := make(map[string]coro.SSALoweredCall)
	for _, call := range plan.LoweredCalls(function) {
		loweredCalls[call.LogicalName] = call
	}

	sites := make([]coro.LoweringFact, 0)
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if _, unevaluated := ctx.unevaluatedSSA[instruction]; unevaluated {
				continue
			}
			if _, debug := instruction.(*ssa.DebugRef); debug {
				continue
			}
			fact, materialized, err := u.coroInstructionLoweringFact(ctx, plan, function, instance, instruction, loweredCalls)
			if err != nil {
				return nil, fmt.Errorf("coroutine lowering facts: function %q block %d: %w", function.Name(), block.Index, err)
			}
			if materialized {
				sites = append(sites, fact)
			}
		}
	}
	return sites, nil
}

func (u *EmissionUniverse) coroInstructionLoweringFact(ctx *context, plan *coro.SSAPlan, function *ssa.Function, instance coro.EmissionInstanceID, instruction ssa.Instruction, loweredCalls map[string]coro.SSALoweredCall) (coro.LoweringFact, bool, error) {
	siteRole := coro.RolePrimary
	contract := coro.ContractID("")
	barrier := false
	helperNames, err := u.coroProgramIR.plannedRuntimeHelpers(ctx, instruction)
	if err != nil {
		return coro.LoweringFact{}, false, fmt.Errorf("load frozen site helper plan: %w", err)
	}
	helpers := make([]coro.ManagedEdge, 0, len(helperNames))
	for _, logicalName := range helperNames {
		if coroCompilerElidesImplicitFaultRuntimeHelper(instruction, logicalName) {
			continue
		}
		planned, ok := loweredCalls[logicalName]
		if !ok || planned.Target == nil {
			return coro.LoweringFact{}, false, fmt.Errorf("instruction helper %q is absent from the frozen plan", logicalName)
		}
		targetID, ok := plan.FunctionID(planned.Target)
		if !ok {
			return coro.LoweringFact{}, false, fmt.Errorf("instruction helper %q target %q has no frozen FunctionID", logicalName, planned.Target.Name())
		}
		helpers = append(helpers, coro.ManagedEdge{
			Order:                len(helpers),
			Role:                 coro.RoleHelper,
			Ordinal:              len(helpers),
			LogicalName:          logicalName,
			Target:               targetID,
			UnwindOnly:           planned.UnwindOnly,
			ExplicitStatusElided: planned.ExplicitStatusElided,
		})
	}

	semantic, err := u.coroProgramIR.semanticInstructionPlan(function, ctx.emissionOwner, instruction)
	if err != nil {
		return coro.LoweringFact{}, false, fmt.Errorf("load frozen semantic SitePlan: %w", err)
	}
	class, recipe, effect, exec, materialized := semantic.class, semantic.recipe, semantic.effect, semantic.exec, semantic.materialized
	functionUses := []coro.FunctionValueFact{}
	if store, ok := instruction.(*ssa.Store); ok {
		if target, conditional := plan.ConditionalManagedStoreTarget(store); conditional {
			if store.Parent() != function || target == nil {
				return coro.LoweringFact{}, false, fmt.Errorf("conditional managed Store has no exact owner/target")
			}
			targetID, planned := plan.FunctionID(target)
			if !planned {
				return coro.LoweringFact{}, false, fmt.Errorf("conditional managed Store target %q has no frozen FunctionID", target.Name())
			}
			class = coro.OpLowered
			recipe = coro.RecipeID("cl.ssa.conditional-managed-store.publish.v0")
			if plan.ElidesConditionalManagedStore(store) {
				recipe = coro.RecipeID("cl.ssa.conditional-managed-store.elide.v0")
			}
			materialized = true
			contract = coro.ContractID("llgo.coro.conditional-managed-publication.v0")
			functionUses = []coro.FunctionValueFact{{
				Order: 0, Role: coro.RolePrimary, Ordinal: 0,
				Targets: []coro.FunctionID{targetID}, Open: false, MayBeNil: false,
			}}
		}
	}
	implicitPanic := coroImplicitPanicFacts(helperNames)
	if len(helpers) != 0 || len(implicitPanic) != 0 {
		materialized = true
		if !semantic.materialized {
			class = coro.OpLowered
			if len(helpers) == 0 {
				recipe = coro.RecipeID("cl.ssa.implicit-fault-guard.v0")
			} else {
				recipe = coro.RecipeID("cl.ssa.hidden-helpers.v0")
			}
		}
	}
	if call, ok := instruction.(ssa.CallInstruction); ok && call.Common() != nil {
		if callee := call.Common().StaticCallee(); callee != nil {
			if _, frozen := u.Resolve(callee); frozen {
				semantics, intrinsic, err := coroIntrinsicCallSiteSemantics(u, call)
				if err != nil {
					return coro.LoweringFact{}, false, err
				}
				if intrinsic && semantics.ElidesManagedCall() {
					if !plan.ElidesCall(call) {
						return coro.LoweringFact{}, false, fmt.Errorf("elided intrinsic call is not elided by the frozen plan")
					}
					materialized = true
					class = coro.OpIntrinsic
					recipe, effect = coroIntrinsicLoweringRecipe(semantics)
					if direct, ok := instruction.(*ssa.Call); ok {
						role, critical, criticalErr := u.coroCriticalCallSite(direct)
						if criticalErr != nil {
							return coro.LoweringFact{}, false, criticalErr
						}
						if critical {
							barrier = true
							contract = coro.ContractID("llgo.coro.critical-depth.v1")
							switch role {
							case coroCriticalCallEnter:
								siteRole = coro.RoleRegionBegin
								recipe = coro.RecipeID("cl.intrinsic.coro-critical-enter.v1")
							case coroCriticalCallExit:
								siteRole = coro.RoleRegionEnd
								recipe = coro.RecipeID("cl.intrinsic.coro-critical-exit.v1")
							default:
								return coro.LoweringFact{}, false, fmt.Errorf("critical intrinsic has no exact region role")
							}
						}
					}
				}
			}
		}
	}
	if !materialized {
		return coro.LoweringFact{}, false, nil
	}

	site, err := coro.NewInstructionEmissionSiteID(instance, instruction, siteRole, 0)
	if err != nil {
		return coro.LoweringFact{}, false, err
	}
	footprint := coro.BackendFootprint(0)
	if len(helpers) != 0 {
		footprint |= coro.FootprintManagedCall
	}
	if effect.MaySuspend() {
		footprint |= coro.FootprintSuspend
	}
	if barrier {
		footprint |= coro.FootprintBarrier
	}
	if exec.Contains(coro.MayUnwind) {
		footprint |= coro.FootprintUnwind
	}
	if len(implicitPanic) != 0 {
		footprint |= coro.FootprintPanic
	}
	if _, explicitPanic := instruction.(*ssa.Panic); explicitPanic {
		footprint |= coro.FootprintPanic
	}
	return coro.LoweringFact{
		Site:          site,
		Class:         class,
		Recipe:        recipe,
		Effect:        effect,
		Exec:          exec,
		Footprint:     footprint,
		Helpers:       helpers,
		ImplicitPanic: implicitPanic,
		FunctionUses:  functionUses,
		Contract:      contract,
	}, true, nil
}

func coroIntrinsicLoweringRecipe(semantics CoroIntrinsicCallSemantics) (coro.RecipeID, coro.Effect) {
	switch semantics {
	case CoroIntrinsicCallInlineNoSuspend:
		return coro.RecipeID("cl.intrinsic.inline-nosuspend.v0"), coro.NoSuspend
	case CoroIntrinsicCallInlineWithLoweredCalls:
		return coro.RecipeID("cl.intrinsic.inline-with-helpers.v0"), coro.NoSuspend
	case CoroIntrinsicCallInlineSuspend:
		return coro.RecipeID("cl.intrinsic.inline-suspend.v0"), coro.MayPark
	case CoroIntrinsicCallInlineYield:
		return coro.RecipeID("cl.intrinsic.inline-yield.v0"), coro.YieldOnly
	default:
		return coro.RecipeID("cl.intrinsic.unsupported.v0"), coro.NoSuspend
	}
}

func coroImplicitPanicFacts(helperNames []string) []coro.ImplicitPanicFact {
	ret := make([]coro.ImplicitPanicFact, 0)
	for _, helper := range helperNames {
		kind := ""
		switch helper {
		case "AssertNilDeref", "AssertNilDerefPtr":
			kind = "nil-deref"
		case "CheckIndexRange":
			kind = "index-range"
		case "PanicSliceConvert":
			kind = "slice-convert"
		}
		if kind == "" {
			continue
		}
		ret = append(ret, coro.ImplicitPanicFact{
			Order:   len(ret),
			Role:    coro.RolePanic,
			Ordinal: len(ret),
			Kind:    kind,
		})
	}
	return ret
}
