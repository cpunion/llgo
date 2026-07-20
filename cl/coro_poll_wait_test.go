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
	"go/ast"
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

const coroPollWaitTestSource = `package foo

import _ "unsafe"

//go:linkname wait llgo.coroPollWait
func wait(fd int32, interest uint32, deadline int64) uint32

func Root(fd int32, interest uint32, deadline int64) uint32 {
	return wait(fd, interest, deadline)
}
`

func TestCoroPollWaitIntrinsicRejectsNonCanonicalShape(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
	}{
		{
			name: "fd",
			source: `package pollwaitbadfd
//llgo:link Wait llgo.coroPollWait
func Wait(uint32, uint32, int64) uint32
func Use(fd uint32, interest uint32, deadline int64) uint32 { return Wait(fd, interest, deadline) }
`,
		},
		{
			name: "result",
			source: `package pollwaitbadresult
//llgo:link Wait llgo.coroPollWait
func Wait(int32, uint32, int64) uint64
func Use(fd int32, interest uint32, deadline int64) uint64 { return Wait(fd, interest, deadline) }
`,
		},
		{
			name: "arity",
			source: `package pollwaitbadarity
//llgo:link Wait llgo.coroPollWait
func Wait(int32, uint32) uint32
func Use(fd int32, interest uint32) uint32 { return Wait(fd, interest) }
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testProg := newEmissionTestProgram()
			pkg := testProg.addPackage(t, "example.com/emission/coropollwaitbad"+test.name, test.source)
			testProg.ssa.Build()
			prog := newLLSSAProg(t)
			defer prog.Dispose()
			universe, err := PrepareEmissionUniverse(
				prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}},
			)
			if err != nil {
				t.Fatal(err)
			}
			calls := allocaCStrTestCalls(pkg.ssa.Func("Use"))
			if len(calls) != 1 {
				t.Fatalf("bad poll wait fixture calls = %d, want 1", len(calls))
			}
			if _, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(calls[0]); err == nil || !intrinsic || !strings.Contains(err.Error(), "coroPollWait") {
				t.Fatalf("bad coroPollWait semantics = _, %v, %v; want exact-shape error", intrinsic, err)
			}
		})
	}
}

func TestCoroPollWaitCurrentFrameNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root, waitCall := compileCoroPollWaitFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			rootPlan, ok := plan.FunctionPlan(root)
			if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.FuncRep != coro.DirectCoro ||
				!rootPlan.DeclaredEffect.Contains(coro.MayPark) || !rootPlan.LocalEffect.Contains(coro.MayPark) ||
				!rootPlan.Effect.Contains(coro.MayPark) {
				t.Fatalf("Root plan = %+v, present=%t; want one local poll-park coroutine", rootPlan, ok)
			}
			if !plan.ElidesCall(waitCall) {
				t.Fatal("coroPollWait declaration call is not frozen as a frontend-elided intrinsic site")
			}
			if _, retained := plan.CallPlan(waitCall); retained {
				t.Fatal("coroPollWait declaration unexpectedly retained a managed CallPlan")
			}

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify poll wait coroutine before CoroSplit: %v\n%s", err, module.String())
			}
			physical := requireCoroPhysicalFunction(t, module, "foo.Root")
			body := physical.String()
			assertCoroCancellationTerminalStatusPublication(t, physical)
			if got := strings.Count(body, "call i8 @llvm.coro.suspend"); got != 3 {
				t.Fatalf("Root coro.suspend calls = %d, want initial + poll + final:\n%s", got, body)
			}
			for _, symbol := range []string{coroPollParkHookV2, coroPollResumeHookV2} {
				if got := strings.Count(body, "@"+symbol); got != 1 {
					t.Fatalf("Root references to %q = %d, want 1:\n%s", symbol, got, body)
				}
			}
			for _, forbidden := range []string{
				"@foo.wait", "@llgo.coroPollWait", "runtime.AllocZ",
				"__llgo_coro_poll_prepare_or_abort_v1", "__llgo_coro_poll_retire_completed_or_abort_v1",
				coroParkPrepareHookV1,
			} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("Poll V2 lowering retained forbidden V1 call/allocation %q:\n%s", forbidden, body)
				}
			}
			stateAndPark := regexp.MustCompile(
				`(?s)store i16 4,.*store i16 3,.*store i32 1,.*call void @` + regexp.QuoteMeta(coroPollParkHookV2) +
					`\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, i32 [^,]+, i32 [^,]+, i64 [^)]+\)`,
			)
			if !stateAndPark.MatchString(body) {
				t.Fatalf("Root does not publish Park/Suspended/stateID=1 before Poll V2 park:\n%s", body)
			}
			park := strings.Index(body, "call void @"+coroPollParkHookV2)
			suspendRelative := strings.Index(body[park:], "call i8 @llvm.coro.suspend")
			resumeRelative := strings.Index(body[park:], "call i32 @"+coroPollResumeHookV2)
			if park < 0 || suspendRelative < 0 || resumeRelative < 0 || suspendRelative >= resumeRelative {
				t.Fatalf("Root does not park, suspend, then consume Poll V2 status in order:\n%s", body)
			}
			dispatch := regexp.MustCompile(
				`(?s)call i32 @` + regexp.QuoteMeta(coroPollResumeHookV2) + `\([^\n]+\).*?switch i32 [^\[]+\[(.*?)\]`,
			).FindStringSubmatch(body)
			if len(dispatch) != 2 {
				t.Fatalf("Root has no isolated Poll V2 resume switch:\n%s", body)
			}
			for _, status := range []uint64{
				coroPollResumeReadyV2,
				coroPollResumeClosingV2,
				coroPollResumeTimeoutV2,
				coroPollResumeTaskAbortV2,
				coroPollResumeShutdownV2,
			} {
				if !regexp.MustCompile(`(?m)^\s+i32 ` + strconv.FormatUint(status, 10) + `, label `).MatchString(dispatch[1]) {
					t.Fatalf("Root Poll V2 resume switch lacks status %d:\n%s", status, dispatch[0])
				}
			}
			if regexp.MustCompile(`(?m)^\s+i32 ` + strconv.FormatUint(coroPollResumeOperationCanceledV2, 10) + `, label `).MatchString(dispatch[1]) {
				t.Fatalf("ordinary poll wait silently accepts operation-only cancellation:\n%s", dispatch[0])
			}

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() || !strings.Contains(resume.String(), "call i32 @"+coroPollResumeHookV2) {
				t.Fatalf("CoroSplit lost Poll V2 resume dispatch:\n%s", module.String())
			}
			assertCoroCancellationTerminalStatusPublication(t, resume)
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit post-CoroSplit poll wait object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			for _, symbol := range []string{coroPollParkHookV2, coroPollResumeHookV2} {
				if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(symbol)) {
					t.Fatalf("post-CoroSplit object lost Poll V2 ABI symbol %q", symbol)
				}
			}
		})
	}
}

func compileCoroPollWaitFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Call,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroPollWaitTestSource)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	prog.SetRuntime(func() *types.Package {
		runtimePackage, err := importer.For("source", nil).Import(llssa.PkgRuntime)
		if err != nil {
			t.Fatal("load runtime failed:", err)
		}
		if runtimePackage.Scope().Lookup("CoroPollParkV2") == nil {
			name := types.NewTypeName(token.NoPos, runtimePackage, "CoroPollParkV2", nil)
			types.NewNamed(name, types.NewArray(types.Typ[types.Uintptr], 32), nil)
			if previous := runtimePackage.Scope().Insert(name); previous != nil {
				t.Fatalf("install Poll V2 test runtime type: duplicate %v", previous)
			}
		}
		return runtimePackage
	})
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
	var waitCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Call.StaticCallee() != nil && call.Call.StaticCallee().Name() == "wait" {
				waitCall = call
			}
		}
	}
	if waitCall == nil {
		prog.Dispose()
		t.Fatal("fixture has no direct coroPollWait call")
	}
	semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(waitCall)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineSuspend {
		prog.Dispose()
		t.Fatalf("coroPollWait semantics = %v, %t, %v; want InlineSuspend, true, nil", semantics, intrinsic, err)
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
		CoroPlan:              plan,
		EmissionUniverse:      universe,
		CoroFrameRetentionABI: CoroFrameRetentionParkABIV2,
	}
	enableCoroPreemptCompilation(compilation)
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, root, waitCall
}
