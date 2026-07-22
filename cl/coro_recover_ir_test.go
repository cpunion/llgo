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
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroRecoverIRFixture = `package foo

var FirstPayload uint32
var SecondPayload uint32

func Catch() { recover() }

func CatchAndRepanic() {
	recover()
	panic(&SecondPayload)
}

func RootRecover(doPanic bool) {
	defer Catch()
	if doPanic { panic(&FirstPayload) }
}

func RootRecoverNil() any { return recover() }

func RootRepanic(doPanic bool) {
	defer CatchAndRepanic()
	if doPanic { panic(&FirstPayload) }
}
`

func TestCoroExplicitStatusRecoverIRNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, functions := compileCoroRecoverIRFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			requireExactStaticCoroRecoverDefer(t, plan, functions["RootRecover"], functions["Catch"])
			requireExactStaticCoroRecoverDefer(t, plan, functions["RootRepanic"], functions["CatchAndRepanic"])

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify explicit-status recover before CoroSplit: %v\n%s", err, module.String())
			}
			assertCoroRecoverIR(t, module, false)

			runCoroABITestPipeline(t, prog, module)
			assertCoroRecoverIR(t, module, true)
		})
	}
}

func assertCoroRecoverIR(t *testing.T, module llvm.Module, split bool) {
	t.Helper()
	suffix := "$coro"
	if split {
		suffix += ".resume"
	}
	function := func(source string) llvm.Value {
		t.Helper()
		value := module.NamedFunction("foo." + source + suffix)
		if value.IsNil() {
			t.Fatalf("recover fixture function %q is absent (post-split=%t):\n%s", source+suffix, split, module.String())
		}
		return value
	}

	rootRecover := function("RootRecover")
	catch := function("Catch")
	rootNil := function("RootRecoverNil")
	rootRepanic := function("RootRepanic")
	catchAndRepanic := function("CatchAndRepanic")

	if got := countCoroIRDirectCalls(rootRecover, coroAwaitPrepareHookV1); got != 1 {
		t.Fatalf("RootRecover await_prepare_v3 calls = %d, want 1 (post-split=%t):\n%s", got, split, rootRecover.String())
	}
	if got := countCoroIRDirectCalls(catch, coroRecoverTakeHookV1); got != 1 {
		t.Fatalf("Catch recover_take_v1 calls = %d, want 1 (post-split=%t):\n%s", got, split, catch.String())
	}
	if got := countCoroIRDirectCalls(rootNil, coroRecoverTakeHookV1); got != 1 {
		t.Fatalf("root recover(nil) take calls = %d, want 1 (post-split=%t):\n%s", got, split, rootNil.String())
	}
	if got := countCoroIRDirectCalls(rootNil, coroAwaitPrepareHookV1); got != 0 {
		t.Fatalf("root recover(nil) unexpectedly creates a child transaction (post-split=%t):\n%s", split, rootNil.String())
	}

	if got := countCoroIRDirectCalls(rootRepanic, coroAwaitPrepareHookV1); got != 1 {
		t.Fatalf("RootRepanic await_prepare_v3 calls = %d, want 1 (post-split=%t):\n%s", got, split, rootRepanic.String())
	}
	if got := countCoroIRDirectCalls(catchAndRepanic, coroRecoverTakeHookV1); got != 1 {
		t.Fatalf("CatchAndRepanic recover_take_v1 calls = %d, want 1 (post-split=%t):\n%s", got, split, catchAndRepanic.String())
	}
	if got := countCoroIRDirectCalls(catchAndRepanic, coroPanicPrepareHookV1); got != 1 {
		t.Fatalf("CatchAndRepanic repanic publications = %d, want 1 (post-split=%t):\n%s", got, split, catchAndRepanic.String())
	}
	if !strings.Contains(catchAndRepanic.String(), "@foo.SecondPayload") {
		t.Fatalf("CatchAndRepanic does not publish the replacement panic payload (post-split=%t):\n%s", split, catchAndRepanic.String())
	}
	if strings.Contains(catchAndRepanic.String(), "@foo.FirstPayload") {
		t.Fatalf("CatchAndRepanic retained the recovered payload as its repanic payload (post-split=%t):\n%s", split, catchAndRepanic.String())
	}

	for _, value := range []llvm.Value{rootRecover, catch, rootNil, rootRepanic, catchAndRepanic} {
		if legacy := firstLegacyRecoverCall(value); legacy != "" {
			t.Fatalf("%s calls legacy recover helper %q (post-split=%t):\n%s", value.Name(), legacy, split, value.String())
		}
	}
}

