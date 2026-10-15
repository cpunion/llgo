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

package cl

import (
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroProgramIRProvesOnlyCanonicalBoundedLoops(t *testing.T) {
	ssaPkg, _, files := buildGoSSAPkg(t, `package boundedloop
func helper(value int) int { return value + 1 }
func Fixed() int {
	total := 0
	for i := 0; i < 4; i++ { total += helper(i) }
	return total
}
func Down() int {
	total := 0
	for i := 5; i >= 2; i-- { total += i }
	return total
}
func Dynamic(limit int) int {
	total := 0
	for i := 0; i < limit; i++ { total += i }
	return total
}
func Nested() int {
	total := 0
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ { total += i + j }
	}
	return total
}
func Wrap() int {
	total := 0
	for i := uint8(250); i != uint8(249); i++ { total++ }
	return total
}
`)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"Fixed", "Down"} {
		function := ssaPkg.Func(name)
		facts, err := universe.CoroLocalBodyFacts(function)
		if err != nil {
			t.Fatal(err)
		}
		if !facts.HasCycle || facts.BoundedCycleCost == 0 {
			t.Fatalf("%s facts = %+v, want a proven bounded cycle", name, facts)
		}
		if facts.BoundedCycleCost <= uint64(facts.InstructionCount) {
			t.Fatalf("%s bounded cost = %d, unique instructions = %d; repeated loop cost was not included", name, facts.BoundedCycleCost, facts.InstructionCount)
		}
	}
	for _, name := range []string{"Dynamic", "Nested", "Wrap"} {
		facts, err := universe.CoroLocalBodyFacts(ssaPkg.Func(name))
		if err != nil {
			t.Fatal(err)
		}
		if !facts.HasCycle || facts.BoundedCycleCost != 0 {
			t.Fatalf("%s facts = %+v, want an unproven cycle", name, facts)
		}
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	fixed := ssaPkg.Func("Fixed")
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: fixed, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:  ssaUniverse,
		FunctionIDs:       functionIDs,
		OutcomeMode:       coro.OutcomeExplicitStatus,
		ClassifyLocalBody: universe.CoroLocalBodyFacts,
		ClassifyFunction: func(*ssa.Function) (coro.SSAFunctionPolicy, error) {
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixedPlan, ok := plan.FunctionPlan(fixed)
	if !ok {
		t.Fatal("Fixed has no coroutine plan")
	}
	if cost, bounded := plan.BoundedCycleCost(fixed); !bounded || cost == 0 {
		t.Fatalf("Fixed accepted bounded-cycle capability = %d, %t", cost, bounded)
	}
	if fixedPlan.Exec.Contains(coro.NeedsPreempt) || fixedPlan.Effect.Contains(coro.YieldOnly) || fixedPlan.Emission != coro.EmitPlain {
		t.Fatalf("Fixed plan = %+v, want a compact plain body", fixedPlan)
	}
}
