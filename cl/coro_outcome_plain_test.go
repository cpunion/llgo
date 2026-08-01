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

const coroOutcomePlainFixture = `package foo

func Leaf(first, second uint32, choose bool, payload any, fail bool) uint32 {
	if fail {
		panic(payload)
	}
	if choose {
		return second
	}
	return first
}

func Parent(first, second uint32, choose bool, payload any, fail bool) uint32 {
	return Leaf(first, second, choose, payload, fail)
}
`

const coroOutcomePlainDAGFixture = `package foo

func Leaf(first, second uint32, choose bool, payload any, fail bool) uint32 {
	if fail {
		panic(payload)
	}
	if choose {
		return second
	}
	return first
}

func Middle(first, second uint32, choose bool, payload any, fail bool) uint32 {
	return Leaf(first, second, choose, payload, fail)
}

func Parent(first, second uint32, choose bool, payload any, fail bool) uint32 {
	return Middle(first, second, choose, payload, fail)
}
`

const coroOutcomePlainLargeDAGFixture = `package foo

type Huge [131073]byte

func Leaf(value Huge, payload any, fail bool) Huge {
	if fail { panic(payload) }
	return value
}
func Middle(value Huge, payload any, fail bool) Huge { return Leaf(value, payload, fail) }
func Parent(value Huge, payload any, fail bool) Huge { return Middle(value, payload, fail) }
`

func TestCoroOutcomePlainLeafNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, parent, leaf := compileCoroOutcomePlainFixture(t, test.target)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			parentPlan, parentFound := plan.FunctionPlan(parent)
			leafPlan, leafFound := plan.FunctionPlan(leaf)
			if !parentFound || parentPlan.Emission != coro.EmitCoroutine {
				t.Fatalf("Parent plan = %+v, present=%t; want coroutine root", parentPlan, parentFound)
			}
			if !leafFound || leafPlan.Emission != coro.EmitOutcomePlain ||
				leafPlan.AtomicCostProof != coro.AtomicCostLeaf || leafPlan.AtomicCost == 0 {
				t.Fatalf("Leaf plan = %+v, present=%t; want proven outcome-plain leaf", leafPlan, leafFound)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify outcome-plain module before CoroSplit: %v\n%s", err, module.String())
			}
			leafBody := module.NamedFunction("foo.Leaf$outcome")
			if leafBody.IsNil() {
				t.Fatalf("outcome-plain leaf symbol is absent:\n%s", module.String())
			}
			leafIR := leafBody.String()
			if !regexp.MustCompile(`define void @"?foo\.Leaf\$outcome"?\(ptr [^,]+, ptr [^,]+, ptr [^,]+,`).MatchString(leafIR) {
				t.Fatalf("outcome-plain leaf does not use (g, out, completion, args...) -> void ABI:\n%s", leafIR)
			}
			for _, forbidden := range []string{"llvm.coro.", "$coro", ".resume", ".destroy"} {
				if strings.Contains(leafIR, forbidden) {
					t.Fatalf("outcome-plain leaf retained coroutine artifact %q:\n%s", forbidden, leafIR)
				}
			}
			parentIR := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
			if !strings.Contains(parentIR, "foo.Leaf$outcome") || !strings.Contains(parentIR, "switch i32") {
				t.Fatalf("coroutine parent did not directly consume outcome-plain completion:\n%s", parentIR)
			}

			runCoroABITestPipeline(t, prog, module)
			for _, name := range []string{"foo.Leaf$outcome.resume", "foo.Leaf$outcome.destroy"} {
				if !module.NamedFunction(name).IsNil() {
					t.Fatalf("CoroSplit manufactured outcome-plain helper %q:\n%s", name, module.String())
				}
			}
			resume := module.NamedFunction("foo.Parent$coro.resume")
			if resume.IsNil() || !strings.Contains(resume.String(), "foo.Leaf$outcome") {
				t.Fatalf("post-split parent lost direct outcome-plain call:\n%s", module.String())
			}
		})
	}
}

