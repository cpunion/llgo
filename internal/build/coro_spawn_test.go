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

package build

import (
	"strings"
	"testing"

	"github.com/goplus/llgo/cl"
	"github.com/goplus/llgo/internal/coro"
	"golang.org/x/tools/go/ssa"
)

func TestCoroPlanInputClosedStaticSpawnSeedsOwnerAndPreservesTargetPrimary(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/spawn", `package spawn
var channel chan int
func plain(value int) { _ = value }
func suspending() { <-channel }
func launchPlain(value int) { plain(value); go plain(value) }
func launchSuspending() { go suspending() }
`, nil)
	launchPlain := ssaPkg.Func("launchPlain")
	launchSuspending := ssaPkg.Func("launchSuspending")
	input := CoroPlanInput{Program: ssaPkg.Prog}
	plan, err := input.Analyze(coro.Roots{
		{Function: launchPlain, Demand: coro.AsyncDemand},
		{Function: launchSuspending, Demand: coro.AsyncDemand},
	}, coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}

	plainPlan, _ := plan.FunctionPlan(ssaPkg.Func("plain"))
	if plainPlan.Emission != coro.EmitCoroutine || plainPlan.Primary != coro.PrimaryCoroutine || plainPlan.FuncRep != coro.DirectCoro ||
		plainPlan.Demand != coro.AsyncDemand || !plainPlan.Effect.Contains(coro.YieldOnly) {
		t.Fatalf("plain sync+spawn target = %+v", plainPlan)
	}
	suspendingPlan, _ := plan.FunctionPlan(ssaPkg.Func("suspending"))
	if suspendingPlan.Emission != coro.EmitCoroutine || suspendingPlan.Primary != coro.PrimaryCoroutine ||
		suspendingPlan.FuncRep != coro.DirectCoro || suspendingPlan.Demand != coro.AsyncDemand {
		t.Fatalf("suspending spawn target = %+v", suspendingPlan)
	}
	for _, owner := range []*ssa.Function{launchPlain, launchSuspending} {
		ownerPlan, _ := plan.FunctionPlan(owner)
		if ownerPlan.DeclaredEffect != coro.YieldOnly || !ownerPlan.LocalEffect.Contains(coro.YieldOnly) ||
			!ownerPlan.Effect.Contains(coro.YieldOnly) || ownerPlan.Emission != coro.EmitCoroutine ||
			ownerPlan.Primary != coro.PrimaryCoroutine || ownerPlan.FuncRep != coro.DirectCoro || ownerPlan.Demand != coro.AsyncDemand {
			t.Fatalf("owner %s = %+v", owner.Name(), ownerPlan)
		}
		for _, call := range coroPlanTestCalls(owner) {
			spawn, ok := call.(*ssa.Go)
			if !ok {
				continue
			}
			if _, _, err := plan.ResolveClosedStaticSpawn(spawn); err != nil {
				t.Fatalf("resolve %s spawn: %v", owner.Name(), err)
			}
			callPlan, ok := plan.CallPlan(spawn)
			if !ok || callPlan.Kind != coro.CallSpawn || callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 {
				t.Fatalf("owner %s spawn CallPlan = %+v, present=%v", owner.Name(), callPlan, ok)
			}
		}
	}
	if !coroPlanContainsSpawn(plan) {
		t.Fatal("emitted plan lost its spawn site")
	}
	err = validateCoroClosedStaticSpawnRunGate(&Config{}, plan, "")
	if err == nil || !strings.Contains(err.Error(), "may-park") || !strings.Contains(err.Error(), "main-return cancellation subset") {
		t.Fatalf("runnable spawn gate error = %v", err)
	}
}

func TestCoroPlanInputClosedStaticSpawnAcceptsContextFreeLiteral(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/literalspawn", `package literalspawn
var sink int
func launch(value int) {
	go func(argument int) { sink = argument }(value)
}
`, nil)
	launch := ssaPkg.Func("launch")
	input := CoroPlanInput{Program: ssaPkg.Prog}
	plan, err := input.Analyze(
		coro.Roots{{Function: launch, Demand: coro.AsyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1},
	)
	if err != nil {
		t.Fatal(err)
	}

	var spawn *ssa.Go
	for _, call := range coroPlanTestCalls(launch) {
		if candidate, ok := call.(*ssa.Go); ok {
			spawn = candidate
			break
		}
	}
	if spawn == nil {
		t.Fatal("launch has no spawn")
	}
	target, direct := spawn.Common().Value.(*ssa.Function)
	if !direct || target == nil || target.Parent() != launch || len(target.FreeVars) != 0 {
		t.Fatalf("spawn target = %#v, want exact context-free nested function", spawn.Common().Value)
	}
	resolved, targetPlan, err := plan.ResolveClosedStaticSpawn(spawn)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != target || targetPlan.Emission != coro.EmitCoroutine ||
		targetPlan.Primary != coro.PrimaryCoroutine || targetPlan.FuncRep != coro.DirectCoro ||
		targetPlan.Demand != coro.AsyncDemand || !targetPlan.Effect.Contains(coro.YieldOnly) {
		t.Fatalf("resolved target/plan = %v / %+v", resolved, targetPlan)
	}
}

func TestCoroPlanInputManagedDescriptorSpawnSeedsCapturedTargetAndOpenValue(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/managedspawn", `package managedspawn
var sink int
func launchCaptured(value int) {
	go func(delta int) { sink = value + delta }(value + 1)
}
func launchDynamic(fn func()) { go fn() }
`, nil)
	launchCaptured := ssaPkg.Func("launchCaptured")
	launchDynamic := ssaPkg.Func("launchDynamic")
	input := CoroPlanInput{
		Program: ssaPkg.Prog,
	}
	plan, err := input.Analyze(coro.Roots{
		{Function: launchCaptured, Demand: coro.AsyncDemand},
		{Function: launchDynamic, Demand: coro.AsyncDemand},
	}, coro.SSAConfig{MaxPlainInstructions: -1})
	if err != nil {
		t.Fatal(err)
	}

	for _, owner := range []*ssa.Function{launchCaptured, launchDynamic} {
		var spawn *ssa.Go
		for _, call := range coroPlanTestCalls(owner) {
			if candidate, ok := call.(*ssa.Go); ok {
				spawn = candidate
				break
			}
		}
		if spawn == nil {
			t.Fatalf("%s: missing spawn", owner.Name())
		}
		callPlan, resolveErr := plan.ResolveManagedDispatchSpawn(spawn)
		if resolveErr != nil {
			t.Fatalf("%s: resolve managed descriptor spawn: %v", owner.Name(), resolveErr)
		}
		if callPlan.Kind != coro.CallSpawn || callPlan.Rep != coro.Dispatch {
			t.Fatalf("%s spawn CallPlan = %+v", owner.Name(), callPlan)
		}
		if owner == launchCaptured {
			if callPlan.Open || len(callPlan.Targets) != 1 {
				t.Fatalf("captured spawn CallPlan = %+v", callPlan)
			}
			target, found := plan.Function(callPlan.Targets[0])
			if !found || target == nil {
				t.Fatalf("captured spawn target %q is absent", callPlan.Targets[0])
			}
			targetPlan, _ := plan.FunctionPlan(target)
			if targetPlan.Emission != coro.EmitCoroutine || targetPlan.Primary != coro.PrimaryCoroutine ||
				targetPlan.FuncRep != coro.Dispatch || targetPlan.Demand != coro.AsyncDemand ||
				!targetPlan.Effect.Contains(coro.YieldOnly) {
				t.Fatalf("captured spawn target = %+v", targetPlan)
			}
		} else if !callPlan.Open || callPlan.Unresolved != coro.UnknownManagedDispatch {
			t.Fatalf("open function-value spawn CallPlan = %+v", callPlan)
		}
	}
}

func TestCoroPlanInputClosedStaticMethodSpawnUsesDescriptorArgument(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/methodspawn", `package methodspawn
var sink int
type worker int
func (receiver worker) run(callback func(int), value int) {
	sink = int(receiver) + value
	_ = callback
}
func receiver(value int) worker { return worker(value + 1) }
func argument(value int) int { return value + 2 }
func launch(callback func(int), value int) {
	go receiver(value).run(callback, argument(value))
}
`, nil)
	launch := ssaPkg.Func("launch")
	input := CoroPlanInput{
		Program: ssaPkg.Prog,
	}
	plan, err := input.Analyze(
		coro.Roots{{Function: launch, Demand: coro.AsyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1},
	)
	if err != nil {
		t.Fatal(err)
	}

	var spawn *ssa.Go
	for _, call := range coroPlanTestCalls(launch) {
		if candidate, ok := call.(*ssa.Go); ok {
			spawn = candidate
			break
		}
	}
	if spawn == nil {
		t.Fatal("launch has no method spawn")
	}
	target, targetPlan, err := resolveCoroDirectStaticSpawnPlan(plan, spawn)
	if err != nil {
		t.Fatalf("resolve method spawn: %v", err)
	}
	if target.Signature == nil || target.Signature.Recv() == nil {
		t.Fatalf("spawn target %q is not a method", target.Name())
	}
	if targetPlan.Emission != coro.EmitCoroutine || targetPlan.Primary != coro.PrimaryCoroutine ||
		targetPlan.FuncRep != coro.DirectCoro || targetPlan.Demand != coro.AsyncDemand ||
		!targetPlan.Effect.Contains(coro.YieldOnly) {
		t.Fatalf("method spawn target = %+v", targetPlan)
	}
	callPlan, found := plan.CallPlan(spawn)
	if !found || callPlan.Kind != coro.CallSpawn || callPlan.Rep != coro.DirectCoro ||
		callPlan.Open || callPlan.MayBeNil || len(callPlan.Targets) != 1 {
		t.Fatalf("method spawn CallPlan = %+v, present=%t", callPlan, found)
	}
	if len(spawn.Common().Args) != 3 {
		t.Fatalf("normalized method arguments = %d, want receiver+2 args", len(spawn.Common().Args))
	}
	callbackPlan, found := plan.ValuePlan(spawn.Common().Args[1])
	if !found || len(callbackPlan.Funcs) != 1 || len(callbackPlan.Funcs[0].Path) != 0 ||
		callbackPlan.Funcs[0].Rep != coro.Dispatch {
		t.Fatalf("method callback ValuePlan = %+v, present=%t; want scalar Dispatch", callbackPlan, found)
	}
}

func TestCoroClosedStaticSpawnRunGateEffectSubset(t *testing.T) {
	tests := []struct {
		name          string
		effect        coro.Effect
		enableWorker  bool
		enableChannel bool
		frameABI      string
		wantOK        bool
		wantDetail    string
	}{
		{name: "yield", effect: coro.YieldOnly, wantOK: true},
		{name: "structured await", effect: coro.YieldOnly | coro.AwaitStructured, wantOK: true},
		{name: "missing yield", effect: coro.AwaitStructured, wantDetail: "await-structured"},
		{name: "park", effect: coro.YieldOnly | coro.MayPark, wantDetail: "may-park"},
		{
			name:     "typed park has command drain",
			effect:   coro.YieldOnly | coro.MayPark,
			frameABI: cl.CoroFrameRetentionParkABIV2,
			wantOK:   true,
		},
		{
			name:         "worker capability does not prove park source",
			effect:       coro.YieldOnly | coro.MayPark,
			enableWorker: true,
			wantDetail:   "may-park",
		},
		{
			name:          "channel capability does not prove park source",
			effect:        coro.YieldOnly | coro.MayPark,
			enableChannel: true,
			wantDetail:    "may-park",
		},
		{name: "platform wait", effect: coro.YieldOnly | coro.WaitPlatform, wantDetail: "wait-platform"},
		{name: "host wait", effect: coro.YieldOnly | coro.WaitHost, wantDetail: "wait-host"},
		{name: "foreign wait", effect: coro.YieldOnly | coro.WaitForeign, wantDetail: "wait-foreign"},
		{
			name:         "worker foreign wait has command drain",
			effect:       coro.YieldOnly | coro.WaitForeign,
			enableWorker: true,
			wantOK:       true,
		},
		{name: "opaque", effect: coro.OpaqueSuspend, wantDetail: "opaque-suspend"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/spawngate", `package spawngate
func target() {}
func launch() { go target() }
`, nil)
			launch, target := ssaPkg.Func("launch"), ssaPkg.Func("target")
			plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: launch, Demand: coro.AsyncDemand}}, coro.SSAConfig{
				MaxPlainInstructions: -1,
				ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
					switch fn {
					case launch:
						return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
					case target:
						return coro.SSAFunctionPolicy{Effect: test.effect}, nil
					default:
						return coro.SSAFunctionPolicy{}, nil
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			conf := &Config{}
			if test.enableWorker {
				conf.Goos, conf.Goarch = "linux", "amd64"
			}
			err = validateCoroClosedStaticSpawnRunGate(conf, plan, test.frameABI)
			if test.wantOK {
				if err != nil {
					t.Fatalf("safe runnable spawn rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantDetail) {
				t.Fatalf("gate error = %v, want detail %q", err, test.wantDetail)
			}
		})
	}
}

func TestCoroClosedStaticSpawnRunGateReportsPreciseEffectTrace(t *testing.T) {
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/spawntrace", `package spawntrace
func leaf() {}
func target() { leaf() }
func launch() { go target() }
`, nil)
	launch := ssaPkg.Func("launch")
	target := ssaPkg.Func("target")
	leaf := ssaPkg.Func("leaf")
	plan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: launch, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				switch fn {
				case launch:
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				case target:
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				case leaf:
					return coro.SSAFunctionPolicy{Effect: coro.WaitForeign}, nil
				default:
					return coro.SSAFunctionPolicy{}, nil
				}
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.SuspensionEffectTrace(target, coro.WaitForeign); got == "unavailable" ||
		!strings.Contains(got, target.String()) ||
		!strings.Contains(got, leaf.String()) ||
		!strings.Contains(got, "[local=wait-foreign") {
		t.Fatalf("suspension trace = %q, want target-to-leaf WaitForeign explanation", got)
	}
	err = validateCoroClosedStaticSpawnRunGate(&Config{}, plan, "")
	if err == nil ||
		!strings.Contains(err.Error(), "effect trace:") ||
		!strings.Contains(err.Error(), leaf.String()) {
		t.Fatalf("spawn gate error = %v, want precise propagated effect trace", err)
	}
}

func TestCoroPlanInputClosedStaticSpawnSupportsDiscardedResults(t *testing.T) {
	const source = `package spawn; func worker() int { return 1 }; func launch() { go worker() }`
	ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/spawn", source, nil)
	input := CoroPlanInput{Program: ssaPkg.Prog}
	plan, err := input.Analyze(
		coro.Roots{{Function: ssaPkg.Func("launch"), Demand: coro.AsyncDemand}},
		coro.SSAConfig{MaxPlainInstructions: -1},
	)
	if err != nil {
		t.Fatalf("discarded-result spawn rejected: %v", err)
	}
	worker, ok := plan.FunctionPlan(ssaPkg.Func("worker"))
	if !ok || worker.Emission != coro.EmitCoroutine || worker.Demand != coro.AsyncDemand {
		t.Fatalf("discarded-result worker plan = %+v, present=%t", worker, ok)
	}
}
