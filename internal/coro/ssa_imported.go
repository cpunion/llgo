/*
 * Copyright (c) 2026 The XGo Authors (xgo.dev). All rights reserved.
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may obtain a copy at http://www.apache.org/licenses/LICENSE-2.0.
 */

package coro

import (
	"fmt"

	"golang.org/x/tools/go/ssa"
)

// SSAImportedFunctionPlan is an exact archive-only leaf in the same fixed-point
// plan as the source functions. Fact owns its published ABI, symbols and service
// requirements; Plan owns this consumer's effects, demand and emission decision.
// It deliberately has no fabricated SSA function or executable body.
type SSAImportedFunctionPlan struct {
	Fact LibraryEffectFunction `json:"fact"`
	Plan FunctionPlan          `json:"plan"`
}

// ImportedFunctions returns archive-only function plans in FunctionID order.
// Functions remains the source-only projection and never contains nil SSA pairs.
func (p *SSAPlan) ImportedFunctions() []SSAImportedFunctionPlan {
	if p == nil {
		return nil
	}
	return append([]SSAImportedFunctionPlan(nil), p.importedFunctions...)
}

func freezeSSAImportedFunctions(facts []LibraryEffectFunction, source map[FunctionID]*ssa.Function) (map[FunctionID]LibraryEffectFunction, error) {
	ret := make(map[FunctionID]LibraryEffectFunction, len(facts))
	for _, fact := range facts {
		if err := validateDigestFunctionID(fact.ID); err != nil {
			return nil, err
		}
		if _, err := fact.ImportedPolicy(); err != nil {
			return nil, fmt.Errorf("coro: imported archive helper %q: %w", fact.ID, err)
		}
		if _, present := source[fact.ID]; present {
			return nil, fmt.Errorf("coro: imported archive helper %q also has a source declaration", fact.ID)
		}
		if _, duplicate := ret[fact.ID]; duplicate {
			return nil, fmt.Errorf("coro: duplicate imported archive helper %q", fact.ID)
		}
		// A helper's own resource result would need a further custody handoff.
		// Do not turn declaration-only support into permission to omit that guard.
		if !fact.Delivery.IsZero() {
			return nil, fmt.Errorf("coro: imported archive helper %q cannot itself publish resource results", fact.ID)
		}
		ret[fact.ID] = fact
	}
	return ret, nil
}

func (call SSALoweredCall) targetID(source map[*ssa.Function]FunctionID) (FunctionID, bool) {
	if call.ImportedTarget != "" {
		return call.ImportedTarget, true
	}
	id, present := source[call.Target]
	return id, present
}

func (p *SSAPlan) canonicalDigestImportedFunctions() ([]SSAImportedFunctionPlan, error) {
	ret := p.ImportedFunctions()
	var previous FunctionID
	for index, function := range ret {
		fact, plan := function.Fact, function.Plan
		if _, err := fact.ImportedPolicy(); err != nil {
			return nil, err
		}
		if err := validateDigestFunctionID(fact.ID); err != nil {
			return nil, err
		}
		if !fact.Delivery.IsZero() || fact.ID != plan.ID || index != 0 && previous >= fact.ID {
			return nil, fmt.Errorf("coro: malformed imported archive helper inventory at %q", fact.ID)
		}
		previous = fact.ID
		if err := validateDigestFunctionPlan(plan); err != nil {
			return nil, err
		}
		base, found := p.plan.Lookup(fact.ID)
		if !found || base != plan || plan.External != ExternalKnown ||
			plan.DeclaredEffect != fact.Effect || plan.DeclaredExec != fact.Exec ||
			plan.ManagedEntry != fact.ManagedEntry || plan.AtomicCost != fact.AtomicCost ||
			plan.AtomicCostProof != fact.AtomicCostProof || plan.AtomicCostCertificate != fact.AtomicCostCertificate ||
			plan.StaticOutcome != fact.StaticOutcome {
			return nil, fmt.Errorf("coro: imported archive helper %q differs from the base plan", fact.ID)
		}
		if _, source := p.byID[fact.ID]; source {
			return nil, fmt.Errorf("coro: imported archive helper %q also has a source declaration", fact.ID)
		}
	}
	return ret, nil
}
