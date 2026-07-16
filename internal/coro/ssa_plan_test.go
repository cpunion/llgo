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
	"fmt"
	"strings"
	"testing"

	"golang.org/x/tools/go/ssa"
)

func TestAnalyzeSSABodySeedsCallsRootsAndLookup(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid

func plain() {}
func direct(ch chan int) { <-ch }
func caller(ch chan int) { direct(ch) }
func launch(ch chan int) { go direct(ch) }
func cleanup() { defer plain() }
func dynamic(fn func()) { fn() }
func loop() { for {} }
func nonblockingSelect(ch chan int) { select { case <-ch: default: } }
func blockingSelect(ch chan int) { select { case <-ch: } }
func send(ch chan int) { ch <- 1 }
`)
	plain := packageFunction(t, pkg, "plain")
	direct := packageFunction(t, pkg, "direct")
	caller := packageFunction(t, pkg, "caller")
	launch := packageFunction(t, pkg, "launch")
	cleanup := packageFunction(t, pkg, "cleanup")
	dynamic := packageFunction(t, pkg, "dynamic")
	loop := packageFunction(t, pkg, "loop")
	nonblocking := packageFunction(t, pkg, "nonblockingSelect")
	blocking := packageFunction(t, pkg, "blockingSelect")
	send := packageFunction(t, pkg, "send")

	plan, err := AnalyzeSSA(prog, Roots{
		{Function: plain, Demand: SyncDemand},
		{Function: plain, Demand: AsyncDemand},
		{Function: caller, Demand: SyncDemand},
		{Function: launch, Demand: AsyncDemand},
		{Function: cleanup, Demand: SyncDemand},
		{Function: dynamic, Demand: SyncDemand},
		{Function: loop, Demand: AsyncDemand},
		{Function: nonblocking, Demand: SyncDemand},
		{Function: blocking, Demand: SyncDemand},
		{Function: send, Demand: SyncDemand},
	}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}

	if got := functionPlanFor(t, plan, plain).Demand; got != BothDemand {
		t.Fatalf("plain demand = %s, want both", got)
	}
	if got := functionPlanFor(t, plan, direct); !got.Effect.Contains(MayPark) || got.Demand != AsyncDemand {
		t.Fatalf("direct plan = %+v, want MayPark/async", got)
	}
	if got := functionPlanFor(t, plan, caller); !got.Effect.Contains(MayPark) || got.Primary != PrimaryCoroutine || got.Demand != SyncDemand {
		t.Fatalf("caller plan = %+v, want sync-demand coroutine with MayPark", got)
	}
	if got := functionPlanFor(t, plan, launch); got.Effect != NoSuspend || got.Primary != PrimaryPlain {
		t.Fatalf("launch plan = %+v, spawn must not taint caller", got)
	}
	if got := functionPlanFor(t, plan, cleanup); !got.Exec.Contains(NeedsCleanupFrame) || got.Effect != NoSuspend {
		t.Fatalf("cleanup plan = %+v", got)
	}
	if got := functionPlanFor(t, plan, dynamic); !got.Effect.IsOpaque() || !got.Exec.Contains(OpaqueExec) {
		t.Fatalf("dynamic plan = %+v, want open-world opaque", got)
	}
	if got := functionPlanFor(t, plan, loop); !got.Effect.Contains(YieldOnly) || !got.Exec.Contains(NeedsPreempt) {
		t.Fatalf("loop plan = %+v, want preempt seed", got)
	}
	if got := functionPlanFor(t, plan, nonblocking); got.Effect != NoSuspend {
		t.Fatalf("nonblocking select effect = %s", got.Effect)
	}
	if got := functionPlanFor(t, plan, blocking); !got.Effect.Contains(MayPark) {
		t.Fatalf("blocking select effect = %s", got.Effect)
	}
	if got := functionPlanFor(t, plan, send); !got.Effect.Contains(MayPark) {
		t.Fatalf("send effect = %s", got.Effect)
	}

	id, ok := plan.FunctionID(caller)
	if !ok {
		t.Fatal("caller has no ID")
	}
	if resolved, ok := plan.Function(id); !ok || resolved != caller {
		t.Fatalf("reverse lookup = %v, %v", resolved, ok)
	}
	functions := plan.Functions()
	if len(functions) == 0 {
		t.Fatal("empty SSA plan")
	}
	functions[0].Function = nil
	if plan.Functions()[0].Function == nil {
		t.Fatal("Functions did not return a defensive slice")
	}
}

func TestSSAPlanResolvesOnlyClosedStaticSpawnAndKeepsOnePreemptibleTargetPrimary(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "spawn.go", `package coroid
var ch chan int
func plain(value int) { _ = value }
func suspending() { <-ch }
func launchPlain(value int) { plain(value); go plain(value) }
func launchSuspending() { go suspending() }
`)
	plain := packageFunction(t, pkg, "plain")
	suspending := packageFunction(t, pkg, "suspending")
	launchPlain := packageFunction(t, pkg, "launchPlain")
	launchSuspending := packageFunction(t, pkg, "launchSuspending")
	plan, err := AnalyzeSSA(prog, Roots{
		{Function: launchPlain, Demand: AsyncDemand},
		{Function: launchSuspending, Demand: AsyncDemand},
	}, SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == launchPlain || fn == launchSuspending || fn == plain || fn == suspending {
				return SSAFunctionPolicy{Effect: YieldOnly}, nil
			}
			return SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, plain); got.Emission != EmitCoroutine || got.Primary != PrimaryCoroutine || got.FuncRep != DirectCoro ||
		got.Demand != AsyncDemand || !got.Effect.Contains(YieldOnly) {
		t.Fatalf("bounded target plan = %+v, want one sync+spawn preemptible coroutine primary", got)
	}
	if got := functionPlanFor(t, plan, suspending); got.Emission != EmitCoroutine || got.Primary != PrimaryCoroutine ||
		got.FuncRep != DirectCoro || got.Demand != AsyncDemand {
		t.Fatalf("suspending target plan = %+v, want one async coroutine primary", got)
	}
	for _, owner := range []*ssa.Function{launchPlain, launchSuspending} {
		ownerPlan := functionPlanFor(t, plan, owner)
		if ownerPlan.DeclaredEffect != YieldOnly || !ownerPlan.LocalEffect.Contains(YieldOnly) || !ownerPlan.Effect.Contains(YieldOnly) ||
			ownerPlan.Emission != EmitCoroutine || ownerPlan.FuncRep != DirectCoro || ownerPlan.Demand != AsyncDemand {
			t.Fatalf("spawn owner %s plan = %+v", owner.Name(), ownerPlan)
		}
		var spawn *ssa.Go
		for _, block := range owner.Blocks {
			for _, instruction := range block.Instrs {
				if candidate, ok := instruction.(*ssa.Go); ok {
					spawn = candidate
				}
			}
		}
		if spawn == nil {
			t.Fatalf("spawn owner %s has no ssa.Go", owner.Name())
		}
		target, targetPlan, err := plan.ResolveClosedStaticSpawn(spawn)
		if err != nil {
			t.Fatalf("resolve spawn in %s: %v", owner.Name(), err)
		}
		callPlan, ok := plan.CallPlan(spawn)
		if !ok || callPlan.Kind != CallSpawn || callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 ||
			targetPlan.Demand != AsyncDemand {
			t.Fatalf("spawn in %s call/target plan = %+v / %+v", owner.Name(), callPlan, targetPlan)
		}
		if owner == launchPlain && target != plain || owner == launchSuspending && target != suspending {
			t.Fatalf("spawn in %s target = %v", owner.Name(), target)
		}
	}

	bothPlan, err := AnalyzeSSA(prog, Roots{{Function: launchPlain, Demand: BothDemand}}, SSAConfig{
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == launchPlain || fn == plain {
				return SSAFunctionPolicy{Effect: YieldOnly}, nil
			}
			return SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var bothSpawn *ssa.Go
	for _, block := range launchPlain.Blocks {
		for _, instruction := range block.Instrs {
			if spawn, ok := instruction.(*ssa.Go); ok {
				bothSpawn = spawn
			}
		}
	}
	if _, _, err := bothPlan.ResolveClosedStaticSpawn(bothSpawn); err == nil || !strings.Contains(err.Error(), "async-only") {
		t.Fatalf("BothDemand spawn owner error = %v, want async-only fail-closed", err)
	}
}

func TestSSAPlanRootsCanonicalJoinedSortedAndDefensive(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "roots.go", `package coroid
func original() {}
func replacement() {}
func other() {}
`)
	original := packageFunction(t, pkg, "original")
	replacement := packageFunction(t, pkg, "replacement")
	other := packageFunction(t, pkg, "other")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{other, replacement})
	if err != nil {
		t.Fatal(err)
	}
	config := SSAConfig{
		EmissionUniverse: universe,
		ResolveFunction: func(fn *ssa.Function) (*ssa.Function, bool, error) {
			if fn == original {
				return replacement, true, nil
			}
			return fn, universe.Contains(fn), nil
		},
	}
	inputs := Roots{
		{Function: original, Demand: SyncDemand},
		{Function: other, Demand: AsyncDemand},
		{Function: replacement, Demand: AsyncDemand},
		{Function: other, Demand: AsyncDemand},
	}
	plan, err := AnalyzeSSA(prog, inputs, config)
	if err != nil {
		t.Fatal(err)
	}
	inputs[0] = Root{}
	permuted, err := AnalyzeSSA(prog, Roots{
		{Function: replacement, Demand: BothDemand},
		{Function: other, Demand: AsyncDemand},
	}, config)
	if err != nil {
		t.Fatal(err)
	}

	wantDemand := map[*ssa.Function]Demand{
		replacement: BothDemand,
		other:       AsyncDemand,
	}
	got := plan.Roots()
	if len(got) != len(wantDemand) {
		t.Fatalf("roots = %+v, want %d canonical roots", got, len(wantDemand))
	}
	for index, root := range got {
		if index != 0 && got[index-1].ID >= root.ID {
			t.Fatalf("roots are not in strict FunctionID order: %+v", got)
		}
		if want, ok := wantDemand[root.Function]; !ok || root.Demand != want {
			t.Fatalf("root %d = %+v, want one of %+v", index, root, wantDemand)
		}
		if id, ok := plan.FunctionID(root.Function); !ok || id != root.ID {
			t.Fatalf("root %d ID = %q, FunctionID = %q, %v", index, root.ID, id, ok)
		}
	}
	permutedRoots := permuted.Roots()
	if len(permutedRoots) != len(got) {
		t.Fatalf("permuted roots = %+v, want %+v", permutedRoots, got)
	}
	for index := range got {
		if permutedRoots[index] != got[index] {
			t.Fatalf("permuted root %d = %+v, want %+v", index, permutedRoots[index], got[index])
		}
	}
	if got := functionPlanFor(t, plan, replacement).Demand; got != BothDemand {
		t.Fatalf("canonical replacement demand = %s, want both", got)
	}
	if _, ok := plan.FunctionPlan(original); ok {
		t.Fatal("aliased root loser entered the plan")
	}

	got[0] = SSARootPlan{}
	if fresh := plan.Roots(); len(fresh) == 0 || fresh[0].Function == nil || fresh[0].ID == "" || fresh[0].Demand == NoDemand {
		t.Fatalf("Roots did not return a defensive slice: %+v", fresh)
	}
	var nilPlan *SSAPlan
	if roots := nilPlan.Roots(); roots != nil {
		t.Fatalf("nil plan roots = %+v, want nil", roots)
	}
}

func TestSSAPlanFunctionPlanUsesExactSSAFunction(t *testing.T) {
	const source = `package coroid
func generic[T any](value T) T { return value }
func root() func() {
	_ = generic(1)
	return func() { _ = generic("value") }
}
`
	prog, pkg := buildCoroTestSSA(t, "source.go", source)
	root := packageFunction(t, pkg, "root")
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}

	wantID, ok := plan.FunctionID(root)
	if !ok {
		t.Fatal("root has no FunctionID")
	}
	want, ok := plan.BasePlan().Lookup(wantID)
	if !ok {
		t.Fatal("root FunctionID is absent from base plan")
	}
	if got, ok := plan.FunctionPlan(root); !ok || got != want {
		t.Fatalf("FunctionPlan(root) = %+v, %v; want %+v, true", got, ok, want)
	}

	if len(root.AnonFuncs) != 1 {
		t.Fatalf("root has %d closures, want 1", len(root.AnonFuncs))
	}
	if _, ok := plan.FunctionPlan(root.AnonFuncs[0]); !ok {
		t.Fatal("closure has no function plan")
	}

	instances := matchingFunctions(prog, func(fn *ssa.Function) bool {
		origin := fn.Origin()
		return origin != nil && origin.Name() == "generic"
	})
	if len(instances) != 2 {
		t.Fatalf("got %d generic instances, want 2: %v", len(instances), instances)
	}
	for _, instance := range instances {
		if _, ok := plan.FunctionPlan(instance); !ok {
			t.Fatalf("generic instance %s has no function plan", instance)
		}
	}

	genericOrigin := packageFunction(t, pkg, "generic")
	if _, ok := plan.FunctionPlan(genericOrigin); ok {
		t.Fatal("uninstantiated generic origin unexpectedly has a function plan")
	}
	if _, ok := plan.FunctionPlan(nil); ok {
		t.Fatal("nil SSA function unexpectedly has a function plan")
	}
	var nilPlan *SSAPlan
	if _, ok := nilPlan.FunctionPlan(root); ok {
		t.Fatal("nil SSA plan unexpectedly resolved a function")
	}

	_, otherPkg := buildCoroTestSSA(t, "source.go", source)
	otherRoot := packageFunction(t, otherPkg, "root")
	rootID, err := StableFunctionID(root, FunctionIDConfig{})
	if err != nil {
		t.Fatal(err)
	}
	otherRootID, err := StableFunctionID(otherRoot, FunctionIDConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if rootID != otherRootID {
		t.Fatalf("logically identical roots have different stable IDs: %s != %s", rootID, otherRootID)
	}
	if _, ok := plan.FunctionPlan(otherRoot); ok {
		t.Fatal("function from another SSA program unexpectedly matched by stable ID")
	}
}

func TestAnalyzeSSAFunctionReferencesPropagateDemandWithoutEffect(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "references.go", `package coroid

var channel chan int
var boxed any
var stored func()

func argumentTarget() {}
func directValueTarget() {}
func boxedTarget() { <-channel }
func returnedTarget() {}
func bindingTarget() { <-channel }
func spawnedTarget() {}
func deadPlainTarget() {}
func deadCoroTarget() { <-channel }
func consume(func()) {}

func owner() func() {
	if directValueTarget == nil {
		panic("unreachable")
	}
	consume(argumentTarget)
	boxed = boxedTarget
	bound := bindingTarget
	stored = func() { bound() }
	go spawnedTarget()
	return returnedTarget
}

func deadOwner() {
	consume(deadPlainTarget)
	boxed = deadCoroTarget
}
`)
	owner := packageFunction(t, pkg, "owner")
	plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}

	ownerPlan := functionPlanFor(t, plan, owner)
	if ownerPlan.Effect != NoSuspend || ownerPlan.Demand != SyncDemand || ownerPlan.Emission != EmitPlain {
		t.Fatalf("owner plan = %+v, function references must not propagate effect", ownerPlan)
	}
	checks := []struct {
		name     string
		demand   Demand
		emission BodyEmission
	}{
		{"directValueTarget", SyncDemand, EmitPlain},
		{"argumentTarget", SyncDemand, EmitPlain},
		{"boxedTarget", AsyncDemand, EmitCoroutine},
		{"returnedTarget", SyncDemand, EmitPlain},
		{"bindingTarget", AsyncDemand, EmitCoroutine},
		// A CallInstruction callee is represented only by its CallEdge. If the
		// go callee operand also became a ReferenceEdge, SyncDemand would join
		// this spawn demand and incorrectly produce BothDemand.
		{"spawnedTarget", AsyncDemand, EmitPlain},
		{"deadPlainTarget", NoDemand, EmitNone},
		{"deadCoroTarget", NoDemand, EmitNone},
		{"deadOwner", NoDemand, EmitNone},
	}
	for _, check := range checks {
		function := packageFunction(t, pkg, check.name)
		got := functionPlanFor(t, plan, function)
		if got.Demand != check.demand || got.Emission != check.emission {
			t.Fatalf("%s plan = %+v, want demand=%s emission=%s", check.name, got, check.demand, check.emission)
		}
	}

	if len(owner.AnonFuncs) != 1 {
		t.Fatalf("owner closures = %d, want 1", len(owner.AnonFuncs))
	}
	closure := functionPlanFor(t, plan, owner.AnonFuncs[0])
	if closure.Demand != AsyncDemand || closure.Emission != EmitCoroutine {
		t.Fatalf("materialized closure plan = %+v", closure)
	}
}

func TestAnalyzeSSAClassifiedDemandReferencesAreOwnerScopedAndFailClosed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "implicit_references.go", `package coroid

var channel chan int

func owner() {}
func deadOwner() {}
func suspendingMethod() { <-channel }
func deadMethod() {}
func outsideFrozenUniverse() {}
`)
	owner := packageFunction(t, pkg, "owner")
	deadOwner := packageFunction(t, pkg, "deadOwner")
	suspending := packageFunction(t, pkg, "suspendingMethod")
	deadMethod := packageFunction(t, pkg, "deadMethod")
	outside := packageFunction(t, pkg, "outsideFrozenUniverse")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{owner, deadOwner, suspending, deadMethod})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse: universe,
		ClassifyDemandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
			switch fn {
			case owner:
				return []*ssa.Function{suspending}, nil
			case deadOwner:
				return []*ssa.Function{deadMethod}, nil
			default:
				return nil, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerPlan := functionPlanFor(t, plan, owner)
	if ownerPlan.Effect != NoSuspend || ownerPlan.Emission != EmitPlain {
		t.Fatalf("owner plan = %+v, demand-only method address inherited its effect", ownerPlan)
	}
	suspendingPlan := functionPlanFor(t, plan, suspending)
	if suspendingPlan.Demand != AsyncDemand || suspendingPlan.Emission != EmitCoroutine || suspendingPlan.Primary != PrimaryCoroutine {
		t.Fatalf("suspending method plan = %+v, want demanded coroutine entry", suspendingPlan)
	}
	for _, fn := range []*ssa.Function{deadOwner, deadMethod} {
		got := functionPlanFor(t, plan, fn)
		if got.Demand != NoDemand || got.Emission != EmitNone {
			t.Fatalf("unreachable %s plan = %+v, want no demand and no emission", fn.Name(), got)
		}
	}

	_, err = AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse: universe,
		ClassifyDemandReferences: func(fn *ssa.Function) ([]*ssa.Function, error) {
			if fn == owner {
				return []*ssa.Function{outside}, nil
			}
			return nil, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "outside the effective emission universe") {
		t.Fatalf("missing frozen method error = %v", err)
	}
}

func TestAnalyzeSSAClassifiedLoweredCallsPropagateEffectAndAreOwnerScoped(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "lowered_calls.go", `package coroid

var channel chan int

func owner() {}
func deadOwner() {}
func plainHelper() {}
func suspendingHelper() { <-channel }
func deadHelper() { <-channel }
func outsideFrozenUniverse() {}
`)
	owner := packageFunction(t, pkg, "owner")
	deadOwner := packageFunction(t, pkg, "deadOwner")
	plain := packageFunction(t, pkg, "plainHelper")
	suspending := packageFunction(t, pkg, "suspendingHelper")
	dead := packageFunction(t, pkg, "deadHelper")
	outside := packageFunction(t, pkg, "outsideFrozenUniverse")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{owner, deadOwner, plain, suspending, dead})
	if err != nil {
		t.Fatal(err)
	}
	classify := func(fn *ssa.Function) ([]SSALoweredCall, error) {
		switch fn {
		case owner:
			// Deliberately reverse logical order. The frozen plan must sort it.
			return []SSALoweredCall{{LogicalName: "runtime.suspend", Target: suspending}, {LogicalName: "runtime.plain", Target: plain}}, nil
		case deadOwner:
			return []SSALoweredCall{{LogicalName: "runtime.dead", Target: dead}}, nil
		default:
			return nil, nil
		}
	}
	plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse:     universe,
		ClassifyLoweredCalls: classify,
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ownerPlan := functionPlanFor(t, plan, owner)
	if !ownerPlan.Effect.Contains(MayPark) || ownerPlan.Demand != SyncDemand || ownerPlan.Emission != EmitCoroutine {
		t.Fatalf("owner plan = %+v, want lowered helper effect and coroutine emission", ownerPlan)
	}
	if got := functionPlanFor(t, plan, plain); got.Demand != SyncDemand || got.Emission != EmitPlain {
		t.Fatalf("plain helper plan = %+v", got)
	}
	if got := functionPlanFor(t, plan, suspending); got.Demand != AsyncDemand || got.Emission != EmitCoroutine {
		t.Fatalf("suspending helper plan = %+v", got)
	}
	if got := functionPlanFor(t, plan, deadOwner); !got.Effect.Contains(MayPark) || got.Demand != NoDemand || got.Emission != EmitNone {
		t.Fatalf("dead owner plan = %+v, want analyzed effect without entry demand", got)
	}
	if got := functionPlanFor(t, plan, dead); got.Demand != NoDemand || got.Emission != EmitNone {
		t.Fatalf("dead helper plan = %+v, want no demand", got)
	}

	calls := plan.LoweredCalls(owner)
	if len(calls) != 2 || calls[0].LogicalName != "runtime.plain" || calls[0].Target != plain || calls[1].LogicalName != "runtime.suspend" || calls[1].Target != suspending {
		t.Fatalf("owner lowered calls = %+v, want sorted exact mapping", calls)
	}
	calls[0].Target = dead
	if target, ok := plan.ResolveLoweredCall(owner, "runtime.plain"); !ok || target != plain {
		t.Fatalf("ResolveLoweredCall(runtime.plain) = %v, %v", target, ok)
	}
	if _, ok := plan.ResolveLoweredCall(owner, "runtime.missing"); ok {
		t.Fatal("missing lowered call unexpectedly resolved")
	}
	if got := plan.LoweredCalls(outside); got != nil {
		t.Fatalf("outside owner lowered calls = %v, want nil", got)
	}

	// Permuting classifier order cannot change fixed-point results or the
	// immutable logical-name mapping.
	permuted, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse: universe,
		ClassifyLoweredCalls: func(fn *ssa.Function) ([]SSALoweredCall, error) {
			calls, err := classify(fn)
			if len(calls) == 2 {
				calls[0], calls[1] = calls[1], calls[0]
			}
			return calls, err
		},
		MaxPlainInstructions: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, permuted, owner); got != ownerPlan {
		t.Fatalf("permuted owner plan = %+v, want %+v", got, ownerPlan)
	}
	if got := permuted.LoweredCalls(owner); len(got) != 2 || got[0].LogicalName != "runtime.plain" || got[1].LogicalName != "runtime.suspend" {
		t.Fatalf("permuted lowered calls = %+v", got)
	}

	_, err = AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, SSAConfig{
		EmissionUniverse: universe,
		ClassifyLoweredCalls: func(fn *ssa.Function) ([]SSALoweredCall, error) {
			if fn == owner {
				return []SSALoweredCall{{LogicalName: "runtime.outside", Target: outside}}, nil
			}
			return nil, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "outside the effective emission universe") {
		t.Fatalf("missing frozen lowered target error = %v", err)
	}
}

func TestAnalyzeSSAUnwindOnlyLoweredCallDoesNotPolluteNormalReturnPlan(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "lowered_unwind_only.go", `package coroid
func owner() {}
func helper() {}
`)
	owner := packageFunction(t, pkg, "owner")
	helper := packageFunction(t, pkg, "helper")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{owner, helper})
	if err != nil {
		t.Fatal(err)
	}
	build := func(unwindOnly bool) *SSAPlan {
		t.Helper()
		plan, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, SSAConfig{
			EmissionUniverse: universe,
			ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
				if fn == helper {
					return SSAFunctionPolicy{Effect: OpaqueSuspend, Exec: IRQUnsafe | OpaqueExec}, nil
				}
				return SSAFunctionPolicy{}, nil
			},
			ClassifyLoweredCalls: func(fn *ssa.Function) ([]SSALoweredCall, error) {
				if fn == owner {
					return []SSALoweredCall{{LogicalName: "runtime.helper", Target: helper, UnwindOnly: unwindOnly}}, nil
				}
				return nil, nil
			},
			MaxPlainInstructions: -1,
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	unwind := build(true)
	unwindOwner := functionPlanFor(t, unwind, owner)
	if unwindOwner.Effect != NoSuspend || unwindOwner.Exec.Contains(IRQUnsafe|OpaqueExec) || unwindOwner.Emission != EmitPlain {
		t.Fatalf("unwind-only owner plan = %+v, want an unpolluted normal-return plain body", unwindOwner)
	}
	unwindTarget := functionPlanFor(t, unwind, helper)
	if unwindTarget.Demand != SyncDemand || unwindTarget.Emission != EmitCoroutine || !unwindTarget.Effect.IsOpaque() {
		t.Fatalf("unwind-only target plan = %+v, want retained synchronous demand and coroutine emission", unwindTarget)
	}
	if got := unwind.LoweredCalls(owner); len(got) != 1 || !got[0].UnwindOnly || got[0].Target != helper {
		t.Fatalf("unwind-only frozen calls = %+v", got)
	}

	ordinary := build(false)
	ordinaryOwner := functionPlanFor(t, ordinary, owner)
	if !ordinaryOwner.Effect.IsOpaque() || !ordinaryOwner.Exec.Contains(IRQUnsafe|OpaqueExec) || ordinaryOwner.Emission != EmitCoroutine {
		t.Fatalf("normal-return-reachable owner plan = %+v, want exact target effects propagated", ordinaryOwner)
	}
	if got := functionPlanFor(t, ordinary, helper); got.Demand != AsyncDemand {
		t.Fatalf("normal-return-reachable target demand = %s, want async", got.Demand)
	}
}

func TestAnalyzeSSAClassifiedLoweredCallsFailClosed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "lowered_calls_invalid.go", `package coroid
func owner() {}
func helper() {}
func alias() {}
`)
	owner := packageFunction(t, pkg, "owner")
	helper := packageFunction(t, pkg, "helper")
	alias := packageFunction(t, pkg, "alias")
	universe, err := NewSSAEmissionUniverse(prog, []*ssa.Function{owner, helper})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		calls []SSALoweredCall
		want  string
	}{
		{name: "empty name", calls: []SSALoweredCall{{Target: helper}}, want: "empty logical name"},
		{name: "nil target", calls: []SSALoweredCall{{LogicalName: "runtime.nil"}}, want: "nil target"},
		{name: "duplicate name", calls: []SSALoweredCall{{LogicalName: "runtime.same", Target: helper}, {LogicalName: "runtime.same", Target: helper}}, want: "duplicated"},
		{name: "alias", calls: []SSALoweredCall{{LogicalName: "runtime.alias", Target: alias}}, want: "not the exact canonical function"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := AnalyzeSSA(prog, Roots{{Function: owner, Demand: SyncDemand}}, SSAConfig{
				EmissionUniverse: universe,
				ResolveFunction: func(fn *ssa.Function) (*ssa.Function, bool, error) {
					if fn == alias {
						return helper, true, nil
					}
					return fn, universe.Contains(fn), nil
				},
				ClassifyLoweredCalls: func(fn *ssa.Function) ([]SSALoweredCall, error) {
					if fn == owner {
						return test.calls, nil
					}
					return nil, nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("AnalyzeSSA error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAnalyzeSSADynamicOpenAndClosedWorld(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid

var channel chan int
type Interface interface { Method() }
type Concrete struct{}
func (Concrete) Method() { <-channel }
func invoke(value Interface) { value.Method() }
func use() { invoke(Concrete{}) }
`)
	invoke := packageFunction(t, pkg, "invoke")
	methodFunctions := matchingFunctions(prog, func(fn *ssa.Function) bool {
		return fn.Name() == "Method" && fn.Signature.Recv() != nil
	})
	if len(methodFunctions) == 0 {
		t.Fatal("Concrete.Method SSA function not found")
	}

	open, err := AnalyzeSSA(prog, Roots{{Function: invoke, Demand: SyncDemand}}, SSAConfig{DynamicResolution: DynamicCHAOpen})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, open, invoke); !got.Effect.IsOpaque() {
		t.Fatalf("open-world invoke effect = %s, want opaque", got.Effect)
	}

	closed, err := AnalyzeSSA(prog, Roots{{Function: invoke, Demand: SyncDemand}}, SSAConfig{DynamicResolution: DynamicCHAClosed})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, closed, invoke); got.Effect.IsOpaque() || !got.Effect.Contains(MayPark) {
		t.Fatalf("closed-world invoke effect = %s, want known MayPark", got.Effect)
	}
	foundDispatch := false
	for _, fn := range methodFunctions {
		if _, ok := closed.FunctionID(fn); !ok {
			continue
		}
		if functionPlanFor(t, closed, fn).FuncRep == Dispatch {
			foundDispatch = true
		}
	}
	if !foundDispatch {
		t.Fatal("dynamic CHA candidate was not conservatively marked Dispatch")
	}
}

