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

func TestAnalyzeSSASelectsProvenOutcomePlainDAG(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "outcome_plain_dag.go", `package coroid

func leaf(value any, fail bool) int {
	if fail {
		panic(value)
	}
	return 7
}

func middle(value any, fail bool) int {
	return leaf(value, fail)
}

func root(value any, fail bool) int {
	return middle(value, fail)
}
`)
	leaf := packageFunction(t, pkg, "leaf")
	middle := packageFunction(t, pkg, "middle")
	root := packageFunction(t, pkg, "root")
	classify := func(callCount int) func(*ssa.Function) (SSAFunctionBodyFacts, error) {
		return func(fn *ssa.Function) (SSAFunctionBodyFacts, error) {
			facts := scanSSAFunctionBody(fn)
			facts.OutcomePlainLeaf = fn == leaf
			facts.OutcomePlainDAG = fn == leaf || fn == middle
			if fn == middle {
				facts.OutcomePlainCallCount = callCount
			}
			return facts, nil
		}
	}
	analyze := func(t *testing.T, budget int, callCount int) *SSAPlan {
		t.Helper()
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, ManagedDemand: AsyncDemand}}, SSAConfig{
			OutcomeMode:          OutcomeExplicitStatus,
			MaxPlainInstructions: budget,
			ClassifyLocalBody:    classify(callCount),
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	plan := analyze(t, 128, 1)
	leafPlan := functionPlanFor(t, plan, leaf)
	middlePlan := functionPlanFor(t, plan, middle)
	rootPlan := functionPlanFor(t, plan, root)
	if leafPlan.Emission != EmitOutcomePlain || leafPlan.AtomicCostProof != AtomicCostLeaf {
		t.Fatalf("leaf plan = %+v, want outcome-plain leaf", leafPlan)
	}
	wantMiddleCost := uint64(scanSSAFunctionBody(middle).InstructionCount) + leafPlan.AtomicCost
	if middlePlan.Emission != EmitOutcomePlain || middlePlan.ManagedEntry != ManagedEntryOutcomePlain ||
		middlePlan.AtomicCostProof != AtomicCostDAG || middlePlan.AtomicCost != wantMiddleCost {
		t.Fatalf("middle plan = %+v, want outcome-plain DAG cost %d", middlePlan, wantMiddleCost)
	}
	if rootPlan.Emission != EmitCoroutine || rootPlan.AtomicCostProof != AtomicCostUnproven {
		t.Fatalf("root plan = %+v, want ordinary coroutine root", rootPlan)
	}

	t.Run("consumer budget", func(t *testing.T) {
		if middlePlan.AtomicCost <= leafPlan.AtomicCost {
			t.Fatalf("middle cost %d must exceed leaf cost %d", middlePlan.AtomicCost, leafPlan.AtomicCost)
		}
		low := analyze(t, int(middlePlan.AtomicCost)-1, 1)
		if got := functionPlanFor(t, low, leaf); got.Emission != EmitOutcomePlain || got.AtomicCostProof != AtomicCostLeaf {
			t.Fatalf("low-budget leaf plan = %+v, want retained leaf proof", got)
		}
		if got := functionPlanFor(t, low, middle); got.Emission == EmitOutcomePlain || got.AtomicCostProof != AtomicCostUnproven {
			t.Fatalf("low-budget middle plan = %+v, DAG must fail closed", got)
		}
	})

	t.Run("incomplete source-call accounting", func(t *testing.T) {
		_, err := AnalyzeSSA(prog, Roots{{Function: root, ManagedDemand: AsyncDemand}}, SSAConfig{
			OutcomeMode:          OutcomeExplicitStatus,
			MaxPlainInstructions: 128,
			ClassifyLocalBody:    classify(2),
		})
		if err == nil || !strings.Contains(err.Error(), "atomic path call occurrences") {
			t.Fatalf("mismatched ProgramIR call ledger error = %v", err)
		}
	})
}