func TestCoroOutcomePlainDAGNativeAndWasm32(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			prog, pkg, plan, parent, middle, leaf := compileCoroOutcomePlainDAGFixture(t, test.target, 128)
			defer prog.Dispose()
			module := pkg.Module()
			defer module.Dispose()

			parentPlan, parentFound := plan.FunctionPlan(parent)
			middlePlan, middleFound := plan.FunctionPlan(middle)
			leafPlan, leafFound := plan.FunctionPlan(leaf)
			if !parentFound || parentPlan.Emission != coro.EmitCoroutine {
				t.Fatalf("Parent plan = %+v, present=%t; want coroutine root", parentPlan, parentFound)
			}
			if !middleFound || middlePlan.Emission != coro.EmitOutcomePlain ||
				middlePlan.AtomicCostProof != coro.AtomicCostDAG || middlePlan.AtomicCost <= leafPlan.AtomicCost {
				t.Fatalf("Middle plan = %+v, present=%t; want proven outcome-plain DAG above leaf cost %d", middlePlan, middleFound, leafPlan.AtomicCost)
			}
			if !leafFound || leafPlan.Emission != coro.EmitOutcomePlain ||
				leafPlan.AtomicCostProof != coro.AtomicCostLeaf || leafPlan.AtomicCost == 0 {
				t.Fatalf("Leaf plan = %+v, present=%t; want proven outcome-plain leaf", leafPlan, leafFound)
			}
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatalf("verify outcome-plain DAG before CoroSplit: %v\n%s", err, module.String())
			}
			middleBody := module.NamedFunction("foo.Middle$outcome")
			if middleBody.IsNil() {
				t.Fatalf("outcome-plain DAG symbol is absent:\n%s", module.String())
			}
			middleIR := middleBody.String()
			if !strings.Contains(middleIR, "foo.Leaf$outcome") || !strings.Contains(middleIR, "switch i32") {
				t.Fatalf("Middle did not synchronously consume Leaf outcome:\n%s", middleIR)
			}
			for _, status := range []uint64{coroAwaitCompletionPanic, coroAwaitCompletionGoexit} {
				if !strings.Contains(middleIR, "store i32 "+strconv.FormatUint(status, 10)+", ptr") {
					t.Fatalf("Middle did not propagate child completion status %d into its parent record:\n%s", status, middleIR)
				}
			}
			for _, forbidden := range []string{"llvm.coro.", "$coro", ".resume", ".destroy", coroAwaitPrepareHookV1, coroAwaitConsumeHookV1} {
				if strings.Contains(middleIR, forbidden) {
					t.Fatalf("outcome-plain DAG retained coroutine artifact %q:\n%s", forbidden, middleIR)
				}
			}
			parentIR := requireCoroPhysicalFunction(t, module, "foo.Parent").String()
			if !strings.Contains(parentIR, "foo.Middle$outcome") || strings.Contains(parentIR, "foo.Leaf$outcome") {
				t.Fatalf("Parent did not consume only the collapsed Middle outcome boundary:\n%s", parentIR)
			}

			runCoroABITestPipeline(t, prog, module)
			for _, base := range []string{"foo.Leaf$outcome", "foo.Middle$outcome"} {
				for _, suffix := range []string{".resume", ".destroy"} {
					if !module.NamedFunction(base + suffix).IsNil() {
						t.Fatalf("CoroSplit manufactured outcome-plain helper %q:\n%s", base+suffix, module.String())
					}
				}
			}
			resume := module.NamedFunction("foo.Parent$coro.resume")
			if resume.IsNil() || !strings.Contains(resume.String(), "foo.Middle$outcome") {
				t.Fatalf("post-split Parent lost direct Middle outcome call:\n%s", module.String())
			}
		})
	}
}

