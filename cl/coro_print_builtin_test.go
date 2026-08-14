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

const coroPrintRuntimeFixture = `package runtime
import "unsafe"

type String struct {
	Data unsafe.Pointer
	Len int
}

func PrintByte(byte) {}
func PrintInt(int64) {}
func PrintFloat(float64) {}
func PrintString(String) {}
`

const coroPrintFixture = `package foo

import "unsafe"

func scalarBitcast32(value int32) float32 {
	return *(*float32)(unsafe.Pointer(&value))
}

func scalarBitcast64(value int64) float64 {
	return *(*float64)(unsafe.Pointer(&value))
}

var escapedScalar float32

func returnTransformed(pointer unsafe.Pointer) float32 {
	return scalarBitcast32(int32(uintptr(pointer)))
}

func storeTransformed(pointer unsafe.Pointer) {
	escapedScalar = scalarBitcast32(int32(uintptr(pointer)))
}

func arithmeticTransformed(pointer unsafe.Pointer) float32 {
	return scalarBitcast32(int32(uintptr(pointer))) + 1
}

func Root(number int, text string, pointer unsafe.Pointer) {
	word := uintptr(pointer)
	if word == 0 {
		word = 1
	}
	print(
		"value=", number, int64(word),
		scalarBitcast32(int32(uintptr(pointer))),
		scalarBitcast64(int64(uintptr(pointer))),
	)
	println(text)
}
`

type coroPrintTestPlan struct {
	prog       llssa.Program
	runtimePkg emissionTestPackage
	fooPkg     emissionTestPackage
	universe   *EmissionUniverse
	plan       *coro.SSAPlan
	root       *ssa.Function
	calls      map[string]*ssa.Call
}

func TestCoroPointerDerivedScalarTransformResultsRemainFailClosed(t *testing.T) {
	fixture := prepareCoroPrintTestPlan(t, nil, true, false)
	defer fixture.prog.Dispose()
	for _, name := range []string{"returnTransformed", "storeTransformed", "arithmeticTransformed"} {
		function := fixture.fooPkg.ssa.Func(name)
		audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, function, "")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				conversion, ok := instruction.(*ssa.Convert)
				if !ok || !coroFrameRetentionPointerToUintptr(conversion) {
					continue
				}
				found = true
				if reason := audit.validateConvert(conversion); !strings.Contains(reason, "not bound to an exact managed-child/worker") {
					t.Fatalf("%s pointer conversion rejection = %q", name, reason)
				}
			}
		}
		if !found {
			t.Fatalf("%s has no pointer-to-uintptr conversion", name)
		}
	}
}

