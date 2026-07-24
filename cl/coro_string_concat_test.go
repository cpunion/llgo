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
	"go/token"
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroStringConcatRuntimeFixture = `package runtime
import "unsafe"

type String struct {
	Data unsafe.Pointer
	Len int
}

func AllocU(uintptr) unsafe.Pointer { return nil }

// The production helper's possible length panic is represented by the test
// plan's MayUnwind policy so codegen can focus on the exact managed edge. A
// separate core-plan test derives that policy from an actual panic instruction.
func StringCat(left, right String) String {
	length := left.Len + right.Len
	return String{AllocU(uintptr(length)), length}
}
`

const coroStringConcatFixture = `package foo

func Pause() {}

func Root(left, right string) string {
	prefix := left + right
	Pause()
	return prefix + left
}
`

type coroStringConcatTestPlan struct {
	prog       llssa.Program
	runtimePkg emissionTestPackage
	fooPkg     emissionTestPackage
	universe   *EmissionUniverse
	plan       *coro.SSAPlan
	root       *ssa.Function
	concats    []*ssa.BinOp
}

func TestCoroStringConcatManagedHelperNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareCoroStringConcatTestPlan(t, test.target, true)
			defer fixture.prog.Dispose()

			audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
			if err != nil {
				t.Fatal(err)
			}
			audit.allowImplicitNilFault = true
			for index, concat := range fixture.concats {
				if reason := audit.validateBinOp(concat); reason != "" {
					t.Fatalf("string concat %d rejected: %s", index, reason)
				}
			}

			helper := fixture.runtimePkg.ssa.Func("StringCat")
			helperPlan, ok := fixture.plan.FunctionPlan(helper)
			if !ok || helperPlan.External != coro.Defined || helperPlan.Emission != coro.EmitCoroutine ||
				helperPlan.Primary != coro.PrimaryCoroutine || helperPlan.FuncRep != coro.DirectCoro ||
				!helperPlan.Demand.Contains(coro.AsyncDemand) || !helperPlan.Effect.Contains(coro.OutcomeStructured) ||
				!helperPlan.Exec.Contains(coro.MayUnwind) {
				t.Fatalf("StringCat plan = %+v, present=%t; want demanded ExplicitStatus coroutine", helperPlan, ok)
			}

			compilation := &Compilation{CoroPlan: fixture.plan, EmissionUniverse: fixture.universe}
			enableCoroChildAwaitCompilation(compilation)
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			runtimeLL, _, err := NewPackageExWithEmbedOptions(
				fixture.prog, nil, nil, nil, fixture.runtimePkg.ssa, []*ast.File{fixture.runtimePkg.file}, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatalf("compile StringCat helper: %v", err)
			}
			runtimeModule := runtimeLL.Module()
			defer runtimeModule.Dispose()
			fooLL, _, err := NewPackageExWithEmbedOptions(
				fixture.prog, nil, nil, nil, fixture.fooPkg.ssa, []*ast.File{fixture.fooPkg.file}, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatalf("compile string concat owner: %v", err)
			}
			fooModule := fooLL.Module()
			defer fooModule.Dispose()
			for name, module := range map[string]llvm.Module{"runtime": runtimeModule, "foo": fooModule} {
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatalf("verify %s before CoroSplit: %v\n%s", name, err, module.String())
				}
			}

			rootIR := requireCoroPhysicalFunction(t, fooModule, "foo.Root").String()
			for _, required := range []string{
				"runtime.StringCat$coro",
				"foo.Pause$coro",
				"call void @" + coroAwaitPrepareHookV1,
				"call i32 @" + coroAwaitConsumeHookV1,
			} {
				if !strings.Contains(rootIR, required) {
					t.Fatalf("managed string concat owner lacks %q:\n%s", required, rootIR)
				}
			}
			if got := strings.Count(rootIR, "runtime.StringCat$coro"); got != 2 {
				t.Fatalf("managed StringCat calls = %d, want two across Pause:\n%s", got, rootIR)
			}
			if got := strings.Count(rootIR, "call void @"+coroAwaitPrepareHookV1); got != 3 {
				t.Fatalf("managed awaits = %d, want StringCat + Pause + StringCat:\n%s", got, rootIR)
			}

			for _, module := range []llvm.Module{runtimeModule, fooModule} {
				runCoroABITestPipeline(t, fixture.prog, module)
				object, err := fixture.prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
				if err != nil {
					t.Fatalf("emit post-CoroSplit string concat object: %v\n%s", err, module.String())
				}
				if len(object.Bytes()) == 0 {
					object.Dispose()
					t.Fatal("post-CoroSplit string concat object is empty")
				}
				object.Dispose()
			}
			if !bytes.Contains([]byte(fooModule.String()), []byte("foo.Root$coro.resume")) {
				t.Fatalf("CoroSplit lost the string concat owner resume entry:\n%s", fooModule.String())
			}
		})
	}
}