func TestCoroOutcomePlainDAGLargeResultFallsBackBeforeEmission(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	prog, pkg, plan, ssaPkg := compileCoroOutcomePlainSource(
		t, nil, coroOutcomePlainLargeDAGFixture, "Parent", 128,
	)
	defer prog.Dispose()
	module := pkg.Module()
	defer module.Dispose()

	leafPlan, leafFound := plan.FunctionPlan(ssaPkg.Func("Leaf"))
	middlePlan, middleFound := plan.FunctionPlan(ssaPkg.Func("Middle"))
	if !leafFound || leafPlan.Emission != coro.EmitOutcomePlain || leafPlan.AtomicCostProof != coro.AtomicCostLeaf {
		t.Fatalf("large-result Leaf plan = %+v, present=%t; caller-owned result must not reject the leaf", leafPlan, leafFound)
	}
	if !middleFound || middlePlan.Emission != coro.EmitCoroutine ||
		middlePlan.AtomicCostProof != coro.AtomicCostUnproven {
		t.Fatalf("large-result Middle plan = %+v, present=%t; DAG optimization must fall back", middlePlan, middleFound)
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify large-result fallback before CoroSplit: %v\n%s", err, module.String())
	}
	middleIR := requireCoroPhysicalFunction(t, module, "foo.Middle").String()
	if !strings.Contains(middleIR, "foo.Leaf$outcome") || !strings.Contains(middleIR, "runtime.AllocZ") {
		t.Fatalf("full coroutine fallback did not use managed storage for the large Leaf result:\n%s", middleIR)
	}
	runCoroABITestPipeline(t, prog, module)
}

