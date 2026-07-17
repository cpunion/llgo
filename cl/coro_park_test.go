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
	"regexp"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroParkTestSource = `package foo

import _ "unsafe"

type WaitToken struct { word uint32 }
type WaitTicket uint32

//go:linkname park llgo.coroPark
func park(token *WaitToken, ticket WaitTicket)

func Root(token *WaitToken, ticket WaitTicket) uint32 {
	before := uint32(ticket) + 7
	park(token, ticket)
	return before + uint32(ticket)
}
`

func TestCoroParkCurrentFrameNativeAndWasm32(t *testing.T) {
	tests := []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root, parkCall := compileCoroParkFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			rootPlan, ok := plan.FunctionPlan(root)
			if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.FuncRep != coro.DirectCoro ||
				!rootPlan.DeclaredEffect.Contains(coro.MayPark) || !rootPlan.LocalEffect.Contains(coro.MayPark) ||
				!rootPlan.Effect.Contains(coro.MayPark) {
				t.Fatalf("Root plan = %+v, present=%t; want one may-park coroutine primary", rootPlan, ok)
			}
			if !plan.ElidesCall(parkCall) {
				t.Fatal("coroPark declaration call is not frozen as a frontend-elided intrinsic site")
			}
			if _, ok := plan.CallPlan(parkCall); ok {
				t.Fatal("coroPark declaration unexpectedly retained a managed CallPlan")
			}

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify park coroutine before CoroSplit: %v\n%s", err, module.String())
			}
			body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
			if got := strings.Count(body, "call i8 @llvm.coro.suspend"); got != 3 {
				t.Fatalf("Root coro.suspend calls = %d, want initial + park + final:\n%s", got, body)
			}
			if strings.Contains(body, "@foo.park") || strings.Contains(body, "@llgo.coroPark") {
				t.Fatalf("structured park leaked an ordinary sync helper call:\n%s", body)
			}
			stateAndHook := regexp.MustCompile(
				`(?s)store i16 4,.*store i16 3,.*store i32 1,.*call void @` + regexp.QuoteMeta(coroParkPrepareHookV1) +
					`\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, i32 [^)]+\)`,
			)
			if !stateAndHook.MatchString(body) {
				t.Fatalf("Root does not publish Park/Suspended/stateID=1 before the exact v1 hook:\n%s", body)
			}
			hook := strings.Index(body, "call void @"+coroParkPrepareHookV1)
			parkSuspendRelative := strings.Index(body[hook:], "call i8 @llvm.coro.suspend")
			if hook < 0 || parkSuspendRelative < 0 {
				t.Fatalf("Root has no park hook followed by a caller-frame suspend:\n%s", body)
			}
			parkSuspend := hook + parkSuspendRelative
			decisionRelative := strings.Index(body[parkSuspend:], "call i32 @"+coroRunDecisionTakeZeroHookV1)
			if decisionRelative < 0 {
				t.Fatalf("Root does not take its run decision after park resume:\n%s", body)
			}
			decision := parkSuspend + decisionRelative
			activate := regexp.MustCompile(`(?s)store i16 0,.*store i16 2,`).FindStringIndex(body[decision:])
			if activate == nil {
				t.Fatalf("Root does not reactivate its exact frame after resume:\n%s", body)
			}
			assertCoroScalarRunDecisionCalls(t, "Root park", body, 2)

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() || !strings.Contains(resume.String(), "call void @"+coroParkPrepareHookV1) {
				t.Fatalf("CoroSplit lost the park handoff in Root.resume:\n%s", module.String())
			}
			assertCoroRunDecisionResumeOnly(t, module, "foo.Root$coro", 2)
			for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend", "llvm.coro.end"} {
				if hasLLVMCall(module.String(), intrinsic) {
					t.Fatalf("post-split park module still calls %s:\n%s", intrinsic, module.String())
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit post-CoroSplit park object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(coroParkPrepareHookV1)) {
				t.Fatalf("post-CoroSplit object lost unresolved park ABI symbol %q", coroParkPrepareHookV1)
			}
			if !bytes.Contains(object.Bytes(), []byte(coroRunDecisionTakeZeroHookV1)) {
				t.Fatalf("post-CoroSplit object lost unresolved run-decision ABI symbol %q", coroRunDecisionTakeZeroHookV1)
			}
		})
	}
}

func compileCoroParkFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Call,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroParkTestSource)
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
	var parkCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok || call.Call.StaticCallee() == nil || call.Call.StaticCallee().Name() != "park" {
				continue
			}
			parkCall = call
		}
	}
	if parkCall == nil {
		prog.Dispose()
		t.Fatal("fixture has no direct park call")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == root {
				return coro.SSAFunctionPolicy{Effect: coro.MayPark}, nil
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
	enableCoroChildAwaitCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, root, parkCall
}
