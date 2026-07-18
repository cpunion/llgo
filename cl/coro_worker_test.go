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
	"go/importer"
	"go/token"
	"go/types"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroWorkerTestSource = `package foo

import _ "unsafe"

//go:linkname raw llgo.syscall
func raw(fn, a0, a1, a2, a3, a4, a5 uintptr) (uintptr, uintptr, uintptr)

func Root(fn, a0, a1, a2, a3, a4, a5 uintptr) (uintptr, uintptr, uintptr) {
	return raw(fn, a0, a1, a2, a3, a4, a5)
}
`

func TestCoroWorkerSyscallCurrentFrame(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog, pkg, plan, root, rawCall := compileCoroWorkerFixture(t)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.FuncRep != coro.DirectCoro ||
		rootPlan.Demand != coro.AsyncDemand || !rootPlan.Effect.Contains(coro.MayPark) ||
		!rootPlan.LocalEffect.Contains(coro.MayPark) {
		t.Fatalf("Root plan = %+v, present=%t; want one local may-park coroutine", rootPlan, ok)
	}
	if !plan.ElidesCall(rawCall) {
		t.Fatal("llgo.syscall declaration call is not frozen as a frontend-elided worker site")
	}
	if _, ok := plan.CallPlan(rawCall); ok {
		t.Fatal("llgo.syscall declaration unexpectedly retained a managed CallPlan")
	}

	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify worker coroutine before CoroSplit: %v\n%s", err, module.String())
	}
	body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
	if got := strings.Count(body, "call i8 @llvm.coro.suspend"); got != 3 {
		t.Fatalf("Root coro.suspend calls = %d, want initial + worker + final:\n%s", got, body)
	}
	for _, symbol := range []string{coroWorkerParkHookV1, coroWorkerResumeHookV1} {
		if got := strings.Count(body, "@"+symbol); got != 1 {
			t.Fatalf("Root references to %q = %d, want 1:\n%s", symbol, got, body)
		}
	}
	for _, forbidden := range []string{"@foo.raw", "@llgo.syscall"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("worker lowering retained ordinary intrinsic call %q:\n%s", forbidden, body)
		}
	}
	dispatch := regexp.MustCompile(
		`(?s)call i32 @` + regexp.QuoteMeta(coroWorkerResumeHookV1) + `\([^\n]+\)\n\s+switch i32 [^\[]+\[(.*?)\]`,
	).FindStringSubmatch(body)
	if len(dispatch) != 2 {
		t.Fatalf("Root has no isolated worker resume switch:\n%s", body)
	}
	for _, status := range []uint64{
		coroWorkerResumeSuccessV1,
		coroWorkerResumeTaskAbortV1,
		coroWorkerResumeShutdownV1,
	} {
		if !regexp.MustCompile(`(?m)^\s+i32 ` + strconv.FormatUint(status, 10) + `, label `).MatchString(dispatch[1]) {
			t.Fatalf("Root worker resume switch lacks status %d:\n%s", status, dispatch[0])
		}
	}
	park := strings.Index(body, "call void @"+coroWorkerParkHookV1)
	suspend := strings.Index(body[park:], "call i8 @llvm.coro.suspend")
	resume := strings.Index(body[park:], "call i32 @"+coroWorkerResumeHookV1)
	if park < 0 || suspend < 0 || resume < 0 || suspend >= resume {
		t.Fatalf("Root does not publish worker park before suspend and consume after resume:\n%s", body)
	}

	runCoroABITestPipeline(t, prog, module)
	resumeBody := module.NamedFunction("foo.Root$coro.resume")
	if resumeBody.IsNil() || !strings.Contains(resumeBody.String(), "call i32 @"+coroWorkerResumeHookV1) {
		t.Fatalf("CoroSplit lost worker resume dispatch:\n%s", module.String())
	}
	object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
	if err != nil {
		t.Fatalf("emit post-CoroSplit worker object: %v\n%s", err, module.String())
	}
	defer object.Dispose()
	for _, symbol := range []string{coroWorkerParkHookV1, coroWorkerResumeHookV1} {
		if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(symbol)) {
			t.Fatalf("post-CoroSplit object lost worker ABI symbol %q", symbol)
		}
	}
}

func compileCoroWorkerFixture(t *testing.T) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Call,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroWorkerTestSource)
	prog := newLLSSAProg(t)
	// The host Go runtime imported by isolated cl tests does not contain LLGo's
	// private runtime type. Production builds load the exact target runtime;
	// this opaque test-only named array is sufficient because the hooks remain
	// unresolved and the test observes only frame storage and ABI calls.
	prog.SetRuntime(func() *types.Package {
		runtimePackage, err := importer.For("source", nil).Import(llssa.PkgRuntime)
		if err != nil {
			t.Fatal("load runtime failed:", err)
		}
		if runtimePackage.Scope().Lookup("CoroWorkerParkV1") == nil {
			name := types.NewTypeName(token.NoPos, runtimePackage, "CoroWorkerParkV1", nil)
			types.NewNamed(name, types.NewArray(types.Typ[types.Uintptr], 32), nil)
			if previous := runtimePackage.Scope().Insert(name); previous != nil {
				t.Fatalf("install test runtime type: duplicate %v", previous)
			}
		}
		return runtimePackage
	})
	universe, err := PrepareEmissionUniverseWithOptions(
		prog,
		nil,
		[]EmissionPackage{{SSA: ssaPkg, Files: files}},
		EmissionUniverseOptions{EnableCoroWorker: true},
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
	var rawCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Call.StaticCallee() != nil && call.Call.StaticCallee().Name() == "raw" {
				rawCall = call
			}
		}
	}
	if rawCall == nil {
		prog.Dispose()
		t.Fatal("fixture has no direct llgo.syscall call")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapWorkerABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == root {
				// Production builds seed this fact through
				// CoroPlanInput.intrinsicCallSemantics. This isolated cl fixture
				// has no build-driver wrapper, so freeze the same owner effect.
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
	compilation := &Compilation{
		CoroPlan:                      plan,
		EmissionUniverse:              universe,
		EnableCoroEntryResolution:     true,
		EnableCoroPhysicalABI:         true,
		EnableCoroChildAwait:          true,
		EnableCoroProgramBootstrapRun: true,
		EnableCoroWorker:              true,
		CoroABI:                       coro.PhysicalABIV1,
		SchedulerABI:                  coro.SchedulerProgramBootstrapWorkerABIV0,
		PanicABI:                      coro.PanicLegacyABIV0,
		FuncRepABI:                    coro.FuncRepABIV0,
	}
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, root, rawCall
}
