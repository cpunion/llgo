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

// This freezes the planning half of compiler-lowered string concatenation.
// The frontend supplies the hidden owner -> StringCat edge; the ordinary SSA
// panic in StringCat must then force an ExplicitStatus coroutine all the way
// through the fixed point without a package/name execution-policy exception.
func TestAnalyzeSSAStringConcatPanicBecomesStructuredOutcome(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "string_concat.go", `package coroid

func owner() {}

func stringCat(left, right string) string {
	length := len(left) + len(right)
	if length < len(left) {
		panic("string concatenation too long")
	}
	return left
}
`)
	owner := packageFunction(t, pkg, "owner")
	stringCat := packageFunction(t, pkg, "stringCat")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{owner, stringCat})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: AsyncDemand}}, SSAConfig{
		EmissionUniverse:     universe,
		OutcomeMode:          OutcomeExplicitStatus,
		MaxPlainInstructions: -1,
		ClassifyLoweredCalls: func(function *ssa.Function) ([]SSALoweredCall, error) {
			if function == owner {
				return []SSALoweredCall{{LogicalName: "StringCat", Target: stringCat}}, nil
			}
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	helperPlan := functionPlanFor(t, plan, stringCat)
	if helperPlan.External != Defined || helperPlan.Demand != AsyncDemand ||
		helperPlan.Emission != EmitCoroutine || helperPlan.Primary != PrimaryCoroutine ||
		helperPlan.FuncRep != DirectCoro || !helperPlan.Exec.Contains(MayUnwind) ||
		!helperPlan.Effect.Contains(OutcomeStructured) {
		t.Fatalf("StringCat plan = %+v, want demanded MayUnwind ExplicitStatus coroutine", helperPlan)
	}
	ownerPlan := functionPlanFor(t, plan, owner)
	if ownerPlan.Emission != EmitCoroutine || !ownerPlan.Effect.Contains(OutcomeStructured) {
		t.Fatalf("concat owner plan = %+v, want propagated structured outcome", ownerPlan)
	}
	lowered := plan.LoweredCalls(owner)
	if len(lowered) != 1 || lowered[0].LogicalName != "StringCat" || lowered[0].Target != stringCat {
		t.Fatalf("concat lowered calls = %+v, want exact StringCat edge", lowered)
	}
}