func TestAnalyzeSSAClosedWorldEmptyCandidateRemainsOpaque(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
func dynamic(fn func(int, int, int) int) { _ = fn(1, 2, 3) }
`)
	dynamic := packageFunction(t, pkg, "dynamic")
	plan, err := AnalyzeSSA(prog, Roots{{Function: dynamic, Demand: SyncDemand}}, SSAConfig{DynamicResolution: DynamicCHAClosed})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, dynamic); !got.Effect.IsOpaque() {
		t.Fatalf("empty dynamic candidate set became clean: %+v", got)
	}
}

func TestAnalyzeSSAClosedWorldExcludedCandidateRemainsOpaque(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
func first() {}
func second() {}
func invoke(fn func()) { fn() }
func use() { invoke(first); invoke(second) }
`)
	invoke := packageFunction(t, pkg, "invoke")
	second := packageFunction(t, pkg, "second")
	plan, err := AnalyzeSSA(prog, Roots{{Function: invoke, Demand: SyncDemand}}, SSAConfig{
		DynamicResolution: DynamicCHAClosed,
		Include: func(fn *ssa.Function) (bool, error) {
			return fn != second, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, invoke); !got.Effect.IsOpaque() {
		t.Fatalf("excluded dynamic candidate made closed-world call look complete: %+v", got)
	}
}

func TestAnalyzeSSAExternalPoliciesPreserveCallSyntax(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
func external()
func direct() { external() }
func deferred() { defer external() }
func spawned() { go external() }
`)
	external := packageFunction(t, pkg, "external")
	direct := packageFunction(t, pkg, "direct")
	deferred := packageFunction(t, pkg, "deferred")
	spawned := packageFunction(t, pkg, "spawned")
	roots := Roots{
		{Function: direct, Demand: SyncDemand},
		{Function: deferred, Demand: SyncDemand},
		{Function: spawned, Demand: AsyncDemand},
	}

	unknown, err := AnalyzeSSA(prog, roots, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, unknown, external); got.External != ExternalUnknownManaged || got.FuncRep != Dispatch {
		t.Fatalf("default external plan = %+v", got)
	}
	if got := functionPlanFor(t, unknown, direct); !got.Effect.IsOpaque() {
		t.Fatalf("unknown managed direct = %+v", got)
	}
	if got := functionPlanFor(t, unknown, spawned); got.Effect != NoSuspend {
		t.Fatalf("unknown managed spawn tainted caller: %+v", got)
	}

	known, err := AnalyzeSSA(prog, roots, SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == external {
				return SSAFunctionPolicy{Effect: WaitPlatform, External: ExternalKnown, OverrideExternal: true}, nil
			}
			return SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, known, direct); got.Effect.IsOpaque() || !got.Effect.Contains(WaitPlatform) {
		t.Fatalf("known external direct = %+v", got)
	}
	if got := functionPlanFor(t, known, deferred); !got.Effect.Contains(WaitPlatform) || !got.Exec.Contains(NeedsCleanupFrame) {
		t.Fatalf("known external defer = %+v", got)
	}
	if got := functionPlanFor(t, known, spawned); got.Effect != NoSuspend {
		t.Fatalf("known external spawn = %+v", got)
	}

	foreign, err := AnalyzeSSA(prog, roots, SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == external {
				return SSAFunctionPolicy{External: ExternalUnknownForeign, OverrideExternal: true}, nil
			}
			return SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, foreign, external); !got.Exec.Contains(BlockForeign|IRQUnsafe) || got.Effect != NoSuspend {
		t.Fatalf("foreign external = %+v", got)
	}
	if got := functionPlanFor(t, foreign, direct); !got.Effect.Contains(WaitForeign) {
		t.Fatalf("foreign direct = %+v", got)
	}
	if got := functionPlanFor(t, foreign, deferred); !got.Effect.Contains(WaitForeign) || !got.Exec.Contains(NeedsCleanupFrame) {
		t.Fatalf("foreign defer = %+v", got)
	}
	if got := functionPlanFor(t, foreign, spawned); got.Effect != NoSuspend {
		t.Fatalf("foreign spawn = %+v", got)
	}
}

func TestAnalyzeSSAStaticCostSeed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
func straight(a int) int { a++; a++; a++; return a }
`)
	straight := packageFunction(t, pkg, "straight")
	plan, err := AnalyzeSSA(prog, Roots{{Function: straight, Demand: AsyncDemand}}, SSAConfig{MaxPlainInstructions: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, straight); !got.Exec.Contains(NeedsPreempt) || !got.Effect.Contains(YieldOnly) {
		t.Fatalf("static cost did not seed preemption: %+v", got)
	}
}

func TestAnalyzeSSATrustedNoPreemptClearsOnlyScannerSeed(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "trusted_no_preempt.go", `package coroid
func trustedLoop() { for {} }
func ordinaryLoop() { for {} }
func recursive() { recursive() }
func explicitPreempt() { for {} }
`)
	trustedLoop := packageFunction(t, pkg, "trustedLoop")
	ordinaryLoop := packageFunction(t, pkg, "ordinaryLoop")
	recursive := packageFunction(t, pkg, "recursive")
	explicitPreempt := packageFunction(t, pkg, "explicitPreempt")
	plan, err := AnalyzeSSA(prog, Roots{
		{Function: trustedLoop, Demand: AsyncDemand},
		{Function: ordinaryLoop, Demand: AsyncDemand},
		{Function: recursive, Demand: AsyncDemand},
		{Function: explicitPreempt, Demand: AsyncDemand},
	}, SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			switch fn {
			case trustedLoop, recursive:
				return SSAFunctionPolicy{TrustedNoPreempt: true}, nil
			case explicitPreempt:
				return SSAFunctionPolicy{TrustedNoPreempt: true, Exec: NeedsPreempt}, nil
			default:
				return SSAFunctionPolicy{}, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	trusted := functionPlanFor(t, plan, trustedLoop)
	if trusted.Exec.Contains(NeedsPreempt) || trusted.Effect.MaySuspend() || trusted.Emission != EmitPlain {
		t.Fatalf("trusted loop plan = %+v, want scanner preemption suppressed and one plain body", trusted)
	}
	ordinary := functionPlanFor(t, plan, ordinaryLoop)
	if !ordinary.Exec.Contains(NeedsPreempt) || !ordinary.Effect.Contains(YieldOnly) || ordinary.Emission != EmitCoroutine {
		t.Fatalf("ordinary loop plan = %+v, want scanner preemption and coroutine body", ordinary)
	}
	recursivePlan := functionPlanFor(t, plan, recursive)
	if !recursivePlan.Recursive || !recursivePlan.Exec.Contains(NeedsPreempt) ||
		!recursivePlan.Effect.Contains(YieldOnly) || recursivePlan.Emission != EmitCoroutine {
		t.Fatalf("recursive trusted plan = %+v, want recursion preemption preserved", recursivePlan)
	}
	explicit := functionPlanFor(t, plan, explicitPreempt)
	if !explicit.Exec.Contains(NeedsPreempt) || !explicit.Effect.Contains(YieldOnly) || explicit.Emission != EmitCoroutine {
		t.Fatalf("explicit trusted preemption plan = %+v, want declared preemption preserved", explicit)
	}
}

func TestAnalyzeSSAIgnoreBodyRequiresAndUsesExternalFrontendPolicy(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "ignored_external_body.go", `package coroid
var channel chan int
var sink func()
func hiddenManaged() { <-channel }
func externalFallback(fn func()) {
	sink = hiddenManaged
	fn()
	externalFallback(fn)
	for { <-channel }
}
func caller() { externalFallback(nil) }
`)
	external := packageFunction(t, pkg, "externalFallback")
	hiddenManaged := packageFunction(t, pkg, "hiddenManaged")
	caller := packageFunction(t, pkg, "caller")
	plan, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: AsyncDemand}}, SSAConfig{
		ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
			if fn == external {
				return SSAFunctionPolicy{
					IgnoreBody:       true,
					External:         ExternalUnknownForeign,
					OverrideExternal: true,
				}, nil
			}
			return SSAFunctionPolicy{}, nil
		},
		ClassifyUnknownCall: func(owner *ssa.Function, _ ssa.CallInstruction) (UnknownTarget, error) {
			if owner == external {
				return UnknownManaged, fmt.Errorf("visited ignored body during unknown-call classification")
			}
			return UnknownManaged, nil
		},
		ClassifyElidedCall: func(owner *ssa.Function, _ ssa.CallInstruction) (bool, error) {
			if owner == external {
				return false, fmt.Errorf("visited ignored body during elided-call classification")
			}
			return false, nil
		},
		ClassifyDirectPlainCallArgument: func(owner *ssa.Function, _ ssa.CallInstruction, _ int) (bool, error) {
			if owner == external {
				return false, fmt.Errorf("visited ignored body during direct-plain argument classification")
			}
			return false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	externalPlan := functionPlanFor(t, plan, external)
	if externalPlan.External != ExternalUnknownForeign || externalPlan.Effect != NoSuspend ||
		externalPlan.Exec.Contains(NeedsPreempt) || !externalPlan.Exec.Contains(BlockForeign|IRQUnsafe) ||
		externalPlan.Emission != EmitExternal || externalPlan.FuncRep != DirectPlain || externalPlan.Recursive {
		t.Fatalf("ignored external fallback plan = %+v", externalPlan)
	}
	if !plan.IgnoresBody(external) || plan.IgnoresBody(caller) || plan.IgnoresBody(nil) {
		t.Fatal("ignored-body identity was not retained exactly")
	}
	callerPlan := functionPlanFor(t, plan, caller)
	if !callerPlan.Effect.Contains(WaitForeign) || callerPlan.Effect.IsOpaque() {
		t.Fatalf("caller plan = %+v, want precise foreign wait", callerPlan)
	}
	if hidden := functionPlanFor(t, plan, hiddenManaged); hidden.Demand != NoDemand || hidden.Emission != EmitNone || hidden.FuncRep == Dispatch {
		t.Fatalf("ignored body leaked a reference/demand to hidden managed target: %+v", hidden)
	}
	if _, ok := plan.ValuePlan(external.Params[0]); ok {
		t.Fatal("ignored external parameter unexpectedly has an SSAValuePlan")
	}
	if _, ok := plan.ValuePlan(hiddenManaged); ok {
		t.Fatal("function value used only by ignored body unexpectedly has an SSAValuePlan")
	}
	for _, block := range external.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(ssa.CallInstruction); ok {
				if _, planned := plan.CallPlan(call); planned {
					t.Fatalf("ignored body call %q unexpectedly has a CallPlan", call)
				}
				if plan.ElidesCall(call) {
					t.Fatalf("ignored body call %q unexpectedly entered elided-call identity", call)
				}
			}
			if value, ok := instruction.(ssa.Value); ok {
				if _, planned := plan.ValuePlan(value); planned {
					t.Fatalf("ignored body value %q unexpectedly has an SSAValuePlan", value)
				}
			}
		}
	}

	for _, test := range []struct {
		name   string
		policy SSAFunctionPolicy
	}{
		{name: "no override", policy: SSAFunctionPolicy{IgnoreBody: true}},
		{name: "defined", policy: SSAFunctionPolicy{IgnoreBody: true, External: Defined, OverrideExternal: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := AnalyzeSSA(prog, Roots{{Function: caller, Demand: AsyncDemand}}, SSAConfig{
				ClassifyFunction: func(fn *ssa.Function) (SSAFunctionPolicy, error) {
					if fn == external {
						return test.policy, nil
					}
					return SSAFunctionPolicy{}, nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), "IgnoreBody requires an explicit non-defined external classification") {
				t.Fatalf("AnalyzeSSA error = %v", err)
			}
		})
	}
}

