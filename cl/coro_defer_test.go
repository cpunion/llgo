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

const coroStaticCleanupIRFixture = `package foo
var Sink uint32
var PanicPayload uint32

type Guard struct{}

func First(value uint32) { Sink = Sink*10 + value }
func Second(value uint32) { Sink = Sink*10 + value }
func (*Guard) Third(value uint32) { Sink = Sink*10 + value }

func Root(guard *Guard, mode uint32) {
	defer First(1)
	defer Second(mode + 2)
	defer guard.Third(mode + 3)
	if mode == 9 { panic(&PanicPayload) }
}
`

func TestCoroStaticCleanupIRNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, root := compileCoroStaticCleanupIRFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			rootPlan, ok := plan.FunctionPlan(root)
			if !ok || rootPlan.Emission != coro.EmitCoroutine ||
				!rootPlan.Exec.Contains(coro.NeedsCleanupFrame) || !rootPlan.Effect.Contains(coro.AwaitStructured) {
				t.Fatalf("Root cleanup plan = %+v, present=%t", rootPlan, ok)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify static cleanup before CoroSplit: %v\n%s", err, module.String())
			}
			body := requireCoroPhysicalFunction(t, module, "foo.Root").String()
			for _, symbol := range []string{"foo.First$coro", "foo.Second$coro", "Third$coro"} {
				if got := strings.Count(body, symbol); got != 1 {
					t.Fatalf("Root cleanup references %s = %d, want one shared guarded call site:\n%s", symbol, got, body)
				}
			}
			for _, forbidden := range []string{"Sigsetjmp", "SetThreadDefer", "GetThreadDefer", "runtime.RunDefers"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("stackless cleanup retained legacy defer machinery %q:\n%s", forbidden, body)
				}
			}
			if !strings.Contains(body, "switch i32") || strings.Count(body, "alloca i1") < 3 ||
				strings.Count(body, "store i1 false") < 3 || strings.Count(body, "store i1 true") < 3 {
				t.Fatalf("static cleanup frame/continuation state is incomplete:\n%s", body)
			}
			if strings.Count(body, "call void @"+coroPanicPrepareHookV1) != 1 ||
				strings.Count(body, "call void @"+coroCompletePrepareHookV1) != 1 {
				t.Fatalf("panic and completion do not share the cleanup drainer:\n%s", body)
			}

			runCoroABITestPipeline(t, prog, module)
			resume := module.NamedFunction("foo.Root$coro.resume")
			if resume.IsNil() {
				t.Fatalf("CoroSplit did not create Root cleanup resume entry:\n%s", module.String())
			}
			post := resume.String()
			for _, symbol := range []string{"foo.First$coro", "foo.Second$coro", "Third$coro"} {
				if got := strings.Count(post, symbol); got != 1 {
					t.Fatalf("post-split cleanup references %s = %d, want one:\n%s", symbol, got, post)
				}
			}
		})
	}
}

func compileCoroStaticCleanupIRFixture(
	t *testing.T,
	target *llssa.Target,
) (llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroStaticCleanupIRFixture)
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
	root, first, second := ssaPkg.Func("Root"), ssaPkg.Func("First"), ssaPkg.Func("Second")
	var third *ssa.Function
	for _, function := range universe.Functions() {
		if function != nil && function.Name() == "Third" && function.Signature != nil && function.Signature.Recv() != nil {
			third = function
			break
		}
	}
	if third == nil {
		prog.Dispose()
		t.Fatal("Third method is absent from the emission universe")
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == first || function == second || function == third {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
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
	compilation.EnableCoroExplicitStatusPanicABI = true
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return prog, pkg, plan, root
}

func TestCoroStaticCleanupPlainTargetQuery(t *testing.T) {
	const source = `package foo
type Guard struct{}
func (*Guard) release() {}
func Root(guard *Guard) { defer guard.release() }
`
	prog, universe, plan, root, target := buildCoroStaticCleanupPlanFixture(t, source)
	defer prog.Dispose()

	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine || !rootPlan.Exec.Contains(coro.NeedsCleanupFrame) {
		t.Fatalf("Root plan = %+v, present=%t; want cleanup coroutine", rootPlan, ok)
	}
	targetPlan, ok := plan.FunctionPlan(target)
	if !ok || targetPlan.Emission != coro.EmitPlain || targetPlan.FuncRep != coro.DirectPlain {
		t.Fatalf("release plan = %+v, present=%t; want DirectPlain", targetPlan, ok)
	}
	cleanup, err := prepareCoroStaticCleanupPlan(root, plan, universe, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup == nil || len(cleanup.sites) != 1 || cleanup.sites[0].target != target ||
		cleanup.sites[0].kind != coroStaticCleanupPlain || len(cleanup.sites[0].instruction.Call.Args) != 1 {
		t.Fatalf("static receiver cleanup = %+v", cleanup)
	}
	certified, err := universe.CoroStaticCleanupPlainTarget(plan, target, "")
	if err != nil || !certified {
		t.Fatalf("plain cleanup target certified=%t, err=%v", certified, err)
	}
}

func TestCoroStaticCleanupPlainTargetQueryRejectsOtherConsumers(t *testing.T) {
	const source = `package foo
func cleanup() {}
func Root() { defer cleanup(); cleanup() }
`
	prog, universe, plan, _, target := buildCoroStaticCleanupPlanFixture(t, source)
	defer prog.Dispose()
	certified, err := universe.CoroStaticCleanupPlainTarget(plan, target, "")
	if err != nil {
		t.Fatal(err)
	}
	if certified {
		t.Fatal("plain cleanup target with an ordinary call consumer was certified")
	}
}

func TestCoroStaticCleanupPlanFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		explicit bool
		want     string
	}{
		{
			name: "legacy panic ABI",
			source: `package foo
func cleanup() {}
func Root() { defer cleanup() }
`,
			want: "legacy panic",
		},
		{
			name: "captured closure",
			source: `package foo
func Root(value uint32) { defer func() { _ = value }() }
`,
			explicit: true,
			want:     "closure",
		},
		{
			name: "loop registration",
			source: `package foo
func cleanup() {}
func Root() { for index := 0; index != 1; index++ { defer cleanup() } }
`,
			explicit: true,
			want:     "cyclic block",
		},
		{
			name: "nested cleanup target",
			source: `package foo
func inner() {}
func cleanup() { defer inner() }
func Root() { defer cleanup() }
`,
			explicit: true,
			want:     "nested cleanup",
		},
		{
			name: "cleanup child panic",
			source: `package foo
var Payload uint32
func cleanup() { panic(&Payload) }
func Root() { defer cleanup() }
`,
			explicit: true,
			want:     "no-unwind proof",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prog, universe, plan, root, _ := buildCoroStaticCleanupPlanFixture(t, test.source)
			defer prog.Dispose()
			_, err := prepareCoroStaticCleanupPlan(root, plan, universe, "", test.explicit)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("cleanup preflight error = %v, want %q", err, test.want)
			}
		})
	}
}

func buildCoroStaticCleanupPlanFixture(
	t *testing.T,
	source string,
) (llssa.Program, *EmissionUniverse, *coro.SSAPlan, *ssa.Function, *ssa.Function) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
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
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerChildAwaitABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if function == root {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	var target *ssa.Function
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			if deferred, ok := instruction.(*ssa.Defer); ok {
				target = deferred.Call.StaticCallee()
				break
			}
		}
	}
	return prog, universe, plan, root, target
}
