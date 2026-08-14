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

const coroCopyRuntimeFixture = `package runtime
import "unsafe"

type Slice struct {
	Data unsafe.Pointer
	Len int
	Cap int
}

type String struct {
	Data unsafe.Pointer
	Len int
}

func SliceCopy(destination Slice, data unsafe.Pointer, count, elementSize int) int {
	if count > destination.Len { return destination.Len }
	return count
}
`

const coroCopyFixture = `package foo

func CopySlice(destination, source []byte, wrong []rune) int {
	return copy(destination, source)
}

func CopyString(destination []byte, source string) int {
	return copy(destination, source)
}
`

type coroCopyTestPlan struct {
	prog       llssa.Program
	runtimePkg emissionTestPackage
	fooPkg     emissionTestPackage
	universe   *EmissionUniverse
	plan       *coro.SSAPlan
	functions  map[string]*ssa.Function
	calls      map[string]*ssa.Call
}

func TestCoroCopyHelperNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			fixture := prepareCoroCopyTestPlan(t, target.target, true)
			defer fixture.prog.Dispose()

			for name, call := range fixture.calls {
				audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.functions[name], "")
				if err != nil {
					t.Fatal(err)
				}
				audit.allowImplicitNilFault = true
				if reason := audit.validateCopyBuiltin(call); reason != "" {
					t.Fatalf("%s copy rejected: %s", name, reason)
				}
			}

			helper := fixture.runtimePkg.ssa.Func("SliceCopy")
			helperPlan, ok := fixture.plan.FunctionPlan(helper)
			if !ok || helperPlan.External != coro.Defined || helperPlan.Emission != coro.EmitPlain ||
				helperPlan.Primary != coro.PrimaryPlain || helperPlan.FuncRep != coro.DirectPlain ||
				helperPlan.Effect != coro.NoSuspend || helperPlan.Exec.Contains(coro.MayUnwind) {
				t.Fatalf("SliceCopy plan = %+v, present=%t; want exact no-unwind direct plain helper", helperPlan, ok)
			}

			compilation := &Compilation{CoroPlan: fixture.plan, EmissionUniverse: fixture.universe}
			enableCoroChildAwaitCompilation(compilation)
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			runtimeLL, _, err := NewPackageExWithEmbedOptions(
				fixture.prog, nil, nil, nil, fixture.runtimePkg.ssa, []*ast.File{fixture.runtimePkg.file}, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatalf("compile SliceCopy helper: %v", err)
			}
			runtimeModule := runtimeLL.Module()
			defer runtimeModule.Dispose()
			fooLL, _, err := NewPackageExWithEmbedOptions(
				fixture.prog, nil, nil, nil, fixture.fooPkg.ssa, []*ast.File{fixture.fooPkg.file}, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatalf("compile copy owners: %v", err)
			}
			fooModule := fooLL.Module()
			defer fooModule.Dispose()
			for name, module := range map[string]llvm.Module{"runtime": runtimeModule, "foo": fooModule} {
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatalf("verify %s before CoroSplit: %v\n%s", name, err, module.String())
				}
			}
			for name := range fixture.functions {
				body := requireCoroPhysicalFunction(t, fooModule, "foo."+name).String()
				if !strings.Contains(body, "runtime.SliceCopy") || strings.Contains(body, "runtime.SliceCopy$coro") {
					t.Fatalf("%s did not call the exact plain SliceCopy helper:\n%s", name, body)
				}
				if strings.Contains(body, coroAwaitPrepareInlineHookV4) || strings.Contains(body, coroAwaitConsumeHookV1) {
					t.Fatalf("%s awaited a proven no-suspend SliceCopy helper:\n%s", name, body)
				}
			}

			for _, module := range []llvm.Module{runtimeModule, fooModule} {
				runCoroABITestPipeline(t, fixture.prog, module)
				object, err := fixture.prog.TargetMachine().EmitToMemoryBuffer(module, llvm.ObjectFile)
				if err != nil {
					t.Fatalf("emit post-CoroSplit copy object: %v\n%s", err, module.String())
				}
				if len(object.Bytes()) == 0 {
					object.Dispose()
					t.Fatal("post-CoroSplit copy object is empty")
				}
				object.Dispose()
			}
			if !bytes.Contains([]byte(fooModule.String()), []byte("foo.CopySlice$coro.resume")) {
				t.Fatalf("CoroSplit lost the copy owner resume entry:\n%s", fooModule.String())
			}
		})
	}
}