func TestCoroOutcomePlainPhysicalCostAgainstCoroutineBaseline(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			baselineProg, baselinePkg, baselinePlan, _, baselineLeaf :=
				compileCoroOutcomePlainFixtureWithBudget(t, test.target, -1)
			defer baselineProg.Dispose()
			baselineModule := baselinePkg.Module()
			defer baselineModule.Dispose()
			optimizedProg, optimizedPkg, optimizedPlan, _, optimizedLeaf :=
				compileCoroOutcomePlainFixtureWithBudget(t, test.target, 64)
			defer optimizedProg.Dispose()
			optimizedModule := optimizedPkg.Module()
			defer optimizedModule.Dispose()

			baselineLeafPlan, baselineFound := baselinePlan.FunctionPlan(baselineLeaf)
			optimizedLeafPlan, optimizedFound := optimizedPlan.FunctionPlan(optimizedLeaf)
			if !baselineFound || baselineLeafPlan.Emission != coro.EmitCoroutine {
				t.Fatalf("baseline Leaf plan = %+v, present=%t; want coroutine", baselineLeafPlan, baselineFound)
			}
			if !optimizedFound || optimizedLeafPlan.Emission != coro.EmitOutcomePlain {
				t.Fatalf("optimized Leaf plan = %+v, present=%t; want outcome-plain", optimizedLeafPlan, optimizedFound)
			}

			runCoroABITestPipeline(t, baselineProg, baselineModule)
			runCoroABITestPipeline(t, optimizedProg, optimizedModule)
			llssa.RemoveKeepAliveCallsAfterCoroSplit(baselineModule)
			llssa.RemoveKeepAliveCallsAfterCoroSplit(optimizedModule)
			baselineIR := baselineModule.String()
			optimizedIR := optimizedModule.String()

			baselineResume := baselineModule.NamedFunction("foo.Leaf$coro.resume")
			baselineDestroy := baselineModule.NamedFunction("foo.Leaf$coro.destroy")
			if baselineResume.IsNil() || baselineDestroy.IsNil() {
				t.Fatalf("coroutine baseline did not materialize Leaf resume/destroy:\n%s", baselineIR)
			}
			if !optimizedModule.NamedFunction("foo.Leaf$outcome.resume").IsNil() ||
				!optimizedModule.NamedFunction("foo.Leaf$outcome.destroy").IsNil() {
				t.Fatalf("outcome-plain optimization retained Leaf resume/destroy:\n%s", optimizedIR)
			}
			baselineRamp := baselineModule.NamedFunction("foo.Leaf$coro").String()
			optimizedLeafIR := optimizedModule.NamedFunction("foo.Leaf$outcome").String()
			if !strings.Contains(baselineRamp, coroFrameAllocHookV1) ||
				strings.Contains(optimizedLeafIR, coroFrameAllocHookV1) {
				t.Fatalf("Leaf frame-allocation boundary baseline=%t optimized=%t",
					strings.Contains(baselineRamp, coroFrameAllocHookV1),
					strings.Contains(optimizedLeafIR, coroFrameAllocHookV1))
			}
			baselineParent := baselineModule.NamedFunction("foo.Parent$coro.resume").String()
			optimizedParent := optimizedModule.NamedFunction("foo.Parent$coro.resume").String()
			baselineAwaitCalls := strings.Count(baselineParent, coroAwaitPrepareHookV1) +
				strings.Count(baselineParent, coroAwaitConsumeHookV1)
			optimizedAwaitCalls := strings.Count(optimizedParent, coroAwaitPrepareHookV1) +
				strings.Count(optimizedParent, coroAwaitConsumeHookV1)
			if baselineAwaitCalls == 0 || optimizedAwaitCalls != 0 ||
				!strings.Contains(optimizedParent, "foo.Leaf$outcome") {
				t.Fatalf("Leaf scheduling boundary baseline calls=%d optimized calls=%d", baselineAwaitCalls, optimizedAwaitCalls)
			}

			optimizeCoroOutcomeCostModule(t, baselineProg, baselineModule)
			optimizeCoroOutcomeCostModule(t, optimizedProg, optimizedModule)
			baselineObject, err := baselineProg.TargetMachine().EmitToMemoryBuffer(baselineModule, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit coroutine baseline object: %v\n%s", err, baselineModule.String())
			}
			defer baselineObject.Dispose()
			optimizedObject, err := optimizedProg.TargetMachine().EmitToMemoryBuffer(optimizedModule, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit outcome-plain object: %v\n%s", err, optimizedModule.String())
			}
			defer optimizedObject.Dispose()
			baselineBytes, optimizedBytes := len(baselineObject.Bytes()), len(optimizedObject.Bytes())
			if baselineBytes == 0 || optimizedBytes == 0 {
				t.Fatalf("empty cost-comparison object baseline=%d optimized=%d", baselineBytes, optimizedBytes)
			}
			if len(optimizedIR) >= len(baselineIR) || optimizedBytes >= baselineBytes {
				t.Fatalf(
					"outcome-plain did not reduce the exact fixture: IR baseline=%d optimized=%d object baseline=%d optimized=%d",
					len(baselineIR), len(optimizedIR), baselineBytes, optimizedBytes,
				)
			}
			t.Logf(
				"post-split fixture: IR baseline=%d optimized=%d; O2 object baseline=%d optimized=%d; eliminated Leaf frame=1 resume=1 destroy=1 await-hook-refs=%d",
				len(baselineIR), len(optimizedIR), baselineBytes, optimizedBytes, baselineAwaitCalls,
			)
		})
	}
}