func TestCoroStringConcatManagedHelperFailsClosed(t *testing.T) {
	t.Run("explicit status required", func(t *testing.T) {
		fixture := prepareCoroStringConcatTestPlan(t, nil, true)
		defer fixture.prog.Dispose()
		audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
		if err != nil {
			t.Fatal(err)
		}
		if reason := audit.validateBinOp(fixture.concats[0]); !strings.Contains(reason, "explicit-status panic ABI") {
			t.Fatalf("missing explicit-status rejection = %q", reason)
		}
	})

	t.Run("lowered fact required", func(t *testing.T) {
		fixture := prepareCoroStringConcatTestPlan(t, nil, false)
		defer fixture.prog.Dispose()
		audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
		if err != nil {
			t.Fatal(err)
		}
		audit.allowImplicitNilFault = true
		if reason := audit.validateBinOp(fixture.concats[0]); !strings.Contains(reason, "exact coroutine-safe lowered-call plan") {
			t.Fatalf("missing lowered-call rejection = %q", reason)
		}
	})
}

func prepareCoroStringConcatTestPlan(t *testing.T, target *llssa.Target, loweredCalls bool) coroStringConcatTestPlan {
	t.Helper()
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, coroStringConcatRuntimeFixture)
	fooPkg := testProg.addPackage(t, "foo", coroStringConcatFixture)
	testProg.ssa.Build()

	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	prog.SetRuntime(runtimePkg.types)
	universe, err := prepareStacklessEmissionUniverseWithOptions(prog, nil, []EmissionPackage{
		{SSA: runtimePkg.ssa, Files: []*ast.File{runtimePkg.file}},
		{SSA: fooPkg.ssa, Files: []*ast.File{fooPkg.file}},
	}, EmissionUniverseOptions{CompleteRuntimeABI: true})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(fooPkg.ssa.Prog, universe.Functions())
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	root := fooPkg.ssa.Func("Root")
	concats := coroStringConcatBinOps(t, root)
	stringCat := runtimePkg.ssa.Func("StringCat")
	pause := fooPkg.ssa.Func("Pause")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	config := coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		OutcomeMode:          coro.OutcomeExplicitStatus,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			switch function {
			case stringCat:
				return coro.SSAFunctionPolicy{Exec: coro.MayUnwind}, nil
			case pause:
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			default:
				return coro.SSAFunctionPolicy{}, nil
			}
		},
	}
	if loweredCalls {
		config.ClassifyLoweredCalls = universe.CoroLoweredCalls
	}
	plan, err := coro.AnalyzeSSA(fooPkg.ssa.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, config)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return coroStringConcatTestPlan{
		prog:       prog,
		runtimePkg: runtimePkg,
		fooPkg:     fooPkg,
		universe:   universe,
		plan:       plan,
		root:       root,
		concats:    concats,
	}
}

func coroStringConcatBinOps(t *testing.T, function *ssa.Function) []*ssa.BinOp {
	t.Helper()
	var found []*ssa.BinOp
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			operation, ok := instruction.(*ssa.BinOp)
			if ok && operation.Op == token.ADD {
				if basic, ok := types.Unalias(operation.Type()).Underlying().(*types.Basic); ok && basic.Kind() == types.String {
					found = append(found, operation)
				}
			}
		}
	}
	if len(found) != 2 {
		t.Fatalf("%s string concatenations = %d, want two\n%s", function, len(found), function.String())
	}
	return found
}
