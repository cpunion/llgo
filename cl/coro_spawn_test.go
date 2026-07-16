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

const coroClosedStaticSpawnTestSource = `package foo

var Sink uint32

func ArgFirst(value uint32) uint32 { return value + 1 }
func ArgSecond(value uint32) uint32 { return value + 2 }
func Plain(first, second uint32) { Sink = first + second }
func Async(value uint32) { Sink = value }

func Parent(value uint32) {
	Plain(value, value)
	go Plain(ArgFirst(value), ArgSecond(value))
	go Async(value)
}
`

func TestCoroClosedStaticSpawnNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, ssaPkg := compileCoroClosedStaticSpawnFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify closed static spawn before CoroSplit: %v\n%s", err, module.String())
			}
			parentPlan, _ := plan.FunctionPlan(ssaPkg.Func("Parent"))
			if parentPlan.DeclaredEffect != coro.YieldOnly || !parentPlan.LocalEffect.Contains(coro.YieldOnly) ||
				!parentPlan.Effect.Contains(coro.YieldOnly) || parentPlan.Emission != coro.EmitCoroutine ||
				parentPlan.Primary != coro.PrimaryCoroutine || parentPlan.FuncRep != coro.DirectCoro || parentPlan.Demand != coro.AsyncDemand {
				t.Fatalf("Parent plan = %+v", parentPlan)
			}
			plainPlan, _ := plan.FunctionPlan(ssaPkg.Func("Plain"))
			if plainPlan.Emission != coro.EmitCoroutine || plainPlan.Primary != coro.PrimaryCoroutine || plainPlan.FuncRep != coro.DirectCoro ||
				!plainPlan.Effect.Contains(coro.YieldOnly) || plainPlan.Demand != coro.AsyncDemand {
				t.Fatalf("Plain sync+spawn plan = %+v", plainPlan)
			}
			asyncPlan, _ := plan.FunctionPlan(ssaPkg.Func("Async"))
			if asyncPlan.Emission != coro.EmitCoroutine || asyncPlan.Primary != coro.PrimaryCoroutine ||
				asyncPlan.FuncRep != coro.DirectCoro || asyncPlan.Demand != coro.AsyncDemand {
				t.Fatalf("Async spawn plan = %+v", asyncPlan)
			}

			ir := module.String()
			parent := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
			if !module.NamedFunction("foo.Plain").IsNil() || module.NamedFunction("foo.Plain"+coroPrimarySuffix).IsNil() {
				t.Fatalf("bounded sync+spawn target did not retain exactly one preemptible coroutine primary:\n%s", ir)
			}
			if !module.NamedFunction("foo.Async").IsNil() || module.NamedFunction("foo.Async"+coroPrimarySuffix).IsNil() {
				t.Fatalf("Async did not retain exactly one coroutine primary:\n%s", ir)
			}
			if strings.Contains(ir, "__llgo_coro_spawn_plain_adapter") {
				t.Fatalf("spawn target incorrectly gained a second plain-root adapter body:\n%s", ir)
			}

			index := func(pattern string) int {
				match := regexp.MustCompile(pattern).FindStringIndex(parent)
				if match == nil {
					return -1
				}
				return match[0]
			}
			first := index(`call i32 @"?foo\.ArgFirst"?`)
			second := index(`call i32 @"?foo\.ArgSecond"?`)
			begin := strings.Index(parent, "call ptr @"+coroSpawnBeginHookV1)
			plainRoot := -1
			if begin >= 0 {
				if relative := regexp.MustCompile(`call ptr @"?foo\.Plain\$coro"?\(`).FindStringIndex(parent[begin:]); relative != nil {
					plainRoot = begin + relative[0]
				}
			}
			commit := strings.Index(parent, "call void @"+coroSpawnCommitHookV1)
			poll := strings.Index(parent, "call i1 @"+coroPreemptPollHookV1)
			if first < 0 || second < 0 || begin < 0 || plainRoot < 0 || commit < 0 || poll < 0 ||
				!(first < second && second < begin && begin < plainRoot && plainRoot < commit && commit < poll) {
				t.Fatalf("argument/begin/root/commit/safepoint order is invalid:\n%s", parent)
			}
			if got := strings.Count(parent, "call ptr @"+coroSpawnBeginHookV1); got != 2 {
				t.Fatalf("spawn begin calls = %d, want two:\n%s", got, parent)
			}
			if got := strings.Count(parent, "call void @"+coroSpawnCommitHookV1); got != 2 {
				t.Fatalf("spawn commit calls = %d, want two:\n%s", got, parent)
			}
			if got := strings.Count(parent, "call i1 @"+coroPreemptPollHookV1); got != 2 {
				t.Fatalf("post-commit explicit preempt polls = %d, want two:\n%s", got, parent)
			}
			if got := strings.Count(parent, "call void @"+coroYieldPrepareHookV1); got != 2 {
				t.Fatalf("post-commit parent yield handoffs = %d, want two:\n%s", got, parent)
			}
			if !regexp.MustCompile(`call ptr @"?foo\.Async\$coro"?\(`).MatchString(parent) {
				t.Fatalf("suspendable target is not called through its unique physical root:\n%s", parent)
			}
			if strings.Contains(parent[begin:commit], "@llvm.coro.promise") {
				t.Fatalf("independent spawned G incorrectly received an await parent-handle link:\n%s", parent[begin:commit])
			}
			if got := len(regexp.MustCompile(`call ptr @"?foo\.Plain\$coro"?\(`).FindAllStringIndex(parent, -1)); got != 2 {
				t.Fatalf("sync await + spawn calls to the one Plain primary = %d, want two:\n%s", got, parent)
			}
			for _, forbidden := range []string{"CreateThread", "InitThreadAttr", "DestroyThreadAttr", "._llgo_routine$", "pthread", "AllocRoot"} {
				if strings.Contains(ir, forbidden) {
					t.Fatalf("closed static spawn leaked legacy native-stack lowering %q:\n%s", forbidden, ir)
				}
			}

			runCoroABITestPipeline(t, prog, module)
			for _, name := range []string{"foo.Parent$coro", "foo.Plain$coro", "foo.Async$coro"} {
				for _, suffix := range []string{".resume", ".destroy"} {
					if module.NamedFunction(name + suffix).IsNil() {
						t.Fatalf("CoroSplit did not create %s%s:\n%s", name, suffix, module.String())
					}
				}
			}
			for _, intrinsic := range []string{"llvm.coro.id", "llvm.coro.begin", "llvm.coro.suspend", "llvm.coro.end"} {
				if hasLLVMCall(module.String(), intrinsic) {
					t.Fatalf("post-split spawn module still calls %s:\n%s", intrinsic, module.String())
				}
			}
			object, err := prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit post-CoroSplit spawn object: %v\n%s", err, module.String())
			}
			defer object.Dispose()
			for _, symbol := range []string{coroSpawnBeginHookV1, coroSpawnCommitHookV1, "foo.Plain$coro"} {
				if len(object.Bytes()) == 0 || !bytes.Contains(object.Bytes(), []byte(symbol)) {
					t.Fatalf("post-CoroSplit object lost spawn symbol %q", symbol)
				}
			}
		})
	}
}

func compileCoroClosedStaticSpawnFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Package,
) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, coroClosedStaticSpawnTestSource)
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
	parent, plain, async := ssaPkg.Func("Parent"), ssaPkg.Func("Plain"), ssaPkg.Func("Async")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: parent, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == parent || fn == plain || fn == async {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
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
		EnableCoroClosedStaticSpawn:   true,
		EnableCoroProgramBootstrapRun: true,
		CoroABI:                       coro.PhysicalABIV1,
		SchedulerABI:                  coro.SchedulerProgramBootstrapClosedStaticSpawnABIV0,
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
	return prog, pkg, plan, ssaPkg
}

func TestCoroClosedStaticSpawnCompilationCapabilityFailsClosed(t *testing.T) {
	compilation := &Compilation{EnableCoroClosedStaticSpawn: true}
	if err := compilation.preflightCoroPlan(); err == nil || !strings.Contains(err.Error(), "requires coroutine child await") {
		t.Fatalf("capability dependency error = %v", err)
	}
	compilation = &Compilation{
		EnableCoroEntryResolution:   true,
		EnableCoroPhysicalABI:       true,
		EnableCoroChildAwait:        true,
		EnableCoroClosedStaticSpawn: true,
	}
	if err := compilation.preflightCoroPlan(); err == nil || !strings.Contains(err.Error(), "requires runnable program bootstrap v2") {
		t.Fatalf("runnable-bootstrap dependency error = %v", err)
	}
}