func TestIgnoredBodyFiltersDynamicCandidatesBeforeTargetResolution(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "ignored_candidate_resolver.go", `package coroid
func poison() {}
func externalFallback(fn func()) { fn() }
func live() {}
`)
	external := packageFunction(t, pkg, "externalFallback")
	poison := packageFunction(t, pkg, "poison")
	call := onlyNonBuiltinCall(t, external)
	poisonResolved := false
	canonicalizer := newSSAFunctionCanonicalizer(prog, SSAConfig{
		ResolveFunction: func(fn *ssa.Function) (*ssa.Function, bool, error) {
			if fn == poison {
				poisonResolved = true
				return nil, false, fmt.Errorf("poison candidate resolver")
			}
			return fn, true, nil
		},
	})
	filtered, err := filterSSADynamicCandidateSites(
		map[ssa.CallInstruction]map[*ssa.Function]struct{}{call: {poison: {}}},
		map[*ssa.Function]bool{packageFunction(t, pkg, "live"): true},
		canonicalizer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 {
		t.Fatalf("ignored-body dynamic candidates survived site filter: %v", filtered)
	}
	if _, err := canonicalizeSSADynamicCandidates(filtered, canonicalizer); err != nil {
		t.Fatal(err)
	}
	if poisonResolved {
		t.Fatal("candidate reachable only from an ignored body reached resolver canonicalization")
	}
}

