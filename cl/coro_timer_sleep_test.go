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

const coroTimerSleepTestSource = `package foo

import _ "unsafe"

//go:linkname sleep llgo.coroTimerSleep
func sleep(delay int64)

func Root(delay int64) int64 {
	before := delay + 7
	sleep(delay)
	return before + delay
}
`

const coroControlledTimerWaitTestSource = `package foo

import "unsafe"

//go:linkname wait llgo.coroControlledTimerWait
func wait(controller unsafe.Pointer, control, ownerRoute *uint32, expected uint32, deadline int64) uint32

func Root(controller unsafe.Pointer, control, ownerRoute *uint32, expected uint32, deadline int64) uint32 {
	return wait(controller, control, ownerRoute, expected, deadline)
}
`

func TestCoroTimerSleepCurrentFrameNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root, sleepCall := compileCoroTimerSleepFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			rootPlan, ok := plan.FunctionPlan(root)
			if !ok || rootPlan.Emission != coro.EmitCoroutine || rootPlan.FuncRep != coro.DirectCoro ||
				!rootPlan.DeclaredEffect.Contains(coro.MayPark) || !rootPlan.LocalEffect.Contains(coro.MayPark) ||
				!rootPlan.Effect.Contains(coro.MayPark) {
				t.Fatalf("Root plan = %+v, present=%t; want one local timer-park coroutine", rootPlan, ok)
			}
			if !plan.ElidesCall(sleepCall) {
				t.Fatal("coroTimerSleep declaration call is not frozen as a frontend-elided intrinsic site")
			}
			if _, retained := plan.CallPlan(sleepCall); retained {
				t.Fatal("coroTimerSleep declaration unexpectedly retained a managed CallPlan")
			}

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify timer Sleep coroutine before CoroSplit: %v\n%s", err, module.String())
			}
			physical := requireCoroPhysicalFunction(t, module, "foo.Root")
			body := physical.String()
			assertCoroCancellationTerminalStatusPublication(t, physical)
			if got := strings.Count(body, "call i8 @llvm.coro.suspend"); got != 3 {
				t.Fatalf("Root coro.suspend calls = %d, want initial + timer + final:\n%s", got, body)
			}
			for _, symbol := range []string{coroTimerParkHookV2, coroTimerResumeHookV2} {
				if got := strings.Count(body, "@"+symbol); got != 1 {
					t.Fatalf("Root references to %q = %d, want 1:\n%s", symbol, got, body)
				}
			}
			for _, forbidden := range []string{"@foo.sleep", "@llgo.coroTimerSleep", "runtime.AllocZ"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("timer Sleep lowering retained forbidden call/allocation %q:\n%s", forbidden, body)
				}
			}
			stateAndPark := regexp.MustCompile(
				`(?s)store i16 4,.*store i16 3,.*store i32 1,.*call void @` + regexp.QuoteMeta(coroTimerParkHookV2) +
					`\(ptr [^,]+, ptr [^,]+, ptr [^,]+, ptr [^,]+, i64 [^)]+\)`,
			)
			if !stateAndPark.MatchString(body) {
				t.Fatalf("Root does not publish Park/Suspended/stateID=1 before Timer V2 park:\n%s", body)
			}
			park := strings.Index(body, "call void @"+coroTimerParkHookV2)
			suspendRelative := strings.Index(body[park:], "call i8 @llvm.coro.suspend")
			resumeRelative := strings.Index(body[park:], "call i32 @"+coroTimerResumeHookV2)
			if park < 0 || suspendRelative < 0 || resumeRelative < 0 || suspendRelative >= resumeRelative {
				t.Fatalf("Root does not park, suspend, then consume Timer V2 status in order:\n%s", body)
			}
			dispatch := regexp.MustCompile(
				`(?s)call i32 @` + regexp.QuoteMeta(coroTimerResumeHookV2) + `\([^\n]+\)\n\s+switch i32 [^\[]+\[(.*?)\]`,
			).FindStringSubmatch(body)
			if len(dispatch) != 2 {
				t.Fatalf("Root has no isolated Timer V2 resume switch:\n%s", body)
			}
			for _, status := range []uint64{coroTimerResumeSuccessV2, coroTimerResumeTaskAbortV2, coroTimerResumeShutdownV2} {
				if !regexp.MustCompile(`(?m)^\s+i32 ` + strconv.FormatUint(status, 10) + `, label `).MatchString(dispatch[1]) {
					t.Fatalf("Root Timer V2 resume switch lacks status %d:\n%s", status, dispatch[0])
				}
			}
			if regexp.MustCompile(`(?m)^\s+i32 ` + strconv.FormatUint(coroTimerResumeOperationCanceledV2, 10) + `, label `).MatchString(dispatch[1]) {
				t.Fatalf("ordinary Sleep accepts an operation-only cancellation status:\n%s", dispatch[0])
			}

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() || !strings.Contains(resume.String(), "call i32 @"+coroTimerResumeHookV2) {
				t.Fatalf("CoroSplit lost Timer V2 resume dispatch:\n%s", module.String())
			}
			assertCoroCancellationTerminalStatusPublication(t, resume)
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit post-CoroSplit timer Sleep object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			for _, symbol := range []string{coroTimerParkHookV2, coroTimerResumeHookV2} {
				if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(symbol)) {
					t.Fatalf("post-CoroSplit object lost Timer V2 ABI symbol %q", symbol)
				}
			}
		})
	}
}

