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
	"bytes"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestSSAEmissionUniverseExactImmutableAndDeterministic(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
func Alpha() {}
func Beta() {}
`)
	alpha := packageFunction(t, pkg, "Alpha")
	beta := packageFunction(t, pkg, "Beta")
	input := []*ssa.Function{beta, alpha, beta}
	universe, err := NewSSAEmissionUniverse(prog, input)
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := NewSSAEmissionUniverse(prog, []*ssa.Function{alpha, beta})
	if err != nil {
		t.Fatal(err)
	}

	if universe.Program() != prog {
		t.Fatal("universe returned a different SSA program")
	}
	want := []*ssa.Function{alpha, beta}
	got := universe.Functions()
	gotReversed := reversed.Functions()
	if len(got) != len(want) || len(gotReversed) != len(want) {
		t.Fatalf("function counts = %d and %d, want %d", len(got), len(gotReversed), len(want))
	}
	for i := range want {
		if got[i] != want[i] || gotReversed[i] != want[i] {
			t.Fatalf("function %d = (%p, %p), want exact pointer %p", i, got[i], gotReversed[i], want[i])
		}
	}

	// Neither the constructor input nor a returned snapshot aliases the frozen
	// function slice.
	input[0] = nil
	got[0] = nil
	if frozen := universe.Functions(); len(frozen) != 2 || frozen[0] != alpha || frozen[1] != beta {
		t.Fatalf("mutating caller-owned slices changed the universe: %v", frozen)
	}
	if !universe.Contains(alpha) || !universe.Contains(beta) || universe.Contains(nil) {
		t.Fatal("universe membership does not match its exact function set")
	}
	alphaCopy := *alpha
	if universe.Contains(&alphaCopy) {
		t.Fatal("distinct SSA function pointer unexpectedly matched")
	}
	withCopy, err := NewSSAEmissionUniverse(prog, []*ssa.Function{alpha, &alphaCopy})
	if err != nil {
		t.Fatalf("constructor performed premature identity validation: %v", err)
	}
	if !withCopy.Contains(alpha) || !withCopy.Contains(&alphaCopy) {
		t.Fatal("constructor did not preserve exact functions with equal raw sort keys")
	}

	_, otherPkg := buildCoroTestSSA(t, "other.go", "package coroid; func Alpha() {}")
	otherAlpha := packageFunction(t, otherPkg, "Alpha")
	if universe.Contains(otherAlpha) {
		t.Fatal("logically identical function from another SSA program unexpectedly matched")
	}
	if _, err := NewSSAEmissionUniverse(nil, nil); err == nil || !strings.Contains(err.Error(), "nil program") {
		t.Fatalf("nil program error = %v", err)
	}
	if _, err := NewSSAEmissionUniverse(prog, []*ssa.Function{nil}); err == nil || !strings.Contains(err.Error(), "is nil") {
		t.Fatalf("nil function error = %v", err)
	}
	if _, err := NewSSAEmissionUniverse(prog, []*ssa.Function{otherAlpha}); err == nil || !strings.Contains(err.Error(), "another program") {
		t.Fatalf("foreign function error = %v", err)
	}
	var nilUniverse *SSAEmissionUniverse
	if nilUniverse.Program() != nil || nilUniverse.Functions() != nil || nilUniverse.Contains(alpha) {
		t.Fatal("nil universe accessors are not nil-safe")
	}
}

func TestAnalyzeSSAEmissionUniverseExcludesProgramFunctions(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
var channel chan int
func outside() { <-channel }
func unrelated() {}
func root() { outside() }
`)
	root := packageFunction(t, pkg, "root")
	outside := packageFunction(t, pkg, "outside")
	unrelated := packageFunction(t, pkg, "unrelated")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{root})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse: universe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(plan.Functions()); got != 1 {
		t.Fatalf("plan function count = %d, want 1", got)
	}
	if _, ok := plan.FunctionPlan(outside); ok {
		t.Fatal("static callee outside emission universe entered the plan")
	}
	if _, ok := plan.FunctionPlan(unrelated); ok {
		t.Fatal("unrelated package member outside emission universe entered the plan")
	}
	if got := functionPlanFor(t, plan, root); !got.Effect.IsOpaque() {
		t.Fatalf("root effect = %s, want opaque call to excluded static callee", got.Effect)
	}
}

