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
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestAnalyzeSSAOutcomeStorageLimitsBothEntryKinds(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "outcome_storage.go", `package coroid
func leaf(value any, fail bool) int { if fail { panic(value) }; return 7 }
func middle(value any, fail bool) int {
  left := leaf(value, fail)
  return left + leaf(value, fail)
}
func outer(value any, fail bool) int { return middle(value, fail) }
func root(value any, fail bool) int { return outer(value, fail) }
`)
	leaf := packageFunction(t, pkg, "leaf")
	middle := packageFunction(t, pkg, "middle")
	outer := packageFunction(t, pkg, "outer")
	root := packageFunction(t, pkg, "root")
	for _, atomicBudget := range []int{512, -1} {
		for _, test := range []struct {
			bytes               uint64
			leaf, middle, outer bool
		}{
			{32 * 1024, true, true, true},
			// The two sequential leaf calls retain only one child frame at a
			// time: middle fits at 96 KiB, while outer exceeds 128 KiB.
			{48 * 1024, true, true, false},
			{^uint64(0), false, false, false},
		} {
			plan, err := AnalyzeSSA(prog, Roots{{Function: root, ManagedDemand: AsyncDemand}}, SSAConfig{
				OutcomeMode:          OutcomeExplicitStatus,
				MaxPlainInstructions: atomicBudget,
				ClassifyLocalBody: func(fn *ssa.Function) (SSAFunctionBodyFacts, error) {
					facts := scanSSAFunctionBody(fn)
					facts.OutcomePlainLeaf = fn == leaf
					facts.OutcomePlainDAG = fn != root
					facts.StaticOutcomeLocal = fn != root
					if fn != root {
						facts.OutcomeLocalStorageBytes = test.bytes
					}
					return facts, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []struct {
				function *ssa.Function
				want     bool
			}{
				{leaf, test.leaf}, {middle, test.middle}, {outer, test.outer},
			} {
				selected := functionPlanFor(t, plan, check.function)
				if selected.HasStaticOutcome() != check.want {
					t.Fatalf("atomic budget %d, local bytes %d, %s: %+v", atomicBudget, test.bytes, check.function.Name(), selected)
				}
			}
			if atomicBudget > 0 && test.leaf && functionPlanFor(t, plan, leaf).AtomicCostProof != AtomicCostLeaf {
				t.Fatal("storage gate bypassed bounded leaf entry selection")
			}
		}
	}
}