func TestAnalyzeSSAStaticCostIgnoresDebugRefs(t *testing.T) {
	const source = `package coroid

func target(value int) int {
	value++
	return value * 2
}
`
	baseMode := ssa.SanityCheckFunctions | ssa.InstantiateGenerics
	plainProg, plainPkg := buildCoroTestSSAWithMode(t, "plain.go", source, baseMode)
	debugProg, debugPkg := buildCoroTestSSAWithMode(t, "debug.go", source, baseMode|ssa.GlobalDebug)
	plainTarget := packageFunction(t, plainPkg, "target")
	debugTarget := packageFunction(t, debugPkg, "target")

	nonDebugInstructions := 0
	for _, block := range plainTarget.Blocks {
		for _, instruction := range block.Instrs {
			if _, debug := instruction.(*ssa.DebugRef); !debug {
				nonDebugInstructions++
			}
		}
	}
	if nonDebugInstructions == 0 {
		t.Fatal("target has no real SSA instructions")
	}
	debugRefs := 0
	for _, block := range debugTarget.Blocks {
		for _, instruction := range block.Instrs {
			if _, debug := instruction.(*ssa.DebugRef); debug {
				debugRefs++
			}
		}
	}
	if debugRefs == 0 {
		t.Fatal("GlobalDebug target has no DebugRef instructions")
	}

	plainPlan, err := AnalyzeSSA(plainProg, Roots{{Function: plainTarget, Demand: AsyncDemand}}, SSAConfig{MaxPlainInstructions: nonDebugInstructions})
	if err != nil {
		t.Fatal(err)
	}
	debugPlan, err := AnalyzeSSA(debugProg, Roots{{Function: debugTarget, Demand: AsyncDemand}}, SSAConfig{MaxPlainInstructions: nonDebugInstructions})
	if err != nil {
		t.Fatal(err)
	}
	plainFunction := functionPlanFor(t, plainPlan, plainTarget)
	debugFunction := functionPlanFor(t, debugPlan, debugTarget)
	if plainFunction.Exec.Contains(NeedsPreempt) || debugFunction.Exec.Contains(NeedsPreempt) {
		t.Fatalf("debug refs changed static cost: plain=%s debug=%s", plainFunction.Exec, debugFunction.Exec)
	}
	if plainFunction.Primary != debugFunction.Primary || plainFunction.Effect != debugFunction.Effect {
		t.Fatalf("debug refs changed plan: plain=%+v debug=%+v", plainFunction, debugFunction)
	}
}

