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
	"fmt"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestAnalyzeSSAResolverCanonicalizesPatchedStaticCall(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "resolver.go", `package coroid
var channel chan int
func original() {}
func replacement() { <-channel }
func root() { original() }
`)
	original := packageFunction(t, pkg, "original")
	replacement := packageFunction(t, pkg, "replacement")
	root := packageFunction(t, pkg, "root")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{root, replacement})
	if err != nil {
		t.Fatal(err)
	}
	callbackCount := make(map[*ssa.Function]int)
	resolver := func(fn *ssa.Function) (*ssa.Function, bool, error) {
		callbackCount[fn]++
		if fn == original {
			return replacement, true, nil
		}
		return fn, universe.Contains(fn), nil
	}
	unknownCalls := 0
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse: universe,
		ResolveFunction:  resolver,
		ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (UnknownTarget, error) {
			unknownCalls++
			return UnknownManaged, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if unknownCalls != 0 {
		t.Fatalf("ClassifyUnknownCall called %d times for a resolved static alias", unknownCalls)
	}
	if callbackCount[original] != 1 || callbackCount[replacement] != 1 {
		t.Fatalf("resolver calls: original=%d replacement=%d, want one each", callbackCount[original], callbackCount[replacement])
	}
	if _, ok := plan.FunctionPlan(original); ok {
		t.Fatal("original patched loser entered the plan")
	}
	if got := functionPlanFor(t, plan, root); got.Effect.IsOpaque() || !got.Effect.Contains(MayPark) {
		t.Fatalf("root plan = %+v, want replacement's MayPark effect", got)
	}
	if got := functionPlanFor(t, plan, replacement); got.Demand == NoDemand {
		t.Fatalf("replacement received no demand: %+v", got)
	}
	call := onlyNonBuiltinCall(t, root)
	if call.Common().StaticCallee() != original {
		t.Fatal("analysis mutated the raw SSA StaticCallee")
	}
	callPlan, ok := plan.CallPlan(call)
	if !ok {
		t.Fatal("resolved static call has no CallPlan")
	}
	replacementID, ok := plan.FunctionID(replacement)
	if !ok {
		t.Fatal("replacement has no FunctionID")
	}
	if callPlan.Open || len(callPlan.Targets) != 1 || callPlan.Targets[0] != replacementID || callPlan.Rep != DirectCoro {
		t.Fatalf("static alias CallPlan = %+v, want closed direct-coro replacement", callPlan)
	}
}

func TestAnalyzeSSAResolverCanonicalizesAndJoinsRootAliases(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "resolver.go", `package coroid
func original() {}
func replacement() {}
`)
	original := packageFunction(t, pkg, "original")
	replacement := packageFunction(t, pkg, "replacement")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{replacement})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := AnalyzeSSA(prog, Roots{
		{Function: original, Demand: SyncDemand},
		{Function: replacement, Demand: AsyncDemand},
	}, SSAConfig{
		EmissionUniverse: universe,
		ResolveFunction: func(fn *ssa.Function) (*ssa.Function, bool, error) {
			if fn == original {
				return replacement, true, nil
			}
			return fn, universe.Contains(fn), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, replacement).Demand; got != BothDemand {
		t.Fatalf("canonical root demand = %s, want both", got)
	}
	if _, ok := plan.FunctionPlan(original); ok {
		t.Fatal("root alias loser entered the plan")
	}
}

func TestAnalyzeSSAResolverCanonicalizesFunctionValuesAndClosures(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "resolver.go", `package coroid
func originalA() {}
func originalB() {}
func replacement() {}
func choose(flag bool) {
	fn := originalA
	if flag { fn = originalB }
	fn()
}
func originalClosureOwner() func() { return func() {} }
func replacementClosureOwner() func() { return func() {} }
func invokeClosure() {
	fn := func() {}
	fn()
}
`)
	originalA := packageFunction(t, pkg, "originalA")
	originalB := packageFunction(t, pkg, "originalB")
	replacement := packageFunction(t, pkg, "replacement")
	choose := packageFunction(t, pkg, "choose")
	invokeClosure := packageFunction(t, pkg, "invokeClosure")
	replacementOwner := packageFunction(t, pkg, "replacementClosureOwner")
	if len(invokeClosure.AnonFuncs) != 1 || len(replacementOwner.AnonFuncs) != 1 {
		t.Fatalf("closure counts: invoke=%d replacement=%d", len(invokeClosure.AnonFuncs), len(replacementOwner.AnonFuncs))
	}
	originalClosure := invokeClosure.AnonFuncs[0]
	replacementClosure := replacementOwner.AnonFuncs[0]
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{choose, invokeClosure, replacement, replacementClosure})
	if err != nil {
		t.Fatal(err)
	}
	aliases := map[*ssa.Function]*ssa.Function{
		originalA:       replacement,
		originalB:       replacement,
		originalClosure: replacementClosure,
	}
	resolver := func(fn *ssa.Function) (*ssa.Function, bool, error) {
		if canonical := aliases[fn]; canonical != nil {
			return canonical, true, nil
		}
		return fn, universe.Contains(fn), nil
	}
	plan, err := AnalyzeSSA(prog, Roots{
		{Function: choose, Demand: AsyncDemand},
		{Function: invokeClosure, Demand: AsyncDemand},
	}, SSAConfig{EmissionUniverse: universe, ResolveFunction: resolver})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		caller *ssa.Function
		target *ssa.Function
	}{
		{caller: choose, target: replacement},
		{caller: invokeClosure, target: replacementClosure},
	} {
		call := onlyNonBuiltinCall(t, test.caller)
		got, ok := plan.CallPlan(call)
		if !ok {
			t.Fatalf("%s call has no plan", test.caller.Name())
		}
		wantID, ok := plan.FunctionID(test.target)
		if !ok {
			t.Fatalf("canonical target %s has no ID", test.target)
		}
		if got.Open || got.Rep != DirectPlain || len(got.Targets) != 1 || got.Targets[0] != wantID {
			t.Fatalf("%s call plan = %+v, want one deduplicated canonical target", test.caller.Name(), got)
		}
	}
	if _, ok := plan.FunctionPlan(originalClosure); ok {
		t.Fatal("original closure alias entered the plan")
	}
}

