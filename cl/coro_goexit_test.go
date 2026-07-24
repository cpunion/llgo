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

const coroGoexitTestSource = `package foo

import _ "unsafe"

//go:linkname goexit llgo.coroGoexit
func goexit()

var Unreachable uint32

func Root() {
	goexit()
	Unreachable = 1
}
`

func TestCoroGoexitCurrentFrameNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root, call := compileCoroGoexitFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			rootPlan, planned := plan.FunctionPlan(root)
			if !planned || rootPlan.Emission != coro.EmitCoroutine ||
				!rootPlan.DeclaredEffect.Contains(coro.OutcomeStructured) ||
				!rootPlan.LocalEffect.Contains(coro.OutcomeStructured) ||
				!rootPlan.Effect.Contains(coro.OutcomeStructured) ||
				!plan.ElidesCall(call) {
				t.Fatalf("Goexit Root plan = %+v, planned=%t, elided=%t", rootPlan, planned, plan.ElidesCall(call))
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify Goexit coroutine before CoroSplit: %v\n%s", err, module.String())
			}
			physical := requireCoroPhysicalFunction(t, module, "foo.Root")
			body := physical.String()
			if strings.Contains(body, "@foo.goexit") || strings.Contains(body, "@llgo.coroGoexit") {
				t.Fatalf("Goexit coroutine retained the compiler declaration:\n%s", body)
			}
			if !coroFunctionStoresI32(physical, coroAwaitCompletionGoexit) {
				t.Fatalf("Goexit coroutine does not publish CompletionGoexit:\n%s", body)
			}
			if got := strings.Count(body, "call void @"+coroCompletePrepareHookV2); got != 1 {
				t.Fatalf("Goexit completion prepare calls = %d, want one:\n%s", got, body)
			}

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() || !coroFunctionStoresI32(resume, coroAwaitCompletionGoexit) {
				t.Fatalf("CoroSplit lost CompletionGoexit publication:\n%s", module.String())
			}
		})
	}
}

func compileCoroGoexitFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Call,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroGoexitTestSource)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := prepareStacklessEmissionUniverse(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	root := ssaPkg.Func("Root")
	var intrinsicCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Common().StaticCallee() != nil &&
				call.Common().StaticCallee().Name() == "goexit" {
				intrinsicCall = call
			}
		}
	}
	if intrinsicCall == nil {
		prog.Dispose()
		t.Fatal("fixture has no direct Goexit intrinsic")
	}
	semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(intrinsicCall)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineOutcome {
		prog.Dispose()
		t.Fatalf("Goexit semantics = %v, %t, %v; want InlineOutcome, true, nil", semantics, intrinsic, err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: root, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if fn == root {
					return coro.SSAFunctionPolicy{Effect: coro.OutcomeStructured}, nil
				}
				return coro.SSAFunctionPolicy{}, nil
			},
			ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
				semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
				return intrinsic && semantics.ElidesManagedCall(), err
			},
		},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{
		CoroPlan:         plan,
		EmissionUniverse: universe,
		PanicABI:         coro.PanicExplicitStatusABIV0,
	}
	enableCoroChildAwaitCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, root, intrinsicCall
}

func coroFunctionStoresI32(function llvm.Value, value uint64) bool {
	for _, block := range function.BasicBlocks() {
		for instruction := block.FirstInstruction(); !instruction.IsNil(); instruction = llvm.NextInstruction(instruction) {
			if instruction.InstructionOpcode() != llvm.Store {
				continue
			}
			stored := instruction.Operand(0)
			if stored.Type().TypeKind() == llvm.IntegerTypeKind && stored.Type().IntTypeWidth() == 32 &&
				!stored.IsAConstantInt().IsNil() && stored.ZExtValue() == value {
				return true
			}
		}
	}
	return false
}