func TestAnalyzeSSAEmissionUniverseRestrictsCHA(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
var channel chan int
type Interface interface { Method() }
type Concrete struct{}
func (Concrete) Method() { <-channel }
func invoke(value Interface) { value.Method() }
func use() { invoke(Concrete{}) }
`)
	invoke := packageFunction(t, pkg, "invoke")
	methods := matchingFunctions(prog, func(fn *ssa.Function) bool {
		return fn.Name() == "Method" && fn.Signature.Recv() != nil
	})
	if len(methods) == 0 {
		t.Fatal("Concrete.Method SSA function not found")
	}
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{invoke})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: invoke, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse:  universe,
		DynamicResolution: DynamicCHAClosed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, invoke); !got.Effect.IsOpaque() {
		t.Fatalf("invoke effect = %s, want opaque without an in-universe CHA target", got.Effect)
	}
	for _, method := range methods {
		if _, ok := plan.FunctionPlan(method); ok {
			t.Fatalf("CHA target outside emission universe entered the plan: %s", method)
		}
	}

	withMethods := append([]*ssa.Function{invoke}, methods...)
	completeUniverse, err := NewSSAEmissionUniverse(prog, withMethods)
	if err != nil {
		t.Fatal(err)
	}
	completePlan, err := AnalyzeSSA(prog, Roots{{Function: invoke, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse:  completeUniverse,
		DynamicResolution: DynamicCHAClosed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, completePlan, invoke); got.Effect.IsOpaque() || !got.Effect.Contains(MayPark) {
		t.Fatalf("invoke effect with in-universe method = %s, want known MayPark", got.Effect)
	}
}

func TestAnalyzeSSAEmissionUniverseDynamicImplementsUsesPatchedRelation(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
var channel chan int
type Interface interface {
	Method()
	RawOnlyMethod()
}
type Concrete struct{}
func (Concrete) Method() { <-channel }
func invoke(value Interface) { value.Method() }
`)
	invoke := packageFunction(t, pkg, "invoke")
	methods := matchingFunctions(prog, func(fn *ssa.Function) bool {
		return fn.Name() == "Method" && fn.Signature.Recv() != nil && fn.Object() != nil && fn.Synthetic == ""
	})
	if len(methods) != 1 {
		t.Fatalf("declared Concrete.Method count = %d, want 1", len(methods))
	}
	method := methods[0]
	call := onlyNonBuiltinCall(t, invoke)
	if !call.Common().IsInvoke() {
		t.Fatalf("invoke call = %s, want interface invoke", call)
	}
	iface, ok := call.Common().Value.Type().Underlying().(*types.Interface)
	if !ok {
		t.Fatalf("invoke receiver type = %T, want interface", call.Common().Value.Type().Underlying())
	}
	receiver := method.Signature.Recv().Type()
	if types.Implements(receiver, iface) {
		t.Fatalf("raw receiver %s unexpectedly implements raw interface %s", receiver, iface)
	}

	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{invoke, method})
	if err != nil {
		t.Fatal(err)
	}
	checks := 0
	plan, err := AnalyzeSSA(prog, Roots{{Function: invoke, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse:  universe,
		DynamicResolution: DynamicCHAClosed,
		DynamicImplements: func(candidate types.Type, dynamicInterface *types.Interface) (bool, error) {
			checks++
			if candidate != receiver || dynamicInterface != iface {
				t.Fatalf("dynamic implements inputs = (%s, %s), want exact raw (%s, %s)", candidate, dynamicInterface, receiver, iface)
			}
			return true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatalf("dynamic implements checks = %d, want 1", checks)
	}
	if got := functionPlanFor(t, plan, invoke); got.Effect.IsOpaque() || !got.Effect.Contains(MayPark) {
		t.Fatalf("invoke effect = %s, want closed patched target with MayPark", got.Effect)
	}
	callPlan, ok := plan.CallPlan(call)
	if !ok || callPlan.Open || len(callPlan.Targets) != 1 {
		t.Fatalf("patched invoke call plan = %+v, %v; want one closed exact target", callPlan, ok)
	}

	_, err = AnalyzeSSA(prog, Roots{{Function: invoke, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse:  universe,
		DynamicResolution: DynamicCHAClosed,
		DynamicImplements: func(types.Type, *types.Interface) (bool, error) {
			return false, bytes.ErrTooLarge
		},
	})
	if err == nil || !strings.Contains(err.Error(), "restricted CHA match candidate") ||
		!strings.Contains(err.Error(), method.String()) || !strings.Contains(err.Error(), bytes.ErrTooLarge.Error()) {
		t.Fatalf("dynamic implements error = %v, want deterministic candidate context and resolver error", err)
	}
}

func TestRestrictedSSACHAMemoizesSharedInterfaceMethod(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
var channel chan int
type Interface interface { Method() }
type Concrete struct{}
func (Concrete) Method() { <-channel }
func invokeA(value Interface) { value.Method() }
func invokeB(value Interface) { value.Method() }
`)
	invokeA := packageFunction(t, pkg, "invokeA")
	invokeB := packageFunction(t, pkg, "invokeB")
	methods := matchingFunctions(prog, func(fn *ssa.Function) bool {
		return fn.Name() == "Method" && fn.Signature.Recv() != nil
	})
	if len(methods) == 0 {
		t.Fatal("Concrete.Method SSA function not found")
	}
	functions := append([]*ssa.Function{invokeA, invokeB}, methods...)
	checks := 0
	candidates := restrictedSSACHACandidatesWithImplements(functions, func(candidate types.Type, iface *types.Interface) bool {
		checks++
		return types.Implements(candidate, iface)
	})
	if checks != len(methods) {
		t.Fatalf("types.Implements checks = %d, want one shared scan of %d methods", checks, len(methods))
	}
	for _, caller := range []*ssa.Function{invokeA, invokeB} {
		invokeSites := 0
		for _, block := range caller.Blocks {
			for _, instruction := range block.Instrs {
				call, ok := instruction.(ssa.CallInstruction)
				if !ok || !call.Common().IsInvoke() {
					continue
				}
				invokeSites++
				if got := len(candidates[call]); got != len(methods) {
					t.Fatalf("%s invoke candidate count = %d, want %d", caller.Name(), got, len(methods))
				}
			}
		}
		if invokeSites != 1 {
			t.Fatalf("%s invoke site count = %d, want 1", caller.Name(), invokeSites)
		}
	}
}

func TestRestrictedSSACHAIndexesOnlyAddressTakenScalarFunctions(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "address_taken.go", `package coroid
var callback func()
func selected() {}
func unrelated() {}
func install() { callback = selected }
func invoke() { callback() }
`)
	selected := packageFunction(t, pkg, "selected")
	unrelated := packageFunction(t, pkg, "unrelated")
	invoke := packageFunction(t, pkg, "invoke")
	functions := matchingFunctions(prog, func(fn *ssa.Function) bool {
		return fn.Pkg == pkg
	})
	candidates := restrictedSSACHACandidates(functions)
	call := onlyNonBuiltinCall(t, invoke)
	targets := candidates[call]
	if _, ok := targets[selected]; !ok {
		t.Fatalf("dynamic call candidates = %v, want address-taken selected", targets)
	}
	if _, ok := targets[unrelated]; ok {
		t.Fatalf("dynamic call candidates include unrelated same-signature function: %v", targets)
	}
}

func TestAnalyzeSSAEmissionUniverseRejectsMissingRootAndProgram(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", "package coroid; func root() {}; func other() {}")
	root := packageFunction(t, pkg, "root")
	other := packageFunction(t, pkg, "other")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AnalyzeSSA(prog, Roots{{Function: other, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse: universe,
	}); err == nil || !strings.Contains(err.Error(), "absent from the SSA emission universe") {
		t.Fatalf("missing root error = %v", err)
	}

	otherProg, _ := buildCoroTestSSA(t, "other.go", "package coroid; func root() {}")
	if _, err := AnalyzeSSA(otherProg, nil, SSAConfig{
		EmissionUniverse: universe,
	}); err == nil || !strings.Contains(err.Error(), "belongs to another program") {
		t.Fatalf("foreign universe error = %v", err)
	}
}

func TestAnalyzeSSAEmissionUniverseDeterministic(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
func receive(ch chan int) { <-ch }
func root(ch chan int) { receive(ch) }
`)
	receive := packageFunction(t, pkg, "receive")
	root := packageFunction(t, pkg, "root")
	universeA, err := NewSSAEmissionUniverse(prog, []*ssa.Function{root, receive})
	if err != nil {
		t.Fatal(err)
	}
	universeB, err := NewSSAEmissionUniverse(prog, []*ssa.Function{receive, root})
	if err != nil {
		t.Fatal(err)
	}
	analyze := func(universe *SSAEmissionUniverse) []byte {
		t.Helper()
		plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{
			EmissionUniverse: universe,
		})
		if err != nil {
			t.Fatal(err)
		}
		data, err := plan.BasePlan().Summary(SummaryMetadata{
			CoroABI:      "analysis-v0",
			SchedulerABI: "analysis-v0",
		}).MarshalStable()
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if a, b := analyze(universeA), analyze(universeB); !bytes.Equal(a, b) {
		t.Fatalf("summary depends on universe input order:\nA: %s\nB: %s", a, b)
	}
}
