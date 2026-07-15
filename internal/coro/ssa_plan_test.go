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