func TestAnalyzeSSADefinedBodiesConservativelyMayUnwind(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
func plain() {}
func deferredPanic() { defer panic("boom") }
func implicitPanic(values []int, index int) int { return values[index] }
`)
	plain := packageFunction(t, pkg, "plain")
	deferred := packageFunction(t, pkg, "deferredPanic")
	implicit := packageFunction(t, pkg, "implicitPanic")
	plan, err := AnalyzeSSA(prog, Roots{
		{Function: plain, Demand: SyncDemand},
		{Function: deferred, Demand: SyncDemand},
		{Function: implicit, Demand: SyncDemand},
	}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, plain); !got.Exec.Contains(MayUnwind) {
		t.Fatalf("plain defined body lacks conservative MayUnwind: %+v", got)
	}
	if got := functionPlanFor(t, plan, deferred); !got.Exec.Contains(MayUnwind | NeedsCleanupFrame) {
		t.Fatalf("deferred panic flags = %s", got.Exec)
	}
	if got := functionPlanFor(t, plan, implicit); !got.Exec.Contains(MayUnwind) {
		t.Fatalf("implicit panic flags = %s", got.Exec)
	}
}

func TestAnalyzeSSASkipsGenericOriginsButKeepsInstances(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
func generic[T any](value T) T { return value }
func root() { _ = generic(1) }
`)
	root := packageFunction(t, pkg, "root")
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}
	foundInstance := false
	for _, function := range plan.Functions() {
		params := function.Function.TypeParams()
		if params != nil && params.Len() != 0 && len(function.Function.TypeArgs()) == 0 {
			t.Fatalf("uninstantiated generic origin entered plan: %s", function.Function)
		}
		if origin := function.Function.Origin(); origin != nil && origin.Name() == "generic" {
			foundInstance = true
		}
	}
	if !foundInstance {
		t.Fatal("generic instance is absent from plan")
	}
}