func TestCoroPrintBuiltinManagedHelpersNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(target.name, func(t *testing.T) {
			fixture := prepareCoroPrintTestPlan(t, target.target, true, false)
			defer fixture.prog.Dispose()

			audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
			if err != nil {
				t.Fatal(err)
			}
			audit.allowImplicitNilFault = true
			proof := audit.currentFrameRetentionProof()
			var pointerWords, integerAliases, transformResults []ssa.Value
			for _, block := range fixture.root.Blocks {
				for _, instruction := range block.Instrs {
					if handled, reason := audit.validate(instruction); handled && reason != "" {
						t.Fatalf("%T %q rejected: %s", instruction, instruction, reason)
					}
					switch instruction := instruction.(type) {
					case *ssa.Convert:
						if coroFrameRetentionPointerToUintptr(instruction) {
							pointerWords = append(pointerWords, instruction)
						} else if instruction.X != nil && coroFrameRetentionUintptrLike(instruction.X.Type()) &&
							coroFrameRetentionIntegerLike(instruction.Type()) {
							integerAliases = append(integerAliases, instruction)
						}
					case *ssa.Call:
						if instruction.Common() != nil && instruction.Common().StaticCallee() != nil &&
							strings.HasPrefix(instruction.Common().StaticCallee().Name(), "scalarBitcast") {
							transformResults = append(transformResults, instruction)
						}
					}
				}
			}
			if len(pointerWords) != 3 || len(integerAliases) != 3 || len(transformResults) != 2 {
				t.Fatalf("pointer print chain values = %d words/%d integer aliases/%d transforms, want 3/3/2",
					len(pointerWords), len(integerAliases), len(transformResults))
			}
			for _, value := range append(append(pointerWords, integerAliases...), transformResults...) {
				if !proof.provesTraceableUintptr(value) {
					t.Fatalf("pointer-derived print value %q has no frozen provenance", value)
				}
			}
			if got := strings.Join(rootNames(proof.exactCallKeepaliveRoots(fixture.calls["print"])), ","); got != "pointer" {
				t.Fatalf("print keepalive roots = %q, want pointer", got)
			}
			printArguments := make(map[ssa.Value]bool)
			for _, argument := range fixture.calls["print"].Common().Args {
				printArguments[argument] = true
			}
			printSources := proof.exactCallKeepaliveSources(fixture.calls["print"])
			if len(printSources) != 3 {
				t.Fatalf("print keepalive sources = %d, want three exact pointer-derived call arguments", len(printSources))
			}
			for _, source := range printSources {
				if !printArguments[source] {
					t.Fatalf("print keepalive source %q is not an exact call argument", source)
				}
			}

			for _, name := range []string{"PrintByte", "PrintFloat", "PrintInt", "PrintString"} {
				helper := fixture.runtimePkg.ssa.Func(name)
				plan, ok := fixture.plan.FunctionPlan(helper)
				if !ok || plan.External != coro.Defined || plan.Emission != coro.EmitCoroutine ||
					plan.Primary != coro.PrimaryCoroutine || plan.FuncRep != coro.DirectCoro ||
					!plan.Demand.Contains(coro.AsyncDemand) || !plan.Effect.MaySuspend() {
					t.Fatalf("%s plan = %+v, present=%t; want demanded managed helper", name, plan, ok)
				}
			}

			compilation := &Compilation{CoroPlan: fixture.plan, EmissionUniverse: fixture.universe}
			enableCoroChildAwaitCompilation(compilation)
			compilation.PanicABI = coro.PanicExplicitStatusABIV0
			runtimeLL, _, err := NewPackageExWithEmbedOptions(
				fixture.prog, nil, nil, nil, fixture.runtimePkg.ssa, []*ast.File{fixture.runtimePkg.file}, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatalf("compile print helpers: %v", err)
			}
			runtimeModule := runtimeLL.Module()
			defer runtimeModule.Dispose()
			fooLL, _, err := NewPackageExWithEmbedOptions(
				fixture.prog, nil, nil, nil, fixture.fooPkg.ssa, []*ast.File{fixture.fooPkg.file}, goembed.VarMap{},
				PackageOptions{Compilation: compilation},
			)
			if err != nil {
				t.Fatalf("compile print owner: %v", err)
			}
			fooModule := fooLL.Module()
			defer fooModule.Dispose()
			for name, module := range map[string]llvm.Module{"runtime": runtimeModule, "foo": fooModule} {
				if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
					t.Fatalf("verify %s before CoroSplit: %v\n%s", name, err, module.String())
				}
			}

			body := requireCoroPhysicalFunction(t, fooModule, "foo.Root").String()
			for _, helper := range []string{"runtime.PrintByte$coro", "runtime.PrintFloat$coro", "runtime.PrintInt$coro", "runtime.PrintString$coro"} {
				if !strings.Contains(body, helper) {
					t.Fatalf("print owner lacks managed helper %q:\n%s", helper, body)
				}
			}
			if got := strings.Count(body, "runtime.PrintString$coro"); got != 2 {
				t.Fatalf("PrintString calls = %d, want 2:\n%s", got, body)
			}
			if !strings.Contains(body, "ptrtoint") {
				t.Fatalf("print owner lost pointer-to-integer transport:\n%s", body)
			}
			for _, transform := range []string{"foo.scalarBitcast32", "foo.scalarBitcast64"} {
				plain := fooModule.NamedFunction(transform)
				if plain.IsNil() || !fooModule.NamedFunction(transform+"$coro").IsNil() ||
					strings.Contains(plain.String(), "call ") || strings.Contains(plain.String(), "llvm.coro.suspend") {
					t.Fatalf("%s is not one call-free plain scalar transform:\n%s", transform, plain.String())
				}
			}
			if got := strings.Count(body, "call i1 @"+coroAwaitPrepareInlineHookV4); got != 7 {
				t.Fatalf("print helper awaits = %d, want 7:\n%s", got, body)
			}

			for _, module := range []llvm.Module{runtimeModule, fooModule} {
				runCoroABITestPipeline(t, fixture.prog, module)
			}
		})
	}
}

