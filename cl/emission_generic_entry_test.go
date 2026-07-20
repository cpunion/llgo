//go:build !llgo

package cl

import (
	"go/ast"
	"strings"
	"testing"

	"github.com/goplus/llgo/internal/coro"
	"github.com/goplus/llgo/internal/goembed"
	llssa "github.com/goplus/llgo/ssa"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

const genericIntrinsicEntryPath = "example.com/emission/genericintrinsic"

const genericIntrinsicEntrySource = `package genericintrinsic
type integer interface { ~int }
//llgo:link Index llgo.index
func Index[T any, I integer](ptr *T, offset I) T { return *ptr }
func Root(ptr *uint32, offset int) uint32 {
	for offset < 0 { offset++ }
	return Index(ptr, offset)
}
`

func TestEmissionGenericIntrinsicInstanceEntryNativeAndWasm(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, test := range []struct {
		name   string
		target *llssa.Target
	}{
		{name: "native"},
		{name: "wasm32", target: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			testGenericIntrinsicInstanceEntry(t, test.target)
		})
	}
}

func testGenericIntrinsicInstanceEntry(t *testing.T, target *llssa.Target) {
	t.Helper()
	testProg := newEmissionTestProgram()
	pkg := testProg.addPackage(t, genericIntrinsicEntryPath, genericIntrinsicEntrySource)
	testProg.ssa.Build()

	origin := pkg.ssa.Func("Index")
	var instance *ssa.Function
	for fn := range ssautil.AllFunctions(testProg.ssa) {
		if fn != nil && fn.Origin() == origin {
			instance = fn
			break
		}
	}
	if instance == nil {
		t.Fatal("Index instance was not materialized")
	}
	if len(origin.Blocks) == 0 || len(instance.Blocks) == 0 {
		t.Fatalf("generic intrinsic lost its exact SSA source body: origin=%d instance=%d", len(origin.Blocks), len(instance.Blocks))
	}

	var prog llssa.Program
	if target == nil {
		prog = newLLSSAProg(t)
	} else {
		prog = newLLSSAProgForTarget(t, target)
	}
	defer prog.Dispose()
	universe, err := PrepareEmissionUniverse(prog, nil, []EmissionPackage{{SSA: pkg.ssa, Files: []*ast.File{pkg.file}}})
	if err != nil {
		t.Fatal(err)
	}
	canonical, ok := universe.Resolve(instance)
	if !ok {
		t.Fatal("Index instance is absent from the emission universe")
	}
	if canonical != instance {
		t.Fatalf("generic intrinsic canonical = %p, want exact instance %p", canonical, instance)
	}
	_, classified, err := universe.FunctionBackground(canonical)
	if err != nil {
		t.Fatal(err)
	}
	opcode, intrinsic, err := universe.coroIntrinsicOpcode(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if classified || !intrinsic || opcode != llgoIndex {
		t.Fatalf("generic instance frontend ownership = classified=%t intrinsic=%t opcode=%d; want exact llgo.index intrinsic", classified, intrinsic, opcode)
	}
	call := firstStaticCallTo(t, pkg.ssa.Func("Root"), canonical)
	semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
	if err != nil || !intrinsic || semantics != CoroIntrinsicCallInlineNoSuspend {
		t.Fatalf("generic Index call semantics = %v, %t, %v; want inline-no-suspend, true, nil", semantics, intrinsic, err)
	}

	ssaUniverse, err := coro.NewSSAEmissionUniverse(testProg.ssa, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	functionIDs := universe.FunctionIDConfig()
	functionIDs.CoroABI = coro.PhysicalABIV1
	functionIDs.SchedulerABI = coro.SchedulerProgramBootstrapABIV2
	functionIDs.ArchiveReady = true
	root := pkg.ssa.Func("Root")
	plan, err := coro.AnalyzeSSA(testProg.ssa, coro.Roots{{Function: root, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse:     ssaUniverse,
		FunctionIDs:          functionIDs,
		MaxPlainInstructions: 1,
		ClassifyElidedCall: func(_ *ssa.Function, call ssa.CallInstruction) (bool, error) {
			semantics, intrinsic, err := universe.CoroIntrinsicCallSiteSemantics(call)
			return intrinsic && semantics.ElidesManagedCall(), err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	instancePlan, ok := plan.FunctionPlan(canonical)
	if !ok {
		t.Fatal("Index instance has no plan")
	}
	if instancePlan.Demand != coro.NoDemand || instancePlan.Emission != coro.EmitNone || !plan.ElidesCall(call) {
		t.Fatalf("Index instance plan = %+v, elided=%t; want one non-emitted exact intrinsic instance", instancePlan, plan.ElidesCall(call))
	}
	rootPlan, ok := plan.FunctionPlan(root)
	if !ok || rootPlan.Emission != coro.EmitCoroutine || !rootPlan.Exec.Contains(coro.NeedsPreempt) {
		t.Fatalf("Root plan = %+v, present=%t; want preemptible coroutine", rootPlan, ok)
	}

	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compiled, _, err := NewPackageExWithEmbedOptions(
		prog, nil, nil, nil, pkg.ssa, []*ast.File{pkg.file}, goembed.VarMap{},
		PackageOptions{Compilation: compilation},
	)
	if err != nil {
		t.Fatalf("compile exact generic intrinsic instance: %v", err)
	}
	module := compiled.Module()
	defer module.Dispose()
	rootIR := requireCoroPhysicalFunction(t, module, genericIntrinsicEntryPath+".Root").String()
	if !strings.Contains(rootIR, "getelementptr") || !strings.Contains(rootIR, "load i32") {
		t.Fatalf("generic llgo.index did not lower inline to typed address/load:\n%s", rootIR)
	}
	if strings.Contains(module.String(), genericIntrinsicEntryPath+".Index") {
		t.Fatalf("non-emitted generic intrinsic acquired a callable LLVM body:\n%s", module.String())
	}
	runCoroABITestPipeline(t, prog, module)
	if module.NamedFunction(genericIntrinsicEntryPath + ".Root$coro.resume").IsNil() {
		t.Fatalf("CoroSplit did not produce the generic-intrinsic Root resume entry:\n%s", module.String())
	}
}

func firstStaticCallTo(t *testing.T, owner, target *ssa.Function) ssa.CallInstruction {
	t.Helper()
	for _, block := range owner.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(ssa.CallInstruction); ok && call.Common().StaticCallee() == target {
				return call
			}
		}
	}
	t.Fatalf("%q has no static call to %q", owner.Name(), target.Name())
	return nil
}
