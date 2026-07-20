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
	"bytes"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroCriticalIRTestSource = `package foo

import _ "unsafe"

//go:linkname criticalEnter llgo.coroCriticalEnter
func criticalEnter()

//go:linkname criticalExit llgo.coroCriticalExit
func criticalExit()

var cell uint32

func Root(value uint32) uint32 {
	criticalEnter()
	cell = value
	result := cell
	criticalExit()
	return result
}
`

func TestCoroCriticalRegionLoweringNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root, enter, exit := compileCoroCriticalIRFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if !plan.ElidesCall(enter) || !plan.ElidesCall(exit) {
				t.Fatal("critical marker declarations were not both frontend-elided")
			}
			if _, retained := plan.CallPlan(enter); retained {
				t.Fatal("critical enter retained an ordinary managed CallPlan")
			}
			if _, retained := plan.CallPlan(exit); retained {
				t.Fatal("critical exit retained an ordinary managed CallPlan")
			}
			rootPlan, ok := plan.FunctionPlan(root)
			if !ok || rootPlan.Emission != coro.EmitCoroutine || !rootPlan.LocalEffect.Contains(coro.YieldOnly) {
				t.Fatalf("critical Root plan = %+v, present=%t; want one yield-capable coroutine body", rootPlan, ok)
			}

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify critical coroutine before CoroSplit: %v\n%s", err, module.String())
			}
			body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
			for _, marker := range []string{"@foo.criticalEnter", "@foo.criticalExit", "@llgo.coroCriticalEnter", "@llgo.coroCriticalExit"} {
				if strings.Contains(body, marker) {
					t.Fatalf("source critical marker %q leaked into physical IR:\n%s", marker, body)
				}
			}
			begin := strings.Index(body, "call void @"+coroCriticalEnterHookV1)
			endRelative := -1
			if begin >= 0 {
				endRelative = strings.Index(body[begin:], "call i1 @"+coroCriticalExitHookV1)
			}
			if begin < 0 || endRelative < 0 {
				t.Fatalf("physical body lacks ordered critical hooks:\n%s", body)
			}
			if got := strings.Count(body[:begin], "call i1 @"+coroPreemptPollHookV1); got != 1 {
				t.Fatalf("outer critical enter has %d pre-entry polls, want the one block-entry safepoint:\n%s", got, body)
			}
			span := body[begin : begin+endRelative]
			for _, forbidden := range []string{
				"@" + coroPreemptPollHookV1,
				"@" + coroYieldPrepareHookV1,
				"@llvm.coro.suspend",
			} {
				if strings.Contains(span, forbidden) {
					t.Fatalf("critical span contains forbidden safepoint %q:\n%s", forbidden, span)
				}
			}
			if exitIndex := begin + endRelative; !strings.Contains(body[exitIndex:], "call void @"+coroYieldPrepareHookV1) ||
				!strings.Contains(body[exitIndex:], "call i8 @llvm.coro.suspend") {
				t.Fatalf("outer critical exit is not connected to conditional runnable handoff:\n%s", body)
			}

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() {
				t.Fatalf("post-CoroSplit module lacks Root resume body:\n%s", module.String())
			}
			for _, hook := range []string{coroCriticalEnterHookV1, coroCriticalExitHookV1} {
				if !strings.Contains(module.String(), "@"+hook) {
					t.Fatalf("post-CoroSplit module lost critical ABI hook %q", hook)
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit post-CoroSplit critical object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			for _, hook := range []string{coroCriticalEnterHookV1, coroCriticalExitHookV1} {
				if !bytes.Contains(object.Bytes(), []byte(hook)) {
					t.Fatalf("post-CoroSplit object lost unresolved critical ABI symbol %q", hook)
				}
			}
		})
	}
}

func compileCoroCriticalIRFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Call, *ssa.Call,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroCriticalIRTestSource)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
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
	var enter, exit *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Common().StaticCallee() == nil {
				continue
			}
			switch call.Common().StaticCallee().Name() {
			case "criticalEnter":
				enter = call
			case "criticalExit":
				exit = call
			}
		}
	}
	if enter == nil || exit == nil {
		prog.Dispose()
		t.Fatal("critical fixture lacks exact enter/exit calls")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == root {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly, Exec: coro.NeedsPreempt}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			callee := call.Common().StaticCallee()
			if callee != nil && callee.Pkg != nil && callee.Pkg.Pkg.Path() == "unsafe" && callee.Name() == "init" {
				return true, nil
			}
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, root, enter, exit
}
