//go:build !llgo

package cl

import (
	"go/ast"
	"testing"

	"github.com/xgo-dev/llgo/internal/coro"
	"github.com/xgo-dev/llgo/internal/goembed"
	llssa "github.com/xgo-dev/llgo/ssa"
	"github.com/xgo-dev/llvm"
)

func TestCoroCrossPackageAnonymousStructMethodABI(t *testing.T) {
	llssa.Initialize(llssa.InitAll)
	for _, target := range []struct {
		name string
		arch *llssa.Target
	}{{name: "native"}, {name: "wasm32", arch: &llssa.Target{GOOS: "wasip1", GOARCH: "wasm"}}} {
		t.Run(target.name, func(t *testing.T) {
			program := newEmissionTestProgram()
			api := program.addPackage(t, "example.com/api", `package api
type Value interface { Value(struct { R0 uint32 }) uint32 }
`)
			foo := program.addPackage(t, "foo", `package foo
import "example.com/api"
var gate chan uint32
type concrete uint32
func (value concrete) Value(input struct { R0 uint32 }) uint32 { <-gate; return uint32(value) + input.R0 }
func Keep() api.Value { return concrete(1) }
func Root(value api.Value) uint32 { return value.Value(struct { R0 uint32 }{9}) }
`)
			program.ssa.Build()
			prog := newLLSSAProgForTarget(t, target.arch)
			defer prog.Dispose()
			universe, err := prepareStacklessEmissionUniverse(prog, nil, []EmissionPackage{
				{SSA: api.ssa, Files: []*ast.File{api.file}},
				{SSA: foo.ssa, Files: []*ast.File{foo.file}},
			})
			if err != nil {
				t.Fatal(err)
			}
			ssaUniverse, err := coro.NewSSAEmissionUniverse(foo.ssa.Prog, universe.Functions())
			if err != nil {
				t.Fatal(err)
			}
			ids := universe.FunctionIDConfig()
			ids.CoroABI, ids.SchedulerABI, ids.ArchiveReady = coro.PhysicalABIV1, coro.SchedulerProgramBootstrapChannelClosedStaticSpawnABIV0, true
			plan, err := coro.AnalyzeSSA(foo.ssa.Prog, coro.Roots{{Function: foo.ssa.Func("Root"), Demand: coro.AsyncDemand}}, coro.SSAConfig{
				EmissionUniverse: ssaUniverse, FunctionIDs: ids, DynamicResolution: coro.DynamicCHAClosed,
				MaxPlainInstructions: -1, OutcomeMode: coro.OutcomeExplicitStatus,
				ClassifyDemandReferences:       universe.CoroDemandReferences,
				ClassifySyncDemandReferences:   universe.CoroSyncDemandReferences,
				ClassifyManagedValueReferences: universe.CoroPlanningMetadata().ManagedValueReferences,
			})
			if err != nil {
				t.Fatal(err)
			}
			compiled, _, err := NewPackageExWithEmbedOptions(prog, nil, nil, nil, foo.ssa, []*ast.File{foo.file}, goembed.VarMap{},
				PackageOptions{Compilation: coroClosedInterfacePlainCompilation(plan, universe)})
			if err != nil {
				t.Fatal(err)
			}
			module := compiled.Module()
			defer module.Dispose()
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
			assertCoroManagedClosedInterfaceIR(t, requireCoroPhysicalFunction(t, module, "foo.Root").String())
			runCoroABITestPipeline(t, prog, module)
			if err := llvm.VerifyModule(module, llvm.ReturnStatusAction); err != nil {
				t.Fatal(err)
			}
		})
	}
}
