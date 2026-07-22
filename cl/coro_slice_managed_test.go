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
	"go/types"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

const coroManagedSliceRuntimeFixture = `package runtime
import "unsafe"

type Slice struct {
	Data unsafe.Pointer
	Len int
	Cap int
}

func MakeSlice(length, capacity, elementSize int) Slice {
	return Slice{nil, length, capacity}
}

func SliceAppend(source Slice, data unsafe.Pointer, count, elementSize int) Slice {
	source.Len += count
	return source
}
`

const coroManagedSliceFixture = `package foo

func Root(source []byte, length, capacity int) []byte {
	result := make([]byte, length, capacity)
	return append(result, source...)
}
`

type coroManagedSlicePlanOptions struct {
	outcome       coro.OutcomeMode
	loweredCalls  bool
	forceRootCoro bool
}

type coroManagedSliceTestPlan struct {
	prog       llssa.Program
	runtimePkg emissionTestPackage
	fooPkg     emissionTestPackage
	universe   *EmissionUniverse
	plan       *coro.SSAPlan
	root       *ssa.Function
	makeSlice  *ssa.MakeSlice
	appendCall *ssa.Call
}

func TestCoroManagedSliceHelpersNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := prepareCoroManagedSliceTestPlan(t, test.target, coroManagedSlicePlanOptions{
				outcome:      coro.OutcomeExplicitStatus,
				loweredCalls: true,
			})
			defer fixture.prog.Dispose()

			audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
			if err != nil {
				t.Fatal(err)
			}
			audit.allowImplicitNilFault = true
			if reason := audit.validateMakeSlice(fixture.makeSlice); reason != "" {
				t.Fatalf("MakeSlice rejected: %s", reason)
			}
			if reason := audit.validateAppendBuiltin(fixture.appendCall); reason != "" {
				t.Fatalf("append rejected: %s", reason)
			}
			for name, helper := range map[string]*ssa.Function{
				"MakeSlice":   fixture.runtimePkg.ssa.Func("MakeSlice"),
				"SliceAppend": fixture.runtimePkg.ssa.Func("SliceAppend"),
			} {
				plan, ok := fixture.plan.FunctionPlan(helper)
				if !ok || plan.External != coro.Defined || plan.Emission != coro.EmitCoroutine ||
					plan.Primary != coro.PrimaryCoroutine || plan.FuncRep != coro.DirectCoro ||
					!plan.Demand.Contains(coro.AsyncDemand) || !plan.Effect.Contains(coro.OutcomeStructured) ||
					!plan.Exec.Contains(coro.MayUnwind) {
					t.Fatalf("%s plan = %+v, present=%t; want demanded ExplicitStatus coroutine", name, plan, ok)
				}
			}

			compilation := &Compilation{CoroPlan: fixture.plan, EmissionUniverse: fixture.universe}
			enableCoroChildAwaitCompilation(compilation)
			compilation.CoroProfile = CoroProfileStackless
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			runtimeLL, _, err := NewPackageExWithEmbedOptions(
				fixture.prog, nil, nil, nil, fixture.runtimePkg.ssa, []*ast.File{fixture.runtimePkg.file}, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatalf("compile runtime helpers: %v", err)
			}
			runtimeModule := runtimeLL.Module()
			defer runtimeModule.Dispose()
			fooLL, _, err := NewPackageExWithEmbedOptions(
				fixture.prog, nil, nil, nil, fixture.fooPkg.ssa, []*ast.File{fixture.fooPkg.file}, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatalf("compile append owner: %v", err)
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
				"runtime.MakeSlice$coro",
				"runtime.SliceAppend$coro",
				"call void @" + coroAwaitPrepareHookV1,
				"call i32 @" + coroAwaitConsumeHookV1,
			} {
				if !strings.Contains(rootIR, required) {
					t.Fatalf("managed slice owner lacks %q:\n%s", required, rootIR)
				}
			}
			if got := strings.Count(rootIR, "call void @"+coroAwaitPrepareHookV1); got != 2 {
				t.Fatalf("managed slice awaits = %d, want MakeSlice + SliceAppend:\n%s", got, rootIR)
			}

			for _, module := range []llvm.Module{runtimeModule, fooModule} {
				runCoroABITestPipeline(t, fixture.prog, module)
				object, err := fixture.prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
				if err != nil {
					t.Fatalf("emit post-CoroSplit object: %v\n%s", err, module.String())
				}
				if len(object.Bytes()) == 0 {
					object.Dispose()
					t.Fatal("post-CoroSplit managed slice object is empty")
				}
				object.Dispose()
			}
			if !bytes.Contains([]byte(fooModule.String()), []byte("foo.Root$coro.resume")) {
				t.Fatalf("CoroSplit lost the managed slice resume entry:\n%s", fooModule.String())
			}
		})
	}
}