func TestAnalyzeSSAOutcomePlainDAGUsesLongestMutuallyExclusivePath(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "outcome_plain_branch.go", `package coroid

func left(value any, fail bool) int {
	if fail { panic(value) }
	return 1
}
func right(value any, fail bool) int {
	if fail { panic(value) }
	return 2
}

func choose(value any, fail, useLeft bool) int {
	if useLeft {
		return left(value, fail)
	}
	return right(value, fail)
}

func root(value any, fail, useLeft bool) int { return choose(value, fail, useLeft) }
`)
	left := packageFunction(t, pkg, "left")
	right := packageFunction(t, pkg, "right")
	choose := packageFunction(t, pkg, "choose")
	root := packageFunction(t, pkg, "root")
	classify := func(fn *ssa.Function) (SSAFunctionBodyFacts, error) {
		facts := scanSSAFunctionBody(fn)
		facts.OutcomePlainLeaf = fn == left || fn == right
		facts.OutcomePlainDAG = facts.OutcomePlainLeaf || fn == choose
		if fn == choose {
			facts.OutcomePlainCallCount = 2
		}
		return facts, nil
	}
	config := planDigestSSAConfig()
	config.OutcomeMode = OutcomeExplicitStatus
	config.MaxPlainInstructions = 128
	config.ClassifyLocalBody = classify
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, ManagedDemand: AsyncDemand}}, config)
	if err != nil {
		t.Fatal(err)
	}
	leftPlan := functionPlanFor(t, plan, left)
	rightPlan := functionPlanFor(t, plan, right)
	choosePlan := functionPlanFor(t, plan, choose)
	if leftPlan.AtomicCostProof != AtomicCostLeaf || rightPlan.AtomicCostProof != AtomicCostLeaf ||
		choosePlan.Emission != EmitOutcomePlain || choosePlan.AtomicCostProof != AtomicCostDAG {
		t.Fatalf("branch plans = left=%+v right=%+v choose=%+v", leftPlan, rightPlan, choosePlan)
	}
	conservativeSum := uint64(scanSSAFunctionBody(choose).InstructionCount) + leftPlan.AtomicCost + rightPlan.AtomicCost
	if choosePlan.AtomicCost >= conservativeSum {
		t.Fatalf("path-sensitive cost = %d, want less than branch-summed cost %d", choosePlan.AtomicCost, conservativeSum)
	}
	if len(choosePlan.AtomicCostCertificate) != 64 {
		t.Fatalf("atomic certificate length = %d, want 64", len(choosePlan.AtomicCostCertificate))
	}
	before, err := plan.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	mutatedCertificate := strings.Repeat("c", 64)
	for index := range plan.functions {
		if plan.functions[index].Plan.ID == choosePlan.ID {
			plan.functions[index].Plan.AtomicCostCertificate = mutatedCertificate
			break
		}
	}
	for index := range plan.plan.functions {
		if plan.plan.functions[index].ID == choosePlan.ID {
			plan.plan.functions[index].AtomicCostCertificate = mutatedCertificate
			break
		}
	}
	after, err := plan.CoroPlanDigest(validPlanDigestMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("atomic certificate mutation did not change plan digest")
	}
}