func countCoroIRDirectCalls(function llvm.Value, callee string) int {
	count := 0
	for _, block := range function.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() == llvm.Call && instruction.CalledValue().Name() == callee {
				count++
			}
		}
	}
	return count
}

func firstLegacyRecoverCall(function llvm.Value) string {
	for _, block := range function.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Call {
				continue
			}
			name := instruction.CalledValue().Name()
			if name == "runtime.Recover" || strings.HasSuffix(name, "/runtime.Recover") ||
				strings.HasSuffix(name, "/runtime/internal/runtime.Recover") {
				return name
			}
		}
	}
	return ""
}

func requireExactStaticCoroRecoverDefer(
	t *testing.T, plan *coro.SSAPlan, caller, target *ssa.Function,
) {
	t.Helper()
	callerPlan, callerOK := plan.FunctionPlan(caller)
	targetPlan, targetOK := plan.FunctionPlan(target)
	if !callerOK || callerPlan.Emission != coro.EmitCoroutine ||
		!callerPlan.Exec.Contains(coro.NeedsCleanupFrame) || !callerPlan.Effect.Contains(coro.AwaitStructured) {
		t.Fatalf("recover caller plan = %+v, present=%t", callerPlan, callerOK)
	}
	if !targetOK || targetPlan.Emission != coro.EmitCoroutine || targetPlan.FuncRep != coro.DirectCoro {
		t.Fatalf("recover target plan = %+v, present=%t", targetPlan, targetOK)
	}
	found := 0
	for _, block := range caller.Blocks {
		for _, instruction := range block.Instrs {
			deferred, ok := instruction.(*ssa.Defer)
			if !ok || deferred.Common().StaticCallee() != target {
				continue
			}
			found++
			callPlan, ok := plan.CallPlan(deferred)
			if !ok || callPlan.Kind != coro.CallDefer || callPlan.Rep != coro.DirectCoro || callPlan.Open ||
				callPlan.MayBeNil || len(callPlan.Targets) != 1 || callPlan.Targets[0] != targetPlan.ID {
				t.Fatalf("recover defer call plan = %+v, present=%t; want one exact DirectCoro target", callPlan, ok)
			}
		}
	}
	if found != 1 {
		t.Fatalf("exact recover defer sites = %d, want 1", found)
	}
}

func compileCoroRecoverIRFixture(
	t *testing.T, target *llssa.Target,
) (llssa.Program, llssa.Package, *coro.SSAPlan, map[string]*ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroRecoverIRFixture)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	functions := map[string]*ssa.Function{
		"Catch":           ssaPkg.Func("Catch"),
		"CatchAndRepanic": ssaPkg.Func("CatchAndRepanic"),
		"RootRecover":     ssaPkg.Func("RootRecover"),
		"RootRecoverNil":  ssaPkg.Func("RootRecoverNil"),
		"RootRepanic":     ssaPkg.Func("RootRepanic"),
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{
		{Function: functions["RootRecover"], Demand: coro.AsyncDemand},
		{Function: functions["RootRecoverNil"], Demand: coro.AsyncDemand},
		{Function: functions["RootRepanic"], Demand: coro.AsyncDemand},
	}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			for _, fixture := range functions {
				if function == fixture {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				}
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.CoroProfile = CoroProfileStackless
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, functions
}
