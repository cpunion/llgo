//go:build !llgo

package cl

import (
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
	"golang.org/x/tools/go/ssa"
)

func TestCoroNamedFunctionResultRemainsDescriptor(t *testing.T) {
	for _, rawFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "raw-first"}[rawFirst], func(t *testing.T) {
			testCoroNamedFunctionResultRemainsDescriptor(t, rawFirst)
		})
	}
}

func testCoroNamedFunctionResultRemainsDescriptor(t *testing.T, rawFirst bool) {
	const source = `package foo
type Handler func(int) int
func choose(callback Handler) Handler { return callback }
func Apply(callback Handler) int { return choose(callback)(42) }
`
	ssaPkg, _, files := buildGoSSAPkg(t, source)
	prog := newLLSSAProg(t)
	defer prog.Dispose()
	if rawFirst {
		prog.Type(ssaPkg.Pkg.Scope().Lookup("Handler").Type(), llssa.InC)
	}
	universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{{SSA: ssaPkg, Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	ssaUniverse, err := coro.NewSSAEmissionUniverse(ssaPkg.Prog, universe.Functions())
	if err != nil {
		t.Fatal(err)
	}
	apply := ssaPkg.Func("Apply")
	var dynamicCall *ssa.Call
	for _, block := range apply.Blocks {
		for _, instruction := range block.Instrs {
			if call, ok := instruction.(*ssa.Call); ok && call.Common().StaticCallee() == nil {
				dynamicCall = call
			}
		}
	}
	if dynamicCall == nil {
		t.Fatal("missing returned callback invocation")
	}
	ids := universe.FunctionIDConfig()
	ids.CoroABI = coro.PhysicalABIV1
	ids.SchedulerABI = coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0
	ids.ArchiveReady = true
	plan, err := coro.AnalyzeSSA(ssaPkg.Prog, coro.Roots{{Function: apply, Demand: coro.AsyncDemand}}, coro.SSAConfig{
		EmissionUniverse: ssaUniverse, FunctionIDs: ids, MaxPlainInstructions: -1,
		ClassifyUnknownCall: func(_ *ssa.Function, call ssa.CallInstruction) (coro.UnknownTarget, error) {
			if call == dynamicCall {
				return coro.UnknownManagedDispatch, nil
			}
			return coro.UnknownManaged, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compilation := &Compilation{CoroPlan: plan, EmissionUniverse: universe}
	enableCoroPreemptCompilation(compilation)
	compilation.PanicABI = coro.PanicExplicitStatusABIV0
	compilation.FuncRepABI = coro.FuncRepABIV2
	compiled, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, ssaPkg, files, goembed.VarMap{}, PackageOptions{Compilation: compilation})
	if err != nil {
		t.Fatal(err)
	}
	if err := llvm.VerifyModule(compiled.Module(), llvm.ReturnStatusAction); err != nil {
		t.Fatal(err)
	}
	runCoroABITestPipeline(t, prog, compiled.Module())
}
