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

func TestAnalyzeSSAStaticCallTargetUsesOrdinaryGoFixedPoint(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "static_target.go", `package coroid
var channel chan int
func declaration(int) int { return 0 }
func implementation(value int) int {
	if value == 0 {
		return <-channel
	}
	return value
}
func caller(value int) int { return declaration(value) }
func deferred(value int) { defer declaration(value) }
func spawned(value int) { go declaration(value) }
`)
	caller := packageFunction(t, pkg, "caller")
	deferred := packageFunction(t, pkg, "deferred")
	spawned := packageFunction(t, pkg, "spawned")
	declaration := packageFunction(t, pkg, "declaration")
	target := packageFunction(t, pkg, "implementation")
	call := onlyStaticTargetTestCall(t, caller, declaration)

	plan, err := AnalyzeSSA(prog, Roots{
		{Function: caller, Demand: SyncDemand},
		{Function: deferred, Demand: SyncDemand},
		{Function: spawned, Demand: AsyncDemand},
	}, SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyStaticCallTarget: func(owner *ssa.Function, candidate ssa.CallInstruction) (*ssa.Function, bool, error) {
			if candidate.Common().StaticCallee() == declaration {
				return target, true, nil
			}
			return nil, false, nil
		},
		ClassifyFunction: func(function *ssa.Function) (SSAFunctionPolicy, error) {
			// AnalyzeSSA owns propagation from the exact redirected edge. The
			// build frontend separately seeds every source `go` owner and exact
			// static spawn target for preemption before invoking AnalyzeSSA.
			if function == spawned || function == target {
				return SSAFunctionPolicy{Effect: YieldOnly}, nil
			}
			return SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	callerPlan := functionPlanFor(t, plan, caller)
	if !callerPlan.Effect.Contains(MayPark) || callerPlan.Exec.Contains(BlockForeign) {
		t.Fatalf("redirected caller plan = %+v", callerPlan)
	}
	callPlan, planned := plan.CallPlan(call)
	targetID, targetPlanned := plan.FunctionID(target)
	if !planned || !targetPlanned || callPlan.Kind != CallDirect ||
		callPlan.Open || len(callPlan.Targets) != 1 ||
		callPlan.Targets[0] != targetID {
		t.Fatalf("redirected CallPlan = %+v, %t; target=%q, %t", callPlan, planned, targetID, targetPlanned)
	}
	declarationPlan := functionPlanFor(t, plan, declaration)
	if declarationPlan.Demand != NoDemand || declarationPlan.Emission != EmitNone {
		t.Fatalf("source declaration unexpectedly demanded = %+v", declarationPlan)
	}
	deferCall := onlyStaticTargetTestInvocation(t, deferred, declaration)
	deferPlan, deferPlanned := plan.CallPlan(deferCall)
	deferredPlan := functionPlanFor(t, plan, deferred)
	if !deferPlanned || deferPlan.Kind != CallDefer ||
		len(deferPlan.Targets) != 1 || deferPlan.Targets[0] != targetID ||
		!deferredPlan.Effect.Contains(MayPark) {
		t.Fatalf("redirected defer = %+v, %t; owner=%+v", deferPlan, deferPlanned, deferredPlan)
	}
	spawnCall, ok := onlyStaticTargetTestInvocation(t, spawned, declaration).(*ssa.Go)
	if !ok {
		t.Fatal("redirected spawn fixture is not an ssa.Go")
	}
	spawnPlan, spawnPlanned := plan.CallPlan(spawnCall)
	resolved, resolvedPlan, resolveErr := plan.ResolveClosedStaticSpawn(spawnCall)
	if !spawnPlanned || spawnPlan.Kind != CallSpawn ||
		len(spawnPlan.Targets) != 1 || spawnPlan.Targets[0] != targetID ||
		resolveErr != nil || resolved != target ||
		resolvedPlan.Primary != PrimaryCoroutine {
		t.Fatalf(
			"redirected spawn = %+v, %t; resolved=%v plan=%+v error=%v",
			spawnPlan, spawnPlanned, resolved, resolvedPlan, resolveErr,
		)
	}
}

func TestAnalyzeSSAStaticCallTargetFailsClosed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "static_target_invalid.go", `package coroid
func declaration(int) int { return 0 }
func implementation(int) int { return 1 }
func mismatch(uint) int { return 2 }
func caller(value int) int { return declaration(value) }
`)
	caller := packageFunction(t, pkg, "caller")
	declaration := packageFunction(t, pkg, "declaration")
	target := packageFunction(t, pkg, "implementation")
	mismatch := packageFunction(t, pkg, "mismatch")
	call := onlyStaticTargetTestCall(t, caller, declaration)

	for _, test := range []struct {
		name       string
		target     *ssa.Function
		redirected bool
		want       string
	}{
		{name: "data without classification", target: target, want: "returned a target"},
		{name: "same target", target: declaration, redirected: true, want: "distinct same-program target"},
		{name: "signature mismatch", target: mismatch, redirected: true, want: "exact source signature"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: SyncDemand}}, SSAConfig{
				MaxPlainInstructions: -1,
				ClassifyStaticCallTarget: func(owner *ssa.Function, candidate ssa.CallInstruction) (*ssa.Function, bool, error) {
					if owner != caller || candidate != call {
						return nil, false, nil
					}
					return test.target, test.redirected, nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid static target error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSSAPlanRootFactoryRootsExcludeRawOnlyEntryOfDualFunction(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "root_factories.go", `package coroid
var channel chan int
func exported() { <-channel }
func runtimeInit() { exported() }
`)
	exported := packageFunction(t, pkg, "exported")
	runtimeInit := packageFunction(t, pkg, "runtimeInit")
	plan, err := AnalyzeSSA(prog, Roots{
		{Function: exported, RawPlainDemand: true},
		{Function: runtimeInit, Demand: AsyncDemand},
	}, SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (SSAFunctionPolicy, error) {
			return SSAFunctionPolicy{RawPlainEntry: function == exported}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	exportedPlan := functionPlanFor(t, plan, exported)
	if exportedPlan.Emission != EmitCoroutine ||
		exportedPlan.ManagedDemand == NoDemand ||
		!exportedPlan.RawPlainDemand ||
		!plan.HasRawPlainVariant(exported) {
		t.Fatalf("dual raw/managed function plan = %+v", exportedPlan)
	}
	factories := plan.RootFactoryRoots()
	if len(factories) != 1 || factories[0].Function != runtimeInit {
		t.Fatalf("root factory roots = %+v, want runtimeInit only", factories)
	}
	factories[0] = SSARootPlan{}
	again := plan.RootFactoryRoots()
	if len(again) != 1 || again[0].Function != runtimeInit {
		t.Fatalf("root factory roots are not immutable = %+v", again)
	}
}

func onlyStaticTargetTestCall(
	t *testing.T,
	owner, target *ssa.Function,
) *ssa.Call {
	t.Helper()
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common() != nil &&
				call.Common().StaticCallee() == target {
				return call
			}
		}
	}
	t.Fatalf("function %q has no static call to %q", owner.Name(), target.Name())
	return nil
}

func onlyStaticTargetTestInvocation(
	t *testing.T,
	owner, target *ssa.Function,
) ssa.CallInstruction {
	t.Helper()
	var found ssa.CallInstruction
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(ssa.CallInstruction)
			if !ok || call.Common() == nil ||
				call.Common().StaticCallee() != target {
				continue
			}
			if found != nil {
				t.Fatalf("function %q has multiple static invocations of %q", owner.Name(), target.Name())
			}
			found = call
		}
	}
	if found == nil {
		t.Fatalf("function %q has no static invocation of %q", owner.Name(), target.Name())
	}
	return found
}