func TestCoroManagedSliceHelpersFailClosed(t *testing.T) {
	t.Run("explicit status required", func(t *testing.T) {
		fixture := prepareCoroManagedSliceTestPlan(t, nil, coroManagedSlicePlanOptions{
			outcome:      coro.OutcomeExplicitStatus,
			loweredCalls: true,
		})
		defer fixture.prog.Dispose()
		audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
		if err != nil {
			t.Fatal(err)
		}
		for name, reason := range map[string]string{
			"append":    audit.validateAppendBuiltin(fixture.appendCall),
			"MakeSlice": audit.validateMakeSlice(fixture.makeSlice),
		} {
			if !strings.Contains(reason, "explicit-status panic ABI") {
				t.Fatalf("%s rejection = %q", name, reason)
			}
		}
	})

	t.Run("lowered fact required", func(t *testing.T) {
		fixture := prepareCoroManagedSliceTestPlan(t, nil, coroManagedSlicePlanOptions{
			outcome: coro.OutcomeExplicitStatus,
		})
		defer fixture.prog.Dispose()
		audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
		if err != nil {
			t.Fatal(err)
		}
		audit.allowImplicitNilFault = true
		for name, reason := range map[string]string{
			"append":    audit.validateAppendBuiltin(fixture.appendCall),
			"MakeSlice": audit.validateMakeSlice(fixture.makeSlice),
		} {
			if !strings.Contains(reason, "exact coroutine-safe lowered-call plan") {
				t.Fatalf("%s missing-fact rejection = %q", name, reason)
			}
		}
	})

	t.Run("plain MayUnwind helper rejected", func(t *testing.T) {
		fixture := prepareCoroManagedSliceTestPlan(t, nil, coroManagedSlicePlanOptions{
			loweredCalls:  true,
			forceRootCoro: true,
		})
		defer fixture.prog.Dispose()
		audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
		if err != nil {
			t.Fatal(err)
		}
		audit.allowImplicitNilFault = true
		for name, reason := range map[string]string{
			"append":    audit.validateAppendBuiltin(fixture.appendCall),
			"MakeSlice": audit.validateMakeSlice(fixture.makeSlice),
		} {
			if !strings.Contains(reason, "exact coroutine-safe lowered-call plan") {
				t.Fatalf("%s plain-MayUnwind rejection = %q", name, reason)
			}
		}
	})

	t.Run("malformed append shape", func(t *testing.T) {
		fixture := prepareCoroManagedSliceTestPlan(t, nil, coroManagedSlicePlanOptions{
			outcome:      coro.OutcomeExplicitStatus,
			loweredCalls: true,
		})
		defer fixture.prog.Dispose()
		audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
		if err != nil {
			t.Fatal(err)
		}
		audit.allowImplicitNilFault = true
		args := fixture.appendCall.Call.Args
		fixture.appendCall.Call.Args = args[:1]
		defer func() { fixture.appendCall.Call.Args = args }()
		if reason := audit.validateAppendBuiltin(fixture.appendCall); !strings.Contains(reason, "invalid argument/result shape") {
			t.Fatalf("malformed append rejection = %q", reason)
		}
	})
}

func prepareCoroManagedSliceTestPlan(
	t *testing.T, target *llssa.Target, options coroManagedSlicePlanOptions,
) coroManagedSliceTestPlan {
	t.Helper()
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, coroManagedSliceRuntimeFixture)
	fooPkg := testProg.addPackage(t, "foo", coroManagedSliceFixture)
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
	makeSlice, appendCall := coroManagedSliceInstructions(t, root)
	makeSliceHelper := runtimePkg.ssa.Func("MakeSlice")
	appendHelper := runtimePkg.ssa.Func("SliceAppend")
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	config := coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		OutcomeMode:          options.outcome,
		ClassifyFunction: func(fn *ssa.Function) (coro.SSAFunctionPolicy, error) {
			if fn == makeSliceHelper || fn == appendHelper {
				return coro.SSAFunctionPolicy{Exec: coro.MayUnwind}, nil
			}
			if fn == root && options.forceRootCoro {
				return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	}
	if options.loweredCalls {
		config.ClassifyLoweredCalls = universe.CoroLoweredCalls
	}
	plan, err := coro.AnalyzeSSA(fooPkg.ssa.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, config)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return coroManagedSliceTestPlan{
		prog:       prog,
		runtimePkg: runtimePkg,
		fooPkg:     fooPkg,
		universe:   universe,
		plan:       plan,
		root:       root,
		makeSlice:  makeSlice,
		appendCall: appendCall,
	}
}

func coroManagedSliceInstructions(t *testing.T, root *ssa.Function) (*ssa.MakeSlice, *ssa.Call) {
	t.Helper()
	var makeSlice *ssa.MakeSlice
	var appendCall *ssa.Call
	for _, block := range root.Blocks {
		for _, instruction := range block.Instrs {
			switch instruction := instruction.(type) {
			case *ssa.MakeSlice:
				makeSlice = instruction
			case *ssa.Call:
				if builtin, ok := instruction.Call.Value.(*ssa.Builtin); ok && builtin.Name() == "append" {
					appendCall = instruction
				}
			}
		}
	}
	if makeSlice == nil || appendCall == nil {
		t.Fatalf("managed slice fixture lacks MakeSlice/append:\n%s", root.String())
	}
	return makeSlice, appendCall
}