func TestCoroOutcomePlainDAGPhysicalCostAgainstCoroutineBaseline(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			baselineProg, baselinePkg, baselinePlan, _, baselineMiddle, baselineLeaf :=
				compileCoroOutcomePlainDAGFixture(t, test.target, -1)
			defer baselineProg.Dispose()
			baselineModule := baselinePkg.Module()
			defer baselineModule.Dispose()
			optimizedProg, optimizedPkg, optimizedPlan, _, optimizedMiddle, optimizedLeaf :=
				compileCoroOutcomePlainDAGFixture(t, test.target, 128)
			defer optimizedProg.Dispose()
			optimizedModule := optimizedPkg.Module()
			defer optimizedModule.Dispose()

			for _, fn := range []*ssa.Function{baselineMiddle, baselineLeaf} {
				if got, found := baselinePlan.FunctionPlan(fn); !found || got.Emission != coro.EmitCoroutine {
					t.Fatalf("baseline %s plan = %+v, present=%t; want coroutine", fn.Name(), got, found)
				}
			}
			if got, found := optimizedPlan.FunctionPlan(optimizedMiddle); !found ||
				got.Emission != coro.EmitOutcomePlain || got.AtomicCostProof != coro.AtomicCostDAG {
				t.Fatalf("optimized Middle plan = %+v, present=%t; want outcome DAG", got, found)
			}
			if got, found := optimizedPlan.FunctionPlan(optimizedLeaf); !found ||
				got.Emission != coro.EmitOutcomePlain || got.AtomicCostProof != coro.AtomicCostLeaf {
				t.Fatalf("optimized Leaf plan = %+v, present=%t; want outcome leaf", got, found)
			}

			runCoroABITestPipeline(t, baselineProg, baselineModule)
			runCoroABITestPipeline(t, optimizedProg, optimizedModule)
			llssa.RemoveKeepAliveCallsAfterCoroSplit(baselineModule)
			llssa.RemoveKeepAliveCallsAfterCoroSplit(optimizedModule)
			for _, name := range []string{"foo.Middle", "foo.Leaf"} {
				for _, suffix := range []string{".resume", ".destroy"} {
					if baselineModule.NamedFunction(name + "$coro" + suffix).IsNil() {
						t.Fatalf("baseline is missing %s%s", name+"$coro", suffix)
					}
					if !optimizedModule.NamedFunction(name + "$outcome" + suffix).IsNil() {
						t.Fatalf("optimized module retained %s%s", name+"$outcome", suffix)
					}
				}
			}
			baselineIR, optimizedIR := baselineModule.String(), optimizedModule.String()
			optimizeCoroOutcomeCostModule(t, baselineProg, baselineModule)
			optimizeCoroOutcomeCostModule(t, optimizedProg, optimizedModule)
			baselineObject, err := baselineProg.TargetMachine().EmitToMemoryBuffer(baselineModule, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit DAG baseline object: %v", err)
			}
			defer baselineObject.Dispose()
			optimizedObject, err := optimizedProg.TargetMachine().EmitToMemoryBuffer(optimizedModule, llvm.ObjectFile)
			if err != nil {
				t.Fatalf("emit DAG outcome object: %v", err)
			}
			defer optimizedObject.Dispose()
			baselineBytes, optimizedBytes := len(baselineObject.Bytes()), len(optimizedObject.Bytes())
			if len(optimizedIR) >= len(baselineIR) || optimizedBytes >= baselineBytes {
				t.Fatalf(
					"outcome DAG did not reduce the exact fixture: IR baseline=%d optimized=%d object baseline=%d optimized=%d",
					len(baselineIR), len(optimizedIR), baselineBytes, optimizedBytes,
				)
			}
			t.Logf(
				"post-split DAG fixture: IR baseline=%d optimized=%d; O2 object baseline=%d optimized=%d; eliminated frames/resume/destroy=2/2/2",
				len(baselineIR), len(optimizedIR), baselineBytes, optimizedBytes,
			)
		})
	}
}

func optimizeCoroOutcomeCostModule(t *testing.T, prog llssa.Program, module llvm.Module) {
	t.Helper()
	options := llvm.NewPassBuilderOptions()
	defer options.Dispose()
	options.SetVerifyEach(true)
	if err := module.RunPasses("default<O2>", prog.TargetMachine(), options); err != nil {
		t.Fatalf("optimize outcome cost-comparison module: %v\n%s", err, module.String())
	}
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify optimized outcome cost-comparison module: %v\n%s", err, module.String())
	}
}