func TestCoroControlledTimerWaitCurrentFrameNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root, waitCall := compileCoroTimerIntrinsicFixture(
				t, test.target, coroControlledTimerWaitTestSource, "wait",
			)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			rootPlan, ok := plan.FunctionPlan(root)
			if !ok || rootPlan.Emission != coro.EmitCoroutine || !rootPlan.Effect.Contains(coro.MayPark) ||
				!plan.ElidesCall(waitCall) {
				t.Fatalf("controlled Timer Root plan = %+v, present=%t, elided=%t", rootPlan, ok, plan.ElidesCall(waitCall))
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify controlled timer coroutine before CoroSplit: %v\n%s", err, module.String())
			}
			physical := requireCoroPhysicalFunction(t, module, "foo.Root")
			body := physical.String()
			if got := strings.Count(body, "call i8 @llvm.coro.suspend"); got != 3 {
				t.Fatalf("controlled Timer coro.suspend calls = %d, want initial + timer + final:\n%s", got, body)
			}
			for _, symbol := range []string{coroControlledTimerParkHookV2, coroTimerResumeHookV2} {
				if got := strings.Count(body, "@"+symbol); got != 1 {
					t.Fatalf("controlled Timer references to %q = %d, want 1:\n%s", symbol, got, body)
				}
			}
			for _, forbidden := range []string{"@foo.wait", "@llgo.coroControlledTimerWait", "runtime.AllocZ"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("controlled Timer lowering retained forbidden call/allocation %q:\n%s", forbidden, body)
				}
			}
			dispatch := regexp.MustCompile(
				`(?s)call i32 @` + regexp.QuoteMeta(coroTimerResumeHookV2) + `\([^\n]+\)\n\s+store i32 [^\n]+\n\s+switch i32 [^\[]+\[(.*?)\]`,
			).FindStringSubmatch(body)
			if len(dispatch) != 2 {
				t.Fatalf("controlled Timer has no isolated V2 resume switch:\n%s", body)
			}
			for _, status := range []uint64{
				coroTimerResumeSuccessV2,
				coroTimerResumeOperationCanceledV2,
				coroTimerResumeTaskAbortV2,
				coroTimerResumeShutdownV2,
			} {
				if !regexp.MustCompile(`(?m)^\s+i32 ` + strconv.FormatUint(status, 10) + `, label `).MatchString(dispatch[1]) {
					t.Fatalf("controlled Timer V2 resume switch lacks status %d:\n%s", status, dispatch[0])
				}
			}

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() || !strings.Contains(resume.String(), "call i32 @"+coroTimerResumeHookV2) {
				t.Fatalf("CoroSplit lost controlled Timer V2 resume dispatch:\n%s", module.String())
			}
		})
	}
}

func compileCoroTimerSleepFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Call,
) {
	return compileCoroTimerIntrinsicFixture(t, target, coroTimerSleepTestSource, "sleep")
}

func compileCoroTimerIntrinsicFixture(t *testing.T, target *llssa.Target, source, calleeName string) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Call,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
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
		if runtimePackage.Scope().Lookup("CoroTimerParkV2") == nil {
			name := types.NewTypeName(token.NoPos, runtimePackage, "CoroTimerParkV2", nil)
			types.NewNamed(name, types.NewArray(types.Typ[types.Uintptr], 32), nil)
			if previous := runtimePackage.Scope().Insert(name); previous != nil {
				t.Fatalf("install Timer V2 test runtime type: duplicate %v", previous)
			}
		}
		return runtimePackage
	})
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
	root := ssaPkg.Func("Root")
	var intrinsicCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if ok && call.Call.StaticCallee() != nil && call.Call.StaticCallee().Name() == calleeName {
				intrinsicCall = call
			}
		}
	}
	if intrinsicCall == nil {
		prog.Dispose()
		t.Fatalf("fixture has no direct %s intrinsic call", calleeName)
	}
	semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(intrinsicCall)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineSuspend {
		prog.Dispose()
		t.Fatalf("%s semantics = %v, %t, %v; want InlineSuspend, true, nil", calleeName, semantics, intrinsic, err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
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
	return prog, pkg, plan, root, intrinsicCall
}
