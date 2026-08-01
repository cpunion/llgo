//go:build !llgo

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

package coro

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestAnalyzeSSASelectsProvenOutcomePlainLeaf(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "outcome_plain.go", `package coroid

func leaf(value any, fail bool) int {
	if fail {
		panic(value)
	}
	return 7
}

func caller(value any, fail bool) int {
	return leaf(value, fail)
}
`)
	leaf := packageFunction(t, pkg, "leaf")
	caller := packageFunction(t, pkg, "caller")
	classify := func(fn *ssa.Function) (SSAFunctionBodyFacts, error) {
		facts := scanSSAFunctionBody(fn)
		facts.OutcomePlainLeaf = fn == leaf
		return facts, nil
	}
	config := planDigestSSAConfig()
	config.OutcomeMode = OutcomeExplicitStatus
	config.MaxPlainInstructions = 64
	config.ClassifyLocalBody = classify
	plan, err := AnalyzeSSA(prog, Roots{{Function: caller, ManagedDemand: AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	leafPlan := functionPlanFor(t, plan, leaf)
	if leafPlan.Emission != EmitOutcomePlain || leafPlan.Primary != PrimaryCoroutine ||
		leafPlan.ManagedEntry != ManagedEntryOutcomePlain || leafPlan.FuncRep != DirectCoro || leafPlan.Effect != OutcomeStructured ||
		leafPlan.AtomicCostProof != AtomicCostLeaf || leafPlan.AtomicCost == 0 || leafPlan.AtomicCost > 64 {
		t.Fatalf("leaf plan = %+v, want proven outcome-plain physical body", leafPlan)
	}
	callerPlan := functionPlanFor(t, plan, caller)
	if callerPlan.Emission != EmitCoroutine || callerPlan.AtomicCostProof != AtomicCostUnproven {
		t.Fatalf("caller plan = %+v, want ordinary coroutine caller", callerPlan)
	}
	if _, err := plan.CoroPlanDigest(validPlanDigestMetadata()); err != nil {
		t.Fatalf("digest proven outcome-plain plan: %v", err)
	}
	t.Run("compiler-lowered caller", func(t *testing.T) {
		plan, err := AnalyzeSSA(prog, Roots{{Function: caller, ManagedDemand: AsyncDemand}}, SSAConfig{
			OutcomeMode:          OutcomeExplicitStatus,
			MaxPlainInstructions: 64,
			ClassifyLocalBody:    classify,
			ClassifyLoweredCalls: func(owner *ssa.Function) ([]SSALoweredCall, error) {
				if owner != caller {
					return nil, nil
				}
				return []SSALoweredCall{{LogicalName: "hidden-leaf", Target: leaf}}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := functionPlanFor(t, plan, leaf)
		if got.Emission == EmitOutcomePlain || got.ManagedEntry == ManagedEntryOutcomePlain {
			t.Fatalf("lowered-call target plan = %+v, outcome-plain must fail closed", got)
		}
	})
}

func TestAnalyzeSSAImportedOutcomePlainHonorsConsumerBudget(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "imported_outcome_plain.go", `package coroid

func imported(value any, fail bool) int

func caller(value any, fail bool) int {
	return imported(value, fail)
}
`)
	imported := packageFunction(t, pkg, "imported")
	caller := packageFunction(t, pkg, "caller")
	classify := func(fn *ssa.Function) (SSAFunctionPolicy, error) {
		if fn != imported {
			return SSAFunctionPolicy{}, nil
		}
		return SSAFunctionPolicy{
			Effect:           OutcomeStructured,
			Exec:             MayUnwind,
			ManagedEntry:     ManagedEntryOutcomePlain,
			AtomicCost:       8,
			AtomicCostProof:  AtomicCostLeaf,
			IgnoreBody:       true,
			External:         ExternalKnown,
			OverrideExternal: true,
		}, nil
	}
	for _, test := range []struct {
		name    string
		budget  int
		wantErr bool
	}{
		{name: "unlimited", budget: -1},
		{name: "exact", budget: 8},
		{name: "too small", budget: 7, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := AnalyzeSSA(prog, Roots{{Function: caller, ManagedDemand: AsyncDemand}}, SSAConfig{
				OutcomeMode:          OutcomeExplicitStatus,
				MaxPlainInstructions: test.budget,
				ClassifyFunction:     classify,
			})
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "exceeds consumer budget") {
					t.Fatalf("low-budget imported outcome analysis error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := functionPlanFor(t, plan, imported)
			if got.Emission != EmitExternal || got.ManagedEntry != ManagedEntryOutcomePlain ||
				got.AtomicCost != 8 || got.AtomicCostProof != AtomicCostLeaf {
				t.Fatalf("imported outcome plan = %+v", got)
			}
		})
	}
}

func TestAnalyzeSSAOutcomePlainLeafFailsClosedWithoutBoundOrAtRoot(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "outcome_plain_reject.go", `package coroid

func leaf(value any, fail bool) int {
	if fail {
		panic(value)
	}
	return 7
}

func caller(value any, fail bool) int { return leaf(value, fail) }
`)
	leaf := packageFunction(t, pkg, "leaf")
	caller := packageFunction(t, pkg, "caller")
	classify := func(fn *ssa.Function) (SSAFunctionBodyFacts, error) {
		facts := scanSSAFunctionBody(fn)
		facts.OutcomePlainLeaf = fn == leaf
		return facts, nil
	}
	for _, test := range []struct {
		name  string
		roots Roots
		max   int
	}{
		{name: "budget disabled", roots: Roots{{Function: caller, ManagedDemand: AsyncDemand}}, max: -1},
		{name: "explicit root", roots: Roots{{Function: leaf, ManagedDemand: AsyncDemand}}, max: 64},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := AnalyzeSSA(prog, test.roots, SSAConfig{
				OutcomeMode:          OutcomeExplicitStatus,
				MaxPlainInstructions: test.max,
				ClassifyLocalBody:    classify,
			})
			if err != nil {
				t.Fatal(err)
			}
			got := functionPlanFor(t, plan, leaf)
			if got.Emission == EmitOutcomePlain || got.AtomicCostProof != AtomicCostUnproven || got.AtomicCost != 0 {
				t.Fatalf("leaf plan = %+v, outcome-plain proof must fail closed", got)
			}
		})
	}
}