func TestCoroImportedOutcomePlainUsesPublishedPhysicalABI(t *testing.T) {
	const source = `package foo
func Imported(value uint32, payload any, fail bool) uint32
func Caller(value uint32, payload any, fail bool) uint32 {
	return Imported(value, payload, fail)
}
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	universe, err := prepareStacklessEmissionUniverse(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files, Identity: "example.com/foo"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	planMetadata := coro.PlanDigestMetadata{
		CoroABI:        coro.PhysicalABIV1,
		SchedulerABI:   coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0,
		PanicABI:       coro.PanicExplicitStatusABIV0,
		FuncRepABI:     coro.FuncRepABIV1,
		TargetTriple:   prog.TargetSpec().Triple,
		TargetCPU:      prog.TargetSpec().CPU,
		TargetFeatures: prog.TargetSpec().Features,
		TargetABI:      prog.TargetSpec().TargetABI,
		PointerBits:    prog.PointerSize() * 8,
		DataLayout:     prog.DataLayout(),
	}
	switch prog.TargetData().ByteOrder() {
	case llvm.LittleEndian:
		planMetadata.Endianness = "little"
	case llvm.BigEndian:
		planMetadata.Endianness = "big"
	default:
		t.Fatal("unknown target byte order")
	}
	libraryMetadata := coro.LibraryEffectMetadata{
		FunctionIDSchema: coro.FunctionIDSchema,
		CoroABI:          planMetadata.CoroABI,
		SchedulerABI:     planMetadata.SchedulerABI,
		PanicABI:         planMetadata.PanicABI,
		FuncRepABI:       planMetadata.FuncRepABI,
		TargetTriple:     planMetadata.TargetTriple,
		TargetCPU:        planMetadata.TargetCPU,
		TargetFeatures:   planMetadata.TargetFeatures,
		TargetABI:        planMetadata.TargetABI,
		PointerBits:      planMetadata.PointerBits,
		Endianness:       planMetadata.Endianness,
		DataLayout:       planMetadata.DataLayout,
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = libraryMetadata.CoroABI
	functionIDs.SchedulerABI = libraryMetadata.SchedulerABI
	functionIDs.ArchiveReady = true
	imported, caller := ssaPkg.Func("Imported"), ssaPkg.Func("Caller")
	importedID, err := coro.StableFunctionID(imported, functionIDs)
	if err != nil {
		t.Fatal(err)
	}
	abiHash, err := universe.CoroLibraryEffects().FunctionABIHash(imported, libraryMetadata)
	if err != nil {
		t.Fatal(err)
	}
	baseSymbol, err := universe.CoroLibraryEffects().FunctionBaseSymbol(imported)
	if err != nil {
		t.Fatal(err)
	}
	fact := coro.LibraryEffectFunction{
		ID:              importedID,
		ABIHash:         abiHash,
		Effect:          coro.OutcomeStructured,
		Exec:            coro.MayUnwind,
		FuncRep:         coro.DirectCoro,
		Primary:         coro.PrimaryCoroutine,
		ManagedEntry:    coro.ManagedEntryOutcomePlain,
		AtomicCost:      3,
		AtomicCostProof: coro.AtomicCostLeaf,
		PrimarySymbol:   baseSymbol + coroOutcomePlainPrimarySuffix,
	}
	plan, err := coro.AnalyzeSSA(
		ssaPkg.Prog,
		coro.Roots{{Function: caller, Demand: coro.AsyncDemand}},
		coro.SSAConfig{
			EmissionUniverse:     ssaUniverse,
			FunctionIDs:          functionIDs,
			MaxPlainInstructions: -1,
			OutcomeMode:          coro.OutcomeExplicitStatus,
			ClassifyFunction: func(function *ssa.Function) (coro.SSAFunctionPolicy, error) {
				if function == imported {
					return fact.ImportedPolicy()
				}
				return coro.SSAFunctionPolicy{}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	importedPlan, found := plan.FunctionPlan(imported)
	if !found || importedPlan.Emission != coro.EmitExternal ||
		importedPlan.ManagedEntry != coro.ManagedEntryOutcomePlain ||
		importedPlan.AtomicCost != fact.AtomicCost || importedPlan.AtomicCostProof != fact.AtomicCostProof {
		t.Fatalf("imported outcome plan = %+v, present=%t", importedPlan, found)
	}
	compilation := &Compilation{
		CoroPlan:                  plan,
		CoroPlanMetadata:          planMetadata,
		CoroLibraryEffectMetadata: libraryMetadata,
		CoroLibraryEffects:        map[*ssa.Function]coro.LibraryEffectFunction{imported: fact},
		EmissionUniverse:          universe,
	}
	enableCoroChildAwaitCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	pkg, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatal(err)
	}
	module := pkg.Module()
	defer module.Dispose()
	if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
		t.Fatalf("verify imported outcome-plain consumer: %v\n%s", err, module.String())
	}
	declaration := module.NamedFunction(fact.PrimarySymbol)
	if declaration.IsNil() || !declaration.IsDeclaration() ||
		!regexp.MustCompile(`declare void @`).MatchString(declaration.String()) {
		t.Fatalf("imported outcome-plain declaration has the wrong physical ABI:\n%s", module.String())
	}
	callerIR := requireCoroPhysicalFunction(t, module, "foo.Caller").String()
	if !strings.Contains(callerIR, fact.PrimarySymbol) || strings.Contains(callerIR, baseSymbol+coroPrimarySuffix) {
		t.Fatalf("imported outcome call did not use the published entry:\n%s", callerIR)
	}
}

func compileCoroOutcomePlainFixture(t *testing.T, target *llssa.Target) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Function,
) {
	return compileCoroOutcomePlainFixtureWithBudget(t, target, 64)
}

func compileCoroOutcomePlainFixtureWithBudget(t *testing.T, target *llssa.Target, maxPlainInstructions int) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Function,
) {
	t.Helper()
	prog, pkg, plan, ssaPkg := compileCoroOutcomePlainSource(
		t, target, coroOutcomePlainFixture, "Parent", maxPlainInstructions,
	)
	parent, leaf := ssaPkg.Func("Parent"), ssaPkg.Func("Leaf")
	return prog, pkg, plan, parent, leaf
}

func compileCoroOutcomePlainDAGFixture(t *testing.T, target *llssa.Target, maxPlainInstructions int) (
	llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Function, *ssa.Function, *ssa.Function,
) {
	t.Helper()
	prog, pkg, plan, ssaPkg := compileCoroOutcomePlainSource(
		t, target, coroOutcomePlainDAGFixture, "Parent", maxPlainInstructions,
	)
	parent, middle, leaf := ssaPkg.Func("Parent"), ssaPkg.Func("Middle"), ssaPkg.Func("Leaf")
	return prog, pkg, plan, parent, middle, leaf
}

func compileCoroOutcomePlainSource(
	t *testing.T,
	target *llssa.Target,
	source, rootName string,
	maxPlainInstructions int,
) (llssa.Program, llssa.Package, *coro.SSAPlan, *ssa.Package) {
	t.Helper()
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	universe, err := prepareStacklessEmissionUniverse(
		prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}},
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
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	functionIDs.ArchiveReady = true
	root := ssaPkg.Func(rootName)
	if root == nil {
		prog.Dispose()
		t.Fatalf("outcome-plain fixture root %q is absent", rootName)
	}
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: maxPlainInstructions,
		OutcomeMode:          coro.OutcomeExplicitStatus,
		ClassifyLocalBody:    universe.CoroLocalBodyFacts,
		ClassifyLoweredCalls: universe.CoroLoweredCalls,
	})
	if err != nil {
		prog.Dispose()
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroChildAwaitCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
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