func TestAnalyzeSSALargeDirectChainIsLinearSized(t *testing.T) {
	const functionCount = 1000
	var source strings.Builder
	source.WriteString("package coroid\nvar channel chan int\n")
	for i := 0; i < functionCount-1; i++ {
		fmt.Fprintf(&source, "func function%04d() { function%04d() }\n", i, i+1)
	}
	fmt.Fprintf(&source, "func function%04d() { <-channel }\n", functionCount-1)

	prog, pkg := buildCoroTestSSA(t, "large.go", source.String())
	root := packageFunction(t, pkg, "function0000")
	plan, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, plan, root); !got.Effect.Contains(MayPark) {
		t.Fatalf("root effect = %s, want MayPark", got.Effect)
	}
	if got := len(plan.Functions()); got < functionCount {
		t.Fatalf("plan has %d functions, want at least %d", got, functionCount)
	}
	for _, function := range plan.Functions() {
		if got, want := len(function.Plan.ID), len(FunctionIDSchema)+1+64; got != want {
			t.Fatalf("FunctionID length = %d, want %d", got, want)
		}
	}
}

func TestAnalyzeSSAExcludedAndDynamicForeignCalls(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "source.go", `package coroid
func target() {}
func static() { target() }
func dynamic(fn func()) { fn() }
func deferred(fn func()) { defer fn() }
func spawned(fn func()) { go fn() }
`)
	target := packageFunction(t, pkg, "target")
	static := packageFunction(t, pkg, "static")
	dynamic := packageFunction(t, pkg, "dynamic")
	deferred := packageFunction(t, pkg, "deferred")
	spawned := packageFunction(t, pkg, "spawned")

	excluded, err := AnalyzeSSA(prog, Roots{{Function: static, Demand: SyncDemand}}, SSAConfig{
		Include: func(fn *ssa.Function) (bool, error) { return fn != target, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, excluded, static); !got.Effect.IsOpaque() {
		t.Fatalf("call to excluded target was not conservative: %+v", got)
	}

	foreign, err := AnalyzeSSA(prog, Roots{
		{Function: dynamic, Demand: SyncDemand},
		{Function: deferred, Demand: SyncDemand},
		{Function: spawned, Demand: AsyncDemand},
	}, SSAConfig{
		DynamicResolution: DynamicCHAClosed,
		ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (UnknownTarget, error) {
			return UnknownForeign, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, foreign, dynamic); !got.Effect.Contains(WaitForeign) || got.Effect.IsOpaque() || !got.Exec.Contains(IRQUnsafe) {
		t.Fatalf("dynamic foreign call = %+v", got)
	}
	if got := functionPlanFor(t, foreign, deferred); !got.Effect.Contains(WaitForeign) || !got.Exec.Contains(IRQUnsafe|NeedsCleanupFrame) {
		t.Fatalf("deferred dynamic foreign call = %+v", got)
	}
	if got := functionPlanFor(t, foreign, spawned); got.Effect != NoSuspend || got.Exec.Contains(IRQUnsafe) {
		t.Fatalf("spawned dynamic foreign call = %+v", got)
	}
}

func TestAnalyzeSSAFrontendElidedStaticCall(t *testing.T) {
	prog, pkg := buildCoroTestSSA(t, "elided.go", `package coroid
func target() {}
func root() { target() }
func dynamic(fn func()) { fn() }
func spawned() { go target() }
`)
	target := packageFunction(t, pkg, "target")
	root := packageFunction(t, pkg, "root")
	rootCall := onlyNonBuiltinCall(t, root)
	includeWithoutTarget := func(fn *ssa.Function) (bool, error) { return fn != target, nil }

	conservative, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{
		Include: includeWithoutTarget,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, conservative, root); !got.Effect.IsOpaque() {
		t.Fatalf("unresolved static call was not conservative: %+v", got)
	}

	elided, err := AnalyzeSSA(prog, Roots{{Function: root, Demand: SyncDemand}}, SSAConfig{
		Include: includeWithoutTarget,
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			return call == rootCall, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := functionPlanFor(t, elided, root); got.Effect != NoSuspend || got.Primary != PrimaryPlain || got.Emission != EmitPlain {
		t.Fatalf("frontend-elided root plan = %+v, want plain no-suspend", got)
	}
	if _, ok := elided.CallPlan(rootCall); ok {
		t.Fatal("frontend-elided call unexpectedly has a CallPlan")
	}
	if !elided.ElidesCall(rootCall) {
		t.Fatal("frontend-elided call identity was not retained")
	}
	if conservative.ElidesCall(rootCall) || elided.ElidesCall(nil) {
		t.Fatal("elided-call query accepted an ordinary or nil call")
	}

	for _, name := range []string{"dynamic", "spawned"} {
		fn := packageFunction(t, pkg, name)
		_, err := AnalyzeSSA(prog, Roots{{Function: fn, Demand: SyncDemand}}, SSAConfig{
			ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
				return call == onlyNonBuiltinCall(t, fn), nil
			},
		})
		if err == nil || !strings.Contains(err.Error(), "must be a direct static call") {
			t.Fatalf("elide %s error = %v", name, err)
		}
	}
}

func TestAnalyzeSSAValidation(t *testing.T) {
	if _, err := AnalyzeSSA(nil, nil, SSAConfig{}); err == nil || !strings.Contains(err.Error(), "nil SSA") {
		t.Fatalf("nil program error = %v", err)
	}
	prog, pkg := buildCoroTestSSA(t, "source.go", "package coroid; func root() {}")
	root := packageFunction(t, pkg, "root")
	otherProg, otherPkg := buildCoroTestSSA(t, "other.go", "package coroid; func other() {}")
	_ = otherProg
	other := packageFunction(t, otherPkg, "other")

	tests := []struct {
		name   string
		roots  Roots
		config SSAConfig
		want   string
	}{
		{name: "nil root", roots: Roots{{Demand: SyncDemand}}, want: "nil SSA function"},
		{name: "no demand", roots: Roots{{Function: root}}, want: "no demand"},
		{name: "other program", roots: Roots{{Function: other, Demand: SyncDemand}}, want: "another SSA program"},
		{
			name:   "invalid dynamic mode",
			roots:  Roots{{Function: root, Demand: SyncDemand}},
			config: SSAConfig{DynamicResolution: DynamicResolution(99)},
			want:   "invalid dynamic resolution",
		},
		{
			name:  "excluded root",
			roots: Roots{{Function: root, Demand: SyncDemand}},
			config: SSAConfig{Include: func(*ssa.Function) (bool, error) {
				return false, nil
			}},
			want: "excluded",
		},
		{
			name:  "function classifier error",
			roots: Roots{{Function: root, Demand: SyncDemand}},
			config: SSAConfig{ClassifyFunction: func(*ssa.Function) (SSAFunctionPolicy, error) {
				return SSAFunctionPolicy{}, bytes.ErrTooLarge
			}},
			want: "classify SSA function",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := AnalyzeSSA(prog, test.roots, test.config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	_, dynamicPkg := buildCoroTestSSA(t, "dynamic.go", "package coroid; func root(fn func()) { fn() }")
	dynamicRoot := packageFunction(t, dynamicPkg, "root")
	_, err := AnalyzeSSA(dynamicPkg.Prog, Roots{{Function: dynamicRoot, Demand: SyncDemand}}, SSAConfig{
		ClassifyUnknownCall: func(*ssa.Function, ssa.CallInstruction) (UnknownTarget, error) {
			return UnknownTarget(99), nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid unknown target") {
		t.Fatalf("invalid unknown target error = %v", err)
	}
}

func TestAnalyzeSSASummaryDeterministicAcrossBuilds(t *testing.T) {
	source := `package coroid
func receive(ch chan int) { <-ch }
func root(ch chan int) { receive(ch) }
`
	progA, pkgA := buildCoroTestSSA(t, "/checkout/a/source.go", source)
	progB, pkgB := buildCoroTestSSA(t, "/different/b/source.go", source)
	planA, err := AnalyzeSSA(progA, Roots{{Function: packageFunction(t, pkgA, "root"), Demand: SyncDemand}}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}
	planB, err := AnalyzeSSA(progB, Roots{{Function: packageFunction(t, pkgB, "root"), Demand: SyncDemand}}, SSAConfig{})
	if err != nil {
		t.Fatal(err)
	}
	metadata := SummaryMetadata{CoroABI: "analysis-v0", SchedulerABI: "analysis-v0"}
	bytesA, err := planA.BasePlan().Summary(metadata).MarshalStable()
	if err != nil {
		t.Fatal(err)
	}
	bytesB, err := planB.BasePlan().Summary(metadata).MarshalStable()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytesA, bytesB) {
		t.Fatalf("summary differs across builds:\nA: %s\nB: %s", bytesA, bytesB)
	}
}
