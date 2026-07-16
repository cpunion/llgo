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
	input := CoroPlanInput{Program: ssaPkg.Prog, enableClosedStaticSpawn: true}
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
	if err := validateCoroClosedStaticSpawnRunGate(&Config{EnableCoroClosedStaticSpawn: true}, plan); err == nil || !strings.Contains(err.Error(), "runnable program bootstrap v2") {
		t.Fatalf("non-runnable spawn gate error = %v", err)
	}
	err = validateCoroClosedStaticSpawnRunGate(&Config{
		EnableCoroClosedStaticSpawn:   true,
		EnableCoroProgramBootstrapRun: true,
	}, plan)
	if err == nil || !strings.Contains(err.Error(), "may-park") || !strings.Contains(err.Error(), "main-return cancellation subset") {
		t.Fatalf("runnable spawn gate error = %v", err)
	}
}

func TestCoroClosedStaticSpawnRunGateEffectSubset(t *testing.T) {
	tests := []struct {
		name       string
		effect     coro.Effect
		wantOK     bool
		wantDetail string
	}{
		{name: "yield", effect: coro.YieldOnly, wantOK: true},
		{name: "structured await", effect: coro.YieldOnly | coro.AwaitStructured, wantOK: true},
		{name: "missing yield", effect: coro.AwaitStructured, wantDetail: "await-structured"},
		{name: "park", effect: coro.YieldOnly | coro.MayPark, wantDetail: "may-park"},
		{name: "platform wait", effect: coro.YieldOnly | coro.WaitPlatform, wantDetail: "wait-platform"},
		{name: "host wait", effect: coro.YieldOnly | coro.WaitHost, wantDetail: "wait-host"},
		{name: "foreign wait", effect: coro.YieldOnly | coro.WaitForeign, wantDetail: "wait-foreign"},
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
			err = validateCoroClosedStaticSpawnRunGate(&Config{
				EnableCoroClosedStaticSpawn:   true,
				EnableCoroProgramBootstrapRun: true,
			}, plan)
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

func TestCoroPlanInputClosedStaticSpawnFailsClosedOnUnsupportedShapes(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "captured closure",
			source: `package spawn; func launch(value int) { go func() { _ = value }() }`,
			want:   "closures, methods, interfaces, and function values",
		},
		{
			name: "method",
			source: `package spawn
type worker int
func (worker) run() {}
func launch(value worker) { go value.run() }
`,
			want: "non-method",
		},
		{
			name:   "dynamic function value",
			source: `package spawn; func launch(fn func()) { go fn() }`,
			want:   "closures, methods, interfaces, and function values",
		},
		{
			name:   "discarded result capability",
			source: `package spawn; func worker() int { return 1 }; func launch() { go worker() }`,
			want:   "zero-result signature",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ssaPkg, _ := buildCoroPlanTestPackage(t, "example.com/spawn", test.source, nil)
			input := CoroPlanInput{Program: ssaPkg.Prog, enableClosedStaticSpawn: true}
			_, err := input.Analyze(coro.Roots{{Function: ssaPkg.Func("launch"), Demand: coro.AsyncDemand}}, coro.SSAConfig{MaxPlainInstructions: -1})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestBuildCoroPlanClosedStaticSpawnCapabilityDependencies(t *testing.T) {
	tests := []struct {
		name string
		conf *Config
		want string
	}{
		{
			name: "runnable bootstrap",
			conf: &Config{EnableCoroClosedStaticSpawn: true},
			want: "runnable program bootstrap v2 is required",
		},
		{
			name: "bootstrap ABI",
			conf: &Config{
				EnableCoroClosedStaticSpawn:   true,
				EnableCoroProgramBootstrapRun: true,
				EnableCoroChildAwait:          true,
			},
			want: "program bootstrap ABI is required",
		},
		{
			name: "child await",
			conf: &Config{
				BuildMode:                 BuildModeExe,
				EnableCoroEntryResolution: true, EnableCoroPhysicalABI: true,
				EnableCoroProgramBootstrapABI: true, EnableCoroProgramBootstrapRun: true,
				EnableCoroClosedStaticSpawn: true,
			},
			want: "coroutine child await is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := buildCoroPlan(&context{buildConf: test.conf})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("dependency error = %v, want %q", err, test.want)
			}
		})
	}
}