func TestAnalyzeSSAOutcomePlainDAGRejectsRecursiveCycle(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "outcome_plain_cycle.go", `package coroid

func left(n int) int {
	if n == 0 { return 0 }
	return right(n - 1)
}

func right(n int) int {
	if n == 0 { return 0 }
	return left(n - 1)
}

func root(n int) int { return left(n) }
`)
	left := packageFunction(t, pkg, "left")
	right := packageFunction(t, pkg, "right")
	root := packageFunction(t, pkg, "root")
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, ManagedDemand: AsyncDemand}}, SSAConfig{
		OutcomeMode:          OutcomeExplicitStatus,
		MaxPlainInstructions: 128,
		ClassifyLocalBody: func(fn *ssa.Function) (SSAFunctionBodyFacts, error) {
			facts := scanSSAFunctionBody(fn)
			if fn == left || fn == right {
				facts.OutcomePlainDAG = true
				facts.OutcomePlainCallCount = 1
			}
			return facts, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []*ssa.Function{left, right} {
		if got := functionPlanFor(t, plan, fn); got.Emission == EmitOutcomePlain ||
			got.AtomicCostProof != AtomicCostUnproven || !got.Recursive {
			t.Fatalf("recursive plan %q = %+v, DAG must fail closed", fn.Name(), got)
		}
	}
}

func TestAnalyzeSSAOutcomePlainDAGRejectsSourceCFGCycle(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "outcome_plain_cfg_cycle.go", `package coroid

func leaf(value any, fail bool) int {
	if fail { panic(value) }
	return 7
}

func loop(value any, fail bool, count int) int {
	for count > 0 {
		count--
		leaf(value, fail)
	}
	return 0
}

func root(value any, fail bool, count int) int { return loop(value, fail, count) }
`)
	leaf := packageFunction(t, pkg, "leaf")
	loop := packageFunction(t, pkg, "loop")
	root := packageFunction(t, pkg, "root")
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, ManagedDemand: AsyncDemand}}, SSAConfig{
		OutcomeMode:          OutcomeExplicitStatus,
		MaxPlainInstructions: 128,
		ClassifyLocalBody: func(fn *ssa.Function) (SSAFunctionBodyFacts, error) {
			facts := scanSSAFunctionBody(fn)
			facts.OutcomePlainLeaf = fn == leaf
			facts.OutcomePlainDAG = fn == leaf || fn == loop
			if fn == loop {
				facts.OutcomePlainCallCount = 1
			}
			return facts, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, leaf); got.Emission != EmitOutcomePlain {
		t.Fatalf("CFG-cycle fixture leaf = %+v, want independent outcome leaf", got)
	}
	if got := functionPlanFor(t, plan, loop); got.Emission == EmitOutcomePlain ||
		got.AtomicCostProof != AtomicCostUnproven || !scanSSAFunctionBody(loop).HasCycle {
		t.Fatalf("CFG-cycle plan = %+v, want coroutine fallback", got)
	}
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
			Effect:                OutcomeStructured,
			Exec:                  MayUnwind,
			ManagedEntry:          ManagedEntryOutcomePlain,
			AtomicCost:            8,
			AtomicCostProof:       AtomicCostLeaf,
			AtomicCostCertificate: testAtomicCostCertificate,
			IgnoreBody:            true,
			External:              ExternalKnown,
			OverrideExternal:      true,
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

func TestAnalyzeSSAOutcomePlainDAGConsumesImportedProof(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "imported_outcome_plain_dag.go", `package coroid

func imported(value any, fail bool) int
func middle(value any, fail bool) int { return imported(value, fail) }
func root(value any, fail bool) int { return middle(value, fail) }
`)
	imported := packageFunction(t, pkg, "imported")
	middle := packageFunction(t, pkg, "middle")
	root := packageFunction(t, pkg, "root")
	classifyFunction := func(fn *ssa.Function) (SSAFunctionPolicy, error) {
		if fn != imported {
			return SSAFunctionPolicy{}, nil
		}
		return SSAFunctionPolicy{
			Effect:                OutcomeStructured,
			Exec:                  MayUnwind,
			ManagedEntry:          ManagedEntryOutcomePlain,
			AtomicCost:            8,
			AtomicCostProof:       AtomicCostDAG,
			AtomicCostCertificate: testAtomicCostCertificate,
			IgnoreBody:            true,
			External:              ExternalKnown,
			OverrideExternal:      true,
		}, nil
	}
	classifyBody := func(fn *ssa.Function) (SSAFunctionBodyFacts, error) {
		facts := scanSSAFunctionBody(fn)
		if fn == middle {
			facts.OutcomePlainDAG = true
			facts.OutcomePlainCallCount = 1
		}
		return facts, nil
	}
	localCost := scanSSAFunctionBody(middle).InstructionCount
	wantCost := 8 + localCost
	for _, test := range []struct {
		name       string
		budget     int
		wantMiddle bool
	}{
		{name: "exact transitive cost", budget: wantCost, wantMiddle: true},
		{name: "transitive cost too large", budget: wantCost - 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := AnalyzeSSA(prog, Roots{{Function: root, ManagedDemand: AsyncDemand}}, SSAConfig{
				OutcomeMode:          OutcomeExplicitStatus,
				MaxPlainInstructions: test.budget,
				ClassifyFunction:     classifyFunction,
				ClassifyLocalBody:    classifyBody,
			})
			if err != nil {
				t.Fatal(err)
			}
			importedPlan := functionPlanFor(t, plan, imported)
			if importedPlan.ManagedEntry != ManagedEntryOutcomePlain ||
				importedPlan.AtomicCostProof != AtomicCostDAG || importedPlan.AtomicCost != 8 {
				t.Fatalf("imported DAG plan = %+v", importedPlan)
			}
			middlePlan := functionPlanFor(t, plan, middle)
			if test.wantMiddle {
				if middlePlan.Emission != EmitOutcomePlain || middlePlan.AtomicCostProof != AtomicCostDAG ||
					middlePlan.AtomicCost != uint64(wantCost) {
					t.Fatalf("middle imported-DAG plan = %+v, want cost %d", middlePlan, wantCost)
				}
			} else if middlePlan.Emission == EmitOutcomePlain || middlePlan.AtomicCostProof != AtomicCostUnproven {
				t.Fatalf("over-budget imported-DAG consumer = %+v, want coroutine fallback", middlePlan)
			}
		})
	}
}

func TestAnalyzeSSAOutcomePlainLeafFailsClosedWithoutBoundAndKeepsRootPrimary(t *testing.T) {
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
		name     string
		roots    Roots
		max      int
		wantTwin bool
	}{
		{name: "budget disabled", roots: Roots{{Function: caller, ManagedDemand: AsyncDemand}}, max: -1},
		{name: "explicit root", roots: Roots{{Function: leaf, ManagedDemand: AsyncDemand}}, max: 64, wantTwin: true},
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
			if test.wantTwin {
				if got.Emission != EmitCoroutine || got.ManagedEntry != ManagedEntryCoroutine ||
					!got.AtomicCostProof.ProvesOutcomePlain() || got.AtomicCost == 0 {
					t.Fatalf("root leaf plan = %+v, want coroutine primary plus static outcome entry", got)
				}
			} else if got.Emission == EmitOutcomePlain || got.AtomicCostProof != AtomicCostUnproven || got.AtomicCost != 0 {
				t.Fatalf("leaf plan = %+v, outcome-plain proof must fail closed", got)
			}
		})
	}
}
