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
	"strconv"

	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

// CoroLoweringFactsReport is a report-only snapshot built from one frozen
// emission universe and its completed whole-program plan. Digest is diagnostic
// in this migration slice: it does not yet participate in CoroPlanDigest or an
// archive cache key.
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
	if c.CoroPlan == nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts require a frozen CoroPlan")
	}
	if c.EmissionUniverse == nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts require a frozen emission universe")
	}
	return c.EmissionUniverse.BuildCoroLoweringFactsReport(c.CoroPlan)
}

// BuildCoroLoweringFactsReport scans only exact functions and owner contexts
// already frozen in u. A complete runtime ABI is required because otherwise cl
// deliberately retains legacy unresolved runtime markers and cannot attach an
// exact FunctionID to every hidden managed helper.
func (u *EmissionUniverse) BuildCoroLoweringFactsReport(plan *coro.SSAPlan) (CoroLoweringFactsReport, error) {
	if u == nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts require a frozen emission universe")
	}
	if plan == nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts require a frozen CoroPlan")
	}
	if !u.CompleteRuntimeABI() || u.prog == nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts require a complete frozen runtime ABI")
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
	if err := facts.Verify(); err != nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts verify frozen ledger: %w", err)
	}
	digest, err := facts.Digest()
	if err != nil {
		return CoroLoweringFactsReport{}, fmt.Errorf("coroutine lowering facts canonical digest: %w", err)
	}
	return CoroLoweringFactsReport{Facts: facts, Digest: digest}, nil
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
		strconv.FormatBool(u.enableCoroChannel),
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
	helperNames := u.loweredRuntimeHelpers(ctx, instruction)
	helpers := make([]coro.ManagedEdge, 0, len(helperNames))
	for index, logicalName := range helperNames {
		planned, ok := loweredCalls[logicalName]
		if !ok || planned.Target == nil {
			return coro.LoweringFact{}, false, fmt.Errorf("instruction helper %q is absent from the frozen plan", logicalName)
		}
		targetID, ok := plan.FunctionID(planned.Target)
		if !ok {
			return coro.LoweringFact{}, false, fmt.Errorf("instruction helper %q target %q has no frozen FunctionID", logicalName, planned.Target.Name())
		}
		helpers = append(helpers, coro.ManagedEdge{
			Order:       index,
			Role:        coro.RoleHelper,
			Ordinal:     index,
			LogicalName: logicalName,
			Target:      targetID,
			UnwindOnly:  u.loweredCallUnwindOnly(function, instruction),
		})
	}

	class, recipe, effect, exec, materialized := coroSourceInstructionFact(instruction)
	if len(helpers) != 0 {
		materialized = true
		if recipe == "" {
			class = coro.OpLowered
			recipe = coro.RecipeID("cl.ssa.hidden-helpers.v0")
		}
	}
	if call, ok := instruction.(ssa.CallInstruction); ok && call.Common() != nil {
		if callee := call.Common().StaticCallee(); callee != nil {
			if _, frozen := u.Resolve(callee); frozen {
				semantics, intrinsic, err := u.CoroIntrinsicCallSiteSemantics(call)
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
				}
			}
		}
	}
	if !materialized {
		return coro.LoweringFact{}, false, nil
	}

	site, err := coro.NewInstructionEmissionSiteID(instance, instruction, coro.RolePrimary, 0)
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
	if exec.Contains(coro.MayUnwind) {
		footprint |= coro.FootprintUnwind
	}
	implicitPanic := coroImplicitPanicFacts(helperNames)
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
		FunctionUses:  []coro.FunctionValueFact{},
	}, true, nil
}

func coroSourceInstructionFact(instruction ssa.Instruction) (class coro.OpClass, recipe coro.RecipeID, effect coro.Effect, exec coro.ExecFlags, materialized bool) {
	switch instruction := instruction.(type) {
	case *ssa.Send:
		return coro.OpChannel, coro.RecipeID("cl.ssa.channel-send.v0"), coro.MayPark, 0, true
	case *ssa.UnOp:
		if instruction.Op == token.ARROW {
			return coro.OpChannel, coro.RecipeID("cl.ssa.channel-recv.v0"), coro.MayPark, 0, true
		}
	case *ssa.Select:
		effect := coro.NoSuspend
		if instruction.Blocking {
			effect = coro.MayPark
		}
		return coro.OpSelect, coro.RecipeID("cl.ssa.select.v0"), effect, 0, true
	case *ssa.Go:
		return coro.OpSpawn, coro.RecipeID("cl.ssa.spawn.v0"), coro.NoSuspend, 0, true
	case *ssa.Defer:
		return coro.OpControl, coro.RecipeID("cl.ssa.defer.v0"), coro.NoSuspend, coro.NeedsCleanupFrame, true
	case *ssa.RunDefers:
		return coro.OpControl, coro.RecipeID("cl.ssa.run-defers.v0"), coro.NoSuspend, coro.NeedsCleanupFrame, true
	case *ssa.Panic:
		return coro.OpControl, coro.RecipeID("cl.ssa.panic.v0"), coro.NoSuspend, coro.MayUnwind, true
	}
	return "", "", coro.NoSuspend, 0, false
}

func coroIntrinsicLoweringRecipe(semantics CoroIntrinsicCallSemantics) (coro.RecipeID, coro.Effect) {
	switch semantics {
	case CoroIntrinsicCallInlineNoSuspend:
		return coro.RecipeID("cl.intrinsic.inline-nosuspend.v0"), coro.NoSuspend
	case CoroIntrinsicCallInlineWithLoweredCalls:
		return coro.RecipeID("cl.intrinsic.inline-with-helpers.v0"), coro.NoSuspend
	case CoroIntrinsicCallInlineSuspend:
		return coro.RecipeID("cl.intrinsic.inline-suspend.v0"), coro.MayPark
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