func TestCoroCopyHelperFailClosed(t *testing.T) {
	t.Run("lowered fact required", func(t *testing.T) {
		fixture := prepareCoroCopyTestPlan(t, nil, false)
		defer fixture.prog.Dispose()
		audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.functions["CopySlice"], "")
		if err != nil {
			t.Fatal(err)
		}
		audit.allowImplicitNilFault = true
		if reason := audit.validateCopyBuiltin(fixture.calls["CopySlice"]); !strings.Contains(reason, "exact coroutine-safe lowered-call plan") {
			t.Fatalf("missing-fact rejection = %q", reason)
		}
	})

	t.Run("malformed shape", func(t *testing.T) {
		fixture := prepareCoroCopyTestPlan(t, nil, true)
		defer fixture.prog.Dispose()
		call := fixture.calls["CopySlice"]
		audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.functions["CopySlice"], "")
		if err != nil {
			t.Fatal(err)
		}
		args := call.Call.Args
		call.Call.Args = args[:1]
		defer func() { call.Call.Args = args }()
		if reason := audit.validateCopyBuiltin(call); !strings.Contains(reason, "invalid argument/result shape") {
			t.Fatalf("malformed copy rejection = %q", reason)
		}
	})

	t.Run("element mismatch", func(t *testing.T) {
		fixture := prepareCoroCopyTestPlan(t, nil, true)
		defer fixture.prog.Dispose()
		function := fixture.functions["CopySlice"]
		call := fixture.calls["CopySlice"]
		audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, function, "")
		if err != nil {
			t.Fatal(err)
		}
		source := call.Call.Args[1]
		call.Call.Args[1] = function.Params[2]
		defer func() { call.Call.Args[1] = source }()
		if reason := audit.validateCopyBuiltin(call); !strings.Contains(reason, "element types differ") {
			t.Fatalf("mismatched copy rejection = %q", reason)
		}
	})
}

func prepareCoroCopyTestPlan(t *testing.T, target *llssa.Target, loweredCalls bool) coroCopyTestPlan {
	t.Helper()
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, coroCopyRuntimeFixture)
	fooPkg := testProg.addPackage(t, "foo", coroCopyFixture)
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
	functions := map[string]*ssa.Function{
		"CopySlice":  fooPkg.ssa.Func("CopySlice"),
		"CopyString": fooPkg.ssa.Func("CopyString"),
	}
	calls := make(map[string]*ssa.Call, len(functions))
	var roots coro.Roots
	for name, function := range functions {
		calls[name] = coroCopyBuiltinCall(t, function)
		roots = append(roots, coro.Root{Function: function, Demand: coro.AsyncDemand})
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	config := coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: -1,
		ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
			for _, root := range functions {
				if function == root {
					return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
				}
			}
			return coro.SSAFunctionPolicy{}, nil
		},
	}
	if loweredCalls {
		config.ClassifyLoweredCalls = universe.CoroLoweredCalls
	}
	plan, err := coro.AnalyzeSSA(fooPkg.ssa.Prog, roots, config)
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	return coroCopyTestPlan{
		prog:       prog,
		runtimePkg: runtimePkg,
		fooPkg:     fooPkg,
		universe:   universe,
		plan:       plan,
		functions:  functions,
		calls:      calls,
	}
}

func coroCopyBuiltinCall(t *testing.T, function *ssa.Function) *ssa.Call {
	t.Helper()
	var found *ssa.Call
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			builtin, ok := call.Call.Value.(*ssa.Builtin)
			if !ok || builtin.Name() != "copy" {
				continue
			}
			if found != nil {
				t.Fatalf("%s has more than one copy builtin", function)
			}
			found = call
		}
	}
	if found == nil {
		t.Fatalf("%s has no copy builtin", function)
	}
	return found
}