func TestCoroPrintBuiltinFailsClosedForBlockingPlainHelper(t *testing.T) {
	fixture := prepareCoroPrintTestPlan(t, nil, true, true)
	defer fixture.prog.Dispose()
	audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
	if err != nil {
		t.Fatal(err)
	}
	audit.allowImplicitNilFault = true
	if reason := audit.validatePrintBuiltin(fixture.calls["print"], "print"); !strings.Contains(reason, "not one non-suspending, non-unwinding direct plain body") {
		t.Fatalf("blocking plain print helper rejection = %q", reason)
	}
}

func TestCoroPrintBuiltinRequiresExactLoweredFacts(t *testing.T) {
	fixture := prepareCoroPrintTestPlan(t, nil, false, false)
	defer fixture.prog.Dispose()
	audit, err := newCoroPhysicalPureSSAAudit(fixture.universe, fixture.plan, fixture.root, "")
	if err != nil {
		t.Fatal(err)
	}
	audit.allowImplicitNilFault = true
	if reason := audit.validatePrintBuiltin(fixture.calls["println"], "println"); !strings.Contains(reason, "lacks an exact non-elided lowered-call fact") {
		t.Fatalf("missing print lowered-fact rejection = %q", reason)
	}
}

func prepareCoroPrintTestPlan(t *testing.T, target *llssa.Target, loweredCalls, blockingPlain bool) coroPrintTestPlan {
	t.Helper()
	testProg := newEmissionTestProgram()
	testProg.ssa.CreatePackage(types.Unsafe, nil, nil, true)
	runtimePkg := testProg.addPackage(t, llssa.PkgRuntime, coroPrintRuntimeFixture)
	fooPkg := testProg.addPackage(t, "foo", coroPrintFixture)
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
	helpers := map[*ssa.Function]bool{
		runtimePkg.ssa.Func("PrintByte"):   true,
		runtimePkg.ssa.Func("PrintFloat"):  true,
		runtimePkg.ssa.Func("PrintInt"):    true,
		runtimePkg.ssa.Func("PrintString"): true,
	}
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
			if !helpers[function] {
				return coro.SSAFunctionPolicy{}, nil
			}
			if blockingPlain {
				return coro.SSAFunctionPolicy{Exec: coro.BlockForeign}, nil
			}
			return coro.SSAFunctionPolicy{Effect: coro.YieldOnly}, nil
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
	return coroPrintTestPlan{
		prog:       prog,
		runtimePkg: runtimePkg,
		fooPkg:     fooPkg,
		universe:   universe,
		plan:       plan,
		root:       root,
		calls:      coroPrintBuiltinCalls(t, root),
	}
}

func coroPrintBuiltinCalls(t *testing.T, function *ssa.Function) map[string]*ssa.Call {
	t.Helper()
	found := make(map[string]*ssa.Call)
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			call, ok := instruction.(*ssa.Call)
			if !ok {
				continue
			}
			builtin, ok := call.Common().Value.(*ssa.Builtin)
			if !ok || builtin.Name() != "print" && builtin.Name() != "println" {
				continue
			}
			if found[builtin.Name()] != nil {
				t.Fatalf("%s has multiple %s builtins", function, builtin.Name())
			}
			found[builtin.Name()] = call
		}
	}
	if found["print"] == nil || found["println"] == nil {
		t.Fatalf("%s print calls = %v, want print and println", function, found)
	}
	return found
}