func TestAnalyzeSSAResolverKeepsCHAClosedOpenForRejectedCandidate(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "resolver.go", `package coroid
type Interface interface { Method() }
type A struct{}
type B struct{}
func (A) Method() {}
func (B) Method() {}
func invoke(value Interface) { value.Method() }
`)
	invoke := packageFunction(t, pkg, "invoke")
	methods := matchingFunctions(prog, func(fn *ssa.Function) bool {
		return fn.Name() == "Method" && fn.Signature.Recv() != nil
	})
	if len(methods) < 2 {
		t.Fatalf("got %d methods, want methods for both A and B", len(methods))
	}
	rejected := make(map[*ssa.Function]bool)
	for _, method := range methods {
		recv := types.TypeString(method.Signature.Recv().Type(), func(*types.Package) string { return "" })
		if strings.Contains(recv, "B") {
			rejected[method] = true
		}
	}
	if len(rejected) == 0 {
		t.Fatal("B.Method not found")
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: invoke, Demand: AsyncDemand}}, SSAConfig{
		DynamicResolution: DynamicCHAClosed,
		ResolveFunction: func(fn *ssa.Function) (*ssa.Function, bool, error) {
			if rejected[fn] {
				return nil, false, nil
			}
			return fn, true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := onlyNonBuiltinCall(t, invoke)
	got, ok := plan.CallPlan(call)
	if !ok || !got.Open || got.Unresolved != UnknownManaged || len(got.Targets) == 0 {
		t.Fatalf("partially rejected CHA call plan = %+v, %v; want known targets plus open fallback", got, ok)
	}
	for method := range rejected {
		if _, ok := plan.FunctionPlan(method); ok {
			t.Fatalf("rejected CHA candidate entered the plan: %s", method)
		}
	}
}

func TestAnalyzeSSAResolverValidation(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "resolver.go", `package coroid
func original() {}
func replacement() {}
`)
	original := packageFunction(t, pkg, "original")
	replacement := packageFunction(t, pkg, "replacement")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{replacement})
	if err != nil {
		t.Fatal(err)
	}
	otherProg, otherPkg := buildCoroTestSSA(t, "other.go", "package coroid; func replacement() {}")
	_ = otherProg
	other := packageFunction(t, otherPkg, "replacement")
	tests := []struct {
		name    string
		root    *ssa.Function
		resolve SSAFunctionResolver
		want    string
	}{
		{
			name: "callback error",
			resolve: func(*ssa.Function) (*ssa.Function, bool, error) {
				return nil, false, fmt.Errorf("sentinel")
			},
			want: "sentinel",
		},
		{
			name: "cross program",
			resolve: func(fn *ssa.Function) (*ssa.Function, bool, error) {
				if fn == original {
					return other, true, nil
				}
				return fn, true, nil
			},
			want: "another SSA program",
		},
		{
			name: "outside universe",
			resolve: func(fn *ssa.Function) (*ssa.Function, bool, error) {
				return fn, true, nil
			},
			want: "outside the SSA emission universe",
		},
		{
			name: "non canonical universe",
			root: replacement,
			resolve: func(fn *ssa.Function) (*ssa.Function, bool, error) {
				if fn == original || fn == replacement {
					return original, true, nil
				}
				return nil, false, nil
			},
			want: "remapped exact emission-universe member",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := test.root
			if root == nil {
				root = original
			}
			_, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{
				EmissionUniverse: universe,
				ResolveFunction:  test.resolve,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestAnalyzeSSAResolverRejectsCyclesAndAliasChains(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "resolver.go", `package coroid
func first() {}
func second() {}
func third() {}
`)
	first := packageFunction(t, pkg, "first")
	second := packageFunction(t, pkg, "second")
	third := packageFunction(t, pkg, "third")
	for _, test := range []struct {
		name    string
		resolve SSAFunctionResolver
		want    string
	}{
		{
			name: "cycle",
			resolve: func(fn *ssa.Function) (*ssa.Function, bool, error) {
				switch fn {
				case first:
					return second, true, nil
				case second:
					return first, true, nil
				default:
					return fn, true, nil
				}
			},
			want: "resolver cycle",
		},
		{
			name: "alias chain",
			resolve: func(fn *ssa.Function) (*ssa.Function, bool, error) {
				switch fn {
				case first:
					return second, true, nil
				case second:
					return third, true, nil
				default:
					return fn, true, nil
				}
			},
			want: "resolves to a different function",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := AnalyzeSSA(prog, Roots{{Function: first, Demand: SyncDemand}}, SSAConfig{
				ResolveFunction: test.resolve,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}
